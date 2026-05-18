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
	"github.com/leonletto/thrum/internal/skills/mirror"
	"github.com/leonletto/thrum/internal/worktree"
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

// stubWorktreeMgr records Create + Destroy calls + returns canned
// values. Used by stage-3a tests; stage-4-6 rollback + stage-8
// teardown tests extend usage (destroyResult / destroyErr).
type stubWorktreeMgr struct {
	createResult  *worktree.CreateResult
	createErr     error
	destroyResult *worktree.DestroyResult
	destroyErr    error

	createCalls  []worktree.CreateOpts
	destroyCalls []worktree.DestroyOpts
}

func (s *stubWorktreeMgr) Create(_ context.Context, opts worktree.CreateOpts) (*worktree.CreateResult, error) {
	s.createCalls = append(s.createCalls, opts)
	return s.createResult, s.createErr
}

func (s *stubWorktreeMgr) Destroy(_ context.Context, opts worktree.DestroyOpts) (*worktree.DestroyResult, error) {
	s.destroyCalls = append(s.destroyCalls, opts)
	return s.destroyResult, s.destroyErr
}

// okWorktree returns a stub wired for the happy path: Create returns
// a populated CreateResult with no error. Stage-3a/3b downstream tests
// use this when they need stage 3a to succeed.
func okWorktree() *stubWorktreeMgr {
	return &stubWorktreeMgr{
		createResult: &worktree.CreateResult{
			Path:   "/tmp/wt/docs_bot-docs_bot_job-1",
			Branch: "agent/docs_bot/job-docs_bot_job-1",
			Reused: false,
		},
	}
}

// stubMirror records EnsureMirrored calls + returns canned errors.
// Stage-3b happy-path tests leave returnErr nil; rollback tests set
// it to a real error; null-adapter tests set it to mirror.ErrNullAdapter
// (treated as success per C-B1 §12.3.1).
type stubMirror struct {
	returnErr error
	calls     []string
}

func (m *stubMirror) EnsureMirrored(_ context.Context, worktreePath string) error {
	m.calls = append(m.calls, worktreePath)
	return m.returnErr
}

// okMirror returns a mirror stub wired for the happy path. Stage-4+
// downstream tests use this when they need stage 3b to succeed.
func okMirror() *stubMirror { return &stubMirror{} }

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
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc, Message: msgRPC, Worktree: okWorktree(), Mirror: okMirror()})
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
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc, Message: msgRPC, Worktree: okWorktree(), Mirror: okMirror()})
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
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc, Message: msgRPC, Worktree: okWorktree(), Mirror: okMirror()})
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
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{Tmux: rpc, Message: msgRPC, Worktree: okWorktree(), Mirror: okMirror()})
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

// TestStage3a_CallsWorktreeCreate_WithCorrectOpts pins the canonical
// stage-3a invocation per spec §7.1: Dispatch builds CreateOpts with
// RepoPath/AgentName/JobID/WakeTimestamp/BaseBranch/Persistent derived
// from the JobSpec + handler RepoPath, then calls
// WorktreeManager.Create. JobID must be ULID-clean (worktree validator
// rejects hyphens), so the handler sanitizes JobSpec.ID by replacing
// hyphens with underscores.
func TestStage3a_CallsWorktreeCreate_WithCorrectOpts(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-3a"}
	wt := okWorktree()
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath: "/repo",
		Tmux:     rpc,
		Message:  msgRPC,
		Worktree: wt,
		Mirror:   okMirror(),
	})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-1", rep, nil)
	if err != nil {
		t.Fatalf("expected stages 0-3a to pass; got: %v", err)
	}

	if len(wt.createCalls) != 1 {
		t.Fatalf("worktree.Create calls = %d; want 1", len(wt.createCalls))
	}
	opts := wt.createCalls[0]
	if opts.AgentName != "docs_bot" {
		t.Errorf("AgentName = %q; want docs_bot", opts.AgentName)
	}
	if opts.RepoPath != "/repo" {
		t.Errorf("RepoPath = %q; want /repo", opts.RepoPath)
	}
	if opts.JobID == "" {
		t.Error("JobID must be set for ephemeral mode")
	}
	if strings.Contains(opts.JobID, "-") {
		t.Errorf("JobID = %q; must be ULID-clean (no hyphens)", opts.JobID)
	}
	if opts.WakeTimestamp <= 0 {
		t.Errorf("WakeTimestamp = %d; want > 0", opts.WakeTimestamp)
	}
	if opts.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q; want main (default)", opts.BaseBranch)
	}
	if opts.Persistent != false {
		t.Errorf("Persistent = %v; want false (default)", opts.Persistent)
	}

	// Stage marker must fire as the third stage in the canonical walk.
	if len(rep.stages) < 4 {
		t.Fatalf("expected at least 4 stage markers; got: %v", rep.stages)
	}
	if rep.stages[3] != agentdispatch.StageCreatingWorktree {
		t.Errorf("stages[3] = %q; want %q", rep.stages[3], agentdispatch.StageCreatingWorktree)
	}
}

