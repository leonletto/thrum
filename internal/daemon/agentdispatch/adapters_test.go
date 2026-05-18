package agentdispatch

import (
	"context"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/escalation"
	"github.com/leonletto/thrum/internal/daemon/rpc"
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
	var _ Restarter = (*restarterAdapter)(nil)
}

// TestRestarterAdapter_NilHandler_ReturnsErrorNotPanic pins the
// defensive nil-handler guard per thrum-6qmf.4.88 wiring contract:
// a misconfigured adapter (nil TmuxHandler dep) surfaces as a
// wrapped error rather than a nil-deref panic at first Restart
// fire. Catches the case where daemon-boot wiring forgets to
// thread the real *rpc.TmuxHandler through buildPaneHealthRespawner.
func TestRestarterAdapter_NilHandler_ReturnsErrorNotPanic(t *testing.T) {
	a := NewRestarterAdapter(nil)
	err := a.Restart(context.Background(), "docs_bot")
	if err == nil {
		t.Fatal("expected error for nil TmuxHandler; got nil")
	}
}

// TestRestarterAdapter_Restart_ForwardsAgentNameAsSession pins
// the canonical mapping per thrum-6qmf.4.88 + adapter docstring:
// the agentName parameter from Respawner.OnPaneGone maps 1:1 to
// the tmux session name passed to RestartSession. Construct an
// rpc.TmuxHandler so the call reaches its body, then assert the
// error chain identifies our agentName — the underlying
// RestartSession will fail because no real session exists, but
// the failure carries the agent name through so the adapter's
// forwarding shape is verified end-to-end.
func TestRestarterAdapter_Restart_ForwardsAgentNameAsSession(t *testing.T) {
	// Build a TmuxHandler against a temp dir. The session
	// "docs_bot_missing" doesn't exist in tmux, so RestartSession
	// will fail at ensureSession; the error message includes the
	// session name we passed → proves the adapter forwarded
	// agentName as the session name.
	h := rpc.NewTmuxHandler(t.TempDir(), nil)
	a := NewRestarterAdapter(h)
	err := a.Restart(context.Background(), "docs_bot_missing")
	if err == nil {
		t.Fatal("expected error from missing session; got nil")
	}
	// The error chain should reference the session we asked to
	// restart. ensureSession's wrapped error includes the session
	// name verbatim.
	if !strings.Contains(err.Error(), "docs_bot_missing") {
		t.Errorf("err = %v; want substring 'docs_bot_missing' (proves agentName→session mapping)", err)
	}
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
// is a wiring bug, not a runtime error. Supplies a non-nil handler
// (&rpc.MessageHandler{}) so the nil-handler guard above doesn't
// fire first — this test must exercise the callerAgentID branch
// specifically.
func TestMessageRPCAdapter_EmptyCallerRejects(t *testing.T) {
	a := &MessageRPCAdapter{handler: &rpc.MessageHandler{}, callerAgentID: ""}
	_, err := a.MessageSend(context.Background(), "docs_bot", "subj", "body")
	if err == nil {
		t.Fatal("expected error for empty callerAgentID")
	}
	if !contains(err.Error(), "callerAgentID") {
		t.Errorf("error should mention empty callerAgentID; got %v", err)
	}
}

