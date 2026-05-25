//go:build unix

package peercred

import (
	"errors"
	"fmt"
	"log/slog"
	"net"

	tspeer "github.com/tailscale/peercred"
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

// processCWDFn is the function used to look up a process's CWD by PID.
// Indirected through a package-level variable so tests can inject forced
// failures — see SetProcessCWDFnForTest in export_unix_test.go.
var processCWDFn = processCWD

// Resolve extracts the connecting process PID via kernel peer credentials,
// resolves its CWD, walks upward to find the git root, and matches against
// registered agent worktrees.
//
// Error taxonomy (see thrum-ndtw): the caller (server.go) distinguishes
// "provably anonymous" (ErrAnonymous → anonymous allowlist enforced) from
// "unknown state" (any other error → fall through to legacy client-asserted
// identity, pre-v0.9.0 behavior). Only steps 3 and 5 — empty git root and
// matchWorktree no-match — produce ErrAnonymous, because only those states
// are provable evidence that the caller is outside every registered
// worktree. Introspection failures at steps 1 and 2 cannot prove that and
// therefore return raw errors so server.go takes the legacy path.
func (r *unixResolver) Resolve(conn net.Conn) (*ResolvedIdentity, error) {
	// Step 1: Extract PID via kernel peer credentials.
	// UNKNOWN state on failure (see Error taxonomy above) — return raw errors.
	creds, err := tspeer.Get(conn)
	if err != nil {
		slog.Warn("peercred.resolve step=pid failed", "err", err.Error())
		return nil, fmt.Errorf("peercred: get peer credentials: %w", err)
	}
	pid, ok := creds.PID()
	if !ok || pid == 0 {
		slog.Warn("peercred.resolve step=pid failed", "err", "no PID in peer credentials")
		return nil, errors.New("peercred: no PID in peer credentials")
	}
	slog.Debug("peercred.resolve step=pid", "pid", pid)

	// Step 2: Resolve PID → CWD via platform-specific implementation.
	//
	// Historical note (thrum-2t7d et al.): from sec.2 (pre-v0.9.0) through
	// v0.10.3-rc.4, this call delegated to gopsutil.Process.Cwd() — which is
	// documented upstream as "not implemented yet" on Darwin and returned an
	// error on EVERY macOS invocation. That error wasn't ErrAnonymous, so
	// server.go's `if resolveErr == nil || errors.Is(resolveErr, ErrAnonymous)`
	// check fell through to the legacy client-asserted-identity path, which
	// trusts whatever agent_id the CLI sends in its RPC payload. The CLI in
	// turn builds that claim from THRUM_AGENT_ID env vars (when set) or a
	// cwd-based identity file lookup. Result: on macOS, peer-credential cwd
	// resolution was effectively a no-op for the entire history of this code,
	// and stale THRUM_* env vars inherited from parent shells silently
	// overrode cwd-based identity on every call. The footgun surfaced
	// repeatedly as "agent is misidentified" symptoms that were diagnosed as
	// other things (binding cache staleness, env-leak in tmux setup, etc.).
	// rc.5 replaces the gopsutil delegation on Darwin with an `lsof -p PID
	// -Fn -d cwd` subprocess (resolver_cwd_darwin.go) — slow path (~30ms per
	// call) but reliable; lsof is a system tool always present on macOS.
	// v0.10.4 candidate: convert to native libproc proc_pidinfo via pure-Go
	// syscall.Syscall6 or matrix-built cgo darwin runners.
	//
	// UNKNOWN state on failure — implementations can still miss for any of:
	// process exited in the race window, permission model drift on macOS, etc.
	// We cannot prove the caller is anonymous, so return a raw error (NOT
	// wrapped with ErrAnonymous) to route through server.go's legacy
	// fallthrough. The fallthrough is still useful for transient races even
	// though it's no longer the everyday path on macOS.
	cwd, err := processCWDFn(pid)
	if err != nil {
		slog.Warn("peercred.resolve step=cwd failed", "pid", pid, "err", err.Error())
		return nil, fmt.Errorf("peercred: cannot read CWD for PID %d: %w", pid, err)
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
		// thrum-wk7d (part 2): no-match logs at DEBUG instead of WARN.
		// The caller (server.go handleConnection) decides whether to
		// promote this to WARN based on whether the request method is
		// in anonymousAllowedMethods. Every anonymous-allowed call
		// (agent.register, session.start, session.setIntent, all the
		// read-only RPCs) hits this path BY DESIGN during the
		// bootstrap window before a binding exists; emitting WARN here
		// floods the logs on routine operation. Real rejections (an
		// anonymous caller hitting a non-allowlisted method) get a
		// caller-side WARN in server.go where the method is known.
		slog.Debug("peercred.resolve step=match: no registered worktree matched", "candidate_git_root", gitRoot, "registered_count", len(agents))
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

// processCWD is the platform-conditional CWD lookup. Implementations live in
// resolver_cwd_darwin.go (libproc syscall via cgo — gopsutil's Cwd() is not
// implemented for Darwin) and resolver_cwd_other.go (gopsutil on Linux and
// other unix). Both signatures are identical so processCWDFn can take either.

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
