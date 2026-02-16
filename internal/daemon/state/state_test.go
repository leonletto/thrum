package state

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/internal/types"
	"github.com/oklog/ulid/v2"
)

// Helper to read all lines from a JSONL file.
func readJSONL(path string) ([]map[string]any, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304 - test fixture path
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	result := make([]map[string]any, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, err
		}
		result = append(result, event)
	}

	return result, nil
}

func TestWriteEvent_GeneratesEventID(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	// Create state
	state, err := NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = state.Close() }()

	// Create a test event
	event := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2024-01-01T12:00:00Z",
		MessageID: "msg_test123",
		AgentID:   "agent:test:ABC123",
		SessionID: "ses_test456",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "Test message",
		},
	}

	// Write event
	if err := state.WriteEvent(context.Background(), event); err != nil {
		t.Fatalf("write event: %v", err)
	}

	// Read back from JSONL (message events go to per-agent files now)
	jsonlPath := filepath.Join(thrumDir, "messages", "test_ABC123.jsonl")
	data, err := os.ReadFile(jsonlPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}

	// Parse the written event
	var written map[string]any
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	// Check event_id exists
	eventID, ok := written["event_id"].(string)
	if !ok {
		t.Fatal("event_id field missing or not a string")
	}

	// Check event_id is non-empty
	if eventID == "" {
		t.Fatal("event_id is empty")
	}

	// Check event_id has evt_ prefix
	if !strings.HasPrefix(eventID, "evt_") {
		t.Errorf("event_id should have evt_ prefix, got: %s", eventID)
	}

	// Check event_id is a valid ULID (after removing prefix)
	ulidStr := strings.TrimPrefix(eventID, "evt_")
	if _, err := ulid.Parse(ulidStr); err != nil {
		t.Errorf("event_id is not a valid ULID: %v", err)
	}

	// Check version field
	version, ok := written["v"].(float64) // JSON numbers unmarshal to float64
	if !ok {
		t.Fatal("v field missing or not a number")
	}
	if version != 1 {
		t.Errorf("v should be 1, got: %v", version)
	}
}

func TestWriteEvent_UniqueEventIDs(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	// Create state
	state, err := NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = state.Close() }()

	// Write multiple events sequentially — we're testing ULID uniqueness, not concurrency.
	// Concurrent writes cause SQLITE_BUSY under the race detector.
	const numEvents = 10

	for i := 0; i < numEvents; i++ {
		event := types.MessageCreateEvent{
			Type:      "message.create",
			Timestamp: "2024-01-01T12:00:00Z",
			MessageID: fmt.Sprintf("msg_test_%d", i),
			AgentID:   "agent:test:ABC123",
			SessionID: "ses_test456",
			Body: types.MessageBody{
				Format:  "markdown",
				Content: "Test message",
			},
		}

		if err := state.WriteEvent(context.Background(), event); err != nil {
			t.Fatalf("write event %d: %v", i, err)
		}
	}

	// Read all lines after all writes complete to avoid race on "last line"
	jsonlPath := filepath.Join(thrumDir, "messages", "test_ABC123.jsonl")
	data, err := os.ReadFile(jsonlPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != numEvents {
		t.Fatalf("expected %d lines, got %d", numEvents, len(lines))
	}

	// Check all event IDs are unique
	seen := make(map[string]bool)
	for _, line := range lines {
		var written map[string]any
		if err := json.Unmarshal([]byte(line), &written); err != nil {
			t.Errorf("unmarshal event: %v", err)
			continue
		}

		eventID, ok := written["event_id"].(string)
		if !ok || eventID == "" {
			t.Errorf("missing event_id in line: %s", line)
			continue
		}

		if seen[eventID] {
			t.Errorf("duplicate event_id: %s", eventID)
		}
		seen[eventID] = true
	}
}

func TestWriteEvent_PreservesExistingEventID(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	// Create state
	state, err := NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = state.Close() }()

	// Create event with pre-existing event_id
	existingID := "evt_01234567890ABCDEFGHIJ"
	event := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2024-01-01T12:00:00Z",
		EventID:   existingID,
		Version:   1,
		MessageID: "msg_test123",
		AgentID:   "agent:test:ABC123",
		SessionID: "ses_test456",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "Test message",
		},
	}

	// Write event
	if err := state.WriteEvent(context.Background(), event); err != nil {
		t.Fatalf("write event: %v", err)
	}

	// Read back from JSONL (message events go to per-agent files now)
	jsonlPath := filepath.Join(thrumDir, "messages", "test_ABC123.jsonl")
	data, err := os.ReadFile(jsonlPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}

	// Parse the written event
	var written map[string]any
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	// Check event_id was preserved
	eventID, ok := written["event_id"].(string)
	if !ok {
		t.Fatal("event_id field missing or not a string")
	}

	if eventID != existingID {
		t.Errorf("event_id should be preserved: expected %s, got %s", existingID, eventID)
	}
}

