package state_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
)

func createTestState(t *testing.T) *state.State {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSequenceNumbers_Monotonic(t *testing.T) {
	st := createTestState(t)

	// Write 10 events
	for i := 0; i < 10; i++ {
		event := types.AgentRegisterEvent{
			Type:      "agent.register",
			Timestamp: "2024-01-01T12:00:00Z",
			AgentID:   fmt.Sprintf("agent:test:%d", i),
			Kind:      "agent",
			Role:      "tester",
			Module:    "test",
		}
		if err := st.WriteEvent(context.Background(), event); err != nil {
			t.Fatalf("write event %d: %v", i, err)
		}
	}

	// Verify sequences are 1-10 with no gaps
	rows, err := st.DB().Query("SELECT sequence FROM events ORDER BY sequence")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	expected := int64(1)
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if seq != expected {
			t.Errorf("expected sequence %d, got %d", expected, seq)
		}
		expected++
	}
	if expected != 11 {
		t.Errorf("expected 10 events, got %d", expected-1)
	}
}

func TestOriginDaemon_PresentInAllEvents(t *testing.T) {
	st := createTestState(t)

	// Write an agent register event
	event := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2024-01-01T12:00:00Z",
		AgentID:   "agent:test:ABC",
		Kind:      "agent",
		Role:      "tester",
		Module:    "test",
	}
	if err := st.WriteEvent(context.Background(), event); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Check origin_daemon is set in events table
	var originDaemon string
	err := st.DB().QueryRow("SELECT origin_daemon FROM events LIMIT 1").Scan(&originDaemon)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if originDaemon == "" {
		t.Error("origin_daemon should not be empty")
	}

	// Verify it matches the daemon ID
	if originDaemon != st.DaemonID() {
		t.Errorf("expected origin_daemon=%s, got %s", st.DaemonID(), originDaemon)
	}
}

func TestOriginDaemon_PreservedIfSet(t *testing.T) {
	st := createTestState(t)

	// Write event with pre-set origin_daemon
	event := types.AgentRegisterEvent{
		Type:         "agent.register",
		Timestamp:    "2024-01-01T12:00:00Z",
		OriginDaemon: "d_remote_peer",
		AgentID:      "agent:test:XYZ",
		Kind:         "agent",
		Role:         "tester",
		Module:       "test",
	}
	if err := st.WriteEvent(context.Background(), event); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Check origin_daemon preserves the pre-set value
	var originDaemon string
	err := st.DB().QueryRow("SELECT origin_daemon FROM events LIMIT 1").Scan(&originDaemon)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if originDaemon != "d_remote_peer" {
		t.Errorf("expected origin_daemon=d_remote_peer, got %s", originDaemon)
	}
}

func TestGetEventsSince_ViaState(t *testing.T) {
	st := createTestState(t)

	// Write 20 events
	for i := 0; i < 20; i++ {
		event := types.AgentRegisterEvent{
			Type:      "agent.register",
			Timestamp: "2024-01-01T12:00:00Z",
			AgentID:   fmt.Sprintf("agent:test:%d", i),
			Kind:      "agent",
			Role:      "tester",
			Module:    "test",
		}
		if err := st.WriteEvent(context.Background(), event); err != nil {
			t.Fatalf("write event %d: %v", i, err)
		}
	}

	// Query first 10
	events, nextSeq, more, err := st.GetEventsSince(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("GetEventsSince: %v", err)
	}
	if len(events) != 10 {
		t.Errorf("expected 10 events, got %d", len(events))
	}
	if nextSeq != 10 {
		t.Errorf("expected nextSeq=10, got %d", nextSeq)
	}
	if !more {
		t.Error("expected moreAvailable=true")
	}

	// Query remaining
	events, nextSeq, more, err = st.GetEventsSince(context.Background(), 10, 100)
	if err != nil {
		t.Fatalf("GetEventsSince: %v", err)
	}
	if len(events) != 10 {
		t.Errorf("expected 10 events, got %d", len(events))
	}
	if nextSeq != 20 {
		t.Errorf("expected nextSeq=20, got %d", nextSeq)
	}
	if more {
		t.Error("expected moreAvailable=false")
	}
}

