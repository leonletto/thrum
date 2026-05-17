package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leonletto/thrum/internal/config"
)

// mockRPCServer creates a WebSocket server that handles message.send requests
// and returns a predictable response. It records all received requests.
func mockRPCServer(t *testing.T, handler func(req map[string]any) map[string]any) (*WSClient, func()) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var req map[string]any
			if err := json.Unmarshal(data, &req); err != nil {
				continue
			}

			resp := handler(req)
			resp["jsonrpc"] = "2.0"
			resp["id"] = req["id"]
			if err := conn.WriteJSON(resp); err != nil {
				return
			}
		}
	}))

	url := "ws" + srv.URL[4:] + "/ws" // http://... → ws://...
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, url)
	if err != nil {
		srv.Close()
		t.Fatalf("Dial: %v", err)
	}

	return client, func() {
		client.Close()
		srv.Close()
	}
}

func TestInboundRelayNewMessage(t *testing.T) {
	var captured map[string]any

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		captured = req
		return map[string]any{
			"result": map[string]any{
				"message_id": "msg_test_123",
			},
		}
	})
	defer cleanup()

	msgMap := NewMessageMap(100)
	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main", nil, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msg := InboundMessage{
		Text:      "Hello from Telegram",
		ChatID:    12345,
		MessageID: 42,
		Username:  "testuser",
		UserID:    67890,
	}

	if err := relay.relay(ctx, msg); err != nil {
		t.Fatalf("relay: %v", err)
	}

	// Verify RPC params
	params := captured["params"].(map[string]any)
	if params["content"] != "Hello from Telegram" {
		t.Errorf("content = %q, want 'Hello from Telegram'", params["content"])
	}
	if params["caller_agent_id"] != "user:leon-letto" {
		t.Errorf("caller_agent_id = %q, want 'user:leon-letto'", params["caller_agent_id"])
	}
	mentions := params["mentions"].([]any)
	if len(mentions) != 1 || mentions[0] != "@coordinator_main" {
		t.Errorf("mentions = %v, want [@coordinator_main]", mentions)
	}

	// Verify structured metadata
	structured := params["structured"].(map[string]any)
	if structured["source"] != "telegram" {
		t.Errorf("source = %v, want telegram", structured["source"])
	}
	if structured["chat_id"].(float64) != 12345 {
		t.Errorf("chat_id = %v, want 12345", structured["chat_id"])
	}
	if structured["telegram_user"] != "testuser" {
		t.Errorf("telegram_user = %v, want testuser", structured["telegram_user"])
	}

	// Verify message ID was stored in map
	thrumID, ok := msgMap.ThrumID(12345, 42)
	if !ok || thrumID != "msg_test_123" {
		t.Errorf("ThrumID = %q, %v; want msg_test_123, true", thrumID, ok)
	}
}

func TestInboundRelayReplyMessage(t *testing.T) {
	var sendReq map[string]any

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		method, _ := req["method"].(string)
		switch method {
		case "message.get":
			// Parent message was authored by the configured target, so the
			// reply's mention routing should still resolve to @coordinator_main.
			return map[string]any{
				"result": map[string]any{
					"message": map[string]any{
						"message_id": "msg_original",
						"author":     map[string]any{"agent_id": "coordinator_main"},
					},
				},
			}
		case "message.send":
			sendReq = req
			return map[string]any{
				"result": map[string]any{
					"message_id": "msg_reply_456",
				},
			}
		}
		return map[string]any{"result": map[string]any{}}
	})
	defer cleanup()

	msgMap := NewMessageMap(100)
	// Pre-populate map: Telegram msg 10 → Thrum msg_original
	msgMap.Store(12345, 10, "msg_original")

	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main", nil, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	replyID := 10
	msg := InboundMessage{
		Text:         "This is a reply",
		ChatID:       12345,
		MessageID:    42,
		Username:     "testuser",
		UserID:       67890,
		ReplyToMsgID: &replyID,
	}

	if err := relay.relay(ctx, msg); err != nil {
		t.Fatalf("relay: %v", err)
	}

	params := sendReq["params"].(map[string]any)
	if params["reply_to"] != "msg_original" {
		t.Errorf("reply_to = %v, want msg_original", params["reply_to"])
	}
	// Parent author is coordinator_main (the configured target), so mention
	// should still be @coordinator_main.
	mentions := params["mentions"].([]any)
	if len(mentions) != 1 || mentions[0] != "@coordinator_main" {
		t.Errorf("mentions = %v, want [@coordinator_main]", mentions)
	}
}