// TestStage3a_HonorsBaseBranchAndPersistentFromJobSpec pins the
// canonical sub-tree wiring: BaseBranch + WorktreePersistent flow from
// JobSpec.ScheduledAgent into worktree.CreateOpts so operator
// configuration reaches the actual create call.
func TestStage3a_HonorsBaseBranchAndPersistentFromJobSpec(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-baseb"}
	wt := okWorktree()
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath: "/repo",
		Tmux:     rpc,
		Message:  msgRPC,
		Worktree: wt,
		Mirror:   okMirror(),
	})
	rep := &recReporter{}

	job := testJob("docs_bot")
	job.ScheduledAgent.BaseBranch = "develop"
	job.ScheduledAgent.WorktreePersistent = true

	if err := h.Dispatch(context.Background(), job, "run-pb", rep, nil); err != nil {
		t.Fatalf("Dispatch err: %v", err)
	}
	if len(wt.createCalls) != 1 {
		t.Fatalf("worktree.Create calls = %d; want 1", len(wt.createCalls))
	}
	opts := wt.createCalls[0]
	if opts.BaseBranch != "develop" {
		t.Errorf("BaseBranch = %q; want develop", opts.BaseBranch)
	}
	if !opts.Persistent {
		t.Error("Persistent = false; want true")
	}
	// Persistent mode skips JobID/WakeTimestamp validation; the handler
	// must NOT populate JobID since the worktree leaf is the agent name.
	if opts.JobID != "" {
		t.Errorf("JobID = %q; want empty when Persistent=true", opts.JobID)
	}
}

// TestStage3a_MapsErrPathExistsToFailedWithSweepDeferralReason pins
// the canonical error mapping per spec §7.1 stage 3 + thrum-non7 §3.5:
// stale ephemeral worktree → StateFailed with the "queued for next-
// boot sweep" reason, error returned wraps ErrPathExists so callers
// can errors.Is against it.
func TestStage3a_MapsErrPathExistsToFailedWithSweepDeferralReason(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-pex"}
	wt := &stubWorktreeMgr{createErr: worktree.ErrPathExists}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath: "/repo",
		Tmux:     rpc,
		Message:  msgRPC,
		Worktree: wt,
	})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-px", rep, nil)
	if !errors.Is(err, worktree.ErrPathExists) {
		t.Errorf("err = %v; want wraps worktree.ErrPathExists", err)
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("lastState = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "queued for next-boot sweep") {
		t.Errorf("reason = %q; want canonical sweep-deferral substring", rep.lastTransition().reason)
	}
}

// TestStage3a_MapsErrPersistentBranchMismatchToManualReconciliation
// pins the second canonical error mapping: an operator-owned branch
// squatting the agent path → StateFailed with the "manual
// reconciliation required" reason. Sweep cannot fix this — needs an
// operator.
func TestStage3a_MapsErrPersistentBranchMismatchToManualReconciliation(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-mismatch"}
	wt := &stubWorktreeMgr{createErr: worktree.ErrPersistentBranchMismatch}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath: "/repo",
		Tmux:     rpc,
		Message:  msgRPC,
		Worktree: wt,
	})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-pm", rep, nil)
	if !errors.Is(err, worktree.ErrPersistentBranchMismatch) {
		t.Errorf("err = %v; want wraps worktree.ErrPersistentBranchMismatch", err)
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("lastState = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "manual reconciliation required") {
		t.Errorf("reason = %q; want canonical manual-reconciliation substring", rep.lastTransition().reason)
	}
}

