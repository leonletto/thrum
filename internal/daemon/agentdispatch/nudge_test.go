package agentdispatch_test

import (
	"testing"

	"github.com/leonletto/thrum/internal/daemon/agentdispatch"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// TestNudgeHandler_SatisfiesHandlerInterface is the canonical
// compile-time pin per E6.3 Task 29 / spec §9.4.1: NudgeHandler must
// implement scheduler.Handler (Dispatch / Reconcile / Stages). The
// assertion fires at compile time, not run time — if NudgeHandler
// ever drifts off the Handler interface, this file won't build.
func TestNudgeHandler_SatisfiesHandlerInterface(t *testing.T) {
	var _ scheduler.Handler = (*agentdispatch.NudgeHandler)(nil)
}

// TestNudgeHandler_StagesReturnsDeliveringWith10s pins the single-
// stage vocabulary per spec §7.2: nudge dispatch is a single
// "delivering" stage with a 10s max-dwell budget. Drift here breaks
// A-B4's stalled-sweep skip-set logic for nudge runs (the sweep
// queries scheduler_job_state.current_stage and "delivering" is the
// only legal value).
func TestNudgeHandler_StagesReturnsDeliveringWith10s(t *testing.T) {
	h := agentdispatch.NewNudgeHandler(agentdispatch.NudgeDeps{})
	stages := h.Stages()

	if got := len(stages); got != 1 {
		t.Fatalf("Stages returned %d entries; want 1 (single 'delivering' stage)", got)
	}
	dur, ok := stages["delivering"]
	if !ok {
		t.Fatalf("Stages missing 'delivering' key; got: %v", stages)
	}
	if dur <= 0 {
		t.Errorf("Stages[delivering] = %v; want positive duration", dur)
	}
}
