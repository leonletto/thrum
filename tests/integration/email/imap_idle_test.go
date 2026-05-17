//go:build integration

// D-B1.19 integration tests — IMAP IDLE + inbound operator-reply routing (§17 AC #5).
//
// AC #5: An operator's email reply is received via IMAP, routed through
// Inbound.ProcessMessage, and dispatched to the local agent via message.send
// with reply_to populated from the In-Reply-To / msgmap threading chain.
//
// Pragmatic shortcuts:
//   - Constructs Inbound directly (no full Bridge spin-up).
//   - Uses go-imap v2 in-memory server (FakeIMAP harness) so no external
//     mail server is required.
//   - Uses a recordingDispatcher stub instead of a real WebSocket RPC client.
//   - goleak verifies no goroutine leaks survive the test.

package email_integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	goImap "github.com/emersion/go-imap/v2"
	"go.uber.org/goleak"

	"github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/tests/integration/email/harness"
)

// --- stubs -------------------------------------------------------------------

// dispatchRecord captures one SendMessage call from the inbound router.
type dispatchRecord struct {
	fromAgent string
	toAgent   string
	body      string
	replyTo   string
}

// inboundRecordingDispatcher implements email.MessageDispatcher. Thread-safe.
type inboundRecordingDispatcher struct {
	mu       sync.Mutex
	sends    []dispatchRecord
	seens    []goImap.UID
	sendErr  error
}

func (d *inboundRecordingDispatcher) SendMessage(_ context.Context, fromAgent, toAgent, body, replyTo string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sends = append(d.sends, dispatchRecord{fromAgent, toAgent, body, replyTo})
	return d.sendErr
}

func (d *inboundRecordingDispatcher) MarkSeen(_ context.Context, uid goImap.UID) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seens = append(d.seens, uid)
	return nil
}

func (d *inboundRecordingDispatcher) MoveToFolder(_ context.Context, _ goImap.UID, _ string) error {
	return nil
}

func (d *inboundRecordingDispatcher) Sends() []dispatchRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]dispatchRecord, len(d.sends))
	copy(cp, d.sends)
	return cp
}

// nopMesh is a no-op mesh handler for inbound tests that don't need mesh routing.
type nopMesh struct{}

func (nopMesh) HandleProtocol(_ context.Context, _ string, _ map[string]string, _ []byte) error {
	return nil
}

func (nopMesh) HandleStrangerPair(_ context.Context, _ map[string]string, _ []byte) (email.ProcessAction, error) {
	return email.ProcessAction{Kind: email.ActionDropped, Reason: "nop"}, nil
}

// --- helpers -----------------------------------------------------------------

// newInboundWithDeps constructs an Inbound with a real dedup+limiter (SQLite DB)
// and recording dispatcher. The "localAgents" set determines which agents are
// treated as local for routing purposes.
func newInboundWithDeps(t *testing.T, db *sql.DB, myDaemonID string, knownPeers map[string]bool, localAgents map[string]bool) (*email.Inbound, *inboundRecordingDispatcher, *email.MsgMap) {
	t.Helper()

	dedup := email.NewDedup(db)
	limiter := email.NewLimiter(db, email.LimiterConfig{
		InboundPerPeerPerHour:  1000,
		OutboundPerPeerPerHour: 1000,
		GlobalInboundPerMinute: 1000,
	}, nil)
	if err := limiter.Init(context.Background()); err != nil {
		t.Fatalf("limiter init: %v", err)
	}

	msgmapPath := filepath.Join(t.TempDir(), "email-msgmap.jsonl")
	msgmap, err := email.NewMsgMap(msgmapPath)
	if err != nil {
		t.Fatalf("msgmap: %v", err)
	}
	t.Cleanup(func() { _ = msgmap.Close() })

	dispatcher := &inboundRecordingDispatcher{}
	mesh := nopMesh{}

	cfg := email.InboundConfig{
		MyDaemonID:  myDaemonID,
		HopCeiling:  5,
		KnownPeers:  knownPeers,
		LocalAgents: localAgents,
	}
	inbound := email.NewInbound(cfg, dedup, limiter, msgmap, dispatcher, mesh)
	return inbound, dispatcher, msgmap
}

