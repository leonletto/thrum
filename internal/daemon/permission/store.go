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

// DeleteAndReturnPendingNudge atomically deletes the row with the
// given MessageID and returns its fields in one SQL statement via
// DELETE ... RETURNING. Returns (nil, nil) if no row matches — the
// idiomatic "not found, not an error" pattern for this package.
//
// This is the race-safe dispatch primitive used by the reply
// interceptor: two concurrent replies (e.g. a local reply and a
// cross-repo synced reply arriving in the same sync batch) can race
// on the hook. A naive Lookup + Delete pair would let both pass the
// nil-check and fire the keystroke twice, which corrupts numeric-
// selection prompts (e.g. claude "1/2/3") where the second keystroke
// lands on whatever prompt replaces the first. With this helper, the
// first caller gets the populated row and fires the keystroke; the
// second caller gets (nil, nil) and is a silent no-op.
//
// Trade-off: if the keystroke fails AFTER the delete succeeds, the
// row is already gone — there is no retry. The reviewer's call on
// Epic C: losing one retry beats double-firing keystrokes, and
// reminders won't re-fire because the row is gone. SweepExpired will
// not resurrect it (SweepExpired only deletes expired rows, never
// inserts).
func (s *Store) DeleteAndReturnPendingNudge(ctx context.Context, msgID string) (*NudgeRow, error) {
	row := s.db.QueryRowContext(ctx, `
		DELETE FROM permission_nudges
		 WHERE message_id = ?
		RETURNING message_id, session, tmux_target, agent_name, pattern_key,
		          approve_key, deny_key, first_detected, last_nudge_at,
		          nudge_count, last_pane_hash, expires_at`, msgID)
	return scanSingleRow(row)
}

// CountUnreadThreadDeliveries reports supervisor-audience read-state across the
// whole nudge thread rooted at rootMsgID — the firstDetect message plus every
// reminder threaded under it (messages.thread_id = rootMsgID). total is the
// number of audience deliveries; unread is how many have
// message_deliveries.read_at IS NULL.
//
// senderID (the supervisor pseudo-agent that AUTHORS the nudges, e.g.
// "supervisor_thrum") is excluded: every supervisor send writes a self-delivery
// to the sender that the projector auto-marks read, which is an artifact of the
// fan-out, not audience read-state. The modal owner is never a recipient (it is
// excluded from the supervisor set on both send paths), so no owner exclusion
// is needed here.
//
// Used by fireReminder (thrum-g23nb): total > 0 && unread == 0 means every real
// supervisor recipient has already read the nudge, so the reminder slot can be
// consumed silently instead of re-blasting an audience that has seen it. The
// total > 0 guard keeps an orphan/no-delivery thread from reading as
// "all read".
func (s *Store) CountUnreadThreadDeliveries(ctx context.Context, rootMsgID, senderID string) (unread, total int, err error) {
	// Column order: COUNT(*) -> total, SUM(read_at IS NULL) -> unread. Scanned
	// in that order below; the named returns are (unread, total), so the return
	// statement reverses them — keep the Scan order matching the SELECT.
	row := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       SUM(CASE WHEN d.read_at IS NULL THEN 1 ELSE 0 END)
		  FROM message_deliveries d
		  JOIN messages m ON d.message_id = m.message_id
		 WHERE (m.message_id = ? OR m.thread_id = ?)
		   AND d.recipient_agent_id != ?`,
		rootMsgID, rootMsgID, senderID)
	// SUM over zero matching rows yields SQL NULL — NullInt64 absorbs it.
	var totalN, unreadN sql.NullInt64
	if err := row.Scan(&totalN, &unreadN); err != nil {
		return 0, 0, fmt.Errorf("count unread thread deliveries: %w", err)
	}
	return int(unreadN.Int64), int(totalN.Int64), nil
}

// LookupMostRecentPendingNudgeByRecipient returns the non-expired
// pending nudge most recently delivered to the given recipient (by
// agent_id, e.g. "user:leon-letto"), or (nil, nil) if none match.
//
// Joins permission_nudges against message_deliveries on the firstDetect
// message_id — the PK of permission_nudges. Reminder messages share
// the same thread_id as the firstDetect but have distinct message_ids
// that are NOT in permission_nudges; this is fine because firstDetect
// is always delivered to every supervisor, so the delivery row for the
// firstDetect is guaranteed to exist whenever a nudge is pending.
//
// Used by the Telegram bridge's fresh-DM fallback (thrum-48kt.3): when
// a supervisor replies with a permission-response token on a fresh DM
// (no reply_to), this query finds the nudge to resolve.
func (s *Store) LookupMostRecentPendingNudgeByRecipient(ctx context.Context, recipientAgentID string) (*NudgeRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT pn.message_id, pn.session, pn.tmux_target, pn.agent_name,
		       pn.pattern_key, pn.approve_key, pn.deny_key,
		       pn.first_detected, pn.last_nudge_at, pn.nudge_count,
		       pn.last_pane_hash, pn.expires_at
		  FROM permission_nudges pn
		  JOIN message_deliveries md ON md.message_id = pn.message_id
		 WHERE md.recipient_agent_id = ?
		   AND pn.expires_at > ?
		 ORDER BY pn.last_nudge_at DESC
		 LIMIT 1`,
		recipientAgentID, time.Now().UTC())
	return scanSingleRow(row)
}

// LookupThreadIDForMessage returns the thread_id stored on the given
// message row, or "" if the row does not exist or has no thread_id.
// Used by TryResolve's fallback path: when a supervisor replies to a
// reminder message_id (not the firstDetect message_id), this lets us
// walk back to the thread root — which equals the nudge row's PK.
func (s *Store) LookupThreadIDForMessage(ctx context.Context, msgID string) (string, error) {
	var threadID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT thread_id FROM messages WHERE message_id = ?`, msgID,
	).Scan(&threadID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup thread_id for message %s: %w", msgID, err)
	}
	if !threadID.Valid {
		return "", nil
	}
	return threadID.String, nil
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