// TestInboundRelayReplyRoutesToParentAuthor verifies the fix for thrum-phn.1:
// when a Telegram user replies to a message authored by a non-target agent,
// the reply's mention is routed to THAT agent — not the hardcoded bridge
// target. This ensures conversations with non-target agents are symmetric.
func TestInboundRelayReplyRoutesToParentAuthor(t *testing.T) {
	var sendReq map[string]any

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		method, _ := req["method"].(string)
		switch method {
		case "message.get":
			// Parent message was authored by impl_writer_website_dev — a
			// different agent than the configured target.
			return map[string]any{
				"result": map[string]any{
					"message": map[string]any{
						"message_id": "msg_from_website_dev",
						"author":     map[string]any{"agent_id": "impl_writer_website_dev"},
					},
				},
			}
		case "message.send":
			sendReq = req
			return map[string]any{
				"result": map[string]any{
					"message_id": "msg_reply_xyz",
				},
			}
		}
		return map[string]any{"result": map[string]any{}}
	})
	defer cleanup()

	msgMap := NewMessageMap(100)
	msgMap.Store(12345, 10, "msg_from_website_dev")

	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main", nil, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	replyID := 10
	msg := InboundMessage{
		Text:         "Testing a reply directly to the sender",
		ChatID:       12345,
		MessageID:    42,
		Username:     "leon-letto",
		UserID:       67890,
		ReplyToMsgID: &replyID,
	}

	if err := relay.relay(ctx, msg); err != nil {
		t.Fatalf("relay: %v", err)
	}

	params := sendReq["params"].(map[string]any)
	mentions := params["mentions"].([]any)
	if len(mentions) != 1 || mentions[0] != "@impl_writer_website_dev" {
		t.Errorf("mentions = %v, want [@impl_writer_website_dev]", mentions)
	}
	if params["reply_to"] != "msg_from_website_dev" {
		t.Errorf("reply_to = %v, want msg_from_website_dev", params["reply_to"])
	}
}

// TestInboundRelayReplyToOwnMessage verifies that when a Telegram user replies
// to a message they themselves authored (possible if their agent posted on
// their behalf), we fall back to the configured target to avoid a self-mention
// loop.
func TestInboundRelayReplyToOwnMessage(t *testing.T) {
	var sendReq map[string]any

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		method, _ := req["method"].(string)
		switch method {
		case "message.get":
			return map[string]any{
				"result": map[string]any{
					"message": map[string]any{
						"message_id": "msg_self",
						"author":     map[string]any{"agent_id": "user:leon-letto"},
					},
				},
			}
		case "message.send":
			sendReq = req
			return map[string]any{
				"result": map[string]any{"message_id": "msg_reply"},
			}
		}
		return map[string]any{"result": map[string]any{}}
	})
	defer cleanup()

	msgMap := NewMessageMap(100)
	msgMap.Store(12345, 10, "msg_self")

	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main", nil, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	replyID := 10
	msg := InboundMessage{
		Text:         "self reply",
		ChatID:       12345,
		MessageID:    42,
		Username:     "leon-letto",
		ReplyToMsgID: &replyID,
	}

	if err := relay.relay(ctx, msg); err != nil {
		t.Fatalf("relay: %v", err)
	}

	params := sendReq["params"].(map[string]any)
	mentions := params["mentions"].([]any)
	if len(mentions) != 1 || mentions[0] != "@coordinator_main" {
		t.Errorf("mentions = %v, want [@coordinator_main] (fallback to avoid self-mention)", mentions)
	}
}