// buildOperatorReplyRaw builds a minimal RFC 5322 message that looks like
// an operator email reply to a prior thrum outbound message.
// The X-Thrum-* headers are set to route the message to toAgent on myDaemonID.
func buildOperatorReplyRaw(t *testing.T, myDaemonID, fromDaemonID, fromAgent, toAgent, inReplyTo string) []byte {
	t.Helper()
	msgID := fmt.Sprintf("<reply-%d@operator.example>", time.Now().UnixNano())
	var headers strings.Builder
	fmt.Fprintf(&headers, "Message-Id: %s\r\n", msgID)
	fmt.Fprintf(&headers, "From: Operator <operator@example.com>\r\n")
	fmt.Fprintf(&headers, "To: thrum-bridge@example.com\r\n")
	fmt.Fprintf(&headers, "Subject: Re: test\r\n")
	fmt.Fprintf(&headers, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	// X-Thrum routing headers
	fmt.Fprintf(&headers, "X-Thrum-Kind: message\r\n")
	fmt.Fprintf(&headers, "X-Thrum-To-Daemon: %s\r\n", myDaemonID)
	fmt.Fprintf(&headers, "X-Thrum-From-Daemon: %s\r\n", fromDaemonID)
	fmt.Fprintf(&headers, "X-Thrum-From-Agent: %s\r\n", fromAgent)
	fmt.Fprintf(&headers, "X-Thrum-To-Agent: %s\r\n", toAgent)
	fmt.Fprintf(&headers, "X-Thrum-Hop-Count: 0\r\n")
	if inReplyTo != "" {
		fmt.Fprintf(&headers, "In-Reply-To: %s\r\n", inReplyTo)
	}
	headers.WriteString("\r\n")
	headers.WriteString("Hello from the operator.\r\n")
	return []byte(headers.String())
}

// --- tests -------------------------------------------------------------------

// TestEmail_AC5_OperatorReply_RoutedBackToAgent verifies AC #5:
// an operator reply arrives via IMAP, the inbound router processes it through
// ProcessMessage, and the recording dispatcher receives a SendMessage call
// with the correct to-agent and (when threading is configured) a reply_to.
//
// Pragmatic note: this test constructs the Inbound pipeline directly rather
// than spinning up a full Bridge+daemon. The fake IMAP server (harness.FakeIMAP)
// is used to validate the APPEND/fetch round-trip path; the recording dispatcher
// captures what would have been sent as an RPC to the real daemon.
func TestEmail_AC5_OperatorReply_RoutedBackToAgent(t *testing.T) {
	const (
		myDaemonID   = "daemon-A-0000"
		fromDaemonID = "daemon-B-1111"
		fromAgent    = "operator@example.com"
		toAgent      = "local-agent"
	)

	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "imap_idle.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	knownPeers := map[string]bool{fromDaemonID: true}
	localAgents := map[string]bool{toAgent: true}
	inbound, dispatcher, _ := newInboundWithDeps(t, db, myDaemonID, knownPeers, localAgents)

	// Start a fake IMAP server and inject the operator reply.
	fakeIMAP := harness.NewFakeIMAP(t)
	rawMsg := buildOperatorReplyRaw(t, myDaemonID, fromDaemonID, fromAgent, toAgent, "")
	fakeIMAP.AppendMessage(t, string(rawMsg))

	// Fetch the message via IMAPClient.
	imapCfg := email.IMAPConfig{
		Host:         fakeIMAP.Host(),
		Port:         fakeIMAP.Port(),
		UseStartTLS:  true,
		UseIDLE:      false,
		Username:     harness.TestIMAPUser,
		Password:     harness.TestIMAPPass,
		PollInterval: 60 * time.Second,
		TLSConfig:    fakeIMAP.ClientTLS,
	}
	client := email.NewIMAPClient(imapCfg)
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("IMAPClient.Connect: %v", err)
	}

	msgs, err := client.Fetch(ctx, time.Now().Add(-1*time.Hour))
	if err != nil {
		_ = client.Close()
		t.Fatalf("Fetch: %v", err)
	}
	if len(msgs) == 0 {
		_ = client.Close()
		t.Fatal("expected at least 1 message from fake IMAP, got 0")
	}

	// Run the fetched message through the inbound pipeline.
	for _, msg := range msgs {
		action, err := inbound.ProcessMessage(ctx, msg.Bytes, msg.UID)
		if err != nil {
			_ = client.Close()
			t.Fatalf("ProcessMessage: %v", err)
		}
		t.Logf("ProcessMessage action: kind=%v reason=%s", action.Kind, action.Reason)
	}

	// Close explicitly before verifying dispatch results so goleak can see a
	// clean goroutine state (the imapclient reader goroutine exits on Close).
	_ = client.Close()
	// Allow the server connection goroutine to drain.
	time.Sleep(20 * time.Millisecond)

	// Verify the recording dispatcher received a SendMessage call.
	sends := dispatcher.Sends()
	if len(sends) == 0 {
		t.Fatal("expected at least 1 SendMessage dispatch, got 0")
	}

	got := sends[0]
	if got.toAgent != toAgent {
		t.Errorf("dispatched to_agent = %q, want %q", got.toAgent, toAgent)
	}
	if got.fromAgent != fromAgent {
		t.Errorf("dispatched from_agent = %q, want %q", got.fromAgent, fromAgent)
	}
	if !strings.Contains(got.body, "operator") {
		t.Errorf("body does not contain expected text: %q", got.body)
	}

	// Verify no goroutine leaks after explicit cleanup.
	// FakeIMAP server goroutine is still alive (blocked on Accept) until the
	// listener closes at t.Cleanup time — ignore it here.
	goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
		goleak.IgnoreAnyFunction("github.com/leonletto/thrum/tests/integration/email/harness.NewFakeIMAP.func2"),
		goleak.IgnoreAnyFunction("github.com/emersion/go-imap/v2/imapserver.(*Server).Serve"),
		goleak.IgnoreAnyFunction("github.com/emersion/go-imap/v2/imapserver.(*Conn).serve"),
	)
}