func TestWriteEvent_Routing(t *testing.T) {
	// Create temp directory for test
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	// Create state
	state, err := NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = state.Close() }()

	// Test 1: Non-message events go to events.jsonl
	agentEvent := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2024-01-01T12:00:00Z",
		AgentID:   "agent:test:ABC123",
		Kind:      "agent",
		Role:      "tester",
		Module:    "test",
	}

	if err := state.WriteEvent(context.Background(), agentEvent); err != nil {
		t.Fatalf("write agent event: %v", err)
	}

	// Check events.jsonl was created and contains the event
	eventsPath := filepath.Join(thrumDir, "events.jsonl")
	eventsData, err := os.ReadFile(eventsPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("read events.jsonl: %v", err)
	}

	var writtenAgent map[string]any
	if err := json.Unmarshal(eventsData, &writtenAgent); err != nil {
		t.Fatalf("unmarshal agent event: %v", err)
	}

	if writtenAgent["type"] != "agent.register" {
		t.Errorf("expected agent.register in events.jsonl, got %s", writtenAgent["type"])
	}

	// Test 2: Message events go to per-agent message files
	messageEvent := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2024-01-01T12:00:00Z",
		MessageID: "msg_test123",
		AgentID:   "agent:test:ABC123",
		SessionID: "ses_test456",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "Test message",
		},
	}

	if err := state.WriteEvent(context.Background(), messageEvent); err != nil {
		t.Fatalf("write message event: %v", err)
	}

	// Check messages/test_ABC123.jsonl was created
	messagePath := filepath.Join(thrumDir, "messages", "test_ABC123.jsonl")
	messageData, err := os.ReadFile(messagePath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("read message file: %v", err)
	}

	var writtenMessage map[string]any
	if err := json.Unmarshal(messageData, &writtenMessage); err != nil {
		t.Fatalf("unmarshal message event: %v", err)
	}

	if writtenMessage["type"] != "message.create" {
		t.Errorf("expected message.create in per-agent file, got %s", writtenMessage["type"])
	}
	if writtenMessage["message_id"] != "msg_test123" {
		t.Errorf("expected msg_test123, got %s", writtenMessage["message_id"])
	}

	// Test 3: Message edit routes to original author's file
	editEvent := types.MessageEditEvent{
		Type:      "message.edit",
		Timestamp: "2024-01-01T13:00:00Z",
		MessageID: "msg_test123",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "Updated message",
		},
	}

	if err := state.WriteEvent(context.Background(), editEvent); err != nil {
		t.Fatalf("write edit event: %v", err)
	}

	// Check the edit went to the same file as the original message
	events, err := readJSONL(messagePath)
	if err != nil {
		t.Fatalf("read message file after edit: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events in message file, got %d", len(events))
	}

	if events[0]["type"] != "message.create" {
		t.Errorf("first event should be message.create, got %s", events[0]["type"])
	}
	if events[1]["type"] != "message.edit" {
		t.Errorf("second event should be message.edit, got %s", events[1]["type"])
	}

	// Test 4: Message delete routes to original author's file
	deleteEvent := types.MessageDeleteEvent{
		Type:      "message.delete",
		Timestamp: "2024-01-01T14:00:00Z",
		MessageID: "msg_test123",
		Reason:    "test delete",
	}

	if err := state.WriteEvent(context.Background(), deleteEvent); err != nil {
		t.Fatalf("write delete event: %v", err)
	}

	// Check the delete went to the same file as the original message
	events, err = readJSONL(messagePath)
	if err != nil {
		t.Fatalf("read message file after delete: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events in message file, got %d", len(events))
	}

	if events[2]["type"] != "message.delete" {
		t.Errorf("third event should be message.delete, got %s", events[2]["type"])
	}
	if events[2]["message_id"] != "msg_test123" {
		t.Errorf("delete should reference msg_test123, got %s", events[2]["message_id"])
	}
}

