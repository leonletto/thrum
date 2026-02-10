package cli

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatus(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	callCount := 0

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		// Handle multiple requests on same connection
		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			callCount++
			method, ok := request["method"].(string)
			if !ok {
				t.Error("method should be string")
				return
			}

			var response map[string]any

			switch method {
			case "health":
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"status":     "ok",
						"uptime_ms":  123456,
						"version":    "1.0.0",
						"repo_id":    "test-repo",
						"sync_state": "synced",
					},
				}

			case "agent.whoami":
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"agent_id":      "agent:implementer:ABC123",
						"role":          "implementer",
						"module":        "auth",
						"display":       "Auth Agent",
						"source":        "environment",
						"session_id":    "ses_01HXE8Z7",
						"session_start": time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
					},
				}

			case "message.list":
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"messages":    []map[string]any{},
						"total":       47,
						"unread":      12,
						"page":        1,
						"page_size":   0,
						"total_pages": 0,
					},
				}
			}

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	})

	time.Sleep(50 * time.Millisecond)

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	result, err := Status(client)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}

	// Verify health
	if result.Health.Status != "ok" {
		t.Errorf("Expected status 'ok', got %s", result.Health.Status)
	}

	if result.Health.SyncState != "synced" {
		t.Errorf("Expected sync_state 'synced', got %s", result.Health.SyncState)
	}

	// Verify agent
	if result.Agent == nil {
		t.Fatal("Expected agent info")
	}

	if result.Agent.Role != "implementer" {
		t.Errorf("Expected role 'implementer', got %s", result.Agent.Role)
	}

	if result.Agent.Module != "auth" {
		t.Errorf("Expected module 'auth', got %s", result.Agent.Module)
	}

	// Verify inbox
	if result.Inbox == nil {
		t.Fatal("Expected inbox info")
	}

	if result.Inbox.Total != 47 {
		t.Errorf("Expected total 47, got %d", result.Inbox.Total)
	}

	if result.Inbox.Unread != 12 {
		t.Errorf("Expected unread 12, got %d", result.Inbox.Unread)
	}
}

func TestFormatStatus(t *testing.T) {
	result := &StatusResult{
		Health: HealthResult{
			Status:    "ok",
			UptimeMs:  7200000, // 2 hours
			Version:   "1.0.0",
			RepoID:    "test-repo",
			SyncState: "synced",
		},
		Agent: &WhoamiResult{
			AgentID:      "agent:implementer:ABC123",
			Role:         "implementer",
			Module:       "auth",
			Display:      "Auth Agent",
			SessionID:    "ses_01HXE8Z7",
			SessionStart: time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
		},
		Inbox: &struct {
			Total  int `json:"total"`
			Unread int `json:"unread"`
		}{
			Total:  47,
			Unread: 12,
		},
	}

	output := FormatStatus(result)

	// Verify output contains expected elements
	if !strings.Contains(output, "agent:implementer:ABC123") {
		t.Error("Output should contain agent ID")
	}

	if !strings.Contains(output, "@implementer") {
		t.Error("Output should contain role")
	}

	if !strings.Contains(output, "auth") {
		t.Error("Output should contain module")
	}

	if !strings.Contains(output, "ses_01HXE8Z7") {
		t.Error("Output should contain session ID")
	}

	if !strings.Contains(output, "47 messages") {
		t.Error("Output should contain message count")
	}

	if !strings.Contains(output, "(12 unread)") {
		t.Error("Output should contain unread count")
	}

	if !strings.Contains(output, "✓ synced") {
		t.Error("Output should contain sync status")
	}

	if !strings.Contains(output, "v1.0.0") {
		t.Error("Output should contain version")
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"seconds", 45 * time.Second, "45s"},
		{"minutes", 5 * time.Minute, "5m"},
		{"hours", 2 * time.Hour, "2h"},
		{"hours and minutes", 2*time.Hour + 30*time.Minute, "2h30m"},
		{"days", 3 * 24 * time.Hour, "3d"},
		{"days and hours", 3*24*time.Hour + 5*time.Hour, "3d5h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDuration(tt.duration)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestReadWebSocketPort(t *testing.T) {
	t.Run("reads valid port", func(t *testing.T) {
		tmpDir := t.TempDir()
		varDir := filepath.Join(tmpDir, ".thrum", "var")
		if err := os.MkdirAll(varDir, 0750); err != nil {
			t.Fatal(err)
		}

		portPath := filepath.Join(varDir, "ws.port")
		if err := os.WriteFile(portPath, []byte("9123\n"), 0600); err != nil {
			t.Fatal(err)
		}

		port := ReadWebSocketPort(tmpDir)
		if port != 9123 {
			t.Errorf("expected port 9123, got %d", port)
		}
	})

	t.Run("returns 0 for missing file", func(t *testing.T) {
		tmpDir := t.TempDir()
		port := ReadWebSocketPort(tmpDir)
		if port != 0 {
			t.Errorf("expected port 0 for missing file, got %d", port)
		}
	})

	t.Run("returns 0 for invalid content", func(t *testing.T) {
		tmpDir := t.TempDir()
		varDir := filepath.Join(tmpDir, ".thrum", "var")
		if err := os.MkdirAll(varDir, 0750); err != nil {
			t.Fatal(err)
		}

		portPath := filepath.Join(varDir, "ws.port")
		if err := os.WriteFile(portPath, []byte("not-a-number\n"), 0600); err != nil {
			t.Fatal(err)
		}

		port := ReadWebSocketPort(tmpDir)
		if port != 0 {
			t.Errorf("expected port 0 for invalid content, got %d", port)
		}
	})
}

func TestFormatStatusWithWebSocket(t *testing.T) {
	result := &StatusResult{
		Health: HealthResult{
			Status:    "ok",
			UptimeMs:  7200000, // 2 hours
			Version:   "1.0.0",
			RepoID:    "test-repo",
			SyncState: "synced",
		},
		WebSocketPort: 9123,
	}

	output := FormatStatus(result)

	// Verify WebSocket port is shown
	if !strings.Contains(output, "WebSocket: ws://localhost:9123") {
		t.Errorf("Output should contain WebSocket URL, got:\n%s", output)
	}
}

func TestFormatStatusWithoutWebSocket(t *testing.T) {
	result := &StatusResult{
		Health: HealthResult{
			Status:    "ok",
			UptimeMs:  7200000,
			Version:   "1.0.0",
			RepoID:    "test-repo",
			SyncState: "synced",
		},
		WebSocketPort: 0, // No WebSocket
	}

	output := FormatStatus(result)

	// Verify WebSocket is not shown when port is 0
	if strings.Contains(output, "WebSocket") {
		t.Errorf("Output should not contain WebSocket when port is 0, got:\n%s", output)
	}
}
