package reminders

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

var ctx = context.Background()

func TestStore_Mint_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	trigger := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	r := &Reminder{
		Source:      SourceAgent,
		TriggerKind: TriggerTime,
		SourceAgent: "docs_bot",
		TriggerAt:   &trigger,
		TargetAgent: "docs_bot",
		Body:        "round-trip body",
	}
	if err := s.Mint(ctx, r); err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if r.ID == "" {
		t.Fatal("Mint should populate id")
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Source != SourceAgent {
		t.Errorf("Source = %q", got.Source)
	}
	if got.TriggerKind != TriggerTime {
		t.Errorf("TriggerKind = %q", got.TriggerKind)
	}
	if got.SourceAgent != "docs_bot" {
		t.Errorf("SourceAgent = %q", got.SourceAgent)
	}
	if got.TargetAgent != "docs_bot" {
		t.Errorf("TargetAgent = %q", got.TargetAgent)
	}
	if got.Body != "round-trip body" {
		t.Errorf("Body = %q", got.Body)
	}
	if got.State != StateOpen {
		t.Errorf("State = %q (expected open)", got.State)
	}
	if got.TriggerAt == nil || !got.TriggerAt.Equal(trigger) {
		t.Errorf("TriggerAt = %v (want %v)", got.TriggerAt, trigger)
	}
	// next_reminder_at must be auto-derived from trigger_at for time rows
	// so the dispatcher sees the row in DueOpen.
	if got.NextReminderAt == nil || !got.NextReminderAt.Equal(trigger) {
		t.Errorf("NextReminderAt = %v (want auto-derived %v)", got.NextReminderAt, trigger)
	}
	if got.DeferHistory == nil || len(got.DeferHistory) != 0 {
		t.Errorf("DeferHistory = %v (want empty)", got.DeferHistory)
	}
}

func TestStore_Mint_RejectsInvalid(t *testing.T) {
	s := newTestStore(t)
	// agent/time missing trigger_at — validator must reject before INSERT.
	r := &Reminder{
		Source:      SourceAgent,
		TriggerKind: TriggerTime,
		SourceAgent: "docs_bot",
		TargetAgent: "docs_bot",
		Body:        "no time",
	}
	if err := s.Mint(ctx, r); err == nil {
		t.Fatal("expected validation error")
	}
	// Confirm no row landed.
	if r.ID != "" {
		got, err := s.Get(ctx, r.ID)
		if err == nil && got != nil {
			t.Errorf("row was inserted despite validator failure: %+v", got)
		}
	}
}

func TestStore_Mint_TruncatesOversizedPaneSnapshot(t *testing.T) {
	s := newTestStore(t)
	huge := makeBigString('q', MaxSnapshotBytes+1024)
	past := time.Now().Add(-time.Minute).UTC()
	r := &Reminder{
		Source:         SourceDaemon,
		TriggerKind:    TriggerConditionPaneQuiet,
		TriggerMeta:    json.RawMessage(`{"agent":"x"}`),
		TargetChain:    []string{"@coord"},
		PaneSnapshot:   huge,
		NextReminderAt: &past,
	}
	if err := s.Mint(ctx, r); err != nil {
		t.Fatalf("Mint: %v", err)
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.PaneSnapshot) > MaxSnapshotBytes {
		t.Errorf("pane_snapshot bytes %d exceeds cap %d", len(got.PaneSnapshot), MaxSnapshotBytes)
	}
}