func TestInboundRelayUnknownReply(t *testing.T) {
	var captured map[string]any

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		captured = req
		return map[string]any{
			"result": map[string]any{
				"message_id": "msg_new",
			},
		}
	})
	defer cleanup()

	msgMap := NewMessageMap(100)
	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main", nil, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	replyID := 999 // not in map
	msg := InboundMessage{
		Text:         "Reply to unknown",
		ChatID:       12345,
		MessageID:    42,
		Username:     "testuser",
		UserID:       67890,
		ReplyToMsgID: &replyID,
	}

	if err := relay.relay(ctx, msg); err != nil {
		t.Fatalf("relay: %v", err)
	}

	// No reply_to should be set since the target isn't in the map
	params := captured["params"].(map[string]any)
	if _, exists := params["reply_to"]; exists {
		t.Error("expected no reply_to for unknown reply target")
	}
}

func TestInboundRelay_SenderIdentity(t *testing.T) {
	msg := InboundMessage{Username: "leon", IsBotSender: false}
	id := senderIdentity(msg)
	if id != "user:leon" {
		t.Errorf("got %q, want user:leon", id)
	}

	msg = InboundMessage{BotUsername: "falcon_bot", IsBotSender: true}
	id = senderIdentity(msg)
	if id != "bot:falcon_bot" {
		t.Errorf("got %q, want bot:falcon_bot", id)
	}
}

func TestInboundRelay_GroupMessage(t *testing.T) {
	var captured map[string]any

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		captured = req
		return map[string]any{
			"result": map[string]any{
				"message_id": "group_msg_001",
			},
		}
	})
	defer cleanup()

	groups := []config.TelegramGroup{
		{ChatID: -100123, Name: "dev-team"},
	}
	msgMap := NewMessageMap(100)
	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main", groups, "mybot")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msg := InboundMessage{
		Text:        "hello group",
		ChatID:      -100123,
		GroupChatID: -100123,
		MessageID:   10,
		Username:    "alice",
		UserID:      555,
	}

	if err := relay.Relay(ctx, msg); err != nil {
		t.Fatalf("Relay: %v", err)
	}

	params := captured["params"].(map[string]any)
	if params["group"] != "tg:dev-team" {
		t.Errorf("group = %q, want tg:dev-team", params["group"])
	}
	if params["content"] != "hello group" {
		t.Errorf("content = %q, want 'hello group'", params["content"])
	}
	if params["caller_agent_id"] != "user:alice" {
		t.Errorf("caller_agent_id = %q, want user:alice", params["caller_agent_id"])
	}

	// Verify message ID was stored in map
	thrumID, ok := msgMap.ThrumID(-100123, 10)
	if !ok || thrumID != "group_msg_001" {
		t.Errorf("ThrumID = %q, %v; want group_msg_001, true", thrumID, ok)
	}
}

func TestInboundRelay_GroupMentionOurBot(t *testing.T) {
	var captured map[string]any

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		captured = req
		return map[string]any{"result": map[string]any{"message_id": "x"}}
	})
	defer cleanup()

	groups := []config.TelegramGroup{{ChatID: -999, Name: "test-group"}}
	msgMap := NewMessageMap(100)
	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main", groups, "mybot")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msg := InboundMessage{
		Text:        "@mybot do the thing",
		ChatID:      -999,
		GroupChatID: -999,
		MessageID:   20,
		Username:    "bob",
		UserID:      777,
	}

	if err := relay.Relay(ctx, msg); err != nil {
		t.Fatalf("Relay: %v", err)
	}

	params := captured["params"].(map[string]any)
	// @mybot mention should be stripped from content
	if params["content"] != "do the thing" {
		t.Errorf("content = %q, want 'do the thing'", params["content"])
	}
}

func TestInboundRelay_GroupMentionOtherBot_Ignored(t *testing.T) {
	called := false

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		called = true
		return map[string]any{"result": map[string]any{"message_id": "x"}}
	})
	defer cleanup()

	groups := []config.TelegramGroup{{ChatID: -999, Name: "test-group"}}
	msgMap := NewMessageMap(100)
	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main", groups, "mybot")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msg := InboundMessage{
		Text:        "@otherbot do something",
		ChatID:      -999,
		GroupChatID: -999,
		MessageID:   30,
		Username:    "bob",
		UserID:      777,
	}

	if err := relay.Relay(ctx, msg); err != nil {
		t.Fatalf("Relay: %v", err)
	}

	if called {
		t.Error("expected RPC to NOT be called for messages mentioning another bot")
	}
}

