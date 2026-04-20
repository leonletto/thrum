package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTouchAgentLastSeen_UpdatesRow covers thrum-7nuj: a call to
// TouchAgentLastSeen must advance the agent's last_seen_at column from
// its pre-touch value.
func TestTouchAgentLastSeen_UpdatesRow(t *testing.T) {
	s := newTestStateForTouch(t)

	const agentID = "coordinator_main"
	seedAgent(t, s, agentID, "2026-01-01T00:00:00Z")

	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	if err := s.touchAgentLastSeenAt(context.Background(), agentID, now); err != nil {
		t.Fatalf("touchAgentLastSeenAt: %v", err)
	}

	got := readLastSeen(t, s, agentID)
	want := now.Format(time.RFC3339Nano)
	if got != want {
		t.Errorf("last_seen_at = %q, want %q", got, want)
	}
}

// TestTouchAgentLastSeen_DebouncesRapidCalls pins the in-memory
// debounce: two TouchAgentLastSeen calls within the debounce window
// must result in only one DB update. Subsequent calls after the window
// elapses DO update again.
func TestTouchAgentLastSeen_DebouncesRapidCalls(t *testing.T) {
	s := newTestStateForTouch(t)

	const agentID = "impl_team_fix"
	seedAgent(t, s, agentID, "2026-01-01T00:00:00Z")

	base := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)

	// First touch — writes.
	if err := s.touchAgentLastSeenAt(context.Background(), agentID, base); err != nil {
		t.Fatalf("first touch: %v", err)
	}
	firstRead := readLastSeen(t, s, agentID)
	wantFirst := base.Format(time.RFC3339Nano)
	if firstRead != wantFirst {
		t.Fatalf("first last_seen_at = %q, want %q", firstRead, wantFirst)
	}

	// Second touch ~10s later (well inside the 30s debounce) — SKIPS.
	if err := s.touchAgentLastSeenAt(context.Background(), agentID, base.Add(10*time.Second)); err != nil {
		t.Fatalf("second touch: %v", err)
	}
	if got := readLastSeen(t, s, agentID); got != wantFirst {
		t.Errorf("after debounced touch: last_seen_at = %q, want %q (unchanged)", got, wantFirst)
	}

	// Third touch well past the window — writes again.
	later := base.Add(time.Minute)
	if err := s.touchAgentLastSeenAt(context.Background(), agentID, later); err != nil {
		t.Fatalf("third touch: %v", err)
	}
	if got := readLastSeen(t, s, agentID); got != later.Format(time.RFC3339Nano) {
		t.Errorf("after post-window touch: last_seen_at = %q, want %q", got, later.Format(time.RFC3339Nano))
	}
}

// TestTouchAgentLastSeen_UnknownAgent is a no-op for an agent that does
// not exist in the agents table. No error, no insert.
func TestTouchAgentLastSeen_UnknownAgent(t *testing.T) {
	s := newTestStateForTouch(t)

	err := s.TouchAgentLastSeen(context.Background(), "never_registered")
	if err != nil {
		t.Errorf("TouchAgentLastSeen for unknown agent: %v", err)
	}
	// Confirm no row was inserted.
	var count int
	if err := s.RawDB().QueryRow(`SELECT COUNT(*) FROM agents WHERE agent_id = ?`, "never_registered").Scan(&count); err != nil {
		t.Fatalf("count agents: %v", err)
	}
	if count != 0 {
		t.Errorf("agents row count = %d for unknown agent, want 0 (no insert)", count)
	}
}

// TestTouchAgentLastSeen_EmptyAgentID is a no-op when the caller
// passes an empty agent_id (e.g. resolveAgentAndSession fell through
// to anonymous before the handler reached touch).
func TestTouchAgentLastSeen_EmptyAgentID(t *testing.T) {
	s := newTestStateForTouch(t)
	if err := s.TouchAgentLastSeen(context.Background(), ""); err != nil {
		t.Errorf("TouchAgentLastSeen with empty agent_id returned: %v", err)
	}
}

// --- helpers ---

func newTestStateForTouch(t *testing.T) *State {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("mkdir .thrum: %v", err)
	}
	s, err := NewState(thrumDir, thrumDir, "r_TOUCH_TEST", "daemon_touch_test")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedAgent(t *testing.T, s *State, agentID, lastSeenAt string) {
	t.Helper()
	_, err := s.RawDB().Exec(`
		INSERT INTO agents (agent_id, kind, role, module, display, registered_at, last_seen_at)
		VALUES (?, 'agent', 'test', 'touch', '', ?, ?)
	`, agentID, lastSeenAt, lastSeenAt)
	if err != nil {
		t.Fatalf("seed agent %s: %v", agentID, err)
	}
}

func readLastSeen(t *testing.T, s *State, agentID string) string {
	t.Helper()
	var lastSeen string
	if err := s.RawDB().QueryRow(`SELECT last_seen_at FROM agents WHERE agent_id = ?`, agentID).Scan(&lastSeen); err != nil {
		t.Fatalf("read last_seen_at: %v", err)
	}
	return lastSeen
}
