package permission

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
)

// newSchedulerFixture constructs a Permission wired to a real State
// with a single live @coordinator_main supervisor agent. Exposes a
// mutable *time.Time so individual tests can advance the clock.
func newSchedulerFixture(t *testing.T) (*Permission, *time.Time) {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_SCHED", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Register a live coordinator so ResolveSupervisors returns a real
	// recipient via the default fallback ["coordinator"].
	ctx := context.Background()
	if err := st.WriteEvent(ctx, types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-04-14T00:00:00Z",
		AgentID:   "coordinator_main",
		Kind:      "agent",
		Role:      "coordinator",
		Module:    "test",
	}); err != nil {
		t.Fatalf("agent.register: %v", err)
	}
	if err := st.WriteEvent(ctx, types.AgentSessionStartEvent{
		Type:      "agent.session.start",
		Timestamp: "2026-04-14T00:00:01Z",
		SessionID: "ses_coordinator_main",
		AgentID:   "coordinator_main",
	}); err != nil {
		t.Fatalf("agent.session.start: %v", err)
	}

	p := New(st, st.RawDB(), "supervisor_thrum", "thrum", thrumDir)

	now := time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC)
	p.SetClock(func() time.Time { return now })
	return p, &now
}

// testPattern mirrors the cursor not_in_allowlist pattern without
// depending on the patterns package internals.
func testPattern() *Pattern {
	return &Pattern{
		Name:       "not_in_allowlist",
		ApproveKey: "y",
		DenyKey:    "Escape",
	}
}

func TestScheduler_FirstDetect(t *testing.T) {
	p, _ := newSchedulerFixture(t)
	ctx := context.Background()

	err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane content A")
	if err != nil {
		t.Fatalf("OnDetection: %v", err)
	}

	row, err := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if row == nil {
		t.Fatal("expected a nudge row after first detect")
	}
	if row.NudgeCount != 1 {
		t.Errorf("NudgeCount = %d, want 1", row.NudgeCount)
	}
	if row.PatternKey != "cursor.not_in_allowlist" {
		t.Errorf("PatternKey = %q", row.PatternKey)
	}
	if row.ApproveKey != "y" || row.DenyKey != "Escape" {
		t.Errorf("keystrokes not captured: %+v", row)
	}
	if row.MessageID == "" {
		t.Error("MessageID should be set to the real first-nudge msg_id")
	}

	// Verify a real message was written to the messages table under the
	// supervisor identity (not "system").
	var agentID string
	if err := p.state.RawDB().QueryRow(
		"SELECT agent_id FROM messages WHERE message_id = ?", row.MessageID,
	).Scan(&agentID); err != nil {
		t.Fatalf("query messages: %v", err)
	}
	if agentID != "supervisor_thrum" {
		t.Errorf("message agent_id = %q, want supervisor_thrum", agentID)
	}
}

func TestScheduler_NoReminderBeforeCadence(t *testing.T) {
	p, clock := newSchedulerFixture(t)
	ctx := context.Background()

	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("first detect: %v", err)
	}

	// Advance 4 minutes — under the 5-minute slot for reminder #2.
	*clock = clock.Add(4 * time.Minute)
	p.SetClock(func() time.Time { return *clock })

	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("second detect: %v", err)
	}
	row, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if row.NudgeCount != 1 {
		t.Errorf("NudgeCount = %d, want 1 (not yet time for reminder)", row.NudgeCount)
	}
}

func TestScheduler_ReminderCadence(t *testing.T) {
	p, clock := newSchedulerFixture(t)
	ctx := context.Background()

	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("first detect: %v", err)
	}

	// Advance through each reminder slot: 5m, 15m, 45m, 2h, 4h. At each
	// step, OnDetection should advance nudge_count by one.
	offsets := []time.Duration{
		5 * time.Minute,
		15 * time.Minute,
		45 * time.Minute,
		2 * time.Hour,
		4 * time.Hour,
	}
	first := *clock
	for i, off := range offsets {
		*clock = first.Add(off)
		p.SetClock(func() time.Time { return *clock })

		if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
			"researcher_cursor", testPattern(), "pane A"); err != nil {
			t.Fatalf("detect at slot %d: %v", i, err)
		}
		row, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
		wantCount := i + 2 // reminder #2, #3, …, #6
		if row.NudgeCount != wantCount {
			t.Errorf("slot %d: NudgeCount = %d, want %d", i, row.NudgeCount, wantCount)
		}
	}
}

