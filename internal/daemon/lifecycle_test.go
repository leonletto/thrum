package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLifecycleRun(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	pidPath := filepath.Join(tmpDir, "test.pid")

	server := NewServer(socketPath)
	lifecycle := NewLifecycle(server, pidPath, nil, "")

	ctx := context.Background()

	// Run lifecycle in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- lifecycle.Run(ctx)
	}()

	// Give server time to start
	time.Sleep(20 * time.Millisecond)

	// Register cleanup to ensure daemon stops even if test fails
	t.Cleanup(func() {
		lifecycle.Shutdown()
		select {
		case <-errCh:
			// Shutdown complete
		case <-time.After(3 * time.Second):
			// Force kill if needed
			pidInfo, err := ReadPIDFileJSON(pidPath)
			if err == nil && isProcessRunning(pidInfo.PID) {
				if proc, err := os.FindProcess(pidInfo.PID); err == nil {
					_ = proc.Kill()
				}
			}
		}
	})

	// Verify PID file was created
	if _, err := os.Stat(pidPath); os.IsNotExist(err) {
		t.Fatal("PID file was not created")
	}

	// Verify socket was created
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Fatal("socket file was not created")
	}

	// Trigger shutdown
	lifecycle.Shutdown()

	// Wait for shutdown to complete
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("lifecycle.Run() failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown timed out")
	}

	// Verify PID file was removed
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatal("PID file was not removed after shutdown")
	}

	// Verify socket was removed
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatal("socket file was not removed after shutdown")
	}
}

func TestLifecycleSignalHandling(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	pidPath := filepath.Join(tmpDir, "test.pid")

	server := NewServer(socketPath)
	lifecycle := NewLifecycle(server, pidPath, nil, "")

	ctx := context.Background()

	// Run lifecycle in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- lifecycle.Run(ctx)
	}()

	// Give server time to start
	time.Sleep(20 * time.Millisecond)

	// Register cleanup to ensure daemon stops even if test fails
	t.Cleanup(func() {
		lifecycle.Shutdown()
		select {
		case <-errCh:
			// Shutdown complete
		case <-time.After(3 * time.Second):
			// Force kill if needed
			pidInfo, err := ReadPIDFileJSON(pidPath)
			if err == nil && isProcessRunning(pidInfo.PID) {
				if proc, err := os.FindProcess(pidInfo.PID); err == nil {
					_ = proc.Kill()
				}
			}
		}
	})

	// Trigger shutdown directly instead of sending SIGTERM to entire test process
	// (sending SIGTERM to os.Getpid() would affect all tests, not just this daemon)
	lifecycle.Shutdown()

	// Wait for shutdown to complete
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("lifecycle.Run() failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown timed out")
	}

	// Verify cleanup happened
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatal("PID file was not removed after signal")
	}
}

func TestLifecycleShutdownWithTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "t.sock") // Shorter name
	pidPath := filepath.Join(tmpDir, "t.pid")

	server := NewServer(socketPath)
	lifecycle := NewLifecycle(server, pidPath, nil, "")

	ctx := context.Background()

	// Run lifecycle in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- lifecycle.Run(ctx)
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Shutdown with timeout
	if err := lifecycle.ShutdownWithTimeout(5 * time.Second); err != nil {
		t.Fatalf("shutdown with timeout failed: %v", err)
	}

	// Wait for Run to finish
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("lifecycle.Run() failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not finish after shutdown signal")
	}

	// Verify cleanup happened
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatal("PID file was not removed")
	}
}

