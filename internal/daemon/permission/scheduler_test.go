package permission

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
)

// readIdentityFile parses the given identity file directly, bypassing
// config.LoadIdentityWithPath which honors THRUM_HOME and would
// redirect the test away from tmp.
func readIdentityFile(t *testing.T, thrumDir, agentName string) *config.IdentityFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(thrumDir, "identities", agentName+".json"))
	if err != nil {
		t.Fatalf("read identity %s: %v", agentName, err)
	}
	var idFile config.IdentityFile
	if err := json.Unmarshal(data, &idFile); err != nil {
		t.Fatalf("parse identity %s: %v", agentName, err)
	}
	return &idFile
}

// newSchedulerFixture constructs a Permission wired to a real State
// with a single live @coordinator_main supervisor agent. It also
// seeds an identity file for researcher_cursor (the nudged agent)
// so mark/clearAgentStuck have a real file to mutate. Exposes a
// mutable *time.Time so individual tests can advance the clock.
func newSchedulerFixture(t *testing.T) (*Permission, *time.Time) {
	t.Helper()
	// Defensive: block the ambient agent-session THRUM_HOME from
	// redirecting any future config.LoadIdentityWithPath reads away
	// from t.TempDir(). SaveIdentityFile takes an explicit thrumDir
	// and is safe today, but a future test extension that adds a
	// load-by-name call would silently pick up the wrong identities
	// dir without this guard. Cheap insurance.
	t.Setenv("THRUM_HOME", "")

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

	// Seed an identity file for the agent that will be nudged, so
	// setAgentStatus has a real file to read/write in the give-up and
	// recovery paths.
	researcherID := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "researcher_cursor",
			Role:   "researcher",
			Module: "cursor-test",
		},
	}
	if err := config.SaveIdentityFile(thrumDir, researcherID); err != nil {
		t.Fatalf("save researcher identity: %v", err)
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

// TestScheduler_PaneHashStableAcrossVolatileLines verifies that a
// volatile-line change (e.g., codex's "Working (Ns)" timer ticking)
// does NOT reset the reminder cadence. Without volatile-line stripping
// in the cadence hash, each poll on the same semantic prompt would
// look like a new prompt → fresh firstDetect → perpetual "Reminder #1"
// spam (observed in thrum-48kt.4 E2E against plugin-skills-slate).
func TestScheduler_PaneHashStableAcrossVolatileLines(t *testing.T) {
	p, clock := newSchedulerFixture(t)
	ctx := context.Background()

	paneA := `Would you like to run the following command?
  $ mkdir -p /tmp/foo
› 1. Yes, proceed (y)
• Working (3s • esc to interrupt)`

	paneB := `Would you like to run the following command?
  $ mkdir -p /tmp/foo
› 1. Yes, proceed (y)
• Working (42s • esc to interrupt)`

	// First detect with paneA (timer at 3s).
	if err := p.OnDetection(ctx, "codex-test", "codex", "codex-test:0.0",
		"researcher_codex", testPattern(), paneA); err != nil {
		t.Fatalf("first detect: %v", err)
	}
	row1, _ := p.store.LookupPendingNudgeBySession(ctx, "codex-test")
	if row1 == nil {
		t.Fatal("expected a pending row after first detect")
	}
	firstMsgID := row1.MessageID

	// Advance time less than the first reminder cadence (5 minutes) to
	// avoid the cadence path taking over.
	*clock = clock.Add(30 * time.Second)
	p.SetClock(func() time.Time { return *clock })

	// Same prompt, volatile timer has ticked (42s). Should be treated as
	// the SAME prompt — no firstDetect, no row delete, NudgeCount stays 1.
	if err := p.OnDetection(ctx, "codex-test", "codex", "codex-test:0.0",
		"researcher_codex", testPattern(), paneB); err != nil {
		t.Fatalf("second detect with volatile-only change: %v", err)
	}
	row2, _ := p.store.LookupPendingNudgeBySession(ctx, "codex-test")
	if row2 == nil {
		t.Fatal("expected pending row still present")
	}
	if row2.MessageID != firstMsgID {
		t.Errorf("expected same MessageID (same prompt), got %s vs %s",
			row2.MessageID, firstMsgID)
	}
	if row2.NudgeCount != 1 {
		t.Errorf("NudgeCount = %d, want 1 (no cadence advance before threshold)",
			row2.NudgeCount)
	}
}

// TestScheduler_PaneHashDistinctPromptsStillReset is the inverse of
// TestScheduler_PaneHashStableAcrossVolatileLines — guards against an
// over-broad stripVolatileLines pattern that would collapse
// semantically distinct prompts into the same hash. Two different
// codex commands (distinct Reason + $ lines) MUST still produce a new
// firstDetect so the scheduler correctly recognizes the prompt change.
func TestScheduler_PaneHashDistinctPromptsStillReset(t *testing.T) {
	p, clock := newSchedulerFixture(t)
	ctx := context.Background()

	promptMkdir := `Would you like to run the following command?
  Reason: Allow creating /tmp/foo outside the workspace?
  $ mkdir -p /tmp/foo
› 1. Yes, proceed (y)
• Working (5s • esc to interrupt)`

	promptRm := `Would you like to run the following command?
  Reason: Allow deleting /tmp/bar?
  $ rm -rf /tmp/bar
› 1. Yes, proceed (y)
• Working (5s • esc to interrupt)`

	if err := p.OnDetection(ctx, "codex-test", "codex", "codex-test:0.0",
		"researcher_codex", testPattern(), promptMkdir); err != nil {
		t.Fatalf("first detect: %v", err)
	}
	row1, _ := p.store.LookupPendingNudgeBySession(ctx, "codex-test")
	if row1 == nil {
		t.Fatal("expected pending row after first detect")
	}
	firstMsgID := row1.MessageID

	*clock = clock.Add(30 * time.Second)
	p.SetClock(func() time.Time { return *clock })

	// Different prompt — different command, different Reason. Must
	// produce a fresh firstDetect (new MessageID) even though both
	// panes share the same volatile "Working (5s)" line.
	if err := p.OnDetection(ctx, "codex-test", "codex", "codex-test:0.0",
		"researcher_codex", testPattern(), promptRm); err != nil {
		t.Fatalf("second detect with different prompt: %v", err)
	}
	row2, _ := p.store.LookupPendingNudgeBySession(ctx, "codex-test")
	if row2 == nil {
		t.Fatal("expected pending row after second detect")
	}
	if row2.MessageID == firstMsgID {
		t.Errorf("different prompt should have produced new MessageID; got same %s", firstMsgID)
	}
	if row2.NudgeCount != 1 {
		t.Errorf("new prompt: NudgeCount = %d, want 1", row2.NudgeCount)
	}
	// The old row must be gone (deleted on pane-hash change).
	if gone, _ := p.store.LookupPendingNudgeByMessageID(ctx, firstMsgID); gone != nil {
		t.Error("old row should have been deleted on pane-hash change")
	}
}

// TestScheduler_PaneHashStableAcrossClaudeStatusLine verifies that
// Claude's ccstatusline drift (Ctx size growing, Block countdown
// decrementing) does NOT reset the cadence hash for a semantically
// identical prompt. Mirrors TestScheduler_PaneHashStableAcrossVolatileLines
// but for thrum-ptcj's Claude-specific bug: three firstDetects fired in
// ~80s on plugin-skills-slate during the thrum-48kt.2 E2E setup because
// stripVolatileLines had no pattern for the Claude status line.
func TestScheduler_PaneHashStableAcrossClaudeStatusLine(t *testing.T) {
	p, clock := newSchedulerFixture(t)
	ctx := context.Background()

	paneA := `Would you like to run this command?
  $ rm -rf /tmp/foo
› 1. Yes, proceed (y)
  Model: Opus 4.7 (1M context) | Ctx: 697.2k | Block: 38m | Ctx: 70.0%`

	paneB := `Would you like to run this command?
  $ rm -rf /tmp/foo
› 1. Yes, proceed (y)
  Model: Opus 4.7 (1M context) | Ctx: 712.8k | Block: 36m | Ctx: 71.2%`

	// First detect with status-line values A.
	if err := p.OnDetection(ctx, "claude-test", "claude", "claude-test:0.0",
		"researcher_cursor", testPattern(), paneA); err != nil {
		t.Fatalf("first detect: %v", err)
	}
	row1, _ := p.store.LookupPendingNudgeBySession(ctx, "claude-test")
	if row1 == nil {
		t.Fatal("expected a pending row after first detect")
	}
	firstMsgID := row1.MessageID

	// Advance well under the first reminder cadence to stay in the
	// stability-hash path.
	*clock = clock.Add(40 * time.Second)
	p.SetClock(func() time.Time { return *clock })

	// Same prompt, status line has drifted (Ctx grew, Block counted
	// down). Must be treated as the SAME prompt — no firstDetect, same
	// MessageID, no NudgeCount advance.
	if err := p.OnDetection(ctx, "claude-test", "claude", "claude-test:0.0",
		"researcher_cursor", testPattern(), paneB); err != nil {
		t.Fatalf("second detect with status-line drift: %v", err)
	}
	row2, _ := p.store.LookupPendingNudgeBySession(ctx, "claude-test")
	if row2 == nil {
		t.Fatal("expected pending row still present")
	}
	if row2.MessageID != firstMsgID {
		t.Errorf("expected same MessageID across Claude status-line drift, got %s vs %s",
			row2.MessageID, firstMsgID)
	}
	if row2.NudgeCount != 1 {
		t.Errorf("NudgeCount = %d, want 1 (no cadence advance before threshold)",
			row2.NudgeCount)
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

// TestScheduler_OrphanPathMarksAgentStuck asserts the thrum-enlw.8 fix:
// when ResolveSupervisors returns zero recipients, the scheduler must
// mark the affected agent stuck immediately — the reminder cadence
// never runs without a recipient, so the give-up path is unreachable.
// Stuck is the observable signal that surfaces the silent-failure state
// in thrum team / UI.
func TestScheduler_OrphanPathMarksAgentStuck(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_ORPHAN_STUCK", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Seed an identity file for the nudged agent so markAgentStuck has
	// a file to mutate. No supervisor is registered, so ResolveSupervisors
	// via the default ["coordinator"] role broadcast returns empty.
	researcherID := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "researcher_cursor",
			Role:   "researcher",
			Module: "cursor-test",
		},
	}
	if err := config.SaveIdentityFile(thrumDir, researcherID); err != nil {
		t.Fatalf("save researcher identity: %v", err)
	}

	p := New(st, st.RawDB(), "supervisor_thrum", "thrum", thrumDir)
	fixedNow := time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC)
	p.SetClock(func() time.Time { return fixedNow })

	ctx := context.Background()
	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane X"); err != nil {
		t.Fatalf("OnDetection: %v", err)
	}

	// Orphan row must still exist (contract preserved).
	row, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if row == nil {
		t.Fatal("orphan row missing after OnDetection")
	}
	if row.NudgeCount != 1 {
		t.Errorf("NudgeCount = %d, want 1 (orphan insert is still a first-detect)", row.NudgeCount)
	}

	// Agent must be flagged stuck — the key regression signal.
	reloaded := readIdentityFile(t, thrumDir, "researcher_cursor")
	if reloaded.AgentStatus != "stuck" {
		t.Errorf("AgentStatus = %q, want stuck (orphan path should mark stuck immediately since give-up cadence is unreachable)", reloaded.AgentStatus)
	}
	if reloaded.AgentStatusUpdatedAt.IsZero() {
		t.Error("AgentStatusUpdatedAt should be set when orphan path marks stuck")
	}
}

