package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	_ "modernc.org/sqlite"
)

// setupMergeTestRepo creates a test repo with a-sync branch and test events.
func setupMergeTestRepo(t *testing.T) string {
	t.Helper()

	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	// Create a-sync branch and worktree
	ctx := context.Background()
	bm := NewBranchManager(repoPath, false)
	if err := bm.CreateSyncBranch(ctx); err != nil {
		t.Fatalf("failed to create a-sync branch: %v", err)
	}

	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	if err := bm.CreateSyncWorktree(ctx, syncDir); err != nil {
		t.Fatalf("failed to create sync worktree: %v", err)
	}

	// Create initial JSONL files in worktree
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	if err := os.WriteFile(eventsPath, []byte{}, 0600); err != nil {
		t.Fatalf("failed to create events.jsonl: %v", err)
	}

	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(messagesDir, 0750); err != nil {
		t.Fatalf("failed to create messages dir: %v", err)
	}

	return repoPath
}

func TestNewMerger(t *testing.T) {
	repoPath := "/test/repo"
	m := NewMerger(repoPath, filepath.Join(repoPath, ".git", "thrum-sync", "a-sync"), false)

	if m == nil {
		t.Fatal("NewMerger returned nil")
	}

	if m.repoPath != repoPath {
		t.Errorf("repoPath = %q, want %q", m.repoPath, repoPath)
	}

	expectedSyncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	if m.syncDir != expectedSyncDir {
		t.Errorf("syncDir = %q, want %q", m.syncDir, expectedSyncDir)
	}
}

func TestMerger_Fetch_NoRemote(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	m := NewMerger(repoPath, filepath.Join(repoPath, ".git", "thrum-sync", "a-sync"), false)

	// Should succeed with no remote
	if err := m.Fetch(context.Background()); err != nil {
		t.Errorf("Fetch failed with no remote: %v", err)
	}
}

func TestMerger_Fetch_WithRemote(t *testing.T) {
	// Create a bare remote repository
	remoteDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create bare remote: %v", err)
	}

	// Create local repository
	repoPath := setupMergeTestRepo(t)

	// Add remote
	cmd = exec.Command("git", "remote", "add", "origin", remoteDir) //nolint:gosec // G204 test uses controlled paths
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to add remote: %v", err)
	}

	// Push a-sync to remote
	cmd = exec.Command("git", "push", "-u", "origin", SyncBranchName)
	cmd.Dir = repoPath
	_ = cmd.Run() // Best effort

	m := NewMerger(repoPath, filepath.Join(repoPath, ".git", "thrum-sync", "a-sync"), false)

	// Should succeed with remote
	if err := m.Fetch(context.Background()); err != nil {
		t.Errorf("Fetch failed with remote: %v", err)
	}
}

func TestMerger_Fetch_LocalOnly(t *testing.T) {
	// Create a repo with a remote configured
	remoteDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create bare remote: %v", err)
	}

	repoPath := setupMergeTestRepo(t)

	// Add remote
	cmd = exec.Command("git", "remote", "add", "origin", remoteDir) //nolint:gosec // G204 test uses controlled paths
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to add remote: %v", err)
	}

	// Push a-sync to remote
	cmd = exec.Command("git", "push", "-u", "origin", SyncBranchName)
	cmd.Dir = repoPath
	_ = cmd.Run()

	// Create merger with localOnly=true
	m := NewMerger(repoPath, filepath.Join(repoPath, ".git", "thrum-sync", "a-sync"), true)

	// Fetch should return nil immediately (skip) even though remote exists
	if err := m.Fetch(context.Background()); err != nil {
		t.Errorf("Fetch should succeed (no-op) in local-only mode: %v", err)
	}
}

func TestNewMerger_LocalOnly(t *testing.T) {
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync", true)
	if !m.localOnly {
		t.Error("expected localOnly=true")
	}
}