func TestLifecycleInFlightRequests(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	pidPath := filepath.Join(tmpDir, "test.pid")

	server := NewServer(socketPath)

	// Register a slow handler
	slowHandlerDone := make(chan struct{})
	server.RegisterHandler("slow", func(ctx context.Context, params json.RawMessage) (any, error) {
		time.Sleep(100 * time.Millisecond)
		close(slowHandlerDone)
		return map[string]string{"status": "ok"}, nil
	})

	lifecycle := NewLifecycle(server, pidPath, nil, "")

	ctx := context.Background()

	// Run lifecycle in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- lifecycle.Run(ctx)
	}()

	// Give server time to start
	time.Sleep(20 * time.Millisecond)

	// Start a slow request in background that will be in-flight during shutdown
	requestErr := make(chan error, 1)
	go func() {
		conn, err := net.Dial("unix", socketPath)
		if err != nil {
			requestErr <- fmt.Errorf("failed to connect: %w", err)
			return
		}
		defer func() { _ = conn.Close() }()

		// Send slow request
		request := map[string]any{
			"jsonrpc": "2.0",
			"method":  "slow",
			"id":      1,
		}
		if err := json.NewEncoder(conn).Encode(request); err != nil {
			requestErr <- fmt.Errorf("failed to send request: %w", err)
			return
		}

		// Read response
		var response map[string]any
		if err := json.NewDecoder(conn).Decode(&response); err != nil {
			requestErr <- fmt.Errorf("failed to read response: %w", err)
			return
		}

		requestErr <- nil
	}()

	// Give request time to start processing
	time.Sleep(10 * time.Millisecond)

	// Trigger shutdown while request is in-flight
	lifecycle.Shutdown()

	// Wait for shutdown - should wait for in-flight request
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("lifecycle.Run() failed: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("shutdown timed out (should wait for in-flight requests)")
	}

	// Verify the slow handler completed
	select {
	case <-slowHandlerDone:
		// Good - handler completed
	case <-time.After(100 * time.Millisecond):
		t.Error("slow handler did not complete (shutdown may not have waited)")
	}

	// Verify request succeeded
	select {
	case err := <-requestErr:
		if err != nil {
			t.Errorf("in-flight request failed: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("request did not complete")
	}
}

func TestLifecycleDoubleShutdown(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	pidPath := filepath.Join(tmpDir, "test.pid")

	server := NewServer(socketPath)
	lifecycle := NewLifecycle(server, pidPath, nil, "")

	ctx := context.Background()

	// Run lifecycle in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- lifecycle.Run(ctx)
	}()

	// Give server time to start
	time.Sleep(20 * time.Millisecond)

	// Trigger shutdown twice
	lifecycle.Shutdown()
	lifecycle.Shutdown() // Should be a no-op

	// Wait for shutdown
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("lifecycle.Run() failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown timed out")
	}
}

func TestLifecyclePIDFileFailure(t *testing.T) {
	// Use a path where we can't write
	socketPath := "/tmp/test.sock"
	pidPath := "/nonexistent/directory/test.pid"

	server := NewServer(socketPath)
	lifecycle := NewLifecycle(server, pidPath, nil, "")

	ctx := context.Background()

	// Run should fail to write PID file
	err := lifecycle.Run(ctx)
	if err == nil {
		t.Fatal("expected error writing PID file to invalid path")
	}
}

// mockWSServer implements WebSocketServer for testing.
type mockWSServer struct {
	port       int
	startedVal atomic.Bool
	stoppedVal atomic.Bool
}

func (m *mockWSServer) Start(ctx context.Context) error {
	m.startedVal.Store(true)
	return nil
}

func (m *mockWSServer) Stop() error {
	m.stoppedVal.Store(true)
	return nil
}

func (m *mockWSServer) Port() int {
	return m.port
}

func TestLifecycleWithWebSocket(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	pidPath := filepath.Join(tmpDir, "test.pid")
	wsPortPath := filepath.Join(tmpDir, "var", "ws.port")

	server := NewServer(socketPath)
	wsServer := &mockWSServer{port: 9123}

	lifecycle := NewLifecycle(server, pidPath, wsServer, wsPortPath)

	ctx := context.Background()

	// Run lifecycle in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- lifecycle.Run(ctx)
	}()

	// Give servers time to start
	time.Sleep(50 * time.Millisecond)

	// Verify PID file was created
	if _, err := os.Stat(pidPath); os.IsNotExist(err) {
		t.Fatal("PID file was not created")
	}

	// Verify WebSocket server was started
	if !wsServer.startedVal.Load() {
		t.Fatal("WebSocket server was not started")
	}

	// Verify WebSocket port file was created with correct port
	port, err := ReadPortFile(wsPortPath)
	if err != nil {
		t.Fatalf("failed to read WebSocket port file: %v", err)
	}
	if port != 9123 {
		t.Fatalf("expected port 9123, got %d", port)
	}

	// Trigger shutdown
	lifecycle.Shutdown()

	// Wait for shutdown to complete
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("lifecycle.Run() failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown timed out")
	}

	// Verify WebSocket server was stopped
	if !wsServer.stoppedVal.Load() {
		t.Fatal("WebSocket server was not stopped")
	}

	// Verify WebSocket port file was removed
	if _, err := os.Stat(wsPortPath); !os.IsNotExist(err) {
		t.Fatal("WebSocket port file was not removed after shutdown")
	}

	// Verify PID file was removed
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatal("PID file was not removed after shutdown")
	}
}

