//go:build resilience

package resilience

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/bootstrap"
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

func TestRecovery_DaemonRestart_WriteRPCSucceeds(t *testing.T) {
	// Build a fresh fixture with an absolute worktree path (the shared
	// thrum-fixture.tar.gz uses worktree="test" which reconcile skips).
	dirA := t.TempDir()
	thrumDir := filepath.Join(dirA, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(thrumDir, "var"), 0o750); err != nil {
		t.Fatal(err)
	}

	// Write an identity for agent_w in dirA worktree.
	idFile := config.IdentityFile{
		Version: 5, RepoID: "test-recovery",
		Agent:     config.AgentConfig{Kind: "agent", Name: "agent_w", Role: "tester"},
		Worktree:  dirA,
		UpdatedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(idFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(thrumDir, "identities", "agent_w.json"),
		data, 0o600); err != nil {
		t.Fatal(err)
	}

	// First daemon: simulate a registered agent that crashed before session.start,
	// or whose session_refs row was lost in the v0.10.0 restart regression.
	// Pre-register agent_w in the agents table directly (mimicking what a prior
	// run's JSONL projection would produce) but leave session_refs empty.
	st1, srv1, socketPath := startTestDaemon(t, thrumDir)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st1.DB().ExecContext(context.Background(),
		`INSERT INTO agents(agent_id, kind, role, module, registered_at)
		 VALUES (?, ?, ?, ?, ?)`,
		"agent_w", "agent", "tester", "test-recovery", now); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	srv1.Stop()
	st1.Close()
	os.Remove(socketPath)

	// Second daemon: open state, then run reconcile to mimic daemonRun.
	st2, _, socketPath2 := startTestDaemon(t, thrumDir)

	stats, err := bootstrap.Reconcile(context.Background(), bootstrap.Deps{
		State:        st2,
		ThrumDir:     thrumDir,
		Now:          time.Now,
		NewSessionID: func() string { return "ses_RECOVERY_" + time.Now().Format("150405.000") },
		TmuxAlive:    func(string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.RefsCreated != 1 {
		t.Fatalf("expected reconcile to insert 1 ref, got %+v", stats)
	}

	// Verify a write RPC (message.send) now resolves agent_w via the
	// session_refs row reconcile created. Self-send so the recipient is
	// guaranteed valid; the property under test is auth resolution past
	// peercred, not delivery semantics.
	var sendResult map[string]any
	rpcCall(t, socketPath2, "message.send", map[string]any{
		"caller_agent_id": "agent_w",
		"content":         "post-restart write",
		"format":          "markdown",
		"to":              "@agent_w",
	}, &sendResult)
	if _, ok := sendResult["message_id"]; !ok {
		t.Fatalf("message.send did not return message_id: %+v", sendResult)
	}
}