func TestAgentIDToName(t *testing.T) {
	tests := []struct {
		agentID string
		want    string
	}{
		{"agent:coordinator:1B9K33T6RK", "coordinator_1B9K33T6RK"},
		{"furiosa", "furiosa"},
		{"implementer_35HV62T9B9", "implementer_35HV62T9B9"},
	}

	for _, tt := range tests {
		t.Run(tt.agentID, func(t *testing.T) {
			got := agentIDToName(tt.agentID)
			if got != tt.want {
				t.Errorf("agentIDToName(%q) = %q, want %q", tt.agentID, got, tt.want)
			}
		})
	}
}

func TestAgentIDToName_ConsistentWithSchema(t *testing.T) {
	// Verify agentIDToName produces identical results to schema.ExtractAgentName
	// to prevent future drift between runtime routing and migration logic.
	inputs := []string{
		"agent:coordinator:1B9K33T6RK",
		"agent:implementer:35HV62T9B9",
		"agent:reviewer:XYZABC",
		"furiosa",
		"implementer_35HV62T9B9",
		"agent:test:ABC123",
	}

	for _, agentID := range inputs {
		t.Run(agentID, func(t *testing.T) {
			stateResult := agentIDToName(agentID)
			schemaResult := schema.ExtractAgentName(agentID)
			if stateResult != schemaResult {
				t.Errorf("agentIDToName(%q) = %q, but schema.ExtractAgentName(%q) = %q — these must match",
					agentID, stateResult, agentID, schemaResult)
			}
		})
	}
}

func TestStateAccessors(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	repoID := "r_TEST123456"
	state, err := NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = state.Close() }()

	// Test DB() returns non-nil
	if state.RawDB() == nil {
		t.Error("DB() returned nil, expected non-nil database")
	}

	// Test RepoID() returns expected value
	if got := state.RepoID(); got != repoID {
		t.Errorf("RepoID() = %q, want %q", got, repoID)
	}

	// Test RepoPath() returns the parent of thrumDir (tmpDir)
	expectedRepoPath := tmpDir
	if got := state.RepoPath(); got != expectedRepoPath {
		t.Errorf("RepoPath() = %q, want %q", got, expectedRepoPath)
	}

	// Test SyncDir() returns the sync dir (we passed thrumDir as syncDir)
	expectedSyncDir := thrumDir
	if got := state.SyncDir(); got != expectedSyncDir {
		t.Errorf("SyncDir() = %q, want %q", got, expectedSyncDir)
	}

	// Test Projector() returns non-nil
	if state.Projector() == nil {
		t.Error("Projector() returned nil, expected non-nil projector")
	}

	// Test Lock() and Unlock() don't deadlock
	state.Lock()
	// Verify Lock/Unlock don't panic (SA2001: intentionally empty critical section)
	state.Unlock() //nolint:staticcheck // SA2001: testing that lock/unlock don't panic

	// Test RLock() and RUnlock() don't deadlock
	state.RLock()
	// Verify RLock/RUnlock don't panic (SA2001: intentionally empty critical section)
	state.RUnlock() //nolint:staticcheck // SA2001: testing that lock/unlock don't panic

	// Test concurrent RLock() works (read locks should not block each other)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state.RLock()
			defer state.RUnlock()
			// Simulate some read operation
			_ = state.RepoID()
		}()
	}
	wg.Wait()
}

func TestStateClose(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	state, err := NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}

	// Close the state
	if err := state.Close(); err != nil {
		t.Errorf("Close() returned error: %v, want nil", err)
	}
}

func TestNewState_InvalidDir(t *testing.T) {
	// Use a path that doesn't exist and can't be created
	invalidDir := "/nonexistent/directory/that/cannot/be/created/.thrum"

	_, err := NewState(invalidDir, invalidDir, "r_TEST123456")
	if err == nil {
		t.Error("NewState with invalid directory returned nil error, expected error")
	}
}

