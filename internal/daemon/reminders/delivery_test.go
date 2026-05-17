package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeMessageSender records every SendReminder call. sentTo asks "did
// the sink dispatch a message to this agent name?"; agents are stored
// without the leading @ since SendReminder receives them stripped.
type fakeMessageSender struct {
	calls []sentMessage
	err   error
}

type sentMessage struct {
	from, to, body string
}

func (f *fakeMessageSender) SendReminder(_ context.Context, from, to, body string) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, sentMessage{from: from, to: to, body: body})
	return nil
}

func (f *fakeMessageSender) sentTo(agentRef string) bool {
	agent := strings.TrimPrefix(agentRef, "@")
	for _, c := range f.calls {
		if c.to == agent {
			return true
		}
	}
	return false
}

// fakeEmailQueue records every QueueReminderEmail call.
type fakeEmailQueue struct {
	calls []queuedEmail
	err   error
}

type queuedEmail struct {
	to, subject, body string
}

func (f *fakeEmailQueue) QueueReminderEmail(_ context.Context, to, subject, body string) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, queuedEmail{to: to, subject: subject, body: body})
	return nil
}

func (f *fakeEmailQueue) queuedTo(addr string) bool {
	for _, c := range f.calls {
		if c.to == addr {
			return true
		}
	}
	return false
}

// fakeSupervisorRouter returns a configurable (routed, err) pair.
type fakeSupervisorRouter struct {
	routed bool
	err    error
	calls  int
}

func (f *fakeSupervisorRouter) MaybeRoute(_ context.Context, _ *Reminder) (bool, error) {
	f.calls++
	return f.routed, f.err
}

func TestDelivery_NilReminder(t *testing.T) {
	sink := NewDeliverySink(&fakeMessageSender{}, nil, nil)
	if err := sink.Fire(ctx, nil); err == nil {
		t.Error("expected error for nil reminder")
	}
}

func TestDelivery_DaemonConditionFansToChain(t *testing.T) {
	chain := []string{"@coordinator_main", "leon@example.com"}
	r := &Reminder{
		Source:       SourceDaemon,
		TriggerKind:  TriggerConditionPaneQuiet,
		TargetAgent:  "docs_bot",
		TargetChain:  chain,
		PaneSnapshot: "stale pane",
		TriggerMeta:  json.RawMessage(`{}`),
		ID:           "reminder-docs_bot-100-0001",
		RaisedAt:     time.Now().Add(-time.Hour),
	}
	fakeMsg := &fakeMessageSender{}
	fakeEmail := &fakeEmailQueue{}
	sink := NewDeliverySink(fakeMsg, fakeEmail, nil)

	if err := sink.Fire(ctx, r); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if !fakeMsg.sentTo("@coordinator_main") {
		t.Errorf("expected message to @coordinator_main; got calls=%+v", fakeMsg.calls)
	}
	if !fakeEmail.queuedTo("leon@example.com") {
		t.Errorf("expected email to leon@example.com; got calls=%+v", fakeEmail.calls)
	}
}

func TestDelivery_DaemonCondition_EmailQueueAbsent_LogsAndContinues(t *testing.T) {
	chain := []string{"@coordinator_main", "leon@example.com"}
	r := &Reminder{
		Source: SourceDaemon, TriggerKind: TriggerConditionPaneQuiet,
		TargetAgent: "x", TargetChain: chain, PaneSnapshot: "x",
		TriggerMeta: json.RawMessage(`{}`),
		ID:          "reminder-x-1-1",
	}
	fakeMsg := &fakeMessageSender{}
	// email is nil → email chain entries should log+skip, not fail
	sink := NewDeliverySink(fakeMsg, nil, nil)

	if err := sink.Fire(ctx, r); err != nil {
		t.Fatalf("Fire: %v (the agent send should have succeeded)", err)
	}
	if !fakeMsg.sentTo("@coordinator_main") {
		t.Error("agent send should have succeeded even without email queue")
	}
}

func TestDelivery_DaemonCondition_AllRecipientsFail_ReturnsError(t *testing.T) {
	r := &Reminder{
		Source: SourceDaemon, TriggerKind: TriggerConditionPaneQuiet,
		TargetAgent: "x", TargetChain: []string{"@coord", "leon@example.com"},
		PaneSnapshot: "x", TriggerMeta: json.RawMessage(`{}`),
		ID: "reminder-x-1-1",
	}
	// Both sender and queue fail.
	sink := NewDeliverySink(
		&fakeMessageSender{err: errors.New("inbox down")},
		&fakeEmailQueue{err: errors.New("smtp down")},
		nil,
	)
	err := sink.Fire(ctx, r)
	if err == nil {
		t.Fatal("expected error when no recipients reached")
	}
	if !strings.Contains(err.Error(), "no recipients reached") {
		t.Errorf("error message should say no recipients reached; got %v", err)
	}
}

