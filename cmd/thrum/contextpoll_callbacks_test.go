package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/contextpoll"
)

// fakeNudger records every SendSystemNudge call so tests can assert both
// fire-count (.15 IsAutoDisabled gating) and body shape (.14 plan §3.4.x text).
type fakeNudger struct {
	mu    sync.Mutex
	calls []nudgeCall
}

type nudgeCall struct {
	recipient string
	body      string
}

func (f *fakeNudger) SendSystemNudge(_ context.Context, recipient, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, nudgeCall{recipient: recipient, body: body})
}

func (f *fakeNudger) recordedCalls() []nudgeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]nudgeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// fakeRestartTrigger records every Restart call so tests can assert on the
// recipient + reason text. ReturnErr lets tests cover the "trigger fails"
// path (CR.4 T4.2 — OnFire must log + survive without panicking when the
// underlying RestartSession returns an error).
type fakeRestartTrigger struct {
	mu        sync.Mutex
	calls     []triggerCall
	returnErr error
}

type triggerCall struct {
	agentName string
	reason    string
}

func (f *fakeRestartTrigger) Restart(_ context.Context, agentName, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, triggerCall{agentName: agentName, reason: reason})
	return f.returnErr
}

func (f *fakeRestartTrigger) recordedCalls() []triggerCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]triggerCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// TestContextPollCallbacks_WarnBody covers thrum-6qmf.1.14 acceptance criteria
// 1-3 (plan lines 549-551): the warn body contains the current percentage,
// the "do NOT dispatch sub-agents" prohibition, and references the
// AutoThreshold so the agent knows when force-fire will hit.
func TestContextPollCallbacks_WarnBody(t *testing.T) {
	cfg := config.RestartConfig{WarnThreshold: 70, AutoThreshold: 80}
	nudger := &fakeNudger{}
	onWarn, _, _ := buildContextPollCallbacks(cfg, nudger, nil)

	onWarn(context.Background(), "test_agent", contextpoll.ContextUsage{UsedPercentage: 72})

	calls := nudger.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("warn should fire once, got %d calls", len(calls))
	}
	if calls[0].recipient != "test_agent" {
		t.Errorf("recipient = %q, want %q", calls[0].recipient, "test_agent")
	}
	body := calls[0].body
	if !strings.Contains(body, "Context at 72%") {
		t.Errorf("warn body missing percentage substitution; body=%q", body)
	}
	if !strings.Contains(body, "Do NOT dispatch sub-agents (Agent, Explore, etc.)") {
		t.Errorf("warn body missing sub-agent prohibition; body=%q", body)
	}
	if !strings.Contains(body, "Do NOT re-read large files") {
		t.Errorf("warn body missing re-read prohibition; body=%q", body)
	}
	if !strings.Contains(body, "self-restart by 80%") {
		t.Errorf("warn body missing AutoThreshold reference; body=%q", body)
	}
	if !strings.Contains(body, "/thrum:restart") {
		t.Errorf("warn body missing /thrum:restart pointer; body=%q", body)
	}
}

// TestContextPollCallbacks_PreFireBody covers thrum-6qmf.1.14 acceptance
// criterion 3 (plan line 551): the pre-fire body contains "three minutes".
func TestContextPollCallbacks_PreFireBody(t *testing.T) {
	cfg := config.RestartConfig{}
	nudger := &fakeNudger{}
	_, onPreFire, _ := buildContextPollCallbacks(cfg, nudger, nil)

	onPreFire(context.Background(), "test_agent", contextpoll.ContextUsage{UsedPercentage: 82})

	calls := nudger.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("pre-fire should fire once, got %d calls", len(calls))
	}
	body := calls[0].body
	if !strings.Contains(body, "three minutes") {
		t.Errorf("pre-fire body missing 'three minutes' phrase; body=%q", body)
	}
	if !strings.Contains(body, "/thrum:restart") {
		t.Errorf("pre-fire body missing /thrum:restart pointer; body=%q", body)
	}
}

// TestContextPollCallbacks_WarnFiresForDisabledAgent covers thrum-6qmf.1.15
// acceptance criterion 1 (plan line 567): disabled agents still receive the
// warn nudge. Per spec §3.1.4 this is operator-visibility preservation —
// suppressing the warn would hide threshold crossings from operators who
// opted the agent out of force-fire.
func TestContextPollCallbacks_WarnFiresForDisabledAgent(t *testing.T) {
	cfg := config.RestartConfig{AutoDisabledAgents: []string{"disabled_agent"}}
	nudger := &fakeNudger{}
	onWarn, _, _ := buildContextPollCallbacks(cfg, nudger, nil)

	onWarn(context.Background(), "disabled_agent", contextpoll.ContextUsage{UsedPercentage: 72})

	calls := nudger.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("warn must still fire for disabled agent (operator visibility); got %d calls", len(calls))
	}
}

// TestContextPollCallbacks_PreFireSkipsDisabledAgent covers thrum-6qmf.1.15
// acceptance criterion 2 (plan line 568): the pre-fire nudge does NOT fire
// for disabled agents. Per spec §3.1.4 the pre-fire is the last warning
// before force-fire; if force-fire is disabled, the pre-fire is false-urgency.
func TestContextPollCallbacks_PreFireSkipsDisabledAgent(t *testing.T) {
	cfg := config.RestartConfig{AutoDisabledAgents: []string{"disabled_agent"}}
	nudger := &fakeNudger{}
	_, onPreFire, _ := buildContextPollCallbacks(cfg, nudger, nil)

	onPreFire(context.Background(), "disabled_agent", contextpoll.ContextUsage{UsedPercentage: 82})

	if got := len(nudger.recordedCalls()); got != 0 {
		t.Errorf("pre-fire must NOT fire for disabled agent; got %d calls", got)
	}
}

