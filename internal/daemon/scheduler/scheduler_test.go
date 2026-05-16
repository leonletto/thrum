package scheduler

import (
	"context"
	"testing"
	"time"
)

// noopHandler is the minimal Handler used in scheduler-registration tests.
// Real handler-behavior coverage lives in E1.3 (handler.go); E1.1 tests only
// need a stub that satisfies the interface.
type noopHandler struct{}

func (n *noopHandler) Dispatch(_ context.Context, _ JobSpec, _ string, _ StateReporter, _ <-chan *Completion) error {
	return nil
}

func (n *noopHandler) Reconcile(_ context.Context, _ JobSpec, _ string, _ State) (State, error) {
	return StateCompleted, nil
}

func (n *noopHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"executing": 5 * time.Minute}
}

func TestScheduler_RegisterInternal_BasicHappy(t *testing.T) {
	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test-daemon"})
	defer func() { _ = s.Stop(context.Background()) }()

	s.RegisterInternal("internal.test-job", "@every 30s", InternalOpts{RunAtStart: false}, &noopHandler{})

	spec, ok := s.JobSpec("internal.test-job")
	if !ok {
		t.Fatal("JobSpec: not found after RegisterInternal")
	}
	if spec.ID != "internal.test-job" || spec.Type != "internal" {
		t.Errorf("spec = %+v", spec)
	}
	if spec.Schedule != "@every 30s" {
		t.Errorf("schedule = %q", spec.Schedule)
	}
	if !spec.Enabled {
		t.Error("internal job should be enabled by default")
	}
	if spec.CatchUp != "skip" {
		t.Errorf("CatchUp = %q, want default 'skip'", spec.CatchUp)
	}
}

func TestScheduler_RegisterInternal_DuplicatePanics(t *testing.T) {
	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test-daemon"})
	defer func() { _ = s.Stop(context.Background()) }()

	s.RegisterInternal("internal.dup", "@every 1m", InternalOpts{}, &noopHandler{})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate RegisterInternal")
		}
	}()
	s.RegisterInternal("internal.dup", "@every 1m", InternalOpts{}, &noopHandler{})
}

func TestScheduler_RegisterInternal_PanicsOnNonInternalPrefix(t *testing.T) {
	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test-daemon"})
	defer func() { _ = s.Stop(context.Background()) }()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on non-internal.* prefix")
		}
	}()
	s.RegisterInternal("not-internal-prefix", "@every 1m", InternalOpts{}, &noopHandler{})
}

func TestScheduler_RegisterInternal_PanicsOnBadSuffix(t *testing.T) {
	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test-daemon"})
	defer func() { _ = s.Stop(context.Background()) }()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on bad suffix (must match idRE kebab-case)")
		}
	}()
	// Trailing-hyphen + uppercase both violate idRE; pick something obviously bad.
	s.RegisterInternal("internal.Bad_ID", "@every 1m", InternalOpts{}, &noopHandler{})
}

func TestScheduler_RegisterTypeHandler_BasicHappy(t *testing.T) {
	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test-daemon"})
	defer func() { _ = s.Stop(context.Background()) }()

	if err := s.RegisterTypeHandler("scheduled_agent", &noopHandler{}); err != nil {
		t.Fatalf("register type: %v", err)
	}
}

func TestScheduler_RegisterTypeHandler_Duplicate(t *testing.T) {
	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test-daemon"})
	defer func() { _ = s.Stop(context.Background()) }()

	if err := s.RegisterTypeHandler("scheduled_agent", &noopHandler{}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := s.RegisterTypeHandler("scheduled_agent", &noopHandler{}); err == nil {
		t.Error("expected error on duplicate type handler registration")
	}
}

func TestScheduler_JobSpec_NotFound(t *testing.T) {
	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test-daemon"})
	defer func() { _ = s.Stop(context.Background()) }()

	if _, ok := s.JobSpec("does-not-exist"); ok {
		t.Error("JobSpec should return ok=false for unknown id")
	}
}

// TestScheduler_JobSpec_SnapshotCopy pins that the returned JobSpec is a
// value-copy: mutating the returned struct must not corrupt the stored
// spec, since downstream consumers (B-B1, A-B4) hold returned specs across
// goroutines.
func TestScheduler_JobSpec_SnapshotCopy(t *testing.T) {
	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test-daemon"})
	defer func() { _ = s.Stop(context.Background()) }()

	s.RegisterInternal("internal.snap", "@every 1m", InternalOpts{}, &noopHandler{})

	got1, _ := s.JobSpec("internal.snap")
	got1.Schedule = "tampered"
	// Read got1 after the write so the linter sees the mutation has an effect
	// (purpose of the test is to verify it doesn't propagate to storage).
	if got1.Schedule != "tampered" {
		t.Fatalf("local mutation lost: %q", got1.Schedule)
	}

	got2, _ := s.JobSpec("internal.snap")
	if got2.Schedule != "@every 1m" {
		t.Errorf("stored spec was mutated via returned snapshot: %q", got2.Schedule)
	}
}
