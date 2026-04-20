package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/types"
)

// countingBroadcaster is a test double for WSBroadcaster that records
// how many times BroadcastAll was called and the last payload. Both
// fields are protected by mu so Calls() and LastPayload() are observed
// consistently (a prior version mixed atomic + mutex, which could let
// Calls() return 1 while LastPayload() still showed nil).
type countingBroadcaster struct {
	mu          sync.Mutex
	calls       int64
	lastPayload map[string]any
}

func (c *countingBroadcaster) BroadcastAll(notification any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if m, ok := notification.(map[string]any); ok {
		c.lastPayload = m
	}
}

func (c *countingBroadcaster) Calls() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingBroadcaster) LastPayload() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastPayload
}

// TestMessageHandler_NotifyMessageCreate_CallsBroadcaster — direct unit
// test of the new NotifyMessageCreate method. Verifies it builds the
// expected notification.message payload and calls BroadcastAll once.
func TestMessageHandler_NotifyMessageCreate_CallsBroadcaster(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_BCTEST", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = st.Close() }()

	handler := NewMessageHandler(st)
	bc := &countingBroadcaster{}
	handler.SetWSBroadcaster(bc)

	evt := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2026-04-20T03:00:00Z",
		EventID:   identity.GenerateEventID(),
		MessageID: "msg_test123",
		AgentID:   "sender_agent",
		SessionID: "ses_test",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "hello world",
		},
		Recipients: []string{"recipient_agent"},
	}

	handler.NotifyMessageCreate(evt)

	if got := bc.Calls(); got != 1 {
		t.Errorf("BroadcastAll calls = %d, want 1", got)
	}
	payload := bc.LastPayload()
	if payload == nil {
		t.Fatal("no payload captured")
	}
	if method, _ := payload["method"].(string); method != "notification.message" {
		t.Errorf("method = %q, want notification.message", method)
	}
	params, _ := payload["params"].(map[string]any)
	if params == nil {
		t.Fatal("params missing")
	}
	if got, _ := params["message_id"].(string); got != "msg_test123" {
		t.Errorf("message_id = %q, want msg_test123", got)
	}
}

// TestMessageHandler_NotifyMessageCreate_SkipsPeerOriginatedEvent — thrum-xfsb
// regression guard. An event whose OriginDaemon points at a PEER daemon arrived
// here via State.IngestSyncedEvent (sync_apply replica), not because we
// authored it. If this daemon broadcasts the notification to its Telegram
// bridge, the message fans out to bot B in addition to bot A — exactly the
// duplicate delivery symptom Leon reported (one nudge → two Telegram bots).
//
// Contract: NotifyMessageCreate must no-op when evt.OriginDaemon is non-empty
// and != this daemon's DaemonID. Matches the "owning daemon_id filter"
// requested by coordinator in the xfsb dispatch.
func TestMessageHandler_NotifyMessageCreate_SkipsPeerOriginatedEvent(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Explicit daemonID so we can compose a peer-origin event below.
	st, err := state.NewState(thrumDir, thrumDir, "r_XFSB", "d_LOCAL")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = st.Close() }()

	handler := NewMessageHandler(st)
	bc := &countingBroadcaster{}
	handler.SetWSBroadcaster(bc)

	peerEvt := types.MessageCreateEvent{
		Type:         "message.create",
		Timestamp:    "2026-04-20T05:40:00Z",
		EventID:      identity.GenerateEventID(),
		OriginDaemon: "d_PEER", // authored on a different daemon
		MessageID:    "msg_peer_origin",
		AgentID:      "sender_on_peer",
		SessionID:    "ses_peer",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "nudge authored on peer, synced here",
		},
		Recipients: []string{"user:leon-letto"},
	}

	handler.NotifyMessageCreate(peerEvt)

	if got := bc.Calls(); got != 0 {
		t.Errorf("peer-origin event triggered %d broadcasts, want 0 (would fan out to local Telegram bridge)", got)
	}

	// Positive control: a local-origin event DOES broadcast.
	localEvt := peerEvt
	localEvt.OriginDaemon = "d_LOCAL"
	localEvt.MessageID = "msg_local_origin"
	handler.NotifyMessageCreate(localEvt)
	if got := bc.Calls(); got != 1 {
		t.Errorf("local-origin event triggered %d broadcasts, want 1", got)
	}
}

