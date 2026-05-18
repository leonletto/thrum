package escalation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/escalation"
)

type stubEmail struct {
	sendErr   error
	sendCalls []emailCall
}

type emailCall struct {
	recipient string
	subject   string
	body      string
}

func (s *stubEmail) Send(_ context.Context, recipient, subject, body string) error {
	s.sendCalls = append(s.sendCalls, emailCall{recipient: recipient, subject: subject, body: body})
	return s.sendErr
}

type stubMessage struct {
	sendErr   error
	sendID    string
	sendCalls []messageCall
}

type messageCall struct {
	target  string
	subject string
	body    string
}

func (s *stubMessage) MessageSend(_ context.Context, target, subject, body string) (string, error) {
	s.sendCalls = append(s.sendCalls, messageCall{target: target, subject: subject, body: body})
	return s.sendID, s.sendErr
}

func sampleAlert() escalation.Alert {
	return escalation.Alert{
		Source:    "b-b1.stage_failure",
		AgentName: "docs_bot",
		JobID:     "docs-bot-job",
		RunID:     "docs-bot-job-g1-1747353600",
	}
}

// TestRouteEscalation_EmailConfigured_CallsEmailRPC pins the canonical
// email route: when EmailEnabled + OperatorAddress + Email RPC are
// all wired, RouteEscalation calls EmailRPC.Send with the recipient
// from config + subject + body. MessageRPC is NOT called even when
// it's wired — operator gets one escalation, not two.
func TestRouteEscalation_EmailConfigured_CallsEmailRPC(t *testing.T) {
	email := &stubEmail{}
	msg := &stubMessage{}
	deps := escalation.Deps{
		Email:   email,
		Message: msg,
		Config: escalation.Config{
			EmailEnabled:    true,
			OperatorAddress: "ops@example.com",
		},
	}
	err := escalation.RouteEscalation(context.Background(), sampleAlert(), "Subj", "Body", deps)
	if err != nil {
		t.Fatalf("expected nil; got: %v", err)
	}
	if len(email.sendCalls) != 1 {
		t.Fatalf("EmailRPC.Send calls = %d; want 1", len(email.sendCalls))
	}
	call := email.sendCalls[0]
	if call.recipient != "ops@example.com" {
		t.Errorf("recipient = %q; want ops@example.com", call.recipient)
	}
	if call.subject != "Subj" {
		t.Errorf("subject = %q; want Subj", call.subject)
	}
	if call.body != "Body" {
		t.Errorf("body = %q; want Body", call.body)
	}
	if len(msg.sendCalls) != 0 {
		t.Errorf("MessageRPC.Send called %d times; want 0 (email route exclusive)", len(msg.sendCalls))
	}
}

// TestRouteEscalation_EmailTransientFailure_AbsorbedAndReturnsNil
// pins the transient-failure contract: a Send error from the email
// bridge does NOT propagate to the caller — D-B1's queue handles
// retry. RouteEscalation logs and returns nil. Without this, every
// escalation site would have to know about email retry semantics.
func TestRouteEscalation_EmailTransientFailure_AbsorbedAndReturnsNil(t *testing.T) {
	email := &stubEmail{sendErr: errors.New("smtp socket reset")}
	deps := escalation.Deps{
		Email: email,
		Config: escalation.Config{
			EmailEnabled:    true,
			OperatorAddress: "ops@example.com",
		},
	}
	err := escalation.RouteEscalation(context.Background(), sampleAlert(), "Subj", "Body", deps)
	if err != nil {
		t.Errorf("expected nil despite email Send error; got: %v", err)
	}
}

