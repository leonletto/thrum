package sync

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupMergeTestRepo creates a test repo with a-sync branch and test events.
func setupMergeTestRepo(t *testing.T) string {
	t.Helper()

	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	// Create a-sync branch and worktree
	bm := NewBranchManager(repoPath)
	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("failed to create a-sync branch: %v", err)
	}

	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	if err := bm.CreateSyncWorktree(syncDir); err != nil {
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
	m := NewMerger(repoPath, filepath.Join(repoPath, ".git", "thrum-sync", "a-sync"))

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
	m := NewMerger(repoPath, filepath.Join(repoPath, ".git", "thrum-sync", "a-sync"))

	// Should succeed with no remote
	if err := m.Fetch(); err != nil {
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

	m := NewMerger(repoPath, filepath.Join(repoPath, ".git", "thrum-sync", "a-sync"))

	// Should succeed with remote
	if err := m.Fetch(); err != nil {
		t.Errorf("Fetch failed with remote: %v", err)
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
			name:      "thread.create",
			eventType: "thread.create",
			data:      `{"type":"thread.create","event_id":"evt_THR456","thread_id":"thr_456","timestamp":"2026-02-03T10:00:00Z"}`,
			wantID:    "evt_THR456",
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
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync")

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
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync")

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
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync")

	messages := []json.RawMessage{
		json.RawMessage(`{"type":"message.create","timestamp":"2026-02-03T10:00:00Z","event_id":"evt_001","message_id":"msg_001"}`),
		json.RawMessage(`{"type":"thread.create","timestamp":"2026-02-03T10:01:00Z","event_id":"evt_002","thread_id":"thr_001"}`),
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
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync")

	messages := []json.RawMessage{
		json.RawMessage(`{"type":"message.create","timestamp":"2026-02-03T10:00:00Z","event_id":"evt_MC1","message_id":"msg_001"}`),
		json.RawMessage(`{"type":"message.edit","timestamp":"2026-02-03T10:01:00Z","event_id":"evt_ME1","message_id":"msg_002"}`),
		json.RawMessage(`{"type":"message.delete","timestamp":"2026-02-03T10:02:00Z","event_id":"evt_MD1","message_id":"msg_003"}`),
		json.RawMessage(`{"type":"thread.create","timestamp":"2026-02-03T10:03:00Z","event_id":"evt_TC1","thread_id":"thr_001"}`),
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
		"thread.create":       true,
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
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync")

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
	m := NewMerger("/test/repo", "/test/repo/.git/thrum-sync/a-sync")

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
	m := NewMerger(repoPath, filepath.Join(repoPath, ".git", "thrum-sync", "a-sync"))

	// Create local files in the sync worktree (not .thrum/ root)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	messagesDir := filepath.Join(syncDir, "messages")

	// Write local events.jsonl
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	eventsData := `{"type":"agent.register","timestamp":"2026-02-06T10:00:00Z","event_id":"evt_LOCAL_AR1","agent_id":"agent:test:123"}
{"type":"thread.create","timestamp":"2026-02-06T10:01:00Z","event_id":"evt_LOCAL_TC1","thread_id":"thr_001"}
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
	result, err := m.MergeAll()
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

	m := NewMerger(tmpDir, tmpDir)

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
	m := NewMerger(tmpDir, tmpDir)

	// Create local file
	localPath := filepath.Join(tmpDir, "test.jsonl")
	localData := `{"type":"message.create","timestamp":"2026-02-06T10:00:00Z","event_id":"evt_LOCAL","message_id":"msg_001"}
`
	if err := os.WriteFile(localPath, []byte(localData), 0600); err != nil {
		t.Fatalf("write local file: %v", err)
	}

	// Merge with non-existent remote (should succeed)
	result, err := m.mergeFile(localPath, "nonexistent.jsonl")
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
