package email

import (
	"context"
	"testing"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// stubReporter is a minimal scheduler.StateReporter that records the
// transitions and stage transitions for assertions. Avoids standing up
// a real StateStore + DB just to drive a no-op Dispatch.
type stubReporter struct {
	transitions []scheduler.State
	stages      []string
}

func (s *stubReporter) Transition(to scheduler.State, _ string, _ map[string]any) error {
	s.transitions = append(s.transitions, to)
	return nil
}
func (s *stubReporter) Stage(name string) error {
	s.stages = append(s.stages, name)
	return nil
}

// newStoppedBridge returns a Bridge that has never had its atomic
// pointers set — every accessor returns nil. Mirrors the production
// case where the substrate handler ticks while bridge.Run() is in its
// retry-backoff window or the operator has disabled the bridge.
func newStoppedBridge(t *testing.T) *Bridge {
	t.Helper()
	return New(config.EmailConfig{DaemonHandle: "stopped"}, nil, "8080")
}

// TestPollHandler_NoOpsWhenBridgeDown: dispatch transitions to Completed
// (not Failed) when IMAP/Inbound atomic pointers are nil. The
// no-op-on-down semantic keeps consecutive_failures bounded while the
// operator is restarting or troubleshooting the bridge.
func TestPollHandler_NoOpsWhenBridgeDown(t *testing.T) {
	t.Parallel()
	b := newStoppedBridge(t)
	h := NewPollHandler(b)
	r := &stubReporter{}

	if err := h.Dispatch(context.Background(), scheduler.JobSpec{}, "run-1", r, nil); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	if got := r.transitions; len(got) < 2 || got[len(got)-1] != scheduler.StateCompleted {
		t.Errorf("final transition = %v; want Completed", got)
	}
}

// TestDedupCleanupHandler_NoOpsWhenBridgeDown: same contract as
// PollHandler — handler swallows the bridge-down condition gracefully.
func TestDedupCleanupHandler_NoOpsWhenBridgeDown(t *testing.T) {
	t.Parallel()
	b := newStoppedBridge(t)
	h := NewDedupCleanupHandler(b)
	r := &stubReporter{}

	if err := h.Dispatch(context.Background(), scheduler.JobSpec{}, "run-1", r, nil); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	if got := r.transitions; len(got) < 2 || got[len(got)-1] != scheduler.StateCompleted {
		t.Errorf("final transition = %v; want Completed", got)
	}
}

// TestQueueDrainHandler_NoOpsWhenBridgeDown: same contract.
func TestQueueDrainHandler_NoOpsWhenBridgeDown(t *testing.T) {
	t.Parallel()
	b := newStoppedBridge(t)
	h := NewQueueDrainHandler(b)
	r := &stubReporter{}

	if err := h.Dispatch(context.Background(), scheduler.JobSpec{}, "run-1", r, nil); err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}
	if got := r.transitions; len(got) < 2 || got[len(got)-1] != scheduler.StateCompleted {
		t.Errorf("final transition = %v; want Completed", got)
	}
}

// TestEmailHandlers_ReconcileIsIdempotent: all three handlers return
// StateCompleted from Reconcile regardless of prior state — the
// fetch+process / sweep / drain operations are all naturally idempotent
// over the dedup table + UPDATE-with-RowsAffected claim, so there is
// nothing to recover.
func TestEmailHandlers_ReconcileIsIdempotent(t *testing.T) {
	t.Parallel()
	b := newStoppedBridge(t)
	priors := []scheduler.State{scheduler.StateRunning, scheduler.StateDispatched, scheduler.StateFailed}

	for _, h := range []scheduler.Handler{
		NewPollHandler(b),
		NewDedupCleanupHandler(b),
		NewQueueDrainHandler(b),
	} {
		for _, prior := range priors {
			got, err := h.Reconcile(context.Background(), scheduler.JobSpec{}, "r", prior)
			if err != nil {
				t.Errorf("Reconcile %T prior=%s: %v", h, prior, err)
			}
			if got != scheduler.StateCompleted {
				t.Errorf("Reconcile %T prior=%s: got %s; want Completed", h, prior, got)
			}
		}
	}
}

// TestEmailHandlers_StagesDeclared verifies each handler declares its
// execution stage so A-B4 stalled-sweep dwell tracking has a key to
// consult. Empty Stages would silently disable nudging.
func TestEmailHandlers_StagesDeclared(t *testing.T) {
	t.Parallel()
	b := newStoppedBridge(t)
	for _, h := range []scheduler.Handler{
		NewPollHandler(b),
		NewDedupCleanupHandler(b),
		NewQueueDrainHandler(b),
	} {
		s := h.Stages()
		if len(s) == 0 {
			t.Errorf("%T: Stages() empty; want at least one declared stage", h)
		}
	}
}
