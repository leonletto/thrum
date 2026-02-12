package eventlog

import (
	"database/sql"
	"fmt"
)

// HasEvent checks whether an event with the given event_id exists in the events table.
// Uses the PRIMARY KEY index on event_id for fast lookups.
func HasEvent(db *sql.DB, eventID string) (bool, error) {
	var exists int
	err := db.QueryRow(`SELECT 1 FROM events WHERE event_id = ? LIMIT 1`, eventID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check event existence: %w", err)
	}
	return true, nil
}
