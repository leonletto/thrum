package monitor

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSupervisor_RejectsInvalidSchedule: schedule validation fires inside Add.
func TestSupervisor_RejectsInvalidSchedule(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	ctx := context.Background()

	spec := makeSpec("bad-sched")
	spec.Schedule = "not a cron expression"

	_, err := sup.Add(ctx, spec)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidSchedule)
}

// TestSupervisor_ScheduleStoredAndListed: a valid schedule round-trips
// through Add → store → ListAll.
func TestSupervisor_ScheduleStoredAndListed(t *testing.T) {
	sup, store := newTestSupervisor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Start(ctx)

	spec := makeSpec("sched-list")
	spec.Schedule = "*/5 * * * *"

	id, err := sup.Add(ctx, spec)
	require.NoError(t, err)

	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "*/5 * * * *", got.Schedule)
}

// TestSupervisor_ScheduledModeFiresOnTick: a scheduled monitor's child runs
// once per tick. We hijack the supervisor's scheduledTickWait so each
// iteration fires immediately, and verify the runner cycles through
// multiple fires within a short window without going dead.
//
// After observing >= 2 fires, ctx is cancelled and we confirm the monitor
// is still in StatusRunning (scheduled mode does NOT MarkDead on per-tick
// exits).
func TestSupervisor_ScheduledModeFiresOnTick(t *testing.T) {
	tickCh := make(chan struct{}, 32)

	sup, store := newTestSupervisor(t)
	sup.scheduledTickWait = func(ctx context.Context, _ time.Time) {
		select {
		case tickCh <- struct{}{}:
		default:
		}
		// Yield briefly so the runner has time to start; this also keeps
		// the loop from saturating a CPU under a pathological test.
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Millisecond):
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supDone := make(chan struct{})
	go func() {
		sup.Start(ctx)
		close(supDone)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-supDone:
		case <-time.After(5 * time.Second):
			t.Log("supervisor did not shut down within 5s")
		}
	})

	spec := makeSpec("sched-fire")
	// Use a very short echo + sleep that exits cleanly. The scheduled
	// runLoop should run this once per tick.
	spec.Argv = []string{"sh", "-c", "echo hi; sleep 0.05"}
	// Any schedule that parses is fine; scheduledTickWait is hijacked.
	spec.Schedule = "* * * * *"

	id, err := sup.Add(ctx, spec)
	require.NoError(t, err)

	// Wait for at least 2 ticks within a generous timeout.
	deadline := time.After(5 * time.Second)
	ticks := 0
	for ticks < 2 {
		select {
		case <-tickCh:
			ticks++
		case <-deadline:
			t.Fatalf("only observed %d ticks; expected >= 2", ticks)
		}
	}

	// Monitor must still be in running status — scheduled-mode exits do
	// NOT MarkDead.
	job, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, job.Status,
		"scheduled monitor must remain in running status after per-tick exits")
}

// TestSupervisor_ContinuousChildExitRestarts: a continuous monitor whose
// child exits should be auto-restarted by the runLoop. We start a child
// that prints once + exits, observe at least one restart, then cancel.
func TestSupervisor_ContinuousChildExitRestarts(t *testing.T) {
	sup, store := newTestSupervisor(t)
	// Tighten backoff so the test doesn't burn real seconds.
	sup.tunables.InitialBackoff = 10 * time.Millisecond
	sup.tunables.MaxBackoff = 50 * time.Millisecond
	sup.tunables.BackoffResetAfter = 24 * time.Hour // never reset

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supDone := make(chan struct{})
	go func() {
		sup.Start(ctx)
		close(supDone)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-supDone:
		case <-time.After(5 * time.Second):
			t.Log("supervisor did not shut down within 5s")
		}
	})

	spec := makeSpec("cont-restart")
	// Quick-exit child — emits one line then exits 0.
	spec.Argv = []string{"sh", "-c", "echo hi"}

	id, err := sup.Add(ctx, spec)
	require.NoError(t, err)

	// Poll the store's last_exit_at to confirm restarts are happening.
	// The runLoop calls RecordExit... actually no, only schedule mode
	// records exits; continuous-mode restarts silently. So we instead
	// confirm the monitor is still running (not dead) after a window in
	// which a non-restarting runner would have MarkDead'd on first exit.
	time.Sleep(300 * time.Millisecond)

	job, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, job.Status,
		"continuous monitor must auto-restart, not MarkDead, on each child exit")
}

