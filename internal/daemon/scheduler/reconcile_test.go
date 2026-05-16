package scheduler

import (
	"context"
	"testing"
	"time"
)

// lostTrackHandler simulates a handler that lost its child process across
// a daemon restart.
type lostTrackHandler struct{}

func (l *lostTrackHandler) Dispatch(_ context.Context, _ JobSpec, _ string, _ StateReporter, _ <-chan *Completion) error {
	return nil
}
func (l *lostTrackHandler) Reconcile(_ context.Context, _ JobSpec, _ string, _ State) (State, error) {
	return "", ErrLostTrack
}
func (l *lostTrackHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"executing": time.Minute}
}

// reconcileToCompletedHandler simulates a handler whose offline run
// actually finished — the row's `running` state can be advanced to
// `completed` at boot.
type reconcileToCompletedHandler struct{}

func (r *reconcileToCompletedHandler) Dispatch(_ context.Context, _ JobSpec, _ string, _ StateReporter, _ <-chan *Completion) error {
	return nil
}
func (r *reconcileToCompletedHandler) Reconcile(_ context.Context, _ JobSpec, _ string, _ State) (State, error) {
	return StateCompleted, nil
}
func (r *reconcileToCompletedHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"executing": time.Minute}
}

// TestReconcile_NonTerminalRows_MarkLostWhenHandlerLostTrack: a non-terminal
// state row with a registered handler that returns ErrLostTrack must be
// transitioned to StateFailed with the canonical reason from spec §8.4.3.
func TestReconcile_NonTerminalRows_MarkLostWhenHandlerLostTrack(t *testing.T) {
	db := setupStateTestDB(t)
	store := NewStateStore(db)
	ctx := context.Background()
	now := time.Unix(1747353600, 0)

	nextFire := now.Add(time.Minute)
	if err := store.UpsertState(ctx, &StateRow{
		JobID: "internal.lost", Generation: 1, CurrentState: StateRunning,
		LastRunID: "internal.lost-g1-1747353500",
		CreatedAt: now, UpdatedAt: now, NextScheduledAt: &nextFire,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()
	s.RegisterInternal("internal.lost", "@every 5m", InternalOpts{}, &lostTrackHandler{})

	if err := s.ReconcileBoot(ctx); err != nil {
		t.Fatalf("reconcile boot: %v", err)
	}

	row, err := store.GetState(ctx, "internal.lost")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if row.CurrentState != StateFailed {
		t.Errorf("state = %q; want failed", row.CurrentState)
	}
	if row.LastError != "lost across daemon restart" {
		t.Errorf("last_error = %q; want canonical §8.4.3 reason string", row.LastError)
	}
}

// TestReconcile_NonTerminalRows_RecoverWhenHandlerKnows: a handler that
// returns a definitive state from Reconcile advances the row to that state.
func TestReconcile_NonTerminalRows_RecoverWhenHandlerKnows(t *testing.T) {
	db := setupStateTestDB(t)
	store := NewStateStore(db)
	ctx := context.Background()
	now := time.Unix(1747353600, 0)

	if err := store.UpsertState(ctx, &StateRow{
		JobID: "internal.recover", Generation: 1, CurrentState: StateRunning,
		LastRunID: "internal.recover-g1-1747353500",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()
	s.RegisterInternal("internal.recover", "@every 5m", InternalOpts{}, &reconcileToCompletedHandler{})

	if err := s.ReconcileBoot(ctx); err != nil {
		t.Fatalf("reconcile boot: %v", err)
	}
	row, err := store.GetState(ctx, "internal.recover")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if row.CurrentState != StateCompleted {
		t.Errorf("state = %q; want completed", row.CurrentState)
	}
}

// TestReconcile_SkipsRowsWithoutRegisteredHandler: rows whose handler isn't
// registered yet (bridge-owned internal jobs at boot) are left alone.
// Task 21 wires the per-handler-registration reconcile that picks them up.
func TestReconcile_SkipsRowsWithoutRegisteredHandler(t *testing.T) {
	db := setupStateTestDB(t)
	store := NewStateStore(db)
	ctx := context.Background()
	now := time.Now()
	if err := store.UpsertState(ctx, &StateRow{
		JobID: "internal.bridge-owned", Generation: 1, CurrentState: StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()
	// Deliberately no RegisterInternal for "internal.bridge-owned".

	if err := s.ReconcileBoot(ctx); err != nil {
		t.Fatalf("reconcile boot: %v", err)
	}
	row, err := store.GetState(ctx, "internal.bridge-owned")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if row.CurrentState != StateRunning {
		t.Errorf("non-registered row should be untouched; state = %q", row.CurrentState)
	}
}

// TestReconcile_SkipsRowsForUnregisteredTypeHandler covers the
// user-job case: a row exists for a `scheduled_agent` job but B-B1's
// RegisterTypeHandler hasn't run yet. Should be skipped.
func TestReconcile_SkipsRowsForUnregisteredTypeHandler(t *testing.T) {
	db := setupStateTestDB(t)
	store := NewStateStore(db)
	ctx := context.Background()
	now := time.Now()
	if err := store.UpsertState(ctx, &StateRow{
		JobID: "user-job", Generation: 1, CurrentState: StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	// Pre-seed a spec for the row but DON'T register the type handler.
	// (Simulates a config-load step before RegisterTypeHandler.)
	s.mu.Lock()
	s.specs["user-job"] = JobSpec{
		ID: "user-job", Type: "scheduled_agent", Schedule: "@every 1h", Enabled: true,
	}
	s.mu.Unlock()

	if err := s.ReconcileBoot(ctx); err != nil {
		t.Fatalf("reconcile boot: %v", err)
	}
	row, _ := store.GetState(ctx, "user-job")
	if row.CurrentState != StateRunning {
		t.Errorf("row should be untouched without type handler; state = %q", row.CurrentState)
	}
}