func TestExtractEventID(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		data      string
		wantID    string
		wantErr   bool
	}{
		{
			name:      "message.create",
			eventType: "message.create",
			data:      `{"type":"message.create","event_id":"evt_MSG123","message_id":"msg_123","timestamp":"2026-02-03T10:00:00Z"}`,
			wantID:    "evt_MSG123",
			wantErr:   false,
		},
		{
			name:      "message.edit",
			eventType: "message.edit",
			data:      `{"type":"message.edit","event_id":"evt_EDT456","message_id":"msg_456","timestamp":"2026-02-03T10:00:00Z"}`,
			wantID:    "evt_EDT456",
			wantErr:   false,
		},
		{
			name:      "agent.register",
			eventType: "agent.register",
			data:      `{"type":"agent.register","event_id":"evt_AGT789","agent_id":"agent:impl:abc123","timestamp":"2026-02-03T10:00:00Z"}`,
			wantID:    "evt_AGT789",
			wantErr:   false,
		},
		{
			name:      "agent.session.start",
			eventType: "agent.session.start",
			data:      `{"type":"agent.session.start","event_id":"evt_SES999","session_id":"ses_789","timestamp":"2026-02-03T10:00:00Z"}`,
			wantID:    "evt_SES999",
			wantErr:   false,
		},
		{
			name:      "unknown event type still works with event_id",
			eventType: "unknown.type",
			data:      `{"type":"unknown.type","event_id":"evt_UNK111","timestamp":"2026-02-03T10:00:00Z"}`,
			wantID:    "evt_UNK111",
			wantErr:   false,
		},
		{
			name:      "missing event_id field",
			eventType: "message.create",
			data:      `{"type":"message.create","message_id":"msg_123","timestamp":"2026-02-03T10:00:00Z"}`,
			wantID:    "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := extractEventID(json.RawMessage(tt.data), tt.eventType)

			if tt.wantErr {
				if err == nil {
					t.Error("extractEventID should have returned error")
				}
				return
			}

			if err != nil {
				t.Errorf("extractEventID failed: %v", err)
				return
			}

			if id != tt.wantID {
				t.Errorf("extractEventID = %q, want %q", id, tt.wantID)
			}
		})
	}
}

func TestMerger_MergeEvents_Deduplication(t *testing.T) {
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync", false)

	local := []*Event{
		{
			ID:        "msg_001",
			Type:      "message.create",
			Timestamp: "2026-02-03T10:00:00Z",
			Raw:       json.RawMessage(`{"type":"message.create","event_id":"evt_001","message_id":"msg_001"}`),
		},
		{
			ID:        "msg_002",
			Type:      "message.create",
			Timestamp: "2026-02-03T10:01:00Z",
			Raw:       json.RawMessage(`{"type":"message.create","event_id":"evt_002","message_id":"msg_002"}`),
		},
	}

	remote := []*Event{
		{
			ID:        "msg_001", // Duplicate
			Type:      "message.create",
			Timestamp: "2026-02-03T10:00:00Z",
			Raw:       json.RawMessage(`{"type":"message.create","event_id":"evt_001","message_id":"msg_001"}`),
		},
		{
			ID:        "msg_003", // New
			Type:      "message.create",
			Timestamp: "2026-02-03T10:02:00Z",
			Raw:       json.RawMessage(`{"type":"message.create","event_id":"evt_003","message_id":"msg_003"}`),
		},
	}

	merged, stats := m.mergeEvents(local, remote)

	if len(merged) != 3 {
		t.Errorf("merged length = %d, want 3", len(merged))
	}

	if stats.Duplicates != 1 {
		t.Errorf("Duplicates = %d, want 1", stats.Duplicates)
	}

	if stats.NewEvents != 1 {
		t.Errorf("NewEvents = %d, want 1", stats.NewEvents)
	}

	if len(stats.EventIDs) != 1 || stats.EventIDs[0] != "msg_003" {
		t.Errorf("EventIDs = %v, want [msg_003]", stats.EventIDs)
	}
}

func TestMerger_MergeEvents_Sorting(t *testing.T) {
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync", false)

	// Events in reverse chronological order
	local := []*Event{
		{
			ID:        "msg_003",
			Type:      "message.create",
			Timestamp: "2026-02-03T10:02:00Z",
			Raw:       json.RawMessage(`{"type":"message.create","event_id":"evt_003","message_id":"msg_003"}`),
		},
		{
			ID:        "msg_001",
			Type:      "message.create",
			Timestamp: "2026-02-03T10:00:00Z",
			Raw:       json.RawMessage(`{"type":"message.create","event_id":"evt_001","message_id":"msg_001"}`),
		},
	}

	remote := []*Event{
		{
			ID:        "msg_002",
			Type:      "message.create",
			Timestamp: "2026-02-03T10:01:00Z",
			Raw:       json.RawMessage(`{"type":"message.create","event_id":"evt_002","message_id":"msg_002"}`),
		},
	}

	merged, _ := m.mergeEvents(local, remote)

	// Note: mergeEvents doesn't sort, Merge() does the sorting
	// So we just check that all events are present
	if len(merged) != 3 {
		t.Errorf("merged length = %d, want 3", len(merged))
	}
}

