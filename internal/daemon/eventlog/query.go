package eventlog

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// Event represents a stored event with its sequence number.
type Event struct {
	EventID      string          `json:"event_id"`
	Sequence     int64           `json:"sequence"`
	Type         string          `json:"type"`
	Timestamp    string          `json:"timestamp"`
	OriginDaemon string          `json:"origin_daemon"`
	EventJSON    json.RawMessage `json:"event_json"`
}

// GetEventsSince returns events with sequence > afterSeq, up to limit.
// Returns (events, nextSequence, moreAvailable, error).
// nextSequence is the highest sequence in the returned batch (for checkpointing).
// moreAvailable is true if more events exist after this batch.
func GetEventsSince(db *sql.DB, afterSeq int64, limit int) ([]Event, int64, bool, error) {
	if limit <= 0 {
		limit = 100
	}

	// Fetch limit+1 rows to detect if more are available
	rows, err := db.Query(
		`SELECT event_id, sequence, type, timestamp, origin_daemon, event_json
		 FROM events WHERE sequence > ? ORDER BY sequence LIMIT ?`,
		afterSeq, limit+1,
	)
	if err != nil {
		return nil, 0, false, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		var eventJSONStr string
		if err := rows.Scan(&e.EventID, &e.Sequence, &e.Type, &e.Timestamp, &e.OriginDaemon, &eventJSONStr); err != nil {
			return nil, 0, false, fmt.Errorf("scan event: %w", err)
		}
		e.EventJSON = json.RawMessage(eventJSONStr)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, fmt.Errorf("iterate events: %w", err)
	}

	if len(events) == 0 {
		return nil, 0, false, nil
	}

	// If we got more than limit, there are more available
	moreAvailable := len(events) > limit
	if moreAvailable {
		events = events[:limit] // Trim to requested limit
	}

	nextSequence := events[len(events)-1].Sequence

	return events, nextSequence, moreAvailable, nil
}