func TestSequence_PersistsAcrossRestart(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}

	// First state instance — write 5 events
	st1, err := state.NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("create state 1: %v", err)
	}
	for i := 0; i < 5; i++ {
		event := types.AgentRegisterEvent{
			Type:      "agent.register",
			Timestamp: "2024-01-01T12:00:00Z",
			AgentID:   fmt.Sprintf("agent:test:%d", i),
			Kind:      "agent",
			Role:      "tester",
			Module:    "test",
		}
		if err := st1.WriteEvent(context.Background(), event); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	_ = st1.Close()

	// Second state instance — should continue from sequence 5
	st2, err := state.NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("create state 2: %v", err)
	}
	defer func() { _ = st2.Close() }()

	// Write one more event
	event := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2024-01-01T12:00:00Z",
		AgentID:   "agent:test:restart",
		Kind:      "agent",
		Role:      "tester",
		Module:    "test",
	}
	if err := st2.WriteEvent(context.Background(), event); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Verify the new event has sequence 6
	var maxSeq int64
	err = st2.DB().QueryRow("SELECT MAX(sequence) FROM events").Scan(&maxSeq)
	if err != nil {
		t.Fatalf("query max seq: %v", err)
	}
	if maxSeq != 6 {
		t.Errorf("expected max sequence 6 after restart, got %d", maxSeq)
	}
}

func TestEventJSON_ContainsAllFields(t *testing.T) {
	st := createTestState(t)

	event := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2024-01-01T12:00:00Z",
		AgentID:   "agent:test:CHECK",
		Kind:      "agent",
		Role:      "tester",
		Module:    "test",
	}
	if err := st.WriteEvent(context.Background(), event); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read event_json from events table
	var eventJSONStr string
	err := st.DB().QueryRow("SELECT event_json FROM events LIMIT 1").Scan(&eventJSONStr)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(eventJSONStr), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify required sync fields are present
	requiredFields := []string{"event_id", "type", "timestamp", "origin_daemon", "sequence", "v"}
	for _, field := range requiredFields {
		if _, ok := parsed[field]; !ok {
			t.Errorf("event_json missing field: %s", field)
		}
	}
}

func TestSequence_MessageEventsAlsoTracked(t *testing.T) {
	st := createTestState(t)

	// Write a non-message event
	agentEvent := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2024-01-01T12:00:00Z",
		AgentID:   "agent:test:ABC123",
		Kind:      "agent",
		Role:      "tester",
		Module:    "test",
	}
	if err := st.WriteEvent(context.Background(), agentEvent); err != nil {
		t.Fatalf("write agent event: %v", err)
	}

	// Write a message event
	msgEvent := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2024-01-01T12:00:01Z",
		MessageID: "msg_test123",
		AgentID:   "agent:test:ABC123",
		SessionID: "ses_test456",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "Test",
		},
	}
	if err := st.WriteEvent(context.Background(), msgEvent); err != nil {
		t.Fatalf("write message event: %v", err)
	}

	// Both should be in the events table with contiguous sequences
	var count int
	err := st.DB().QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 events in events table, got %d", count)
	}

	// Verify sequences
	rows, err := st.DB().Query("SELECT sequence, type FROM events ORDER BY sequence")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var sequences []int64
	for rows.Next() {
		var seq int64
		var evtType string
		if err := rows.Scan(&seq, &evtType); err != nil {
			t.Fatalf("scan: %v", err)
		}
		sequences = append(sequences, seq)
	}

	if len(sequences) != 2 || sequences[0] != 1 || sequences[1] != 2 {
		t.Errorf("expected sequences [1, 2], got %v", sequences)
	}
}

func TestDaemonID_Stable(t *testing.T) {
	st := createTestState(t)
	id := st.DaemonID()
	if id == "" {
		t.Error("daemon ID should not be empty")
	}
	// Daemon ID should start with "d_"
	if len(id) < 3 || id[:2] != "d_" {
		t.Errorf("daemon ID should start with 'd_', got %s", id)
	}
}

// Silence the "imported and not used" error for sql package.
var _ = sql.ErrNoRows