func TestScheduler_GiveUp(t *testing.T) {
	// At count==6, the scheduler must (a) stop sending further nudges
	// and (b) mark the agent stuck via markAgentStuck — which Task 5.6
	// implemented as a real identity-file mutation.
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

	// The researcher's identity must now be flagged stuck.
	reloaded := readIdentityFile(t, p.thrumDir, "researcher_cursor")
	if reloaded.AgentStatus != "stuck" {
		t.Errorf("AgentStatus = %q, want stuck", reloaded.AgentStatus)
	}
	if reloaded.AgentStatusUpdatedAt.IsZero() {
		t.Error("AgentStatusUpdatedAt should be set when give-up fires")
	}
}

func TestScheduler_Recovery(t *testing.T) {
	// OnRecovery must (a) delete the pending row and (b) clear the
	// stuck flag via clearAgentStuck — which Task 5.6 implemented as
	// a real identity-file mutation. We seed the stuck status
	// beforehand so the clear path has something to reset.
	p, _ := newSchedulerFixture(t)
	ctx := context.Background()

	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("first detect: %v", err)
	}

	// Simulate the prior give-up having marked the agent stuck.
	if err := p.markAgentStuck(ctx, "researcher_cursor"); err != nil {
		t.Fatalf("seed stuck: %v", err)
	}
	seed := readIdentityFile(t, p.thrumDir, "researcher_cursor")
	if seed.AgentStatus != "stuck" {
		t.Fatalf("seed assertion: AgentStatus = %q, want stuck", seed.AgentStatus)
	}

	if err := p.OnRecovery(ctx, "cursor-test", "researcher_cursor"); err != nil {
		t.Fatalf("OnRecovery: %v", err)
	}

	row, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if row != nil {
		t.Errorf("expected row to be deleted after recovery, got %+v", row)
	}

	reloaded := readIdentityFile(t, p.thrumDir, "researcher_cursor")
	if reloaded.AgentStatus == "stuck" {
		t.Error("AgentStatus should be cleared after recovery")
	}
}

