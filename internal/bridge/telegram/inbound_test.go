package telegram

import (
	"context"
	"encoding/json"
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
