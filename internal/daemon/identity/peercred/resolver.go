// Package peercred resolves unix-socket connecting processes to registered
// agent identities via kernel peer credentials and CWD resolution.
//
// Flow:
//  1. Extract the connecting process PID via SO_PEERCRED (Linux) or
//     LOCAL_PEERPID (macOS) using tailscale/peercred.
//  2. Resolve PID → process CWD via gopsutil (cross-platform).
//  3. Walk CWD upward to find the nearest .git directory or .git FILE
//     (git worktree indicator).
//  4. Canonicalize paths via filepath.EvalSymlinks before comparison.
//  5. Match against registered agent worktree paths.
//  6. Return ResolvedIdentity if matched; ErrAnonymous if not.
package peercred

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
)

// ErrAnonymous is returned when a connecting process cannot be matched
// to any registered agent worktree. It is NOT a hard error — callers
// should treat it as "unauthenticated, handle per policy.".
var ErrAnonymous = errors.New("peercred: no registered agent matched the connecting process")

// ResolvedIdentity holds the kernel-verified identity of a connecting process.
type ResolvedIdentity struct {
	AgentID  string
	Worktree string
	PID      int
}

// AgentWorktree is a registered agent entry provided by the caller via the
// AgentLister interface. The Worktree field must be the absolute path to the
// agent's worktree directory.
type AgentWorktree struct {
	AgentID  string
	Worktree string
}

// AgentLister lists the registered agent worktree paths.
// Implementations are injected via NewResolver to keep this package
// free of direct storage imports and thus unit-testable with a mock.
type AgentLister interface {
	ListAgentWorktrees() ([]AgentWorktree, error)
}

// Resolver resolves a net.Conn to a ResolvedIdentity.
type Resolver interface {
	Resolve(conn net.Conn) (*ResolvedIdentity, error)
}

// findGitRoot walks dir upward until it finds a directory that contains a
// ".git" entry (either a directory — normal repo — or a plain file — git
// worktree). Returns the directory containing ".git", or "" if not found.
//
// It handles the git worktree case where .git is a plain FILE containing a
// gitdir: pointer, not a subdirectory. Both shapes are accepted because all
// we need is confirmation that the directory is "under" a git checkout.
func findGitRoot(dir string) string {
	prev := ""
	for dir != "" && dir != prev {
		gitEntry := filepath.Join(dir, ".git")
		fi, err := os.Lstat(gitEntry)
		if err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
			return dir
		}
		prev = dir
		dir = filepath.Dir(dir)
	}
	return ""
}

// matchWorktree canonicalizes both candidate and each registered worktree
// via filepath.EvalSymlinks before comparison.  This handles the macOS
// /var → /private/var rename and similar symlink mismatches.
//
// Candidate is the git root derived from the connecting process's CWD.
// Agents is the list provided by AgentLister.
//
// Returns the first matching AgentWorktree or an error.
func matchWorktree(candidate string, agents []AgentWorktree) (*AgentWorktree, error) {
	canon, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		// If the candidate path can't be resolved, fall through to no-match.
		// Treat this as ErrAnonymous rather than a hard error.
		slog.Warn("peercred.matchWorktree candidate EvalSymlinks failed", "path", candidate, "err", err)
		canon = candidate
	}

	for _, a := range agents {
		wt := a.Worktree
		canonWt, err := filepath.EvalSymlinks(wt)
		if err != nil {
			// thrum-g1ux: downgrade to Debug for the
			// torn-down-worktree case (os.IsNotExist). Teardown
			// removes the worktree from disk but doesn't end the
			// agent's sessions, so the session_ref row persists and
			// surfaces here on every resolution. Other failure modes
			// (permission errors etc) keep WARN — those are real
			// diagnostics worth surfacing. Behavior is unchanged:
			// canonWt = wt fallback below means a deleted path can't
			// match the (resolvable) candidate, so the iteration
			// proceeds without a false match. Option B daemon-side
			// stale-row cleanup is deferred to v0.10.7 / v0.11.
			if os.IsNotExist(err) {
				slog.Debug("peercred.matchWorktree stored path missing on disk (torn-down worktree)", "path", wt, "err", err)
			} else {
				slog.Warn("peercred.matchWorktree stored EvalSymlinks failed", "path", wt, "err", err)
			}
			canonWt = wt
		}
		if canon == canonWt {
			slog.Debug("peercred.matchWorktree matched", "candidate", canon, "stored", canonWt, "agent_id", a.AgentID)
			result := a
			return &result, nil
		}
	}
	return nil, fmt.Errorf("%w: git root %q matches no registered worktree", ErrAnonymous, candidate)
}