func TestScheduler_RecoveryWithoutPendingRow_NoOp(t *testing.T) {
	p, _ := newSchedulerFixture(t)
	if err := p.OnRecovery(context.Background(), "cursor-test", "researcher_cursor"); err != nil {
		t.Fatalf("OnRecovery on empty session should be a no-op, got %v", err)
	}
}

func TestLoadSupervisorEntries_UsesConfig(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := map[string]any{
		"permission_supervisors": []string{"coordinator", "@user:leon-letto"},
	}
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), b, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	p := &Permission{thrumDir: thrumDir}
	got := p.loadSupervisorEntries()
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %v", len(got), got)
	}
	if got[0] != "coordinator" || got[1] != "@user:leon-letto" {
		t.Errorf("entries mismatch: %v", got)
	}
}

func TestLoadSupervisorEntries_MissingFile(t *testing.T) {
	// LoadThrumConfig treats ENOENT as "use defaults", so a thrumDir
	// that exists but has no config.json must yield a nil
	// PermissionSupervisors slice (the field is zero-valued).
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	_ = os.MkdirAll(thrumDir, 0o750)

	p := &Permission{thrumDir: thrumDir}
	if got := p.loadSupervisorEntries(); got != nil {
		t.Errorf("expected nil for missing config, got %v", got)
	}
}

