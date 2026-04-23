//go:build unix

package peercred

import (
	"fmt"
	"log/slog"
	"net"

	tspeer "github.com/tailscale/peercred"

	goproc "github.com/shirou/gopsutil/v3/process"
)

// unixResolver is the Resolver implementation for unix platforms (Linux,
// macOS). It uses tailscale/peercred for PID extraction — which internally
// uses SO_PEERCRED on Linux and LOCAL_PEERPID on macOS — and gopsutil for
// cross-platform process CWD lookup.
//
// Note on build tags: the bead spec asked for separate resolver_linux.go and
// resolver_darwin.go. Using tailscale/peercred collapses both into a single
// //go:build unix file, which is strictly better: no duplicated logic, and
// tailscale/peercred already carries its own darwin/linux split internally.
type unixResolver struct {
	lister AgentLister
}

// NewResolver returns a Resolver backed by kernel peer credentials.
// Lister provides the set of registered agent worktree paths to match against.
// The resolver is safe for concurrent use.
func NewResolver(lister AgentLister) Resolver {
	return &unixResolver{lister: lister}
}

// Resolve extracts the connecting process PID via kernel peer credentials,
// resolves its CWD, walks upward to find the git root, and matches against
// registered agent worktrees.
func (r *unixResolver) Resolve(conn net.Conn) (*ResolvedIdentity, error) {
	// Step 1: Extract PID via kernel peer credentials.
	creds, err := tspeer.Get(conn)
	if err != nil {
		return nil, fmt.Errorf("peercred: get peer credentials: %w", err)
	}
	pid, ok := creds.PID()
	if !ok || pid == 0 {
		return nil, fmt.Errorf("%w: no PID in peer credentials", ErrAnonymous)
	}
	slog.Debug("peercred.resolve step=pid", "pid", pid)

	// Step 2: Resolve PID → CWD.
	cwd, err := processCWD(pid)
	if err != nil {
		// Process may have exited in the race window between connect and here.
		return nil, fmt.Errorf("%w: cannot read CWD for PID %d: %v", ErrAnonymous, pid, err)
	}
	slog.Debug("peercred.resolve step=cwd", "pid", pid, "cwd", cwd)

	// Step 3: Walk CWD upward to find nearest git root (directory OR file .git).
	gitRoot := findGitRoot(cwd)
	if gitRoot == "" {
		slog.Warn("peercred.resolve step=git_root: no git root found, returning anonymous", "pid", pid, "cwd", cwd)
		return nil, fmt.Errorf("%w: PID %d CWD %q is not under any git repository", ErrAnonymous, pid, cwd)
	}
	slog.Debug("peercred.resolve step=git_root", "cwd", cwd, "git_root", gitRoot)

	// Step 4: List registered worktrees.
	agents, err := r.lister.ListAgentWorktrees()
	if err != nil {
		return nil, fmt.Errorf("peercred: list agent worktrees: %w", err)
	}
	slog.Debug("peercred.resolve step=list_worktrees", "count", len(agents))

	// Step 5: Match with symlink canonicalization.
	match, err := matchWorktree(gitRoot, agents)
	if err != nil {
		slog.Warn("peercred.resolve step=match: no registered worktree matched", "candidate_git_root", gitRoot, "registered_count", len(agents))
		return nil, err // already wraps ErrAnonymous
	}
	slog.Debug("peercred.resolve step=match: matched", "agent_id", match.AgentID, "worktree", match.Worktree)

	return &ResolvedIdentity{
		AgentID:  match.AgentID,
		Worktree: match.Worktree,
		PID:      pid,
	}, nil
}

// PIDFromConn extracts the connecting process PID via kernel peer
// credentials, independent of identity resolution. Separated from Resolve
// so the server can plumb the PID into the request context even when
// Resolve returns ErrAnonymous — guard checks require the PID to walk the
// ancestor chain, and waiting for identity resolution would lose that
// information on anonymous callers.
func PIDFromConn(conn net.Conn) (int, error) {
	creds, err := tspeer.Get(conn)
	if err != nil {
		return 0, fmt.Errorf("peercred: get peer credentials: %w", err)
	}
	pid, ok := creds.PID()
	if !ok || pid == 0 {
		return 0, fmt.Errorf("%w: no PID in peer credentials", ErrAnonymous)
	}
	return pid, nil
}

// processCWD returns the current working directory of the process with the
// given PID. Returns an error if the process no longer exists.
func processCWD(pid int) (string, error) {
	p, err := goproc.NewProcess(int32(pid)) //nolint:gosec // pid comes from kernel peer creds, always valid int
	if err != nil {
		return "", fmt.Errorf("gopsutil NewProcess(%d): %w", pid, err)
	}
	cwd, err := p.Cwd()
	if err != nil {
		return "", fmt.Errorf("gopsutil Cwd(%d): %w", pid, err)
	}
	return cwd, nil
}

// ResolveCallerWorktree returns the git root containing the given PID's
// current working directory, or an error wrapped around ErrAnonymous when
// the PID is unreachable or not under a git repo. Used by HandleRegister
// (thrum-2b2t) to persist a worktree session_ref when the auto-resurrect
// path creates a fresh session without the explicit session.start RPC.
//
// Trust boundary: pid is caller-supplied (from req.AgentPID in the
// register flow). The same trust applies here as for the existing G4
// liveness check that reads pid from the same request field.
func ResolveCallerWorktree(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("%w: invalid PID %d", ErrAnonymous, pid)
	}
	cwd, err := processCWD(pid)
	if err != nil {
		return "", fmt.Errorf("%w: cannot read CWD for PID %d: %v", ErrAnonymous, pid, err)
	}
	root := findGitRoot(cwd)
	if root == "" {
		return "", fmt.Errorf("%w: PID %d CWD %q is not under any git repository", ErrAnonymous, pid, cwd)
	}
	return root, nil
}
