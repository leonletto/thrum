package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDaemonConfig holds options for StartTestDaemon.
type TestDaemonConfig struct {
	Handlers   map[string]Handler
	WSServer   WebSocketServer
	WSPortFile string
}

// TestDaemon wraps a Lifecycle with a ready channel for deterministic testing.
type TestDaemon struct {
	*Lifecycle
	ready chan struct{}
}

// Ready returns a channel that will be closed when the daemon is ready to accept connections.
func (td *TestDaemon) Ready() <-chan struct{} {
	return td.ready
}

// StartTestDaemon creates and starts a lifecycle in a goroutine,
// registering t.Cleanup to force-kill on test exit.
// This prevents test orphan processes when tests timeout or panic.
// Returns a TestDaemon with a Ready() channel for deterministic synchronization.
func StartTestDaemon(t *testing.T, cfg *TestDaemonConfig) *TestDaemon {
	t.Helper()

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	pidPath := filepath.Join(tmpDir, "test.pid")

	server := NewServer(socketPath)
	if cfg != nil && cfg.Handlers != nil {
		for name, handler := range cfg.Handlers {
			server.RegisterHandler(name, handler)
		}
	}

	var wsServer WebSocketServer
	var wsPortFile string
	if cfg != nil && cfg.WSServer != nil {
		wsServer = cfg.WSServer
		if cfg.WSPortFile != "" {
			wsPortFile = cfg.WSPortFile
		} else {
			wsPortFile = filepath.Join(tmpDir, "ws.port")
		}
	}

	l := NewLifecycle(server, pidPath, wsServer, wsPortFile)
	ready := make(chan struct{})

	// Start lifecycle in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- l.Run(context.Background())
	}()

	// Wait for socket to appear (with timeout) and signal ready
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(socketPath); err == nil {
				close(ready)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		// If we reach here, startup failed - ready channel will never close
		// and test will fail when it tries to use the daemon
	}()

	// Wait for ready signal before continuing
	<-ready

	// Verify socket exists (fail test if startup failed)
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Fatal("daemon failed to start: socket was not created")
	}

	// Register cleanup: shutdown gracefully, then force-kill if needed
	t.Cleanup(func() {
		// Trigger graceful shutdown with timeout
		if err := l.ShutdownWithTimeout(2 * time.Second); err != nil {
			t.Logf("WARNING: graceful shutdown failed: %v", err)
		}

		// Wait for Run() to finish or force-kill
		select {
		case <-errCh:
			// Clean exit
		case <-time.After(3 * time.Second):
			// Force kill via PID
			pidInfo, err := ReadPIDFileJSON(pidPath)
			if err == nil && isProcessRunning(pidInfo.PID) {
				if proc, err := os.FindProcess(pidInfo.PID); err == nil {
					_ = proc.Kill() // SIGKILL
				}
			}
			t.Log("WARNING: daemon required force-kill in cleanup")
		}
	})

	return &TestDaemon{
		Lifecycle: l,
		ready:     ready,
	}
}
