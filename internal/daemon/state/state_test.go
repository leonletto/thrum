package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	state, err := NewState(thrumDir, thrumDir, "r_TEST123456", "")
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
	if _, err := state.WriteEvent(context.Background(), event); err != nil {
		t.Fatalf("write event: %v", err)
	}

	// Read back from JSONL (all events go to the local journal as of v0.10.6)
	jsonlPath := filepath.Join(thrumDir, "events.jsonl")
	events, err := readJSONL(jsonlPath)
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least 1 event in events.jsonl")
	}
	written := events[len(events)-1]

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
	state, err := NewState(thrumDir, thrumDir, "r_TEST123456", "")
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

		if _, err := state.WriteEvent(context.Background(), event); err != nil {
			t.Fatalf("write event %d: %v", i, err)
		}
	}

	// Read all lines after all writes complete to avoid race on "last line"
	jsonlPath := filepath.Join(thrumDir, "events.jsonl")
	lines, err := readJSONL(jsonlPath)
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}

	if len(lines) != numEvents {
		t.Fatalf("expected %d lines, got %d", numEvents, len(lines))
	}

	// Check all event IDs are unique
	seen := make(map[string]bool)
	for _, written := range lines {
		eventID, ok := written["event_id"].(string)
		if !ok || eventID == "" {
			t.Errorf("missing event_id in event: %v", written)
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
	state, err := NewState(thrumDir, thrumDir, "r_TEST123456", "")
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
	if _, err := state.WriteEvent(context.Background(), event); err != nil {
		t.Fatalf("write event: %v", err)
	}

	// Read back from JSONL (all events go to the local journal as of v0.10.6)
	jsonlPath := filepath.Join(thrumDir, "events.jsonl")
	events, err := readJSONL(jsonlPath)
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least 1 event in events.jsonl")
	}
	written := events[len(events)-1]

	// Check event_id was preserved
	eventID, ok := written["event_id"].(string)
	if !ok {
		t.Fatal("event_id field missing or not a string")
	}

	if eventID != existingID {
		t.Errorf("event_id should be preserved: expected %s, got %s", existingID, eventID)
	}
}

func TestStateAccessors(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	repoID := "r_TEST123456"
	state, err := NewState(thrumDir, thrumDir, repoID, "")
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

	state, err := NewState(thrumDir, thrumDir, "r_TEST123456", "")
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

	_, err := NewState(invalidDir, invalidDir, "r_TEST123456", "")
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

	s, err := NewState(thrumDir, syncDir, "r_SEPARATE01", "")
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

	// Write an event — should go to thrumDir/events.jsonl (local journal) NOT syncDir
	event := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2024-01-01T12:00:00Z",
		AgentID:   "agent:test:SEP123",
		Kind:      "agent",
		Role:      "tester",
		Module:    "test",
	}
	if _, err := s.WriteEvent(context.Background(), event); err != nil {
		t.Fatalf("write event: %v", err)
	}

	// Verify events.jsonl is in thrumDir (local journal, as of v0.10.6)
	eventsPath := filepath.Join(thrumDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); os.IsNotExist(err) {
		t.Error("events.jsonl should be in thrumDir (local journal), not syncDir")
	}
	// Confirm events.jsonl is NOT in syncDir
	syncEventsPath := filepath.Join(syncDir, "events.jsonl")
	if _, err := os.Stat(syncEventsPath); err == nil {
		t.Error("events.jsonl must NOT be written to syncDir; it is a local-only journal")
	}

	// Write a message event — should also go to thrumDir/events.jsonl (all events local)
	msgEvent := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2024-01-01T12:00:00Z",
		MessageID: "msg_sep001",
		AgentID:   "agent:test:SEP123",
		SessionID: "ses_sep001",
		Body:      types.MessageBody{Format: "markdown", Content: "separate sync dir"},
	}
	if _, err := s.WriteEvent(context.Background(), msgEvent); err != nil {
		t.Fatalf("write message event: %v", err)
	}

	// Verify message event also lands in thrumDir/events.jsonl
	events, err := readJSONL(eventsPath)
	if err != nil {
		t.Fatalf("read events.jsonl: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events in thrumDir/events.jsonl, got %d", len(events))
	}
}

