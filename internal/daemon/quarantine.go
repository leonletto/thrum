package daemon

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// QuarantineStore manages quarantined events that failed validation.
type QuarantineStore struct {
	db *sql.DB
}

// QuarantinedEvent represents an event that failed validation.
type QuarantinedEvent struct {
	EventID    string `json:"event_id"`
	ReceivedAt int64  `json:"received_at"`
	FromDaemon string `json:"from_daemon"`
	Reason     string `json:"reason"`
	EventJSON  string `json:"event_json"`
}

// QuarantineAlertThreshold is the number of invalid events per peer per hour
// that triggers a warning.
const QuarantineAlertThreshold = 10

// NewQuarantineStore creates a new quarantine store and ensures the table exists.
func NewQuarantineStore(db *sql.DB) (*QuarantineStore, error) {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS quarantined_events (
			event_id    TEXT PRIMARY KEY,
			received_at INTEGER NOT NULL,
			from_daemon TEXT NOT NULL,
			reason      TEXT NOT NULL,
			event_json  TEXT NOT NULL
		)
	`)
	if err != nil {
		return nil, fmt.Errorf("create quarantined_events table: %w", err)
	}

	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_quarantine_daemon ON quarantined_events(from_daemon)`)
	if err != nil {
		return nil, fmt.Errorf("create quarantine index: %w", err)
	}

	return &QuarantineStore{db: db}, nil
}

// Quarantine stores an invalid event in the quarantine table.
// It also checks the alert threshold and logs a warning if exceeded.
func (q *QuarantineStore) Quarantine(eventID, fromDaemon, reason, eventJSON string) error {
	now := time.Now().Unix()

	_, err := q.db.Exec(
		`INSERT OR REPLACE INTO quarantined_events (event_id, received_at, from_daemon, reason, event_json) VALUES (?, ?, ?, ?, ?)`,
		eventID, now, fromDaemon, reason, eventJSON,
	)
	if err != nil {
		return fmt.Errorf("insert quarantined event: %w", err)
	}

	log.Printf("[quarantine] event %s from %s quarantined: %s", eventID, fromDaemon, reason)

	// Check alert threshold: count invalid events from this peer in the last hour
	oneHourAgo := now - 3600
	var count int
	err = q.db.QueryRow(
		`SELECT COUNT(*) FROM quarantined_events WHERE from_daemon = ? AND received_at > ?`,
		fromDaemon, oneHourAgo,
	).Scan(&count)
	if err != nil {
		return nil // Don't fail the quarantine over an alert check
	}

	if count > QuarantineAlertThreshold {
		log.Printf("[quarantine] WARNING: %d invalid events from peer %s in the last hour (threshold: %d)",
			count, fromDaemon, QuarantineAlertThreshold)
	}

	return nil
}

// List returns all quarantined events, ordered by most recent first.
func (q *QuarantineStore) List(limit int) ([]QuarantinedEvent, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := q.db.Query(
		`SELECT event_id, received_at, from_daemon, reason, event_json FROM quarantined_events ORDER BY received_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query quarantined events: %w", err)
	}
	defer rows.Close()

	var events []QuarantinedEvent
	for rows.Next() {
		var e QuarantinedEvent
		if err := rows.Scan(&e.EventID, &e.ReceivedAt, &e.FromDaemon, &e.Reason, &e.EventJSON); err != nil {
			return nil, fmt.Errorf("scan quarantined event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// CountByPeer returns the number of quarantined events from a given peer in the last hour.
func (q *QuarantineStore) CountByPeer(peerID string) (int, error) {
	oneHourAgo := time.Now().Unix() - 3600
	var count int
	err := q.db.QueryRow(
		`SELECT COUNT(*) FROM quarantined_events WHERE from_daemon = ? AND received_at > ?`,
		peerID, oneHourAgo,
	).Scan(&count)
	return count, err
}

// Count returns the total number of quarantined events.
func (q *QuarantineStore) Count() (int, error) {
	var count int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM quarantined_events`).Scan(&count)
	return count, err
}
