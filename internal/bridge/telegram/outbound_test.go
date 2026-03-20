package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOutboundSkipsOwnMessages(t *testing.T) {
	relay := &OutboundRelay{
		userID: "user:leon-letto",
	}

	// Notification from the bridge user itself — should be skipped
	params, _ := json.Marshal(notificationParams{
		MessageID: "msg_1",
		Author:    struct {
			AgentID string `json:"agent_id"`
			Name    string `json:"name"`
		}{AgentID: "user:leon-letto", Name: "leon-letto"},
	})

	// This should not panic or call any RPC — ws and bot are nil
	relay.handleNotification(context.Background(), params)
}

func TestOutboundIsForUser(t *testing.T) {
	relay := &OutboundRelay{userID: "user:leon-letto"}

	tests := []struct {
		name       string
		recipients []string
		want       bool
	}{
		{"user is recipient", []string{"user:leon-letto", "other_agent"}, true},
		{"user not recipient", []string{"other_agent"}, false},
		{"empty recipients", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			full := &fullMessage{}
			for _, r := range tt.recipients {
				full.Message.Recipients = append(full.Message.Recipients, struct {
					AgentID string `json:"agent_id"`
				}{AgentID: r})
			}
			if got := relay.isForUser(full); got != tt.want {
				t.Errorf("isForUser() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOutboundFormatForTelegram(t *testing.T) {
	relay := &OutboundRelay{}

	full := &fullMessage{}
	full.Message.Body.Content = "Hello from Thrum"

	got := relay.formatForTelegram("coordinator_main", full)
	if got != "@coordinator_main: Hello from Thrum" {
		t.Errorf("format = %q, want '@coordinator_main: Hello from Thrum'", got)
	}

	// Empty author
	got = relay.formatForTelegram("", full)
	if got != "Hello from Thrum" {
		t.Errorf("format = %q, want 'Hello from Thrum'", got)
	}
}

func TestOutboundRelayChatIDRestriction(t *testing.T) {
	// Verify that OutboundRelay only sends to the configured chatID,
	// not to arbitrary chat IDs from the message map.
	// This is a design test — the chatID is a fixed field, not derived from data.
	relay := NewOutboundRelay(nil, nil, nil, "user:leon-letto", -100123456)
	if relay.chatID != -100123456 {
		t.Errorf("chatID = %d, want -100123456", relay.chatID)
	}
}

func TestOutboundRelayThreading(t *testing.T) {
	// Set up mock WS server that serves message.get responses
	var sentToTelegram struct {
		text    string
		replyTo *int
	}

	client, cleanup := mockRPCServer(t, func(req map[string]any) map[string]any {
		method := req["method"].(string)
		switch method {
		case "message.get":
			return map[string]any{
				"result": map[string]any{
					"message": map[string]any{
						"message_id": "msg_thrum_reply",
						"reply_to":   "msg_thrum_original",
						"author":     map[string]any{"agent_id": "coordinator_main"},
						"body":       map[string]any{"content": "Reply content"},
						"recipients": []map[string]any{
							{"agent_id": "user:leon-letto"},
						},
					},
				},
			}
		case "message.markRead":
			return map[string]any{"result": map[string]any{}}
		default:
			return map[string]any{"result": map[string]any{}}
		}
	})
	defer cleanup()

	msgMap := NewMessageMap(100)
	// Pre-populate: Thrum msg_thrum_original came from Telegram msg 10 in chat 12345
	msgMap.Store(12345, 10, "msg_thrum_original")

	// Create a mock bot that records what was sent
	mockBot := &Bot{
		messages: make(chan InboundMessage, 32),
	}
	// We can't use a real bot (needs API), so we test the logic components
	// The outbound relay uses bot.SendMessage which requires a real API client.
	// Instead, we verify the threading lookup works correctly.

	relay := NewOutboundRelay(client, mockBot, msgMap, "user:leon-letto", 12345)

	// Verify TeleID lookup for threading
	chatID, teleID, ok := msgMap.TeleID("msg_thrum_original")
	if !ok || chatID != 12345 || teleID != 10 {
		t.Errorf("TeleID = %d, %d, %v; want 12345, 10, true", chatID, teleID, ok)
	}

	_ = sentToTelegram
	_ = relay

	// Test fetchMessage + isForUser + format flow
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	full, err := relay.fetchMessage(ctx, "msg_thrum_reply")
	if err != nil {
		t.Fatalf("fetchMessage: %v", err)
	}

	if !relay.isForUser(full) {
		t.Error("expected message to be for user")
	}

	content := relay.formatForTelegram("coordinator_main", full)
	if content != "@coordinator_main: Reply content" {
		t.Errorf("format = %q", content)
	}

	// Verify reply_to would be set via msgMap
	if full.Message.ReplyTo != "msg_thrum_original" {
		t.Errorf("reply_to = %q, want msg_thrum_original", full.Message.ReplyTo)
	}
}

func TestOutboundNoInternalMetadata(t *testing.T) {
	relay := &OutboundRelay{}

	full := &fullMessage{}
	full.Message.MessageID = "msg_internal_123"
	full.Message.Author.AgentID = "coordinator_main"
	full.Message.Body.Content = "Hello from coordinator"

	// formatForTelegram must only include author display name and content —
	// never message_id, agent_id in raw form, session IDs, or structured metadata.
	result := relay.formatForTelegram("coordinator_main", full)

	if result != "@coordinator_main: Hello from coordinator" {
		t.Errorf("unexpected format: %q", result)
	}

	// Verify internal IDs don't leak
	if strings.Contains(result, "msg_internal_123") {
		t.Error("message ID leaked to outbound Telegram message")
	}
	if strings.Contains(result, "session") {
		t.Error("session data leaked to outbound Telegram message")
	}
}
