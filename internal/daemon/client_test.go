package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/rpc"
)

// waitForSocket waits for a Unix socket to become available, with timeout.
func waitForSocketReady(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("socket %s did not become available", socketPath)
}

func TestNewClient(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Start server
	server := NewServer(socketPath)
	ctx := context.Background()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	// Wait for server to be ready
	waitForSocketReady(t, socketPath)

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()
}

func TestClientCall(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Start server
	server := NewServer(socketPath)

	// Register health handler
	healthHandler := rpc.NewHealthHandler(time.Now(), "1.0.0", "test-repo")
	server.RegisterHandler("health", healthHandler.Handle)

	ctx := context.Background()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	// Wait for server to be ready
	waitForSocketReady(t, socketPath)

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call health method
	result, err := client.Call("health", map[string]any{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}

	// Parse result
	var healthResp rpc.HealthResponse
	if err := json.Unmarshal(result, &healthResp); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	// Verify response
	if healthResp.Status != "ok" {
		t.Errorf("expected status 'ok', got %s", healthResp.Status)
	}
}

func TestClientCallMethodNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Start server
	server := NewServer(socketPath)
	ctx := context.Background()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	// Wait for server to be ready
	waitForSocketReady(t, socketPath)

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call non-existent method
	_, err = client.Call("nonexistent", map[string]any{})
	if err == nil {
		t.Fatal("expected error for non-existent method")
	}
}

func TestWaitForSocket(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Start server in background after a delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		server := NewServer(socketPath)
		ctx := context.Background()
		if err := server.Start(ctx); err != nil {
			t.Logf("failed to start server: %v", err)
		}
		// Keep server running for a bit
		time.Sleep(500 * time.Millisecond)
		_ = server.Stop()
	}()

	// Wait for socket
	client, err := waitForSocket(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("waitForSocket failed: %v", err)
	}
	defer func() { _ = client.Close() }()
}

func TestWaitForSocketTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "nonexistent.sock")

	// Wait for socket that will never appear
	_, err := waitForSocket(socketPath, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClientConnectAndCallHealth(t *testing.T) {
	tmpDir := t.TempDir()

	// Use simple paths directly in tmpDir to avoid path length issues
	socketPath := filepath.Join(tmpDir, "d.sock")
	pidPath := filepath.Join(tmpDir, "d.pid")

	// Start server
	server := NewServer(socketPath)

	// Register health handler
	healthHandler := rpc.NewHealthHandler(time.Now(), "1.0.0", "test-repo")
	server.RegisterHandler("health", healthHandler.Handle)

	// Write PID file
	if err := WritePIDFile(pidPath); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}
	defer func() { _ = RemovePIDFile(pidPath) }()

	ctx := context.Background()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	// Wait for server to be ready
	waitForSocketReady(t, socketPath)

	// Test that client can connect
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Verify we can call methods
	result, err := client.Call("health", map[string]any{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}

	var healthResp rpc.HealthResponse
	if err := json.Unmarshal(result, &healthResp); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if healthResp.Status != "ok" {
		t.Errorf("expected status 'ok', got %s", healthResp.Status)
	}
}
