//go:build resilience

package resilience

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/projection"
	"github.com/leonletto/thrum/internal/schema"
)

// TestCrash_KillDuringWrite simulates killing a daemon while a write is in-flight.
// The client should get an error within the timeout, not hang forever.
func TestCrash_KillDuringWrite(t *testing.T) {
	thrumDir := setupFixture(t)

	st, err := state.NewState(thrumDir, thrumDir, "test-crash-write")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	socketPath := shortSocketPath(t)
	server := daemon.NewServer(socketPath)
	registerAllHandlers(server, st)

	// Register a handler that blocks to simulate an in-flight write,
	// then we'll kill the server while this handler is running
	handlerStarted := make(chan struct{})
	server.RegisterHandler("test.blockingWrite", func(ctx context.Context, params json.RawMessage) (any, error) {
		close(handlerStarted)
		// Block until context done (server shutdown will cancel this)
		<-ctx.Done()
		return nil, ctx.Err()
	})

	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("server start: %v", err)
	}

	// Wait for socket ready
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Start a blocking write in background
	var clientErr error
	var clientDuration time.Duration
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		_, clientErr = rpcCallRaw(socketPath, "test.blockingWrite", nil)
		clientDuration = time.Since(start)
	}()

	// Wait for handler to be running
	<-handlerStarted

	// Kill the server abruptly (simulates crash)
	server.Stop()
	st.Close()

	// Client should get an error quickly after server death
	wg.Wait()

	if clientErr == nil {
		t.Error("expected client error after server crash, got nil")
	}
	t.Logf("Client got error after %v: %v", clientDuration, clientErr)

	if clientDuration > 15*time.Second {
		t.Errorf("client took %v to detect crash (expected <15s)", clientDuration)
	}
}

// TestCrash_DBIntegrityAfterAbruptShutdown verifies that the SQLite database
// is not corrupted after an abrupt daemon shutdown during active writes.
func TestCrash_DBIntegrityAfterAbruptShutdown(t *testing.T) {
	thrumDir := setupFixture(t)

	// Phase 1: Start daemon and send messages, then kill abruptly
	st, err := state.NewState(thrumDir, thrumDir, "test-crash-integrity")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	socketPath := shortSocketPath(t)
	server := daemon.NewServer(socketPath)
	registerAllHandlers(server, st)

	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("server start: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Ensure session and send some messages
	ensureSession(t, socketPath, "coordinator_0000")
	for i := range 10 {
		rpcCallRaw(socketPath, "message.send", map[string]any{
			"caller_agent_id": "coordinator_0000",
			"content":         fmt.Sprintf("Pre-crash message %d", i),
			"format":          "markdown",
		})
	}

	// Abrupt shutdown (no graceful close)
	server.Stop()
	st.Close()

	// Phase 2: Verify DB integrity after crash
	dbPath := filepath.Join(thrumDir, "var", "messages.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("reopen DB: %v", err)
	}
	defer db.Close()

	// Run integrity check
	var integrityResult string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrityResult); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if integrityResult != "ok" {
		t.Errorf("DB integrity check failed: %s", integrityResult)
	}
	t.Logf("DB integrity after crash: %s", integrityResult)

	// Verify message count is reasonable (fixture has ~10K + our 10)
	var msgCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&msgCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	t.Logf("Messages after crash: %d", msgCount)

	if msgCount < 10000 {
		t.Errorf("expected at least 10000 messages (fixture has ~10K), got %d", msgCount)
	}
}