func TestParseEvents(t *testing.T) {
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync", false)

	messages := []json.RawMessage{
		json.RawMessage(`{"type":"message.create","timestamp":"2026-02-03T10:00:00Z","event_id":"evt_001","message_id":"msg_001"}`),
		json.RawMessage(`{"type":"agent.register","timestamp":"2026-02-03T10:01:00Z","event_id":"evt_002","agent_id":"agent:test:ABC"}`),
		json.RawMessage(`{invalid json}`), // Should be skipped
		json.RawMessage(`{"type":"message.create","timestamp":"2026-02-03T10:02:00Z","event_id":"evt_003","message_id":"msg_002"}`),
	}

	events, err := m.parseEvents(messages)
	if err != nil {
		t.Fatalf("parseEvents failed: %v", err)
	}

	if len(events) != 3 {
		t.Errorf("parsed %d events, want 3 (skipping invalid)", len(events))
	}

	// Check first event
	if events[0].Type != "message.create" {
		t.Errorf("events[0].Type = %q, want message.create", events[0].Type)
	}

	if events[0].ID != "evt_001" {
		t.Errorf("events[0].ID = %q, want evt_001", events[0].ID)
	}
}

func TestContains(t *testing.T) {
	events := []*Event{
		{ID: "msg_001"},
		{ID: "msg_002"},
	}

	if !contains(events, "msg_001") {
		t.Error("contains returned false for existing ID msg_001")
	}

	if contains(events, "msg_999") {
		t.Error("contains returned true for non-existent ID msg_999")
	}
}

func TestMerger_ParseEvents_AllEventTypes(t *testing.T) {
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync", false)

	messages := []json.RawMessage{
		json.RawMessage(`{"type":"message.create","timestamp":"2026-02-03T10:00:00Z","event_id":"evt_MC1","message_id":"msg_001"}`),
		json.RawMessage(`{"type":"message.edit","timestamp":"2026-02-03T10:01:00Z","event_id":"evt_ME1","message_id":"msg_002"}`),
		json.RawMessage(`{"type":"message.delete","timestamp":"2026-02-03T10:02:00Z","event_id":"evt_MD1","message_id":"msg_003"}`),
		json.RawMessage(`{"type":"agent.update","timestamp":"2026-02-03T10:03:00Z","event_id":"evt_AU1","agent_id":"agent:test:123"}`),
		json.RawMessage(`{"type":"agent.register","timestamp":"2026-02-03T10:04:00Z","event_id":"evt_AR1","agent_id":"agent:test:123"}`),
		json.RawMessage(`{"type":"agent.session.start","timestamp":"2026-02-03T10:05:00Z","event_id":"evt_SS1","session_id":"ses_001"}`),
		json.RawMessage(`{"type":"agent.session.end","timestamp":"2026-02-03T10:06:00Z","event_id":"evt_SE1","session_id":"ses_002"}`),
	}

	events, err := m.parseEvents(messages)
	if err != nil {
		t.Fatalf("parseEvents failed: %v", err)
	}

	if len(events) != 7 {
		t.Errorf("parsed %d events, want 7", len(events))
	}

	// Verify all event types were parsed correctly
	expectedTypes := map[string]bool{
		"message.create":      true,
		"message.edit":        true,
		"message.delete":      true,
		"agent.update":        true,
		"agent.register":      true,
		"agent.session.start": true,
		"agent.session.end":   true,
	}

	for _, event := range events {
		if !expectedTypes[event.Type] {
			t.Errorf("unexpected event type: %s", event.Type)
		}
	}
}