func TestLoadSupervisorEntries_EmptyField(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	_ = os.MkdirAll(thrumDir, 0o750)
	_ = os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600)

	p := &Permission{thrumDir: thrumDir}
	if got := p.loadSupervisorEntries(); got != nil {
		t.Errorf("expected nil when field absent, got %v", got)
	}
}

// seedAgentIdentity drops a minimal identity file into the given
// thrumDir/identities directory and returns the Permission pointing
// at it. Shared by the stuck / clear tests below.
func seedAgentIdentity(t *testing.T, agentName, initialStatus string) (*Permission, string) {
	t.Helper()
	t.Setenv("THRUM_HOME", "") // see newSchedulerFixture for rationale
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	idFile := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   agentName,
			Role:   "researcher",
			Module: "test",
		},
		AgentStatus: initialStatus,
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity: %v", err)
	}
	return &Permission{thrumDir: thrumDir}, thrumDir
}

func TestMarkAgentStuck_WritesStatusField(t *testing.T) {
	p, thrumDir := seedAgentIdentity(t, "researcher_cursor", "")

	if err := p.markAgentStuck(context.Background(), "researcher_cursor"); err != nil {
		t.Fatalf("markAgentStuck: %v", err)
	}

	reloaded := readIdentityFile(t, thrumDir, "researcher_cursor")
	if reloaded.AgentStatus != "stuck" {
		t.Errorf("AgentStatus = %q, want stuck", reloaded.AgentStatus)
	}
	if reloaded.AgentStatusUpdatedAt.IsZero() {
		t.Error("AgentStatusUpdatedAt should be set")
	}
}

