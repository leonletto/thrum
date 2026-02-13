package checkpoint

import (
	"database/sql"
	"fmt"
)

// Checkpoint represents sync progress for a peer daemon.
type Checkpoint struct {
	PeerDaemonID      string `json:"peer_daemon_id"`
	LastSyncedSeq     int64  `json:"last_synced_sequence"`
	LastSyncTimestamp int64  `json:"last_sync_timestamp"`
	SyncStatus        string `json:"sync_status"`
}

// GetCheckpoint returns the checkpoint for a peer daemon.
// Returns nil with no error if the peer has no checkpoint.
func GetCheckpoint(db *sql.DB, peerID string) (*Checkpoint, error) {
	var c Checkpoint
	err := db.QueryRow(
		`SELECT peer_daemon_id, last_synced_sequence, last_sync_timestamp, sync_status
		 FROM sync_checkpoints WHERE peer_daemon_id = ?`,
		peerID,
	).Scan(&c.PeerDaemonID, &c.LastSyncedSeq, &c.LastSyncTimestamp, &c.SyncStatus)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get checkpoint: %w", err)
	}
	return &c, nil
}

// UpdateCheckpoint creates or updates the checkpoint for a peer daemon.
// This is idempotent — calling with the same values has no effect.
func UpdateCheckpoint(db *sql.DB, peerID string, seq int64, timestamp int64) error {
	_, err := db.Exec(
		`INSERT INTO sync_checkpoints (peer_daemon_id, last_synced_sequence, last_sync_timestamp, sync_status)
		 VALUES (?, ?, ?, 'idle')
		 ON CONFLICT(peer_daemon_id) DO UPDATE SET
		   last_synced_sequence = excluded.last_synced_sequence,
		   last_sync_timestamp = excluded.last_sync_timestamp`,
		peerID, seq, timestamp,
	)
	if err != nil {
		return fmt.Errorf("update checkpoint: %w", err)
	}
	return nil
}

// UpdateSyncStatus updates the sync_status for a peer daemon.
// Valid statuses: 'idle', 'syncing', 'error'.
func UpdateSyncStatus(db *sql.DB, peerID string, status string) error {
	_, err := db.Exec(
		`UPDATE sync_checkpoints SET sync_status = ? WHERE peer_daemon_id = ?`,
		status, peerID,
	)
	if err != nil {
		return fmt.Errorf("update sync status: %w", err)
	}
	return nil
}

// ListCheckpoints returns all checkpoints.
func ListCheckpoints(db *sql.DB) ([]Checkpoint, error) {
	rows, err := db.Query(
		`SELECT peer_daemon_id, last_synced_sequence, last_sync_timestamp, sync_status
		 FROM sync_checkpoints ORDER BY peer_daemon_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list checkpoints: %w", err)
	}
	defer rows.Close()

	var checkpoints []Checkpoint
	for rows.Next() {
		var c Checkpoint
		if err := rows.Scan(&c.PeerDaemonID, &c.LastSyncedSeq, &c.LastSyncTimestamp, &c.SyncStatus); err != nil {
			return nil, fmt.Errorf("scan checkpoint: %w", err)
		}
		checkpoints = append(checkpoints, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate checkpoints: %w", err)
	}
	return checkpoints, nil
}
