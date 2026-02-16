package eventlog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/checkpoint"
	"github.com/leonletto/thrum/internal/daemon/eventlog"
	"github.com/leonletto/thrum/internal/daemon/safedb"
)

func TestIntegration_FullSyncWorkflow(t *testing.T) {
	db := setupTestDB(t)

	// Create 100 events with origin_daemon and sequences
	for i := int64(1); i <= 100; i++ {
		eventJSON, _ := json.Marshal(map[string]any{
			"event_id":      fmt.Sprintf("evt_%03d", i),
			"type":          "agent.register",
			"timestamp":     "2024-01-01T12:00:00Z",
			"origin_daemon": "d_source",
			"sequence":      i,
			"agent_id":      fmt.Sprintf("agent:test:%d", i),
		})
		_, err := db.Exec(
			`INSERT INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json) VALUES (?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("evt_%03d", i), i, "agent.register", "2024-01-01T12:00:00Z", "d_source", string(eventJSON),
		)
		if err != nil {
			t.Fatalf("insert event %d: %v", i, err)
		}
	}

	// Simulate sync: query in batches of 20 with checkpoint updates
	peerID := "d_destination"
	var allRetrieved []eventlog.Event
	afterSeq := int64(0)
	batchNum := 0

	for {
		events, nextSeq, more, err := eventlog.GetEventsSince(context.Background(), safedb.New(db), afterSeq, 20)
		if err != nil {
			t.Fatalf("batch %d: %v", batchNum, err)
		}

		allRetrieved = append(allRetrieved, events...)

		if len(events) > 0 {
			// Update checkpoint after each batch
			if err := checkpoint.UpdateCheckpoint(context.Background(), safedb.New(db), peerID, nextSeq, 1700000000+int64(batchNum)); err != nil {
				t.Fatalf("update checkpoint: %v", err)
			}
		}

		if !more {
			break
		}
		afterSeq = nextSeq
		batchNum++
	}

	// Verify all 100 events retrieved
	if len(allRetrieved) != 100 {
		t.Errorf("expected 100 events, got %d", len(allRetrieved))
	}

	// Verify no duplicates
	seen := make(map[string]bool)
	for _, e := range allRetrieved {
		if seen[e.EventID] {
			t.Errorf("duplicate event: %s", e.EventID)
		}
		seen[e.EventID] = true
	}

	// Verify checkpoint reflects final state
	cp, err := checkpoint.GetCheckpoint(context.Background(), safedb.New(db), peerID)
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if cp == nil {
		t.Fatal("expected checkpoint")
	}
	if cp.LastSyncedSeq != 100 {
		t.Errorf("expected checkpoint seq 100, got %d", cp.LastSyncedSeq)
	}

	// Verify dedup: all events should exist
	for i := int64(1); i <= 100; i++ {
		exists, err := eventlog.HasEvent(context.Background(), safedb.New(db), fmt.Sprintf("evt_%03d", i))
		if err != nil {
			t.Fatalf("HasEvent: %v", err)
		}
		if !exists {
			t.Errorf("event evt_%03d should exist", i)
		}
	}

	// Verify dedup: non-existent event
	exists, err := eventlog.HasEvent(context.Background(), safedb.New(db), "evt_999")
	if err != nil {
		t.Fatalf("HasEvent: %v", err)
	}
	if exists {
		t.Error("evt_999 should not exist")
	}
}

func TestIntegration_EventFieldsParsing(t *testing.T) {
	db := setupTestDB(t)

	// Insert event with all fields populated
	eventJSON, _ := json.Marshal(map[string]any{
		"event_id":      "evt_fields_test",
		"type":          "message.create",
		"timestamp":     "2024-06-15T10:30:00Z",
		"origin_daemon": "d_myhost",
		"sequence":      1,
		"message_id":    "msg_123",
		"agent_id":      "agent:coordinator:ABC",
		"body":          map[string]string{"format": "markdown", "content": "Hello"},
	})

	_, err := db.Exec(
		`INSERT INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json) VALUES (?, ?, ?, ?, ?, ?)`,
		"evt_fields_test", 1, "message.create", "2024-06-15T10:30:00Z", "d_myhost", string(eventJSON),
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Query it back
	events, _, _, err := eventlog.GetEventsSince(context.Background(), safedb.New(db), 0, 10)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.EventID != "evt_fields_test" {
		t.Errorf("expected event_id=evt_fields_test, got %s", e.EventID)
	}
	if e.Type != "message.create" {
		t.Errorf("expected type=message.create, got %s", e.Type)
	}
	if e.Timestamp != "2024-06-15T10:30:00Z" {
		t.Errorf("expected timestamp=2024-06-15T10:30:00Z, got %s", e.Timestamp)
	}
	if e.OriginDaemon != "d_myhost" {
		t.Errorf("expected origin_daemon=d_myhost, got %s", e.OriginDaemon)
	}
	if e.Sequence != 1 {
		t.Errorf("expected sequence=1, got %d", e.Sequence)
	}

	// Parse the event_json to verify full content
	var parsed map[string]any
	if err := json.Unmarshal(e.EventJSON, &parsed); err != nil {
		t.Fatalf("unmarshal event_json: %v", err)
	}
	if parsed["message_id"] != "msg_123" {
		t.Errorf("expected message_id=msg_123 in event_json, got %v", parsed["message_id"])
	}
}
