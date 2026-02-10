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

// StartTestDaemon creates and starts a lifecycle in a goroutine,
// registering t.Cleanup to force-kill on test exit.
// This prevents test orphan processes when tests timeout or panic.
func StartTestDaemon(t *testing.T, cfg *TestDaemonConfig) *Lifecycle {
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

	// Start lifecycle in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- l.Run(context.Background())
	}()

	// Wait for socket to appear (with timeout)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

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

	return l
}
