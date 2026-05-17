package state

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/leonletto/thrum/internal/types"
)

// newTestState creates a fresh state backed by a temp directory.
// It mirrors the pattern used by existing tests in state_test.go.
func newTestState(t *testing.T) *State {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}
	st, err := NewState(thrumDir, thrumDir, "r_TESTQUERY", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// registerAgentWithSession writes an agent.register event followed by an
// agent.session.start event so the SQLite projection has both an agents
// row and a non-ended sessions row for the agent.
func registerAgentWithSession(t *testing.T, st *State, agentID, role string) {
	t.Helper()
	ctx := context.Background()

	reg := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-04-14T00:00:00Z",
		AgentID:   agentID,
		Kind:      "agent",
		Role:      role,
		Module:    "test",
	}
	if err := st.WriteEvent(ctx, reg); err != nil {
		t.Fatalf("write agent.register for %s: %v", agentID, err)
	}

	start := types.AgentSessionStartEvent{
		Type:      "agent.session.start",
		Timestamp: "2026-04-14T00:00:01Z",
		SessionID: "ses_" + agentID,
		AgentID:   agentID,
	}
	if err := st.WriteEvent(ctx, start); err != nil {
		t.Fatalf("write agent.session.start for %s: %v", agentID, err)
	}
}

// endAgentSession writes an agent.session.end event closing the session
// for the given agent.
func endAgentSession(t *testing.T, st *State, agentID string) {
	t.Helper()
	end := types.AgentSessionEndEvent{
		Type:      "agent.session.end",
		Timestamp: "2026-04-14T01:00:00Z",
		SessionID: "ses_" + agentID,
		Reason:    "test",
	}
	if err := st.WriteEvent(context.Background(), end); err != nil {
		t.Fatalf("write agent.session.end for %s: %v", agentID, err)
	}
}

func TestIsAgentActive_LiveSession(t *testing.T) {
	st := newTestState(t)
	registerAgentWithSession(t, st, "coordinator_main", "coordinator")

	active, err := st.IsAgentActive(context.Background(), "coordinator_main")
	if err != nil {
		t.Fatalf("IsAgentActive: %v", err)
	}
	if !active {
		t.Error("expected coordinator_main to be active")
	}
}

func TestIsAgentActive_MissingAgent(t *testing.T) {
	st := newTestState(t)

	active, err := st.IsAgentActive(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("IsAgentActive: %v", err)
	}
	if active {
		t.Error("missing agent should not be active")
	}
}

func TestIsAgentActive_EndedSession(t *testing.T) {
	st := newTestState(t)
	registerAgentWithSession(t, st, "coordinator_main", "coordinator")
	endAgentSession(t, st, "coordinator_main")

	active, err := st.IsAgentActive(context.Background(), "coordinator_main")
	if err != nil {
		t.Fatalf("IsAgentActive: %v", err)
	}
	if active {
		t.Error("ended session should not count as active")
	}
}

