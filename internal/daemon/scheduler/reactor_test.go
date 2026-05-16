package scheduler

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// panicHandler always panics on Dispatch. Used to exercise the reactor's
// defer-recover wrapper.
type panicHandler struct{ msg string }

func (p *panicHandler) Dispatch(_ context.Context, _ JobSpec, _ string, _ StateReporter, _ <-chan *Completion) error {
	panic(p.msg)
}

func (p *panicHandler) Reconcile(_ context.Context, _ JobSpec, _ string, _ State) (State, error) {
	return StateFailed, nil
}

func (p *panicHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"executing": time.Minute}
}

// recordingHandler is a test-only Handler that calls onDispatch on each
// Dispatch. Real handler-behavior coverage lives in E1.3.
type recordingHandler struct {
	onDispatch func(id string)
}

func (r *recordingHandler) Dispatch(_ context.Context, job JobSpec, _ string, _ StateReporter, _ <-chan *Completion) error {
	if r.onDispatch != nil {
		r.onDispatch(job.ID)
	}
	return nil
}

func (r *recordingHandler) Reconcile(_ context.Context, _ JobSpec, _ string, _ State) (State, error) {
	return StateCompleted, nil
}

func (r *recordingHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"executing": time.Minute}
}

// TestReactor_TickOrdering: two internal jobs at different cadences fire
// in heap-order; the slow @500ms job should not fire within a 300ms window.
func TestReactor_TickOrdering(t *testing.T) {
	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	var mu sync.Mutex
	var fired []string
	h := &recordingHandler{onDispatch: func(id string) {
		mu.Lock()
		fired = append(fired, id)
		mu.Unlock()
	}}
	s.RegisterInternal("internal.fast", "@every 100ms", InternalOpts{RunAtStart: false}, h)
	s.RegisterInternal("internal.slow", "@every 500ms", InternalOpts{RunAtStart: false}, h)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	<-ctx.Done()
	// Give in-flight goroutines a moment to record.
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	fast := 0
	for _, id := range fired {
		if id == "internal.fast" {
			fast++
		}
		if id == "internal.slow" {
			t.Errorf("slow job (@500ms) should not have fired in 300ms; fired=%v", fired)
		}
	}
	if fast < 2 {
		t.Errorf("fast job fired only %d times in 300ms; expected >= 2 (fired=%v)", fast, fired)
	}
}

// TestReactor_RunAtStart: RunAtStart=true pins the first fire to now so
// the job dispatches immediately on Start.
func TestReactor_RunAtStart(t *testing.T) {
	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	fired := make(chan string, 1)
	h := &recordingHandler{onDispatch: func(id string) {
		select {
		case fired <- id:
		default:
		}
	}}
	s.RegisterInternal("internal.run-at-start", "@every 1h", InternalOpts{RunAtStart: true}, h)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case id := <-fired:
		if id != "internal.run-at-start" {
			t.Errorf("fired = %q", id)
		}
	case <-ctx.Done():
		t.Error("RunAtStart job did not fire within 200ms of Start")
	}
}

// TestReactor_OneShotOnce: @once fires once and leaves
// scheduler_job_state.next_scheduled_at = NULL (canonical-ref §3.11 Guard 1).
func TestReactor_OneShotOnce(t *testing.T) {
	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	fired := make(chan string, 5)
	h := &recordingHandler{onDispatch: func(id string) {
		select {
		case fired <- id:
		default:
		}
	}}
	s.RegisterInternal("internal.oneshot", "@once", InternalOpts{}, h)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	timeout := time.After(300 * time.Millisecond)
	count := 0
loop:
	for {
		select {
		case <-fired:
			count++
		case <-timeout:
			break loop
		}
	}
	if count != 1 {
		t.Errorf("@once fired %d times; want 1", count)
	}
	// Give the dispatch goroutine + state write a moment to land.
	time.Sleep(50 * time.Millisecond)
	row, err := s.state.GetState(context.Background(), "internal.oneshot")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if row.NextScheduledAt != nil {
		t.Errorf("post-fire next_scheduled_at = %v; want nil for one-shot terminal row", row.NextScheduledAt)
	}
	// Task 13's launchRun auto-transitions dispatched → completed when the
	// handler returns nil without an explicit terminal transition; the
	// recordingHandler used here does exactly that.
	if row.CurrentState != StateCompleted {
		t.Errorf("post-fire state = %q; want %q", row.CurrentState, StateCompleted)
	}
}

// TestReactor_HandlerPanic_TransitionsToFailed: a handler that panics must
// not crash the daemon. Run transitions to StateFailed with the panic
// message in last_error; reactor continues processing other jobs.
func TestReactor_HandlerPanic_TransitionsToFailed(t *testing.T) {
	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.RegisterInternal("internal.panic", "@every 50ms", InternalOpts{RunAtStart: true}, &panicHandler{msg: "boom from handler"})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Give the run a moment to dispatch, panic, and transition.
	time.Sleep(150 * time.Millisecond)

	row, err := s.state.GetState(context.Background(), "internal.panic")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if row.CurrentState != StateFailed {
		t.Errorf("state = %q; want %q", row.CurrentState, StateFailed)
	}
	if !strings.Contains(row.LastError, "handler panic") {
		t.Errorf("last_error doesn't mention panic: %q", row.LastError)
	}

	// Scheduler is still running — register a second job and verify the
	// reactor still ticks after the prior panic.
	fired := make(chan string, 1)
	s.RegisterInternal("internal.alive", "@every 30ms", InternalOpts{RunAtStart: true},
		&recordingHandler{onDispatch: func(id string) {
			select {
			case fired <- id:
			default:
			}
		}})
	select {
	case <-fired:
		// reactor still works
	case <-time.After(150 * time.Millisecond):
		t.Error("reactor stuck after panic in another handler")
	}
}

// TestScheduler_ResolveLocation_AllFourLevels exercises the spec §8.2.5
// cascade: per-job > daemon > operator-local > UTC.
func TestScheduler_ResolveLocation_AllFourLevels(t *testing.T) {
	nyLoc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load NY: %v", err)
	}

	// (1) per-job override wins.
	s1 := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s1.Stop(context.Background()) }()
	got := s1.resolveLocation(JobSpec{ID: "x", ScheduleTZ: "America/New_York"})
	if got.String() != nyLoc.String() {
		t.Errorf("per-job: got %v, want %v", got, nyLoc)
	}

	// (2) daemon default when per-job empty.
	s2 := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: nyLoc})
	defer func() { _ = s2.Stop(context.Background()) }()
	got = s2.resolveLocation(JobSpec{ID: "x"})
	if got.String() != nyLoc.String() {
		t.Errorf("daemon: got %v, want %v", got, nyLoc)
	}

	// (3) operator-local fallback when neither per-job nor daemon set.
	// New() defaults Location to time.Local when nil; force back to nil to
	// exercise the cascade past step 2.
	s3 := New(Config{DB: setupStateTestDB(t), DaemonID: "test"})
	defer func() { _ = s3.Stop(context.Background()) }()
	s3.cfg.Location = nil
	got = s3.resolveLocation(JobSpec{ID: "x"})
	if got == nil {
		t.Error("operator-local fallback returned nil")
	}

	// (4) Invalid per-job TZ falls back to daemon default (not UTC).
	s4 := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: nyLoc})
	defer func() { _ = s4.Stop(context.Background()) }()
	got = s4.resolveLocation(JobSpec{ID: "x", ScheduleTZ: "Not/A_Real_TZ"})
	if got.String() != nyLoc.String() {
		t.Errorf("invalid per-job: got %v, want daemon default %v", got, nyLoc)
	}
}