// TestRouteEscalation_EmailDisabled_FallsBackToSupervisor pins the
// supervisor-fallback route: when email is not enabled, the alert
// goes to MessageRPC.MessageSend targeting the configured supervisor
// agent (or "coordinator" when SupervisorAgentName is empty).
func TestRouteEscalation_EmailDisabled_FallsBackToSupervisor(t *testing.T) {
	email := &stubEmail{}
	msg := &stubMessage{sendID: "msg-supervisor-1"}
	deps := escalation.Deps{
		Email:   email,
		Message: msg,
		Config: escalation.Config{
			EmailEnabled:        false,
			SupervisorAgentName: "ops_lead",
		},
	}
	if err := escalation.RouteEscalation(context.Background(), sampleAlert(), "Subj", "Body", deps); err != nil {
		t.Fatalf("expected nil; got: %v", err)
	}
	if len(msg.sendCalls) != 1 {
		t.Fatalf("MessageRPC.Send calls = %d; want 1", len(msg.sendCalls))
	}
	if msg.sendCalls[0].target != "ops_lead" {
		t.Errorf("target = %q; want ops_lead", msg.sendCalls[0].target)
	}
	if msg.sendCalls[0].subject != "Subj" {
		t.Errorf("subject = %q; want Subj", msg.sendCalls[0].subject)
	}
	// Body must compose subject + blank line + body so a single inbox
	// message contains everything an inline-reading operator needs.
	if msg.sendCalls[0].body != "Subj\n\nBody" {
		t.Errorf("body = %q; want %q", msg.sendCalls[0].body, "Subj\n\nBody")
	}
	if len(email.sendCalls) != 0 {
		t.Errorf("EmailRPC.Send called %d times; want 0 (email disabled)", len(email.sendCalls))
	}
}

// TestRouteEscalation_SupervisorDefault_WhenAgentNameEmpty pins the
// "coordinator" default per spec §8: an unconfigured
// SupervisorAgentName falls back to "coordinator" rather than failing
// silently. Eliminates a class of "no escalation arrived"
// misconfiguration bugs.
func TestRouteEscalation_SupervisorDefault_WhenAgentNameEmpty(t *testing.T) {
	msg := &stubMessage{sendID: "msg-default"}
	deps := escalation.Deps{
		Message: msg,
		Config:  escalation.Config{}, // EmailEnabled=false, SupervisorAgentName=""
	}
	if err := escalation.RouteEscalation(context.Background(), sampleAlert(), "Subj", "Body", deps); err != nil {
		t.Fatalf("expected nil; got: %v", err)
	}
	if msg.sendCalls[0].target != "coordinator" {
		t.Errorf("target = %q; want default 'coordinator'", msg.sendCalls[0].target)
	}
}

// TestRouteEscalation_EmailConfiguredButNilRPC_FallsBackToSupervisor
// pins the defensive partial-config path: if EmailEnabled is true
// but the Email RPC isn't wired (boot-order race or missing dep
// injection), RouteEscalation falls back to the supervisor rather
// than panicking on a nil interface.
func TestRouteEscalation_EmailConfiguredButNilRPC_FallsBackToSupervisor(t *testing.T) {
	msg := &stubMessage{sendID: "msg-fallback"}
	deps := escalation.Deps{
		Email:   nil, // partial config
		Message: msg,
		Config: escalation.Config{
			EmailEnabled:    true,
			OperatorAddress: "ops@example.com", // set but Email RPC missing
		},
	}
	if err := escalation.RouteEscalation(context.Background(), sampleAlert(), "Subj", "Body", deps); err != nil {
		t.Fatalf("expected nil; got: %v", err)
	}
	if len(msg.sendCalls) != 1 {
		t.Errorf("MessageRPC.Send calls = %d; want 1 (supervisor fallback when email RPC missing)",
			len(msg.sendCalls))
	}
}

// TestRouteEscalation_SupervisorSendError_Propagates pins the contract
// asymmetry: email transient errors are absorbed (queue retry), but
// supervisor-fallback errors propagate because there's no retry
// backstop at this layer. Callers (escalation site) are expected
// to log + continue rather than crash, but they need to KNOW the
// route failed.
func TestRouteEscalation_SupervisorSendError_Propagates(t *testing.T) {
	wantErr := errors.New("inbox shard offline")
	msg := &stubMessage{sendErr: wantErr}
	deps := escalation.Deps{
		Message: msg,
		Config:  escalation.Config{SupervisorAgentName: "coordinator"},
	}
	err := escalation.RouteEscalation(context.Background(), sampleAlert(), "Subj", "Body", deps)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v; want wraps %v", err, wantErr)
	}
}

// TestRouteEscalation_NoEmailNoMessage_ReturnsError pins the
// degenerate config: zero routes wired = explicit error so a
// misconfiguration surfaces loudly rather than dropping the alert.
func TestRouteEscalation_NoEmailNoMessage_ReturnsError(t *testing.T) {
	deps := escalation.Deps{} // nothing wired
	err := escalation.RouteEscalation(context.Background(), sampleAlert(), "Subj", "Body", deps)
	if err == nil {
		t.Fatal("expected error when no route is wired; got nil")
	}
}