func TestInboundRelay_GroupUnknownChatID_Ignored(t *testing.T) {
	called := false

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		called = true
		return map[string]any{"result": map[string]any{"message_id": "x"}}
	})
	defer cleanup()

	groups := []config.TelegramGroup{{ChatID: -999, Name: "test-group"}}
	msgMap := NewMessageMap(100)
	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main", groups, "mybot")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msg := InboundMessage{
		Text:        "hello from unconfigured group",
		ChatID:      -8888,
		GroupChatID: -8888,
		MessageID:   40,
		Username:    "carol",
		UserID:      888,
	}

	if err := relay.Relay(ctx, msg); err != nil {
		t.Fatalf("Relay: %v", err)
	}

	if called {
		t.Error("expected RPC to NOT be called for unconfigured group chat ID")
	}
}

func TestInboundRelay_DMStillRoutesThroughRelayMethod(t *testing.T) {
	var captured map[string]any

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		captured = req
		return map[string]any{"result": map[string]any{"message_id": "dm_msg_001"}}
	})
	defer cleanup()

	groups := []config.TelegramGroup{{ChatID: -999, Name: "test-group"}}
	msgMap := NewMessageMap(100)
	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main", groups, "mybot")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// GroupChatID == 0 means DM
	msg := InboundMessage{
		Text:        "direct message",
		ChatID:      12345,
		GroupChatID: 0,
		MessageID:   50,
		Username:    "alice",
		UserID:      555,
	}

	if err := relay.Relay(ctx, msg); err != nil {
		t.Fatalf("Relay: %v", err)
	}

	params := captured["params"].(map[string]any)
	// DM path: uses r.target as mention
	mentions := params["mentions"].([]any)
	if len(mentions) != 1 || mentions[0] != "@coordinator_main" {
		t.Errorf("DM path: mentions = %v, want [@coordinator_main]", mentions)
	}
	// DM path: no "group" field
	if _, exists := params["group"]; exists {
		t.Error("DM path: unexpected 'group' field in params")
	}
}

// TestInboundRelay_FreshDMResolvesPendingNudge covers the thrum-48kt.3
// happy path: supervisor sends a fresh 'y' DM (no ReplyToMsgID), the
// lookup returns a pending nudge's thrumID, reply_to is set on the
// relayed message so TryResolve fires downstream.
func TestInboundRelay_FreshDMResolvesPendingNudge(t *testing.T) {
	var sendReq map[string]any

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		method, _ := req["method"].(string)
		switch method {
		case "message.send":
			sendReq = req
			return map[string]any{
				"result": map[string]any{"message_id": "msg_relay_fresh_y"},
			}
		}
		return map[string]any{"result": map[string]any{}}
	})
	defer cleanup()

	relay := NewInboundRelay(client, NewMessageMap(100), "user:leon-letto",
		"@coordinator_main", nil, "")
	relay.SetPendingNudgeLookup(func(ctx context.Context, supervisor string) (string, error) {
		if supervisor != "user:leon-letto" {
			t.Errorf("lookup called with wrong supervisor: %q", supervisor)
		}
		return "msg_pending_nudge_01", nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := relay.relay(ctx, InboundMessage{
		Text: "y", ChatID: 42, MessageID: 100, Username: "leon", UserID: 999,
		// ReplyToMsgID is nil — fresh DM, not a reply
	}); err != nil {
		t.Fatalf("relay: %v", err)
	}

	params := sendReq["params"].(map[string]any)
	if got := params["reply_to"]; got != "msg_pending_nudge_01" {
		t.Errorf("reply_to = %v, want msg_pending_nudge_01", got)
	}
}

// TestInboundRelay_FreshDMTokenVariants covers the full token set that
// the fresh-DM fallback accepts (y/n/yes/no/allow/deny, case-insensitive,
// whitespace-trimmed). Each triggers the lookup.
func TestInboundRelay_FreshDMTokenVariants(t *testing.T) {
	variants := []string{"y", "Y", "n", " N ", "yes", "YES", "no", "No",
		"allow", "Allow", "deny", "DENY"}

	for _, body := range variants {
		t.Run(body, func(t *testing.T) {
			var sendReq map[string]any
			client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
				if req["method"] == "message.send" {
					sendReq = req
				}
				return map[string]any{"result": map[string]any{"message_id": "msg_x"}}
			})
			defer cleanup()

			relay := NewInboundRelay(client, NewMessageMap(100), "user:leon-letto",
				"@coordinator_main", nil, "")
			relay.SetPendingNudgeLookup(func(ctx context.Context, s string) (string, error) {
				return "msg_pending", nil
			})

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := relay.relay(ctx, InboundMessage{Text: body, ChatID: 1, MessageID: 1}); err != nil {
				t.Fatalf("relay: %v", err)
			}

			params := sendReq["params"].(map[string]any)
			if got := params["reply_to"]; got != "msg_pending" {
				t.Errorf("variant %q: reply_to = %v, want msg_pending", body, got)
			}
		})
	}
}

