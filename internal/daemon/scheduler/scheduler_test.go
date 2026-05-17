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
			t.Error("expected panic on bad suffix (must match idRE — uppercase letters rejected)")
		}
	}()
	// Uppercase violates idRE; lowercase + underscores + hyphens are all accepted
	// by the relaxed regex (canonical IDs use both styles), so the suffix needs
	// a hard-disqualifying character to exercise the panic.
	s.RegisterInternal("internal.BadID", "@every 1m", InternalOpts{}, &noopHandler{})
}

// TestScheduler_RegisterInternal_AcceptsSnakeAndKebab pins the relaxed
// idRE contract: both `internal.scheduler_event_cleanup` (snake — the
// canonical-ref shape used by the cleanup job, email_poll,
// stalled_agent_sweep, skill_staleness_check, telemetry_persistent_poll,
// peer_sync, etc.) AND `internal.my-job` (kebab — older docs) must
// register without panicking.
func TestScheduler_RegisterInternal_AcceptsSnakeAndKebab(t *testing.T) {
	cases := []string{
		"internal.scheduler_event_cleanup",  // canonical snake — Task 35
		"internal.email_poll",                // D-B1 RegisterInternal target
		"internal.stalled_agent_sweep",       // A-B4 RegisterInternal target
		"internal.skill_staleness_check",     // C-B1 RegisterInternal target
		"internal.telemetry_persistent_poll", // MB-1.S6 RegisterInternal target
		"internal.peer_sync",                 // A-B2 RegisterInternal target
		"internal.backup",                    // A-B2 RegisterInternal target
		"internal.kebab-style-job",           // kebab form still works
		"internal.mixed_kebab-and_snake",     // both separators in one id
	}
	for _, id := range cases {
		s := New(Config{DB: setupStateTestDB(t), DaemonID: "test"})
		// Use a recover-wrapped call so one bad id doesn't kill the whole loop.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("RegisterInternal(%q) panicked: %v", id, r)
				}
				_ = s.Stop(context.Background())
			}()
			s.RegisterInternal(id, "@every 1h", InternalOpts{}, &noopHandler{})
			if _, ok := s.JobSpec(id); !ok {
				t.Errorf("RegisterInternal(%q): spec not stored", id)
			}
		}()
	}
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