// TestMessageHandler_NotifyMessageCreate_EmptyOriginDaemonBroadcasts covers
// legacy/test paths where an event arrives without OriginDaemon populated
// (e.g. a hand-crafted event that never passed through State.WriteEvent).
// The xfsb filter must not block these: empty origin is treated as local.
// This also protects the existing TestMessageHandler_NotifyMessageCreate_CallsBroadcaster
// fixture, which builds events without OriginDaemon.
func TestMessageHandler_NotifyMessageCreate_EmptyOriginDaemonBroadcasts(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_XFSB_EMPTY", "d_LOCAL")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = st.Close() }()

	handler := NewMessageHandler(st)
	bc := &countingBroadcaster{}
	handler.SetWSBroadcaster(bc)

	evt := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2026-04-20T05:41:00Z",
		MessageID: "msg_empty_origin",
		Body:      types.MessageBody{Format: "markdown", Content: "legacy"},
	}
	handler.NotifyMessageCreate(evt)
	if got := bc.Calls(); got != 1 {
		t.Errorf("empty-origin event triggered %d broadcasts, want 1 (treated as local)", got)
	}
}

// TestMessageHandler_NotifyMessageCreate_NilBroadcasterSafe — nil wiring
// must not panic (matches pattern of every other nil-safe Set-style
// broadcaster/permission hook in this codebase).
func TestMessageHandler_NotifyMessageCreate_NilBroadcasterSafe(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	_ = os.MkdirAll(thrumDir, 0o750)
	st, _ := state.NewState(thrumDir, thrumDir, "r_NIL", "")
	defer func() { _ = st.Close() }()

	handler := NewMessageHandler(st)
	// No SetWSBroadcaster.
	handler.NotifyMessageCreate(types.MessageCreateEvent{MessageID: "msg_nil"})
	// If the call panics, the test fails.
}