func makeBigString(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func TestStore_Defer_AppendsHistory(t *testing.T) {
	s := newTestStore(t)
	id := mintOpenTime(t, s)
	t1 := time.Now().Add(2 * time.Hour).UTC()
	if err := s.Defer(ctx, id, t1, "leon"); err != nil {
		t.Fatalf("Defer 1: %v", err)
	}
	t2 := time.Now().Add(4 * time.Hour).UTC()
	if err := s.Defer(ctx, id, t2, "coordinator"); err != nil {
		t.Fatalf("Defer 2: %v", err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.DeferHistory) != 2 {
		t.Fatalf("DeferHistory len = %d, want 2 (got=%+v)", len(got.DeferHistory), got.DeferHistory)
	}
	if got.DeferHistory[0].DeferredBy != "leon" || got.DeferHistory[1].DeferredBy != "coordinator" {
		t.Errorf("DeferHistory entries out of order or wrong by: %+v", got.DeferHistory)
	}
	if got.NextReminderAt == nil || !got.NextReminderAt.Equal(t2.Truncate(time.Second)) {
		t.Errorf("NextReminderAt = %v, want last defer target %v", got.NextReminderAt, t2)
	}
}

func TestStore_Clear_TerminatesNextReminderAt(t *testing.T) {
	s := newTestStore(t)
	id := mintOpenTime(t, s)
	if err := s.Clear(ctx, id, "leon"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateCleared {
		t.Errorf("State = %q (want cleared)", got.State)
	}
	if got.NextReminderAt != nil {
		t.Errorf("NextReminderAt = %v (want nil after Clear)", got.NextReminderAt)
	}
	if got.ClearedAt == nil {
		t.Error("ClearedAt should be populated after Clear")
	}
}

func TestStore_Cancel_TerminatesNextReminderAt(t *testing.T) {
	s := newTestStore(t)
	id := mintOpenTime(t, s)
	if err := s.Cancel(ctx, id, "leon"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateCancelled {
		t.Errorf("State = %q (want cancelled)", got.State)
	}
	if got.NextReminderAt != nil {
		t.Errorf("NextReminderAt = %v (want nil after Cancel)", got.NextReminderAt)
	}
	if got.CancelledAt == nil {
		t.Error("CancelledAt should be populated after Cancel")
	}
}

func TestStore_Fire_OneShotTransitionsToFired(t *testing.T) {
	s := newTestStore(t)
	id := mintOpenTime(t, s)
	fired := time.Now().UTC()
	if err := s.Fire(ctx, id, fired); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateFired {
		t.Errorf("State = %q (want fired)", got.State)
	}
	if got.NextReminderAt != nil {
		t.Errorf("NextReminderAt = %v (want nil after Fire)", got.NextReminderAt)
	}
	if got.LastFiredAt == nil {
		t.Error("LastFiredAt should be populated after Fire")
	}
}

func TestStore_Fire_RejectsConditionTriggerKind(t *testing.T) {
	s := newTestStore(t)
	id := mintOpenCondition(t, s)
	err := s.Fire(ctx, id, time.Now())
	if err == nil {
		t.Fatal("expected ErrWrongTriggerKind for Fire on condition row")
	}
	if !errors.Is(err, ErrWrongTriggerKind) {
		t.Errorf("expected ErrWrongTriggerKind, got %v", err)
	}
}

func TestStore_FireAndRearm_ConditionStaysOpen(t *testing.T) {
	s := newTestStore(t)
	id := mintOpenCondition(t, s)
	fired := time.Now().UTC()
	next := fired.Add(15 * time.Minute)
	if err := s.FireAndRearm(ctx, id, fired, next); err != nil {
		t.Fatalf("FireAndRearm: %v", err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateOpen {
		t.Errorf("State = %q (want still open per Q3.4)", got.State)
	}
	if got.LastFiredAt == nil || !got.LastFiredAt.Equal(fired.Truncate(time.Second)) {
		t.Errorf("LastFiredAt = %v, want %v", got.LastFiredAt, fired)
	}
	if got.NextReminderAt == nil || !got.NextReminderAt.Equal(next.Truncate(time.Second)) {
		t.Errorf("NextReminderAt = %v, want %v", got.NextReminderAt, next)
	}
}

func TestStore_FireAndRearm_RejectsTimeTriggerKind(t *testing.T) {
	s := newTestStore(t)
	id := mintOpenTime(t, s)
	err := s.FireAndRearm(ctx, id, time.Now(), time.Now().Add(time.Hour))
	if err == nil {
		t.Fatal("expected ErrWrongTriggerKind for FireAndRearm on time row")
	}
	if !errors.Is(err, ErrWrongTriggerKind) {
		t.Errorf("expected ErrWrongTriggerKind, got %v", err)
	}
}

func TestStore_DueOpen_OnlyReturnsDue(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	// past — due
	pastTrigger := now.Add(-2 * time.Hour)
	pastRow := &Reminder{
		Source: SourceAgent, TriggerKind: TriggerTime, SourceAgent: "a",
		TriggerAt: &pastTrigger, TargetAgent: "a", Body: "past",
	}
	if err := s.Mint(ctx, pastRow); err != nil {
		t.Fatalf("mint past: %v", err)
	}
	// now-ish — due (within window)
	nowTrigger := now.Add(-1 * time.Second)
	nowRow := &Reminder{
		Source: SourceAgent, TriggerKind: TriggerTime, SourceAgent: "a",
		TriggerAt: &nowTrigger, TargetAgent: "a", Body: "now",
	}
	if err := s.Mint(ctx, nowRow); err != nil {
		t.Fatalf("mint now: %v", err)
	}
	// future — not due
	futureTrigger := now.Add(time.Hour)
	futureRow := &Reminder{
		Source: SourceAgent, TriggerKind: TriggerTime, SourceAgent: "a",
		TriggerAt: &futureTrigger, TargetAgent: "a", Body: "future",
	}
	if err := s.Mint(ctx, futureRow); err != nil {
		t.Fatalf("mint future: %v", err)
	}

	due, err := s.DueOpen(ctx, now)
	if err != nil {
		t.Fatalf("DueOpen: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("DueOpen returned %d rows, want 2: %+v", len(due), due)
	}
	for _, r := range due {
		if r.Body == "future" {
			t.Errorf("DueOpen included future row %s", r.ID)
		}
	}
}

func TestStore_DueOpen_SkipsTerminal(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	pastTrigger := now.Add(-1 * time.Hour)
	r := &Reminder{
		Source: SourceAgent, TriggerKind: TriggerTime, SourceAgent: "a",
		TriggerAt: &pastTrigger, TargetAgent: "a", Body: "past",
	}
	if err := s.Mint(ctx, r); err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := s.Clear(ctx, r.ID, "leon"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	due, err := s.DueOpen(ctx, now)
	if err != nil {
		t.Fatalf("DueOpen: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("DueOpen returned %d rows after Clear (want 0)", len(due))
	}
}

func TestStore_OpenForAgent_FiltersByTarget(t *testing.T) {
	s := newTestStore(t)
	for _, who := range []string{"alice", "alice", "bob"} {
		trigger := time.Now().Add(time.Hour).UTC()
		r := &Reminder{
			Source: SourceAgent, TriggerKind: TriggerTime, SourceAgent: who,
			TriggerAt: &trigger, TargetAgent: who, Body: "x",
		}
		if err := s.Mint(ctx, r); err != nil {
			t.Fatalf("mint for %s: %v", who, err)
		}
	}
	got, err := s.OpenForAgent(ctx, "alice")
	if err != nil {
		t.Fatalf("OpenForAgent: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("alice rows: got %d, want 2", len(got))
	}
	for _, r := range got {
		if r.TargetAgent != "alice" {
			t.Errorf("returned row with target_agent=%q", r.TargetAgent)
		}
	}
}

func TestStore_OpenForAgent_ExcludesTerminal(t *testing.T) {
	s := newTestStore(t)
	id := mintOpenTime(t, s)
	if err := s.Cancel(ctx, id, "leon"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	got, err := s.OpenForAgent(ctx, "test_agent")
	if err != nil {
		t.Fatalf("OpenForAgent: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d rows after cancel, want 0", len(got))
	}
}

func TestStore_List_ByFilter(t *testing.T) {
	s := newTestStore(t)
	// Two agent/time rows for alice, one user/time row for alice.
	for range 2 {
		trigger := time.Now().Add(time.Hour).UTC()
		r := &Reminder{
			Source: SourceAgent, TriggerKind: TriggerTime, SourceAgent: "alice",
			TriggerAt: &trigger, TargetAgent: "alice", Body: "agent",
		}
		if err := s.Mint(ctx, r); err != nil {
			t.Fatalf("mint agent: %v", err)
		}
	}
	{
		trigger := time.Now().Add(time.Hour).UTC()
		r := &Reminder{
			Source: SourceUser, TriggerKind: TriggerTime,
			TriggerAt: &trigger, TargetAgent: "alice", Body: "user",
		}
		if err := s.Mint(ctx, r); err != nil {
			t.Fatalf("mint user: %v", err)
		}
	}

	srcAgent := SourceAgent
	got, err := s.List(ctx, ListFilter{Source: &srcAgent, TargetAgent: "alice"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("filter (source=agent, target=alice): got %d, want 2", len(got))
	}
	for _, r := range got {
		if r.Source != SourceAgent {
			t.Errorf("returned row with source=%q", r.Source)
		}
	}
}

func TestStore_Get_NotFoundReturnsErrNoRows(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Get(ctx, "reminder-nobody-000-0000")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("got %v, want sql.ErrNoRows", err)
	}
}

// ----- MintConditionForAgent idempotency (brainstorm Q3.8 match-key) -----

func TestStore_MintConditionForAgent_Idempotent(t *testing.T) {
	s := newTestStore(t)
	snap := "pane snapshot bytes"
	chain := []string{"@coordinator_main"}
	meta := json.RawMessage(`{"agent":"docs_bot","quiet_since":1700000000}`)

	r1, minted1, err := s.MintConditionForAgent(ctx, "docs_bot", meta, chain, snap, time.Now().Add(15*time.Minute))
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !minted1 {
		t.Fatal("first call should mint")
	}
	if r1.ID == "" {
		t.Fatal("first call should return populated id")
	}

	r2, minted2, err := s.MintConditionForAgent(ctx, "docs_bot", meta, chain, snap, time.Now().Add(20*time.Minute))
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if minted2 {
		t.Fatal("second call should NOT mint (existing open row)")
	}
	if r2.ID != r1.ID {
		t.Errorf("second call returned %s; want existing %s", r2.ID, r1.ID)
	}
}

func TestStore_MintConditionForAgent_RemintsAfterClear(t *testing.T) {
	s := newTestStore(t)
	chain := []string{"@coord"}
	meta := json.RawMessage(`{"agent":"docs_bot"}`)

	r1, minted, err := s.MintConditionForAgent(ctx, "docs_bot", meta, chain, "snap", time.Now().Add(time.Minute))
	if err != nil || !minted {
		t.Fatalf("first mint: minted=%v err=%v", minted, err)
	}
	if err := s.Clear(ctx, r1.ID, "leon"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	r2, minted2, err := s.MintConditionForAgent(ctx, "docs_bot", meta, chain, "snap2", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("re-mint after clear: %v", err)
	}
	if !minted2 {
		t.Fatal("re-mint after clear should mint a new row")
	}
	if r2.ID == r1.ID {
		t.Errorf("re-mint returned same id %s; expected new row", r2.ID)
	}
}

// MintConditionForAgent is scoped per target_agent — different agents
// don't share the idempotency match-key.
func TestStore_MintConditionForAgent_PerAgentScoping(t *testing.T) {
	s := newTestStore(t)
	chain := []string{"@coord"}
	meta := json.RawMessage(`{}`)

	_, mintedA, err := s.MintConditionForAgent(ctx, "alice", meta, chain, "x", time.Now().Add(time.Minute))
	if err != nil || !mintedA {
		t.Fatalf("alice mint: minted=%v err=%v", mintedA, err)
	}
	rB, mintedB, err := s.MintConditionForAgent(ctx, "bob", meta, chain, "y", time.Now().Add(time.Minute))
	if err != nil || !mintedB {
		t.Fatalf("bob mint: minted=%v err=%v", mintedB, err)
	}
	if !mintedB {
		t.Error("bob should mint independently of alice's open row")
	}
	if rB.TargetAgent != "bob" {
		t.Errorf("bob row TargetAgent = %q", rB.TargetAgent)
	}
}

// ----- Exhaustive negative-transition matrix (dual-review IMPORTANT #8) -----
// fired / cleared / cancelled are terminal. All five mutation ops must
// reject when targeting a terminal row (3 states × 5 ops = 15 cases).
func TestStore_TerminalTransitions_RejectAllMutations(t *testing.T) {
	terminal := []State{StateFired, StateCleared, StateCancelled}
	ops := map[string]func(*SQLStore, string) error{
		"Defer":        func(s *SQLStore, id string) error { return s.Defer(ctx, id, time.Now().Add(time.Hour), "tester") },
		"Clear":        func(s *SQLStore, id string) error { return s.Clear(ctx, id, "tester") },
		"Cancel":       func(s *SQLStore, id string) error { return s.Cancel(ctx, id, "tester") },
		"Fire":         func(s *SQLStore, id string) error { return s.Fire(ctx, id, time.Now()) },
		"FireAndRearm": func(s *SQLStore, id string) error { return s.FireAndRearm(ctx, id, time.Now(), time.Now().Add(time.Hour)) },
	}
	for _, term := range terminal {
		for opName, op := range ops {
			t.Run(fmt.Sprintf("%s_on_%s", opName, term), func(t *testing.T) {
				s := newTestStore(t)
				id := mintOpenTime(t, s)
				forceState(t, s, id, term)
				err := op(s, id)
				if err == nil {
					t.Fatalf("expected error transitioning %s from %s", opName, term)
				}
				// All 15 cases should surface ErrTerminalState specifically
				// (Fire/FireAndRearm check state BEFORE trigger_kind so the
				// terminal-state guard fires first).
				if !errors.Is(err, ErrTerminalState) {
					t.Errorf("expected ErrTerminalState, got %v", err)
				}
			})
		}
	}
}
