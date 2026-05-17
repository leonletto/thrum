package sessionarchive

import (
	"path/filepath"

	agentpkg "github.com/leonletto/thrum/internal/agent"
)

// SessionsDir returns the absolute path to the agent's sessions/ folder
// per spec §5.1 / §5.2.
//
// Routing:
//   - ModePersistent → <mainRepoThrumDir>/agents/<agentID>/sessions
//   - ModeEphemeral  → <worktreeThrumDir>/agents/<agentID>/sessions
//
// Both root arguments are .thrum/ directories (not repo roots) — caller
// passes the daemon's `h.thrumDir` (main-repo .thrum/) and the per-RPC
// `wtThrumDir` (worktree .thrum/) that already flow as a pair through
// daemon RPC handlers (see internal/daemon/rpc/tmux.go for the
// existing convention).
//
// Q-Spec-5 was resolved 2026-05-17 in favor of the free-function form
// over an Agent-method form: the substrate is data-only at v0.11 and
// the daemon already has both roots available at every call site. If
// a third consumer materializes, this helper can be promoted to a
// method on Agent in v0.12+.
func SessionsDir(agent agentpkg.Agent, mainRepoThrumDir, worktreeThrumDir string) string {
	var root string
	switch agent.Mode {
	case agentpkg.ModePersistent:
		root = mainRepoThrumDir
	case agentpkg.ModeEphemeral:
		root = worktreeThrumDir
	default:
		// Defensive fallback. The agent.register validator (B-B1
		// E6.0 Task 5) rejects unknown / empty Mode at registration
		// time, so reaching this branch indicates a code bug
		// upstream — not a user-facing surface — but returning a
		// usable path keeps downstream archive logic free of nil-
		// handling for an in-practice unreachable case.
		root = mainRepoThrumDir
	}
	return filepath.Join(root, "agents", agent.AgentID, "sessions")
}