// TestInboundRelay_FreshDMProseDoesNotTrigger guards against loose
// matching: "yeah sure let me think" or "nope, hold off" must NOT
// trigger the fallback.
func TestInboundRelay_FreshDMProseDoesNotTrigger(t *testing.T) {
	prose := []string{"yeah sure", "nope, hold off", "y please", "can you deny this?"}
	for _, body := range prose {
		t.Run(body, func(t *testing.T) {
			var sendReq map[string]any
			client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
				if req["method"] == "message.send" {
					sendReq = req
				}
				return map[string]any{"result": map[string]any{"message_id": "msg_x"}}
			})
			defer cleanup()

			lookupCalls := 0
			relay := NewInboundRelay(client, NewMessageMap(100), "user:leon-letto",
				"@coordinator_main", nil, "")
			relay.SetPendingNudgeLookup(func(ctx context.Context, s string) (string, error) {
				lookupCalls++
				return "msg_pending_DO_NOT_USE", nil
			})

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := relay.relay(ctx, InboundMessage{Text: body, ChatID: 1, MessageID: 1}); err != nil {
				t.Fatalf("relay: %v", err)
			}
			if lookupCalls != 0 {
				t.Errorf("variant %q: lookup should not have been called, got %d calls", body, lookupCalls)
			}
			params := sendReq["params"].(map[string]any)
			if _, exists := params["reply_to"]; exists {
				t.Errorf("variant %q: prose should not set reply_to, got %v", body, params["reply_to"])
			}
		})
	}
}

