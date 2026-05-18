package agentdispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/schema"
)

// Layer-D escalation round-trip test per plan §Task 37. Pins the
// cross-epic invariant: when the B-B1 idle-nudge loop exhausts,
// the escalation_emitted_by="b-b1.idle_nudge" marker propagates
// through the scheduler's state writer into scheduler_job_state
// so A-B1's evaluator-side suppression engages on the next
// reactor tick.
//
// Scope clarification: this test exercises BOTH halves of the
// contract in one shot —
//   (a) loop.onTimerFire emits the right details map (also covered
//       in isolation by idle_nudge_test.go)
//   (b) the marker-translation logic in scheduler.stateReporter
//       (lived at internal/daemon/scheduler/handler.go) sets the
//       EscalationSent flag (also covered by scheduler/handler_test.go)
//
// The real scheduler.stateReporter has unexported fields and a
// package-private constructor, so this test wires a mirror reporter
// (stateStoreRoundtripReporter) that replicates the marker-handling
// logic against a real scheduler.StateStore. If scheduler's reporter
// ever adds new marker sources beyond "b-b1.*", the mirror MUST
// update in lockstep — scheduler/handler_test.go is the authoritative
// pin for that logic; this test is the agentdispatch-side pin.

// stateStoreRoundtripReporter mirrors scheduler.stateReporter's
// marker-handling logic against a real scheduler.StateStore.
// Constructed for this test only — production code wires through
// scheduler.RegisterRun + the reactor's stateReporter.
type stateStoreRoundtripReporter struct {
	store *scheduler.StateStore
	jobID string
	runID string
}

func (r *stateStoreRoundtripReporter) Transition(to scheduler.State, reason string, details map[string]any) error {
	ctx := context.Background()
	existing, err := r.store.GetState(ctx, r.jobID)
	if err != nil || existing == nil {
		return fmt.Errorf("no state row for %s: %v", r.jobID, err)
	}
	now := time.Now()
	fromState := existing.CurrentState
	newRow := *existing
	newRow.CurrentState = to
	newRow.UpdatedAt = now

	// Mirror of scheduler.stateReporter.Transition from
	// internal/daemon/scheduler/handler.go. The marker translation
	// is the load-bearing line for Layer-D suppression.
	//
	// SCOPE NOTE: this mirror is StateFailed-ONLY. The real
	// scheduler.stateReporter has additional bookkeeping on
	// StateCompleted (resets ConsecutiveFailures, EscalationSent,
	// LastError) — that branch is NOT replicated here because
	// Layer-D escalation always ends in StateFailed, never
	// Completed. A future test that walks a Completed path
	// through this mirror would see incorrect retained
	// ConsecutiveFailures + EscalationSent state. Two ways to
	// extend safely:
	//   (a) Replicate the StateCompleted reset block here from
	//       handler.go before adding the new test, OR
	//   (b) Move the new test into the scheduler package as a
	//       *_test.go file so it can construct the real
	//       stateReporter directly (which has package-private
	//       fields + constructor).
	// Either path keeps the round-trip honest; choose based on
	// whether the new test crosses the agentdispatch boundary
	// (use (a)) or stays scheduler-internal (use (b)).
	switch to {
	case scheduler.StateCompleted, scheduler.StateFailed, scheduler.StateCancelled, scheduler.StateOverBudget:
		newRow.LastCompletedAt = &now
		newRow.LastCompletionState = to
		if to == scheduler.StateFailed {
			newRow.ConsecutiveFailures = existing.ConsecutiveFailures + 1
			if reason != "" {
				newRow.LastError = reason
			}
			if details != nil {
				if marker, ok := details["escalation_emitted_by"].(string); ok && strings.HasPrefix(marker, "b-b1.") {
					newRow.EscalationSent = true
				}
			}
		}
	}

	event := &scheduler.Event{
		JobID:     r.jobID,
		RunID:     r.runID,
		EventTime: now,
		FromState: fromState,
		ToState:   to,
		Reason:    reason,
		Details:   details,
	}
	return r.store.UpsertStateAndEvent(ctx, &newRow, event)
}

func (r *stateStoreRoundtripReporter) Stage(_ string) error {
	// Stage marker writes touch only current_stage + stage_entered_at
	// — not load-bearing for the Layer-D round-trip. No-op here so
	// the test focuses on the transition semantics.
	return nil
}

// setupRoundtripDB builds the in-memory SQLite DB the round-trip
// test runs against. Same pattern as scheduler/state_test.go's
// setupStateTestDB but co-located here so the test is single-file.
func setupRoundtripDB(t *testing.T) *safedb.DB {
	t.Helper()
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return safedb.New(db)
}