// TestCrash_RestartAfterCrash verifies that a daemon can restart cleanly
// after an abrupt shutdown and serve requests normally.
func TestCrash_RestartAfterCrash(t *testing.T) {
	thrumDir := setupFixture(t)

	// Phase 1: Start daemon, send data, crash
	st1, err := state.NewState(thrumDir, thrumDir, "test-crash-restart-1")
	if err != nil {
		t.Fatalf("NewState 1: %v", err)
	}

	socketPath := shortSocketPath(t)
	server1 := daemon.NewServer(socketPath)
	registerAllHandlers(server1, st1)

	if err := server1.Start(context.Background()); err != nil {
		t.Fatalf("server 1 start: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Send a unique message before crash
	ensureSession(t, socketPath, "coordinator_0000")
	var sendResult map[string]any
	rpcCall(t, socketPath, "message.send", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"content":         "Message before crash",
		"format":          "markdown",
	}, &sendResult)
	preCrashMsgID := sendResult["message_id"].(string)
	t.Logf("Pre-crash message ID: %s", preCrashMsgID)

	// Crash
	server1.Stop()
	st1.Close()

	// Phase 2: Restart daemon on same data
	os.Remove(socketPath)

	st2, err := state.NewState(thrumDir, thrumDir, "test-crash-restart-2")
	if err != nil {
		t.Fatalf("NewState 2: %v", err)
	}
	t.Cleanup(func() { st2.Close() })

	server2 := daemon.NewServer(socketPath)
	registerAllHandlers(server2, st2)

	if err := server2.Start(context.Background()); err != nil {
		t.Fatalf("server 2 start: %v", err)
	}
	t.Cleanup(func() { server2.Stop() })

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Verify health
	var health map[string]any
	rpcCall(t, socketPath, "health", nil, &health)
	if health["status"] != "ok" {
		t.Errorf("daemon not healthy after restart: %v", health["status"])
	}

	// Verify pre-crash message persisted
	var inbox struct {
		Messages []map[string]any `json:"messages"`
	}
	rpcCall(t, socketPath, "message.list", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"page_size":       20,
	}, &inbox)

	found := false
	for _, m := range inbox.Messages {
		if m["message_id"] == preCrashMsgID {
			found = true
			break
		}
	}
	if !found {
		t.Error("pre-crash message not found after restart")
	}

	// Verify we can send new messages after restart
	var newSend map[string]any
	rpcCall(t, socketPath, "message.send", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"content":         "Message after restart",
		"format":          "markdown",
	}, &newSend)
	if newSend["message_id"] == "" {
		t.Error("failed to send message after restart")
	}
	t.Logf("Post-restart message ID: %s", newSend["message_id"])
}

// TestCrash_ProjectionRebuildAfterCrash verifies that projection rebuild
// works correctly after an abrupt shutdown.
func TestCrash_ProjectionRebuildAfterCrash(t *testing.T) {
	thrumDir := setupFixture(t)

	// Phase 1: Get baseline counts
	dbPath := filepath.Join(thrumDir, "var", "messages.db")
	origDB, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open original db: %v", err)
	}
	var origMessages int
	origDB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&origMessages)
	origDB.Close()

	// Phase 2: Start daemon, send more messages, crash
	st, err := state.NewState(thrumDir, thrumDir, "test-crash-rebuild")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	socketPath := shortSocketPath(t)
	server := daemon.NewServer(socketPath)
	registerAllHandlers(server, st)

	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("server start: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	ensureSession(t, socketPath, "coordinator_0000")
	for i := range 5 {
		rpcCallRaw(socketPath, "message.send", map[string]any{
			"caller_agent_id": "coordinator_0000",
			"content":         fmt.Sprintf("Rebuild test %d", i),
			"format":          "markdown",
		})
	}

	// Crash
	server.Stop()
	st.Close()

	// Phase 3: Delete DB and rebuild from JSONL
	os.Remove(dbPath)
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	newDB, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open new db: %v", err)
	}
	defer newDB.Close()

	if err := schema.InitDB(newDB); err != nil {
		t.Fatalf("init db: %v", err)
	}

	projector := projection.NewProjector(safedb.New(newDB))
	if err := projector.Rebuild(context.Background(), thrumDir); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	var rebuiltMessages int
	newDB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&rebuiltMessages)
	t.Logf("Original: %d, After crash+rebuild: %d", origMessages, rebuiltMessages)

	// Rebuilt count should be >= original (we added 5 more messages)
	if rebuiltMessages < origMessages {
		t.Errorf("rebuilt messages (%d) < original (%d) — data loss after crash", rebuiltMessages, origMessages)
	}
}