// TestExtractEventID_WithEventIDField tests that extractEventID uses event_id field.
func TestExtractEventID_WithEventIDField(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		data      string
		wantID    string
		wantErr   bool
	}{
		{
			name:      "message.create with event_id",
			eventType: "message.create",
			data:      `{"type":"message.create","event_id":"evt_ABC123","message_id":"msg_123","timestamp":"2026-02-03T10:00:00Z"}`,
			wantID:    "evt_ABC123",
			wantErr:   false,
		},
		{
			name:      "message.edit with event_id",
			eventType: "message.edit",
			data:      `{"type":"message.edit","event_id":"evt_XYZ789","message_id":"msg_123","timestamp":"2026-02-03T10:00:00Z"}`,
			wantID:    "evt_XYZ789",
			wantErr:   false,
		},
		{
			name:      "message.delete with event_id",
			eventType: "message.delete",
			data:      `{"type":"message.delete","event_id":"evt_DEF456","message_id":"msg_123","timestamp":"2026-02-03T10:00:00Z"}`,
			wantID:    "evt_DEF456",
			wantErr:   false,
		},
		{
			name:      "missing event_id",
			eventType: "message.create",
			data:      `{"type":"message.create","message_id":"msg_123","timestamp":"2026-02-03T10:00:00Z"}`,
			wantID:    "",
			wantErr:   true,
		},
		{
			name:      "empty event_id",
			eventType: "message.create",
			data:      `{"type":"message.create","event_id":"","message_id":"msg_123","timestamp":"2026-02-03T10:00:00Z"}`,
			wantID:    "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := extractEventID(json.RawMessage(tt.data), tt.eventType)

			if tt.wantErr {
				if err == nil {
					t.Error("extractEventID should have returned error")
				}
				return
			}

			if err != nil {
				t.Errorf("extractEventID failed: %v", err)
				return
			}

			if id != tt.wantID {
				t.Errorf("extractEventID = %q, want %q", id, tt.wantID)
			}
		})
	}
}

// TestMerger_MergeEvents_MessageEditPreserved tests that message.create and message.edit
// with same message_id but different event_id are both preserved (the original bug fix).
func TestMerger_MergeEvents_MessageEditPreserved(t *testing.T) {
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync", false)

	// Local has message.create
	local := []*Event{
		{
			ID:        "evt_CREATE123",
			Type:      "message.create",
			Timestamp: "2026-02-03T10:00:00Z",
			Raw:       json.RawMessage(`{"type":"message.create","event_id":"evt_CREATE123","message_id":"msg_001","timestamp":"2026-02-03T10:00:00Z"}`),
		},
	}

	// Remote has message.edit for same message_id
	remote := []*Event{
		{
			ID:        "evt_EDIT456",
			Type:      "message.edit",
			Timestamp: "2026-02-03T10:01:00Z",
			Raw:       json.RawMessage(`{"type":"message.edit","event_id":"evt_EDIT456","message_id":"msg_001","timestamp":"2026-02-03T10:01:00Z"}`),
		},
	}

	merged, stats := m.mergeEvents(local, remote)

	// BOTH events should be preserved (not deduplicated)
	if len(merged) != 2 {
		t.Errorf("merged length = %d, want 2 (both create and edit should be preserved)", len(merged))
	}

	if stats.NewEvents != 1 {
		t.Errorf("NewEvents = %d, want 1", stats.NewEvents)
	}

	if stats.Duplicates != 0 {
		t.Errorf("Duplicates = %d, want 0 (different event_ids should not deduplicate)", stats.Duplicates)
	}

	// Verify both event IDs are present
	foundCreate := false
	foundEdit := false
	for _, event := range merged {
		if event.ID == "evt_CREATE123" {
			foundCreate = true
		}
		if event.ID == "evt_EDIT456" {
			foundEdit = true
		}
	}

	if !foundCreate {
		t.Error("message.create event (evt_CREATE123) was lost")
	}
	if !foundEdit {
		t.Error("message.edit event (evt_EDIT456) was lost")
	}
}

// TestMerger_MergeEvents_SameEventIDDeduped tests that events with same event_id
// are correctly deduplicated.
func TestMerger_MergeEvents_SameEventIDDeduped(t *testing.T) {
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync", false)

	// Both local and remote have same event
	local := []*Event{
		{
			ID:        "evt_ABC123",
			Type:      "message.create",
			Timestamp: "2026-02-03T10:00:00Z",
			Raw:       json.RawMessage(`{"type":"message.create","event_id":"evt_ABC123","message_id":"msg_001","timestamp":"2026-02-03T10:00:00Z"}`),
		},
	}

	remote := []*Event{
		{
			ID:        "evt_ABC123", // Same event_id
			Type:      "message.create",
			Timestamp: "2026-02-03T10:00:00Z",
			Raw:       json.RawMessage(`{"type":"message.create","event_id":"evt_ABC123","message_id":"msg_001","timestamp":"2026-02-03T10:00:00Z"}`),
		},
	}

	merged, stats := m.mergeEvents(local, remote)

	// Only one event should remain (deduplicated)
	if len(merged) != 1 {
		t.Errorf("merged length = %d, want 1 (duplicate should be removed)", len(merged))
	}

	if stats.Duplicates != 1 {
		t.Errorf("Duplicates = %d, want 1", stats.Duplicates)
	}

	if stats.NewEvents != 0 {
		t.Errorf("NewEvents = %d, want 0", stats.NewEvents)
	}

	if merged[0].ID != "evt_ABC123" {
		t.Errorf("merged event ID = %q, want %q", merged[0].ID, "evt_ABC123")
	}
}