func TestDelivery_DaemonCondition_PartialFailureReturnsNil(t *testing.T) {
	r := &Reminder{
		Source: SourceDaemon, TriggerKind: TriggerConditionPaneQuiet,
		TargetAgent: "x", TargetChain: []string{"@coord", "leon@example.com"},
		PaneSnapshot: "x", TriggerMeta: json.RawMessage(`{}`),
		ID: "reminder-x-1-1",
	}
	// Email fails, agent succeeds.
	sink := NewDeliverySink(
		&fakeMessageSender{},
		&fakeEmailQueue{err: errors.New("smtp down")},
		nil,
	)
	if err := sink.Fire(ctx, r); err != nil {
		t.Errorf("partial-failure should return nil to avoid duplicate-fire on the succeeded recipient; got %v", err)
	}
}

func TestDelivery_DaemonCondition_EmptyChain_Rejects(t *testing.T) {
	r := &Reminder{
		Source: SourceDaemon, TriggerKind: TriggerConditionPaneQuiet,
		TargetAgent: "x", PaneSnapshot: "x", TriggerMeta: json.RawMessage(`{}`),
		ID: "reminder-x-1-1",
	}
	sink := NewDeliverySink(&fakeMessageSender{}, nil, nil)
	if err := sink.Fire(ctx, r); err == nil {
		t.Error("expected error for empty target_chain on daemon-condition row")
	}
}

func TestDelivery_AgentTime_ToInbox(t *testing.T) {
	r := &Reminder{
		Source: SourceAgent, TriggerKind: TriggerTime,
		SourceAgent: "docs_bot", TargetAgent: "docs_bot",
		Body: "x", ID: "reminder-docs_bot-1-1",
	}
	fakeMsg := &fakeMessageSender{}
	sink := NewDeliverySink(fakeMsg, nil, nil) // no supervisor

	if err := sink.Fire(ctx, r); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if !fakeMsg.sentTo("@docs_bot") {
		t.Errorf("expected delivery to @docs_bot; got %+v", fakeMsg.calls)
	}
	if len(fakeMsg.calls) > 0 && fakeMsg.calls[0].from != "docs_bot" {
		t.Errorf("expected from=docs_bot (SourceAgent for agent-source); got %q", fakeMsg.calls[0].from)
	}
}

func TestDelivery_UserTime_ToInbox(t *testing.T) {
	r := &Reminder{
		Source: SourceUser, TriggerKind: TriggerTime,
		TargetAgent: "leon", Body: "stand-up",
		ID: "reminder-leon-2-2",
	}
	fakeMsg := &fakeMessageSender{}
	sink := NewDeliverySink(fakeMsg, nil, nil)

	if err := sink.Fire(ctx, r); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if len(fakeMsg.calls) != 1 {
		t.Fatalf("expected 1 call; got %d", len(fakeMsg.calls))
	}
	if fakeMsg.calls[0].from != "" {
		t.Errorf("user-source reminder should have empty from; got %q", fakeMsg.calls[0].from)
	}
}

func TestDelivery_AgentTime_SupervisorTakesOver(t *testing.T) {
	r := &Reminder{
		Source: SourceAgent, TriggerKind: TriggerTime,
		SourceAgent: "docs_bot", TargetAgent: "docs_bot",
		Body: "x", ID: "reminder-docs_bot-1-1",
	}
	fakeMsg := &fakeMessageSender{}
	fakeSup := &fakeSupervisorRouter{routed: true}
	sink := NewDeliverySink(fakeMsg, nil, fakeSup)

	if err := sink.Fire(ctx, r); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if fakeSup.calls != 1 {
		t.Errorf("supervisor should have been consulted exactly once; got %d", fakeSup.calls)
	}
	if len(fakeMsg.calls) != 0 {
		t.Errorf("supervisor took over; MessageSender should NOT be called; got %+v", fakeMsg.calls)
	}
}

func TestDelivery_AgentTime_SupervisorDeclines_FallsThrough(t *testing.T) {
	r := &Reminder{
		Source: SourceAgent, TriggerKind: TriggerTime,
		SourceAgent: "docs_bot", TargetAgent: "docs_bot",
		Body: "x", ID: "reminder-docs_bot-1-1",
	}
	fakeMsg := &fakeMessageSender{}
	fakeSup := &fakeSupervisorRouter{routed: false}
	sink := NewDeliverySink(fakeMsg, nil, fakeSup)

	if err := sink.Fire(ctx, r); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if fakeSup.calls != 1 {
		t.Errorf("supervisor should have been consulted; got %d", fakeSup.calls)
	}
	if !fakeMsg.sentTo("@docs_bot") {
		t.Errorf("supervisor declined; normal delivery should fire; got %+v", fakeMsg.calls)
	}
}

