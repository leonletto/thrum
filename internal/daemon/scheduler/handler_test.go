package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Compile-time assertions that the substrate's stateReporter satisfies
// StateReporter. The handlers-package compile-time pin for CommandHandler /
// ThrumCommandHandler lives in those packages' test files (importing
// scheduler from there is one-way; pinning here would create a cycle).
var _ StateReporter = (*stateReporter)(nil)

// TestStateReporter_Transition_IsAtomic pins spec §8.4.2: the state-row
// update and the event-log INSERT land in a single SQLite transaction. We
// can't easily simulate a mid-transaction crash, but we can verify that
// the public API behaviour writes BOTH rows in lockstep and that they
// reach consistent post-call values.
func TestStateReporter_Transition_IsAtomic(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()
	now := time.Now()
	if err := store.UpsertState(ctx, &StateRow{
		JobID:        "test-job",
		Generation:   1,
		CurrentState: StateRunning,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	reporter := &stateReporter{store: store, jobID: "test-job", runID: "rid"}
	if err := reporter.Transition(StateFailed, "boom", map[string]any{"k": "v"}); err != nil {
		t.Fatalf("transition: %v", err)
	}

	row, err := store.GetState(ctx, "test-job")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if row.CurrentState != StateFailed {
		t.Errorf("state not updated: %q", row.CurrentState)
	}
	if row.ConsecutiveFailures != 1 {
		t.Errorf("consecutive_failures = %d; want 1", row.ConsecutiveFailures)
	}
	if row.LastError != "boom" {
		t.Errorf("last_error = %q; want %q", row.LastError, "boom")
	}

	events, err := store.RecentEvents(ctx, "test-job", 10)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	if len(events) == 0 || events[0].ToState != StateFailed {
		t.Error("event not appended in same transaction as state update")
	}
	if events[0].Details["k"] != "v" {
		t.Errorf("event details not propagated: %v", events[0].Details)
	}
}

// TestStateReporter_EscalationMarkerSuppresses pins canonical §6.3
// read-side: a StateFailed transition with details["escalation_emitted_by"]
// matching `b-b1.*` sets escalation_sent=true on the state row so A-B1's
// own emit path can short-circuit. Critically, consecutive_failures is
// STILL incremented — the marker only suppresses the redundant escalation
// emit, not the failure accounting (§8.4.5 invariant).
func TestStateReporter_EscalationMarkerSuppresses(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()
	now := time.Now()
	if err := store.UpsertState(ctx, &StateRow{
		JobID: "agent-x", Generation: 1, CurrentState: StateRunning,
		ConsecutiveFailures: 2, // pre-existing accounting from prior failures
		CreatedAt:           now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reporter := &stateReporter{store: store, jobID: "agent-x", runID: "rid"}
	err := reporter.Transition(StateFailed, "idle nudge exhausted", map[string]any{
		"escalation_emitted_by": "b-b1.idle_nudge",
		"max_idle_nudges":       5,
	})
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	row, _ := store.GetState(ctx, "agent-x")
	if !row.EscalationSent {
		t.Error("escalation_sent should be true when B-B1 emit marker is present")
	}
	if row.ConsecutiveFailures != 3 {
		t.Errorf("consecutive_failures = %d; want 3 (marker suppresses emit, NOT accounting)", row.ConsecutiveFailures)
	}
	if row.CurrentState != StateFailed {
		t.Errorf("state = %q; want failed", row.CurrentState)
	}

	// Non-matching prefix: marker should NOT set escalation_sent.
	if err := store.UpsertState(ctx, &StateRow{
		JobID: "agent-y", Generation: 1, CurrentState: StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed agent-y: %v", err)
	}
	reporter2 := &stateReporter{store: store, jobID: "agent-y", runID: "rid"}
	_ = reporter2.Transition(StateFailed, "other failure", map[string]any{
		"escalation_emitted_by": "some-other-source.event",
	})
	row, _ = store.GetState(ctx, "agent-y")
	if row.EscalationSent {
		t.Error("escalation_sent must NOT be set for non-b-b1 markers")
	}
}

// TestStateReporter_NoMarker_NoSuppression: a failure with no details map
// (or with details that lack escalation_emitted_by) leaves escalation_sent
// at its prior value (false on first failure). consecutive_failures still
// increments — the readback only affects the escalation flag.
func TestStateReporter_NoMarker_NoSuppression(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()
	now := time.Now()
	if err := store.UpsertState(ctx, &StateRow{
		JobID: "agent-z", Generation: 1, CurrentState: StateRunning,
		ConsecutiveFailures: 0, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reporter := &stateReporter{store: store, jobID: "agent-z", runID: "rid"}
	if err := reporter.Transition(StateFailed, "handler error", nil); err != nil {
		t.Fatalf("transition: %v", err)
	}
	row, _ := store.GetState(ctx, "agent-z")
	if row.EscalationSent {
		t.Error("escalation_sent should be false without b-b1.* marker")
	}
	if row.ConsecutiveFailures != 1 {
		t.Errorf("consecutive_failures = %d; want 1", row.ConsecutiveFailures)
	}
}

// TestStateReporter_CompletedClearsEscalationFlag: per §6.3 / §8.4.5, a
// successful run resets the escalation state — escalation_sent goes back
// to false, consecutive_failures back to 0, last_error cleared.
func TestStateReporter_CompletedClearsEscalationFlag(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()
	now := time.Now()
	if err := store.UpsertState(ctx, &StateRow{
		JobID: "agent-r", Generation: 1, CurrentState: StateRunning,
		ConsecutiveFailures: 3, EscalationSent: true,
		LastError: "prior failure", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reporter := &stateReporter{store: store, jobID: "agent-r", runID: "rid"}
	if err := reporter.Transition(StateCompleted, "recovered", nil); err != nil {
		t.Fatalf("transition: %v", err)
	}
	row, _ := store.GetState(ctx, "agent-r")
	if row.EscalationSent {
		t.Error("escalation_sent should reset on completion")
	}
	if row.ConsecutiveFailures != 0 {
		t.Errorf("consecutive_failures = %d; want 0 after completion", row.ConsecutiveFailures)
	}
	if row.LastError != "" {
		t.Errorf("last_error = %q; want empty after completion", row.LastError)
	}
}

// TestStateReporter_TerminalRollbackOnEventError verifies that if the
// event-log INSERT fails inside the transaction, the state-row UPDATE
// also rolls back. We force the failure by stuffing a Details value
// json.Marshal cannot handle (a channel).
func TestStateReporter_TerminalRollbackOnEventError(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()
	now := time.Now()
	if err := store.UpsertState(ctx, &StateRow{
		JobID: "rb", Generation: 1, CurrentState: StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reporter := &stateReporter{store: store, jobID: "rb", runID: "rid"}
	err := reporter.Transition(StateCompleted, "ok", map[string]any{
		"unmarshalable": make(chan int), // json.Marshal will fail on this
	})
	if err == nil {
		t.Fatal("expected json.Marshal error to propagate")
	}

	// State row must remain in StateRunning — the transaction rolled back.
	row, err := store.GetState(ctx, "rb")
	if err != nil {
		t.Fatalf("get state post-rollback: %v", err)
	}
	if row.CurrentState != StateRunning {
		t.Errorf("state = %q after failed transition; want still %q (rollback)", row.CurrentState, StateRunning)
	}
	events, _ := store.RecentEvents(ctx, "rb", 10)
	if len(events) != 0 {
		t.Errorf("event count = %d after rollback; want 0", len(events))
	}
}

// TestStateReporter_Stage_AtomicWithEvent pins the same single-transaction
// behaviour for Stage(name): the stage marker write and the stage-entry
// event must commit together.
func TestStateReporter_Stage_AtomicWithEvent(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()
	now := time.Now()
	if err := store.UpsertState(ctx, &StateRow{
		JobID: "stg", Generation: 1, CurrentState: StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reporter := &stateReporter{store: store, jobID: "stg", runID: "rid"}
	if err := reporter.Stage("executing"); err != nil {
		t.Fatalf("stage: %v", err)
	}

	row, err := store.GetState(ctx, "stg")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if row.CurrentStage != "executing" {
		t.Errorf("current_stage = %q; want %q", row.CurrentStage, "executing")
	}
	if row.StageEnteredAt == nil {
		t.Error("stage_entered_at not set")
	}

	events, err := store.RecentEvents(ctx, "stg", 10)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no event recorded for stage entry")
	}
	if events[0].Reason != "stage: executing" {
		t.Errorf("event reason = %q; want %q", events[0].Reason, "stage: executing")
	}
}

// TestIsTerminal pins the canonical terminal-state set; downstream
// consumers (E1.4 RPC handlers, A-B4 stalled-sweep) read this through
// the public state vocabulary.
func TestIsTerminal(t *testing.T) {
	cases := map[State]bool{
		StateScheduled:          false,
		StateDispatched:         false,
		StateRunning:            false,
		StateCompleted:          true,
		StateFailed:             true,
		StateCancelled:          true,
		StateOverBudget:         true,
		StateOverlappingSkipped: true,
		StateDisabled:           false,
	}
	for s, want := range cases {
		if got := isTerminal(s); got != want {
			t.Errorf("isTerminal(%q) = %v; want %v", s, got, want)
		}
	}
}

// TestSentinelErrors_Distinct verifies the sentinels do not silently
// collapse into one identity — downstream `errors.Is` checks rely on
// distinctness.
func TestSentinelErrors_Distinct(t *testing.T) {
	cases := []error{
		ErrUnknownRun,
		ErrCompletionAlreadyDelivered,
		ErrJobActive,
		ErrLostTrack,
	}
	for i, a := range cases {
		for j, b := range cases {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel %v and %v collapsed", a, b)
			}
		}
	}
}
