package email

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DefaultDedupTTL is the retention window for email_msg_seen rows. 30
// days matches design-spec §9 step 3 — a Message-Id older than 30d
// won't be re-delivered by any sane IMAP server, so dedup rows past
// that age are dead weight.
const DefaultDedupTTL = 30 * 24 * time.Hour

// DefaultDedupSweepInterval is the cadence between sweeper passes. 24h
// matches the cleanup-handler cadence reserved in canonical-ref §6.3
// (internal.email_dedup_cleanup). Small relative to TTL so abandoned
// rows linger at most TTL + 24h.
const DefaultDedupSweepInterval = 24 * time.Hour

// Dedup is the L4 loop-protection layer for the inbound IMAP path
// (design-spec §9 step 3). SeenOrInsert atomically checks-and-records
// a Message-Id; concurrent inserts of the same id are race-safe via
// ON CONFLICT DO NOTHING, which is the load-bearing alternative to a
// read-then-write pattern that would race under multi-goroutine fetch.
type Dedup struct {
	db *sql.DB
}

// NewDedup wraps a *sql.DB ready for dedup operations. The caller owns
// the db lifetime; Close on the bridge does NOT close the underlying
// connection.
func NewDedup(db *sql.DB) *Dedup {
	return &Dedup{db: db}
}

// SeenOrInsert is the atomic check-and-record entry point. Returns
// alreadySeen=true when the Message-Id was previously recorded (caller
// drops the inbound message). alreadySeen=false on a fresh insert
// (caller proceeds to route).
//
// nonce and fromDaemonID accept "" — empty strings are persisted as
// NULL per canonical-ref §3.7 (replay-nonce defense reserved for
// v0.11.x; supervisor traffic legitimately has no daemon attribution).
//
// The ON CONFLICT DO NOTHING clause + RowsAffected check is the
// race-safe primitive: two concurrent goroutines inserting the same
// id will each call Exec, one will land the row, the other will see
// RowsAffected=0. No read-then-write window means no race.
func (d *Dedup) SeenOrInsert(ctx context.Context, messageID, fromDaemonID, nonce string, processedAt time.Time) (alreadySeen bool, err error) {
	var fromArg, nonceArg any
	if fromDaemonID == "" {
		fromArg = nil
	} else {
		fromArg = fromDaemonID
	}
	if nonce == "" {
		nonceArg = nil
	} else {
		nonceArg = nonce
	}

	res, err := d.db.ExecContext(ctx,
		`INSERT INTO email_msg_seen (message_id, from_daemon_id, nonce, processed_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(message_id) DO NOTHING`,
		messageID, fromArg, nonceArg, processedAt.UnixMilli(),
	)
	if err != nil {
		return false, fmt.Errorf("dedup insert: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("dedup rows-affected: %w", err)
	}
	if rows == 0 {
		return true, nil
	}
	return false, nil
}

// Sweep drops rows with processed_at < cutoff (UnixMilli). Returns the
// number of rows deleted. Called from the bridge's sweeper goroutine
// at DefaultDedupSweepInterval cadence.
//
// In D-B1 the sweeper goroutine launches from bridge.go's Run via
// safeGo (D-B1.14); A-B1's RegisterInternal adoption is a follow-up
// task filed at D-B1.17 Step 7, NOT a D-B1 ship blocker. The
// handler-shape contract from design-spec §13 anticipates the
// migration but D-B1 ships with a bare ticker.
func (d *Dedup) Sweep(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := d.db.ExecContext(ctx,
		`DELETE FROM email_msg_seen WHERE processed_at < ?`,
		cutoff.UnixMilli(),
	)
	if err != nil {
		return 0, fmt.Errorf("dedup sweep: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("dedup sweep rows-affected: %w", err)
	}
	return rows, nil
}