// TestEmail_AC5_OperatorReply_WithReplyTo verifies that when the inbound
// message's In-Reply-To header maps to a known thrum message ID via MsgMap,
// the dispatched SendMessage carries a non-empty reply_to field.
func TestEmail_AC5_OperatorReply_WithReplyTo(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)

	const (
		myDaemonID   = "daemon-A-0001"
		fromDaemonID = "daemon-B-2222"
		fromAgent    = "operator@example.com"
		toAgent      = "reply-agent"
		parentMsgID  = "<parent-msg-001@daemon.example>"
		parentThrum  = "thrum_msg_parent_001"
	)

	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "reply_to.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	knownPeers := map[string]bool{fromDaemonID: true}
	localAgents := map[string]bool{toAgent: true}
	inbound, dispatcher, msgmap := newInboundWithDeps(t, db, myDaemonID, knownPeers, localAgents)

	// Seed MsgMap so In-Reply-To resolves to a thrum message ID.
	if err := msgmap.Insert(parentMsgID, parentThrum); err != nil {
		t.Fatalf("msgmap.Insert: %v", err)
	}

	// Build a raw reply referencing the parent message ID.
	rawMsg := buildOperatorReplyRaw(t, myDaemonID, fromDaemonID, fromAgent, toAgent, parentMsgID)
	action, err := inbound.ProcessMessage(context.Background(), rawMsg, 1)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionRouted {
		t.Fatalf("expected ActionRouted, got kind=%v reason=%s", action.Kind, action.Reason)
	}

	sends := dispatcher.Sends()
	if len(sends) == 0 {
		t.Fatal("expected at least 1 SendMessage dispatch, got 0")
	}
	if sends[0].replyTo != parentThrum {
		t.Errorf("reply_to = %q, want %q", sends[0].replyTo, parentThrum)
	}
}