// TestStage3a_MapsContextCanceledToCancelledWithoutJournalWrite pins
// the cancellation-discipline contract per IMPORTANT #7 + thrum-non7
// §3.7: a context.Canceled error from worktree.Create on the cancel
// path must NOT journal the worktree_path; E6.9 sweep owns the
// orphan reclamation. Inline rollback would race the sweep and
// double-delete; the asymmetric stage-failure rollback table is the
// point.
func TestStage3a_MapsContextCanceledToCancelledWithoutJournalWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-cancel"}
	wt := &stubWorktreeMgr{createErr: context.Canceled}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath: "/repo",
		Tmux:     rpc,
		Message:  msgRPC,
		Worktree: wt,
	})
	rep := &recReporter{}

	_ = h.Dispatch(ctx, testJob("docs_bot"), "run-cancel", rep, nil)

	if rep.lastTransition().state != scheduler.StateCancelled {
		t.Errorf("lastState = %v; want StateCancelled", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "cancelled mid-create") {
		t.Errorf("reason = %q; want canonical cancellation substring", rep.lastTransition().reason)
	}
	// CRITICAL: no worktree_path in ANY journal entry on the cancel path.
	for i, tr := range rep.transitions {
		if _, has := tr.details["worktree_path"]; has {
			t.Errorf("transitions[%d].details has worktree_path on cancel path; want absent (defer to sweep): %+v",
				i, tr.details)
		}
	}
}

// TestStage3a_MapsContextDeadlineExceededToCancelledWithoutJournalWrite
// is the deadline-exceeded twin of the cancel test: both share the
// thrum-non7 §3.7 deferral semantics so the sweep can reclaim either
// orphan class uniformly.
func TestStage3a_MapsContextDeadlineExceededToCancelledWithoutJournalWrite(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-deadline"}
	wt := &stubWorktreeMgr{createErr: context.DeadlineExceeded}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath: "/repo",
		Tmux:     rpc,
		Message:  msgRPC,
		Worktree: wt,
	})
	rep := &recReporter{}

	_ = h.Dispatch(context.Background(), testJob("docs_bot"), "run-deadline", rep, nil)

	if rep.lastTransition().state != scheduler.StateCancelled {
		t.Errorf("lastState = %v; want StateCancelled", rep.lastTransition().state)
	}
	for i, tr := range rep.transitions {
		if _, has := tr.details["worktree_path"]; has {
			t.Errorf("transitions[%d].details has worktree_path on deadline path; want absent: %+v",
				i, tr.details)
		}
	}
}

// TestStage3a_DefaultErrorMapsToFailedWithRawErrorString pins the
// fallback classification: an unclassified worktree.Create error
// (e.g. ErrInvalidOpts in a misconfigured path, or some future
// sentinel) surfaces as StateFailed with the raw error string in the
// transition reason so operator diagnostics include the root cause.
func TestStage3a_DefaultErrorMapsToFailedWithRawErrorString(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-default"}
	rawErr := errors.New("disk full")
	wt := &stubWorktreeMgr{createErr: rawErr}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath: "/repo",
		Tmux:     rpc,
		Message:  msgRPC,
		Worktree: wt,
	})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-def", rep, nil)
	if !errors.Is(err, rawErr) {
		t.Errorf("err = %v; want wraps raw err", err)
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("lastState = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "disk full") {
		t.Errorf("reason = %q; want raw error substring 'disk full'", rep.lastTransition().reason)
	}
}

