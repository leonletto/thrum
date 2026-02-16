package cli

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/leonletto/thrum/internal/types"
)

func TestSubscribe(t *testing.T) {
	mockResponse := SubscribeResponse{
		SubscriptionID: 42,
		SessionID:      "ses_01HXE...",
		CreatedAt:      "2026-02-03T10:00:00Z",
	}

	// Create mock daemon
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Start mock daemon with handler
	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			t.Logf("Failed to decode request: %v", err)
			return
		}

		// Verify method
		if request["method"] != "subscribe" {
			t.Errorf("Expected method 'subscribe', got %v", request["method"])
		}

		// Send response
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  mockResponse,
		}

		if err := encoder.Encode(response); err != nil {
			t.Logf("Failed to encode response: %v", err)
		}
	})

	// Wait for daemon to be ready
	<-daemon.Ready()

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call Subscribe with scope
	scope := types.Scope{Type: "module", Value: "auth"}
	opts := SubscribeOptions{
		Scope: &scope,
	}

	result, err := Subscribe(client, opts)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	if result.SubscriptionID != mockResponse.SubscriptionID {
		t.Errorf("SubscriptionID = %d, want %d", result.SubscriptionID, mockResponse.SubscriptionID)
	}
}

func TestUnsubscribe(t *testing.T) {
	mockResponse := UnsubscribeResponse{
		Removed: true,
	}

	// Create mock daemon
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Start mock daemon with handler
	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			t.Logf("Failed to decode request: %v", err)
			return
		}

		// Verify method
		if request["method"] != "unsubscribe" {
			t.Errorf("Expected method 'unsubscribe', got %v", request["method"])
		}

		// Send response
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  mockResponse,
		}

		if err := encoder.Encode(response); err != nil {
			t.Logf("Failed to encode response: %v", err)
		}
	})

	// Wait for daemon to be ready
	<-daemon.Ready()

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call Unsubscribe
	result, err := Unsubscribe(client, 42)
	if err != nil {
		t.Fatalf("Unsubscribe() error = %v", err)
	}

	if !result.Removed {
		t.Error("Expected Removed=true")
	}
}

func TestListSubscriptions(t *testing.T) {
	mockResponse := ListSubscriptionsResponse{
		Subscriptions: []SubscriptionInfo{
			{
				ID:         42,
				ScopeType:  "module",
				ScopeValue: "auth",
				CreatedAt:  "2026-02-03T10:00:00Z",
			},
			{
				ID:          43,
				MentionRole: "reviewer",
				CreatedAt:   "2026-02-03T10:05:00Z",
			},
		},
	}

	// Create mock daemon
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Start mock daemon with handler
	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			t.Logf("Failed to decode request: %v", err)
			return
		}

		// Verify method
		if request["method"] != "subscriptions.list" {
			t.Errorf("Expected method 'subscriptions.list', got %v", request["method"])
		}

		// Send response
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  mockResponse,
		}

		if err := encoder.Encode(response); err != nil {
			t.Logf("Failed to encode response: %v", err)
		}
	})

	// Wait for daemon to be ready
	<-daemon.Ready()

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call ListSubscriptions
	result, err := ListSubscriptions(client, "test-agent")
	if err != nil {
		t.Fatalf("ListSubscriptions() error = %v", err)
	}

	if len(result.Subscriptions) != 2 {
		t.Errorf("Subscriptions count = %d, want 2", len(result.Subscriptions))
	}
}

func TestFormatSubscribe(t *testing.T) {
	result := SubscribeResponse{
		SubscriptionID: 42,
		SessionID:      "ses_01HXE...",
		CreatedAt:      "2026-02-03T10:00:00Z",
	}

	output := FormatSubscribe(&result)

	expectedFields := []string{
		"42",
		"ses_01HXE...",
		"Created",
	}

	for _, field := range expectedFields {
		if !contains(output, field) {
			t.Errorf("Output should contain '%s'", field)
		}
	}
}

func TestFormatUnsubscribe(t *testing.T) {
	tests := []struct {
		name     string
		response UnsubscribeResponse
		contains string
	}{
		{
			name:     "removed",
			response: UnsubscribeResponse{Removed: true},
			contains: "removed",
		},
		{
			name:     "not_removed",
			response: UnsubscribeResponse{Removed: false},
			contains: "Failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatUnsubscribe(42, &tt.response)
			if !contains(output, tt.contains) {
				t.Errorf("Output should contain '%s'", tt.contains)
			}
		})
	}
}

func TestFormatSubscriptionsList(t *testing.T) {
	tests := []struct {
		name     string
		response ListSubscriptionsResponse
		contains []string
	}{
		{
			name:     "empty",
			response: ListSubscriptionsResponse{Subscriptions: []SubscriptionInfo{}},
			contains: []string{"No active"},
		},
		{
			name: "with_subscriptions",
			response: ListSubscriptionsResponse{
				Subscriptions: []SubscriptionInfo{
					{
						ID:         42,
						ScopeType:  "module",
						ScopeValue: "auth",
						CreatedAt:  "2026-02-03T10:00:00Z",
					},
					{
						ID:          43,
						MentionRole: "reviewer",
						CreatedAt:   "2026-02-03T10:05:00Z",
					},
				},
			},
			contains: []string{"42", "module:auth", "43", "@reviewer"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatSubscriptionsList(&tt.response)
			for _, substr := range tt.contains {
				if !contains(output, substr) {
					t.Errorf("Output should contain '%s'", substr)
				}
			}
		})
	}
}
