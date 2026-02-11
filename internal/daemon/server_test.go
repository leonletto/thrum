package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
)


func TestServerStartStop(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	server := NewServer(socketPath)
	ctx := context.Background()

	// Start server
	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Verify socket exists
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Fatal("socket file was not created")
	}

	// Stop server
	if err := server.Stop(); err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}

	// Verify socket was removed
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatal("socket file was not removed")
	}
}

func TestServerHandlerRegistration(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	server := NewServer(socketPath)
	ctx := context.Background()

	called := false
	server.RegisterHandler("test_method", func(ctx context.Context, params json.RawMessage) (any, error) {
		called = true
		return map[string]string{"status": "ok"}, nil
	})

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	// Wait for server to be ready
	waitForSocketReady(t, socketPath)

	// Connect to server
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to connect to server: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send JSON-RPC request
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "test_method",
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

	var resp jsonRPCResponse
	if err := json.Unmarshal(response[:n], &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error in response: %v", resp.Error)
	}

	if !called {
		t.Fatal("handler was not called")
	}

	var result map[string]string
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if result["status"] != "ok" {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestServerMethodNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	server := NewServer(socketPath)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	// Wait for server to be ready
	waitForSocketReady(t, socketPath)

	// Connect to server
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to connect to server: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send request for non-existent method
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "nonexistent_method",
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

	var resp jsonRPCResponse
	if err := json.Unmarshal(response[:n], &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error in response")
	}

	if resp.Error.Code != -32601 {
		t.Fatalf("expected error code -32601, got %d", resp.Error.Code)
	}
}

func TestServerInvalidJSONRPC(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	server := NewServer(socketPath)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	// Wait for server to be ready
	waitForSocketReady(t, socketPath)

	// Connect to server
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to connect to server: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send request with wrong jsonrpc version
	request := map[string]any{
		"jsonrpc": "1.0",
		"method":  "test",
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

	var resp jsonRPCResponse
	if err := json.Unmarshal(response[:n], &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error in response")
	}

	if resp.Error.Code != -32600 {
		t.Fatalf("expected error code -32600, got %d", resp.Error.Code)
	}
}

func TestServerHandlerError(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	server := NewServer(socketPath)
	ctx := context.Background()

	// Register handler that returns an error
	server.RegisterHandler("error_method", func(ctx context.Context, params json.RawMessage) (any, error) {
		return nil, fmt.Errorf("intentional error")
	})

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	// Wait for server to be ready
	waitForSocketReady(t, socketPath)

	// Connect to server
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to connect to server: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send request
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "error_method",
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

	var resp jsonRPCResponse
	if err := json.Unmarshal(response[:n], &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error in response")
	}

	if resp.Error.Code != -32000 {
		t.Fatalf("expected error code -32000, got %d", resp.Error.Code)
	}

	if resp.Error.Message != "intentional error" {
		t.Fatalf("unexpected error message: %s", resp.Error.Message)
	}
}

func TestServerStaleSocketRemoval(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create a stale socket file
	staleConn, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create stale socket: %v", err)
	}
	_ = staleConn.Close() // Close immediately to make it stale

	// New server should remove stale socket and start successfully
	server := NewServer(socketPath)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server with stale socket: %v", err)
	}
	defer func() { _ = server.Stop() }()

	// Verify we can connect to the new server
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to connect to new server: %v", err)
	}
	_ = conn.Close()
}