// TestMerger_MergeAll tests the multi-file merge orchestration.
func TestMerger_MergeAll(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	m := NewMerger(repoPath, filepath.Join(repoPath, ".git", "thrum-sync", "a-sync"), false)

	// Create local files in the sync worktree (not .thrum/ root)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	messagesDir := filepath.Join(syncDir, "messages")

	// Write local events.jsonl
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	eventsData := `{"type":"agent.register","timestamp":"2026-02-06T10:00:00Z","event_id":"evt_LOCAL_AR1","agent_id":"agent:test:123"}
{"type":"agent.session.start","timestamp":"2026-02-06T10:01:00Z","event_id":"evt_LOCAL_SS1","session_id":"ses_001","agent_id":"agent:test:123"}
`
	if err := os.WriteFile(eventsPath, []byte(eventsData), 0600); err != nil {
		t.Fatalf("write events.jsonl: %v", err)
	}

	// Write local message file (agent:test:123.jsonl)
	localMsgPath := filepath.Join(messagesDir, "agent:test:123.jsonl")
	localMsgData := `{"type":"message.create","timestamp":"2026-02-06T10:02:00Z","event_id":"evt_LOCAL_MSG1","message_id":"msg_001","agent_id":"agent:test:123"}
`
	if err := os.WriteFile(localMsgPath, []byte(localMsgData), 0600); err != nil {
		t.Fatalf("write local message file: %v", err)
	}

	// Test MergeAll without remote (should succeed with no remote files)
	result, err := m.MergeAll(context.Background())
	if err != nil {
		t.Fatalf("MergeAll failed: %v", err)
	}

	// Should have local events but no new events from remote
	if result.NewEvents != 0 {
		t.Errorf("NewEvents = %d, want 0 (no remote files)", result.NewEvents)
	}

	// Verify files still exist and are readable
	if _, err := os.Stat(eventsPath); err != nil {
		t.Errorf("events.jsonl missing after MergeAll: %v", err)
	}
	if _, err := os.Stat(localMsgPath); err != nil {
		t.Errorf("local message file missing after MergeAll: %v", err)
	}
}

// TestMerger_ListLocalMessageFiles tests listing local message files.
func TestMerger_ListLocalMessageFiles(t *testing.T) {
	tmpDir := t.TempDir()
	messagesDir := filepath.Join(tmpDir, "messages")
	if err := os.MkdirAll(messagesDir, 0750); err != nil {
		t.Fatalf("create messages dir: %v", err)
	}

	// Create test files
	files := []string{"agent:test:123.jsonl", "agent:test:456.jsonl", "other.txt"}
	for _, filename := range files {
		path := filepath.Join(messagesDir, filename)
		if err := os.WriteFile(path, []byte{}, 0600); err != nil {
			t.Fatalf("write test file %s: %v", filename, err)
		}
	}

	m := NewMerger(tmpDir, tmpDir, false)

	result, err := m.listLocalMessageFiles(messagesDir)
	if err != nil {
		t.Fatalf("listLocalMessageFiles failed: %v", err)
	}

	// Should only list .jsonl files
	if len(result) != 2 {
		t.Errorf("found %d files, want 2 (.jsonl files only)", len(result))
	}

	if !result["agent:test:123.jsonl"] {
		t.Error("agent:test:123.jsonl not found")
	}
	if !result["agent:test:456.jsonl"] {
		t.Error("agent:test:456.jsonl not found")
	}
	if result["other.txt"] {
		t.Error("other.txt should not be listed (not a .jsonl file)")
	}
}