// TestLayerDEscalation_MarkerSetsEscalationSentFlag pins the
// agentdispatch-side of the Layer-D round-trip: idleNudgeLoop's
// exhaustion path emits a StateFailed transition with the
// canonical escalation_emitted_by="b-b1.idle_nudge" marker, the
// mirror reporter applies the same translation logic the real
// scheduler.stateReporter uses, and the resulting scheduler_job_state
// row carries escalation_sent=1.
//
// A-B1's evaluator (in scheduler's reactor loop) reads
// EscalationSent before firing its own consecutive-failure
// escalation; with the flag set, that fire is suppressed. The
// evaluator behavior is pinned by scheduler/handler_test.go's
// existing tests; this test pins the input that gates them.
func TestLayerDEscalation_MarkerSetsEscalationSentFlag(t *testing.T) {
	db := setupRoundtripDB(t)
	store := scheduler.NewStateStore(db)
	ctx := context.Background()

	// Seed an initial running state row so the mirror reporter
	// has something to update.
	now := time.Now()
	if err := store.UpsertState(ctx, &scheduler.StateRow{
		JobID:        "docs_bot",
		Generation:   1,
		CurrentState: scheduler.StateRunning,
		LastRunID:    "run-rt-1",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	// Drive the idle-nudge loop to exhaustion in a single fire by
	// setting maxNudges=1 + a silent-pane probe. The first fire
	// hits nudgesFired=1 which equals maxNudges → Layer-D.
	silentProbe := func(_ context.Context, _ string) (time.Time, error) {
		return time.Now().Add(-2 * time.Hour), nil
	}
	loop, _, router := newTestIdleNudgeLoop(silentProbe)
	defer loop.timer.Stop()
	loop.maxNudges = 1
	loop.runID = "run-rt-1"

	rep := &stateStoreRoundtripReporter{store: store, jobID: "docs_bot", runID: "run-rt-1"}

	err := loop.onTimerFire(ctx, rep)
	if !errors.Is(err, ErrIdleNudgeExhausted) {
		t.Fatalf("onTimerFire err = %v; want wraps ErrIdleNudgeExhausted", err)
	}

	// Assertion (1): loop fired Layer-D exactly once.
	if len(router.calls) != 1 {
		t.Errorf("Route calls = %d; want exactly 1 (one Layer-D fire from B-B1)", len(router.calls))
	}
	if len(router.calls) > 0 && router.calls[0].alert.Source != "b-b1.idle_nudge" {
		t.Errorf("Route alert.Source = %q; want b-b1.idle_nudge", router.calls[0].alert.Source)
	}

	// Assertion (2): scheduler_job_state row reflects the round-trip.
	// EscalationSent=true is the input A-B1's evaluator reads to
	// suppress its own escalation; ConsecutiveFailures still
	// increments (suppression is on the alert, not the bookkeeping).
	row, err := store.GetState(ctx, "docs_bot")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !row.EscalationSent {
		t.Errorf("EscalationSent = false; want true (b-b1.idle_nudge marker should trip the flag)")
	}
	if row.ConsecutiveFailures != 1 {
		t.Errorf("ConsecutiveFailures = %d; want 1 (bookkeeping still increments under suppression)", row.ConsecutiveFailures)
	}
	if row.CurrentState != scheduler.StateFailed {
		t.Errorf("CurrentState = %q; want StateFailed", row.CurrentState)
	}
	if !strings.Contains(row.LastError, "idle nudge exhausted") {
		t.Errorf("LastError = %q; want canonical 'idle nudge exhausted' substring", row.LastError)
	}
}

// TestLayerDEscalation_MarkerRemoval_BreaksSuppression is the
// negative-control test per plan §Task 37 Step 2 ("verify test
// fails when the marker is REMOVED"). The test goes through the
// SAME round-trip but with a synthesized loop that emits no
// marker (simulating a regression that drops the canonical
// details key). Result: EscalationSent stays false — so A-B1's
// evaluator would fire its own escalation on the next tick,
// producing the double-escalation the marker exists to prevent.
//
// This proves the test actually exercises the cross-epic contract
// rather than just observing a happy-coincidence flag state.
func TestLayerDEscalation_MarkerRemoval_BreaksSuppression(t *testing.T) {
	db := setupRoundtripDB(t)
	store := scheduler.NewStateStore(db)
	ctx := context.Background()

	now := time.Now()
	if err := store.UpsertState(ctx, &scheduler.StateRow{
		JobID:        "docs_bot",
		Generation:   1,
		CurrentState: scheduler.StateRunning,
		LastRunID:    "run-rt-2",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	rep := &stateStoreRoundtripReporter{store: store, jobID: "docs_bot", runID: "run-rt-2"}

	// Direct call simulating a regression: StateFailed transition
	// WITHOUT the escalation_emitted_by marker.
	if err := rep.Transition(scheduler.StateFailed, "idle nudge exhausted", map[string]any{
		"nudges_fired": 1,
		// NO escalation_emitted_by → marker absent → suppression breaks.
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}

	row, err := store.GetState(ctx, "docs_bot")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if row.EscalationSent {
		t.Error("EscalationSent = true with no marker; suppression would fire incorrectly")
	}
	// Bookkeeping still increments (consistent with the happy path).
	if row.ConsecutiveFailures != 1 {
		t.Errorf("ConsecutiveFailures = %d; want 1", row.ConsecutiveFailures)
	}
}