func TestScheduler_PaneHashChange(t *testing.T) {
	p, clock := newSchedulerFixture(t)
	ctx := context.Background()

	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("first detect: %v", err)
	}
	row1, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	firstMsgID := row1.MessageID

	// Advance the clock arbitrarily and present a DIFFERENT pane tail
	// (different sha256 hash). The scheduler should treat this as a
	// new prompt — delete the old row and insert a fresh first-nudge.
	*clock = clock.Add(30 * time.Minute)
	p.SetClock(func() time.Time { return *clock })

	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane B (different)"); err != nil {
		t.Fatalf("second detect with new pane: %v", err)
	}
	row2, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if row2 == nil {
		t.Fatal("expected a fresh nudge row")
	}
	if row2.NudgeCount != 1 {
		t.Errorf("new prompt: NudgeCount = %d, want 1", row2.NudgeCount)
	}
	if row2.MessageID == firstMsgID {
		t.Error("expected a different MessageID for the fresh nudge")
	}
	// The old row must be gone.
	if gone, _ := p.store.LookupPendingNudgeByMessageID(ctx, firstMsgID); gone != nil {
		t.Error("old row should have been deleted on pane-hash change")
	}
}

func TestScheduler_FirstDetectWithoutSupervisors_InsertsOrphanRow(t *testing.T) {
	// No supervisor registered — only the permission agent itself, via
	// a minimal State fixture. We still want a row so a later recovery
	// path can clean it up.
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_ORPHAN", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	p := New(st, st.RawDB(), "supervisor_thrum", "thrum", thrumDir)
	fixedNow := time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC)
	p.SetClock(func() time.Time { return fixedNow })

	ctx := context.Background()
	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane X"); err != nil {
		t.Fatalf("OnDetection: %v", err)
	}

	row, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if row == nil {
		t.Fatal("expected an orphan row even with no supervisors")
	}
	if row.NudgeCount != 1 {
		t.Errorf("NudgeCount = %d, want 1", row.NudgeCount)
	}
}

func TestScheduler_GiveUp(t *testing.T) {
	// markAgentStuck is stubbed until Task 5.6 lands; this test verifies
	// the scheduler at count==6 takes the giveUp branch without crashing
	// and stops sending further nudges. Un-skip deeper assertions in
	// Task 5.7.
	p, clock := newSchedulerFixture(t)
	ctx := context.Background()

	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("first detect: %v", err)
	}

	// Manually advance the row to nudge_count=6 so the next OnDetection
	// hits the give-up branch without grinding through the full cadence.
	row, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	row.NudgeCount = 6
	if err := p.store.UpdatePendingNudge(ctx, row); err != nil {
		t.Fatalf("update: %v", err)
	}

	// Count messages in the messages table before, then after, the
	// give-up call. The give-up path must NOT send any more.
	var before int
	_ = p.state.RawDB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&before)

	*clock = clock.Add(8 * time.Hour)
	p.SetClock(func() time.Time { return *clock })

	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("give-up detect: %v", err)
	}

	var after int
	_ = p.state.RawDB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&after)
	if after != before {
		t.Errorf("give-up path sent %d extra nudges; want 0", after-before)
	}
}

func TestScheduler_Recovery(t *testing.T) {
	// clearAgentStuck is stubbed until Task 5.6; this test verifies
	// OnRecovery deletes the pending row and returns cleanly. Un-skip
	// deeper assertions in Task 5.7.
	p, _ := newSchedulerFixture(t)
	ctx := context.Background()

	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("first detect: %v", err)
	}

	if err := p.OnRecovery(ctx, "cursor-test", "researcher_cursor"); err != nil {
		t.Fatalf("OnRecovery: %v", err)
	}

	row, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if row != nil {
		t.Errorf("expected row to be deleted after recovery, got %+v", row)
	}
}

func TestScheduler_RecoveryWithoutPendingRow_NoOp(t *testing.T) {
	p, _ := newSchedulerFixture(t)
	if err := p.OnRecovery(context.Background(), "cursor-test", "researcher_cursor"); err != nil {
		t.Fatalf("OnRecovery on empty session should be a no-op, got %v", err)
	}
}
