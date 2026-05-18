package agentdispatch

import (
	"context"
	"errors"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// TestPlaceholderHandler_SatisfiesInterface is a compile-time pin
// mirroring the var _ assertion in placeholder.go.
func TestPlaceholderHandler_SatisfiesInterface(t *testing.T) {
	var _ scheduler.Handler = (*PlaceholderHandler)(nil)
}

// TestPlaceholderHandler_DispatchReturnsWiringPending pins the
// canonical 42a contract: Dispatch returns
// ErrHandlerWiringPending wrapped with the job type, so operators
// reading `thrum cron history` see a clear "wiring pending" error
// rather than a nil-deref panic.
func TestPlaceholderHandler_DispatchReturnsWiringPending(t *testing.T) {
	h := NewPlaceholderHandler("scheduled_agent")
	err := h.Dispatch(context.Background(), scheduler.JobSpec{ID: "test-job"}, "run-1", nil, nil)
	if err == nil {
		t.Fatal("expected Dispatch to return ErrHandlerWiringPending; got nil")
	}
	if !errors.Is(err, ErrHandlerWiringPending) {
		t.Errorf("errors.Is(err, ErrHandlerWiringPending) = false; got %v", err)
	}
	// Job type appears in the wrapped error so operators can
	// disambiguate scheduled_agent vs nudge wiring gaps.
	if got := err.Error(); !contains(got, "scheduled_agent") {
		t.Errorf("error %q should mention job type", got)
	}
}

// TestPlaceholderHandler_ReconcileMarksFailed pins the
// conservative-default behavior on boot recovery: a non-terminal
// row tagged with a placeholder-handled type can't be resumed, so
// reconcile reports StateFailed + ErrHandlerWiringPending.
func TestPlaceholderHandler_ReconcileMarksFailed(t *testing.T) {
	h := NewPlaceholderHandler("nudge")
	state, err := h.Reconcile(context.Background(), scheduler.JobSpec{ID: "test-job"}, "run-1", scheduler.StateRunning)
	if state != scheduler.StateFailed {
		t.Errorf("Reconcile state = %v; want StateFailed", state)
	}
	if err == nil {
		t.Fatal("expected Reconcile to return error; got nil")
	}
	if !errors.Is(err, ErrHandlerWiringPending) {
		t.Errorf("errors.Is(err, ErrHandlerWiringPending) = false; got %v", err)
	}
	if got := err.Error(); !contains(got, "nudge") {
		t.Errorf("error %q should mention job type", got)
	}
}

// TestPlaceholderHandler_StagesNonEmpty pins the stalled-sweep
// contract: the stages map is non-empty so A-B4 doesn't trip on a
// missing-entry case.
func TestPlaceholderHandler_StagesNonEmpty(t *testing.T) {
	h := NewPlaceholderHandler("scheduled_agent")
	stages := h.Stages()
	if len(stages) == 0 {
		t.Error("Stages returned empty map; A-B4 stalled-sweep would have no dwell budget")
	}
}

// TestPlaceholderHandler_JobTypeIsolation verifies that two
// placeholders carry distinct job types so their dispatch errors
// can be distinguished in operator logs.
func TestPlaceholderHandler_JobTypeIsolation(t *testing.T) {
	hSched := NewPlaceholderHandler("scheduled_agent")
	hNudge := NewPlaceholderHandler("nudge")

	errSched := hSched.Dispatch(context.Background(), scheduler.JobSpec{}, "r", nil, nil)
	errNudge := hNudge.Dispatch(context.Background(), scheduler.JobSpec{}, "r", nil, nil)

	if !contains(errSched.Error(), "scheduled_agent") {
		t.Errorf("scheduled_agent placeholder error %q missing job type", errSched)
	}
	if !contains(errNudge.Error(), "nudge") {
		t.Errorf("nudge placeholder error %q missing job type", errNudge)
	}
	if contains(errSched.Error(), "nudge") {
		t.Errorf("scheduled_agent placeholder error %q mentions wrong type", errSched)
	}
}

// contains is a tiny test helper to avoid pulling strings in.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