// TestLifecycleDeferCleanup verifies that the defer in Run() cleans up files
// even when shutdown() is not called (early return after server start).
func TestLifecycleDeferCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	pidPath := filepath.Join(tmpDir, "test.pid")

	server := NewServer(socketPath)

	// Create a lifecycle that will fail after starting the server
	// We'll use a mock WebSocket server that returns an error
	wsServer := &mockWSServerWithError{
		port:       9123,
		startError: fmt.Errorf("mock WebSocket start error"),
	}
	wsPortPath := filepath.Join(tmpDir, "var", "ws.port")
	lifecycle := NewLifecycle(server, pidPath, wsServer, wsPortPath)

	ctx := context.Background()

	// Run should fail when starting WebSocket server
	err := lifecycle.Run(ctx)
	if err == nil {
		t.Fatal("expected error when WebSocket server fails to start")
	}

	// Verify PID file was cleaned up by defer (not by shutdown)
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("PID file was not removed by defer cleanup")
	}

	// Verify socket was cleaned up
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("socket file was not removed by defer cleanup")
	}
}

// mockWSServerWithError is a mock WebSocket server that can inject errors.
type mockWSServerWithError struct {
	port       int
	started    bool
	stopped    bool
	startError error
}

func (m *mockWSServerWithError) Start(ctx context.Context) error {
	if m.startError != nil {
		return m.startError
	}
	m.started = true
	return nil
}

func (m *mockWSServerWithError) Stop() error {
	m.stopped = true
	return nil
}

func (m *mockWSServerWithError) Port() int {
	return m.port
}

// TestStartTestDaemonHelper verifies that the StartTestDaemon helper
// correctly starts a daemon and cleans it up automatically.
func TestStartTestDaemonHelper(t *testing.T) {
	// Register a test handler to verify daemon functionality
	called := false
	handler := func(ctx context.Context, params json.RawMessage) (any, error) {
		called = true
		return map[string]string{"status": "ok"}, nil
	}

	cfg := &TestDaemonConfig{
		Handlers: map[string]Handler{
			"test.ping": handler,
		},
	}

	// Start daemon using helper - cleanup is automatic via t.Cleanup
	daemon := StartTestDaemon(t, cfg)

	// Verify daemon is ready
	select {
	case <-daemon.Ready():
		// Good - daemon is ready
	case <-time.After(1 * time.Second):
		t.Fatal("daemon ready signal not received")
	}

	// Verify we can connect and call a handler
	conn, err := net.Dial("unix", daemon.server.socketPath)
	if err != nil {
		t.Fatalf("failed to connect to daemon: %v", err)
	}
	defer func() { _ = conn.Close() }()

	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "test.ping",
		"id":      1,
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		t.Fatalf("failed to send request: %v", err)
	}

	var response map[string]any
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	if !called {
		t.Error("handler was not called")
	}

	// Trigger shutdown manually to verify cleanup
	daemon.Shutdown()

	// Additional cleanup will happen via t.Cleanup
}

// TestLifecycleDuplicateDaemonDetection verifies that pre-startup validation
// detects an already-running daemon for the same repo.
func TestLifecycleDuplicateDaemonDetection(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "t.pid")
	repoPath := "/test/repo"

	// Write a PID file for a running process (this process)
	pidInfo := PIDInfo{
		PID:      os.Getpid(),
		RepoPath: repoPath,
	}
	if err := WritePIDFileJSON(pidPath, pidInfo); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	// Try to start a daemon for the same repo
	socketPath := filepath.Join(tmpDir, "t.sock")
	server := NewServer(socketPath)
	lifecycle := NewLifecycle(server, pidPath, nil, "")
	lifecycle.SetRepoInfo(repoPath, socketPath)

	err := lifecycle.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when starting duplicate daemon for same repo")
	}

	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected 'already running' error, got: %v", err)
	}

	if !strings.Contains(err.Error(), repoPath) {
		t.Fatalf("expected error to mention repo path %s, got: %v", repoPath, err)
	}
}
