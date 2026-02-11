package cli

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mockDaemon is a simple mock daemon for testing.
type mockDaemon struct {
	listener net.Listener
	stopChan chan struct{}
	ready    chan struct{}
}

func newMockDaemon(t *testing.T) (*mockDaemon, string) {
	t.Helper()

	// Create temp directory for socket
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	daemon := &mockDaemon{
		listener: listener,
		stopChan: make(chan struct{}),
		ready:    make(chan struct{}),
	}

	return daemon, socketPath
}

func (d *mockDaemon) start(t *testing.T, handler func(conn net.Conn)) {
	t.Helper()

	go func() {
		// Signal ready immediately since listener is already created and listening
		close(d.ready)
		for {
			select {
			case <-d.stopChan:
				return
			default:
				if ul, ok := d.listener.(*net.UnixListener); ok {
					_ = ul.SetDeadline(time.Now().Add(100 * time.Millisecond))
				}
				conn, err := d.listener.Accept()
				if err != nil {
					continue
				}
				go handler(conn)
			}
		}
	}()
}

// Ready returns a channel that will be closed when the daemon is ready to accept connections.
func (d *mockDaemon) Ready() <-chan struct{} {
	return d.ready
}

func (d *mockDaemon) stop() {
	close(d.stopChan)
	_ = d.listener.Close() // Best effort close
}

func TestNewClient(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		_ = conn.Close() // Best effort close
	})

	// Wait for daemon to be ready
	<-daemon.Ready()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	if client.socketPath != socketPath {
		t.Errorf("Expected socketPath %s, got %s", socketPath, client.socketPath)
	}
}

func TestNewClient_DaemonNotRunning(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "nonexistent.sock")

	_, err := NewClient(socketPath)
	if err == nil {
		t.Fatal("Expected error when daemon is not running, got nil")
	}
}

func TestClient_Call(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Mock handler that responds to "health" method
	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			t.Logf("Failed to decode request: %v", err)
			return
		}

		// Verify request structure
		if request["jsonrpc"] != "2.0" {
			t.Errorf("Expected jsonrpc 2.0, got %v", request["jsonrpc"])
		}

		if request["method"] != "health" {
			t.Errorf("Expected method 'health', got %v", request["method"])
		}

		// Send response
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"status": "ok",
			},
		}

		if err := encoder.Encode(response); err != nil {
			t.Logf("Failed to encode response: %v", err)
		}
	})

	// Wait for daemon to be ready
	<-daemon.Ready()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	var result map[string]any
	err = client.Call("health", nil, &result)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	if result["status"] != "ok" {
		t.Errorf("Expected status 'ok', got %v", result["status"])
	}
}

func TestClient_Call_Error(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Mock handler that returns an error
	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			t.Logf("Failed to decode request: %v", err)
			return
		}

		// Send error response
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"error": map[string]any{
				"code":    -32601,
				"message": "Method not found",
			},
		}

		if err := encoder.Encode(response); err != nil {
			t.Logf("Failed to encode response: %v", err)
		}
	})

	// Wait for daemon to be ready
	<-daemon.Ready()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	var result map[string]any
	err = client.Call("unknown_method", nil, &result)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	if err.Error() != "RPC error -32601: Method not found" {
		t.Errorf("Expected 'RPC error -32601: Method not found', got %v", err)
	}
}

func TestDefaultSocketPath(t *testing.T) {
	tests := []struct {
		name     string
		repoPath string
		want     string
	}{
		{
			name:     "current directory",
			repoPath: ".",
			want:     filepath.Join(".", ".thrum", "var", "thrum.sock"),
		},
		{
			name:     "absolute path",
			repoPath: "/home/user/repo",
			want:     filepath.Join("/home/user/repo", ".thrum", "var", "thrum.sock"),
		},
		{
			name:     "relative path",
			repoPath: "../project",
			want:     filepath.Join("..", "project", ".thrum", "var", "thrum.sock"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefaultSocketPath(tt.repoPath)
			if got != tt.want {
				t.Errorf("DefaultSocketPath(%q) = %q, want %q", tt.repoPath, got, tt.want)
			}
		})
	}
}

func TestClient_Close(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		_ = conn.Close() // Best effort close
	})

	// Wait for daemon to be ready
	<-daemon.Ready()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if err := client.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Closing again should not panic (may error though)
	_ = client.Close() // Best effort - may error on already closed connection
}

func TestClient_CallWithParams(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Mock handler that echoes params
	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			t.Logf("Failed to decode request: %v", err)
			return
		}

		// Echo params as result
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  request["params"],
		}

		if err := encoder.Encode(response); err != nil {
			t.Logf("Failed to encode response: %v", err)
		}
	})

	// Wait for daemon to be ready
	<-daemon.Ready()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{
		"message": "Hello, world!",
		"scope":   "module:test",
	}

	var result map[string]any
	err = client.Call("message.send", params, &result)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	if result["message"] != "Hello, world!" {
		t.Errorf("Expected message 'Hello, world!', got %v", result["message"])
	}

	if result["scope"] != "module:test" {
		t.Errorf("Expected scope 'module:test', got %v", result["scope"])
	}
}

// TestMain ensures cleanup.
func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}
