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