// TestMerger_MergeFile tests merging a single file.
func TestMerger_MergeFile(t *testing.T) {
	tmpDir := t.TempDir()
	m := NewMerger(tmpDir, tmpDir, false)

	// Create local file
	localPath := filepath.Join(tmpDir, "test.jsonl")
	localData := `{"type":"message.create","timestamp":"2026-02-06T10:00:00Z","event_id":"evt_LOCAL","message_id":"msg_001"}
`
	if err := os.WriteFile(localPath, []byte(localData), 0600); err != nil {
		t.Fatalf("write local file: %v", err)
	}

	// Merge with non-existent remote (should succeed)
	result, err := m.mergeFile(context.Background(), localPath, "nonexistent.jsonl")
	if err != nil {
		t.Fatalf("mergeFile failed: %v", err)
	}

	// Should have local events but no new events
	if result.LocalEvents != 1 {
		t.Errorf("LocalEvents = %d, want 1", result.LocalEvents)
	}
	if result.NewEvents != 0 {
		t.Errorf("NewEvents = %d, want 0", result.NewEvents)
	}
}

// openIngestTestDB creates an in-memory SQLite database with the events table
// schema needed for BootstrapIngestLegacyEvents tests.
func openIngestTestDB(t *testing.T) *safedb.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	_, err = raw.Exec(`CREATE TABLE events (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id      TEXT NOT NULL UNIQUE,
		sequence      INTEGER,
		type          TEXT,
		timestamp     TEXT,
		origin_daemon TEXT,
		event_json    TEXT
	)`)
	if err != nil {
		t.Fatalf("create events table: %v", err)
	}
	return safedb.New(raw)
}

