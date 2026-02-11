package cli

import (
	"encoding/json"
	"net"
	"testing"
)

func TestSyncForce(t *testing.T) {
	mockResponse := SyncForceResponse{
		Triggered:  true,
		LastSyncAt: "2026-02-03T12:30:00Z",
		SyncState:  "synced",
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
		if request["method"] != "sync.force" {
			t.Errorf("Expected method 'sync.force', got %v", request["method"])
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

	// Call SyncForce
	result, err := SyncForce(client)
	if err != nil {
		t.Fatalf("SyncForce() error = %v", err)
	}

	if !result.Triggered {
		t.Error("Expected Triggered=true")
	}

	if result.SyncState != "synced" {
		t.Errorf("SyncState = %s, want synced", result.SyncState)
	}
}

func TestSyncStatus(t *testing.T) {
	mockResponse := SyncStatusResponse{
		Running:    true,
		LastSyncAt: "2026-02-03T12:30:00Z",
		LastError:  "",
		SyncState:  "synced",
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
		if request["method"] != "sync.status" {
			t.Errorf("Expected method 'sync.status', got %v", request["method"])
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

	// Call SyncStatus
	result, err := SyncStatus(client)
	if err != nil {
		t.Fatalf("SyncStatus() error = %v", err)
	}

	if !result.Running {
		t.Error("Expected Running=true")
	}

	if result.SyncState != "synced" {
		t.Errorf("SyncState = %s, want synced", result.SyncState)
	}
}

func TestFormatSyncForce(t *testing.T) {
	result := SyncForceResponse{
		Triggered:  true,
		LastSyncAt: "2026-02-03T12:30:00Z",
		SyncState:  "synced",
	}

	output := FormatSyncForce(&result)

	expectedFields := []string{
		"triggered",
		"synced",
		"Last sync",
	}

	for _, field := range expectedFields {
		if !contains(output, field) {
			t.Errorf("Output should contain '%s'", field)
		}
	}
}

func TestFormatSyncStatus(t *testing.T) {
	tests := []struct {
		name     string
		response SyncStatusResponse
		contains []string
	}{
		{
			name: "running_synced",
			response: SyncStatusResponse{
				Running:    true,
				LastSyncAt: "2026-02-03T12:30:00Z",
				SyncState:  "synced",
			},
			contains: []string{"running", "synced", "Last sync"},
		},
		{
			name: "stopped",
			response: SyncStatusResponse{
				Running:   false,
				SyncState: "stopped",
			},
			contains: []string{"stopped", "never"},
		},
		{
			name: "error",
			response: SyncStatusResponse{
				Running:   true,
				SyncState: "error",
				LastError: "connection failed",
			},
			contains: []string{"error", "connection failed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatSyncStatus(&tt.response)
			for _, substr := range tt.contains {
				if !contains(output, substr) {
					t.Errorf("Output should contain '%s'", substr)
				}
			}
		})
	}
}