// TestContextPollCallbacks_PreFireFiresForEnabledAgent is the
// negative-control pair to the previous test: pre-fire must still fire for
// agents NOT in AutoDisabledAgents. Guards against an overzealous gate that
// suppresses every pre-fire because of a misread of the disabled list.
func TestContextPollCallbacks_PreFireFiresForEnabledAgent(t *testing.T) {
	cfg := config.RestartConfig{AutoDisabledAgents: []string{"some_other_agent"}}
	nudger := &fakeNudger{}
	_, onPreFire, _ := buildContextPollCallbacks(cfg, nudger, nil)

	onPreFire(context.Background(), "test_agent", contextpoll.ContextUsage{UsedPercentage: 82})

	if got := len(nudger.recordedCalls()); got != 1 {
		t.Errorf("pre-fire must fire for non-disabled agent; got %d calls", got)
	}
}

// TestContextPollCallbacks_OnFireSkipsDisabledAgent covers thrum-6qmf.1.15
// acceptance criterion 3 (plan line 569): force-fire does NOT fire for
// disabled agents. With CR.4 wired, this is now observable directly on
// the RestartTrigger fake: no Restart calls recorded for disabled agents.
func TestContextPollCallbacks_OnFireSkipsDisabledAgent(t *testing.T) {
	cfg := config.RestartConfig{AutoDisabledAgents: []string{"disabled_agent"}}
	nudger := &fakeNudger{}
	trigger := &fakeRestartTrigger{}
	_, _, onFire := buildContextPollCallbacks(cfg, nudger, trigger)

	onFire(context.Background(), "disabled_agent", contextpoll.ContextUsage{UsedPercentage: 90})

	if got := len(nudger.recordedCalls()); got != 0 {
		t.Errorf("OnFire must NOT touch sender for disabled agent; got %d calls", got)
	}
	if got := len(trigger.recordedCalls()); got != 0 {
		t.Errorf("OnFire must NOT call RestartTrigger for disabled agent; got %d calls", got)
	}
}

// TestContextPollCallbacks_OnFireCallsRestartTrigger covers CR.4 T4.3
// (thrum-6qmf.1.18) acceptance: OnFire invokes the configured
// RestartTrigger.Restart on enabled agents with a reason that includes
// the percentage at the time of fire.
func TestContextPollCallbacks_OnFireCallsRestartTrigger(t *testing.T) {
	cfg := config.RestartConfig{}
	nudger := &fakeNudger{}
	trigger := &fakeRestartTrigger{}
	_, _, onFire := buildContextPollCallbacks(cfg, nudger, trigger)

	onFire(context.Background(), "test_agent", contextpoll.ContextUsage{UsedPercentage: 88})

	calls := trigger.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("OnFire should fire RestartTrigger once, got %d calls", len(calls))
	}
	if calls[0].agentName != "test_agent" {
		t.Errorf("agentName = %q, want %q", calls[0].agentName, "test_agent")
	}
	if !strings.Contains(calls[0].reason, "88%") {
		t.Errorf("reason missing percentage; got %q", calls[0].reason)
	}
	if !strings.Contains(calls[0].reason, "automatic context-threshold restart") {
		t.Errorf("reason missing canonical phrase; got %q", calls[0].reason)
	}
}

// TestContextPollCallbacks_OnFireSurvivesTriggerError pins the failure
// path: if RestartTrigger.Restart returns an error, OnFire must NOT
// panic, must NOT bubble the error up (there's no caller), and must NOT
// re-fire on its own. The Poller's in-flight guard handles retry
// suppression; the InFlightMaxWait backstop eventually re-arms the
// callback.
func TestContextPollCallbacks_OnFireSurvivesTriggerError(t *testing.T) {
	cfg := config.RestartConfig{}
	nudger := &fakeNudger{}
	trigger := &fakeRestartTrigger{returnErr: errors.New("simulated restart failure")}
	_, _, onFire := buildContextPollCallbacks(cfg, nudger, trigger)

	// Should NOT panic.
	onFire(context.Background(), "test_agent", contextpoll.ContextUsage{UsedPercentage: 88})

	if got := len(trigger.recordedCalls()); got != 1 {
		t.Errorf("OnFire must still call RestartTrigger even when it errors; got %d calls", got)
	}
}

// TestContextPollCallbacks_OnFireNilTriggerStub pins the nil-trigger
// fallback behavior: OnFire logs the stub breadcrumb and returns
// cleanly. This branch exists for the tests above that build
// callbacks with a nil trigger (warn/pre-fire only).
func TestContextPollCallbacks_OnFireNilTriggerStub(t *testing.T) {
	cfg := config.RestartConfig{}
	nudger := &fakeNudger{}
	_, _, onFire := buildContextPollCallbacks(cfg, nudger, nil)

	// Should NOT panic; should NOT call sender.
	onFire(context.Background(), "test_agent", contextpoll.ContextUsage{UsedPercentage: 88})

	if got := len(nudger.recordedCalls()); got != 0 {
		t.Errorf("nil-trigger OnFire must not call sender; got %d calls", got)
	}
}
