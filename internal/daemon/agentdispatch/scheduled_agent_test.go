package agentdispatch_test

import (
	"testing"

	"github.com/leonletto/thrum/internal/daemon/agentdispatch"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// TestScheduledAgentHandler_SatisfiesHandlerInterface is the canonical
// compile-time pin: ScheduledAgentHandler must implement scheduler.Handler
// (Dispatch / Reconcile / Stages). The assertion fires at compile time,
// not run time — the `var _` line is the guard.
func TestScheduledAgentHandler_SatisfiesHandlerInterface(t *testing.T) {
	var _ scheduler.Handler = (*agentdispatch.ScheduledAgentHandler)(nil)
}

// TestScheduledAgentHandler_StagesReturnsNineStages pins the canonical
// nine-stage vocabulary per spec §7.1 / canonical §2.2. Drift here —
// added stage, dropped stage, renamed stage — breaks A-B4's stalled-
// sweep skip-set logic, B-B1's idle-nudge stage marker (idle_nudge_NofM
// is dynamic and not in this set), and the operator-facing
// `thrum cron history` output.
func TestScheduledAgentHandler_StagesReturnsNineStages(t *testing.T) {
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{})
	stages := h.Stages()

	for _, want := range []string{
		agentdispatch.StageNameCollisionCheck,
		agentdispatch.StageBudgetCheck,
		agentdispatch.StageEnqueueWakeMessage,
		agentdispatch.StageCreatingWorktree,
		agentdispatch.StageCreatingTmuxSession,
		agentdispatch.StageLaunchingRuntime,
		agentdispatch.StageWaitingForPaneReady,
		agentdispatch.StageRunningWork,
		agentdispatch.StageTearingDown,
	} {
		dur, ok := stages[want]
		if !ok {
			t.Errorf("Stages missing %q", want)
			continue
		}
		if dur <= 0 {
			t.Errorf("Stages[%q] = %v; want positive duration", want, dur)
		}
	}
	if got := len(stages); got != 9 {
		t.Errorf("Stages returned %d entries; want 9 canonical stages", got)
	}
}

// TestIdleNudgeStageFmt pins the canonical §2.2 dynamic stage marker
// format used during stage 7's multi-fire loop (E6.4 Task 36 will
// emit these). `thrum cron history` and the A-B4 sweep observability
// query both string-match against the "idle_nudge_NofM" shape; drift
// in the format string here silently breaks both.
func TestIdleNudgeStageFmt(t *testing.T) {
	cases := []struct {
		n, m int
		want string
	}{
		{1, 5, "idle_nudge_1of5"},
		{3, 5, "idle_nudge_3of5"},
		{10, 10, "idle_nudge_10of10"},
	}
	for _, c := range cases {
		if got := agentdispatch.IdleNudgeStageFmt(c.n, c.m); got != c.want {
			t.Errorf("IdleNudgeStageFmt(%d, %d) = %q; want %q", c.n, c.m, got, c.want)
		}
	}
}
