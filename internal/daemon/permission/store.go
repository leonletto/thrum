package permission

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNudgeNotFound is returned by UpdatePendingNudge when zero rows
// match the target MessageID. Callers (the scheduler, the reply
// interceptor) should treat this as a lost-race with SweepExpired or
// a concurrent DeletePendingNudge and resynchronize their in-memory
// state with the DB — NOT silently succeed, which would diverge the
// scheduler's view from reality and cause double-sent nudges on the
// next check-pane fire.
var ErrNudgeNotFound = errors.New("permission: pending nudge not found")

// Store is a thin wrapper over the daemon's SQLite handle, providing
// typed CRUD helpers for the permission_nudges table (schema v21).
//
// All methods take context.Context so they honor the daemon's per-
// request timeouts. Lookups that find no row return (nil, nil) rather
// than sql.ErrNoRows, matching the idiom used by the rest of the
// daemon.
type Store struct {
	db *sql.DB
}

// NewStore constructs a Store from an existing *sql.DB. The caller
// keeps ownership of the handle; Store never closes it.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// InsertPendingNudge writes a new row. Returns an error if a row with
// the same MessageID already exists (primary key conflict).
func (s *Store) InsertPendingNudge(ctx context.Context, row *NudgeRow) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO permission_nudges
			(message_id, session, tmux_target, agent_name, pattern_key,
			 approve_key, deny_key, first_detected, last_nudge_at,
			 nudge_count, last_pane_hash, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.MessageID, row.Session, row.TmuxTarget, row.AgentName,
		row.PatternKey, row.ApproveKey, nullableString(row.DenyKey),
		row.FirstDetected, row.LastNudgeAt, row.NudgeCount,
		row.LastPaneHash[:], row.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert permission_nudges: %w", err)
	}
	return nil
}

// LookupPendingNudgeByMessageID returns the row whose primary key
// matches, or (nil, nil) if not found. Used by the reply interceptor.
func (s *Store) LookupPendingNudgeByMessageID(ctx context.Context, msgID string) (*NudgeRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT message_id, session, tmux_target, agent_name, pattern_key,
		       approve_key, deny_key, first_detected, last_nudge_at,
		       nudge_count, last_pane_hash, expires_at
		  FROM permission_nudges
		 WHERE message_id = ?`, msgID)
	return scanSingleRow(row)
}

// LookupPendingNudgeBySession returns the (at most one) row matching a
// session name, or (nil, nil) if none. Used by the scheduler on each
// check-pane fire.
func (s *Store) LookupPendingNudgeBySession(ctx context.Context, session string) (*NudgeRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT message_id, session, tmux_target, agent_name, pattern_key,
		       approve_key, deny_key, first_detected, last_nudge_at,
		       nudge_count, last_pane_hash, expires_at
		  FROM permission_nudges
		 WHERE session = ?
		 LIMIT 1`, session)
	return scanSingleRow(row)
}

// UpdatePendingNudge writes changed fields back. All fields are
// updated — callers should read-modify-write the whole struct.
// Returns ErrNudgeNotFound if no row matches the MessageID (lost race
// with SweepExpired / concurrent Delete). See ErrNudgeNotFound for
// why silent success is unsafe here.
func (s *Store) UpdatePendingNudge(ctx context.Context, row *NudgeRow) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE permission_nudges
		   SET session = ?, tmux_target = ?, agent_name = ?,
		       pattern_key = ?, approve_key = ?, deny_key = ?,
		       first_detected = ?, last_nudge_at = ?, nudge_count = ?,
		       last_pane_hash = ?, expires_at = ?
		 WHERE message_id = ?`,
		row.Session, row.TmuxTarget, row.AgentName,
		row.PatternKey, row.ApproveKey, nullableString(row.DenyKey),
		row.FirstDetected, row.LastNudgeAt, row.NudgeCount,
		row.LastPaneHash[:], row.ExpiresAt,
		row.MessageID,
	)
	if err != nil {
		return fmt.Errorf("update permission_nudges: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update permission_nudges rows affected: %w", err)
	}
	if n == 0 {
		return ErrNudgeNotFound
	}
	return nil
}

// DeletePendingNudge removes the row. Idempotent — a missing row is
// not an error.
func (s *Store) DeletePendingNudge(ctx context.Context, msgID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM permission_nudges WHERE message_id = ?`, msgID)
	if err != nil {
		return fmt.Errorf("delete permission_nudges: %w", err)
	}
	return nil
}

// ReloadOnBoot returns all non-expired rows, used by the scheduler to
// rehydrate its view after a daemon restart.
func (s *Store) ReloadOnBoot(ctx context.Context) ([]*NudgeRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, session, tmux_target, agent_name, pattern_key,
		       approve_key, deny_key, first_detected, last_nudge_at,
		       nudge_count, last_pane_hash, expires_at
		  FROM permission_nudges
		 WHERE expires_at > ?`, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("reload permission_nudges: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*NudgeRow
	for rows.Next() {
		r, err := scanMultiRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SweepExpired deletes rows whose expires_at is in the past. Returns
// the number of rows deleted. Called periodically by the daemon's
// background janitor.
func (s *Store) SweepExpired(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM permission_nudges WHERE expires_at <= ?`,
		time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("sweep permission_nudges: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// --- helpers ---

// nullableString returns nil for empty strings so DenyKey=="" survives
// the round-trip as SQL NULL rather than an empty-string literal.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// scanSingleRow scans a *sql.Row produced by QueryRowContext. Returns
// (nil, nil) on sql.ErrNoRows.
func scanSingleRow(row *sql.Row) (*NudgeRow, error) {
	r := &NudgeRow{}
	var denyKey sql.NullString
	var paneHash []byte
	err := row.Scan(
		&r.MessageID, &r.Session, &r.TmuxTarget, &r.AgentName,
		&r.PatternKey, &r.ApproveKey, &denyKey,
		&r.FirstDetected, &r.LastNudgeAt, &r.NudgeCount,
		&paneHash, &r.ExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan permission_nudges: %w", err)
	}
	if denyKey.Valid {
		r.DenyKey = denyKey.String
	}
	if len(paneHash) == 32 {
		copy(r.LastPaneHash[:], paneHash)
	}
	return r, nil
}

// scanMultiRow scans a single row from a *sql.Rows iterator.
func scanMultiRow(rows *sql.Rows) (*NudgeRow, error) {
	r := &NudgeRow{}
	var denyKey sql.NullString
	var paneHash []byte
	err := rows.Scan(
		&r.MessageID, &r.Session, &r.TmuxTarget, &r.AgentName,
		&r.PatternKey, &r.ApproveKey, &denyKey,
		&r.FirstDetected, &r.LastNudgeAt, &r.NudgeCount,
		&paneHash, &r.ExpiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan permission_nudges row: %w", err)
	}
	if denyKey.Valid {
		r.DenyKey = denyKey.String
	}
	if len(paneHash) == 32 {
		copy(r.LastPaneHash[:], paneHash)
	}
	return r, nil
}
