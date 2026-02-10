package daemon_test

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/types"
)

func TestClientRegistry_RegisterUnregister(t *testing.T) {
	registry := daemon.NewClientRegistry()

	// Create a mock connection
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	// Register a client
	registry.Register("ses_001", server)

	// Send notification - should succeed
	notification := &daemon.Notification{
		Method: "notification.message",
		Params: daemon.NotifyParams{
			MessageID: "msg_001",
			Timestamp: "2026-01-01T00:00:00Z",
		},
	}

	// Read in goroutine (net.Pipe is synchronous)
	type readResult struct {
		data []byte
		n    int
		err  error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 1024)
		if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			resultCh <- readResult{data: buf, n: 0, err: err}
			return
		}
		n, err := client.Read(buf)
		resultCh <- readResult{data: buf, n: n, err: err}
	}()

	// Send notification
	err := registry.Notify("ses_001", notification)
	if err != nil {
		t.Fatalf("Notify() failed: %v", err)
	}

	// Get read result
	result := <-resultCh
	if result.err != nil {
		t.Fatalf("Read() failed: %v", result.err)
	}
	buf := result.data
	n := result.n

	// Verify it's valid JSON
	var payload map[string]any
	if err := json.Unmarshal(buf[:n], &payload); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}

	if payload["jsonrpc"] != "2.0" {
		t.Errorf("Expected jsonrpc='2.0', got %v", payload["jsonrpc"])
	}
	if payload["method"] != "notification.message" {
		t.Errorf("Expected method='notification.message', got %v", payload["method"])
	}

	// Unregister
	registry.Unregister("ses_001")

	// Notify should now silently succeed (client not connected)
	err = registry.Notify("ses_001", notification)
	if err != nil {
		t.Errorf("Notify() after unregister should succeed, got error: %v", err)
	}
}

func TestClientRegistry_NotifyNonExistent(t *testing.T) {
	registry := daemon.NewClientRegistry()

	notification := &daemon.Notification{
		Method: "notification.message",
		Params: daemon.NotifyParams{
			MessageID: "msg_001",
			Timestamp: "2026-01-01T00:00:00Z",
		},
	}

	// Notify non-existent session - should silently succeed
	err := registry.Notify("ses_nonexistent", notification)
	if err != nil {
		t.Errorf("Notify() for non-existent session should succeed, got error: %v", err)
	}
}

func TestClientRegistry_NotifyDisconnected(t *testing.T) {
	registry := daemon.NewClientRegistry()

	server, client := net.Pipe()

	// Register
	registry.Register("ses_001", server)

	// Close client side (simulating disconnect)
	_ = client.Close()
	_ = server.Close()

	notification := &daemon.Notification{
		Method: "notification.message",
		Params: daemon.NotifyParams{
			MessageID: "msg_001",
			Timestamp: "2026-01-01T00:00:00Z",
		},
	}

	// Notify should fail and auto-unregister
	err := registry.Notify("ses_001", notification)
	if err == nil {
		t.Error("Notify() should fail for disconnected client")
	}

	// Second notify should succeed (client already unregistered)
	err = registry.Notify("ses_001", notification)
	if err != nil {
		t.Errorf("Second Notify() should succeed, got error: %v", err)
	}
}

func TestNotification_Format(t *testing.T) {
	registry := daemon.NewClientRegistry()

	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	registry.Register("ses_001", server)

	// Send notification with full params
	notification := &daemon.Notification{
		Method: "notification.message",
		Params: daemon.NotifyParams{
			MessageID: "msg_123",
			ThreadID:  "thread_456",
			Author: daemon.AuthorInfo{
				AgentID: "agent:reviewer:XYZ",
				Role:    "reviewer",
				Module:  "auth",
			},
			Preview: "This is a test message",
			Scopes: []types.Scope{
				{Type: "module", Value: "auth"},
			},
			MatchedSubscription: daemon.MatchInfo{
				SubscriptionID: 42,
				MatchType:      "scope",
			},
			Timestamp: "2026-01-01T12:00:00Z",
		},
	}

	// Read in goroutine (net.Pipe is synchronous)
	type readResult struct {
		data []byte
		n    int
		err  error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 2048)
		if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			resultCh <- readResult{data: buf, n: 0, err: err}
			return
		}
		n, err := client.Read(buf)
		resultCh <- readResult{data: buf, n: n, err: err}
	}()

	// Send notification
	err := registry.Notify("ses_001", notification)
	if err != nil {
		t.Fatalf("Notify() failed: %v", err)
	}

	// Get read result
	result := <-resultCh
	if result.err != nil {
		t.Fatalf("Read() failed: %v", result.err)
	}
	buf := result.data
	n := result.n

	var payload struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			MessageID string `json:"message_id"`
			ThreadID  string `json:"thread_id"`
			Author    struct {
				AgentID string `json:"agent_id"`
				Role    string `json:"role"`
				Module  string `json:"module"`
			} `json:"author"`
			Preview             string        `json:"preview"`
			Scopes              []types.Scope `json:"scopes"`
			MatchedSubscription struct {
				SubscriptionID int    `json:"subscription_id"`
				MatchType      string `json:"match_type"`
			} `json:"matched_subscription"`
			Timestamp string `json:"timestamp"`
		} `json:"params"`
	}

	if err := json.Unmarshal(buf[:n], &payload); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}

	// Verify all fields
	if payload.JSONRPC != "2.0" {
		t.Errorf("Expected jsonrpc='2.0', got '%s'", payload.JSONRPC)
	}
	if payload.Method != "notification.message" {
		t.Errorf("Expected method='notification.message', got '%s'", payload.Method)
	}
	if payload.Params.MessageID != "msg_123" {
		t.Errorf("Expected message_id='msg_123', got '%s'", payload.Params.MessageID)
	}
	if payload.Params.ThreadID != "thread_456" {
		t.Errorf("Expected thread_id='thread_456', got '%s'", payload.Params.ThreadID)
	}
	if payload.Params.Author.AgentID != "agent:reviewer:XYZ" {
		t.Errorf("Expected author.agent_id='agent:reviewer:XYZ', got '%s'", payload.Params.Author.AgentID)
	}
	if payload.Params.Preview != "This is a test message" {
		t.Errorf("Expected preview='This is a test message', got '%s'", payload.Params.Preview)
	}
	if payload.Params.MatchedSubscription.SubscriptionID != 42 {
		t.Errorf("Expected subscription_id=42, got %d", payload.Params.MatchedSubscription.SubscriptionID)
	}
	if payload.Params.MatchedSubscription.MatchType != "scope" {
		t.Errorf("Expected match_type='scope', got '%s'", payload.Params.MatchedSubscription.MatchType)
	}
}
