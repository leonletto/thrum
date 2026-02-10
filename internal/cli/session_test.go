package cli

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestSessionStart(t *testing.T) {
	mockResponse := SessionStartResponse{
		SessionID: "ses_01HXE...",
		AgentID:   "agent:implementer:ABC123",
		StartedAt: "2026-02-03T10:00:00Z",
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
		if request["method"] != "session.start" {
			t.Errorf("Expected method 'session.start', got %v", request["method"])
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
	time.Sleep(50 * time.Millisecond)

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call SessionStart
	opts := SessionStartOptions{
		AgentID: "agent:implementer:ABC123",
	}

	result, err := SessionStart(client, opts)
	if err != nil {
		t.Fatalf("SessionStart() error = %v", err)
	}

	if result.SessionID != mockResponse.SessionID {
		t.Errorf("SessionID = %s, want %s", result.SessionID, mockResponse.SessionID)
	}

	if result.AgentID != mockResponse.AgentID {
		t.Errorf("AgentID = %s, want %s", result.AgentID, mockResponse.AgentID)
	}
}

func TestSessionEnd(t *testing.T) {
	mockResponse := SessionEndResponse{
		SessionID: "ses_01HXE...",
		EndedAt:   "2026-02-03T12:00:00Z",
		Duration:  7200000, // 2 hours
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
		if request["method"] != "session.end" {
			t.Errorf("Expected method 'session.end', got %v", request["method"])
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
	time.Sleep(50 * time.Millisecond)

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call SessionEnd
	opts := SessionEndOptions{
		SessionID: "ses_01HXE...",
		Reason:    "normal",
	}

	result, err := SessionEnd(client, opts)
	if err != nil {
		t.Fatalf("SessionEnd() error = %v", err)
	}

	if result.SessionID != mockResponse.SessionID {
		t.Errorf("SessionID = %s, want %s", result.SessionID, mockResponse.SessionID)
	}

	if result.Duration != mockResponse.Duration {
		t.Errorf("Duration = %d, want %d", result.Duration, mockResponse.Duration)
	}
}

func TestFormatSessionStart(t *testing.T) {
	result := SessionStartResponse{
		SessionID: "ses_01HXE...",
		AgentID:   "agent:implementer:ABC123",
		StartedAt: "2026-02-03T10:00:00Z",
	}

	output := FormatSessionStart(&result)

	expectedFields := []string{
		"ses_01HXE...",
		"agent:implementer:ABC123",
		"Started",
	}

	for _, field := range expectedFields {
		if !contains(output, field) {
			t.Errorf("Output should contain '%s'", field)
		}
	}
}

func TestSessionHeartbeat(t *testing.T) {
	mockResponse := HeartbeatResponse{
		SessionID:  "ses_01HXE...",
		LastSeenAt: "2026-02-03T12:00:00Z",
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
		if request["method"] != "session.heartbeat" {
			t.Errorf("Expected method 'session.heartbeat', got %v", request["method"])
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
	time.Sleep(50 * time.Millisecond)

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call SessionHeartbeat
	opts := HeartbeatOptions{
		SessionID: "ses_01HXE...",
	}

	result, err := SessionHeartbeat(client, opts)
	if err != nil {
		t.Fatalf("SessionHeartbeat() error = %v", err)
	}

	if result.SessionID != mockResponse.SessionID {
		t.Errorf("SessionID = %s, want %s", result.SessionID, mockResponse.SessionID)
	}

	if result.LastSeenAt != mockResponse.LastSeenAt {
		t.Errorf("LastSeenAt = %s, want %s", result.LastSeenAt, mockResponse.LastSeenAt)
	}
}

func TestFormatHeartbeat(t *testing.T) {
	tests := []struct {
		name     string
		response HeartbeatResponse
		context  *AgentWorkContext
		contains []string
	}{
		{
			name: "basic heartbeat",
			response: HeartbeatResponse{
				SessionID:  "ses_01HXE...",
				LastSeenAt: "2026-02-03T12:00:00Z",
			},
			context:  nil,
			contains: []string{"Heartbeat sent", "ses_01HXE..."},
		},
		{
			name: "heartbeat with context",
			response: HeartbeatResponse{
				SessionID:  "ses_01HXE...",
				LastSeenAt: "2026-02-03T12:00:00Z",
			},
			context: &AgentWorkContext{
				Branch:          "feature/auth",
				UnmergedCommits: []CommitSummary{{SHA: "abc1234", Message: "test"}},
				ChangedFiles:    []string{"auth.go", "handler.go"},
			},
			contains: []string{"Heartbeat sent", "feature/auth", "1 commits", "2 files"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatHeartbeat(&tt.response, tt.context)
			for _, substr := range tt.contains {
				if !contains(output, substr) {
					t.Errorf("Output should contain '%s', got: %s", substr, output)
				}
			}
		})
	}
}

func TestFormatSessionEnd(t *testing.T) {
	result := SessionEndResponse{
		SessionID: "ses_01HXE...",
		EndedAt:   "2026-02-03T12:00:00Z",
		Duration:  7200000, // 2 hours
	}

	output := FormatSessionEnd(&result)

	expectedFields := []string{
		"ses_01HXE...",
		"Ended",
		"Duration",
	}

	for _, field := range expectedFields {
		if !contains(output, field) {
			t.Errorf("Output should contain '%s'", field)
		}
	}
}
