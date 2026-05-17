package sessionarchive_test

import (
	"path/filepath"
	"testing"

	agentpkg "github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/sessionarchive"
)

func TestSessionsDir_PersistentAgent_RoutesToMainRepo(t *testing.T) {
	agent := agentpkg.Agent{
		AgentID: "researcher_agents",
		Mode:    agentpkg.ModePersistent,
	}
	mainRepoThrumDir := "/main/repo/.thrum"
	worktreeThrumDir := "/wt/path/.thrum"

	got := sessionarchive.SessionsDir(agent, mainRepoThrumDir, worktreeThrumDir)
	want := filepath.Join("/main/repo/.thrum", "agents", "researcher_agents", "sessions")
	if got != want {
		t.Errorf("persistent SessionsDir: got %q, want %q", got, want)
	}
}

func TestSessionsDir_EphemeralAgent_RoutesToWorktree(t *testing.T) {
	agent := agentpkg.Agent{
		AgentID: "impl_session_archive",
		Mode:    agentpkg.ModeEphemeral,
	}
	mainRepoThrumDir := "/main/repo/.thrum"
	worktreeThrumDir := "/wt/path/.thrum"

	got := sessionarchive.SessionsDir(agent, mainRepoThrumDir, worktreeThrumDir)
	want := filepath.Join("/wt/path/.thrum", "agents", "impl_session_archive", "sessions")
	if got != want {
		t.Errorf("ephemeral SessionsDir: got %q, want %q", got, want)
	}
}

// TestSessionsDir_UnknownMode_DefensiveFallback covers the
// defensive default branch. The agent.register validator (B-B1 E6.0
// Task 5) rejects unknown modes at registration time, so this only
// fires in a pathological state — a code bug, not a user path —
// but the helper must still return a usable path to avoid
// downstream nil-handling churn.
func TestSessionsDir_UnknownMode_DefensiveFallback(t *testing.T) {
	agent := agentpkg.Agent{
		AgentID: "broken_mode_agent",
		Mode:    "totally-invalid-mode",
	}
	mainRepoThrumDir := "/main/repo/.thrum"
	worktreeThrumDir := "/wt/path/.thrum"

	got := sessionarchive.SessionsDir(agent, mainRepoThrumDir, worktreeThrumDir)
	want := filepath.Join("/main/repo/.thrum", "agents", "broken_mode_agent", "sessions")
	if got != want {
		t.Errorf("defensive fallback: got %q, want %q", got, want)
	}
}

func TestSessionsDir_EmptyMode_DefensiveFallback(t *testing.T) {
	// Pre-v0.11 rows backfill to (persistent, long_lived) per Agent
	// docstring — but Lookup might return zero-valued Mode in odd
	// edge cases. Fallback to main-repo keeps behavior predictable.
	agent := agentpkg.Agent{
		AgentID: "legacy_no_mode",
		Mode:    "",
	}
	got := sessionarchive.SessionsDir(agent, "/main/.thrum", "/wt/.thrum")
	want := filepath.Join("/main/.thrum", "agents", "legacy_no_mode", "sessions")
	if got != want {
		t.Errorf("empty-mode fallback: got %q, want %q", got, want)
	}
}
