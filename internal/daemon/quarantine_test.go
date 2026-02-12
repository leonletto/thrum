package daemon

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestQuarantine_Basic(t *testing.T) {
	db := newTestDB(t)
	q, err := NewQuarantineStore(db)
	if err != nil {
		t.Fatalf("NewQuarantineStore: %v", err)
	}

	// Quarantine an event
	err = q.Quarantine("evt_01", "d_peer1", "invalid_signature", `{"event_id":"evt_01","type":"message.create"}`)
	if err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	// List should return it
	events, err := q.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventID != "evt_01" {
		t.Errorf("EventID = %q, want %q", events[0].EventID, "evt_01")
	}
	if events[0].FromDaemon != "d_peer1" {
		t.Errorf("FromDaemon = %q, want %q", events[0].FromDaemon, "d_peer1")
	}
	if events[0].Reason != "invalid_signature" {
		t.Errorf("Reason = %q, want %q", events[0].Reason, "invalid_signature")
	}
}

func TestQuarantine_MultipleEvents(t *testing.T) {
	db := newTestDB(t)
	q, err := NewQuarantineStore(db)
	if err != nil {
		t.Fatalf("NewQuarantineStore: %v", err)
	}

	// Quarantine multiple events
	for i := range 5 {
		err = q.Quarantine(
			fmt.Sprintf("evt_%02d", i),
			"d_peer1",
			"schema_violation",
			fmt.Sprintf(`{"event_id":"evt_%02d"}`, i),
		)
		if err != nil {
			t.Fatalf("Quarantine %d: %v", i, err)
		}
	}

	count, err := q.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 5 {
		t.Errorf("Count = %d, want 5", count)
	}
}

func TestQuarantine_CountByPeer(t *testing.T) {
	db := newTestDB(t)
	q, err := NewQuarantineStore(db)
	if err != nil {
		t.Fatalf("NewQuarantineStore: %v", err)
	}

	// Add events from two peers
	for i := range 3 {
		q.Quarantine(fmt.Sprintf("evt_a%d", i), "d_peer1", "reason", "{}")
	}
	for i := range 2 {
		q.Quarantine(fmt.Sprintf("evt_b%d", i), "d_peer2", "reason", "{}")
	}

	count1, err := q.CountByPeer("d_peer1")
	if err != nil {
		t.Fatalf("CountByPeer peer1: %v", err)
	}
	if count1 != 3 {
		t.Errorf("peer1 count = %d, want 3", count1)
	}

	count2, err := q.CountByPeer("d_peer2")
	if err != nil {
		t.Fatalf("CountByPeer peer2: %v", err)
	}
	if count2 != 2 {
		t.Errorf("peer2 count = %d, want 2", count2)
	}
}

func TestQuarantine_AlertThreshold(t *testing.T) {
	db := newTestDB(t)
	q, err := NewQuarantineStore(db)
	if err != nil {
		t.Fatalf("NewQuarantineStore: %v", err)
	}

	// Add QuarantineAlertThreshold + 1 events - the last should trigger WARNING log
	for i := range QuarantineAlertThreshold + 1 {
		err = q.Quarantine(
			fmt.Sprintf("evt_%03d", i),
			"d_suspicious",
			"invalid_signature",
			"{}",
		)
		if err != nil {
			t.Fatalf("Quarantine %d: %v", i, err)
		}
	}

	count, err := q.CountByPeer("d_suspicious")
	if err != nil {
		t.Fatalf("CountByPeer: %v", err)
	}
	if count != QuarantineAlertThreshold+1 {
		t.Errorf("count = %d, want %d", count, QuarantineAlertThreshold+1)
	}
}

func TestQuarantine_ListLimit(t *testing.T) {
	db := newTestDB(t)
	q, err := NewQuarantineStore(db)
	if err != nil {
		t.Fatalf("NewQuarantineStore: %v", err)
	}

	for i := range 20 {
		q.Quarantine(fmt.Sprintf("evt_%02d", i), "d_peer1", "reason", "{}")
	}

	events, err := q.List(5)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 5 {
		t.Errorf("List(5) returned %d events, want 5", len(events))
	}
}

func TestQuarantine_ListOrderByRecent(t *testing.T) {
	db := newTestDB(t)
	q, err := NewQuarantineStore(db)
	if err != nil {
		t.Fatalf("NewQuarantineStore: %v", err)
	}

	// Insert directly with explicit timestamps to test ordering
	now := time.Now().Unix()
	db.Exec(`INSERT INTO quarantined_events (event_id, received_at, from_daemon, reason, event_json) VALUES (?, ?, ?, ?, ?)`,
		"evt_old", now-10, "d_peer1", "old reason", "{}")
	db.Exec(`INSERT INTO quarantined_events (event_id, received_at, from_daemon, reason, event_json) VALUES (?, ?, ?, ?, ?)`,
		"evt_new", now, "d_peer1", "new reason", "{}")

	events, err := q.List(10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	// Most recent first
	if events[0].EventID != "evt_new" {
		t.Errorf("first event should be newest, got %q", events[0].EventID)
	}
	if events[1].EventID != "evt_old" {
		t.Errorf("second event should be oldest, got %q", events[1].EventID)
	}
}

func TestQuarantine_DoesNotBlockValidEvents(t *testing.T) {
	db := newTestDB(t)
	q, err := NewQuarantineStore(db)
	if err != nil {
		t.Fatalf("NewQuarantineStore: %v", err)
	}

	// Quarantine many events
	for i := range 50 {
		err = q.Quarantine(fmt.Sprintf("evt_%03d", i), "d_peer1", "reason", "{}")
		if err != nil {
			t.Fatalf("Quarantine %d: %v", i, err)
		}
	}

	// The quarantine store should not affect event processing
	// (it only stores invalid events, doesn't block the pipeline)
	count, err := q.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 50 {
		t.Errorf("expected 50 quarantined events, got %d", count)
	}
}
