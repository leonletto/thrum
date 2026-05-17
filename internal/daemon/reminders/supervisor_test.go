package reminders

import (
	"context"
	"errors"
	"testing"
)

// fakeAgentRuntimeResolver returns a configurable lookup. Default
// behavior: unknown agent → ok=false.
type fakeAgentRuntimeResolver struct {
	knownAgents map[string]struct{ runtime, tmuxTarget string }
}

func (f *fakeAgentRuntimeResolver) AgentRuntime(_ context.Context, agent string) (string, string, bool) {
	if v, ok := f.knownAgents[agent]; ok {
		return v.runtime, v.tmuxTarget, true
	}
	return "", "", false
}

// fakeSpoolWriter records every EnqueueSupervisorMessage call.
type fakeSpoolWriter struct {
	calls []spooledMessage
	err   error
}

type spooledMessage struct {
	targetAgent, body string
}

func (f *fakeSpoolWriter) EnqueueSupervisorMessage(_ context.Context, target, body string) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, spooledMessage{targetAgent: target, body: body})
	return nil
}

// stockResolver returns a resolver that knows docs_bot.
func stockResolver() *fakeAgentRuntimeResolver {
	return &fakeAgentRuntimeResolver{
		knownAgents: map[string]struct{ runtime, tmuxTarget string }{
			"docs_bot": {runtime: "claude", tmuxTarget: "docs:0.0"},
		},
	}
}

func TestSupervisor_NilReminder_DoesNotRoute(t *testing.T) {
	sup := NewSupervisorRouter(stockResolver(), nil, &fakeSpoolWriter{})
	routed, err := sup.MaybeRoute(ctx, nil)
	if err != nil || routed {
		t.Errorf("nil reminder: got routed=%v err=%v; want false/nil", routed, err)
	}
}

func TestSupervisor_EmptyTargetAgent_DoesNotRoute(t *testing.T) {
	sup := NewSupervisorRouter(stockResolver(), nil, &fakeSpoolWriter{})
	routed, _ := sup.MaybeRoute(ctx, &Reminder{ID: "reminder-x-1-1"})
	if routed {
		t.Error("empty target_agent: should not route")
	}
}

// Agent registered but has no live tmux session — e.g. remote-only
// peer. Caller falls through to normal delivery.
func TestSupervisor_TargetHasNoTmuxSession_DoesNotRoute(t *testing.T) {
	resolver := &fakeAgentRuntimeResolver{} // no known agents
	sup := NewSupervisorRouter(resolver, nil, &fakeSpoolWriter{})
	r := &Reminder{Source: SourceAgent, TargetAgent: "unknown_agent", Body: "x"}
	routed, err := sup.MaybeRoute(ctx, r)
	if err != nil {
		t.Fatalf("MaybeRoute: %v", err)
	}
	if routed {
		t.Error("unknown agent: should not route (caller falls through)")
	}
}

// tmux capture-pane fails — conservative default is "assume normal";
// caller falls through to normal delivery (better to over-deliver than
// drop).
func TestSupervisor_CaptureError_DoesNotRoute(t *testing.T) {
	capture := func(string, int) (string, error) {
		return "", errors.New("tmux unhappy")
	}
	spool := &fakeSpoolWriter{}
	sup := NewSupervisorRouter(stockResolver(), capture, spool)
	r := &Reminder{Source: SourceAgent, TargetAgent: "docs_bot", Body: "x"}
	routed, err := sup.MaybeRoute(ctx, r)
	if err != nil {
		t.Errorf("capture error should be swallowed (conservative fallthrough); got %v", err)
	}
	if routed {
		t.Error("capture error: should not route")
	}
	if len(spool.calls) != 0 {
		t.Error("capture error should not reach the spool")
	}
}

// Pane is normal (shell prompt) — IsPaneSafeToType returns true.
// Caller falls through to normal delivery.
func TestSupervisor_PaneNormal_DoesNotRoute(t *testing.T) {
	capture := func(string, int) (string, error) {
		// "$ " is a shell prompt; permission.IsPaneSafeToType returns
		// true for this kind of content.
		return "$ ", nil
	}
	spool := &fakeSpoolWriter{}
	sup := NewSupervisorRouter(stockResolver(), capture, spool)
	r := &Reminder{Source: SourceAgent, TargetAgent: "docs_bot", Body: "x"}
	routed, err := sup.MaybeRoute(ctx, r)
	if err != nil {
		t.Fatalf("MaybeRoute: %v", err)
	}
	if routed {
		t.Error("normal pane: should not route through supervisor")
	}
	if len(spool.calls) != 0 {
		t.Error("normal pane should not reach the spool")
	}
}

// trustGatePane returns a pane fixture matching
// permission.trustGateGenericRE — generic "1. Yes / 2. No" + the word
// "trust" inside a 400-char window. Real Claude/codex fixtures live in
// the permission package's tests; we just need pane content that
// reliably trips IsPaneSafeToType=false.
func trustGatePane() string {
	return `Quick safety check before we run anything destructive.

Do you trust this folder?
  1. Yes
  2. No, exit`
}

// Pane is at a trust gate — IsPaneSafeToType returns false.
// Router spools the message + reports routed=true.
func TestSupervisor_PaneAtTrustGate_RoutesToSpool(t *testing.T) {
	capture := func(string, int) (string, error) { return trustGatePane(), nil }
	spool := &fakeSpoolWriter{}
	sup := NewSupervisorRouter(stockResolver(), capture, spool)
	r := &Reminder{
		ID:          "reminder-docs_bot-100-0001",
		Source:      SourceAgent,
		TriggerKind: TriggerTime,
		TargetAgent: "docs_bot",
		Body:        "finish release notes",
	}
	routed, err := sup.MaybeRoute(ctx, r)
	if err != nil {
		t.Fatalf("MaybeRoute: %v", err)
	}
	if !routed {
		t.Error("trust gate: should route through supervisor")
	}
	if len(spool.calls) != 1 {
		t.Fatalf("spool calls = %d, want 1", len(spool.calls))
	}
	if spool.calls[0].targetAgent != "docs_bot" {
		t.Errorf("target = %q, want docs_bot", spool.calls[0].targetAgent)
	}
	// Body is the terse-agent fire message format (FormatAgentBody).
	if spool.calls[0].body == "" {
		t.Error("spooled body should be non-empty")
	}
}

// Spool fails — supervisor returns the error; caller (DeliverySink)
// falls through to normal delivery to avoid losing the reminder.
func TestSupervisor_SpoolError_ReturnsErrorNotRouted(t *testing.T) {
	capture := func(string, int) (string, error) { return trustGatePane(), nil }
	spool := &fakeSpoolWriter{err: errors.New("spool full")}
	sup := NewSupervisorRouter(stockResolver(), capture, spool)
	r := &Reminder{Source: SourceAgent, TargetAgent: "docs_bot", Body: "x"}
	routed, err := sup.MaybeRoute(ctx, r)
	if err == nil {
		t.Error("expected error to propagate from spool failure")
	}
	if routed {
		t.Error("spool failure: routed should be false (caller falls through)")
	}
}

// Test the SupervisorMaybeRouter interface satisfaction at the type
// level. Compile-time check is in supervisor.go; this test catches a
// future refactor that would break interface satisfaction.
func TestSupervisor_SatisfiesInterface(t *testing.T) {
	var _ SupervisorMaybeRouter = (*SupervisorRouter)(nil)
}