func TestMarkAgentStuck_MissingIdentityErrors(t *testing.T) {
	p, _ := seedAgentIdentity(t, "researcher_cursor", "")
	err := p.markAgentStuck(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing identity")
	}
}

func TestClearAgentStuck_ClearsStatusField(t *testing.T) {
	p, thrumDir := seedAgentIdentity(t, "researcher_cursor", "stuck")

	if err := p.clearAgentStuck(context.Background(), "researcher_cursor"); err != nil {
		t.Fatalf("clearAgentStuck: %v", err)
	}
	reloaded := readIdentityFile(t, thrumDir, "researcher_cursor")
	if reloaded.AgentStatus == "stuck" {
		t.Error("AgentStatus should be cleared")
	}
	if reloaded.AgentStatusUpdatedAt.IsZero() {
		t.Error("AgentStatusUpdatedAt should be touched even on clear")
	}
}

func TestClearAgentStuck_NonStuckNoop(t *testing.T) {
	p, thrumDir := seedAgentIdentity(t, "researcher_cursor", "working")

	if err := p.clearAgentStuck(context.Background(), "researcher_cursor"); err != nil {
		t.Fatalf("clearAgentStuck: %v", err)
	}
	reloaded := readIdentityFile(t, thrumDir, "researcher_cursor")
	if reloaded.AgentStatus != "working" {
		t.Errorf("clearAgentStuck should only touch stuck status; got %q", reloaded.AgentStatus)
	}
}

func TestSetAgentStatus_EmptyNameIsNoOp(t *testing.T) {
	// Edge case: OnRecovery can call clearAgentStuck("") when
	// findIdentityForSession returns "" because the agent's
	// identity file was deleted between firstDetect and recovery
	// (e.g. `thrum agent delete` ran while a nudge was pending).
	// Without the empty-name guard in setAgentStatus, we'd try to
	// read .thrum/identities/.json and return a spurious ENOENT.
	p, _ := seedAgentIdentity(t, "placeholder", "")
	for _, fn := range []func(string) error{
		func(name string) error { return p.markAgentStuck(context.Background(), name) },
		func(name string) error { return p.clearAgentStuck(context.Background(), name) },
	} {
		if err := fn(""); err != nil {
			t.Errorf("empty name should be a silent no-op, got %v", err)
		}
	}
}

func TestOnRecovery_ClearsRowEvenWhenIdentityDeleted(t *testing.T) {
	// Full integration regression for Medium 1: seed a pending
	// nudge, delete the identity file, call OnRecovery with the
	// empty agent name that findIdentityForSession returns in
	// production — the row must still be deleted and no error
	// should propagate.
	p, clock := newSchedulerFixture(t)
	ctx := context.Background()

	// Seed a pending row via the normal first-detect path.
	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("first detect: %v", err)
	}

	// Delete the identity file out from under the scheduler.
	idPath := filepath.Join(p.thrumDir, "identities", "researcher_cursor.json")
	if err := os.Remove(idPath); err != nil {
		t.Fatalf("remove identity: %v", err)
	}

	// Simulate HandleCheckPane's idle path: findIdentityForSession
	// returns empty name because the file is gone.
	*clock = clock.Add(5 * time.Second)
	p.SetClock(func() time.Time { return *clock })

	if err := p.OnRecovery(ctx, "cursor-test", ""); err != nil {
		t.Errorf("OnRecovery with empty agent name should succeed, got %v", err)
	}

	// Row must still be deleted from the store.
	row, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if row != nil {
		t.Errorf("expected row to be deleted by OnRecovery, got %+v", row)
	}
}

