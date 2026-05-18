package agentdispatch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/escalation"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// Test fixtures for idle-nudge loop. Lives in this internal test
// file (package agentdispatch, not agentdispatch_test) so the
// unexported idleNudgeLoop type + onTimerFire method are reachable
// without exporting test-only API.

// idleNudgeRecReporter records every Transition + Stage call. The
// type is local to idle_nudge_test.go rather than reusing the
// _test.go fixture from scheduled_agent_test.go because that one
// lives in package agentdispatch_test (external) — Go does not
// allow internal tests to reference external test types.
type idleNudgeRecReporter struct {
	transitions []idleNudgeRecCall
	stages      []string
}

type idleNudgeRecCall struct {
	state   scheduler.State
	reason  string
	details map[string]any
}

func (r *idleNudgeRecReporter) Transition(s scheduler.State, reason string, details map[string]any) error {
	r.transitions = append(r.transitions, idleNudgeRecCall{state: s, reason: reason, details: details})
	return nil
}

func (r *idleNudgeRecReporter) Stage(name string) error {
	r.stages = append(r.stages, name)
	return nil
}

func (r *idleNudgeRecReporter) lastTransition() idleNudgeRecCall {
	if len(r.transitions) == 0 {
		return idleNudgeRecCall{}
	}
	return r.transitions[len(r.transitions)-1]
}

// idleNudgeStubTmux records PaneInjectPrompt calls — the loop's
// only TmuxRPC method usage. Other methods are no-op stubs so the
// interface is satisfied without bloating the fixture.
type idleNudgeStubTmux struct {
	injectErr   error
	injectCalls []idleNudgeInjectCall
}

// idleNudgeInjectCall is local to this internal test file. The
// twin type in scheduled_agent_test.go (package agentdispatch_test
// — external) is unreachable from here because Go enforces
// internal/external test-package separation.
type idleNudgeInjectCall struct {
	target string
	text   string
}

