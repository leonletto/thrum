package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main")

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
	var captured map[string]any

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		captured = req
		return map[string]any{
			"result": map[string]any{
				"message_id": "msg_reply_456",
			},
		}
	})
	defer cleanup()

	msgMap := NewMessageMap(100)
	// Pre-populate map: Telegram msg 10 → Thrum msg_original
	msgMap.Store(12345, 10, "msg_original")

	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main")

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

	params := captured["params"].(map[string]any)
	if params["reply_to"] != "msg_original" {
		t.Errorf("reply_to = %v, want msg_original", params["reply_to"])
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
	relay := NewInboundRelay(client, msgMap, "user:leon-letto", "@coordinator_main")

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