// TestScheduler_ClaudeDenyKeyDisambiguation — thrum-uy1n. The
// scheduler must consult DisambiguateClaudeDeny when the matched
// pattern is claude.tool_confirmation, so the row's DenyKey reflects
// the on-screen prompt shape rather than the pattern library's
// default. The bug 2026-04-20 14:30 UTC: a 2-option Bash picker on
// screen, but the row carried DenyKey="3" (Variant A default), so
// the supervisor's nudge hint read `"1"|"3"` for a dialog that only
// offered 1 and 2.
func TestScheduler_ClaudeDenyKeyDisambiguation(t *testing.T) {
	p, _ := newSchedulerFixture(t)
	ctx := context.Background()

	// Pattern carries the library default (Variant A → "3"). Scheduler
	// must override per shape.
	claudeTC := &Pattern{
		Name:       "tool_confirmation",
		ApproveKey: "1",
		DenyKey:    "3",
	}

	// cursorAllowlist (non-claude control). The scheduler's guard is
	// `runtime == "claude" && matched.Name == "tool_confirmation"`;
	// any future relaxation that lets DisambiguateClaudeDeny run for
	// non-claude runtimes would silently corrupt the row's DenyKey
	// based on whatever digits happen to live in the cursor pane's
	// scrollback. Pinned here so a regression fails CI.
	cursorAllowlist := &Pattern{
		Name:       "not_in_allowlist",
		ApproveKey: "y",
		DenyKey:    "Escape",
	}

	cases := []struct {
		name     string
		runtime  string
		pattern  *Pattern
		pane     string
		wantDeny string
	}{
		{
			name:    "VariantA_3option_keeps_library_default",
			runtime: "claude",
			pattern: claudeTC,
			pane: `⏺ Bash(curl https://example.com)
  ⎿  Do you want to proceed?
     1. Yes
     2. Yes, and don't ask again for Bash(curl)
     3. No, and tell Claude what to do differently (Esc)`,
			wantDeny: "3",
		},
		{
			name:    "VariantB_Bash_2option_overrides_to_2",
			runtime: "claude",
			pattern: claudeTC,
			pane: `⏺ Bash(rm -rf /tmp/foo)
  ⎿  Do you want to proceed?
     1. Yes
     2. No
     Esc to cancel · Tab to amend · ctrl+e to explain`,
			wantDeny: "2",
		},
		{
			name:    "VariantB_Read_2option_falls_back_to_Escape",
			runtime: "claude",
			pattern: claudeTC,
			pane: ` Read file

 Search(pattern: "## Task 0.2", path: "~/plans/...")


 Do you want to proceed?
 ❯ 1. Yes
   2. Yes, allow reading from plans/ during this session

 Esc to cancel · Tab to amend`,
			wantDeny: "Escape",
		},
		{
			// Non-claude control. Pane deliberately contains an
			// option-3-shaped line that DisambiguateClaudeDeny WOULD
			// match if invoked; the scheduler guard must keep the
			// library DenyKey (Escape) intact for the cursor runtime.
			name:    "Cursor_runtime_keeps_library_default",
			runtime: "cursor",
			pattern: cursorAllowlist,
			pane: `Run this command?
Not in allowlist: curl https://example.com
 → Run (once) (y)
   3. some line that happens to start with three-dot in unrelated context
   Skip (esc or n)`,
			wantDeny: "Escape",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := "test-" + tc.name
			if err := p.OnDetection(ctx, session, tc.runtime, session+":0.0",
				"researcher_cursor", tc.pattern, tc.pane); err != nil {
				t.Fatalf("OnDetection: %v", err)
			}
			row, err := p.store.LookupPendingNudgeBySession(ctx, session)
			if err != nil || row == nil {
				t.Fatalf("lookup row: %v (row=%+v)", err, row)
			}
			if row.DenyKey != tc.wantDeny {
				t.Errorf("DenyKey = %q, want %q", row.DenyKey, tc.wantDeny)
			}
		})
	}
}
