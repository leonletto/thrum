package email_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/schema"
)

// --- stubs ---

// chanSubscriber feeds synthetic MessageNotification events on a buffered
// channel. Close() must be called to signal Run() to exit cleanly.
type chanSubscriber struct {
	ch chan email.MessageNotification
}

func newChanSubscriber() *chanSubscriber {
	return &chanSubscriber{ch: make(chan email.MessageNotification, 16)}
}

func (s *chanSubscriber) Subscribe(_ context.Context, _ string) (<-chan email.MessageNotification, error) {
	return s.ch, nil
}

func (s *chanSubscriber) Send(n email.MessageNotification) {
	s.ch <- n
}

func (s *chanSubscriber) Close() {
	close(s.ch)
}

// --- test helpers ---

func newTestOutboundDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "outbound.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestLimiterForOutbound(t *testing.T) *email.Limiter {
	t.Helper()
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "limiter.db"))
	if err != nil {
		t.Fatalf("limiter OpenDB: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("limiter InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return email.NewLimiter(db, email.LimiterConfig{
		InboundPerPeerPerHour:  100,
		OutboundPerPeerPerHour: 100,
		GlobalInboundPerMinute: 1000,
	}, nil)
}

// newTestMsgMap returns a MsgMap backed by a temp-dir sidecar.
func newTestMsgMap(t *testing.T) *email.MsgMap {
	t.Helper()
	m, err := email.NewMsgMap(filepath.Join(t.TempDir(), "msgmap.jsonl"))
	if err != nil {
		t.Fatalf("NewMsgMap: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// defaultOutboundCfg returns an OutboundConfig wired for the supervisor path.
func defaultOutboundCfg() email.OutboundConfig {
	return email.OutboundConfig{
		MyDaemonID:            "daemon-aabbccdd",
		MyDaemonShort:         "aabbccdd",
		Host:                  "example.com",
		FromAddress:           "thrum@example.com",
		FromDisplayNameFormat: "{agent} @ {handle}",
		DaemonHandle:          "myhandle",
		TargetUser:            "leon-letto",
		TargetEmail:           "leon@example.com",
		DefaultMention:        "@coordinator_main",
		EmbedShortID:          false,
		KnownPeers:            map[string]config.EmailPeer{},
		UserPrefs:             map[string]config.UserPrefs{},
		Repo:                  "thrum",
	}
}

// notification produces a basic MessageNotification for tests.
func notification(to, authorID, body string) email.MessageNotification {
	return email.MessageNotification{
		Body:       body,
		Author:     struct{ AgentID string `json:"agent_id"` }{AgentID: authorID},
		To:         to,
		ThrumMsgID: "msg_01TEST",
		Subject:    "hello",
	}
}

// runRelayOnce sends one notification, waits for the relay to process it
// (channel drains + cancel), and returns. The caller checks DB / queue state.
func runRelayOnce(t *testing.T, o *email.Outbound, notif email.MessageNotification, sub *chanSubscriber) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sub.Send(notif)
	sub.Close()

	if err := o.Run(ctx); err != nil && err != context.DeadlineExceeded {
		// context.Canceled is expected when Run exits after channel close; non-ctx
		// errors are real failures.
		if !strings.Contains(err.Error(), "context") {
			t.Fatalf("Run returned unexpected error: %v", err)
		}
	}
}

// queueRowCount returns the number of rows in email_outbound_queue for the DB.
func queueRowCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM email_outbound_queue`).Scan(&n); err != nil {
		t.Fatalf("count queue rows: %v", err)
	}
	return n
}

// queueFirstRow returns the first row's to_address, status, headers_json, and body.
func queueFirstRow(t *testing.T, db *sql.DB) (toAddr, status, headersJSON, body string) {
	t.Helper()
	err := db.QueryRow(`
		SELECT to_address, status, headers_json, body FROM email_outbound_queue LIMIT 1`,
	).Scan(&toAddr, &status, &headersJSON, &body)
	if err != nil {
		t.Fatalf("queueFirstRow: %v", err)
	}
	return
}

// --- tests ---

func TestOutbound_SelfEchoSkipped(t *testing.T) {
	t.Parallel()
	db := newTestOutboundDB(t)
	sub := newChanSubscriber()
	cfg := defaultOutboundCfg()

	o := email.NewOutbound(cfg, sub, newTestMsgMap(t), newTestLimiterForOutbound(t), email.NewQueue(db))
	notif := notification("@coordinator_main", cfg.MyDaemonID, "hello")
	runRelayOnce(t, o, notif, sub)

	if n := queueRowCount(t, db); n != 0 {
		t.Errorf("expected 0 queued rows (self-echo), got %d", n)
	}
}

func TestOutbound_LocalRecipientSupervisorPath(t *testing.T) {
	t.Parallel()
	db := newTestOutboundDB(t)
	sub := newChanSubscriber()
	cfg := defaultOutboundCfg()

	o := email.NewOutbound(cfg, sub, newTestMsgMap(t), newTestLimiterForOutbound(t), email.NewQueue(db))
	notif := notification("@coordinator_main", "agent_a", "ping")
	runRelayOnce(t, o, notif, sub)

	if n := queueRowCount(t, db); n != 1 {
		t.Fatalf("expected 1 queued row, got %d", n)
	}
	toAddr, status, _, _ := queueFirstRow(t, db)
	if toAddr != cfg.TargetEmail {
		t.Errorf("to_address: got %q want %q", toAddr, cfg.TargetEmail)
	}
	if status != "queued" {
		t.Errorf("status: got %q want %q", status, "queued")
	}
}

func TestOutbound_CrossDaemonRecipientPath(t *testing.T) {
	t.Parallel()
	db := newTestOutboundDB(t)
	sub := newChanSubscriber()
	cfg := defaultOutboundCfg()
	cfg.KnownPeers = map[string]config.EmailPeer{
		"peer1": {
			Handle:       "peer1",
			DaemonID:     "daemon-peer1",
			ContactEmail: "relay@peer1.example.com",
		},
	}

	o := email.NewOutbound(cfg, sub, newTestMsgMap(t), newTestLimiterForOutbound(t), email.NewQueue(db))
	notif := notification("peer1:coordinator_main", "agent_a", "cross-daemon ping")
	runRelayOnce(t, o, notif, sub)

	if n := queueRowCount(t, db); n != 1 {
		t.Fatalf("expected 1 queued row, got %d", n)
	}
	toAddr, _, _, _ := queueFirstRow(t, db)
	// Plus-addressing: relay@peer1.example.com → relay+aabbccdd--peer1@peer1.example.com
	if !strings.Contains(toAddr, "relay+") {
		t.Errorf("expected plus-addressed to_address, got %q", toAddr)
	}
	if !strings.Contains(toAddr, "peer1.example.com") {
		t.Errorf("expected original domain in to_address, got %q", toAddr)
	}
}

func TestOutbound_PreferredChannelTelegramSkips(t *testing.T) {
	t.Parallel()
	db := newTestOutboundDB(t)
	sub := newChanSubscriber()
	cfg := defaultOutboundCfg()
	cfg.UserPrefs = map[string]config.UserPrefs{
		cfg.TargetUser: {PreferredChannel: "telegram"},
	}

	o := email.NewOutbound(cfg, sub, newTestMsgMap(t), newTestLimiterForOutbound(t), email.NewQueue(db))
	notif := notification("@coordinator_main", "agent_a", "should not enqueue")
	runRelayOnce(t, o, notif, sub)

	if n := queueRowCount(t, db); n != 0 {
		t.Errorf("expected 0 queued rows (telegram preferred), got %d", n)
	}
}

func TestOutbound_PreferredChannelEmailContinues(t *testing.T) {
	t.Parallel()
	db := newTestOutboundDB(t)
	sub := newChanSubscriber()
	cfg := defaultOutboundCfg()
	cfg.UserPrefs = map[string]config.UserPrefs{
		cfg.TargetUser: {PreferredChannel: "email"},
	}

	o := email.NewOutbound(cfg, sub, newTestMsgMap(t), newTestLimiterForOutbound(t), email.NewQueue(db))
	notif := notification("@coordinator_main", "agent_a", "email preferred")
	runRelayOnce(t, o, notif, sub)

	if n := queueRowCount(t, db); n != 1 {
		t.Errorf("expected 1 queued row (email preferred), got %d", n)
	}
}

func TestOutbound_PreferredChannelAbsentTreatedAsBoth(t *testing.T) {
	t.Parallel()
	db := newTestOutboundDB(t)
	sub := newChanSubscriber()
	cfg := defaultOutboundCfg()
	// No entry for TargetUser → treated as "both"
	cfg.UserPrefs = map[string]config.UserPrefs{}

	o := email.NewOutbound(cfg, sub, newTestMsgMap(t), newTestLimiterForOutbound(t), email.NewQueue(db))
	notif := notification("@coordinator_main", "agent_a", "no prefs set")
	runRelayOnce(t, o, notif, sub)

	if n := queueRowCount(t, db); n != 1 {
		t.Errorf("expected 1 queued row (absent prefs = both), got %d", n)
	}
}

func TestOutbound_ReplyToThreadingPopulated(t *testing.T) {
	t.Parallel()
	db := newTestOutboundDB(t)
	sub := newChanSubscriber()
	cfg := defaultOutboundCfg()

	o := email.NewOutbound(cfg, sub, newTestMsgMap(t), newTestLimiterForOutbound(t), email.NewQueue(db))
	notif := email.MessageNotification{
		Body:       "a reply",
		Author:     struct{ AgentID string `json:"agent_id"` }{AgentID: "agent_a"},
		To:         "@coordinator_main",
		ThrumMsgID: "msg_REPLY",
		ReplyTo:    "msg_PARENT",
		Subject:    "Re: hello",
	}
	runRelayOnce(t, o, notif, sub)

	if n := queueRowCount(t, db); n != 1 {
		t.Fatalf("expected 1 queued row, got %d", n)
	}
	_, _, headersJSON, _ := queueFirstRow(t, db)

	var headers map[string]string
	if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
		t.Fatalf("parse headers_json: %v", err)
	}
	inReplyTo, ok := headers["In-Reply-To"]
	if !ok || inReplyTo == "" {
		t.Errorf("expected In-Reply-To in headers_json, got: %v", headers)
	}
	if !strings.Contains(inReplyTo, "msg_PARENT") {
		t.Errorf("In-Reply-To should reference parent msg id, got %q", inReplyTo)
	}
}

func TestOutbound_RateLimitedPausedNoEnqueue(t *testing.T) {
	t.Parallel()
	db := newTestOutboundDB(t)
	limiterDB, err := schema.OpenDB(filepath.Join(t.TempDir(), "rl.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.InitDB(limiterDB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = limiterDB.Close() })

	// Threshold=2: first message is allowed (count reaches 2 → pause), second is blocked.
	limiter := email.NewLimiter(limiterDB, email.LimiterConfig{
		InboundPerPeerPerHour:  100,
		OutboundPerPeerPerHour: 2,
		GlobalInboundPerMinute: 1000,
	}, nil)

	sub := newChanSubscriber()
	cfg := defaultOutboundCfg()
	o := email.NewOutbound(cfg, sub, newTestMsgMap(t), limiter, email.NewQueue(db))

	// First message: allowed (count reaches threshold → pause).
	ctx1 := context.Background()
	sub.Send(notification("@coordinator_main", "agent_a", "msg1"))
	// Second message: should be blocked (paused).
	sub.Send(notification("@coordinator_main", "agent_a", "msg2"))
	sub.Close()

	ctx, cancel := context.WithTimeout(ctx1, 2*time.Second)
	defer cancel()
	_ = o.Run(ctx)

	// First message enqueued, second blocked.
	if n := queueRowCount(t, db); n != 1 {
		t.Errorf("expected 1 row (second was rate-limited), got %d", n)
	}
}

func TestOutbound_EnqueuesQueuedRow(t *testing.T) {
	t.Parallel()
	db := newTestOutboundDB(t)
	sub := newChanSubscriber()
	cfg := defaultOutboundCfg()

	o := email.NewOutbound(cfg, sub, newTestMsgMap(t), newTestLimiterForOutbound(t), email.NewQueue(db))
	notif := notification("@coordinator_main", "agent_b", "hello world")
	runRelayOnce(t, o, notif, sub)

	if n := queueRowCount(t, db); n != 1 {
		t.Fatalf("expected 1 queued row, got %d", n)
	}
	_, status, _, _ := queueFirstRow(t, db)
	if status != "queued" {
		t.Errorf("status: got %q want %q", status, "queued")
	}
}

func TestOutbound_HopCountZeroOnOrigination(t *testing.T) {
	t.Parallel()
	db := newTestOutboundDB(t)
	sub := newChanSubscriber()
	cfg := defaultOutboundCfg()

	o := email.NewOutbound(cfg, sub, newTestMsgMap(t), newTestLimiterForOutbound(t), email.NewQueue(db))
	notif := notification("@coordinator_main", "agent_c", "hop test")
	runRelayOnce(t, o, notif, sub)

	if n := queueRowCount(t, db); n != 1 {
		t.Fatalf("expected 1 queued row, got %d", n)
	}
	_, _, headersJSON, _ := queueFirstRow(t, db)

	var headers map[string]string
	if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
		t.Fatalf("parse headers_json: %v", err)
	}
	if hop, ok := headers["X-Thrum-Hop-Count"]; !ok || hop != "0" {
		t.Errorf("X-Thrum-Hop-Count: got %q want %q (ok=%v)", hop, "0", ok)
	}
}

func TestOutbound_TelegramCoexistenceBothChannels(t *testing.T) {
	t.Parallel()
	// preferred_channel="both": email bridge should enqueue (Telegram bridge
	// would also enqueue independently — no shared state between the two).
	db := newTestOutboundDB(t)
	sub := newChanSubscriber()
	cfg := defaultOutboundCfg()
	cfg.UserPrefs = map[string]config.UserPrefs{
		cfg.TargetUser: {PreferredChannel: "both"},
	}

	o := email.NewOutbound(cfg, sub, newTestMsgMap(t), newTestLimiterForOutbound(t), email.NewQueue(db))
	notif := notification("@coordinator_main", "agent_d", "both channels")
	runRelayOnce(t, o, notif, sub)

	if n := queueRowCount(t, db); n != 1 {
		t.Errorf("expected 1 queued row (both channels → email enqueues), got %d", n)
	}
}

func TestOutbound_TelegramCoexistenceTelegramOnly(t *testing.T) {
	t.Parallel()
	// preferred_channel="telegram": email bridge must NOT enqueue.
	db := newTestOutboundDB(t)
	sub := newChanSubscriber()
	cfg := defaultOutboundCfg()
	cfg.UserPrefs = map[string]config.UserPrefs{
		cfg.TargetUser: {PreferredChannel: "telegram"},
	}

	o := email.NewOutbound(cfg, sub, newTestMsgMap(t), newTestLimiterForOutbound(t), email.NewQueue(db))
	notif := notification("@coordinator_main", "agent_e", "telegram only")
	runRelayOnce(t, o, notif, sub)

	if n := queueRowCount(t, db); n != 0 {
		t.Errorf("expected 0 queued rows (telegram-only), got %d", n)
	}
}
