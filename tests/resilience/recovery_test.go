//go:build resilience

package resilience

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/projection"
	"github.com/leonletto/thrum/internal/schema"
)

func TestRecovery_FixtureRestore(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	// Verify health check works
	var result map[string]any
	rpcCall(t, socketPath, "health", nil, &result)

	if result["status"] != "ok" {
		t.Errorf("daemon not healthy after fixture restore: %v", result["status"])
	}
}

func TestRecovery_ProjectionConsistency(t *testing.T) {
	thrumDir := setupFixture(t)

	// Get counts from pre-built DB
	dbPath := filepath.Join(thrumDir, "var", "messages.db")
	origDB, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open original db: %v", err)
	}

	var origAgents, origMessages, origSessions, origGroups int
	origDB.QueryRow("SELECT COUNT(*) FROM agents").Scan(&origAgents)
	origDB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&origMessages)
	origDB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&origSessions)
	origDB.QueryRow("SELECT COUNT(*) FROM groups").Scan(&origGroups)
	origDB.Close()

	t.Logf("Original DB: agents=%d messages=%d sessions=%d groups=%d",
		origAgents, origMessages, origSessions, origGroups)

	// Delete the SQLite DB and rebuild from JSONL
	os.Remove(dbPath)
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	newDB, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open new db: %v", err)
	}
	defer newDB.Close()

	if err := schema.InitDB(newDB); err != nil {
		t.Fatalf("init new db: %v", err)
	}

	// Rebuild from JSONL (syncDir = thrumDir since events.jsonl and messages/ are there)
	projector := projection.NewProjector(safedb.New(newDB))
	if err := projector.Rebuild(context.Background(), thrumDir); err != nil {
		t.Fatalf("rebuild projection: %v", err)
	}

	// Compare counts — sessions are rebuilt from events, not JSONL directly,
	// so the projector may not recreate them all. Focus on agents and messages.
	var newAgents, newMessages int
	newDB.QueryRow("SELECT COUNT(*) FROM agents").Scan(&newAgents)
	newDB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&newMessages)

	t.Logf("Rebuilt DB: agents=%d messages=%d", newAgents, newMessages)

	if newAgents != origAgents {
		t.Errorf("agent count mismatch: orig=%d rebuilt=%d", origAgents, newAgents)
	}
	if newMessages != origMessages {
		t.Errorf("message count mismatch: orig=%d rebuilt=%d", origMessages, newMessages)
	}
}

func TestRecovery_DaemonRestart(t *testing.T) {
	thrumDir := setupFixture(t)

	// Start first daemon, send a message
	st1, srv1, socketPath := startTestDaemon(t, thrumDir)

	var sendResult map[string]any
	rpcCall(t, socketPath, "message.send", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"content":         "Pre-restart message",
		"format":          "markdown",
	}, &sendResult)

	msgID := sendResult["message_id"].(string)
	t.Logf("Sent message %s before restart", msgID)

	// Stop first daemon
	srv1.Stop()
	st1.Close()

	// Remove socket for clean restart
	os.Remove(socketPath)

	// Start second daemon on same data
	_, _, socketPath2 := startTestDaemon(t, thrumDir)

	// Verify the message persisted
	var inbox struct {
		Messages []map[string]any `json:"messages"`
	}
	rpcCall(t, socketPath2, "message.list", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"page_size":       5,
	}, &inbox)

	found := false
	for _, m := range inbox.Messages {
		if m["message_id"] == msgID {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("message %s not found after daemon restart", msgID)
	}
}

func TestRecovery_WALRecovery(t *testing.T) {
	thrumDir := setupFixture(t)

	dbPath := filepath.Join(thrumDir, "var", "messages.db")
	walPath := dbPath + "-wal"

	// Truncate WAL file if it exists
	if _, err := os.Stat(walPath); err == nil {
		if err := os.Truncate(walPath, 0); err != nil {
			t.Fatalf("truncate WAL: %v", err)
		}
	}

	// Daemon should start cleanly despite truncated WAL
	_, _, socketPath := startTestDaemon(t, thrumDir)

	var result map[string]any
	rpcCall(t, socketPath, "health", nil, &result)
	if result["status"] != "ok" {
		t.Errorf("daemon not healthy after WAL recovery: %v", result["status"])
	}
}

func TestRecovery_CorruptedMessageJSONL(t *testing.T) {
	thrumDir := setupFixture(t)

	// Append a malformed line to a message JSONL file
	messagesDir := filepath.Join(thrumDir, "messages")
	entries, err := os.ReadDir(messagesDir)
	if err != nil {
		t.Fatalf("read messages dir: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("no message JSONL files found")
	}

	firstFile := filepath.Join(messagesDir, entries[0].Name())
	f, err := os.OpenFile(firstFile, os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		t.Fatalf("open message file: %v", err)
	}
	f.WriteString("{this is not valid json\n")
	f.Close()

	// Delete DB and rebuild — projector should handle the corrupted line gracefully
	dbPath := filepath.Join(thrumDir, "var", "messages.db")
	os.Remove(dbPath)
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	newDB, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer newDB.Close()

	if err := schema.InitDB(newDB); err != nil {
		t.Fatalf("init db: %v", err)
	}

	projector := projection.NewProjector(safedb.New(newDB))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	err = projector.Rebuild(ctx, thrumDir)
	// The projector may return an error or skip the line — either is acceptable
	// as long as it doesn't panic
	if err != nil {
		t.Logf("Rebuild with corrupt data returned error (acceptable): %v", err)
	}

	// Verify some data was still projected
	var count int
	newDB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	t.Logf("Messages projected despite corruption: %d", count)
	if count == 0 {
		t.Error("expected some messages to be projected despite corruption")
	}
}
