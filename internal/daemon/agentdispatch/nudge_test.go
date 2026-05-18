package agentdispatch_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/agentdispatch"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// stubAgentRegistry records Lookup calls + returns canned values.
// Mutator methods (SetAutoRespawnDisabledAt etc.) return nil since
// NudgeHandler doesn't touch them; they're stubbed for interface
// satisfaction.
type stubAgentRegistry struct {
	lookupAgent agent.Agent
	lookupErr   error
	lookupCalls []string
}

func (s *stubAgentRegistry) Lookup(_ context.Context, name string) (agent.Agent, error) {
	s.lookupCalls = append(s.lookupCalls, name)
	return s.lookupAgent, s.lookupErr
}
func (s *stubAgentRegistry) ListAutoRespawnEnabled(_ context.Context) ([]agent.Agent, error) {
	return nil, nil
}
func (s *stubAgentRegistry) SetAutoRespawnDisabledAt(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (s *stubAgentRegistry) ClearAutoRespawnDisabledAt(_ context.Context, _ string) error {
	return nil
}
func (s *stubAgentRegistry) SetStateMdParseFailedAt(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (s *stubAgentRegistry) ClearStateMdParseFailedAt(_ context.Context, _ string) error {
	return nil
}

// testNudgeJob builds a minimal JobSpec for the nudge handler.
func testNudgeJob(target string) scheduler.JobSpec {
	return scheduler.JobSpec{
		ID:       "nudge-job-1",
		Type:     "nudge",
		Schedule: "@every 1h",
		Enabled:  true,
		Nudge:    &scheduler.NudgeSpec{Target: target, Message: "reminder: check the inbox"},
	}
}

// TestTypeNudge_MatchesSchedulerValidator pins the canonical type
// ID per spec §7.2: the exported TypeNudge constant MUST equal the
// "nudge" string literal that internal/daemon/scheduler/validator.go
// keys off. Drift between the constant and the validator would mean
// E6.5's registration call succeeds but the validator rejects every
// user-supplied nudge job — silent in-production breakage.
func TestTypeNudge_MatchesSchedulerValidator(t *testing.T) {
	if agentdispatch.TypeNudge != "nudge" {
		t.Errorf("TypeNudge = %q; want %q (validator keys off this string)",
			agentdispatch.TypeNudge, "nudge")
	}
}

// TestNudgeDispatch_ErrTargetOffline pins the canonical pre-enqueue
// liveness gate per spec §7.2 + AC 9.4.2: if CheckPane reports the
// target's pane is not alive, Dispatch refuses with ErrTargetOffline
// and transitions to StateFailed with the canonical reason. A nudge
// sent to an offline agent is wasted I/O; refusing at the
// dispatcher protects the inbox from filling with stale prods.
func TestNudgeDispatch_ErrTargetOffline(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: false} // pane gone
	msgRPC := &stubMessageRPC{}
	reg := &stubAgentRegistry{}
	h := agentdispatch.NewNudgeHandler(agentdispatch.NudgeDeps{Tmux: rpc, Message: msgRPC, Registry: reg})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testNudgeJob("docs_bot"), "run-offline", rep, nil)
	if !errors.Is(err, agentdispatch.ErrTargetOffline) {
		t.Errorf("err = %v; want wraps ErrTargetOffline", err)
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("lastState = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "nudge target offline") {
		t.Errorf("reason = %q; want canonical 'nudge target offline'", rep.lastTransition().reason)
	}
	// Critical: NO message send when offline.
	if len(msgRPC.calls) != 0 {
		t.Errorf("MessageSend called %d times; want 0 (refuse before enqueue)", len(msgRPC.calls))
	}
	// Registry lookup must NOT fire when the pane is offline — the
	// liveness check is the first gate, ordered to fail-fast on the
	// most common cause of nudge waste.
	if len(reg.lookupCalls) != 0 {
		t.Errorf("Registry.Lookup calls = %d; want 0 (liveness gate fails fast)", len(reg.lookupCalls))
	}
}

// TestNudgeDispatch_ErrTargetNotRegistered pins the canonical
// registry-presence gate per spec §7.2 + AC 9.4.3 + BLOCKING #6
// fix: if the target's pane is alive but the agent isn't in the
// registry, Dispatch refuses with ErrTargetNotRegistered. Use of
// errors.Is(err, agent.ErrAgentNotFound) (NOT a boolean-ok pattern)
// is the fix-shape mandated by the dual-review BLOCKING #6 finding.
func TestNudgeDispatch_ErrTargetNotRegistered(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: true} // alive
	msgRPC := &stubMessageRPC{}
	reg := &stubAgentRegistry{lookupErr: agent.ErrAgentNotFound}
	h := agentdispatch.NewNudgeHandler(agentdispatch.NudgeDeps{Tmux: rpc, Message: msgRPC, Registry: reg})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testNudgeJob("docs_bot"), "run-nr", rep, nil)
	if !errors.Is(err, agentdispatch.ErrTargetNotRegistered) {
		t.Errorf("err = %v; want wraps ErrTargetNotRegistered", err)
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("lastState = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "nudge target not in agent registry") {
		t.Errorf("reason = %q; want canonical 'nudge target not in agent registry'",
			rep.lastTransition().reason)
	}
	// Registry was consulted exactly once with the target name.
	if len(reg.lookupCalls) != 1 || reg.lookupCalls[0] != "docs_bot" {
		t.Errorf("Registry.Lookup calls = %v; want [docs_bot]", reg.lookupCalls)
	}
	// MessageSend must NOT fire when the target isn't registered.
	if len(msgRPC.calls) != 0 {
		t.Errorf("MessageSend called %d times; want 0", len(msgRPC.calls))
	}
}