func TestListActiveAgentsByRole_MultipleMatches(t *testing.T) {
	st := newTestState(t)
	registerAgentWithSession(t, st, "coordinator_main", "coordinator")
	registerAgentWithSession(t, st, "coordinator_secondary", "coordinator")
	registerAgentWithSession(t, st, "researcher_alice", "researcher")

	got, err := st.ListActiveAgentsByRole(context.Background(), "coordinator")
	if err != nil {
		t.Fatalf("ListActiveAgentsByRole: %v", err)
	}
	sort.Strings(got)
	want := []string{"coordinator_main", "coordinator_secondary"}
	if len(got) != len(want) {
		t.Fatalf("got %d agents, want %d: %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestListActiveAgentsByRole_EmptyForUnknownRole(t *testing.T) {
	st := newTestState(t)
	registerAgentWithSession(t, st, "coordinator_main", "coordinator")

	got, err := st.ListActiveAgentsByRole(context.Background(), "nonexistent_role")
	if err != nil {
		t.Fatalf("ListActiveAgentsByRole: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestListActiveAgentsByRole_ExcludesEndedSessions(t *testing.T) {
	st := newTestState(t)
	registerAgentWithSession(t, st, "coordinator_main", "coordinator")
	registerAgentWithSession(t, st, "coordinator_secondary", "coordinator")
	endAgentSession(t, st, "coordinator_secondary")

	got, err := st.ListActiveAgentsByRole(context.Background(), "coordinator")
	if err != nil {
		t.Fatalf("ListActiveAgentsByRole: %v", err)
	}
	if len(got) != 1 || got[0] != "coordinator_main" {
		t.Errorf("expected [coordinator_main], got %v", got)
	}
}

// insertSessionRef directly inserts a session_ref row for an existing
// session. Used by the thrum-0pos worktree-lookup tests; the regular
// event-projection path does not materialize session_refs for the
// state_query test harness, so we insert by SQL to exercise the query.
func insertSessionRef(t *testing.T, st *State, sessionID, refType, refValue string) {
	t.Helper()
	_, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO session_refs (session_id, ref_type, ref_value, added_at)
		 VALUES (?, ?, ?, ?)`,
		sessionID, refType, refValue, "2026-04-14T00:00:02Z")
	if err != nil {
		t.Fatalf("insert session_ref: %v", err)
	}
}

// TestListAgentsInWorktree_SharedWorktree — thrum-0pos core case.
// Multiple agents share a worktree path; list returns all of them,
// regardless of whether their sessions are still active. Ended
// sessions must still surface so enforceWorktreeIdentity preserves
// files for agents between session.end and the next session.start.
func TestListAgentsInWorktree_SharedWorktree(t *testing.T) {
	st := newTestState(t)
	registerAgentWithSession(t, st, "coord", "coordinator")
	registerAgentWithSession(t, st, "tester", "tester")
	registerAgentWithSession(t, st, "ended_agent", "implementer")

	shared := "/tmp/shared-worktree"
	other := "/tmp/other-worktree"
	insertSessionRef(t, st, "ses_coord", "worktree", shared)
	insertSessionRef(t, st, "ses_tester", "worktree", shared)
	insertSessionRef(t, st, "ses_ended_agent", "worktree", shared)
	endAgentSession(t, st, "ended_agent")

	// Agent in a different worktree must not leak.
	registerAgentWithSession(t, st, "foreign", "tester")
	insertSessionRef(t, st, "ses_foreign", "worktree", other)

	got := st.ListAgentsInWorktree(context.Background(), shared)
	sort.Strings(got)
	want := []string{"coord", "ended_agent", "tester"}
	if !equalStringSlices(got, want) {
		t.Errorf("ListAgentsInWorktree(%q) = %v, want %v", shared, got, want)
	}

	// Empty worktree argument → empty result.
	empty := st.ListAgentsInWorktree(context.Background(), "")
	if len(empty) != 0 {
		t.Errorf("empty worktree must return empty list, got %v", empty)
	}
}

// TestIsAgentInWorktree_SessionRefAndFileFallback — thrum-0pos. The
// predicate is true when EITHER (a) a session_ref row maps the agent
// to the worktree (live or historical), OR (b) the caller's
// peercred-verified worktree contains a
// .thrum/identities/<agentID>.json file. The file-fallback closes
// the AN-10-style gap where `thrum agent register` runs before any
// session.start.
func TestIsAgentInWorktree_SessionRefAndFileFallback(t *testing.T) {
	st := newTestState(t)
	wt := "/tmp/zero-pos-worktree"

	// Agent A: has a session_ref mapping to wt (positive via SQL path).
	registerAgentWithSession(t, st, "has_ref", "coordinator")
	insertSessionRef(t, st, "ses_has_ref", "worktree", wt)
	if !st.IsAgentInWorktree(context.Background(), "has_ref", wt) {
		t.Error("has_ref must match via session_refs path")
	}

	// Agent B: registered but no session_ref; identity file exists in wt.
	// Use a real temp dir so the file-stat fallback can succeed.
	tmpWT := t.TempDir()
	idDir := filepath.Join(tmpWT, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "fresh_register.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write identity file: %v", err)
	}
	if !st.IsAgentInWorktree(context.Background(), "fresh_register", tmpWT) {
		t.Error("fresh_register must match via identity-file fallback")
	}

	// Agent C: not registered, no identity file → false.
	if st.IsAgentInWorktree(context.Background(), "stranger", wt) {
		t.Error("stranger must not match any path")
	}

	// Empty inputs → false.
	if st.IsAgentInWorktree(context.Background(), "", wt) {
		t.Error("empty agentID must return false")
	}
	if st.IsAgentInWorktree(context.Background(), "has_ref", "") {
		t.Error("empty worktree must return false")
	}
}

// TestIsAgentInWorktree_DifferentWorktreeRejected — forgery-defense
// anchor for DaemonResolve's shared-worktree fallback. An agent
// registered in worktree A must NOT be reported as belonging to
// worktree B; without this, a malicious process in B could pass a
// CallerAgentID claim of A's agent and get authenticated.
func TestIsAgentInWorktree_DifferentWorktreeRejected(t *testing.T) {
	st := newTestState(t)
	registerAgentWithSession(t, st, "coord_a", "coordinator")
	insertSessionRef(t, st, "ses_coord_a", "worktree", "/tmp/A")

	if st.IsAgentInWorktree(context.Background(), "coord_a", "/tmp/B") {
		t.Error("coord_a must NOT be reported in /tmp/B (forgery defense)")
	}
}

// TestListAgentsInWorktree_CanonicalizesPaths — peercred's resolver
// canonicalizes via filepath.EvalSymlinks before comparing; the DB
// may hold either the raw or the /private-prefixed form (macOS
// /tmp → /private/tmp). The state helper must match either direction
// so enforceWorktreeIdentity sees the same worktree the resolver did.
func TestListAgentsInWorktree_CanonicalizesPaths(t *testing.T) {
	st := newTestState(t)
	registerAgentWithSession(t, st, "coord", "coordinator")

	// Use an actual path so EvalSymlinks can produce a stable canonical form.
	tmpDir := t.TempDir()
	insertSessionRef(t, st, "ses_coord", "worktree", tmpDir)

	// Query using the same path returns the agent.
	got := st.ListAgentsInWorktree(context.Background(), tmpDir)
	if len(got) != 1 || got[0] != "coord" {
		t.Errorf("same-path lookup = %v, want [coord]", got)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
