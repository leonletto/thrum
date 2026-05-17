package agentdispatch_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/agentdispatch"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/schema"
)

// newLifecycleStore builds an in-memory store backed by a fully-migrated
// SQLite DB — mirrors the test pattern established in
// internal/daemon/state/agent_lifecycle_test.go.
func newLifecycleStore(t *testing.T) state.AgentLifecycleStore {
	t.Helper()
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return state.NewAgentLifecycleStore(safedb.New(db))
}

// stubReporter records every Transition + Stage call so tests can pin
// the canonical Running → Completed flow without booting a real
// scheduler. Mirrors the spying-handler pattern used by A-B1's
// reactor tests.
type stubReporter struct {
	transitions []scheduler.State
	reasons     []string
	stages      []string
	failErr     error // when set, every method returns this error
}

func (r *stubReporter) Transition(to scheduler.State, reason string, _ map[string]any) error {
	if r.failErr != nil {
		return r.failErr
	}
	r.transitions = append(r.transitions, to)
	r.reasons = append(r.reasons, reason)
	return nil
}

func (r *stubReporter) Stage(name string) error {
	if r.failErr != nil {
		return r.failErr
	}
	r.stages = append(r.stages, name)
	return nil
}

// TestCleanupHandler_PrunesOlderThanRetention pins the handler's
// load-bearing contract: rows with event_time older than retentionDays
// are deleted; rows within the window survive.
func TestCleanupHandler_PrunesOlderThanRetention(t *testing.T) {
	store := newLifecycleStore(t)
	ctx := context.Background()
	now := time.Now()

	// 3 stale (8/9/10 days ago) + 2 fresh (1h ago + 6 days ago).
	for _, d := range []time.Duration{
		-8 * 24 * time.Hour,
		-9 * 24 * time.Hour,
		-10 * 24 * time.Hour,
		-1 * time.Hour,
		-6 * 24 * time.Hour,
	} {
		if _, err := store.Append(ctx, state.AgentLifecycleEvent{
			AgentName: "x",
			EventKind: state.EventCrashDetected,
			EventTime: now.Add(d),
		}); err != nil {
			t.Fatalf("append at %v: %v", d, err)
		}
	}

	h := agentdispatch.NewCleanupHandler(store, 7)
	rep := &stubReporter{}
	if err := h.Dispatch(ctx, scheduler.JobSpec{}, "test-run", rep, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	events, err := store.ListByAgent(ctx, "x", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 remaining, got %d", len(events))
	}
}

// TestCleanupHandler_DefaultRetentionClampsNonPositive pins canonical
// §6.3 + Q-Spec-3: non-positive retentionDays clamps to 7 at construct
// time so the on-disk default (zero) yields the documented 7-day
// behavior without an operator-visible value.
func TestCleanupHandler_DefaultRetentionClampsNonPositive(t *testing.T) {
	store := newLifecycleStore(t)
	ctx := context.Background()
	now := time.Now()

	// Plant one stale event (8 days old) and one fresh (6 days old).
	// retentionDays=0 must clamp to 7, so the 8-day-old is pruned and
	// the 6-day-old survives.
	for _, d := range []time.Duration{-8 * 24 * time.Hour, -6 * 24 * time.Hour} {
		if _, err := store.Append(ctx, state.AgentLifecycleEvent{
			AgentName: "y",
			EventKind: state.EventCrashDetected,
			EventTime: now.Add(d),
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	for _, retention := range []int{0, -1, -100} {
		h := agentdispatch.NewCleanupHandler(store, retention)
		rep := &stubReporter{}
		if err := h.Dispatch(ctx, scheduler.JobSpec{}, "test-run", rep, nil); err != nil {
			t.Fatalf("dispatch retention=%d: %v", retention, err)
		}
	}

	events, err := store.ListByAgent(ctx, "y", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// After the prune the stale row is gone; the fresh row survives.
	if len(events) != 1 {
		t.Errorf("expected 1 surviving row after clamped prune, got %d", len(events))
	}
}

// TestCleanupHandler_TransitionsAreCanonical pins the happy-path state
// machine: Running → Completed with a single "pruning" stage. This is
// the contract the A-B1 reactor expects so the scheduler_job_state
// row reaches a terminal value and the next tick re-arms cleanly.
func TestCleanupHandler_TransitionsAreCanonical(t *testing.T) {
	store := newLifecycleStore(t)
	h := agentdispatch.NewCleanupHandler(store, 7)
	rep := &stubReporter{}
	if err := h.Dispatch(context.Background(), scheduler.JobSpec{}, "r1", rep, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(rep.transitions) != 2 ||
		rep.transitions[0] != scheduler.StateRunning ||
		rep.transitions[1] != scheduler.StateCompleted {
		t.Errorf("transitions = %v; want [Running Completed]", rep.transitions)
	}
	if len(rep.stages) != 1 || rep.stages[0] != "pruning" {
		t.Errorf("stages = %v; want [pruning]", rep.stages)
	}
}

// TestCleanupHandler_FailedReporter_PropagatesError pins error-path
// behavior: if the reporter itself fails, Dispatch returns that error
// unwrapped so the scheduler treats the run as a failure (rather than
// silently committing to a half-state).
func TestCleanupHandler_FailedReporter_PropagatesError(t *testing.T) {
	store := newLifecycleStore(t)
	h := agentdispatch.NewCleanupHandler(store, 7)
	want := errors.New("reporter offline")
	rep := &stubReporter{failErr: want}
	got := h.Dispatch(context.Background(), scheduler.JobSpec{}, "r1", rep, nil)
	if !errors.Is(got, want) {
		t.Errorf("Dispatch err = %v; want %v", got, want)
	}
}

// TestCleanupHandler_Reconcile_IsIdempotent confirms Reconcile reports
// Completed unconditionally — boot-time recovery has nothing to undo
// because PruneOlderThan never enters a partial state (single DELETE
// statement, transactionally safe).
func TestCleanupHandler_Reconcile_IsIdempotent(t *testing.T) {
	h := agentdispatch.NewCleanupHandler(newLifecycleStore(t), 7)
	st, err := h.Reconcile(context.Background(), scheduler.JobSpec{}, "r1", scheduler.StateRunning)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if st != scheduler.StateCompleted {
		t.Errorf("Reconcile state = %q; want %q", st, scheduler.StateCompleted)
	}
}

// TestCleanupHandler_Stages declares a single "pruning" stage with a
// non-trivial budget so A-B4's stalled-sweep (when wired) knows when
// to nudge. Drift here that drops the stage or yields a zero duration
// would silently disable nudging for this job.
func TestCleanupHandler_Stages(t *testing.T) {
	h := agentdispatch.NewCleanupHandler(newLifecycleStore(t), 7)
	stages := h.Stages()
	dur, ok := stages["pruning"]
	if !ok {
		t.Fatalf("Stages missing 'pruning' key: %v", stages)
	}
	if dur <= 0 {
		t.Errorf("pruning stage duration = %v; want positive", dur)
	}
}

// TestDispatch_RespectsContextCancellation pins brainstormer-third I2:
// a cancelled context propagates cleanly through Dispatch's storage
// path. SQLite's ExecContext honors ctx.Done(); on cancel the prune
// fails and the handler reports StateFailed (not StateCompleted) so
// the scheduler audit trail reflects what actually happened.
func TestDispatch_RespectsContextCancellation(t *testing.T) {
	store := newLifecycleStore(t)
	h := agentdispatch.NewCleanupHandler(store, 7)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so the first DB op observes Done

	rep := &stubReporter{}
	err := h.Dispatch(ctx, scheduler.JobSpec{}, "r-cancel", rep, nil)
	if err != nil {
		// Dispatch returns the reporter.Transition(Failed) call's error;
		// our stub returns nil so a successful "I marked it failed"
		// surfaces as nil here. That's the canonical pattern from
		// A-B1's cleanup handler.
		t.Fatalf("Dispatch: unexpected outer err=%v", err)
	}

	// Sequence pins the cancellation path:
	//   Transition(Running) → Stage(pruning) → runOnce fails on cancelled ctx
	//   → Transition(Failed, "prune error: ...").
	// We expect at least 2 transitions: Running then Failed.
	if len(rep.transitions) < 2 {
		t.Fatalf("transitions=%v; want at least [Running, Failed]", rep.transitions)
	}
	if rep.transitions[0] != scheduler.StateRunning {
		t.Errorf("transitions[0]=%q; want Running", rep.transitions[0])
	}
	last := rep.transitions[len(rep.transitions)-1]
	if last != scheduler.StateFailed {
		t.Errorf("final transition=%q; want Failed (ctx cancelled)", last)
	}
	if !strings.Contains(rep.reasons[len(rep.reasons)-1], "prune error") {
		t.Errorf("final reason=%q; want substring 'prune error'", rep.reasons[len(rep.reasons)-1])
	}
}
