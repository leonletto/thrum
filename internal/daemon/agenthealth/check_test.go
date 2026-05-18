package agenthealth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/agenthealth"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// Test fixtures: stub Registry, Prober, Respawner, Reporter.
// Local to this package (external test) because the agentdispatch
// stubs live in their own _test package and can't be reused.

type stubRegistry struct {
	agents []agent.Agent
	err    error
	calls  int
}

func (s *stubRegistry) Lookup(_ context.Context, _ string) (agent.Agent, error) {
	return agent.Agent{}, nil
}
func (s *stubRegistry) ListAutoRespawnEnabled(_ context.Context) ([]agent.Agent, error) {
	s.calls++
	return s.agents, s.err
}
func (s *stubRegistry) SetAutoRespawnDisabledAt(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (s *stubRegistry) ClearAutoRespawnDisabledAt(_ context.Context, _ string) error {
	return nil
}
func (s *stubRegistry) SetStateMdParseFailedAt(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (s *stubRegistry) ClearStateMdParseFailedAt(_ context.Context, _ string) error {
	return nil
}

// stubProber returns a per-agent alive map. Agents not present in
// the map default to alive=true so test setup is concise — explicit
// false-set means "pane is gone.".
type stubProber struct {
	alive map[string]bool
	err   map[string]error
	calls []string
}

func (s *stubProber) CheckPane(_ context.Context, target string) (bool, error) {
	s.calls = append(s.calls, target)
	if e, ok := s.err[target]; ok {
		return false, e
	}
	if a, ok := s.alive[target]; ok {
		return a, nil
	}
	return true, nil
}

type stubRespawner struct {
	err   map[string]error
	calls []respawnCall
}

type respawnCall struct {
	agent     string
	detection state.DetectionMethod
}

func (s *stubRespawner) OnPaneGone(_ context.Context, agentName string, detection state.DetectionMethod) error {
	s.calls = append(s.calls, respawnCall{agent: agentName, detection: detection})
	if e, ok := s.err[agentName]; ok {
		return e
	}
	return nil
}

// stubReporter records every Stage + Transition call. Mirrors the
// shape used by other agentdispatch tests for the canonical
// scheduler.StateReporter contract.
type stubReporter struct {
	stages      []string
	transitions []transitionCall
}

type transitionCall struct {
	state   scheduler.State
	reason  string
	details map[string]any
}

func (r *stubReporter) Stage(name string) error {
	r.stages = append(r.stages, name)
	return nil
}
func (r *stubReporter) Transition(s scheduler.State, reason string, details map[string]any) error {
	r.transitions = append(r.transitions, transitionCall{state: s, reason: reason, details: details})
	return nil
}

func (r *stubReporter) lastTransition() transitionCall {
	if len(r.transitions) == 0 {
		return transitionCall{}
	}
	return r.transitions[len(r.transitions)-1]
}

func mkAgent(id string) agent.Agent {
	return agent.Agent{
		AgentID:            id,
		Mode:               agent.ModePersistent,
		Identity:           agent.IdentityLongLived,
		AutoRespawnEnabled: true,
	}
}

// TestCheckHandler_PaneAlive_NoRespawn pins the canonical happy
// path: when every probed pane is alive, no OnPaneGone fires.
// The handler completes with the scanned-agent count for
// observability.
func TestCheckHandler_PaneAlive_NoRespawn(t *testing.T) {
	reg := &stubRegistry{agents: []agent.Agent{mkAgent("docs_bot"), mkAgent("ops_bot")}}
	prober := &stubProber{} // empty map → all alive
	resp := &stubRespawner{}
	h := agenthealth.New(reg, prober, resp, nil)
	rep := &stubReporter{}

	if err := h.Dispatch(context.Background(), scheduler.JobSpec{}, "run-1", rep, nil); err != nil {
		t.Fatalf("Dispatch err = %v; want nil", err)
	}
	if len(prober.calls) != 2 {
		t.Errorf("CheckPane calls = %v; want 2 (one per listed agent)", prober.calls)
	}
	if len(resp.calls) != 0 {
		t.Errorf("OnPaneGone calls = %d; want 0 (all panes alive)", len(resp.calls))
	}
	tr := rep.lastTransition()
	if tr.state != scheduler.StateCompleted {
		t.Errorf("last state = %v; want StateCompleted", tr.state)
	}
	if tr.details["scanned"] != 2 {
		t.Errorf("details[scanned] = %v; want 2", tr.details["scanned"])
	}
	if tr.details["respawns_triggered"] != 0 {
		t.Errorf("details[respawns_triggered] = %v; want 0", tr.details["respawns_triggered"])
	}
	// Stage marker must fire as the single canonical stage.
	if len(rep.stages) != 1 || rep.stages[0] != agenthealth.StageCheckName {
		t.Errorf("stages = %v; want [%q]", rep.stages, agenthealth.StageCheckName)
	}
}

// TestCheckHandler_PaneGone_FiresRespawn pins the canonical
// crash-detection trigger: an agent whose pane probe returns
// false routes through Respawner.OnPaneGone with
// DetectionHealthCheckTick. Alive panes in the same tick are
// untouched.
func TestCheckHandler_PaneGone_FiresRespawn(t *testing.T) {
	reg := &stubRegistry{agents: []agent.Agent{mkAgent("docs_bot"), mkAgent("ops_bot")}}
	prober := &stubProber{alive: map[string]bool{"docs_bot": false}} // ops_bot defaults to alive
	resp := &stubRespawner{}
	h := agenthealth.New(reg, prober, resp, nil)
	rep := &stubReporter{}

	if err := h.Dispatch(context.Background(), scheduler.JobSpec{}, "run-1", rep, nil); err != nil {
		t.Fatalf("Dispatch err = %v; want nil", err)
	}
	if len(resp.calls) != 1 {
		t.Fatalf("OnPaneGone calls = %d; want 1", len(resp.calls))
	}
	if resp.calls[0].agent != "docs_bot" {
		t.Errorf("OnPaneGone target = %q; want docs_bot", resp.calls[0].agent)
	}
	if resp.calls[0].detection != state.DetectionHealthCheckTick {
		t.Errorf("OnPaneGone detection = %q; want health_check_tick",
			resp.calls[0].detection)
	}
	if rep.lastTransition().details["respawns_triggered"] != 1 {
		t.Errorf("details[respawns_triggered] = %v; want 1",
			rep.lastTransition().details["respawns_triggered"])
	}
}

// TestCheckHandler_ProbeError_SkipsAgentNoRespawn pins the probe-
// error contract: a transient CheckPane error means "skip this
// agent for the tick" — NOT "fire respawn." A probe error doesn't
// prove the agent is gone; firing respawn on every probe glitch
// would cause spurious crash-loop trips.
func TestCheckHandler_ProbeError_SkipsAgentNoRespawn(t *testing.T) {
	reg := &stubRegistry{agents: []agent.Agent{mkAgent("docs_bot")}}
	prober := &stubProber{err: map[string]error{"docs_bot": errors.New("tmux socket gone")}}
	resp := &stubRespawner{}
	h := agenthealth.New(reg, prober, resp, nil)
	rep := &stubReporter{}

	if err := h.Dispatch(context.Background(), scheduler.JobSpec{}, "run-1", rep, nil); err != nil {
		t.Fatalf("Dispatch err = %v; want nil (probe errors logged + skipped)", err)
	}
	if len(resp.calls) != 0 {
		t.Errorf("OnPaneGone calls = %d; want 0 (probe error must not fire respawn)",
			len(resp.calls))
	}
	// Dispatch completes — one probe error doesn't kill the tick.
	if rep.lastTransition().state != scheduler.StateCompleted {
		t.Errorf("last state = %v; want StateCompleted", rep.lastTransition().state)
	}
}

// TestCheckHandler_RegistryError_BubblesUp pins the only error
// path that surfaces as Dispatch failure: a registry-list error
// (DB unreachable) means we can't iterate at all, so the tick
// transitions to StateFailed + returns the wrapped error. Future
// ticks retry from scratch (idempotent — no partial state to
// reconcile).
func TestCheckHandler_RegistryError_BubblesUp(t *testing.T) {
	regErr := errors.New("sqlite busy")
	reg := &stubRegistry{err: regErr}
	prober := &stubProber{}
	resp := &stubRespawner{}
	h := agenthealth.New(reg, prober, resp, nil)
	rep := &stubReporter{}

	err := h.Dispatch(context.Background(), scheduler.JobSpec{}, "run-1", rep, nil)
	if !errors.Is(err, regErr) {
		t.Errorf("err = %v; want wraps %v", err, regErr)
	}
	if rep.lastTransition().state != scheduler.StateFailed {
		t.Errorf("last state = %v; want StateFailed", rep.lastTransition().state)
	}
	if !strings.Contains(rep.lastTransition().reason, "list agents") {
		t.Errorf("reason = %q; want substring 'list agents'", rep.lastTransition().reason)
	}
}

// TestCheckHandler_RespawnError_ContinuesLoop pins the per-agent
// error isolation contract: one agent's OnPaneGone failure must
// NOT break the loop — subsequent agents in the batch still get
// probed + respawned. Operator sees the failed agent in logs +
// the per-agent crash_detected event was still written.
func TestCheckHandler_RespawnError_ContinuesLoop(t *testing.T) {
	reg := &stubRegistry{agents: []agent.Agent{mkAgent("docs_bot"), mkAgent("ops_bot")}}
	prober := &stubProber{alive: map[string]bool{"docs_bot": false, "ops_bot": false}}
	resp := &stubRespawner{err: map[string]error{"docs_bot": errors.New("registry down")}}
	h := agenthealth.New(reg, prober, resp, nil)
	rep := &stubReporter{}

	if err := h.Dispatch(context.Background(), scheduler.JobSpec{}, "run-1", rep, nil); err != nil {
		t.Fatalf("Dispatch err = %v; want nil (per-agent errors logged + continued)", err)
	}
	// Both agents probed.
	if len(prober.calls) != 2 {
		t.Errorf("CheckPane calls = %v; want 2", prober.calls)
	}
	// Both OnPaneGone fires (docs_bot errors, ops_bot succeeds).
	if len(resp.calls) != 2 {
		t.Errorf("OnPaneGone calls = %d; want 2", len(resp.calls))
	}
	// respawns_triggered counts only successful evaluations.
	if rep.lastTransition().details["respawns_triggered"] != 1 {
		t.Errorf("details[respawns_triggered] = %v; want 1 (docs_bot errored)",
			rep.lastTransition().details["respawns_triggered"])
	}
}

// cancellingProber cancels the supplied ctx the first time it sees a
// probe call, simulating the realistic mid-loop cancel scenario:
// the registry list completes successfully, the first probe runs,
// then ctx is cancelled (daemon shutdown / scheduler stop). The
// loop's ctx.Err() check on subsequent iterations must short-
// circuit gracefully — NOT mark the whole tick failed and NOT
// keep probing.
type cancellingProber struct {
	cancel    context.CancelFunc
	cancelled bool
	calls     []string
}

func (c *cancellingProber) CheckPane(_ context.Context, target string) (bool, error) {
	c.calls = append(c.calls, target)
	if !c.cancelled {
		c.cancelled = true
		c.cancel() // fire the cancel AFTER recording this probe
	}
	return true, nil // first agent appears alive; we don't care
}

// TestCheckHandler_CtxCancelMidScan_GracefulExit pins the
// cancellation-discipline contract for the REAL mid-loop case: the
// registry list succeeds, the first probe runs, then ctx is
// cancelled (daemon shutdown / scheduler stop). The loop's
// ctx.Err() check on the SECOND iteration must short-circuit
// gracefully — completing the tick rather than failing it, and
// NOT probing agents 2 and 3. Next tick re-scans from scratch
// (idempotent).
//
// This is structured to exercise the real ctx.Err() check —
// pre-cancelling before Dispatch would only verify the loop's
// stub-receptive behavior since stubs ignore ctx; cancelling
// AFTER the first probe exercises the production path that
// actually consults ctx mid-loop.
func TestCheckHandler_CtxCancelMidScan_GracefulExit(t *testing.T) {
	reg := &stubRegistry{agents: []agent.Agent{
		mkAgent("agent_1"), mkAgent("agent_2"), mkAgent("agent_3"),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	prober := &cancellingProber{cancel: cancel}
	resp := &stubRespawner{}
	h := agenthealth.New(reg, prober, resp, nil)
	rep := &stubReporter{}

	if err := h.Dispatch(ctx, scheduler.JobSpec{}, "run-1", rep, nil); err != nil {
		t.Errorf("Dispatch err = %v; want nil (cancel mid-scan is graceful)", err)
	}
	if rep.lastTransition().state != scheduler.StateCompleted {
		t.Errorf("last state = %v; want StateCompleted (graceful exit)",
			rep.lastTransition().state)
	}
	// The mid-loop short-circuit: only ONE agent should have been
	// probed before the ctx.Err() check kicked in on the next
	// iteration. Agents 2 + 3 must be untouched.
	if len(prober.calls) != 1 {
		t.Errorf("probe calls = %v; want exactly 1 (mid-loop short-circuit on agent 2)",
			prober.calls)
	}
	if prober.calls[0] != "agent_1" {
		t.Errorf("first probe = %q; want agent_1", prober.calls[0])
	}
}

// TestCheckHandler_Stages_DeclaresCheckNameWith30sBudget pins the
// single-stage budget contract. Drift in either the stage name
// or duration would silently break A-B4's stalled-sweep skip-set
// integration (the sweep keys off
// scheduler_job_state.current_stage = "pane-health-check").
func TestCheckHandler_Stages_DeclaresCheckNameWith30sBudget(t *testing.T) {
	h := agenthealth.New(nil, nil, nil, nil)
	stages := h.Stages()
	if len(stages) != 1 {
		t.Fatalf("Stages = %d entries; want 1", len(stages))
	}
	got, ok := stages[agenthealth.StageCheckName]
	if !ok {
		t.Fatalf("Stages missing %q key", agenthealth.StageCheckName)
	}
	if got != 30*time.Second {
		t.Errorf("Stages[%q] = %v; want 30s", agenthealth.StageCheckName, got)
	}
}

// TestCheckHandler_Reconcile_NoOpReturnsCompleted pins the
// reconciliation contract: the check is idempotent (next tick
// re-scans), so a daemon-crash mid-tick is harmless. Reconcile
// returns StateCompleted to clear the non-terminal row at boot.
func TestCheckHandler_Reconcile_NoOpReturnsCompleted(t *testing.T) {
	h := agenthealth.New(nil, nil, nil, nil)
	got, err := h.Reconcile(context.Background(), scheduler.JobSpec{}, "run-1", scheduler.StateRunning)
	if err != nil {
		t.Errorf("Reconcile err = %v; want nil", err)
	}
	if got != scheduler.StateCompleted {
		t.Errorf("got = %v; want StateCompleted", got)
	}
}

// TestCheckHandler_EmptyRegistry_NoProbeNoRespawn pins the trivial
// path: zero eligible agents means zero CheckPane calls + zero
// OnPaneGone calls + clean completion. Catches a regression
// where the iteration logic doesn't handle empty slices cleanly.
func TestCheckHandler_EmptyRegistry_NoProbeNoRespawn(t *testing.T) {
	reg := &stubRegistry{agents: nil}
	prober := &stubProber{}
	resp := &stubRespawner{}
	h := agenthealth.New(reg, prober, resp, nil)
	rep := &stubReporter{}

	if err := h.Dispatch(context.Background(), scheduler.JobSpec{}, "run-1", rep, nil); err != nil {
		t.Fatalf("Dispatch err = %v; want nil", err)
	}
	if len(prober.calls) != 0 || len(resp.calls) != 0 {
		t.Errorf("expected no probe/respawn calls; got probe=%v respawn=%d",
			prober.calls, len(resp.calls))
	}
	if rep.lastTransition().details["scanned"] != 0 {
		t.Errorf("details[scanned] = %v; want 0", rep.lastTransition().details["scanned"])
	}
}
