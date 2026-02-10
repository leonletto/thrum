package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"
	"github.com/leonletto/thrum/internal/websocket"
)

// TestMultiDaemonPortAllocation tests that multiple daemons can run
// simultaneously with different dynamically allocated WebSocket ports.
func TestMultiDaemonPortAllocation(t *testing.T) {
	// Create two temporary repo directories
	repo1 := t.TempDir()
	repo2 := t.TempDir()

	// Create .thrum/var directories for each repo
	varDir1 := filepath.Join(repo1, ".thrum", "var")
	varDir2 := filepath.Join(repo2, ".thrum", "var")
	if err := os.MkdirAll(varDir1, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(varDir2, 0750); err != nil {
		t.Fatal(err)
	}

	// Find two different available ports
	port1, err := FindAvailablePort(9100, 9199)
	if err != nil {
		t.Fatalf("failed to find first port: %v", err)
	}

	port2, err := FindAvailablePort(port1+1, 9299)
	if err != nil {
		t.Fatalf("failed to find second port: %v", err)
	}

	// Verify different ports
	if port1 == port2 {
		t.Fatalf("expected different ports, got %d for both", port1)
	}

	t.Logf("Using ports: %d and %d", port1, port2)

	// Create WebSocket servers for each "daemon"
	registry1 := websocket.NewSimpleRegistry()
	registry1.Register("ping", func(ctx context.Context, params json.RawMessage) (any, error) {
		return map[string]string{"daemon": "1", "status": "pong"}, nil
	})

	registry2 := websocket.NewSimpleRegistry()
	registry2.Register("ping", func(ctx context.Context, params json.RawMessage) (any, error) {
		return map[string]string{"daemon": "2", "status": "pong"}, nil
	})

	server1 := websocket.NewServer(fmt.Sprintf("localhost:%d", port1), registry1, nil)
	server2 := websocket.NewServer(fmt.Sprintf("localhost:%d", port2), registry2, nil)

	ctx := context.Background()

	// Start both servers
	if err := server1.Start(ctx); err != nil {
		t.Fatalf("failed to start server1: %v", err)
	}
	defer func() { _ = server1.Stop() }()

	if err := server2.Start(ctx); err != nil {
		t.Fatalf("failed to start server2: %v", err)
	}
	defer func() { _ = server2.Stop() }()

	// Write port files
	portPath1 := filepath.Join(varDir1, "ws.port")
	portPath2 := filepath.Join(varDir2, "ws.port")

	if err := WritePortFile(portPath1, port1); err != nil {
		t.Fatalf("failed to write port file 1: %v", err)
	}
	if err := WritePortFile(portPath2, port2); err != nil {
		t.Fatalf("failed to write port file 2: %v", err)
	}

	// Give servers time to start
	time.Sleep(100 * time.Millisecond)

	// Verify ports can be read from files
	readPort1, err := ReadPortFile(portPath1)
	if err != nil {
		t.Fatalf("failed to read port file 1: %v", err)
	}
	if readPort1 != port1 {
		t.Errorf("port file 1: expected %d, got %d", port1, readPort1)
	}

	readPort2, err := ReadPortFile(portPath2)
	if err != nil {
		t.Fatalf("failed to read port file 2: %v", err)
	}
	if readPort2 != port2 {
		t.Errorf("port file 2: expected %d, got %d", port2, readPort2)
	}

	// Connect to both WebSocket servers
	conn1, _, err := gws.DefaultDialer.Dial(fmt.Sprintf("ws://localhost:%d", port1), nil)
	if err != nil {
		t.Fatalf("failed to connect to server 1: %v", err)
	}
	defer func() { _ = conn1.Close() }()

	conn2, _, err := gws.DefaultDialer.Dial(fmt.Sprintf("ws://localhost:%d", port2), nil)
	if err != nil {
		t.Fatalf("failed to connect to server 2: %v", err)
	}
	defer func() { _ = conn2.Close() }()

	// Send ping to server 1
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "ping",
		"params":  map[string]any{},
		"id":      1,
	}

	if err := conn1.WriteJSON(request); err != nil {
		t.Fatalf("failed to send to server 1: %v", err)
	}

	var resp1 map[string]any
	if err := conn1.ReadJSON(&resp1); err != nil {
		t.Fatalf("failed to read from server 1: %v", err)
	}

	// Verify response from server 1
	result1, ok := resp1["result"].(map[string]any)
	if !ok {
		t.Fatalf("server 1: expected result object, got %T", resp1["result"])
	}
	if result1["daemon"] != "1" {
		t.Errorf("server 1: expected daemon '1', got %v", result1["daemon"])
	}

	// Send ping to server 2
	if err := conn2.WriteJSON(request); err != nil {
		t.Fatalf("failed to send to server 2: %v", err)
	}

	var resp2 map[string]any
	if err := conn2.ReadJSON(&resp2); err != nil {
		t.Fatalf("failed to read from server 2: %v", err)
	}

	// Verify response from server 2
	result2, ok := resp2["result"].(map[string]any)
	if !ok {
		t.Fatalf("server 2: expected result object, got %T", resp2["result"])
	}
	if result2["daemon"] != "2" {
		t.Errorf("server 2: expected daemon '2', got %v", result2["daemon"])
	}

	t.Log("Both daemons running with different ports and accessible via WebSocket")
}

// TestDynamicPortRange tests that FindAvailablePort properly handles port ranges.
func TestDynamicPortRange(t *testing.T) {
	// Find multiple consecutive ports to verify range works
	ports := make([]int, 5)

	for i := 0; i < 5; i++ {
		minPort := 9300
		if i > 0 {
			minPort = ports[i-1] + 1
		}

		port, err := FindAvailablePort(minPort, 9399)
		if err != nil {
			t.Fatalf("failed to find port %d: %v", i, err)
		}
		ports[i] = port
	}

	// Verify all ports are different
	seen := make(map[int]bool)
	for i, port := range ports {
		if seen[port] {
			t.Errorf("duplicate port found: %d", port)
		}
		seen[port] = true
		t.Logf("port %d: %d", i, port)
	}
}

// TestPortFileCleanupOnShutdown tests that port files are cleaned up on daemon shutdown.
func TestPortFileCleanupOnShutdown(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	pidPath := filepath.Join(tmpDir, "test.pid")
	wsPortPath := filepath.Join(tmpDir, "var", "ws.port")

	// Create mock WebSocket server
	mockWS := &mockWSServer{port: 9456}

	server := NewServer(socketPath)
	lifecycle := NewLifecycle(server, pidPath, mockWS, wsPortPath)

	ctx := context.Background()

	// Run lifecycle in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- lifecycle.Run(ctx)
	}()

	// Give servers time to start
	time.Sleep(50 * time.Millisecond)

	// Verify port file was created
	port, err := ReadPortFile(wsPortPath)
	if err != nil {
		t.Fatalf("port file should exist after startup: %v", err)
	}
	if port != 9456 {
		t.Errorf("expected port 9456, got %d", port)
	}

	// Trigger shutdown
	lifecycle.Shutdown()

	// Wait for shutdown
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("lifecycle error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown timed out")
	}

	// Verify port file was removed
	if _, err := os.Stat(wsPortPath); !os.IsNotExist(err) {
		t.Error("port file should be removed after shutdown")
	}

	// Verify PID file was removed
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("PID file should be removed after shutdown")
	}
}