func TestNewState_SeparateSyncDir(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(tmpDir, "sync-worktree")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}
	if err := os.MkdirAll(syncDir, 0750); err != nil {
		t.Fatalf("create sync dir: %v", err)
	}

	s, err := NewState(thrumDir, syncDir, "r_SEPARATE01")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Verify paths are distinct
	if s.SyncDir() != syncDir {
		t.Errorf("SyncDir() = %s, want %s", s.SyncDir(), syncDir)
	}
	if s.RepoPath() != tmpDir {
		t.Errorf("RepoPath() = %s, want %s", s.RepoPath(), tmpDir)
	}

	// Write an event — should go to syncDir/events.jsonl, not thrumDir
	event := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2024-01-01T12:00:00Z",
		AgentID:   "agent:test:SEP123",
		Kind:      "agent",
		Role:      "tester",
		Module:    "test",
	}
	if err := s.WriteEvent(context.Background(), event); err != nil {
		t.Fatalf("write event: %v", err)
	}

	// Verify events.jsonl is in syncDir
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); os.IsNotExist(err) {
		t.Error("events.jsonl should be in syncDir, not thrumDir")
	}

	// Write a message event — should create per-agent file in syncDir/messages/
	msgEvent := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2024-01-01T12:00:00Z",
		MessageID: "msg_sep001",
		AgentID:   "agent:test:SEP123",
		SessionID: "ses_sep001",
		Body:      types.MessageBody{Format: "markdown", Content: "separate sync dir"},
	}
	if err := s.WriteEvent(context.Background(), msgEvent); err != nil {
		t.Fatalf("write message event: %v", err)
	}

	msgPath := filepath.Join(syncDir, "messages", "test_SEP123.jsonl")
	if _, err := os.Stat(msgPath); os.IsNotExist(err) {
		t.Error("message file should be in syncDir/messages/")
	}
}

func TestNewState_InvalidSyncDir(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	// Create a file where events.jsonl directory should be, to force writer creation failure
	badSyncDir := filepath.Join(tmpDir, "bad-sync")
	// Create the directory but put a file where "messages" dir would go
	if err := os.MkdirAll(badSyncDir, 0750); err != nil {
		t.Fatalf("create bad sync dir: %v", err)
	}
	// Create events.jsonl as a directory to cause issues
	eventsDir := filepath.Join(badSyncDir, "events.jsonl")
	if err := os.MkdirAll(eventsDir, 0750); err != nil {
		t.Fatalf("create events dir: %v", err)
	}

	_, err := NewState(thrumDir, badSyncDir, "r_BADSYNC01")
	if err == nil {
		t.Error("Expected error when events.jsonl is a directory")
	}
}

func TestWriteEvent_MarshalError(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	s, err := NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Channels can't be marshaled to JSON
	type BadEvent struct {
		Ch chan int
	}
	err = s.WriteEvent(context.Background(), BadEvent{Ch: make(chan int)})
	if err == nil {
		t.Error("Expected marshal error for channel type")
	}
}

func TestResolveAgentForMessage_EdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	s, err := NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	t.Run("message_create_missing_agent_id", func(t *testing.T) {
		event := map[string]any{
			"type": "message.create",
		}
		_, err := s.resolveAgentForMessage(context.Background(), event)
		if err == nil {
			t.Error("Expected error for missing agent_id")
		}
	})

	t.Run("message_edit_missing_message_id", func(t *testing.T) {
		event := map[string]any{
			"type": "message.edit",
		}
		_, err := s.resolveAgentForMessage(context.Background(), event)
		if err == nil {
			t.Error("Expected error for missing message_id")
		}
	})

	t.Run("message_delete_nonexistent_message", func(t *testing.T) {
		event := map[string]any{
			"type":       "message.delete",
			"message_id": "msg_nonexistent",
		}
		_, err := s.resolveAgentForMessage(context.Background(), event)
		if err == nil {
			t.Error("Expected error for nonexistent message")
		}
	})

	t.Run("unexpected_event_type", func(t *testing.T) {
		event := map[string]any{
			"type": "message.unknown",
		}
		_, err := s.resolveAgentForMessage(context.Background(), event)
		if err == nil {
			t.Error("Expected error for unexpected event type")
		}
	})

	t.Run("message_create_valid", func(t *testing.T) {
		event := map[string]any{
			"type":     "message.create",
			"agent_id": "agent:coordinator:XYZ789",
		}
		name, err := s.resolveAgentForMessage(context.Background(), event)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if name != "coordinator_XYZ789" {
			t.Errorf("Expected 'coordinator_XYZ789', got '%s'", name)
		}
	})
}

func TestWriteEvent_NonMessageEvent(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	s, err := NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Write a session event (non-message) — should go to events.jsonl
	event := types.AgentSessionStartEvent{
		Type:      "session.start",
		Timestamp: "2024-01-01T12:00:00Z",
		SessionID: "ses_test001",
		AgentID:   "agent:test:ABC123",
	}

	if err := s.WriteEvent(context.Background(), event); err != nil {
		t.Fatalf("write event: %v", err)
	}

	// Verify it went to events.jsonl
	eventsPath := filepath.Join(thrumDir, "events.jsonl")
	events, err := readJSONL(eventsPath)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("Expected at least 1 event in events.jsonl")
	}
	if events[0]["type"] != "session.start" {
		t.Errorf("Expected session.start, got %s", events[0]["type"])
	}
}
