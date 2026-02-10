package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBackfillEventID(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Create JSONL file with events lacking event_id
	jsonlPath := filepath.Join(tmpDir, "messages.jsonl")
	f, err := os.Create(jsonlPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("create JSONL file: %v", err)
	}

	// Write events without event_id
	events := []map[string]any{
		{
			"type":       "message.create",
			"timestamp":  "2024-01-01T12:00:00Z",
			"message_id": "msg_001",
			"agent_id":   "agent:test:123",
		},
		{
			"type":       "message.edit",
			"timestamp":  "2024-01-01T12:01:00Z",
			"message_id": "msg_001",
		},
		{
			"type":      "thread.create",
			"timestamp": "2024-01-01T12:02:00Z",
			"thread_id": "thr_001",
		},
	}

	encoder := json.NewEncoder(f)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			_ = f.Close()
			t.Fatalf("write event: %v", err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Run backfill
	if err := BackfillEventID(tmpDir); err != nil {
		t.Fatalf("BackfillEventID failed: %v", err)
	}

	// Read back and verify all events have event_id
	f2, err := os.Open(jsonlPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("open JSONL file: %v", err)
	}
	defer func() { _ = f2.Close() }()

	// Parse events
	var readEvents []map[string]any
	decoder := json.NewDecoder(f2)
	for decoder.More() {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		readEvents = append(readEvents, event)
	}

	if len(readEvents) != 3 {
		t.Errorf("expected 3 events, got %d", len(readEvents))
	}

	// Verify all events have event_id
	for i, event := range readEvents {
		eventID, ok := event["event_id"].(string)
		if !ok || eventID == "" {
			t.Errorf("event %d missing event_id", i)
		}

		// Verify event_id has evt_ prefix
		if len(eventID) < 4 || eventID[:4] != "evt_" {
			t.Errorf("event %d event_id %q should have evt_ prefix", i, eventID)
		}

		// Verify version field
		version, ok := event["v"].(float64)
		if !ok || version != 1 {
			t.Errorf("event %d missing or invalid version field", i)
		}
	}
}

func TestBackfillEventID_Deterministic(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Create JSONL file with single event
	jsonlPath := filepath.Join(tmpDir, "messages.jsonl")
	f, err := os.Create(jsonlPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("create JSONL file: %v", err)
	}

	event := map[string]any{
		"type":       "message.create",
		"timestamp":  "2024-01-01T12:00:00Z",
		"message_id": "msg_deterministic",
		"agent_id":   "agent:test:456",
	}

	encoder := json.NewEncoder(f)
	if err := encoder.Encode(event); err != nil {
		_ = f.Close()
		t.Fatalf("write event: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Run backfill twice
	if err := BackfillEventID(tmpDir); err != nil {
		t.Fatalf("BackfillEventID failed: %v", err)
	}

	// Read the event_id
	data, err := os.ReadFile(jsonlPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("read JSONL file: %v", err)
	}

	var firstEvent map[string]any
	if err := json.Unmarshal(data, &firstEvent); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	firstEventID, ok := firstEvent["event_id"].(string)
	if !ok {
		t.Fatalf("event_id should be string")
	}

	// Run backfill again (should be idempotent)
	if err := BackfillEventID(tmpDir); err != nil {
		t.Fatalf("BackfillEventID second run failed: %v", err)
	}

	// Read again
	data, err = os.ReadFile(jsonlPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("read JSONL file after second backfill: %v", err)
	}

	var secondEvent map[string]any
	if err := json.Unmarshal(data, &secondEvent); err != nil {
		t.Fatalf("unmarshal event after second backfill: %v", err)
	}

	secondEventID, ok := secondEvent["event_id"].(string)
	if !ok {
		t.Fatalf("event_id should be string")
	}

	// Verify event_id is the same (deterministic)
	if firstEventID != secondEventID {
		t.Errorf("event_id not deterministic: first=%q, second=%q", firstEventID, secondEventID)
	}
}

func TestBackfillEventID_NoFile(t *testing.T) {
	// Create temp directory without JSONL file
	tmpDir := t.TempDir()

	// Should not error if file doesn't exist
	if err := BackfillEventID(tmpDir); err != nil {
		t.Errorf("BackfillEventID should not error on missing file: %v", err)
	}
}

func TestBackfillEventID_AlreadyBackfilled(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Create JSONL file with events that already have event_id
	jsonlPath := filepath.Join(tmpDir, "messages.jsonl")
	f, err := os.Create(jsonlPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("create JSONL file: %v", err)
	}

	event := map[string]any{
		"type":       "message.create",
		"timestamp":  "2024-01-01T12:00:00Z",
		"event_id":   "evt_EXISTING123",
		"message_id": "msg_001",
		"v":          1,
	}

	encoder := json.NewEncoder(f)
	if err := encoder.Encode(event); err != nil {
		_ = f.Close()
		t.Fatalf("write event: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Run backfill
	if err := BackfillEventID(tmpDir); err != nil {
		t.Fatalf("BackfillEventID failed: %v", err)
	}

	// Read back and verify event_id is unchanged
	data, err := os.ReadFile(jsonlPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("read JSONL file: %v", err)
	}

	var readEvent map[string]any
	if err := json.Unmarshal(data, &readEvent); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	eventID, ok := readEvent["event_id"].(string)
	if !ok {
		t.Fatalf("event_id should be string")
	}
	if eventID != "evt_EXISTING123" {
		t.Errorf("existing event_id was changed: got %q, want evt_EXISTING123", eventID)
	}
}