// TestMessageHandler_HookDispatch_FiresBroadcastOncePerMessage — integration
// test that wires state.SetOnEventWrite → NotifyMessageCreate (matching
// cmd/thrum/main.go) and then exercises both HandleSend AND a direct
// state.WriteEvent write (simulating permission.SendSupervisorMessage's
// path). Asserts each write triggers exactly one BroadcastAll, no
// double-fire on HandleSend.
func TestMessageHandler_HookDispatch_FiresBroadcastOncePerMessage(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	repoID := "r_HOOK_E2E"
	st, err := state.NewState(thrumDir, thrumDir, repoID, "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Register a sender agent + start a session so HandleSend can author.
	t.Setenv("THRUM_ROLE", "tester")
	t.Setenv("THRUM_MODULE", "bc-test")
	agentID := identity.GenerateAgentID(repoID, "tester", "bc-test", "")
	agentHandler := NewAgentHandler(st)
	regParams, _ := json.Marshal(RegisterRequest{Role: "tester", Module: "bc-test"})
	if _, err := agentHandler.HandleRegister(context.Background(), regParams); err != nil {
		t.Fatalf("register: %v", err)
	}
	sessionHandler := NewSessionHandler(st)
	sesParams, _ := json.Marshal(SessionStartRequest{AgentID: agentID})
	if _, err := sessionHandler.HandleStart(context.Background(), sesParams); err != nil {
		t.Fatalf("session.start: %v", err)
	}

	handler := NewMessageHandler(st)
	bc := &countingBroadcaster{}
	handler.SetWSBroadcaster(bc)

	// Wire the hook to mirror cmd/thrum/main.go SetOnEventWrite —
	// we invoke synchronously (not go func) so the test can observe
	// the call count deterministically without sleep loops. Production
	// dispatches NotifyMessageCreate on a goroutine (see main.go hook
	// body) to avoid blocking the state.WriteEvent writer. This test
	// does not exercise that goroutine directly; if a future refactor
	// changes the captured-evt semantics, add a -race dispatching
	// variant that exercises the async path.
	st.SetOnEventWrite(func(_ string, _ int64, event []byte) {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(event, &head); err != nil {
			return
		}
		if head.Type != "message.create" {
			return
		}
		var evt types.MessageCreateEvent
		if err := json.Unmarshal(event, &evt); err != nil {
			return
		}
		handler.NotifyMessageCreate(evt)
	})

	// Case 1: HandleSend → one broadcast (via hook only, no double-fire).
	sendParams, _ := json.Marshal(SendRequest{
		Content:       "from HandleSend",
		Format:        "markdown",
		CallerAgentID: agentID,
	})
	if _, err := handler.HandleSend(context.Background(), sendParams); err != nil {
		t.Fatalf("HandleSend: %v", err)
	}
	if got := bc.Calls(); got != 1 {
		t.Errorf("after HandleSend, BroadcastAll calls = %d, want 1 (no double-fire)", got)
	}

	// Case 2: simulate permission.SendSupervisorMessage — direct
	// state.WriteEvent of a message.create. Hook should fire broadcast.
	directEvt := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2026-04-20T03:05:00Z",
		EventID:   identity.GenerateEventID(),
		MessageID: identity.GenerateMessageID(),
		AgentID:   agentID,
		SessionID: "ses_direct_write",
		Body:      types.MessageBody{Format: "markdown", Content: "from WriteEvent"},
		Recipients: []string{"some_recipient"},
	}
	st.Lock()
	if err := st.WriteEvent(context.Background(), directEvt); err != nil {
		st.Unlock()
		t.Fatalf("WriteEvent: %v", err)
	}
	st.Unlock()
	if got := bc.Calls(); got != 2 {
		t.Errorf("after direct WriteEvent, BroadcastAll calls = %d, want 2", got)
	}
}

