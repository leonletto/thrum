package rpc

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
)

func TestHealthCheckIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create server
	server := daemon.NewServer(socketPath)

	// Register health handler with a start time in the past to ensure positive uptime
	startTime := time.Now().Add(-10 * time.Millisecond)
	healthHandler := NewHealthHandler(startTime, "1.0.0-test", "test-repo-123")
	server.RegisterHandler("health", healthHandler.Handle)

	// Start server
	ctx := context.Background()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	// Wait for socket to be ready
	waitForSocketReady(t, socketPath)

	// Connect to server
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to connect to server: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send health check request
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "health",
		"params":  map[string]any{},
		"id":      1,
	}
	requestJSON, _ := json.Marshal(request)
	requestJSON = append(requestJSON, '\n')

	if _, err := conn.Write(requestJSON); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// Read response
	response := make([]byte, 4096)
	n, err := conn.Read(response)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	// Parse JSON-RPC response
	var rpcResp struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(response[:n], &rpcResp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Check for errors
	if rpcResp.Error != nil {
		t.Fatalf("RPC error: %v", rpcResp.Error)
	}

	// Parse health response
	var healthResp HealthResponse
	if err := json.Unmarshal(rpcResp.Result, &healthResp); err != nil {
		t.Fatalf("failed to parse health response: %v", err)
	}

	// Verify health response
	if healthResp.Status != "ok" {
		t.Errorf("expected status 'ok', got %s", healthResp.Status)
	}

	if healthResp.Version != "1.0.0-test" {
		t.Errorf("expected version '1.0.0-test', got %s", healthResp.Version)
	}

	if healthResp.RepoID != "test-repo-123" {
		t.Errorf("expected repo ID 'test-repo-123', got %s", healthResp.RepoID)
	}

	if healthResp.Uptime <= 0 {
		t.Errorf("expected positive uptime, got %d", healthResp.Uptime)
	}

	if healthResp.SyncState != "synced" {
		t.Errorf("expected sync state 'synced', got %s", healthResp.SyncState)
	}
}

// waitForSocketReady waits for a Unix socket to become available and accept connections, with timeout.
func waitForSocketReady(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		// Check if socket file exists
		if _, err := os.Stat(socketPath); err == nil {
			// Try to actually connect to verify server is ready
			conn, err := net.Dial("unix", socketPath)
			if err == nil {
				_ = conn.Close()
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("socket %s did not become available", socketPath)
}
