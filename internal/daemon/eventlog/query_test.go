package eventlog_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/eventlog"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/schema"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertTestEvent(t *testing.T, db *sql.DB, seq int64, eventID, eventType string) {
	t.Helper()
	eventJSON, _ := json.Marshal(map[string]any{
		"event_id":      eventID,
		"type":          eventType,
		"timestamp":     "2024-01-01T12:00:00Z",
		"origin_daemon": "d_testhost",
		"sequence":      seq,
	})
	_, err := db.Exec(
		`INSERT INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json) VALUES (?, ?, ?, ?, ?, ?)`,
		eventID, seq, eventType, "2024-01-01T12:00:00Z", "d_testhost", string(eventJSON),
	)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
}

func TestGetEventsSince_EmptyDatabase(t *testing.T) {
	db := setupTestDB(t)

	events, nextSeq, more, err := eventlog.GetEventsSince(context.Background(), safedb.New(db), 0, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
	if nextSeq != 0 {
		t.Errorf("expected nextSeq=0, got %d", nextSeq)
	}
	if more {
		t.Error("expected moreAvailable=false")
	}
}

func TestGetEventsSince_PartialBatch(t *testing.T) {
	db := setupTestDB(t)

	// Insert 100 events
	for i := int64(1); i <= 100; i++ {
		insertTestEvent(t, db, i, fmt.Sprintf("evt_%03d", i), "agent.register")
	}

	// Query with limit 50 — should get partial batch
	events, nextSeq, more, err := eventlog.GetEventsSince(context.Background(), safedb.New(db), 0, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 50 {
		t.Errorf("expected 50 events, got %d", len(events))
	}
	if nextSeq != 50 {
		t.Errorf("expected nextSeq=50, got %d", nextSeq)
	}
	if !more {
		t.Error("expected moreAvailable=true")
	}

	// Verify sequences are contiguous
	for i, e := range events {
		expected := int64(i + 1)
		if e.Sequence != expected {
			t.Errorf("event %d: expected sequence %d, got %d", i, expected, e.Sequence)
		}
	}
}

func TestGetEventsSince_FullBatch(t *testing.T) {
	db := setupTestDB(t)

	// Insert 50 events
	for i := int64(1); i <= 50; i++ {
		insertTestEvent(t, db, i, fmt.Sprintf("evt_%03d", i), "message.create")
	}

	// Query with limit 100 — should get all events, no more available
	events, nextSeq, more, err := eventlog.GetEventsSince(context.Background(), safedb.New(db), 0, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 50 {
		t.Errorf("expected 50 events, got %d", len(events))
	}
	if nextSeq != 50 {
		t.Errorf("expected nextSeq=50, got %d", nextSeq)
	}
	if more {
		t.Error("expected moreAvailable=false")
	}
}

func TestGetEventsSince_BeyondLastEvent(t *testing.T) {
	db := setupTestDB(t)

	// Insert 10 events
	for i := int64(1); i <= 10; i++ {
		insertTestEvent(t, db, i, fmt.Sprintf("evt_%03d", i), "agent.register")
	}

	// Query beyond last event
	events, nextSeq, more, err := eventlog.GetEventsSince(context.Background(), safedb.New(db), 10, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
	if nextSeq != 0 {
		t.Errorf("expected nextSeq=0, got %d", nextSeq)
	}
	if more {
		t.Error("expected moreAvailable=false")
	}
}

func TestGetEventsSince_NextSequenceIsMaxInBatch(t *testing.T) {
	db := setupTestDB(t)

	for i := int64(1); i <= 20; i++ {
		insertTestEvent(t, db, i, fmt.Sprintf("evt_%03d", i), "agent.register")
	}

	// Get events 6-15 (afterSeq=5, limit=10)
	events, nextSeq, more, err := eventlog.GetEventsSince(context.Background(), safedb.New(db), 5, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 10 {
		t.Errorf("expected 10 events, got %d", len(events))
	}
	if nextSeq != 15 {
		t.Errorf("expected nextSeq=15, got %d", nextSeq)
	}
	if !more {
		t.Error("expected moreAvailable=true")
	}
}

func TestGetEventsSince_IteratesAllEvents(t *testing.T) {
	db := setupTestDB(t)

	total := 100
	for i := int64(1); i <= int64(total); i++ {
		insertTestEvent(t, db, i, fmt.Sprintf("evt_%03d", i), "agent.register")
	}

	// Iterate in batches of 20
	var allEvents []eventlog.Event
	afterSeq := int64(0)
	for {
		events, nextSeq, more, err := eventlog.GetEventsSince(context.Background(), safedb.New(db), afterSeq, 20)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		allEvents = append(allEvents, events...)
		if !more {
			break
		}
		afterSeq = nextSeq
	}

	if len(allEvents) != total {
		t.Errorf("expected %d total events, got %d", total, len(allEvents))
	}

	// Verify no duplicates
	seen := make(map[string]bool)
	for _, e := range allEvents {
		if seen[e.EventID] {
			t.Errorf("duplicate event: %s", e.EventID)
		}
		seen[e.EventID] = true
	}
}

func TestGetEventsSince_DefaultLimit(t *testing.T) {
	db := setupTestDB(t)

	for i := int64(1); i <= 200; i++ {
		insertTestEvent(t, db, i, fmt.Sprintf("evt_%03d", i), "agent.register")
	}

	// Pass limit=0 should default to 100
	events, _, more, err := eventlog.GetEventsSince(context.Background(), safedb.New(db), 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 100 {
		t.Errorf("expected 100 events (default limit), got %d", len(events))
	}
	if !more {
		t.Error("expected moreAvailable=true")
	}
}