func (s *idleNudgeStubTmux) CheckPane(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (s *idleNudgeStubTmux) TmuxCreate(_ context.Context, _ string, _ TmuxCreateOpts) error {
	return nil
}
func (s *idleNudgeStubTmux) TmuxLaunch(_ context.Context, _ string) error        { return nil }
func (s *idleNudgeStubTmux) WaitForPaneReady(_ context.Context, _ string) error  { return nil }
func (s *idleNudgeStubTmux) TmuxKillSession(_ context.Context, _ string) error   { return nil }
func (s *idleNudgeStubTmux) PaneSendCtrlCExit(_ context.Context, _ string) error { return nil }
func (s *idleNudgeStubTmux) PaneInjectPrompt(_ context.Context, target, text string) error {
	s.injectCalls = append(s.injectCalls, idleNudgeInjectCall{target: target, text: text})
	return s.injectErr
}

// idleNudgeStubRouter records Route invocations + returns canned
// errors. Used for Layer-D escalation tests.
type idleNudgeStubRouter struct {
	returnErr error
	calls     []idleNudgeRouterCall
}

type idleNudgeRouterCall struct {
	alert   escalation.Alert
	subject string
	body    string
}

func (s *idleNudgeStubRouter) Route(_ context.Context, alert escalation.Alert, subject, body string) error {
	s.calls = append(s.calls, idleNudgeRouterCall{alert: alert, subject: subject, body: body})
	return s.returnErr
}

// newTestIdleNudgeLoop constructs a loop wired with safe defaults
// for the most-common test path. Tests override individual fields
// (probe, maxNudges, etc.) by field assignment after the call.
// The timer is set to a long deadline so a re-arm by onTimerFire
// REPLACES the pending fire rather than racing it — keeps tests
// deterministic without sleeping.
func newTestIdleNudgeLoop(probe func(context.Context, string) (time.Time, error)) (*idleNudgeLoop, *idleNudgeStubTmux, *idleNudgeStubRouter) {
	tmux := &idleNudgeStubTmux{}
	router := &idleNudgeStubRouter{}
	loop := &idleNudgeLoop{
		target:           "docs_bot",
		runID:            "run-test",
		idleSeconds:      60,
		maxNudges:        3,
		lastPaneActivity: time.Now().Add(-time.Hour), // older than any probe will return
		timer:            time.NewTimer(time.Hour),
		tmux:             tmux,
		escalation:       router,
		probe:            probe,
	}
	return loop, tmux, router
}

// TestIdleNudgeLoop_FiresAtIdleThreshold pins AC 9.5.2: when the
// pane has been silent (probe activity timestamp older than
// lastPaneActivity), one nudge fires, the counter increments, and
// the prompt is injected into the pane.
func TestIdleNudgeLoop_FiresAtIdleThreshold(t *testing.T) {
	silentProbe := func(_ context.Context, _ string) (time.Time, error) {
		// Activity older than lastPaneActivity → pane was silent.
		return time.Now().Add(-2 * time.Hour), nil
	}
	loop, tmux, _ := newTestIdleNudgeLoop(silentProbe)
	defer loop.timer.Stop()
	rep := &idleNudgeRecReporter{}

	if err := loop.onTimerFire(context.Background(), rep); err != nil {
		t.Fatalf("onTimerFire err = %v; want nil (first fire pre-exhaustion)", err)
	}
	if loop.nudgesFired != 1 {
		t.Errorf("nudgesFired = %d; want 1", loop.nudgesFired)
	}
	if len(tmux.injectCalls) != 1 {
		t.Fatalf("PaneInjectPrompt calls = %d; want 1", len(tmux.injectCalls))
	}
	if tmux.injectCalls[0].target != "docs_bot" {
		t.Errorf("inject target = %q; want docs_bot", tmux.injectCalls[0].target)
	}
	if !strings.Contains(tmux.injectCalls[0].text, "Nudge 1 of 3") {
		t.Errorf("inject text missing 'Nudge 1 of 3': %q", tmux.injectCalls[0].text)
	}
	// Stage marker for AC 9.5.6 + canonical §2.2 — "idle_nudge_1of3"
	if len(rep.stages) == 0 || rep.stages[0] != "idle_nudge_1of3" {
		t.Errorf("stages = %v; want first entry 'idle_nudge_1of3'", rep.stages)
	}
}

// TestIdleNudgeLoop_PaneActivityResetsTimer pins AC 9.5.3: when
// the probe reports activity newer than lastPaneActivity, the
// loop interprets the window as not-yet-silent — no nudge fires,
// lastPaneActivity advances, and the timer re-arms with the 2s
// settle added (per Task 40 + feedback_byte_equality_pane_detection
// memory — runtime needs time to finish painting).
func TestIdleNudgeLoop_PaneActivityResetsTimer(t *testing.T) {
	freshActivity := time.Now()
	activeProbe := func(_ context.Context, _ string) (time.Time, error) {
		return freshActivity, nil
	}
	loop, tmux, router := newTestIdleNudgeLoop(activeProbe)
	defer loop.timer.Stop()
	loop.lastPaneActivity = freshActivity.Add(-30 * time.Second) // pre-window
	rep := &idleNudgeRecReporter{}

	if err := loop.onTimerFire(context.Background(), rep); err != nil {
		t.Fatalf("onTimerFire err = %v; want nil (active window)", err)
	}
	if loop.nudgesFired != 0 {
		t.Errorf("nudgesFired = %d; want 0 (pane was active)", loop.nudgesFired)
	}
	if len(tmux.injectCalls) != 0 {
		t.Errorf("PaneInjectPrompt called %d times; want 0 (no nudge on active window)",
			len(tmux.injectCalls))
	}
	if len(router.calls) != 0 {
		t.Errorf("Route called %d times; want 0 (active window)", len(router.calls))
	}
	if !loop.lastPaneActivity.Equal(freshActivity) {
		t.Errorf("lastPaneActivity = %v; want updated to %v", loop.lastPaneActivity, freshActivity)
	}
	if len(rep.transitions) != 0 {
		t.Errorf("transitions = %d; want 0 (active window → no state change)", len(rep.transitions))
	}
}

// TestIdleNudgeLoop_SettleSecondsAppliedOnActivity pins the Task 40
// invariant: when activity resets the timer, the new deadline is
// idleSeconds + 2s (idleNudgeSettleSeconds), not just idleSeconds.
// Verified by inspecting the re-armed timer's expiry — the loop
// uses time.NewTimer/Reset so we can check whether the next fire
// is at the expected horizon. Done by checking the wall-clock
// elapsed-to-next-fire window via timer.Stop() return + a
// measurement.
//
// Implementation: we override the timer with a short-fuse value
// before calling onTimerFire, then check loop.timer.C drain
// timing.
func TestIdleNudgeLoop_SettleSecondsAppliedOnActivity(t *testing.T) {
	probe := func(_ context.Context, _ string) (time.Time, error) {
		return time.Now(), nil // current activity
	}
	loop, _, _ := newTestIdleNudgeLoop(probe)
	defer loop.timer.Stop()
	loop.idleSeconds = 1 // short so the test runs in ~3s
	loop.lastPaneActivity = time.Now().Add(-30 * time.Second)
	rep := &idleNudgeRecReporter{}

	// First fire: probe returns active. Loop should reset timer to
	// 1s + 2s settle = 3s.
	start := time.Now()
	if err := loop.onTimerFire(context.Background(), rep); err != nil {
		t.Fatalf("onTimerFire err = %v", err)
	}

	// Wait for the timer to fire at the settled horizon. The
	// generous upper bound (5s) absorbs scheduler jitter on CI;
	// the meaningful assertion is that the fire happens AFTER
	// the bare idleSeconds (1s), proving the settle was applied.
	select {
	case <-loop.timer.C:
		elapsed := time.Since(start)
		if elapsed < 2500*time.Millisecond {
			t.Errorf("timer fired in %v; want >= 2.5s (idleSeconds + 2s settle - jitter)", elapsed)
		}
		if elapsed > 5*time.Second {
			t.Errorf("timer fired in %v; want <= 5s (settle window exceeded)", elapsed)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("timer never fired within 6s; settle window appears unbounded")
	}
}

// TestIdleNudgeLoop_ExhaustionTriggersLayerD pins the canonical
// Layer-D contract per spec §6.2 + AC 9.5.4: when nudgesFired
// reaches maxNudges, the loop transitions to StateFailed with the
// escalation_emitted_by marker, calls Route, and returns
// ErrIdleNudgeExhausted so Dispatch closes.
func TestIdleNudgeLoop_ExhaustionTriggersLayerD(t *testing.T) {
	silentProbe := func(_ context.Context, _ string) (time.Time, error) {
		return time.Now().Add(-2 * time.Hour), nil
	}
	loop, tmux, router := newTestIdleNudgeLoop(silentProbe)
	defer loop.timer.Stop()
	loop.maxNudges = 2
	loop.nudgesFired = 1 // one fire away from exhaustion
	rep := &idleNudgeRecReporter{}

	err := loop.onTimerFire(context.Background(), rep)
	if !errors.Is(err, ErrIdleNudgeExhausted) {
		t.Errorf("err = %v; want wraps ErrIdleNudgeExhausted", err)
	}
	if loop.nudgesFired != 2 {
		t.Errorf("nudgesFired = %d; want 2", loop.nudgesFired)
	}

	// Layer-D transition: StateFailed with marker.
	tr := rep.lastTransition()
	if tr.state != scheduler.StateFailed {
		t.Errorf("last state = %v; want StateFailed", tr.state)
	}
	if tr.details["escalation_emitted_by"] != "b-b1.idle_nudge" {
		t.Errorf("details[escalation_emitted_by] = %v; want 'b-b1.idle_nudge' (canonical §6.2 marker)",
			tr.details["escalation_emitted_by"])
	}
	if tr.details["nudges_fired"] != 2 {
		t.Errorf("details[nudges_fired] = %v; want 2", tr.details["nudges_fired"])
	}

	// Operator escalation routed via the canonical helper.
	if len(router.calls) != 1 {
		t.Fatalf("Route calls = %d; want 1", len(router.calls))
	}
	if router.calls[0].alert.Source != "b-b1.idle_nudge" {
		t.Errorf("Route alert.Source = %q; want b-b1.idle_nudge", router.calls[0].alert.Source)
	}
	if router.calls[0].alert.AgentName != "docs_bot" {
		t.Errorf("Route alert.AgentName = %q; want docs_bot", router.calls[0].alert.AgentName)
	}
	// Layer-D fire does NOT inject a nudge prompt (the runtime is
	// being given up on, not nudged).
	if len(tmux.injectCalls) != 0 {
		t.Errorf("PaneInjectPrompt called %d times on Layer-D; want 0", len(tmux.injectCalls))
	}

	// Stage marker still fires for the canonical N-of-M emit at
	// exhaustion (per AC 9.5.6).
	if len(rep.stages) == 0 || rep.stages[0] != "idle_nudge_2of2" {
		t.Errorf("stages = %v; want 'idle_nudge_2of2'", rep.stages)
	}
}

// TestIdleNudgeLoop_NilEscalation_StillFailsWithMarker pins the I3
// forward-compat invariant: when Escalation isn't wired (partial-
// config deployment), Layer-D still transitions the run to
// StateFailed with the marker and returns ErrIdleNudgeExhausted.
// Only the operator-facing alert is skipped — the substrate's
// bookkeeping is correct so A-B1's evaluator-side suppression
// works unchanged.
func TestIdleNudgeLoop_NilEscalation_StillFailsWithMarker(t *testing.T) {
	silentProbe := func(_ context.Context, _ string) (time.Time, error) {
		return time.Now().Add(-2 * time.Hour), nil
	}
	loop, _, _ := newTestIdleNudgeLoop(silentProbe)
	defer loop.timer.Stop()
	loop.escalation = nil // partial-config
	loop.maxNudges = 1
	rep := &idleNudgeRecReporter{}

	err := loop.onTimerFire(context.Background(), rep)
	if !errors.Is(err, ErrIdleNudgeExhausted) {
		t.Errorf("err = %v; want wraps ErrIdleNudgeExhausted", err)
	}
	tr := rep.lastTransition()
	if tr.state != scheduler.StateFailed {
		t.Errorf("last state = %v; want StateFailed", tr.state)
	}
	if tr.details["escalation_emitted_by"] != "b-b1.idle_nudge" {
		t.Errorf("details[escalation_emitted_by] = %v; want 'b-b1.idle_nudge'",
			tr.details["escalation_emitted_by"])
	}
}

// TestIdleNudgeLoop_ProbeErrorBudget pins the consecutive-error
// budget: a single probe error increments the counter + re-arms;
// at idleNudgeMaxProbeErrors (3) the loop concludes the runtime
// is gone and StateFailed with the probe-error count in details.
func TestIdleNudgeLoop_ProbeErrorBudget(t *testing.T) {
	failingProbe := func(_ context.Context, _ string) (time.Time, error) {
		return time.Time{}, errors.New("tmux socket gone")
	}
	loop, _, _ := newTestIdleNudgeLoop(failingProbe)
	defer loop.timer.Stop()
	rep := &idleNudgeRecReporter{}

	// First two errors: absorbed.
	for i := 1; i <= 2; i++ {
		if err := loop.onTimerFire(context.Background(), rep); err != nil {
			t.Errorf("onTimerFire fire %d: err = %v; want nil (within budget)", i, err)
		}
	}
	if loop.consecutiveProbeErrors != 2 {
		t.Errorf("consecutiveProbeErrors = %d after 2 errors; want 2", loop.consecutiveProbeErrors)
	}
	if len(rep.transitions) != 0 {
		t.Errorf("transitions = %d after 2 errors; want 0 (within budget)", len(rep.transitions))
	}

	// Third error: trips the budget.
	err := loop.onTimerFire(context.Background(), rep)
	if err == nil {
		t.Fatal("err = nil after 3rd probe error; want budget-exhausted error")
	}
	tr := rep.lastTransition()
	if tr.state != scheduler.StateFailed {
		t.Errorf("last state = %v; want StateFailed", tr.state)
	}
	if !strings.Contains(tr.reason, "probe failed consecutively") {
		t.Errorf("reason = %q; want canonical 'probe failed consecutively'", tr.reason)
	}
	if tr.details["probe_errors"] != 3 {
		t.Errorf("details[probe_errors] = %v; want 3", tr.details["probe_errors"])
	}
}

// TestIdleNudgeLoop_ProbeRecoveryResetsErrorCounter pins the
// recovery-after-transient-error invariant: a single failing
// probe increments the counter, but a subsequent successful
// probe resets it to 0. Without this, a single stale error
// during long idle windows would eventually trip the budget for
// otherwise-healthy runs.
func TestIdleNudgeLoop_ProbeRecoveryResetsErrorCounter(t *testing.T) {
	calls := 0
	flakyProbe := func(_ context.Context, _ string) (time.Time, error) {
		calls++
		if calls == 1 {
			return time.Time{}, errors.New("transient")
		}
		// Second call onward: succeed with current activity (active window).
		return time.Now(), nil
	}
	loop, _, _ := newTestIdleNudgeLoop(flakyProbe)
	defer loop.timer.Stop()
	loop.lastPaneActivity = time.Now().Add(-time.Hour)
	rep := &idleNudgeRecReporter{}

	// First fire: probe error → counter 1.
	if err := loop.onTimerFire(context.Background(), rep); err != nil {
		t.Errorf("fire 1 err = %v; want nil (within budget)", err)
	}
	if loop.consecutiveProbeErrors != 1 {
		t.Errorf("consecutiveProbeErrors = %d after fire 1; want 1", loop.consecutiveProbeErrors)
	}

	// Second fire: probe succeeds → counter resets.
	if err := loop.onTimerFire(context.Background(), rep); err != nil {
		t.Errorf("fire 2 err = %v; want nil", err)
	}
	if loop.consecutiveProbeErrors != 0 {
		t.Errorf("consecutiveProbeErrors = %d after recovery; want 0", loop.consecutiveProbeErrors)
	}
}

// TestIdleNudgeLoop_FreshDispatchFiresOnFirstSilentWindow pins
// the production initialization path: scheduled_agent.go sets
// lastPaneActivity to time.Now() at loop construction (line ~494),
// so a probe returning activity <= time.Now() means the pane was
// silent during the first idle window → nudge fires on the very
// first timer pop.
//
// The default test fixture (newTestIdleNudgeLoop) backdates
// lastPaneActivity by an hour so EVERY probe looks fresh; that
// pattern is convenient for happy-path tests but doesn't exercise
// the production "lastPaneActivity == start time" boundary. This
// test pins that boundary directly.
func TestIdleNudgeLoop_FreshDispatchFiresOnFirstSilentWindow(t *testing.T) {
	now := time.Now()
	silentProbe := func(_ context.Context, _ string) (time.Time, error) {
		// Probe returns activity that's older than lastPaneActivity
		// (the production init time) — equivalent to "no new tmux
		// pane updates during the idle window."
		return now.Add(-30 * time.Second), nil
	}
	loop, tmux, _ := newTestIdleNudgeLoop(silentProbe)
	defer loop.timer.Stop()
	// Reset lastPaneActivity to the production init pattern: same
	// instant as construction (a "fresh" loop with no prior activity
	// observation).
	loop.lastPaneActivity = now
	rep := &idleNudgeRecReporter{}

	if err := loop.onTimerFire(context.Background(), rep); err != nil {
		t.Fatalf("onTimerFire err = %v; want nil (first fire pre-exhaustion)", err)
	}
	if loop.nudgesFired != 1 {
		t.Errorf("nudgesFired = %d; want 1 (fresh dispatch + silent probe → first fire)",
			loop.nudgesFired)
	}
	if len(tmux.injectCalls) != 1 {
		t.Errorf("PaneInjectPrompt calls = %d; want 1", len(tmux.injectCalls))
	}
}

// TestIdleNudgePrompt_ContainsCanonicalMarkers pins AC 9.5.7
// (distinguishable markers): the operator-visible prompt body
// includes "thrum job done" + the N-of-M counter so the agent
// knows what action closes the run cleanly + how close it is to
// Layer-D escalation.
func TestIdleNudgePrompt_ContainsCanonicalMarkers(t *testing.T) {
	prompt := idleNudgePrompt(2, 5)
	for _, want := range []string{"Idle detection", "thrum job done", "Nudge 2 of 5"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q substring; got: %q", want, prompt)
		}
	}
}