func TestDelivery_AgentTime_SupervisorErrors_FallsThrough(t *testing.T) {
	r := &Reminder{
		Source: SourceAgent, TriggerKind: TriggerTime,
		SourceAgent: "docs_bot", TargetAgent: "docs_bot",
		Body: "x", ID: "reminder-docs_bot-1-1",
	}
	fakeMsg := &fakeMessageSender{}
	fakeSup := &fakeSupervisorRouter{err: errors.New("supervisor lookup failed")}
	sink := NewDeliverySink(fakeMsg, nil, fakeSup)

	if err := sink.Fire(ctx, r); err != nil {
		t.Fatalf("Fire: %v (supervisor error should fall through, not fail the fire)", err)
	}
	if !fakeMsg.sentTo("@docs_bot") {
		t.Error("supervisor error should fall through to normal delivery (conservative default)")
	}
}

func TestDelivery_AgentTime_EmptyTarget_Rejects(t *testing.T) {
	r := &Reminder{
		Source: SourceAgent, TriggerKind: TriggerTime,
		SourceAgent: "docs_bot", Body: "x",
		ID: "reminder-docs_bot-1-1",
	}
	sink := NewDeliverySink(&fakeMessageSender{}, nil, nil)
	if err := sink.Fire(ctx, r); err == nil {
		t.Error("expected error for empty target_agent on agent/time row")
	}
}

// daemon/time rows have a chain (not target_agent) per canonical §3.5 row 4.
// They should fan out the same way as daemon/condition.
func TestDelivery_DaemonTime_FansToChain(t *testing.T) {
	r := &Reminder{
		Source: SourceDaemon, TriggerKind: TriggerTime,
		TargetChain: []string{"@coordinator_main"},
		Body:        "C-B1 skill proposal awaiting review",
		ID:          "reminder-daemon-3-3",
		RaisedAt:    time.Now().Add(-30 * time.Minute),
	}
	fakeMsg := &fakeMessageSender{}
	sink := NewDeliverySink(fakeMsg, nil, nil)

	if err := sink.Fire(ctx, r); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if !fakeMsg.sentTo("@coordinator_main") {
		t.Errorf("expected delivery to @coordinator_main; got %+v", fakeMsg.calls)
	}
}

func TestDelivery_ChainEntries_AgentRefStripsAtPrefix(t *testing.T) {
	r := &Reminder{
		Source: SourceDaemon, TriggerKind: TriggerConditionPaneQuiet,
		TargetAgent: "x", TargetChain: []string{"@coord"},
		PaneSnapshot: "x", TriggerMeta: json.RawMessage(`{}`),
		ID: "reminder-x-1-1",
	}
	fakeMsg := &fakeMessageSender{}
	sink := NewDeliverySink(fakeMsg, nil, nil)
	_ = sink.Fire(ctx, r)
	if len(fakeMsg.calls) != 1 || fakeMsg.calls[0].to != "coord" {
		t.Errorf("expected to=coord (@ stripped); got %+v", fakeMsg.calls)
	}
}

func TestDelivery_ChainEntries_UnknownShapeSkipped(t *testing.T) {
	r := &Reminder{
		Source: SourceDaemon, TriggerKind: TriggerConditionPaneQuiet,
		TargetAgent: "x", TargetChain: []string{"not-an-agent-or-email", "@coord"},
		PaneSnapshot: "x", TriggerMeta: json.RawMessage(`{}`),
		ID: "reminder-x-1-1",
	}
	fakeMsg := &fakeMessageSender{}
	sink := NewDeliverySink(fakeMsg, nil, nil)
	if err := sink.Fire(ctx, r); err != nil {
		t.Fatalf("Fire: %v (the @coord recipient should succeed and absorb the unknown skip)", err)
	}
	if !fakeMsg.sentTo("@coord") {
		t.Error("expected @coord to receive despite unknown sibling entry")
	}
}

// --- isAgentRef + isEmailAddress unit coverage ---

func TestIsAgentRef(t *testing.T) {
	cases := map[string]bool{
		"@coord":      true,
		"@x":          true,
		"@":           false, // empty after @
		"coord":       false,
		"leon@x.com":  false,
		"":            false,
	}
	for in, want := range cases {
		if got := isAgentRef(in); got != want {
			t.Errorf("isAgentRef(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsEmailAddress(t *testing.T) {
	cases := map[string]bool{
		"leon@example.com":  true,
		"user+tag@host.net": true,
		"a@b.c":             true,
		"@coord":            false, // leading @ → agent ref, not email
		"plain":             false,
		"missing-at.com":    false,
		"user@":             false,
		"@bare":             false,
		"":                  false,
		"user@nodot":        false, // no dot in domain → not an email
	}
	for in, want := range cases {
		if got := isEmailAddress(in); got != want {
			t.Errorf("isEmailAddress(%q) = %v, want %v", in, got, want)
		}
	}
}