func TestWriteEvent_MarshalError(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	s, err := NewState(thrumDir, thrumDir, "r_TEST123456", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Channels can't be marshaled to JSON
	type BadEvent struct {
		Ch chan int
	}
	_, err = s.WriteEvent(context.Background(), BadEvent{Ch: make(chan int)})
	if err == nil {
		t.Error("Expected marshal error for channel type")
	}
}

func TestWriteEvent_NonMessageEvent(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	s, err := NewState(thrumDir, thrumDir, "r_TEST123456", "")
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

	if _, err := s.WriteEvent(context.Background(), event); err != nil {
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

func TestNewState_BootstrapsIdentityWhenEmpty(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}

	s, err := NewState(thrumDir, thrumDir, "r_test_bootstrap", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer s.Close()

	id := s.DaemonID()
	if id == "" || !strings.HasPrefix(id, "d_") {
		t.Fatalf("daemonID invalid: %q", id)
	}

	// config.json persisted
	cfgBytes, err := os.ReadFile(filepath.Join(thrumDir, "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	if !strings.Contains(string(cfgBytes), id) {
		t.Fatalf("daemon_id %q not found in config.json:\n%s", id, cfgBytes)
	}

	// SQLite mirror populated
	var dbID string
	if err := s.RawDB().QueryRow(`SELECT daemon_id FROM daemon_identity`).Scan(&dbID); err != nil {
		t.Fatalf("query daemon_identity: %v", err)
	}
	if dbID != id {
		t.Fatalf("SQLite daemon_id %q != state DaemonID %q", dbID, id)
	}

	// Identity accessor returns populated struct
	ident := s.Identity()
	if ident.DaemonID != id {
		t.Fatalf("Identity().DaemonID = %q, want %q", ident.DaemonID, id)
	}
	if ident.RepoPath == "" {
		t.Fatalf("Identity().RepoPath empty")
	}
}

func TestNewState_UsesCallerIDVerbatim(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	_ = os.MkdirAll(thrumDir, 0o750)

	s, err := NewState(thrumDir, thrumDir, "r_test_verbatim", "fixed-test-daemon")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer s.Close()

	if s.DaemonID() != "fixed-test-daemon" {
		t.Fatalf("DaemonID = %q, want fixed-test-daemon", s.DaemonID())
	}

	// config.json must NOT have been created — caller-provided id path.
	if _, err := os.Stat(filepath.Join(thrumDir, "config.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("config.json should not exist for test path; stat err = %v", err)
	}

	// daemon_identity table must be empty.
	var count int
	if err := s.RawDB().QueryRow(`SELECT COUNT(*) FROM daemon_identity`).Scan(&count); err != nil {
		t.Fatalf("count daemon_identity: %v", err)
	}
	if count != 0 {
		t.Fatalf("daemon_identity row count = %d, want 0", count)
	}
}

// TestWriteEvent_StructuralEvent_FiresSyncTrigger pins E3.AC.2 (positive case):
// agent.register is a structural event and must return a non-nil postCommit
// closure that, when invoked, fires the trigger hook exactly once.
//
// thrum-bsn7 contract: the trigger no longer fires inline inside WriteEvent;
// callers must invoke the returned postCommit closure after releasing any
// external lock so walker+compactor cannot starve concurrent lock-holders.
func TestWriteEvent_StructuralEvent_FiresSyncTrigger(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	st, err := NewState(thrumDir, thrumDir, "r_TRIGGER_TEST", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = st.Close() }()

	var triggerCount atomic.Int32
	st.SetSyncTrigger(func(ctx context.Context) {
		triggerCount.Add(1)
	})

	evt := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-05-18T00:00:00Z",
		AgentID:   "agent:test:TRIGGER01",
		Kind:      "agent",
		Role:      "tester",
		Module:    "test",
	}
	postCommit, err := st.WriteEvent(context.Background(), evt)
	if err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	// Trigger must NOT have fired inline (bsn7 contract — caller-driven).
	if got := triggerCount.Load(); got != 0 {
		t.Errorf("triggerCount before postCommit = %d, want 0 (bsn7 contract: trigger is deferred)", got)
	}
	if postCommit == nil {
		t.Fatal("postCommit is nil for a structural event — should be a non-nil closure")
	}
	postCommit()
	if got := triggerCount.Load(); got != 1 {
		t.Errorf("triggerCount after postCommit = %d, want 1 (structural event must fire trigger exactly once)", got)
	}
}

// TestWriteEvent_NonStructuralEvent_NoSyncTrigger pins E3.AC.2 (negative case /
// T2 invariant): message.receipt is a non-structural event and must NOT invoke
// the trigger hook. This is the core of the "100 receipts → 0 commits" invariant.
func TestWriteEvent_NonStructuralEvent_NoSyncTrigger(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	st, err := NewState(thrumDir, thrumDir, "r_TRIGGER_TEST", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = st.Close() }()

	var triggerCount atomic.Int32
	st.SetSyncTrigger(func(ctx context.Context) {
		triggerCount.Add(1)
	})

	// First write a message.create so the message exists in the DB for the receipt
	createEvt := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2026-05-18T00:00:00Z",
		MessageID: "msg_receipt_test",
		AgentID:   "agent:test:SENDER",
		SessionID: "ses_test",
		Body:      types.MessageBody{Format: "markdown", Content: "hello"},
	}
	createPost, err := st.WriteEvent(context.Background(), createEvt)
	if err != nil {
		t.Fatalf("WriteEvent (message.create): %v", err)
	}
	// thrum-bsn7: message.create IS structural — invoke postCommit to
	// stay faithful to the production call shape, then reset the counter
	// before the negative-case message.receipt write below.
	if createPost != nil {
		createPost()
	}
	triggerCount.Store(0)

	// Now write message.receipt — non-structural, must NOT fire trigger
	receiptEvt := types.MessageReceiptEvent{
		Type:        "message.receipt",
		Timestamp:   "2026-05-18T00:01:00Z",
		MessageID:   "msg_receipt_test",
		AgentID:     "agent:test:READER",
		SessionID:   "ses_reader",
		ReceiptType: "read",
	}
	receiptPost, err := st.WriteEvent(context.Background(), receiptEvt)
	if err != nil {
		t.Fatalf("WriteEvent (message.receipt): %v", err)
	}
	// Defensive: receipt is non-structural so postCommit MUST be nil.
	// Invoking it (or not) is a no-op semantically — the test asserts
	// triggerCount stays zero below — but guard the invariant directly.
	if receiptPost != nil {
		t.Errorf("postCommit for message.receipt = non-nil, want nil (non-structural events must not return a trigger closure)")
	}

	if got := triggerCount.Load(); got != 0 {
		t.Errorf("triggerCount = %d, want 0 (non-structural event must not fire trigger)", got)
	}
}

// TestWriteEvent_NoTriggerSet_DoesNotPanic verifies that when SetSyncTrigger
// was never called (the default for tests), structural events succeed without
// panicking on the nil hook.
func TestWriteEvent_NoTriggerSet_DoesNotPanic(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	st, err := NewState(thrumDir, thrumDir, "r_TRIGGER_NIL", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = st.Close() }()

	// No SetSyncTrigger call — trigger is nil. Must not panic on structural event.
	evt := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-05-18T00:00:00Z",
		AgentID:   "agent:test:NILHOOK",
		Kind:      "agent",
		Role:      "tester",
		Module:    "test",
	}
	if _, err := st.WriteEvent(context.Background(), evt); err != nil {
		t.Fatalf("WriteEvent with nil sync trigger: %v", err)
	}
}

// TestGoPostCommit_NilIsNoOp verifies the nil-fn fast path so callers
// can pass WriteEvent's return value directly. thrum-1nkt.5.
func TestGoPostCommit_NilIsNoOp(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	st, err := NewState(thrumDir, thrumDir, "r_GOPC_NIL", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Must not panic and must not increment the WaitGroup.
	st.GoPostCommit(nil)
	if !st.WaitPostCommit(50 * time.Millisecond) {
		t.Error("WaitPostCommit timed out after GoPostCommit(nil); the no-op fast path must not register on the WaitGroup")
	}
}

// TestGoPostCommit_DrainsOnWait verifies that WaitPostCommit blocks
// until in-flight GoPostCommit goroutines complete. thrum-1nkt.5.
func TestGoPostCommit_DrainsOnWait(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	st, err := NewState(thrumDir, thrumDir, "r_GOPC_DRAIN", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = st.Close() }()

	const callers = 8
	const work = 100 * time.Millisecond

	var done atomic.Int32
	for range callers {
		st.GoPostCommit(func() {
			time.Sleep(work)
			done.Add(1)
		})
	}

	// Short wait must time out because in-flight goroutines still sleep.
	if st.WaitPostCommit(work / 4) {
		t.Error("WaitPostCommit returned true with in-flight goroutines (timeout was too short to have drained); drain semantics broken")
	}

	// Generous wait must succeed and observe all callers complete.
	if !st.WaitPostCommit(work * 4) {
		t.Fatalf("WaitPostCommit timed out; expected %d goroutines to finish within budget", callers)
	}
	if got := int(done.Load()); got != callers {
		t.Errorf("done = %d, want %d after drain", got, callers)
	}
}

// TestStateClose_DrainsInflightPostCommits verifies Close()'s drain
// path actually waits for in-flight GoPostCommit goroutines so the DB
// and JSONL writer don't shut down underneath them. thrum-1nkt.5.
func TestStateClose_DrainsInflightPostCommits(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	st, err := NewState(thrumDir, thrumDir, "r_GOPC_CLOSE", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	work := 80 * time.Millisecond
	var done atomic.Bool
	st.GoPostCommit(func() {
		time.Sleep(work)
		done.Store(true)
	})

	start := time.Now()
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	elapsed := time.Since(start)

	if !done.Load() {
		t.Errorf("Close returned before in-flight GoPostCommit goroutine completed (elapsed=%v); drain skipped", elapsed)
	}
	if elapsed < work/2 {
		t.Errorf("Close elapsed = %v, want ≥ %v (must have waited for the in-flight goroutine)", elapsed, work/2)
	}
}