// TestSupervisor_RestartBudgetExhaustionMarksDead: when child exits faster
// than the budget allows, the runLoop should MarkDead + deliver an
// "exceeded restart budget" notice.
func TestSupervisor_RestartBudgetExhaustionMarksDead(t *testing.T) {
	store, _ := newTestStore(t)
	delivery, captured := newCapturingDelivery()
	sup := NewMonitorSupervisor(store, delivery)
	// Tight tunables so the budget can be exhausted in well under a second.
	sup.tunables = restartTunables{
		MaxRestartsPerWindow: 3,
		RestartBudgetWindow:  10 * time.Second,
		InitialBackoff:       1 * time.Millisecond,
		MaxBackoff:           5 * time.Millisecond,
		BackoffResetAfter:    24 * time.Hour, // never reset
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supDone := make(chan struct{})
	go func() {
		sup.Start(ctx)
		close(supDone)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-supDone:
		case <-time.After(5 * time.Second):
			t.Log("supervisor did not shut down within 5s")
		}
	})

	spec := makeSpec("cont-exhaust")
	spec.Argv = []string{"sh", "-c", "exit 7"} // crashes immediately

	id, err := sup.Add(ctx, spec)
	require.NoError(t, err)

	// Wait for the monitor row to flip to dead. The runLoop runs ~4 child
	// invocations before exhausting the budget (3 allowed + 1 over).
	deadline := time.After(5 * time.Second)
	for {
		job, gerr := store.GetByID(ctx, id)
		require.NoError(t, gerr)
		if job.Status == StatusDead {
			assert.NotNil(t, job.LastExitCode)
			if job.LastExitCode != nil {
				assert.Equal(t, 7, *job.LastExitCode,
					"last_exit_code should reflect the child's exit")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("monitor never reached dead status; last status = %s", job.Status)
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Confirm an "exceeded restart budget" message was delivered.
	captured.waitForBudgetNotice(t, 2*time.Second)
}

// budgetCapture records delivered message bodies so a test can assert the
// "exceeded restart budget" notice was emitted.
type budgetCapture struct {
	mu     sync.Mutex
	bodies []string
	got    chan struct{}
}

func newCapturingDelivery() (*Delivery, *budgetCapture) {
	bc := &budgetCapture{got: make(chan struct{}, 8)}
	return NewDelivery(&captureSender{cap: bc}), bc
}

type captureSender struct {
	cap *budgetCapture
}

// HandleSend matches the MessageSender interface contract: receives JSON
// params, extracts the body content, and records it for the test.
func (s *captureSender) HandleSend(_ context.Context, params json.RawMessage) (any, error) {
	var payload struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(params, &payload)
	s.cap.mu.Lock()
	s.cap.bodies = append(s.cap.bodies, payload.Content)
	s.cap.mu.Unlock()
	select {
	case s.cap.got <- struct{}{}:
	default:
	}
	return nil, nil
}

// waitForBudgetNotice blocks until a delivered message body contains the
// "exceeded restart budget" marker, or the deadline elapses.
func (b *budgetCapture) waitForBudgetNotice(t *testing.T, dur time.Duration) {
	t.Helper()
	deadline := time.After(dur)
	for {
		b.mu.Lock()
		for _, body := range b.bodies {
			if strings.Contains(body, "exceeded restart budget") {
				b.mu.Unlock()
				return
			}
		}
		b.mu.Unlock()
		select {
		case <-b.got:
			continue
		case <-deadline:
			t.Fatalf("did not observe 'exceeded restart budget' notice within %s; got bodies: %v", dur, b.bodies)
		}
	}
}