// TestStage3b_HappyPath_JournalsAfterMirrorSuccess pins the canonical
// stage-3 closing: when BOTH worktree.Create and EnsureMirrored
// succeed, Dispatch emits a StateRunning transition with
// worktree_path + branch_name + reused under the details map. Stage 4+
// pivots off this atomic record (transcript_dir join + tmux create
// would otherwise lose ground-truth on a crash between sub-actions).
func TestStage3b_HappyPath_JournalsAfterMirrorSuccess(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-3b"}
	wt := okWorktree()
	mir := okMirror()
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath: "/repo",
		Tmux:     rpc,
		Message:  msgRPC,
		Worktree: wt,
		Mirror:   mir,
	})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-3b", rep, nil)
	if err != nil {
		t.Fatalf("expected stages 0-3 to pass; got: %v", err)
	}

	if len(mir.calls) != 1 || mir.calls[0] != wt.createResult.Path {
		t.Errorf("EnsureMirrored calls = %v; want [%q]", mir.calls, wt.createResult.Path)
	}

	// Stage 3 atomic journal-write must be the LAST transition with
	// worktree_path/branch_name/reused all populated.
	tr := rep.lastTransition()
	if tr.state != scheduler.StateRunning {
		t.Errorf("last state = %v; want StateRunning", tr.state)
	}
	if !strings.Contains(tr.reason, "stage 3 complete") {
		t.Errorf("last reason = %q; want substring 'stage 3 complete'", tr.reason)
	}
	if tr.details["worktree_path"] != wt.createResult.Path {
		t.Errorf("details[worktree_path] = %v; want %q", tr.details["worktree_path"], wt.createResult.Path)
	}
	if tr.details["branch_name"] != wt.createResult.Branch {
		t.Errorf("details[branch_name] = %v; want %q", tr.details["branch_name"], wt.createResult.Branch)
	}
	if tr.details["reused"] != false {
		t.Errorf("details[reused] = %v; want false", tr.details["reused"])
	}
}

// TestStage3b_ErrNullAdapter_TreatedAsSuccess pins the C-B1 §12.3.1
// null-adapter contract: some runtimes (codex, opencode, kiro, cursor
// as of plan v2) have no mirror surface in v0.11; EnsureMirrored
// returning ErrNullAdapter is success-skip, NOT a rollback trigger.
// Stage 3 must still close with the worktree_path journal entry — the
// agent still reads skills directly from the worktree even without a
// per-runtime mirror copy.
func TestStage3b_ErrNullAdapter_TreatedAsSuccess(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-3b-null"}
	wt := okWorktree()
	mir := &stubMirror{returnErr: mirror.ErrNullAdapter}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath: "/repo",
		Tmux:     rpc,
		Message:  msgRPC,
		Worktree: wt,
		Mirror:   mir,
	})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-null", rep, nil)
	if err != nil {
		t.Fatalf("ErrNullAdapter should NOT propagate; got err: %v", err)
	}

	tr := rep.lastTransition()
	if tr.state != scheduler.StateRunning {
		t.Errorf("last state = %v; want StateRunning", tr.state)
	}
	if tr.details["worktree_path"] != wt.createResult.Path {
		t.Errorf("ErrNullAdapter blocked stage-3 close; details = %+v", tr.details)
	}
	// And critically: NO inline Destroy was triggered.
	if len(wt.destroyCalls) != 0 {
		t.Errorf("ErrNullAdapter triggered inline Destroy: %d call(s); want 0", len(wt.destroyCalls))
	}
}

