package email_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/schema"
)

// --- stubs ---

type dispatchCall struct {
	fromAgent string
	toAgent   string
	body      string
	replyTo   string
}

type recordingDispatcher struct {
	mu            sync.Mutex
	sends         []dispatchCall
	markSeens     []imap.UID
	moveToFolders []struct {
		uid    imap.UID
		folder string
	}
	sendErr     error
	markSeenErr error
	moveErr     error
}

func (r *recordingDispatcher) SendMessage(_ context.Context, fromAgent, toAgent, body, replyTo string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends = append(r.sends, dispatchCall{fromAgent, toAgent, body, replyTo})
	return r.sendErr
}

func (r *recordingDispatcher) MarkSeen(_ context.Context, uid imap.UID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markSeens = append(r.markSeens, uid)
	return r.markSeenErr
}

func (r *recordingDispatcher) MoveToFolder(_ context.Context, uid imap.UID, folder string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.moveToFolders = append(r.moveToFolders, struct {
		uid    imap.UID
		folder string
	}{uid, folder})
	return r.moveErr
}

type meshCall struct {
	verb    string
	headers map[string]string
	body    []byte
}

type recordingMesh struct {
	mu             sync.Mutex
	protocolCalls  []meshCall
	strangerCalls  []meshCall
	strangerAction email.ProcessAction
	strangerErr    error
}

func (r *recordingMesh) HandleProtocol(_ context.Context, verb string, headers map[string]string, body []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.protocolCalls = append(r.protocolCalls, meshCall{verb, headers, body})
	return nil
}

func (r *recordingMesh) HandleStrangerPair(_ context.Context, headers map[string]string, body []byte) (email.ProcessAction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.strangerCalls = append(r.strangerCalls, meshCall{"", headers, body})
	return r.strangerAction, r.strangerErr
}

// --- test helpers ---

// makeRawMessage builds a minimal valid RFC 5322 message with the supplied
// X-Thrum-* headers and a text/plain body.
func makeRawMessage(headers map[string]string, body string) []byte {
	var sb strings.Builder
	// Always emit a basic set of structural headers.
	if _, ok := headers["Message-Id"]; !ok {
		headers["Message-Id"] = fmt.Sprintf("<auto-%d@test.example>", time.Now().UnixNano())
	}
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	for k, v := range headers {
		sb.WriteString(k)
		sb.WriteString(": ")
		sb.WriteString(v)
		sb.WriteString("\r\n")
	}
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return []byte(sb.String())
}

// makeHTMLMessage builds a raw message with a text/html body.
func makeHTMLMessage(headers map[string]string, htmlBody string) []byte {
	var sb strings.Builder
	if _, ok := headers["Message-Id"]; !ok {
		headers["Message-Id"] = fmt.Sprintf("<auto-%d@test.example>", time.Now().UnixNano())
	}
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	for k, v := range headers {
		sb.WriteString(k)
		sb.WriteString(": ")
		sb.WriteString(v)
		sb.WriteString("\r\n")
	}
	sb.WriteString("\r\n")
	sb.WriteString(htmlBody)
	return []byte(sb.String())
}

type inboundFixture struct {
	inbound    *email.Inbound
	dispatcher *recordingDispatcher
	mesh       *recordingMesh
	dedup      *email.Dedup
	limiter    *email.Limiter
	msgmap     *email.MsgMap
}

func newFixture(t *testing.T, cfg email.InboundConfig) *inboundFixture {
	t.Helper()
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "ib.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	dedup := email.NewDedup(db)
	limiter := email.NewLimiter(db, email.LimiterConfig{
		InboundPerPeerPerHour:  100,
		OutboundPerPeerPerHour: 100,
		GlobalInboundPerMinute: 1000,
	}, nil)

	msgmapPath := filepath.Join(t.TempDir(), "msgmap.jsonl")
	msgmap, err := email.NewMsgMap(msgmapPath)
	if err != nil {
		t.Fatalf("NewMsgMap: %v", err)
	}
	t.Cleanup(func() { _ = msgmap.Close() })

	disp := &recordingDispatcher{}
	mesh := &recordingMesh{}

	ib := email.NewInbound(cfg, dedup, limiter, msgmap, disp, mesh)
	return &inboundFixture{ib, disp, mesh, dedup, limiter, msgmap}
}

