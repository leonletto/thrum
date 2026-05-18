package agentdispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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
func (s *stubTmuxRPC) TmuxLaunch(_ context.Context, _ string) error        { return nil }
func (s *stubTmuxRPC) WaitForPaneReady(_ context.Context, _ string) error  { return nil }
func (s *stubTmuxRPC) TmuxKillSession(_ context.Context, _ string) error   { return nil }
func (s *stubTmuxRPC) PaneSendCtrlCExit(_ context.Context, _ string) error { return nil }

// stubMessageRPC records MessageSend calls + returns canned values.
// Used by stage-2 tests; the recorded call shape lets pinning tests
// assert subject/body/target without spying on RPC internals.
type stubMessageRPC struct {
	returnMessageID string
	returnErr       error

	calls []messageSendCall
}

type messageSendCall struct {
	target  string
	subject string
	body    string
}

func (m *stubMessageRPC) MessageSend(_ context.Context, target, subject, body string) (string, error) {
	m.calls = append(m.calls, messageSendCall{target: target, subject: subject, body: body})
	return m.returnMessageID, m.returnErr
}

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
	msgRPC := &stubMessageRPC{returnMessageID: "msg-stage1"}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc, Message: msgRPC})
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
	msgRPC := &stubMessageRPC{returnMessageID: "msg-happy"}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc, Message: msgRPC})
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

// TestStage2_EnqueuesWakeMessageAndJournalsMessageID pins the canonical
// stage-2 happy path per spec §7.1: Dispatch composes the agent.wake
// body, sends it via MessageRPC.MessageSend, and atomically journals
// the returned message ID under the "wake_message_id" details key on
// the running-state transition. Without atomic journal-write, an A-B4
// stalled-sweep + recovery on this run would have no audit pointer
// back to the inbox row.
func TestStage2_EnqueuesWakeMessageAndJournalsMessageID(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-123"}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc, Message: msgRPC})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-1", rep, nil)
	if err != nil {
		t.Fatalf("expected stages 0-2 to pass; got: %v", err)
	}

	if len(rep.stages) < 3 {
		t.Fatalf("expected stage marker for stage 2; got: %v", rep.stages)
	}
	if rep.stages[2] != agentdispatch.StageEnqueueWakeMessage {
		t.Errorf("stages[2] = %q; want %q", rep.stages[2], agentdispatch.StageEnqueueWakeMessage)
	}

	if len(rep.transitions) == 0 {
		t.Fatalf("expected at least one Transition; got none")
	}
	tr := rep.transitions[0]
	if tr.state != scheduler.StateRunning {
		t.Errorf("transitions[0].state = %v; want StateRunning", tr.state)
	}
	if !strings.Contains(tr.reason, "stage 2 complete") {
		t.Errorf("transitions[0].reason = %q; want substring 'stage 2 complete'", tr.reason)
	}
	if got := tr.details["wake_message_id"]; got != "msg-123" {
		t.Errorf("transitions[0].details[wake_message_id] = %v; want msg-123", got)
	}

	// MessageSend must have been called exactly once with target + subject + body.
	if len(msgRPC.calls) != 1 {
		t.Fatalf("MessageSend calls = %d; want 1", len(msgRPC.calls))
	}
	call := msgRPC.calls[0]
	if call.target != "docs_bot" {
		t.Errorf("MessageSend target = %q; want docs_bot", call.target)
	}
	if !strings.HasPrefix(call.subject, "Wake: docs-bot-job @ ") {
		t.Errorf("MessageSend subject = %q; want prefix 'Wake: docs-bot-job @ '", call.subject)
	}
}

// TestStage2_FailsOnMessageSendError pins the error-propagation path:
// MessageSend returning a real error surfaces as StateFailed with the
// canonical reason prefix and the wrapped error returned from Dispatch.
// Stage-2 emit-failure rolls back via spec §8 escalation in later
// tasks; here we just guard the Transition + return-err contract.
func TestStage2_FailsOnMessageSendError(t *testing.T) {
	wantErr := errors.New("inbox shard offline")
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnErr: wantErr}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc, Message: msgRPC})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-1", rep, nil)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v; want wraps %v", err, wantErr)
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("lastState = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "stage 2: agent.wake enqueue failed") {
		t.Errorf("reason = %q; want substring 'stage 2: agent.wake enqueue failed'", rep.lastTransition().reason)
	}
}

// TestStage2_BuildWakeMessage_ShapeMatchesSpec7_4 pins the canonical
// agent.wake wire format per spec §7.4: JSON inside a markdown fenced
// block with kind, job_id, run_id, scheduled_at (RFC3339), wake_reason
// ("scheduled"), primer, prior_run_summary (nullable; nil for first
// wake). Drift here breaks the lean-prime skill parser on the agent
// side (E6.2).
func TestStage2_BuildWakeMessage_ShapeMatchesSpec7_4(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-shape"}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc, Message: msgRPC})
	rep := &recReporter{}

	if err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-shape", rep, nil); err != nil {
		t.Fatalf("Dispatch returned %v", err)
	}
	if len(msgRPC.calls) != 1 {
		t.Fatalf("MessageSend calls = %d; want 1", len(msgRPC.calls))
	}

	body := msgRPC.calls[0].body
	if !strings.HasPrefix(body, "```json\n") || !strings.HasSuffix(body, "\n```\n") {
		t.Errorf("body not wrapped in json fenced block; got: %q", body)
	}

	// Strip the fence to validate the inner JSON shape.
	inner := strings.TrimPrefix(body, "```json\n")
	inner = strings.TrimSuffix(inner, "\n```\n")

	var got map[string]any
	if err := json.Unmarshal([]byte(inner), &got); err != nil {
		t.Fatalf("inner body is not valid JSON: %v\nbody:\n%s", err, inner)
	}

	for _, key := range []string{"kind", "job_id", "run_id", "scheduled_at", "wake_reason", "primer", "prior_run_summary"} {
		if _, ok := got[key]; !ok {
			t.Errorf("body missing required key %q", key)
		}
	}
	if got["kind"] != "agent.wake" {
		t.Errorf("kind = %v; want 'agent.wake'", got["kind"])
	}
	if got["job_id"] != "docs-bot-job" {
		t.Errorf("job_id = %v; want docs-bot-job", got["job_id"])
	}
	if got["run_id"] != "run-shape" {
		t.Errorf("run_id = %v; want run-shape", got["run_id"])
	}
	if got["wake_reason"] != "scheduled" {
		t.Errorf("wake_reason = %v; want scheduled", got["wake_reason"])
	}
	if got["primer"] != "wake up" {
		t.Errorf("primer = %v; want 'wake up'", got["primer"])
	}
	if got["prior_run_summary"] != nil {
		t.Errorf("prior_run_summary = %v; want nil for first wake", got["prior_run_summary"])
	}
	// scheduled_at must parse as RFC3339.
	ts, ok := got["scheduled_at"].(string)
	if !ok {
		t.Fatalf("scheduled_at = %v; want string", got["scheduled_at"])
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("scheduled_at = %q; not RFC3339 (%v)", ts, err)
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
