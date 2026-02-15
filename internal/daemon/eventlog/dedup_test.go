package eventlog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/eventlog"
	"github.com/leonletto/thrum/internal/daemon/safedb"
)

func TestHasEvent_Exists(t *testing.T) {
	db := setupTestDB(t)
	insertTestEvent(t, db, 1, "evt_existing", "agent.register")

	exists, err := eventlog.HasEvent(context.Background(), safedb.New(db), "evt_existing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("expected HasEvent to return true for existing event")
	}
}

func TestHasEvent_NotExists(t *testing.T) {
	db := setupTestDB(t)

	exists, err := eventlog.HasEvent(context.Background(), safedb.New(db), "evt_nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected HasEvent to return false for non-existent event")
	}
}

func TestHasEvent_Performance(t *testing.T) {
	db := setupTestDB(t)

	// Insert 1000 events
	for i := int64(1); i <= 1000; i++ {
		eventJSON, _ := json.Marshal(map[string]any{
			"event_id": fmt.Sprintf("evt_%04d", i),
			"type":     "agent.register",
		})
		_, err := db.Exec(
			`INSERT INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json) VALUES (?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("evt_%04d", i), i, "agent.register", "2024-01-01T12:00:00Z", "d_test", string(eventJSON),
		)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Dedup lookup should be fast (<10ms for 1000 events)
	start := time.Now()
	for i := 1; i <= 1000; i++ {
		_, err := eventlog.HasEvent(context.Background(), safedb.New(db), fmt.Sprintf("evt_%04d", i))
		if err != nil {
			t.Fatalf("HasEvent: %v", err)
		}
	}
	elapsed := time.Since(start)

	// 1000 lookups should take well under 1 second with PRIMARY KEY index
	if elapsed > time.Second {
		t.Errorf("dedup too slow: 1000 lookups took %v", elapsed)
	}
}

func TestDuplicateEventInsert(t *testing.T) {
	db := setupTestDB(t)
	insertTestEvent(t, db, 1, "evt_dup", "agent.register")

	// Inserting same event_id again should fail or be ignored (PRIMARY KEY constraint)
	eventJSON, _ := json.Marshal(map[string]any{"event_id": "evt_dup"})
	_, err := db.Exec(
		`INSERT OR IGNORE INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json) VALUES (?, ?, ?, ?, ?, ?)`,
		"evt_dup", 2, "agent.register", "2024-01-01T12:00:00Z", "d_test", string(eventJSON),
	)
	if err != nil {
		t.Fatalf("insert or ignore should not error: %v", err)
	}

	// Verify only one event exists
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM events WHERE event_id = 'evt_dup'`).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 event, got %d", count)
	}
}
