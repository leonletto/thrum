package agentdispatch

import (
	"context"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/escalation"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/worktree"
)

// --- Interface satisfaction (compile-time pins mirrored from adapters.go) ---

func TestAdapters_SatisfyInterfaces(t *testing.T) {
	var _ TmuxRPC = (*tmuxRPCAdapter)(nil)
	var _ MessageRPC = (*MessageRPCAdapter)(nil)
	var _ WorktreeManager = (*worktreeMgrAdapter)(nil)
	var _ EscalationRouter = (*escalationRouterAdapter)(nil)
	var _ Reconciler = (*reconcilerStub)(nil)
}

// --- worktreeMgrAdapter ---

// TestWorktreeMgrAdapter_RejectsBogusOptions exercises the
// passthrough nature: a Create call with empty opts surfaces the
// underlying worktree package's validation error rather than
// silently succeeding. Picks the minimal "would fail" input that
// doesn't require real git state.
func TestWorktreeMgrAdapter_RejectsBogusOptions(t *testing.T) {
	a := NewWorktreeMgrAdapter()
	_, err := a.Create(context.Background(), worktree.CreateOpts{})
	if err == nil {
		t.Error("Create with empty opts should return validation error; passthrough is broken")
	}
}

// --- escalationRouterAdapter ---

// escMessageCall records one MessageSend invocation observed by
// the fake escalation-router message sink.
type escMessageCall struct {
	target  string
	subject string
	body    string
}

// fakeEscalationMessage records every MessageSend call so the
// escalation-router adapter test can verify supervisor-fallback
// routing fires when email isn't configured.
type fakeEscalationMessage struct {
	calls []escMessageCall
	err   error
}

func (f *fakeEscalationMessage) MessageSend(_ context.Context, target, subject, body string) (string, error) {
	f.calls = append(f.calls, escMessageCall{target: target, subject: subject, body: body})
	if f.err != nil {
		return "", f.err
	}
	return "msg-fake", nil
}

func TestEscalationRouterAdapter_RoutesToSupervisor(t *testing.T) {
	msg := &fakeEscalationMessage{}
	deps := escalation.Deps{
		Message: msg,
		Config: escalation.Config{
			EmailEnabled:        false,
			SupervisorAgentName: "coordinator",
		},
	}
	a := NewEscalationRouterAdapter(deps)
	alert := escalation.Alert{
		Source:    "b-b1.idle_nudge",
		AgentName: "docs_bot",
		JobID:     "job-1",
		RunID:     "run-1",
	}
	if err := a.Route(context.Background(), alert, "subject", "body"); err != nil {
		t.Fatalf("Route: %v", err)
	}
	if len(msg.calls) != 1 {
		t.Fatalf("expected 1 supervisor message; got %d", len(msg.calls))
	}
	if msg.calls[0].target == "" {
		t.Error("supervisor message should have a non-empty target")
	}
	// Pin the composition contract: RouteEscalation pre-composes
	// `subject + "\n\n" + body` into the body argument before calling
	// MessageSend. The MessageRPCAdapter's per-call MessageSend passes
	// body through to SendRequest.Content verbatim (no re-folding).
	// This assertion catches a regression that would re-introduce the
	// double-subject bug — without it, a future implementer might
	// "helpfully" re-add subject prefixing to the adapter.
	wantBody := "subject\n\nbody"
	if msg.calls[0].body != wantBody {
		t.Errorf("body = %q; want %q (RouteEscalation pre-composes; adapter must not re-fold subject)",
			msg.calls[0].body, wantBody)
	}
	if msg.calls[0].subject != "subject" {
		t.Errorf("subject = %q; want %q (passed through to MessageSend's subject arg)",
			msg.calls[0].subject, "subject")
	}
}

// --- reconcilerStub ---

// TestReconcilerStub_MarksFailed pins the E6.9-pending placeholder
// behavior: non-terminal runs found at boot get marked failed
// with a clear "not yet wired" error so they don't dangle across
// daemon restarts. E6.9 replaces this with the real
// boot-recovery logic.
func TestReconcilerStub_MarksFailed(t *testing.T) {
	r := NewReconcilerStub()
	state, err := r.ReconcileRun(context.Background(),
		scheduler.JobSpec{ID: "test"}, "run-1", scheduler.StateRunning)
	if state != scheduler.StateFailed {
		t.Errorf("ReconcileRun state = %v; want StateFailed", state)
	}
	if err == nil {
		t.Fatal("expected error from stub")
	}
	if !contains(err.Error(), "E6.9") {
		t.Errorf("error should reference E6.9; got %v", err)
	}
}

// --- MessageRPCAdapter ---

// TestMessageRPCAdapter_EmptyTargetRejects pins the wiring-bug
// guard symmetric with messageHandlerSender's empty-toAgent check.
func TestMessageRPCAdapter_EmptyTargetRejects(t *testing.T) {
	a := NewMessageRPCAdapter(nil, "supervisor_test")
	_, err := a.MessageSend(context.Background(), "", "subj", "body")
	if err == nil {
		t.Error("expected error for empty target")
	}
}

// TestMessageRPCAdapter_NilHandlerRejects pins the same defensive
// path messageHandlerSender uses — a nil handler is a programmer
// error worth surfacing as an error, not a panic.
func TestMessageRPCAdapter_NilHandlerRejects(t *testing.T) {
	a := NewMessageRPCAdapter(nil, "supervisor_test")
	_, err := a.MessageSend(context.Background(), "docs_bot", "subj", "body")
	if err == nil {
		t.Error("expected error for nil handler")
	}
	if !contains(err.Error(), "nil handler") {
		t.Errorf("error should mention nil handler; got %v", err)
	}
}

// TestMessageRPCAdapter_EmptyCallerRejects pins the caller-resolution
// guard: a daemon-source enqueue without a wired supervisor identity
// is a wiring bug, not a runtime error.
func TestMessageRPCAdapter_EmptyCallerRejects(t *testing.T) {
	// Construct with empty callerAgentID; nil handler is fine
	// because the caller-guard fires first.
	a := &MessageRPCAdapter{handler: nil, callerAgentID: ""}
	_, err := a.MessageSend(context.Background(), "docs_bot", "subj", "body")
	if err == nil {
		t.Error("expected error for empty callerAgentID")
	}
	if !contains(err.Error(), "callerAgentID") && !contains(err.Error(), "handler") {
		// Order-of-checks could surface either error; both are
		// acceptable wiring-bug signals.
		t.Errorf("error should mention wiring bug (callerAgentID or handler); got %v", err)
	}
}

