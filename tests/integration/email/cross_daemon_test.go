//go:build integration

// D-B1.19 integration tests — cross-daemon message exchange (§17 AC #1, #4).
//
// AC #1: Two Bridge instances (daemon-A + daemon-B) exchange messages through a
//
//	shared fake-SMTP outbound server. A enqueues → fake-SMTP receives → B's
//	Inbound processes → dispatch recorded.
//
// AC #4: An agent message is enqueued via the outbound queue with template-
//
//	substituted From display name; the worker drains it to fake-SMTP;
//	the submitted envelope's raw content contains the expected display name
//	and, when reply_to was set, an In-Reply-To header.
//
// Pragmatic shortcuts (as allowed by the plan):
//   - No full daemon spin-up: Bridge sub-components are constructed directly.
//   - Cross-daemon "exchange" wires outbound Queue+Worker on side A to submit
//     to fake-SMTP, then feeds the received envelope bytes into B's Inbound.
//   - Recording dispatcher stub replaces the real WS-RPC message.send path.
//   - goleak asserts no goroutine leaks.

package email_integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/tests/integration/email/harness"
)

// --- helpers -----------------------------------------------------------------

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// buildRawAgentMessage constructs a valid RFC 5322 agent message with
// X-Thrum-* headers for cross-daemon routing.
func buildRawAgentMessage(params struct {
	fromDaemonID, toDaemonID string
	fromAgent, toAgent       string
	subject, body            string
	msgID, inReplyTo         string
}) []byte {
	if params.msgID == "" {
		params.msgID = fmt.Sprintf("<thrum-%d@test.local>", time.Now().UnixNano())
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Message-Id: %s\r\n", params.msgID)
	fmt.Fprintf(&sb, "From: Bridge <%s@test.local>\r\n", params.fromDaemonID)
	fmt.Fprintf(&sb, "To: bridge@test.local\r\n")
	fmt.Fprintf(&sb, "Subject: %s\r\n", params.subject)
	fmt.Fprintf(&sb, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&sb, "X-Thrum-Kind: message\r\n")
	fmt.Fprintf(&sb, "X-Thrum-To-Daemon: %s\r\n", params.toDaemonID)
	fmt.Fprintf(&sb, "X-Thrum-From-Daemon: %s\r\n", params.fromDaemonID)
	fmt.Fprintf(&sb, "X-Thrum-From-Agent: %s\r\n", params.fromAgent)
	fmt.Fprintf(&sb, "X-Thrum-To-Agent: %s\r\n", params.toAgent)
	fmt.Fprintf(&sb, "X-Thrum-Hop-Count: 0\r\n")
	if params.inReplyTo != "" {
		fmt.Fprintf(&sb, "In-Reply-To: %s\r\n", params.inReplyTo)
	}
	sb.WriteString("\r\n")
	sb.WriteString(params.body)
	sb.WriteString("\r\n")
	return []byte(sb.String())
}

// newInbound builds an Inbound instance with real SQLite-backed dedup+limiter.
func newInbound(t *testing.T, db *sql.DB, myDaemonID string, knownPeers, localAgents map[string]bool) (*email.Inbound, *inboundRecordingDispatcher) {
	t.Helper()
	dedup := email.NewDedup(db)
	limiter := email.NewLimiter(db, email.LimiterConfig{
		InboundPerPeerPerHour:  10000,
		OutboundPerPeerPerHour: 10000,
		GlobalInboundPerMinute: 10000,
	}, nil)
	if err := limiter.Init(context.Background()); err != nil {
		t.Fatalf("limiter.Init: %v", err)
	}
	msgmapPath := filepath.Join(t.TempDir(), "msgmap.jsonl")
	msgmap, err := email.NewMsgMap(msgmapPath)
	if err != nil {
		t.Fatalf("NewMsgMap: %v", err)
	}
	t.Cleanup(func() { _ = msgmap.Close() })

	dispatcher := &inboundRecordingDispatcher{}
	inbound := email.NewInbound(email.InboundConfig{
		MyDaemonID:  myDaemonID,
		HopCeiling:  5,
		KnownPeers:  knownPeers,
		LocalAgents: localAgents,
	}, dedup, limiter, msgmap, dispatcher, nopMesh{})
	return inbound, dispatcher
}

// smtpSubmitterFromFake returns an SMTPSubmitter that injects envelopes directly
// into the fake-SMTP server's receive list (in-process; no real TCP connection).
// This avoids the need for a real SMTP client with port validation on port 587/465.
type fakeSMTPAdapter struct {
	smtp *harness.FakeSMTP
}

func (a *fakeSMTPAdapter) Submit(_ context.Context, env email.Envelope) error {
	a.smtp.InjectEnvelope(harness.ReceivedEnvelope{
		From: env.From,
		To:   env.To,
		Data: string(env.Raw),
	})
	return nil
}

// --- tests -------------------------------------------------------------------

// TestEmail_AC1_TwoDaemonPair_Exchange verifies AC #1:
//
//	daemon-A enqueues a message for daemon-B → Worker drains → fake-SMTP
//	receives → the MIME bytes are fed into daemon-B's Inbound → B's recording
//	dispatcher gets a SendMessage call for the correct local agent.
func TestEmail_AC1_TwoDaemonPair_Exchange(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
		// Fake server goroutines are blocked on Accept/Read until t.Cleanup
		// closes the listener. goleak fires before t.Cleanup; they exit promptly.
		goleak.IgnoreAnyFunction("github.com/leonletto/thrum/tests/integration/email/harness.(*FakeSMTP).serve"),
	)

	const (
		daemonA = "daemon-A-AC1"
		daemonB = "daemon-B-AC1"
		agentA  = "agent-on-A"
		agentB  = "agent-on-B"
	)

	// --- daemon-A side: queue + worker ---
	dbA := openDB(t)
	queueA := email.NewQueue(dbA)
	smtpSrv := harness.NewFakeSMTP(t)
	adapter := &fakeSMTPAdapter{smtp: smtpSrv}
	workerCfg := email.QueueConfig{
		MaxAttempts:    3,
		BackoffInitial: 5 * time.Millisecond,
		BackoffCap:     50 * time.Millisecond,
	}
	workerA := email.NewWorker(queueA, adapter, nil, workerCfg)

	// Compose a cross-daemon agent message from A → B.
	msgParams := struct {
		fromDaemonID, toDaemonID string
		fromAgent, toAgent       string
		subject, body            string
		msgID, inReplyTo         string
	}{
		fromDaemonID: daemonA,
		toDaemonID:   daemonB,
		fromAgent:    agentA,
		toAgent:      agentB,
		subject:      "Hello from A",
		body:         "ping from daemon-A to daemon-B",
	}
	rawMIME := buildRawAgentMessage(msgParams)

	// Enqueue into A's outbound queue (using the raw MIME bytes directly as body).
	_, err := queueA.Enqueue(context.Background(), email.QueueEnvelope{
		FromAgent:   agentA,
		ToAddress:   "bridge@daemon-b.example",
		Subject:     "Hello from A",
		Body:        string(rawMIME),
		HeadersJSON: `{}`,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Drain A's queue → adapter submits to fake-SMTP in-process.
	sent, _, _, err := workerA.Drain(context.Background())
	if err != nil {
		t.Fatalf("workerA.Drain: %v", err)
	}
	if sent != 1 {
		t.Fatalf("expected 1 sent by workerA, got %d", sent)
	}

	// Verify fake-SMTP received the envelope.
	received := smtpSrv.Received()
	if len(received) == 0 {
		t.Fatal("fake-SMTP received 0 envelopes")
	}

	// --- daemon-B side: inbound pipeline ---
	dbB := openDB(t)
	// B knows A as a trusted peer.
	knownPeers := map[string]bool{daemonA: true}
	localAgents := map[string]bool{agentB: true}
	inboundB, dispatcherB := newInbound(t, dbB, daemonB, knownPeers, localAgents)

	// Feed the MIME bytes from the queue row (which are the raw message bytes)
	// into B's inbound pipeline.
	msgBytes := []byte(received[0].Data)
	action, err := inboundB.ProcessMessage(context.Background(), msgBytes, 1)
	if err != nil {
		t.Fatalf("B ProcessMessage: %v", err)
	}
	t.Logf("B ProcessMessage action: kind=%v reason=%s", action.Kind, action.Reason)

	if action.Kind != email.ActionRouted {
		t.Errorf("expected ActionRouted on B, got kind=%v reason=%s", action.Kind, action.Reason)
	}

	sends := dispatcherB.Sends()
	if len(sends) == 0 {
		t.Fatal("B dispatcher received 0 SendMessage calls")
	}
	if sends[0].toAgent != agentB {
		t.Errorf("B dispatch to_agent = %q, want %q", sends[0].toAgent, agentB)
	}
}

// TestEmail_AC4_ScheduledAgentSend_Threading verifies AC #4:
//
//	The outbound queue is enqueued with a from-display-name that uses the
//	{agent} / {handle} template; the worker submits it to fake-SMTP;
//	the submitted envelope's raw MIME contains the expected display name.
//	When reply_to is set, the raw MIME contains In-Reply-To.
//
// Note: display-name substitution is done by Outbound.handle(), not by the
// Worker. This test exercises the Queue→Worker→SMTP path directly using
// pre-composed MIME bytes (as Outbound would have produced them).
func TestEmail_AC4_ScheduledAgentSend_Threading(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
		goleak.IgnoreAnyFunction("github.com/leonletto/thrum/tests/integration/email/harness.(*FakeSMTP).serve"),
	)

	const (
		daemonA      = "daemon-A-AC4"
		daemonShort  = "da4"
		agentA       = "my-agent"
		daemonHandle = "alpha"
		parentMsgID  = "<parent-ac4@daemon.example>"
	)

	// Compose MIME using ComposeAgentMessage (the real encoder).
	env := email.AgentMessageEnvelope{
		FromAddr:        "thrum-bridge@alpha.example",
		FromDisplayName: fmt.Sprintf("%s @ %s", agentA, daemonHandle),
		ToAddr:          "dest@beta.example",
		Subject:         "[thrum:alpha/my-agent] ping",
		MessageID:       fmt.Sprintf("<thrum-%s@alpha.example>", daemonShort),
		InReplyTo:       parentMsgID,
		References:      []string{parentMsgID},
		Date:            time.Now().UTC(),
		FromDaemonID:    daemonA,
		ToDaemonID:      "daemon-B-AC4",
		FromAgent:       agentA,
		ToAgent:         "target-agent",
		ShortMessageID:  "msg_AC4",
		HopCount:        0,
		Body:            "AC4 test body content",
	}
	rawMIME, err := email.ComposeAgentMessage(env)
	if err != nil {
		t.Fatalf("ComposeAgentMessage: %v", err)
	}

	// Enqueue the pre-composed MIME into the queue.
	db := openDB(t)
	q := email.NewQueue(db)
	_, err = q.Enqueue(context.Background(), email.QueueEnvelope{
		FromAgent:   agentA,
		ToAddress:   "dest@beta.example",
		Subject:     env.Subject,
		Body:        string(rawMIME),
		HeadersJSON: `{}`,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Drain via the fake-SMTP adapter (records what the SMTP client would receive).
	smtpSrv := harness.NewFakeSMTP(t)
	adapter := &fakeSMTPAdapter{smtp: smtpSrv}
	cfg := email.QueueConfig{
		MaxAttempts:    3,
		BackoffInitial: 5 * time.Millisecond,
		BackoffCap:     50 * time.Millisecond,
	}
	w := email.NewWorker(q, adapter, nil, cfg)
	sent, _, _, err := w.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if sent != 1 {
		t.Fatalf("expected 1 sent, got %d", sent)
	}

	// Inspect the received MIME envelope.
	received := smtpSrv.Received()
	if len(received) == 0 {
		t.Fatal("fake-SMTP received 0 envelopes")
	}
	rawReceived := received[0].Data

	// Verify display name template substitution present in MIME.
	expectedDisplay := fmt.Sprintf("%s @ %s", agentA, daemonHandle)
	if !strings.Contains(rawReceived, expectedDisplay) {
		t.Errorf("MIME does not contain expected display name %q\nMIME head:\n%.500s", expectedDisplay, rawReceived)
	}

	// Verify In-Reply-To threading header present when reply_to was set.
	if !strings.Contains(rawReceived, parentMsgID) {
		t.Errorf("MIME does not contain In-Reply-To %q\nMIME head:\n%.500s", parentMsgID, rawReceived)
	}
}
