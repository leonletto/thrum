package daemon

import (
	"testing"
)

// mockWSClientNotifier is a mock WebSocket client notifier for testing.
type mockWSClientNotifier struct {
	notifications map[string][]any
	errors        map[string]error
}

func newMockWSClientNotifier() *mockWSClientNotifier {
	return &mockWSClientNotifier{
		notifications: make(map[string][]any),
		errors:        make(map[string]error),
	}
}

func (m *mockWSClientNotifier) Notify(sessionID string, notification any) error {
	if err, ok := m.errors[sessionID]; ok {
		return err
	}
	m.notifications[sessionID] = append(m.notifications[sessionID], notification)
	return nil
}

func TestBroadcaster_Notify_WebSocket(t *testing.T) {
	wsClients := newMockWSClientNotifier()
	broadcaster := NewBroadcaster(nil, wsClients)

	notification := map[string]any{
		"method": "notification.message",
		"params": map[string]any{
			"message_id": "msg-123",
			"preview":    "Test message",
		},
	}

	err := broadcaster.Notify("session-1", notification)
	if err != nil {
		t.Fatalf("Notify failed: %v", err)
	}

	if len(wsClients.notifications["session-1"]) != 1 {
		t.Fatalf("Expected 1 notification, got %d", len(wsClients.notifications["session-1"]))
	}
}

func TestBroadcaster_Notify_UnixSocket(t *testing.T) {
	// This would require setting up a mock Unix socket client registry
	// For now, we'll test the WebSocket path which is the primary use case for Epic 9
	t.Skip("Unix socket notification testing requires mock client registry")
}

func TestBroadcaster_Notify_ClientNotConnected(t *testing.T) {
	wsClients := newMockWSClientNotifier()
	broadcaster := NewBroadcaster(nil, wsClients)

	notification := map[string]any{
		"method": "notification.message",
		"params": map[string]any{
			"message_id": "msg-123",
		},
	}

	// Notify a session that doesn't exist - should not error
	err := broadcaster.Notify("nonexistent-session", notification)
	if err != nil {
		t.Fatalf("Notify should not error for nonexistent session: %v", err)
	}
}

func TestGetNotificationMethod(t *testing.T) {
	tests := []struct {
		name     string
		notif    map[string]any
		expected string
	}{
		{
			name:     "with method field",
			notif:    map[string]any{"method": "notification.message"},
			expected: "notification.message",
		},
		{
			name:     "without method field",
			notif:    map[string]any{},
			expected: "notification",
		},
		{
			name:     "with non-string method",
			notif:    map[string]any{"method": 123},
			expected: "notification",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getNotificationMethod(tt.notif)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestConvertToNotifyParams(t *testing.T) {
	notification := map[string]any{
		"method": "notification.message",
		"params": map[string]any{
			"message_id": "msg-123",
			"thread_id":  "thread-456",
			"preview":    "Test message",
			"timestamp":  "2024-01-01T00:00:00Z",
			"author": map[string]any{
				"agent_id": "agent:test:module:hash",
				"role":     "test",
				"module":   "module",
			},
			"matched_subscription": map[string]any{
				"subscription_id": 42,
				"match_type":      "scope",
			},
		},
	}

	result := convertToNotifyParams(notification)

	if result.MessageID != "msg-123" {
		t.Errorf("Expected message_id msg-123, got %s", result.MessageID)
	}
	if result.ThreadID != "thread-456" {
		t.Errorf("Expected thread_id thread-456, got %s", result.ThreadID)
	}
	if result.Preview != "Test message" {
		t.Errorf("Expected preview 'Test message', got %s", result.Preview)
	}
	if result.Timestamp != "2024-01-01T00:00:00Z" {
		t.Errorf("Expected timestamp 2024-01-01T00:00:00Z, got %s", result.Timestamp)
	}
	if result.Author.AgentID != "agent:test:module:hash" {
		t.Errorf("Expected author.agent_id agent:test:module:hash, got %s", result.Author.AgentID)
	}
	if result.MatchedSubscription.SubscriptionID != 42 {
		t.Errorf("Expected subscription_id 42, got %d", result.MatchedSubscription.SubscriptionID)
	}
	if result.MatchedSubscription.MatchType != "scope" {
		t.Errorf("Expected match_type scope, got %s", result.MatchedSubscription.MatchType)
	}
}

func TestConvertToNotifyParams_MissingFields(t *testing.T) {
	// Test with minimal notification
	notification := map[string]any{
		"params": map[string]any{
			"message_id": "msg-123",
		},
	}

	result := convertToNotifyParams(notification)

	if result.MessageID != "msg-123" {
		t.Errorf("Expected message_id msg-123, got %s", result.MessageID)
	}
	if result.ThreadID != "" {
		t.Errorf("Expected empty thread_id, got %s", result.ThreadID)
	}
}

func TestConvertToNotifyParams_InvalidParams(t *testing.T) {
	// Test with invalid params structure
	notification := map[string]any{
		"method": "notification.message",
		"params": "invalid",
	}

	result := convertToNotifyParams(notification)

	// Should return empty params without panic
	if result.MessageID != "" {
		t.Errorf("Expected empty message_id, got %s", result.MessageID)
	}
}