func defaultInboundCfg() email.InboundConfig {
	return email.InboundConfig{
		MyDaemonID:       "daemon-local",
		HopCeiling:       5,
		UnknownRecipient: "drop",
		MoveAfterProcess: false,
		KnownPeers:       map[string]bool{"daemon-peer": true},
		LocalAgents:      map[string]bool{"agentA": true, "agentB": true},
	}
}

// baseHeaders returns the minimum required X-Thrum-* headers for a
// well-formed inbound message.
func baseHeaders(kind string) map[string]string {
	return map[string]string{
		"X-Thrum-To-Daemon":   "daemon-local",
		"X-Thrum-From-Daemon": "daemon-peer",
		"X-Thrum-From-Agent":  "agentRemote",
		"X-Thrum-To-Agent":    "agentA",
		"X-Thrum-Kind":        kind,
		"X-Thrum-Hop-Count":   "1",
	}
}

// --- tests ---

func TestInbound_DedupHitDrops(t *testing.T) {
	f := newFixture(t, defaultInboundCfg())
	ctx := context.Background()
	uid := imap.UID(1)
	msgID := "<dedup-hit@test.example>"

	h := baseHeaders("message")
	h["Message-Id"] = msgID
	raw := makeRawMessage(h, "hello")

	// First call routes successfully.
	action, err := f.inbound.ProcessMessage(ctx, raw, uid)
	if err != nil {
		t.Fatalf("first ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionRouted {
		t.Fatalf("first call: want Routed, got %v reason=%s", action.Kind, action.Reason)
	}

	// Second call with the same Message-Id — dedup table has the row.
	action, err = f.inbound.ProcessMessage(ctx, raw, uid+1)
	if err != nil {
		t.Fatalf("second ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionDropped {
		t.Fatalf("dedup: want Dropped, got %v", action.Kind)
	}
	if action.Reason != "dedup_hit" {
		t.Errorf("dedup reason: want dedup_hit, got %q", action.Reason)
	}
}

func TestInbound_NotForMeIgnored(t *testing.T) {
	f := newFixture(t, defaultInboundCfg())
	h := baseHeaders("message")
	h["X-Thrum-To-Daemon"] = "daemon-other"
	raw := makeRawMessage(h, "hello")

	action, err := f.inbound.ProcessMessage(context.Background(), raw, 1)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionDropped {
		t.Fatalf("want Dropped, got %v", action.Kind)
	}
	if action.Reason != "not_for_me" {
		t.Errorf("reason: want not_for_me, got %q", action.Reason)
	}
}

func TestInbound_SelfEchoDrops(t *testing.T) {
	f := newFixture(t, defaultInboundCfg())
	h := baseHeaders("message")
	h["X-Thrum-From-Daemon"] = "daemon-local" // same as MyDaemonID
	raw := makeRawMessage(h, "hello")

	action, err := f.inbound.ProcessMessage(context.Background(), raw, 1)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionDropped {
		t.Fatalf("L1: want Dropped, got %v", action.Kind)
	}
	if action.Reason != "self_echo" {
		t.Errorf("reason: want self_echo, got %q", action.Reason)
	}
}

func TestInbound_HopCountOverCeilingDrops(t *testing.T) {
	f := newFixture(t, defaultInboundCfg()) // HopCeiling=5
	h := baseHeaders("message")
	h["X-Thrum-Hop-Count"] = "6"
	raw := makeRawMessage(h, "hello")

	action, err := f.inbound.ProcessMessage(context.Background(), raw, 1)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionDropped {
		t.Fatalf("L2: want Dropped, got %v", action.Kind)
	}
	if action.Reason != "hop_ceiling" {
		t.Errorf("reason: want hop_ceiling, got %q", action.Reason)
	}
}

func TestInbound_KnownPeerProceeds(t *testing.T) {
	f := newFixture(t, defaultInboundCfg()) // daemon-peer is in KnownPeers
	h := baseHeaders("message")
	raw := makeRawMessage(h, "hello from peer")

	action, err := f.inbound.ProcessMessage(context.Background(), raw, 1)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionRouted {
		t.Fatalf("known peer: want Routed, got %v reason=%s", action.Kind, action.Reason)
	}
}

func TestInbound_UnknownPeerPeerPairTriggersBootstrap(t *testing.T) {
	f := newFixture(t, defaultInboundCfg())
	// mesh.HandleStrangerPair returns Pending.
	f.mesh.strangerAction = email.ProcessAction{
		Kind:   email.ActionPending,
		Reason: "operator_confirm_pair",
	}

	h := baseHeaders("protocol")
	h["X-Thrum-From-Daemon"] = "daemon-stranger" // not in KnownPeers
	h["X-Thrum-Verb"] = "peer.pair"
	raw := makeRawMessage(h, `{"daemon_id":"daemon-stranger"}`)

	action, err := f.inbound.ProcessMessage(context.Background(), raw, 1)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionPending {
		t.Fatalf("stranger pair: want Pending, got %v", action.Kind)
	}
	if action.Reason != "operator_confirm_pair" {
		t.Errorf("reason: want operator_confirm_pair, got %q", action.Reason)
	}
	f.mesh.mu.Lock()
	strangerCount := len(f.mesh.strangerCalls)
	f.mesh.mu.Unlock()
	if strangerCount != 1 {
		t.Errorf("HandleStrangerPair called %d times; want 1", strangerCount)
	}
}

func TestInbound_UnknownPeerOtherKindDrops(t *testing.T) {
	f := newFixture(t, defaultInboundCfg())
	h := baseHeaders("message")
	h["X-Thrum-From-Daemon"] = "daemon-stranger" // not in KnownPeers
	raw := makeRawMessage(h, "from unknown sender")

	action, err := f.inbound.ProcessMessage(context.Background(), raw, 1)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionDropped {
		t.Fatalf("unknown sender: want Dropped, got %v", action.Kind)
	}
	if action.Reason != "unknown_sender" {
		t.Errorf("reason: want unknown_sender, got %q", action.Reason)
	}
}

func TestInbound_RateLimitedPeerDrops(t *testing.T) {
	cfg := defaultInboundCfg()
	// Set low threshold so we hit the per-peer ceiling quickly.
	f := newFixture(t, cfg)
	// Replace the limiter with one that has a threshold of 1.
	db, _ := schema.OpenDB(filepath.Join(t.TempDir(), "rl.db"))
	_ = schema.InitDB(db)
	t.Cleanup(func() { _ = db.Close() })
	tightLimiter := email.NewLimiter(db, email.LimiterConfig{
		InboundPerPeerPerHour:  1,
		OutboundPerPeerPerHour: 100,
		GlobalInboundPerMinute: 1000,
	}, nil)
	f2 := &inboundFixture{
		dispatcher: f.dispatcher,
		mesh:       f.mesh,
		dedup:      f.dedup,
		limiter:    tightLimiter,
		msgmap:     f.msgmap,
	}
	f2.inbound = email.NewInbound(cfg, f.dedup, tightLimiter, f.msgmap, f.dispatcher, f.mesh)

	ctx := context.Background()

	// First message: allowed (count=1, threshold=1 means ≥1 pauses).
	// (IncrementInbound pauses when count >= threshold; threshold=1 means
	// the first message itself triggers pausing.)
	h1 := baseHeaders("message")
	h1["Message-Id"] = "<rl-first@test.example>"
	raw1 := makeRawMessage(h1, "first")
	action1, _ := f2.inbound.ProcessMessage(ctx, raw1, 1)
	// Either routed (if first call succeeded before pause) or dropped — just
	// ensure the second definitely drops.
	_ = action1

	// Second message from the same peer: must be dropped (rate_paused).
	h2 := baseHeaders("message")
	h2["Message-Id"] = "<rl-second@test.example>"
	raw2 := makeRawMessage(h2, "second")
	action2, err := f2.inbound.ProcessMessage(ctx, raw2, 2)
	if err != nil {
		t.Fatalf("ProcessMessage second: %v", err)
	}
	if action2.Kind != email.ActionDropped {
		t.Fatalf("rate-limited: want Dropped, got %v reason=%s", action2.Kind, action2.Reason)
	}
	if action2.Reason != "rate_paused" {
		t.Errorf("reason: want rate_paused, got %q", action2.Reason)
	}
}

func TestInbound_GlobalCeilingDrops(t *testing.T) {
	cfg := defaultInboundCfg()
	db, _ := schema.OpenDB(filepath.Join(t.TempDir(), "gc.db"))
	_ = schema.InitDB(db)
	t.Cleanup(func() { _ = db.Close() })
	// Global ceiling of 1 per minute.
	tightLimiter := email.NewLimiter(db, email.LimiterConfig{
		InboundPerPeerPerHour:  1000,
		OutboundPerPeerPerHour: 1000,
		GlobalInboundPerMinute: 1,
	}, nil)

	dedup := email.NewDedup(db)
	msgmap, _ := email.NewMsgMap(filepath.Join(t.TempDir(), "m.jsonl"))
	t.Cleanup(func() { _ = msgmap.Close() })
	disp := &recordingDispatcher{}
	mesh := &recordingMesh{}
	ib := email.NewInbound(cfg, dedup, tightLimiter, msgmap, disp, mesh)

	ctx := context.Background()

	// First message hits the global counter (count=1, ceiling=1 → exceeded).
	// bumpGlobalInbound increments then checks > ceiling, so ceiling=1 means
	// second message is the first to exceed.
	h1 := baseHeaders("message")
	h1["Message-Id"] = "<gc-first@test.example>"
	action1, _ := ib.ProcessMessage(ctx, makeRawMessage(h1, "first"), 1)
	_ = action1 // might route or drop depending on order

	h2 := baseHeaders("message")
	h2["Message-Id"] = "<gc-second@test.example>"
	action2, err := ib.ProcessMessage(ctx, makeRawMessage(h2, "second"), 2)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if action2.Kind != email.ActionDropped {
		t.Fatalf("global ceiling: want Dropped, got %v reason=%s", action2.Kind, action2.Reason)
	}
	if action2.Reason != "global_flood" {
		t.Errorf("reason: want global_flood, got %q", action2.Reason)
	}
}

func TestInbound_ProtocolDispatchesToMesh(t *testing.T) {
	f := newFixture(t, defaultInboundCfg())
	h := baseHeaders("protocol")
	h["X-Thrum-Verb"] = "peer.announce"
	raw := makeRawMessage(h, `{"hello":"world"}`)

	action, err := f.inbound.ProcessMessage(context.Background(), raw, 1)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionRouted {
		t.Fatalf("protocol: want Routed, got %v reason=%s", action.Kind, action.Reason)
	}
	if action.RoutedKind != "protocol" {
		t.Errorf("RoutedKind: want protocol, got %q", action.RoutedKind)
	}
	f.mesh.mu.Lock()
	calls := len(f.mesh.protocolCalls)
	verb := ""
	if calls > 0 {
		verb = f.mesh.protocolCalls[0].verb
	}
	f.mesh.mu.Unlock()
	if calls != 1 {
		t.Fatalf("HandleProtocol called %d times; want 1", calls)
	}
	if verb != "peer.announce" {
		t.Errorf("verb: want peer.announce, got %q", verb)
	}
}

func TestInbound_MessageDispatchesToMessageSendRpc(t *testing.T) {
	f := newFixture(t, defaultInboundCfg())
	h := baseHeaders("message")
	raw := makeRawMessage(h, "hello agent")

	action, err := f.inbound.ProcessMessage(context.Background(), raw, 1)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionRouted {
		t.Fatalf("message: want Routed, got %v reason=%s", action.Kind, action.Reason)
	}
	if action.RoutedKind != "message" {
		t.Errorf("RoutedKind: want message, got %q", action.RoutedKind)
	}
	f.dispatcher.mu.Lock()
	sends := len(f.dispatcher.sends)
	f.dispatcher.mu.Unlock()
	if sends != 1 {
		t.Fatalf("SendMessage called %d times; want 1", sends)
	}
	if f.dispatcher.sends[0].toAgent != "agentA" {
		t.Errorf("toAgent: want agentA, got %q", f.dispatcher.sends[0].toAgent)
	}
	if f.dispatcher.sends[0].body != "hello agent" {
		t.Errorf("body: want 'hello agent', got %q", f.dispatcher.sends[0].body)
	}
}

func TestInbound_UnknownRecipientDrops(t *testing.T) {
	f := newFixture(t, defaultInboundCfg())
	h := baseHeaders("message")
	h["X-Thrum-To-Agent"] = "agentUnknown" // not in LocalAgents
	raw := makeRawMessage(h, "hello")

	action, err := f.inbound.ProcessMessage(context.Background(), raw, 1)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionDropped {
		t.Fatalf("unknown recipient: want Dropped, got %v", action.Kind)
	}
	if action.Reason != "unknown_recipient" {
		t.Errorf("reason: want unknown_recipient, got %q", action.Reason)
	}
}

func TestInbound_ReplyThreadingResolvesParent(t *testing.T) {
	f := newFixture(t, defaultInboundCfg())
	ctx := context.Background()

	// Pre-seed the msgmap: external Message-Id → thrum msg id.
	parentMsgID := "<parent-123@peer.example>"
	thrumID := "msg_01PARENT"
	if err := f.msgmap.Insert(parentMsgID, thrumID); err != nil {
		t.Fatalf("msgmap Insert: %v", err)
	}

	h := baseHeaders("message")
	h["In-Reply-To"] = parentMsgID
	raw := makeRawMessage(h, "reply body")

	_, err := f.inbound.ProcessMessage(ctx, raw, 1)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}

	f.dispatcher.mu.Lock()
	sends := f.dispatcher.sends
	f.dispatcher.mu.Unlock()

	if len(sends) != 1 {
		t.Fatalf("SendMessage called %d times; want 1", len(sends))
	}
	if sends[0].replyTo != thrumID {
		t.Errorf("replyTo: want %q, got %q", thrumID, sends[0].replyTo)
	}
}

func TestInbound_HtmlOnlyStripsToText(t *testing.T) {
	f := newFixture(t, defaultInboundCfg())
	h := baseHeaders("message")
	// makeHTMLMessage sets Content-Type: text/html — ParseInbound strips it.
	raw := makeHTMLMessage(h, "<p>Hello <b>World</b></p>")

	_, err := f.inbound.ProcessMessage(context.Background(), raw, 1)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}

	f.dispatcher.mu.Lock()
	sends := f.dispatcher.sends
	f.dispatcher.mu.Unlock()

	if len(sends) != 1 {
		t.Fatalf("SendMessage called %d; want 1", len(sends))
	}
	// HTML tags must be gone.
	if bytes.Contains([]byte(sends[0].body), []byte("<")) {
		t.Errorf("body still contains HTML tags: %q", sends[0].body)
	}
	if !strings.Contains(sends[0].body, "Hello") || !strings.Contains(sends[0].body, "World") {
		t.Errorf("body missing expected text: %q", sends[0].body)
	}
}