// TestStage3b_NonCancelError_DestroysWorktreeAndTransitionsFailed pins
// the inline-rollback contract per spec §7.1 stage 3b + thrum-non7
// §3.5: a non-cancel hard failure from EnsureMirrored leaves a
// fully-created worktree behind that must be inline-destroyed (with
// the destroyed paths recorded in the failure event details so an
// audit trail back to the lost worktree is preserved).
func TestStage3b_NonCancelError_DestroysWorktreeAndTransitionsFailed(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-3b-fail"}
	wt := okWorktree()
	mirrorErr := errors.New("mirror disk full")
	mir := &stubMirror{returnErr: mirrorErr}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath: "/repo",
		Tmux:     rpc,
		Message:  msgRPC,
		Worktree: wt,
		Mirror:   mir,
	})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testJob("docs_bot"), "run-3b-fail", rep, nil)
	if !errors.Is(err, mirrorErr) {
		t.Errorf("err = %v; want wraps mirrorErr", err)
	}

	// Inline rollback fires with the right opts.
	if len(wt.destroyCalls) != 1 {
		t.Fatalf("worktree.Destroy calls = %d; want 1", len(wt.destroyCalls))
	}
	d := wt.destroyCalls[0]
	if d.WorktreePath != wt.createResult.Path {
		t.Errorf("Destroy.WorktreePath = %q; want %q", d.WorktreePath, wt.createResult.Path)
	}
	if d.Branch != wt.createResult.Branch {
		t.Errorf("Destroy.Branch = %q; want %q", d.Branch, wt.createResult.Branch)
	}
	if !d.Force {
		t.Error("Destroy.Force = false; want true (ephemeral teardown requires --force)")
	}
	if d.RepoPath != "/repo" {
		t.Errorf("Destroy.RepoPath = %q; want /repo", d.RepoPath)
	}

	tr := rep.lastTransition()
	if tr.state != scheduler.StateFailed {
		t.Errorf("last state = %v; want StateFailed", tr.state)
	}
	if !strings.Contains(tr.reason, "stage 3b: skill mirror failed") {
		t.Errorf("reason = %q; want substring 'stage 3b: skill mirror failed'", tr.reason)
	}
	if tr.details["worktree_path_destroyed"] != wt.createResult.Path {
		t.Errorf("details[worktree_path_destroyed] = %v; want %q (audit trail)",
			tr.details["worktree_path_destroyed"], wt.createResult.Path)
	}
	if tr.details["branch_name_destroyed"] != wt.createResult.Branch {
		t.Errorf("details[branch_name_destroyed] = %v; want %q",
			tr.details["branch_name_destroyed"], wt.createResult.Branch)
	}
}

// TestStage3b_ContextCanceled_DefersToSweep pins the asymmetric
// rollback table (coordinator trap #3 in the resume plan): a
// context.Canceled error from EnsureMirrored must NOT trigger inline
// worktree.Destroy. The E6.9 sweep reclaims the orphan; inline rollback
// would race the sweep AND extend daemon-shutdown latency unnecessarily.
func TestStage3b_ContextCanceled_DefersToSweep(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-3b-cancel"}
	wt := okWorktree()
	mir := &stubMirror{returnErr: context.Canceled}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath: "/repo",
		Tmux:     rpc,
		Message:  msgRPC,
		Worktree: wt,
		Mirror:   mir,
	})
	rep := &recReporter{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = h.Dispatch(ctx, testJob("docs_bot"), "run-3b-cancel", rep, nil)

	if len(wt.destroyCalls) != 0 {
		t.Errorf("context-cancel triggered inline Destroy: %d call(s); want 0 (defer to sweep)",
			len(wt.destroyCalls))
	}
	if rep.lastTransition().state != scheduler.StateCancelled {
		t.Errorf("last state = %v; want StateCancelled", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "cancelled mid-mirror") {
		t.Errorf("reason = %q; want substring 'cancelled mid-mirror'", rep.lastTransition().reason)
	}
}

// TestStage3b_DeadlineExceeded_DefersToSweep is the deadline-exceeded
// twin of the cancel test: both share thrum-non7 §3.7 deferral
// semantics so the sweep can reclaim either orphan class uniformly.
func TestStage3b_DeadlineExceeded_DefersToSweep(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-3b-deadline"}
	wt := okWorktree()
	mir := &stubMirror{returnErr: context.DeadlineExceeded}
	h := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath: "/repo",
		Tmux:     rpc,
		Message:  msgRPC,
		Worktree: wt,
		Mirror:   mir,
	})
	rep := &recReporter{}

	_ = h.Dispatch(context.Background(), testJob("docs_bot"), "run-3b-deadline", rep, nil)

	if len(wt.destroyCalls) != 0 {
		t.Errorf("deadline-exceeded triggered inline Destroy: %d call(s); want 0",
			len(wt.destroyCalls))
	}
	if rep.lastTransition().state != scheduler.StateCancelled {
		t.Errorf("last state = %v; want StateCancelled", rep.lastTransition().state)
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
