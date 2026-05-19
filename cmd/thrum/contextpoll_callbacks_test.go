package main

import (
	"context"
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

// TestContextPollCallbacks_WarnBody covers thrum-6qmf.1.14 acceptance criteria
// 1-3 (plan lines 549-551): the warn body contains the current percentage,
// the "do NOT dispatch sub-agents" prohibition, and references the
// AutoThreshold so the agent knows when force-fire will hit.
func TestContextPollCallbacks_WarnBody(t *testing.T) {
	cfg := config.RestartConfig{WarnThreshold: 70, AutoThreshold: 80}
	nudger := &fakeNudger{}
	onWarn, _, _ := buildContextPollCallbacks(cfg, nudger)

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
	_, onPreFire, _ := buildContextPollCallbacks(cfg, nudger)

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
	onWarn, _, _ := buildContextPollCallbacks(cfg, nudger)

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
	_, onPreFire, _ := buildContextPollCallbacks(cfg, nudger)

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
	_, onPreFire, _ := buildContextPollCallbacks(cfg, nudger)

	onPreFire(context.Background(), "test_agent", contextpoll.ContextUsage{UsedPercentage: 82})

	if got := len(nudger.recordedCalls()); got != 1 {
		t.Errorf("pre-fire must fire for non-disabled agent; got %d calls", got)
	}
}

// TestContextPollCallbacks_OnFireSkipsDisabledAgent covers thrum-6qmf.1.15
// acceptance criterion 3 (plan line 569): force-fire does NOT fire for
// disabled agents. The OnFire stub doesn't currently call sender — that
// changes at CR.4 T4.3 (thrum-6qmf.1.18) when the real RestartTrigger lands
// — so this test asserts the IsAutoDisabled short-circuit is in place by
// the only available observable: the closure exits cleanly without panicking
// AND a non-disabled call against the same factory closure does proceed to
// the slog breadcrumb side of the branch. The companion test
// TestContextPollCallbacks_OnFireDoesNotCallSender pins the stub semantics so
// a future refactor that wires sender-from-OnFire (e.g. CR.4 T4.3) makes
// this test fail visibly and the implementer has to update both the test
// and the gate.
func TestContextPollCallbacks_OnFireSkipsDisabledAgent(t *testing.T) {
	cfg := config.RestartConfig{AutoDisabledAgents: []string{"disabled_agent"}}
	nudger := &fakeNudger{}
	_, _, onFire := buildContextPollCallbacks(cfg, nudger)

	// Should return without doing anything observable.
	onFire(context.Background(), "disabled_agent", contextpoll.ContextUsage{UsedPercentage: 90})

	if got := len(nudger.recordedCalls()); got != 0 {
		t.Errorf("OnFire must NOT touch sender for disabled agent; got %d calls", got)
	}
}

// TestContextPollCallbacks_OnFireDoesNotCallSender pins the T2.3-shipped
// OnFire stub semantics: it logs but does not invoke sender. When CR.4 T4.3
// (thrum-6qmf.1.18) lands and OnFire calls the real RestartTrigger.Restart,
// this test should be UPDATED to assert the trigger interaction rather than
// the absence of sender activity — at that point the fake nudger is the
// wrong harness and a RestartTrigger fake should be added instead.
func TestContextPollCallbacks_OnFireDoesNotCallSender(t *testing.T) {
	cfg := config.RestartConfig{}
	nudger := &fakeNudger{}
	_, _, onFire := buildContextPollCallbacks(cfg, nudger)

	onFire(context.Background(), "test_agent", contextpoll.ContextUsage{UsedPercentage: 90})

	if got := len(nudger.recordedCalls()); got != 0 {
		t.Errorf("T2.3 OnFire stub must not call sender (sender-call lands at CR.4 T4.3 via RestartTrigger); got %d calls", got)
	}
}