func TestInbound_MarkSeenOnlyOnSuccess(t *testing.T) {
	f := newFixture(t, defaultInboundCfg())
	// Make the dispatcher return an error so the route step fails.
	f.dispatcher.sendErr = fmt.Errorf("rpc unavailable")

	h := baseHeaders("message")
	raw := makeRawMessage(h, "hello")

	_, err := f.inbound.ProcessMessage(context.Background(), raw, 99)
	// Error expected from the failed dispatch.
	if err == nil {
		t.Fatal("expected error from failed dispatch; got nil")
	}

	f.dispatcher.mu.Lock()
	markSeens := f.dispatcher.markSeens
	moves := f.dispatcher.moveToFolders
	f.dispatcher.mu.Unlock()

	if len(markSeens) != 0 {
		t.Errorf("MarkSeen called %d times on error; want 0", len(markSeens))
	}
	if len(moves) != 0 {
		t.Errorf("MoveToFolder called %d times on error; want 0", len(moves))
	}
}

func TestInbound_MoveAfterProcessHonored(t *testing.T) {
	cfg := defaultInboundCfg()
	cfg.MoveAfterProcess = true
	cfg.MoveFolder = "Thrum/myrepo"
	f := newFixture(t, cfg)

	h := baseHeaders("message")
	raw := makeRawMessage(h, "hello")

	action, err := f.inbound.ProcessMessage(context.Background(), raw, 77)
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if action.Kind != email.ActionRouted {
		t.Fatalf("want Routed, got %v reason=%s", action.Kind, action.Reason)
	}

	f.dispatcher.mu.Lock()
	markSeens := f.dispatcher.markSeens
	moves := f.dispatcher.moveToFolders
	f.dispatcher.mu.Unlock()

	if len(markSeens) != 0 {
		t.Errorf("MarkSeen called %d times; want 0 (MoveAfterProcess=true)", len(markSeens))
	}
	if len(moves) != 1 {
		t.Fatalf("MoveToFolder called %d times; want 1", len(moves))
	}
	if moves[0].uid != 77 {
		t.Errorf("uid: want 77, got %v", moves[0].uid)
	}
	if moves[0].folder != "Thrum/myrepo" {
		t.Errorf("folder: want Thrum/myrepo, got %q", moves[0].folder)
	}
}
