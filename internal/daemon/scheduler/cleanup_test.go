package scheduler

import (
	"context"
	"testing"
	"time"
)

// TestCleanupHandler_PrunesOldEvents: events older than retentionDays
// are deleted; younger events survive.
func TestCleanupHandler_PrunesOldEvents(t *testing.T) {
	db := setupStateTestDB(t)
	store := NewStateStore(db)
	ctx := context.Background()
	now := time.Now()

	// 5 old events (10 days ago) + 3 recent (1 day ago).
	for i := 0; i < 5; i++ {
		if err := store.AppendEvent(ctx, &Event{
			JobID: "test-job", RunID: "run-old",
			EventTime: now.Add(-10 * 24 * time.Hour),
			FromState: "", ToState: StateDispatched, Reason: "old",
		}); err != nil {
			t.Fatalf("append old #%d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := store.AppendEvent(ctx, &Event{
			JobID: "test-job", RunID: "run-new",
			EventTime: now.Add(-1 * 24 * time.Hour),
			FromState: "", ToState: StateDispatched, Reason: "new",
		}); err != nil {
			t.Fatalf("append new #%d: %v", i, err)
		}
	}

	h := NewCleanupHandler(store, 7) // 7-day retention
	if err := h.runOnce(ctx); err != nil {
		t.Fatalf("runOnce: %v", err)
	}

	events, err := store.RecentEvents(ctx, "test-job", 100)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("after cleanup: %d events; want 3", len(events))
	}
	for _, e := range events {
		if e.Reason == "old" {
			t.Errorf("old event survived cleanup: %v", e)
		}
	}
}

// TestCleanupHandler_DefaultRetention: non-positive retention argument
// clamps to the canonical 7-day default.
func TestCleanupHandler_DefaultRetention(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	for _, ret := range []int{0, -1, -100} {
		h := NewCleanupHandler(store, ret)
		if h.retentionDays != 7 {
			t.Errorf("retentionDays = %d for input %d; want 7", h.retentionDays, ret)
		}
	}
	// Positive value is honored as-is.
	h := NewCleanupHandler(store, 30)
	if h.retentionDays != 30 {
		t.Errorf("retentionDays = %d; want 30", h.retentionDays)
	}
}

// TestCleanupHandler_RegistersViaRegisterInternal: cleanup handler
// composes with the substrate's RegisterInternal contract — the
// canonical `internal.scheduler_event_cleanup` id passes both the
// internal-prefix and kebab-case shape guards.
func TestCleanupHandler_RegistersViaRegisterInternal(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	h := NewCleanupHandler(s.state, 7)
	s.RegisterInternal("internal.scheduler_event_cleanup", "@daily", InternalOpts{
		RunAtStart: false, CatchUp: "skip",
	}, h)

	spec, ok := s.JobSpec("internal.scheduler_event_cleanup")
	if !ok {
		t.Fatal("not registered")
	}
	if spec.Type != "internal" {
		t.Errorf("type = %q; want internal", spec.Type)
	}
	if spec.Schedule != "@daily" {
		t.Errorf("schedule = %q; want @daily", spec.Schedule)
	}
}

// TestCleanupHandler_DispatchTransitions: full Dispatch path drives the
// state-machine from dispatched → running → completed through the
// stateReporter, and prunes the appropriate rows.
func TestCleanupHandler_DispatchTransitions(t *testing.T) {
	db := setupStateTestDB(t)
	store := NewStateStore(db)
	ctx := context.Background()
	now := time.Now()

	// Seed: 2 old events + 2 new.
	for i := 0; i < 2; i++ {
		_ = store.AppendEvent(ctx, &Event{
			JobID: "internal.scheduler_event_cleanup", RunID: "r-old",
			EventTime: now.Add(-30 * 24 * time.Hour),
			FromState: "", ToState: StateDispatched, Reason: "old",
		})
	}
	for i := 0; i < 2; i++ {
		_ = store.AppendEvent(ctx, &Event{
			JobID: "internal.scheduler_event_cleanup", RunID: "r-new",
			EventTime: now.Add(-1 * time.Hour),
			FromState: "", ToState: StateDispatched, Reason: "new",
		})
	}
	// Seed the state row so the reporter has something to update.
	if err := store.UpsertState(ctx, &StateRow{
		JobID: "internal.scheduler_event_cleanup", Generation: 1,
		CurrentState: StateDispatched, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	reporter := &stateReporter{store: store, jobID: "internal.scheduler_event_cleanup", runID: "r-test"}
	h := NewCleanupHandler(store, 7)
	if err := h.Dispatch(ctx, JobSpec{ID: "internal.scheduler_event_cleanup", Type: "internal"}, "r-test", reporter, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	row, err := store.GetState(ctx, "internal.scheduler_event_cleanup")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if row.CurrentState != StateCompleted {
		t.Errorf("post-dispatch state = %q; want completed", row.CurrentState)
	}
	// Only the 2 recent events survive; 2 old were pruned.
	events, _ := store.RecentEvents(ctx, "internal.scheduler_event_cleanup", 100)
	// Note: the dispatch path itself appends transition events (running,
	// stage:pruning, completed). Count the original-content events only.
	originals := 0
	for _, e := range events {
		if e.Reason == "old" || e.Reason == "new" {
			originals++
		}
	}
	if originals != 2 {
		t.Errorf("originals surviving = %d; want 2 (the 2 'new' events)", originals)
	}
}

// TestCleanupHandler_Reconcile_AlwaysCompleted: reconcile is idempotent;
// regardless of prior state, returns StateCompleted with no error.
func TestCleanupHandler_Reconcile_AlwaysCompleted(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	h := NewCleanupHandler(store, 7)
	for _, prior := range []State{StateRunning, StateDispatched, StateFailed} {
		got, err := h.Reconcile(context.Background(), JobSpec{}, "rid", prior)
		if err != nil {
			t.Errorf("reconcile %q: %v", prior, err)
		}
		if got != StateCompleted {
			t.Errorf("reconcile %q: got %q; want completed", prior, got)
		}
	}
}