// countEventsDB returns the number of rows in the events table.
func countEventsDB(t *testing.T, db *safedb.DB) int {
	t.Helper()
	var count int
	err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM events`).Scan(&count)
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	return count
}

// seedLegacyEventsFile writes N events to a legacy events.jsonl path.
func seedLegacyEventsFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create legacy events.jsonl: %v", err)
	}
	defer func() { _ = f.Close() }()
	for i := range n {
		line := fmt.Sprintf(`{"type":"agent.register","event_id":"evt_%04d","timestamp":"2026-01-01T00:00:%02dZ","origin_daemon":"peer1","sequence":%d}`, i, i%60, i)
		if _, err := fmt.Fprintln(f, line); err != nil {
			t.Fatalf("write line %d: %v", i, err)
		}
	}
}

// countJSONLLines returns the number of non-empty lines in a JSONL file.
func countJSONLLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	lines := 0
	for _, line := range splitLines(string(data)) {
		if len(line) > 0 {
			lines++
		}
	}
	return lines
}

func splitLines(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

// TestMergeAll_IncludesNewPaths verifies that MergeAll processes the v0.10.6
// paths (state/, messages-v2/, receipts/) alongside legacy paths without error.
func TestMergeAll_IncludesNewPaths(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	m := NewMerger(repoPath, syncDir, false)

	// Seed legacy paths.
	legacyEventsPath := filepath.Join(syncDir, "events.jsonl")
	if err := os.WriteFile(legacyEventsPath, []byte(`{"type":"agent.register","event_id":"evt_LEG1","timestamp":"2026-01-01T00:00:00Z"}`+"\n"), 0600); err != nil {
		t.Fatalf("write legacy events.jsonl: %v", err)
	}

	// Seed v0.10.6 paths.
	for _, dir := range []string{
		filepath.Join(syncDir, "state", "agents"),
		filepath.Join(syncDir, "state", "bridge-groups"),
		filepath.Join(syncDir, "messages-v2"),
		filepath.Join(syncDir, "receipts"),
	} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	// Write a state file.
	stateFile := filepath.Join(syncDir, "state", "agents", "agent_test_001.json")
	if err := os.WriteFile(stateFile, []byte(`{"agent_id":"agent:test:001"}`), 0600); err != nil {
		t.Fatalf("write state file: %v", err)
	}

	// Write a messages-v2 JSONL file.
	v2File := filepath.Join(syncDir, "messages-v2", "agent:test:001.jsonl")
	if err := os.WriteFile(v2File, []byte(`{"type":"message.create","event_id":"evt_V2_1","timestamp":"2026-01-01T00:00:00Z","message_id":"msg_001"}`+"\n"), 0600); err != nil {
		t.Fatalf("write messages-v2 file: %v", err)
	}

	// Write a receipts JSONL file.
	receiptFile := filepath.Join(syncDir, "receipts", "agent:test:001.jsonl")
	if err := os.WriteFile(receiptFile, []byte(`{"type":"message.read","event_id":"evt_REC_1","timestamp":"2026-01-01T00:00:00Z","message_id":"msg_001","agent_id":"agent:test:001"}`+"\n"), 0600); err != nil {
		t.Fatalf("write receipts file: %v", err)
	}

	// MergeAll must succeed without error.
	result, err := m.MergeAll(context.Background())
	if err != nil {
		t.Fatalf("MergeAll failed: %v", err)
	}

	// No remote files means no new events; local files preserved.
	if result == nil {
		t.Fatal("MergeAll returned nil result")
	}

	// Verify files still exist after merge.
	for _, path := range []string{legacyEventsPath, stateFile, v2File, receiptFile} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("file missing after MergeAll: %s", path)
		}
	}
}

// TestBootstrapIngestLegacyEvents_FirstRun verifies that on first run the
// legacy events are ingested into both the local journal and SQLite.
func TestBootstrapIngestLegacyEvents_FirstRun(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(tmpDir, "sync")

	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("mkdir thrumDir: %v", err)
	}
	if err := os.MkdirAll(syncDir, 0750); err != nil {
		t.Fatalf("mkdir syncDir: %v", err)
	}

	const legacyRows = 5
	seedLegacyEventsFile(t, filepath.Join(syncDir, "events.jsonl"), legacyRows)

	db := openIngestTestDB(t)

	rows, err := BootstrapIngestLegacyEvents(context.Background(), thrumDir, syncDir, db)
	if err != nil {
		t.Fatalf("BootstrapIngestLegacyEvents failed: %v", err)
	}
	if rows != legacyRows {
		t.Errorf("ingested rows = %d, want %d", rows, legacyRows)
	}

	// Local journal should have legacyRows lines.
	journalPath := filepath.Join(thrumDir, "events.jsonl")
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatalf("local events.jsonl missing after ingest: %v", err)
	}
	gotLines := countJSONLLines(t, journalPath)
	if gotLines != legacyRows {
		t.Errorf("local events.jsonl lines = %d, want %d", gotLines, legacyRows)
	}

	// SQLite events table should have legacyRows rows.
	gotDBRows := countEventsDB(t, db)
	if gotDBRows != legacyRows {
		t.Errorf("SQLite events rows = %d, want %d", gotDBRows, legacyRows)
	}

	// Sentinel file must exist.
	sentinelPath := filepath.Join(thrumDir, "legacy_ingested")
	if _, err := os.Stat(sentinelPath); err != nil {
		t.Errorf("sentinel file missing after ingest: %v", err)
	}

	// Legacy events.jsonl must NOT be deleted (spec §4.6).
	if _, err := os.Stat(filepath.Join(syncDir, "events.jsonl")); err != nil {
		t.Errorf("legacy events.jsonl was deleted — violates spec §4.6")
	}
}

// TestBootstrapIngestLegacyEvents_Idempotent verifies that a second call is
// a no-op and does not duplicate rows.
func TestBootstrapIngestLegacyEvents_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(tmpDir, "sync")

	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("mkdir thrumDir: %v", err)
	}
	if err := os.MkdirAll(syncDir, 0750); err != nil {
		t.Fatalf("mkdir syncDir: %v", err)
	}

	const legacyRows = 3
	seedLegacyEventsFile(t, filepath.Join(syncDir, "events.jsonl"), legacyRows)

	db := openIngestTestDB(t)

	// First run.
	rows1, err := BootstrapIngestLegacyEvents(context.Background(), thrumDir, syncDir, db)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if rows1 != legacyRows {
		t.Errorf("first call rows = %d, want %d", rows1, legacyRows)
	}

	// Second run must be a no-op.
	rows2, err := BootstrapIngestLegacyEvents(context.Background(), thrumDir, syncDir, db)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if rows2 != 0 {
		t.Errorf("second call rows = %d, want 0 (no-op)", rows2)
	}

	// SQLite row count must still equal legacyRows (no duplicates).
	gotDBRows := countEventsDB(t, db)
	if gotDBRows != legacyRows {
		t.Errorf("SQLite rows after second call = %d, want %d (no duplication)", gotDBRows, legacyRows)
	}
}

// TestBootstrapIngestLegacyEvents_NoLegacyFile verifies that when the legacy
// events.jsonl is absent, the function returns (0, nil) and still writes the
// sentinel so subsequent boots don't keep checking.
func TestBootstrapIngestLegacyEvents_NoLegacyFile(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(tmpDir, "sync")

	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("mkdir thrumDir: %v", err)
	}
	if err := os.MkdirAll(syncDir, 0750); err != nil {
		t.Fatalf("mkdir syncDir: %v", err)
	}
	// Do NOT create events.jsonl in syncDir.

	db := openIngestTestDB(t)

	rows, err := BootstrapIngestLegacyEvents(context.Background(), thrumDir, syncDir, db)
	if err != nil {
		t.Fatalf("BootstrapIngestLegacyEvents failed: %v", err)
	}
	if rows != 0 {
		t.Errorf("rows = %d, want 0 (no legacy file)", rows)
	}

	// Sentinel must be written to prevent repeated no-op checks.
	sentinelPath := filepath.Join(thrumDir, "legacy_ingested")
	if _, err := os.Stat(sentinelPath); err != nil {
		t.Errorf("sentinel file missing even when legacy file absent: %v", err)
	}
}

// TestReadLegacyMessageFallback_LegacyPresent verifies that when
// messages/<agent>.jsonl exists and messages-v2/<agent>.jsonl is absent,
// ReadLegacyMessageFallback returns the rows and the legacy path.
func TestReadLegacyMessageFallback_LegacyPresent(t *testing.T) {
	syncDir := t.TempDir()

	agentID := "agent:test:abc"

	// Create legacy messages/ directory and file.
	legacyDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(legacyDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacyPath := filepath.Join(legacyDir, agentID+".jsonl")
	legacyData := `{"type":"message.create","event_id":"evt_L1","timestamp":"2026-01-01T00:00:00Z","message_id":"msg_001"}` + "\n" +
		`{"type":"message.create","event_id":"evt_L2","timestamp":"2026-01-01T00:01:00Z","message_id":"msg_002"}` + "\n"
	if err := os.WriteFile(legacyPath, []byte(legacyData), 0600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}
	// Do NOT create messages-v2/ file.

	events, sourcePath, err := ReadLegacyMessageFallback(syncDir, agentID)
	if err != nil {
		t.Fatalf("ReadLegacyMessageFallback failed: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("events count = %d, want 2", len(events))
	}
	if sourcePath != legacyPath {
		t.Errorf("sourcePath = %q, want %q", sourcePath, legacyPath)
	}

	// Verify events are valid JSON.
	for i, ev := range events {
		var obj map[string]any
		if err := json.Unmarshal(ev, &obj); err != nil {
			t.Errorf("event[%d] is not valid JSON: %v", i, err)
		}
	}
}

// TestReadLegacyMessageFallback_LegacyAbsent verifies that when neither
// messages/ nor messages-v2/ file exists, the function returns (nil, "", nil).
func TestReadLegacyMessageFallback_LegacyAbsent(t *testing.T) {
	syncDir := t.TempDir()
	agentID := "agent:test:xyz"

	events, sourcePath, err := ReadLegacyMessageFallback(syncDir, agentID)
	if err != nil {
		t.Fatalf("ReadLegacyMessageFallback failed: %v", err)
	}
	if events != nil {
		t.Errorf("events = %v, want nil", events)
	}
	if sourcePath != "" {
		t.Errorf("sourcePath = %q, want empty", sourcePath)
	}
}

// TestReadLegacyMessageFallback_V2Present verifies that when messages-v2/
// file exists, ReadLegacyMessageFallback returns (nil, "", nil) — v2
// is preferred, so no fallback is needed.
func TestReadLegacyMessageFallback_V2Present(t *testing.T) {
	syncDir := t.TempDir()
	agentID := "agent:test:v2"

	// Create messages-v2/ file.
	v2Dir := filepath.Join(syncDir, "messages-v2")
	if err := os.MkdirAll(v2Dir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	v2Path := filepath.Join(v2Dir, agentID+".jsonl")
	if err := os.WriteFile(v2Path, []byte(`{"type":"message.create","event_id":"evt_V1","message_id":"msg_v2","timestamp":"2026-01-01T00:00:00Z"}`+"\n"), 0600); err != nil {
		t.Fatalf("write v2 file: %v", err)
	}

	// Also create legacy file to ensure v2 takes precedence.
	legacyDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(legacyDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, agentID+".jsonl"), []byte(`{"type":"message.create","event_id":"evt_OLD","message_id":"msg_old","timestamp":"2026-01-01T00:00:00Z"}`+"\n"), 0600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	events, sourcePath, err := ReadLegacyMessageFallback(syncDir, agentID)
	if err != nil {
		t.Fatalf("ReadLegacyMessageFallback failed: %v", err)
	}
	if events != nil {
		t.Errorf("events = %v, want nil (v2 present, no fallback needed)", events)
	}
	if sourcePath != "" {
		t.Errorf("sourcePath = %q, want empty (no fallback needed)", sourcePath)
	}
}
