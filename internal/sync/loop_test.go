package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/jsonl"
	"github.com/leonletto/thrum/internal/projection"
	_ "modernc.org/sqlite"
)

func TestSyncLoop_StartStop(t *testing.T) {
	tmpDir := setupTestRepoWithCommit(t)
	setupThrumFiles(t, tmpDir)

	syncer := NewSyncer(tmpDir, filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync"), false)
	projector := setupTestProjector(t, tmpDir)

	loop := NewSyncLoop(syncer, projector, tmpDir, filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync"), filepath.Join(tmpDir, ".thrum"), 100*time.Millisecond, false)

	ctx := context.Background()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Let loop run through a few cycles (intentional - testing start/stop/restart behavior)
	time.Sleep(250 * time.Millisecond)

	if err := loop.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Verify we can restart
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Restart failed: %v", err)
	}

	if err := loop.Stop(); err != nil {
		t.Fatalf("Stop after restart failed: %v", err)
	}
}

func TestSyncLoop_ManualTrigger(t *testing.T) {
	tmpDir := setupMergeTestRepo(t)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	syncer := NewSyncer(tmpDir, syncDir, false)
	projector := setupTestProjector(t, tmpDir)

	// Use a long interval so manual trigger is the only way it runs
	loop := NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), 10*time.Second, false)

	ctx := context.Background()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	// Trigger manual sync
	loop.TriggerSync()

	// Poll until sync completes (with timeout)
	deadline := time.After(2 * time.Second)
	for {
		status := loop.GetStatus()
		if !status.LastSyncAt.IsZero() {
			if status.LastError != "" {
				t.Errorf("Expected no error, got: %s", status.LastError)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("Expected lastSyncAt to be set")
		default:
			// Poll interval - waiting for async sync operation to complete
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestSyncLoop_Status(t *testing.T) {
	tmpDir := setupMergeTestRepo(t)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	syncer := NewSyncer(tmpDir, syncDir, false)
	projector := setupTestProjector(t, tmpDir)

	loop := NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), 1*time.Second, false)

	// Check status before start
	status := loop.GetStatus()
	if status.Running {
		t.Error("Expected Running=false before start")
	}

	ctx := context.Background()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	// Poll until initial sync completes (with timeout)
	deadline := time.After(2 * time.Second)
	for {
		status = loop.GetStatus()
		if !status.LastSyncAt.IsZero() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Expected lastSyncAt to be set after initial sync")
		default:
			// Poll interval - waiting for async sync operation to complete
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Check status after sync
	if !status.Running {
		t.Error("Expected Running=true after start")
	}
}

func TestSyncLoop_NotifyChannel(t *testing.T) {
	tmpDir := setupTestRepoWithCommit(t)
	setupThrumFiles(t, tmpDir)

	syncer := NewSyncer(tmpDir, filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync"), false)
	projector := setupTestProjector(t, tmpDir)

	loop := NewSyncLoop(syncer, projector, tmpDir, filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync"), filepath.Join(tmpDir, ".thrum"), 100*time.Millisecond, false)

	// Get notify channel before starting
	notifyCh := loop.NotifyChannel()
	if notifyCh == nil {
		t.Fatal("Expected notify channel to be non-nil")
	}

	// Channel should be buffered
	select {
	case <-notifyCh:
		t.Error("Expected channel to be empty initially")
	default:
		// Good - channel is empty
	}
}

func TestSyncLoop_DefaultInterval(t *testing.T) {
	tmpDir := setupTestRepoWithCommit(t)
	setupThrumFiles(t, tmpDir)

	syncer := NewSyncer(tmpDir, filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync"), false)
	projector := setupTestProjector(t, tmpDir)

	// Pass 0 interval to get default
	loop := NewSyncLoop(syncer, projector, tmpDir, filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync"), filepath.Join(tmpDir, ".thrum"), 0, false)

	if loop.interval != 60*time.Second {
		t.Errorf("Expected default interval of 60s, got %v", loop.interval)
	}
}

func TestLockAcquireRelease(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Acquire lock
	lock1, err := acquireLock(lockPath)
	if err != nil {
		t.Fatalf("Failed to acquire lock: %v", err)
	}

	// Try to acquire again (should fail)
	lock2, err := acquireLock(lockPath)
	if err == nil {
		if err := releaseLock(lock2); err != nil {
			t.Logf("release lock: %v", err)
		}
		t.Fatal("Expected lock acquisition to fail when already held")
	}
	// The error should indicate the lock is held (wrapped error)
	if err.Error() != "acquire lock: lock is held by another process" {
		t.Errorf("Expected lock error, got: %v", err)
	}

	// Release lock
	if err := releaseLock(lock1); err != nil {
		t.Fatalf("Failed to release lock: %v", err)
	}

	// Should be able to acquire again
	lock3, err := acquireLock(lockPath)
	if err != nil {
		t.Fatalf("Failed to re-acquire lock after release: %v", err)
	}
	if err := releaseLock(lock3); err != nil {
		t.Logf("release lock: %v", err)
	}
}

// setupTestProjector creates a test projector with a file-based database.
func setupTestProjector(t *testing.T, repoPath string) *projection.Projector {
	t.Helper()

	// Ensure .thrum/var directory exists
	varDir := filepath.Join(repoPath, ".thrum", "var")
	if err := os.MkdirAll(varDir, 0750); err != nil {
		t.Fatalf("Failed to create var directory: %v", err)
	}

	// Use a file-based database in .thrum/var for testing
	dbPath := filepath.Join(varDir, "messages.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Initialize schema
	if err := initTestSchema(db); err != nil {
		t.Fatalf("Failed to initialize schema: %v", err)
	}

	return projection.NewProjector(db)
}

// initTestSchema initializes the database schema for testing.
func initTestSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS agents (
		agent_id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		role TEXT NOT NULL,
		module TEXT NOT NULL,
		display TEXT,
		registered_at TEXT NOT NULL,
		last_seen_at TEXT
	);

	CREATE TABLE IF NOT EXISTS sessions (
		session_id TEXT PRIMARY KEY,
		agent_id TEXT NOT NULL,
		started_at TEXT NOT NULL,
		ended_at TEXT,
		end_reason TEXT,
		last_seen_at TEXT NOT NULL,
		FOREIGN KEY (agent_id) REFERENCES agents(agent_id)
	);

	CREATE TABLE IF NOT EXISTS threads (
		thread_id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		created_at TEXT NOT NULL,
		created_by TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS messages (
		message_id TEXT PRIMARY KEY,
		thread_id TEXT,
		agent_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT,
		deleted INTEGER DEFAULT 0,
		deleted_at TEXT,
		delete_reason TEXT,
		body_format TEXT NOT NULL,
		body_content TEXT NOT NULL,
		body_structured TEXT,
		authored_by TEXT,
		disclosed INTEGER DEFAULT 0,
		FOREIGN KEY (thread_id) REFERENCES threads(thread_id),
		FOREIGN KEY (agent_id) REFERENCES agents(agent_id),
		FOREIGN KEY (session_id) REFERENCES sessions(session_id)
	);

	CREATE TABLE IF NOT EXISTS message_scopes (
		message_id TEXT NOT NULL,
		scope_type TEXT NOT NULL,
		scope_value TEXT NOT NULL,
		FOREIGN KEY (message_id) REFERENCES messages(message_id)
	);

	CREATE TABLE IF NOT EXISTS message_refs (
		message_id TEXT NOT NULL,
		ref_type TEXT NOT NULL,
		ref_value TEXT NOT NULL,
		FOREIGN KEY (message_id) REFERENCES messages(message_id)
	);
	`

	_, err := db.Exec(schema)
	return err
}

func TestSyncLoop_IsLocalOnly(t *testing.T) {
	tmpDir := setupTestRepoWithCommit(t)
	setupThrumFiles(t, tmpDir)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	t.Run("false", func(t *testing.T) {
		syncer := NewSyncer(tmpDir, syncDir, false)
		projector := setupTestProjector(t, tmpDir)
		loop := NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), 1*time.Second, false)
		if loop.IsLocalOnly() {
			t.Error("expected IsLocalOnly()=false")
		}
	})

	t.Run("true", func(t *testing.T) {
		syncer := NewSyncer(tmpDir, syncDir, true)
		projector := setupTestProjector(t, tmpDir)
		loop := NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), 1*time.Second, true)
		if !loop.IsLocalOnly() {
			t.Error("expected IsLocalOnly()=true")
		}
	})
}

func TestSyncLoop_LocalOnly_StatusReportsMode(t *testing.T) {
	tmpDir := setupMergeTestRepo(t)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	syncer := NewSyncer(tmpDir, syncDir, true)
	projector := setupTestProjector(t, tmpDir)

	loop := NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), 1*time.Second, true)

	// Check status shows local-only before start
	status := loop.GetStatus()
	if !status.LocalOnly {
		t.Error("expected LocalOnly=true in status")
	}

	ctx := context.Background()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	// Poll until initial sync completes
	deadline := time.After(2 * time.Second)
	for {
		status = loop.GetStatus()
		if !status.LastSyncAt.IsZero() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Expected lastSyncAt to be set after initial sync")
		default:
			// Poll interval - waiting for async sync operation to complete
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Verify status still shows local-only after sync
	if !status.LocalOnly {
		t.Error("expected LocalOnly=true in status after sync")
	}
	if !status.Running {
		t.Error("expected Running=true after start")
	}
	if status.LastError != "" {
		t.Errorf("expected no error, got: %s", status.LastError)
	}
}

func TestSyncLoop_LocalOnly_FullCycle(t *testing.T) {
	tmpDir := setupMergeTestRepo(t)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	syncer := NewSyncer(tmpDir, syncDir, true)
	projector := setupTestProjector(t, tmpDir)

	// Use local-only mode with a long interval, rely on manual trigger
	loop := NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), 10*time.Second, true)

	ctx := context.Background()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	// Wait for initial sync to complete
	deadline := time.After(2 * time.Second)
	for {
		status := loop.GetStatus()
		if !status.LastSyncAt.IsZero() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("initial sync did not complete")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Write a local event to the sync worktree
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // G304 - test path
	if err != nil {
		t.Fatalf("failed to open events.jsonl: %v", err)
	}
	_, _ = f.WriteString(`{"type":"agent.register","timestamp":"2026-02-10T10:00:00Z","event_id":"evt_LOC1","agent_id":"agent:test:local","kind":"test","role":"tester","module":"test-mod","v":1}` + "\n")
	_ = f.Close()

	// Record sync time before trigger
	beforeTrigger := loop.GetStatus().LastSyncAt

	// Trigger manual sync
	loop.TriggerSync()

	// Wait for sync to run again
	deadline = time.After(2 * time.Second)
	for {
		status := loop.GetStatus()
		if status.LastSyncAt.After(beforeTrigger) {
			if status.LastError != "" {
				t.Errorf("sync error: %s", status.LastError)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("manual sync did not complete")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestLoop_SetError(t *testing.T) {
	tmpDir := setupTestRepoWithCommit(t)
	setupThrumFiles(t, tmpDir)

	syncer := NewSyncer(tmpDir, filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync"), false)
	projector := setupTestProjector(t, tmpDir)
	loop := NewSyncLoop(syncer, projector, tmpDir, filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync"), filepath.Join(tmpDir, ".thrum"), 1*time.Second, false)

	// Set an error
	testErr := fmt.Errorf("test error")
	loop.setError(testErr)

	// Check that error was recorded
	status := loop.GetStatus()
	if status.LastError != "test error" {
		t.Errorf("Expected error to be recorded, got: %s", status.LastError)
	}
}

func TestExtractEventIDFromRaw(t *testing.T) {
	tests := []struct {
		name        string
		event       json.RawMessage
		expectedID  string
		expectError bool
	}{
		{
			name:       "message.create event",
			event:      json.RawMessage(`{"type":"message.create","event_id":"evt_MSG123","message_id":"msg-123","timestamp":"2024-01-01T00:00:00Z"}`),
			expectedID: "evt_MSG123",
		},
		{
			name:       "thread.create event",
			event:      json.RawMessage(`{"type":"thread.create","event_id":"evt_THR456","thread_id":"thread-456","timestamp":"2024-01-01T00:00:00Z"}`),
			expectedID: "evt_THR456",
		},
		{
			name:        "invalid JSON",
			event:       json.RawMessage(`{invalid`),
			expectError: true,
		},
		{
			name:        "missing event_id",
			event:       json.RawMessage(`{"type":"message.create","message_id":"msg-123","timestamp":"2024-01-01T00:00:00Z"}`),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := extractEventIDFromRaw(tt.event)
			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if id != tt.expectedID {
					t.Errorf("Expected ID %q, got %q", tt.expectedID, id)
				}
			}
		})
	}
}

func TestUpdateProjection(t *testing.T) {
	tmpDir := setupTestRepoWithCommit(t)
	setupThrumFiles(t, tmpDir)

	syncer := NewSyncer(tmpDir, filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync"), false)
	projector := setupTestProjector(t, tmpDir)
	loop := NewSyncLoop(syncer, projector, tmpDir, filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync"), filepath.Join(tmpDir, ".thrum"), 1*time.Second, false)

	// Write some test events to JSONL
	jsonlPath := filepath.Join(tmpDir, ".thrum", "messages.jsonl")
	writer, err := jsonl.NewWriter(jsonlPath)
	if err != nil {
		t.Fatalf("Failed to create JSONL writer: %v", err)
	}

	// First register agent and start session (required for message foreign keys)
	agentEvent := map[string]any{
		"type":      "agent.register",
		"event_id":  "evt_AGT001",
		"v":         1,
		"agent_id":  "agent-1",
		"timestamp": "2024-01-01T00:00:00Z",
		"kind":      "test",
		"role":      "tester",
		"module":    "test-module",
	}
	if err := writer.Append(agentEvent); err != nil {
		t.Fatalf("Failed to write agent event: %v", err)
	}

	sessionEvent := map[string]any{
		"type":       "agent.session.start",
		"event_id":   "evt_SES001",
		"v":          1,
		"session_id": "session-1",
		"agent_id":   "agent-1",
		"timestamp":  "2024-01-01T00:00:00Z",
	}
	if err := writer.Append(sessionEvent); err != nil {
		t.Fatalf("Failed to write session event: %v", err)
	}

	messageEvent := map[string]any{
		"type":       "message.create",
		"event_id":   "evt_MSG001",
		"v":          1,
		"message_id": "msg-test-1",
		"agent_id":   "agent-1",
		"session_id": "session-1",
		"timestamp":  "2024-01-01T00:00:00Z",
		"body": map[string]any{
			"format":  "text",
			"content": "Test message",
		},
		"scopes":      []any{},
		"refs":        []any{},
		"authored_by": "",
		"disclosed":   false,
	}
	if err := writer.Append(messageEvent); err != nil {
		t.Fatalf("Failed to write message event: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Prepare parsed events for projection (Phase 5: pass parsed events, not IDs)
	parsedEvents := []json.RawMessage{}
	for _, event := range []map[string]any{agentEvent, sessionEvent, messageEvent} {
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("Failed to marshal event: %v", err)
		}
		parsedEvents = append(parsedEvents, json.RawMessage(data))
	}

	// Update projection with all events
	if err := loop.updateProjection(parsedEvents); err != nil {
		t.Fatalf("updateProjection failed: %v", err)
	}

	// Verify event was applied to database
	db, err := sql.Open("sqlite", filepath.Join(tmpDir, ".thrum", "var", "messages.db"))
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM messages WHERE message_id = ?", "msg-test-1").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query database: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 message in database, got %d", count)
	}
}