// TestMessageHandler_TwoDaemons_OnlyOriginBroadcasts — thrum-xfsb integration
// guard simulating the coordinator's acceptance criterion: two daemons each
// with a Telegram bridge, one permission nudge, exactly one bridge delivery.
//
// Daemon A authors the message locally (WriteEvent). Its hook must fire
// BroadcastAll on A's registry — that's how A's bridge forwards to its bot.
// Daemon B receives the same event via IngestSyncedEvent (the peer-sync
// replica path). B's hook must NOT fire BroadcastAll — otherwise B's bridge
// would also forward the nudge to its own bot, which is the duplicate-delivery
// symptom Leon saw (@impl_skills nudges in BOTH thrum-bot AND fm_mock_sf_bot).
func TestMessageHandler_TwoDaemons_OnlyOriginBroadcasts(t *testing.T) {
	// Daemon A: owning/origin.
	tmpA := t.TempDir()
	thrumA := filepath.Join(tmpA, ".thrum")
	if err := os.MkdirAll(thrumA, 0o750); err != nil {
		t.Fatalf("mkdir A: %v", err)
	}
	stA, err := state.NewState(thrumA, thrumA, "r_A", "d_A")
	if err != nil {
		t.Fatalf("NewState A: %v", err)
	}
	defer func() { _ = stA.Close() }()

	// Daemon B: peer receiver.
	tmpB := t.TempDir()
	thrumB := filepath.Join(tmpB, ".thrum")
	if err := os.MkdirAll(thrumB, 0o750); err != nil {
		t.Fatalf("mkdir B: %v", err)
	}
	stB, err := state.NewState(thrumB, thrumB, "r_B", "d_B")
	if err != nil {
		t.Fatalf("NewState B: %v", err)
	}
	defer func() { _ = stB.Close() }()

	handlerA := NewMessageHandler(stA)
	handlerB := NewMessageHandler(stB)
	bcA := &countingBroadcaster{}
	bcB := &countingBroadcaster{}
	handlerA.SetWSBroadcaster(bcA)
	handlerB.SetWSBroadcaster(bcB)

	// Wire both hooks with the same policy main.go uses. Synchronous
	// dispatch so assertions are deterministic; production wraps the
	// NotifyMessageCreate call in `go func(evt)` (see cmd/thrum/main.go
	// SetOnEventWrite closure) to avoid blocking the state.WriteEvent
	// writer. That goroutine path is covered separately — here we need
	// determinism so call-count assertions don't race the scheduler.
	hookFor := func(h *MessageHandler) state.EventWriteHook {
		return func(_ string, _ int64, event []byte) {
			var head struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(event, &head); err != nil {
				return
			}
			if head.Type != "message.create" {
				return
			}
			var evt types.MessageCreateEvent
			if err := json.Unmarshal(event, &evt); err != nil {
				return
			}
			h.NotifyMessageCreate(evt)
		}
	}
	stA.SetOnEventWrite(hookFor(handlerA))
	stB.SetOnEventWrite(hookFor(handlerB))

	// Simulate permission.SendSupervisorMessage on daemon A — direct
	// WriteEvent bypassing HandleSend (the path that 48kt.1 originally
	// unblocked for Telegram forwarding).
	evt := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2026-04-20T05:45:00Z",
		EventID:   identity.GenerateEventID(),
		MessageID: identity.GenerateMessageID(),
		AgentID:   "supervisor_on_A",
		SessionID: "ses_supervisor_A",
		Body:      types.MessageBody{Format: "markdown", Content: "[Permission] Allow tool use? y/n"},
		Recipients: []string{"user:leon-letto"},
	}
	stA.Lock()
	if err := stA.WriteEvent(context.Background(), evt); err != nil {
		stA.Unlock()
		t.Fatalf("daemon A WriteEvent: %v", err)
	}
	stA.Unlock()

	// Read the enriched event back from A's event log. WriteEvent stamps
	// origin_daemon=stA.DaemonID() into the stored JSON, so this is the
	// exact byte payload daemon B would receive over sync_apply. Doing a
	// real round-trip (rather than synthesising the enriched struct in
	// memory) pins the contract: if WriteEvent ever stops stamping
	// origin_daemon, this test flips — which is what we want, because
	// the whole xfsb filter depends on that stamp being present.
	events, _, _, err := stA.GetEventsSince(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("daemon A GetEventsSince: %v", err)
	}
	var enrichedBytes []byte
	for _, e := range events {
		if e.Type == "message.create" {
			enrichedBytes = e.EventJSON
			if e.OriginDaemon != stA.DaemonID() {
				t.Fatalf("stored event origin_daemon = %q, want %q (WriteEvent enrichment contract broken)",
					e.OriginDaemon, stA.DaemonID())
			}
			break
		}
	}
	if enrichedBytes == nil {
		t.Fatal("no message.create event found in daemon A's log")
	}

	// Daemon B ingests the replicated event. IngestSyncedEvent fires B's
	// hook; under the fix the NotifyMessageCreate call short-circuits
	// because evt.OriginDaemon != stB.DaemonID().
	if err := stB.IngestSyncedEvent(context.Background(), enrichedBytes); err != nil {
		t.Fatalf("daemon B IngestSyncedEvent: %v", err)
	}

	if got := bcA.Calls(); got != 1 {
		t.Errorf("daemon A BroadcastAll = %d, want 1 (origin daemon must forward to its bridge)", got)
	}
	if got := bcB.Calls(); got != 0 {
		t.Errorf("daemon B BroadcastAll = %d, want 0 (peer-received event must not fan out to local bridge)", got)
	}
}
