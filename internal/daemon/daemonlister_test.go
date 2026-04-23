package daemon_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/stretchr/testify/require"
)

// TestDaemonAgentLister_TwoAgentsTwoWorktrees is the regression test for
// thrum-2x0p. It registers two distinct agents in two distinct worktrees
// (matching the real multi-agent topology on a developer machine) and proves
// the lister returns BOTH from session_refs — not just whichever happens to
// also be in agent_work_contexts.
//
// Pre-fix (sec.3 commit 7a08714): the lister queried agent_work_contexts,
// which is sparsely populated. Only agents with rows there were resolvable;
// every other agent was treated as anonymous → -32002 on every mutating RPC.
//
// Post-fix: the lister queries session_refs JOIN sessions, which is the
// canonical "this agent has registered a worktree" mapping. Every agent
// that has called session.start with a worktree ref is now resolvable.
func TestDaemonAgentLister_TwoAgentsTwoWorktrees(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := state.NewState(tmpDir, tmpDir, "test-repo", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC().Format(time.RFC3339)

	// Two distinct agents in two distinct worktrees (matches real topology).
	agents := []struct {
		agentID   string
		sessionID string
		worktree  string
	}{
		{"impl_alpha", "ses_alpha_001", "/Users/leon/.workspaces/thrum/alpha"},
		{"impl_bravo", "ses_bravo_001", "/Users/leon/.workspaces/thrum/bravo"},
	}

	for _, a := range agents {
		// Insert agent row.
		_, err := st.DB().ExecContext(context.Background(), `
			INSERT INTO agents (agent_id, kind, role, module, display, hostname, agent_pid, registered_at, last_seen_at)
			VALUES (?, 'implementer', 'implementer', 'test', ?, '', 0, ?, ?)
		`, a.agentID, a.agentID, now, now)
		require.NoError(t, err)

		// Insert active session.
		_, err = st.DB().ExecContext(context.Background(), `
			INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
			VALUES (?, ?, ?, ?)
		`, a.sessionID, a.agentID, now, now)
		require.NoError(t, err)

		// Insert session_ref with worktree path (canonical mapping).
		_, err = st.DB().ExecContext(context.Background(), `
			INSERT INTO session_refs (session_id, ref_type, ref_value, added_at)
			VALUES (?, 'worktree', ?, ?)
		`, a.sessionID, a.worktree, now)
		require.NoError(t, err)

		// Deliberately do NOT seed agent_work_contexts — proves the lister no
		// longer depends on it. (Pre-fix: missing this row → anonymous.)
	}

	lister := daemon.NewDaemonAgentLister(st)
	got, err := lister.ListAgentWorktrees()
	require.NoError(t, err)
	require.Len(t, got, 2, "lister must return both registered agents")

	gotMap := make(map[string]string, len(got))
	for _, w := range got {
		gotMap[w.AgentID] = w.Worktree
	}
	require.Equal(t, "/Users/leon/.workspaces/thrum/alpha", gotMap["impl_alpha"])
	require.Equal(t, "/Users/leon/.workspaces/thrum/bravo", gotMap["impl_bravo"])
}

// TestDaemonAgentLister_EndedSessionExcluded proves that an agent whose
// session has ended is no longer returned, so a stale worktree path can't
// shadow a fresh registration from the same agent in a different worktree.
func TestDaemonAgentLister_EndedSessionExcluded(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := state.NewState(tmpDir, tmpDir, "test-repo", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC().Format(time.RFC3339)

	// Register agent.
	_, err = st.DB().ExecContext(context.Background(), `
		INSERT INTO agents (agent_id, kind, role, module, display, hostname, agent_pid, registered_at, last_seen_at)
		VALUES ('impl_test', 'implementer', 'implementer', 'test', 'test', '', 0, ?, ?)
	`, now, now)
	require.NoError(t, err)

	// Old, ended session in worktree A.
	_, err = st.DB().ExecContext(context.Background(), `
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at, ended_at, end_reason)
		VALUES ('ses_old', 'impl_test', ?, ?, ?, 'closed')
	`, now, now, now)
	require.NoError(t, err)
	_, err = st.DB().ExecContext(context.Background(), `
		INSERT INTO session_refs (session_id, ref_type, ref_value, added_at)
		VALUES ('ses_old', 'worktree', '/path/to/old', ?)
	`, now)
	require.NoError(t, err)

	// Active session in worktree B.
	_, err = st.DB().ExecContext(context.Background(), `
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES ('ses_new', 'impl_test', ?, ?)
	`, now, now)
	require.NoError(t, err)
	_, err = st.DB().ExecContext(context.Background(), `
		INSERT INTO session_refs (session_id, ref_type, ref_value, added_at)
		VALUES ('ses_new', 'worktree', '/path/to/new', ?)
	`, now)
	require.NoError(t, err)

	lister := daemon.NewDaemonAgentLister(st)
	got, err := lister.ListAgentWorktrees()
	require.NoError(t, err)
	require.Len(t, got, 1, "ended session must be excluded")
	require.Equal(t, "impl_test", got[0].AgentID)
	require.Equal(t, "/path/to/new", got[0].Worktree, "active session's worktree wins")
}

// TestDaemonAgentLister_EmptyDB returns nothing, no error. Sanity baseline
// for fresh-daemon scenario before any agent has registered.
func TestDaemonAgentLister_EmptyDB(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := state.NewState(tmpDir, tmpDir, "test-repo", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	lister := daemon.NewDaemonAgentLister(st)
	got, err := lister.ListAgentWorktrees()
	require.NoError(t, err)
	require.Empty(t, got)
}

// stash unused import to satisfy go vet during early scaffolding edits;
// keep using filepath in case a future test wants temp-dir paths.
var _ = filepath.Join
