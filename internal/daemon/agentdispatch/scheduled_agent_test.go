package agentdispatch_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/agentdispatch"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// stubTmuxRPC records calls + returns canned values. Used by stage-0
// (CheckPane) tests; later stage tests extend the fields as needed.
type stubTmuxRPC struct {
	checkPaneResult bool
	checkPaneErr    error
	checkPaneCalls  []string // recorded targets
}

func (s *stubTmuxRPC) CheckPane(_ context.Context, target string) (bool, error) {
	s.checkPaneCalls = append(s.checkPaneCalls, target)
	return s.checkPaneResult, s.checkPaneErr
}
func (s *stubTmuxRPC) TmuxCreate(_ context.Context, _ string, _ agentdispatch.TmuxCreateOpts) error {
	return nil
}
func (s *stubTmuxRPC) TmuxLaunch(_ context.Context, _ string) error            { return nil }
func (s *stubTmuxRPC) WaitForPaneReady(_ context.Context, _ string) error      { return nil }
func (s *stubTmuxRPC) TmuxKillSession(_ context.Context, _ string) error       { return nil }
func (s *stubTmuxRPC) PaneSendCtrlCExit(_ context.Context, _ string) error     { return nil }

// recReporter pins the scheduler.StateReporter interface for the
// scheduled-agent stage tests — records every Transition + Stage call
// plus the details map (richer than cleanup_internal_test.go's
// stubReporter, which only records state + reason parallel slices).
// Kept package-private so cleanup tests stay on the simpler shape.
type recReporter struct {
	transitions []recCall
	stages      []string
}

type recCall struct {
	state   scheduler.State
	reason  string
	details map[string]any
}

func (r *recReporter) Transition(s scheduler.State, reason string, details map[string]any) error {
	r.transitions = append(r.transitions, recCall{state: s, reason: reason, details: details})
	return nil
}

func (r *recReporter) Stage(name string) error {
	r.stages = append(r.stages, name)
	return nil
}

func (r *recReporter) lastTransition() recCall {
	if len(r.transitions) == 0 {
		return recCall{}
	}
	return r.transitions[len(r.transitions)-1]
}

// testJob builds a minimal JobSpec with a scheduled_agent target.
func testJob(target string) scheduler.JobSpec {
	return scheduler.JobSpec{
		ID:             "docs-bot-job",
		Type:           "scheduled_agent",
		Schedule:       "@every 1h",
		Enabled:        true,
		ScheduledAgent: &scheduler.ScheduledAgentSpec{Target: target, Primer: "wake up"},
	}
}

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

// TestStage0_RejectsWhenTargetSessionAlive pins the canonical name-
// collision behavior per spec §7.1 stage 0: if a tmux pane already
// exists for the target agent, Dispatch refuses with
// ErrTargetSessionAlive and transitions the run to StateFailed.
// Without this guard, a wake fire would clobber a live agent.
func TestStage0_RejectsWhenTargetSessionAlive(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: true}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-1", rep, nil)
	if err == nil {
		t.Fatal("expected stage-0 failure, got nil")
	}
	if !errors.Is(err, agentdispatch.ErrTargetSessionAlive) {
		t.Errorf("err = %v; want wraps ErrTargetSessionAlive", err)
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("lastState = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "stage 0") {
		t.Errorf("reason = %q; want substring 'stage 0'", rep.lastTransition().reason)
	}
	if !strings.Contains(rep.lastTransition().reason, "docs_bot") {
		t.Errorf("reason = %q; want mention of target name", rep.lastTransition().reason)
	}
	// Stage marker must fire — observability depends on the nine-stage walk.
	if len(rep.stages) == 0 || rep.stages[0] != agentdispatch.StageNameCollisionCheck {
		t.Errorf("first stage = %v; want %q", rep.stages, agentdispatch.StageNameCollisionCheck)
	}
}

// TestStage0_FailsOnCheckPaneError pins the error-propagation path:
// CheckPane returning a real error (not just "alive=true") surfaces
// as StateFailed with the wrapped error returned from Dispatch.
// Distinguishes "could not determine" from "pane alive" cleanly so
// operator diagnostics aren't ambiguous.
func TestStage0_FailsOnCheckPaneError(t *testing.T) {
	wantErr := errors.New("tmux socket gone")
	rpc := &stubTmuxRPC{checkPaneErr: wantErr}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-1", rep, nil)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v; want wraps %v", err, wantErr)
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("lastState = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "stage 0: CheckPane error") {
		t.Errorf("reason = %q; want substring 'stage 0: CheckPane error'", rep.lastTransition().reason)
	}
}

// TestStage1_BudgetCheckMarkerEmittedEvenThoughCheckIsUpstream pins
// the canonical Q-Spec-3 resolution + MINOR #6 reframing from plan
// v1 dual-review: A-B1's reactor performs the actual budget check
// BEFORE invoking Dispatch (over-budget jobs never reach this
// handler — A-B1 emits dispatched → over_budget upstream). B-B1's
// stage-1 contribution is the observability marker so downstream
// tools (`thrum cron history`, A-B4 stalled-sweep skip-set logic)
// see the full nine-stage walk in scheduler_job_events.
func TestStage1_BudgetCheckMarkerEmittedEvenThoughCheckIsUpstream(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-1", rep, nil)
	if err != nil {
		t.Fatalf("expected stages 0-1 to pass; got: %v", err)
	}
	// Both stages 0 + 1 must fire as the dispatch advances. Order
	// matters: name_collision_check then budget_check.
	if len(rep.stages) < 2 {
		t.Fatalf("expected at least 2 stage markers; got: %v", rep.stages)
	}
	if rep.stages[0] != agentdispatch.StageNameCollisionCheck {
		t.Errorf("stages[0] = %q; want %q", rep.stages[0], agentdispatch.StageNameCollisionCheck)
	}
	if rep.stages[1] != agentdispatch.StageBudgetCheck {
		t.Errorf("stages[1] = %q; want %q", rep.stages[1], agentdispatch.StageBudgetCheck)
	}
}

// TestStage0_HappyPath pins the no-collision path: when CheckPane
// returns (false, nil), stage 0 emits its marker and Dispatch falls
// through to stage 1+ (which are still placeholders in Task 10 — the
// test asserts the marker fired but doesn't assert downstream state
// since Tasks 11-19 fill in the remaining stages).
func TestStage0_HappyPath(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-1", rep, nil)
	if err != nil {
		t.Fatalf("expected stage-0 to pass, got: %v", err)
	}
	if len(rep.stages) == 0 || rep.stages[0] != agentdispatch.StageNameCollisionCheck {
		t.Errorf("first stage = %v; want %q", rep.stages, agentdispatch.StageNameCollisionCheck)
	}
	// CheckPane should have been called exactly once with our target.
	if len(rpc.checkPaneCalls) != 1 || rpc.checkPaneCalls[0] != "docs_bot" {
		t.Errorf("CheckPane calls = %v; want [docs_bot]", rpc.checkPaneCalls)
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