// TestInboundRelay_FreshDMNoPendingNudge covers the no-pending case:
// matching token but the lookup returns empty — relay must fall through
// to normal DM behavior (no reply_to, message still sent).
func TestInboundRelay_FreshDMNoPendingNudge(t *testing.T) {
	var sendReq map[string]any
	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		if req["method"] == "message.send" {
			sendReq = req
		}
		return map[string]any{"result": map[string]any{"message_id": "msg_x"}}
	})
	defer cleanup()

	relay := NewInboundRelay(client, NewMessageMap(100), "user:leon-letto",
		"@coordinator_main", nil, "")
	relay.SetPendingNudgeLookup(func(ctx context.Context, s string) (string, error) {
		return "", nil // no pending nudge for this supervisor
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := relay.relay(ctx, InboundMessage{Text: "y", ChatID: 1, MessageID: 1}); err != nil {
		t.Fatalf("relay: %v", err)
	}

	params := sendReq["params"].(map[string]any)
	if _, exists := params["reply_to"]; exists {
		t.Errorf("no-pending case: reply_to should not be set, got %v", params["reply_to"])
	}
	// Content still relays normally
	if params["content"] != "y" {
		t.Errorf("content = %v, want y", params["content"])
	}
}

// TestInboundRelay_FreshDMLookupErrorFallsThrough covers the error
// branch: if the lookup callback returns an error, relay must log and
// continue — no reply_to set, but the message still relays successfully.
// Locks in the degradation behavior documented at inbound.go:SetPendingNudgeLookup.
func TestInboundRelay_FreshDMLookupErrorFallsThrough(t *testing.T) {
	var sendReq map[string]any
	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		if req["method"] == "message.send" {
			sendReq = req
		}
		return map[string]any{"result": map[string]any{"message_id": "msg_x"}}
	})
	defer cleanup()

	relay := NewInboundRelay(client, NewMessageMap(100), "user:leon-letto",
		"@coordinator_main", nil, "")
	relay.SetPendingNudgeLookup(func(ctx context.Context, s string) (string, error) {
		return "", fmt.Errorf("synthetic store failure")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := relay.relay(ctx, InboundMessage{Text: "y", ChatID: 1, MessageID: 1})
	if err != nil {
		t.Fatalf("relay should log-and-continue on lookup error, got err: %v", err)
	}

	params := sendReq["params"].(map[string]any)
	if _, exists := params["reply_to"]; exists {
		t.Errorf("error branch: reply_to must NOT be set, got %v", params["reply_to"])
	}
	if params["content"] != "y" {
		t.Errorf("content = %v, want y (message still relayed)", params["content"])
	}
}

// TestInboundRelay_FreshDMSupervisorMismatch is the direct test for the
// scenario coordinator flagged: if a DIFFERENT human DMs the bot with
// 'y' while Leon has a pending nudge, the lookup is keyed on the OTHER
// human's r.userID — which returns empty — so Leon's nudge is not
// inadvertently resolved. R.userID naturally enforces this, but the
// test locks the behavior in.
func TestInboundRelay_FreshDMSupervisorMismatch(t *testing.T) {
	var sendReq map[string]any
	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		if req["method"] == "message.send" {
			sendReq = req
		}
		return map[string]any{"result": map[string]any{"message_id": "msg_x"}}
	})
	defer cleanup()

	// Bob's bridge instance — r.userID is Bob, not Leon.
	relay := NewInboundRelay(client, NewMessageMap(100), "user:bob",
		"@coordinator_main", nil, "")
	relay.SetPendingNudgeLookup(func(ctx context.Context, supervisor string) (string, error) {
		// The lookup should see Bob, not Leon.
		if supervisor != "user:bob" {
			t.Errorf("lookup called with %q, want user:bob", supervisor)
		}
		// No pending nudge for Bob, even though Leon has one. Realistic
		// store query keyed on recipient_agent_id returns empty here.
		return "", nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := relay.relay(ctx, InboundMessage{Text: "y", ChatID: 1, MessageID: 1}); err != nil {
		t.Fatalf("relay: %v", err)
	}

	params := sendReq["params"].(map[string]any)
	if _, exists := params["reply_to"]; exists {
		t.Errorf("mismatched supervisor: reply_to must NOT be set, got %v", params["reply_to"])
	}
}

// TestInboundRelay_FreshDMReplyThreadedStillTakesExistingPath is the
// regression guard: when ReplyToMsgID IS set, the fresh-DM lookup is
// bypassed entirely and the existing threading path controls reply_to.
func TestInboundRelay_FreshDMReplyThreadedStillTakesExistingPath(t *testing.T) {
	var sendReq map[string]any

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		method, _ := req["method"].(string)
		switch method {
		case "message.get":
			return map[string]any{
				"result": map[string]any{
					"message": map[string]any{
						"message_id": "msg_threaded_parent",
						"author":     map[string]any{"agent_id": "coordinator_main"},
					},
				},
			}
		case "message.send":
			sendReq = req
		}
		return map[string]any{"result": map[string]any{"message_id": "msg_relay"}}
	})
	defer cleanup()

	msgMap := NewMessageMap(100)
	msgMap.Store(42, 10, "msg_threaded_parent")

	relay := NewInboundRelay(client, msgMap, "user:leon-letto",
		"@coordinator_main", nil, "")
	lookupCalls := 0
	relay.SetPendingNudgeLookup(func(ctx context.Context, s string) (string, error) {
		lookupCalls++
		return "msg_SHOULD_NOT_BE_USED", nil
	})

	replyID := 10
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := relay.relay(ctx, InboundMessage{
		Text: "y", ChatID: 42, MessageID: 100, ReplyToMsgID: &replyID,
	}); err != nil {
		t.Fatalf("relay: %v", err)
	}

	params := sendReq["params"].(map[string]any)
	if got := params["reply_to"]; got != "msg_threaded_parent" {
		t.Errorf("reply_to = %v, want msg_threaded_parent (existing path)", got)
	}
	if lookupCalls != 0 {
		t.Errorf("threaded path should not invoke fresh-DM lookup, got %d calls", lookupCalls)
	}
}