// TestNudgeDispatch_HappyPath_DispatchedToCompleted pins the canonical
// success path per spec §7.2 + AC 9.4.4: alive pane + registered
// agent + successful message send → exactly one MessageSend call
// and a StateCompleted transition with "nudge delivered" reason.
// Spec calls out "dispatched → completed (no running)" — nudge
// state machine skips the StateRunning intermediate state because
// the dispatch IS the work; there's no long-running stage.
func TestNudgeDispatch_HappyPath_DispatchedToCompleted(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: true}
	msgRPC := &stubMessageRPC{returnMessageID: "msg-nudge-1"}
	reg := &stubAgentRegistry{} // lookupErr nil → agent found
	h := agentdispatch.NewNudgeHandler(agentdispatch.NudgeDeps{Tmux: rpc, Message: msgRPC, Registry: reg})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testNudgeJob("docs_bot"), "run-happy", rep, nil)
	if err != nil {
		t.Errorf("Dispatch err = %v; want nil", err)
	}
	if rep.lastTransition().state != scheduler.StateCompleted {
		t.Errorf("lastState = %v; want StateCompleted", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "nudge delivered") {
		t.Errorf("reason = %q; want 'nudge delivered'", rep.lastTransition().reason)
	}
	if len(msgRPC.calls) != 1 {
		t.Fatalf("MessageSend calls = %d; want 1", len(msgRPC.calls))
	}
	call := msgRPC.calls[0]
	if call.target != "docs_bot" {
		t.Errorf("MessageSend target = %q; want docs_bot", call.target)
	}
	if !strings.HasPrefix(call.subject, "Nudge: nudge-job-1") {
		t.Errorf("MessageSend subject = %q; want prefix 'Nudge: nudge-job-1'", call.subject)
	}
	if call.body != "reminder: check the inbox" {
		t.Errorf("MessageSend body = %q; want operator message verbatim", call.body)
	}
	// AC 9.4.4: state machine goes dispatched → delivering → completed
	// WITHOUT StateRunning. Pin by walking the transitions and
	// confirming none of them is StateRunning.
	for i, tr := range rep.transitions {
		if tr.state == scheduler.StateRunning {
			t.Errorf("transitions[%d].state = StateRunning; nudges must skip StateRunning per AC 9.4.4 (transitions: %+v)",
				i, rep.transitions)
		}
	}
	// Stage marker must fire ("delivering") so observability + the
	// stalled-sweep see the in-stage dwell.
	if len(rep.stages) == 0 || rep.stages[0] != "delivering" {
		t.Errorf("stages = %v; want first entry 'delivering'", rep.stages)
	}
}

// TestNudgeDispatch_CheckPaneError_PropagatesAndFails pins the
// distinct error-propagation path: CheckPane returning a real
// error (not just alive=false) surfaces as StateFailed with the
// wrapped error returned from Dispatch. Distinguishes "could not
// determine liveness" from "definitely offline" cleanly so operator
// diagnostics aren't ambiguous.
func TestNudgeDispatch_CheckPaneError_PropagatesAndFails(t *testing.T) {
	wantErr := errors.New("tmux socket gone")
	rpc := &stubTmuxRPC{checkPaneErr: wantErr}
	msgRPC := &stubMessageRPC{}
	reg := &stubAgentRegistry{}
	h := agentdispatch.NewNudgeHandler(agentdispatch.NudgeDeps{Tmux: rpc, Message: msgRPC, Registry: reg})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testNudgeJob("docs_bot"), "run-checkfail", rep, nil)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v; want wraps %v", err, wantErr)
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("lastState = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "liveness check failed") {
		t.Errorf("reason = %q; want substring 'liveness check failed'", rep.lastTransition().reason)
	}
}

// TestNudgeDispatch_RegistryError_PropagatesAndFails pins the
// distinct registry-failure path: a DB-down or transport error
// from registry.Lookup (NOT ErrAgentNotFound) surfaces as
// StateFailed with the wrapped error. Operator can tell from the
// reason string whether the target is missing or the registry is
// degraded.
func TestNudgeDispatch_RegistryError_PropagatesAndFails(t *testing.T) {
	wantErr := errors.New("sqlite busy")
	rpc := &stubTmuxRPC{checkPaneResult: true}
	msgRPC := &stubMessageRPC{}
	reg := &stubAgentRegistry{lookupErr: wantErr}
	h := agentdispatch.NewNudgeHandler(agentdispatch.NudgeDeps{Tmux: rpc, Message: msgRPC, Registry: reg})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testNudgeJob("docs_bot"), "run-regfail", rep, nil)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v; want wraps %v", err, wantErr)
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("lastState = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "registry lookup failed") {
		t.Errorf("reason = %q; want substring 'registry lookup failed'", rep.lastTransition().reason)
	}
	// Message must not have been sent.
	if len(msgRPC.calls) != 0 {
		t.Errorf("MessageSend called %d times; want 0", len(msgRPC.calls))
	}
}

// TestNudgeDispatch_NilNudgeSpec_FailsWithoutPanic pins the
// defensive nil-guard at the API boundary: a JobSpec with a nil
// Nudge sub-tree (validator bypass, direct test invocation) must
// surface as a clean StateFailed + returned error, not a nil-
// deref panic. The validator rejects nil Nudge before dispatch
// in production, but defense-in-depth here is the locked
// expectation.
func TestNudgeDispatch_NilNudgeSpec_FailsWithoutPanic(t *testing.T) {
	rpc := &stubTmuxRPC{checkPaneResult: true}
	msgRPC := &stubMessageRPC{}
	reg := &stubAgentRegistry{}
	h := agentdispatch.NewNudgeHandler(agentdispatch.NudgeDeps{Tmux: rpc, Message: msgRPC, Registry: reg})
	rep := &recReporter{}

	job := scheduler.JobSpec{ID: "broken-job", Type: "nudge", Nudge: nil}
	err := h.Dispatch(context.Background(), job, "run-nil", rep, nil)
	if err == nil {
		t.Fatal("expected error for nil Nudge spec; got nil")
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("lastState = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "missing Nudge field") {
		t.Errorf("reason = %q; want canonical 'missing Nudge field'", rep.lastTransition().reason)
	}
	// Gate calls must NOT fire — guard runs before Stage marker.
	if len(rpc.checkPaneCalls) != 0 {
		t.Errorf("CheckPane called %d times; want 0", len(rpc.checkPaneCalls))
	}
	if len(reg.lookupCalls) != 0 {
		t.Errorf("Registry.Lookup called %d times; want 0", len(reg.lookupCalls))
	}
	if len(msgRPC.calls) != 0 {
		t.Errorf("MessageSend called %d times; want 0", len(msgRPC.calls))
	}
}

// TestNudgeDispatch_MessageSendError_FailsCleanly pins the enqueue
// failure path: a MessageSend error → StateFailed with the wrapped
// error. The fixed registry presence check already passed at this
// point, so the failure reason calls out the enqueue path
// specifically.
func TestNudgeDispatch_MessageSendError_FailsCleanly(t *testing.T) {
	wantErr := errors.New("inbox shard offline")
	rpc := &stubTmuxRPC{checkPaneResult: true}
	msgRPC := &stubMessageRPC{returnErr: wantErr}
	reg := &stubAgentRegistry{}
	h := agentdispatch.NewNudgeHandler(agentdispatch.NudgeDeps{Tmux: rpc, Message: msgRPC, Registry: reg})
	rep := &recReporter{}

	err := h.Dispatch(context.Background(), testNudgeJob("docs_bot"), "run-sendfail", rep, nil)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v; want wraps %v", err, wantErr)
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("lastState = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "nudge enqueue failed") {
		t.Errorf("reason = %q; want substring 'nudge enqueue failed'", rep.lastTransition().reason)
	}
}

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
	// Pin the exact 10s budget — drift to e.g. 1s would break
	// A-B4's stalled-sweep skip-set logic for nudge runs.
	if dur != 10*time.Second {
		t.Errorf("Stages[delivering] = %v; want 10s exactly", dur)
	}
}
