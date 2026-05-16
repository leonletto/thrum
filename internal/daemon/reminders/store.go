package reminders

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
)

// ErrTerminalState is returned by mutation ops (Defer/Clear/Cancel/Fire/
// FireAndRearm) when the target row is already in a terminal state
// (fired/cleared/cancelled). Per canonical §3.5 state machine, terminal
// states accept no further transitions.
var ErrTerminalState = errors.New("reminders: row is in terminal state")

// ErrWrongTriggerKind is returned by Fire when called on a non-time row
// or by FireAndRearm when called on a non-condition row. The Fire vs
// FireAndRearm split is enforced rather than advisory.
var ErrWrongTriggerKind = errors.New("reminders: wrong trigger_kind for this op")

// SQLStore is the SQLite-backed Store implementation. Wraps *safedb.DB
// per project rule feedback_safecmd_safedb — never the raw *sql.DB.
type SQLStore struct {
	db *safedb.DB
}

// NewSQLStore wraps a *safedb.DB. The caller owns the safedb.DB lifecycle.
func NewSQLStore(db *safedb.DB) *SQLStore {
	return &SQLStore{db: db}
}

// timeNullable converts a *time.Time to sql.NullInt64 (unix seconds).
// Nil times become Valid=false (NULL in the column).
func timeNullable(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.Unix(), Valid: true}
}

// nullableUnixToTime is the inverse of timeNullable. Returns nil for
// NULL columns; otherwise the UTC time at the stored unix seconds.
func nullableUnixToTime(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.Unix(n.Int64, 0).UTC()
	return &t
}

// Mint validates the row, truncates the pane_snapshot, mints an id if
// absent, and INSERTs with state='open'. For time-triggered rows the
// dispatcher needs next_reminder_at populated to find them via the
// idx_reminders_next partial index — auto-derive from trigger_at when
// the caller didn't set it explicitly.
func (s *SQLStore) Mint(ctx context.Context, r *Reminder) error {
	if err := Validate(r); err != nil {
		return err
	}
	r.PaneSnapshot = TruncateSnapshot(r.PaneSnapshot)

	if r.ID == "" {
		// Daemon-chain rows (no single target_agent) prefix with "daemon"
		// so the id remains parseable.
		targetForID := r.TargetAgent
		if targetForID == "" {
			targetForID = "daemon"
		}
		r.ID = MintID(targetForID)
	}

	now := time.Now().UTC()
	if r.RaisedAt.IsZero() {
		r.RaisedAt = now
	}
	if r.State == "" {
		r.State = StateOpen
	}
	if r.DeferHistory == nil {
		r.DeferHistory = []DeferEntry{}
	}
	// For time-triggered rows the next fire is the trigger time. The
	// dispatcher's DueOpen query reads next_reminder_at, not trigger_at;
	// without this auto-derive a freshly minted time row would be
	// invisible to DueOpen until the caller separately set
	// next_reminder_at.
	if r.NextReminderAt == nil && r.TriggerKind == TriggerTime && r.TriggerAt != nil {
		t := *r.TriggerAt
		r.NextReminderAt = &t
	}
	r.CreatedAt = now
	r.UpdatedAt = now

	chainJSON, err := json.Marshal(r.TargetChain)
	if err != nil {
		return fmt.Errorf("marshal target_chain: %w", err)
	}
	histJSON, err := json.Marshal(r.DeferHistory)
	if err != nil {
		return fmt.Errorf("marshal defer_history: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO reminders (
			id, source, source_agent, trigger_kind, trigger_at, trigger_meta,
			target_agent, target_chain, body, raised_at, next_reminder_at,
			last_fired_at, state, pane_snapshot, defer_history,
			cleared_at, cancelled_at, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`,
		r.ID, string(r.Source), nullableString(r.SourceAgent), string(r.TriggerKind),
		timeNullable(r.TriggerAt), bytesOrNil(r.TriggerMeta),
		nullableString(r.TargetAgent), chainJSON, nullableString(r.Body),
		r.RaisedAt.Unix(), timeNullable(r.NextReminderAt),
		timeNullable(r.LastFiredAt), string(r.State), nullableString(r.PaneSnapshot),
		histJSON, timeNullable(r.ClearedAt), timeNullable(r.CancelledAt),
		r.CreatedAt.Unix(), r.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("insert reminder: %w", err)
	}
	return nil
}

// nullableString collapses "" → NULL for columns documented as nullable.
// Mirrors canonical §3.5: source_agent / target_agent / body /
// pane_snapshot are NULLable; empty Go strings map to NULL rather than
// the empty string so the column-info distinction stays meaningful.
func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// bytesOrNil maps nil/zero-length to NULL.
func bytesOrNil(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return []byte(b)
}

// scanReminder scans one row from a SELECT against the reminders table
// in the canonical column order. Both Get and the slice-returning
// methods share this helper.
type scannable interface {
	Scan(dest ...any) error
}

func scanReminder(row scannable) (*Reminder, error) {
	var (
		r            Reminder
		sourceAgent  sql.NullString
		triggerAt    sql.NullInt64
		triggerMeta  sql.NullString
		targetAgent  sql.NullString
		targetChain  sql.NullString
		body         sql.NullString
		nextRemAt    sql.NullInt64
		lastFiredAt  sql.NullInt64
		paneSnap     sql.NullString
		deferHistRaw sql.NullString
		clearedAt    sql.NullInt64
		cancelledAt  sql.NullInt64
		raisedAt     int64
		createdAt    int64
		updatedAt    int64
		source       string
		triggerKind  string
		state        string
	)
	err := row.Scan(
		&r.ID, &source, &sourceAgent, &triggerKind, &triggerAt, &triggerMeta,
		&targetAgent, &targetChain, &body, &raisedAt, &nextRemAt,
		&lastFiredAt, &state, &paneSnap, &deferHistRaw,
		&clearedAt, &cancelledAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	r.Source = Source(source)
	r.TriggerKind = TriggerKind(triggerKind)
	r.State = State(state)
	if sourceAgent.Valid {
		r.SourceAgent = sourceAgent.String
	}
	r.TriggerAt = nullableUnixToTime(triggerAt)
	if triggerMeta.Valid {
		r.TriggerMeta = json.RawMessage(triggerMeta.String)
	}
	if targetAgent.Valid {
		r.TargetAgent = targetAgent.String
	}
	if targetChain.Valid && targetChain.String != "" && targetChain.String != "null" {
		if err := json.Unmarshal([]byte(targetChain.String), &r.TargetChain); err != nil {
			return nil, fmt.Errorf("unmarshal target_chain for %s: %w", r.ID, err)
		}
	}
	if body.Valid {
		r.Body = body.String
	}
	r.RaisedAt = time.Unix(raisedAt, 0).UTC()
	r.NextReminderAt = nullableUnixToTime(nextRemAt)
	r.LastFiredAt = nullableUnixToTime(lastFiredAt)
	if paneSnap.Valid {
		r.PaneSnapshot = paneSnap.String
	}
	if deferHistRaw.Valid && deferHistRaw.String != "" {
		if err := json.Unmarshal([]byte(deferHistRaw.String), &r.DeferHistory); err != nil {
			return nil, fmt.Errorf("unmarshal defer_history for %s: %w", r.ID, err)
		}
	}
	if r.DeferHistory == nil {
		r.DeferHistory = []DeferEntry{}
	}
	r.ClearedAt = nullableUnixToTime(clearedAt)
	r.CancelledAt = nullableUnixToTime(cancelledAt)
	r.CreatedAt = time.Unix(createdAt, 0).UTC()
	r.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &r, nil
}

const selectColumns = `
	id, source, source_agent, trigger_kind, trigger_at, trigger_meta,
	target_agent, target_chain, body, raised_at, next_reminder_at,
	last_fired_at, state, pane_snapshot, defer_history,
	cleared_at, cancelled_at, created_at, updated_at
`

// Get returns the row with the given id. Returns sql.ErrNoRows when
// no row matches.
func (s *SQLStore) Get(ctx context.Context, id string) (*Reminder, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+selectColumns+` FROM reminders WHERE id = ?`, id)
	return scanReminder(row)
}

// List returns rows matching the filter. nil pointer filters skip that
// column entirely; empty string filters skip too. Ordered by raised_at
// DESC for deterministic test output.
func (s *SQLStore) List(ctx context.Context, filter ListFilter) ([]*Reminder, error) {
	q := `SELECT ` + selectColumns + ` FROM reminders WHERE 1=1`
	var args []any
	if filter.Source != nil {
		q += ` AND source = ?`
		args = append(args, string(*filter.Source))
	}
	if filter.TriggerKind != nil {
		q += ` AND trigger_kind = ?`
		args = append(args, string(*filter.TriggerKind))
	}
	if filter.State != nil {
		q += ` AND state = ?`
		args = append(args, string(*filter.State))
	}
	if filter.TargetAgent != "" {
		q += ` AND target_agent = ?`
		args = append(args, filter.TargetAgent)
	}
	if filter.SourceAgent != "" {
		q += ` AND source_agent = ?`
		args = append(args, filter.SourceAgent)
	}
	q += ` ORDER BY raised_at DESC`
	if filter.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*Reminder
	for rows.Next() {
		r, err := scanReminder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OpenForAgent returns the agent's open rows. Backed by
// idx_reminders_target (partial on state='open').
func (s *SQLStore) OpenForAgent(ctx context.Context, agent string) ([]*Reminder, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM reminders
		 WHERE target_agent = ? AND state = 'open'
		 ORDER BY next_reminder_at ASC NULLS LAST`,
		agent,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*Reminder
	for rows.Next() {
		r, err := scanReminder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DueOpen returns rows where state='open' AND next_reminder_at <= now.
// Backed by idx_reminders_next (partial on state='open').
func (s *SQLStore) DueOpen(ctx context.Context, now time.Time) ([]*Reminder, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM reminders
		 WHERE state = 'open' AND next_reminder_at IS NOT NULL AND next_reminder_at <= ?
		 ORDER BY next_reminder_at ASC`,
		now.Unix(),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*Reminder
	for rows.Next() {
		r, err := scanReminder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Defer requires state=open. Appends to defer_history and updates
// next_reminder_at.
func (s *SQLStore) Defer(ctx context.Context, id string, until time.Time, by string) error {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if cur.State != StateOpen {
		return fmt.Errorf("%w: defer on %s (id=%s)", ErrTerminalState, cur.State, id)
	}
	cur.DeferHistory = append(cur.DeferHistory, DeferEntry{
		DeferredBy: by, DeferTo: until, When: time.Now().UTC(),
	})
	histJSON, err := json.Marshal(cur.DeferHistory)
	if err != nil {
		return fmt.Errorf("marshal defer_history: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE reminders
		SET next_reminder_at = ?, defer_history = ?, updated_at = ?
		WHERE id = ?
	`, until.Unix(), histJSON, time.Now().UTC().Unix(), id)
	return err
}

// Clear transitions an open row to state='cleared' with cleared_at=now
// and next_reminder_at=NULL. Per canonical §3.5 state machine, terminal
// rows cannot be re-cleared.
func (s *SQLStore) Clear(ctx context.Context, id string, by string) error {
	return s.transitionToTerminal(ctx, id, StateCleared, "cleared_at", by)
}

// Cancel transitions an open row to state='cancelled' with
// cancelled_at=now and next_reminder_at=NULL.
func (s *SQLStore) Cancel(ctx context.Context, id string, by string) error {
	return s.transitionToTerminal(ctx, id, StateCancelled, "cancelled_at", by)
}

// transitionToTerminal is the shared body for Clear and Cancel — only the
// new state and the timestamp column differ. `by` is currently unused on
// the row (no audit column for "cleared_by" in the canonical DDL); kept
// in the signature for future expansion and parity with Defer.
func (s *SQLStore) transitionToTerminal(ctx context.Context, id string, newState State, tsCol string, _ string) error {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if cur.State != StateOpen {
		return fmt.Errorf("%w: %s on %s (id=%s)", ErrTerminalState, newState, cur.State, id)
	}
	now := time.Now().UTC().Unix()
	// tsCol is a hardcoded literal from Clear/Cancel — never user input.
	q := `UPDATE reminders SET state = ?, ` + tsCol + ` = ?, next_reminder_at = NULL, updated_at = ? WHERE id = ?`
	_, err = s.db.ExecContext(ctx, q, string(newState), now, now, id)
	return err
}

// Fire is the terminal transition for time-triggered (one-shot)
// reminders. Requires state=open AND trigger_kind=time; both gates are
// enforced rather than advisory.
func (s *SQLStore) Fire(ctx context.Context, id string, fired time.Time) error {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if cur.State != StateOpen {
		return fmt.Errorf("%w: fire on %s (id=%s)", ErrTerminalState, cur.State, id)
	}
	if cur.TriggerKind != TriggerTime {
		return fmt.Errorf("%w: Fire requires trigger_kind=time; got %s (id=%s; use FireAndRearm for condition rows)",
			ErrWrongTriggerKind, cur.TriggerKind, id)
	}
	now := time.Now().UTC().Unix()
	_, err = s.db.ExecContext(ctx, `
		UPDATE reminders
		SET state = ?, last_fired_at = ?, next_reminder_at = NULL, updated_at = ?
		WHERE id = ?
	`, string(StateFired), fired.Unix(), now, id)
	return err
}

// FireAndRearm is the recurring transition for condition-triggered
// reminders. State stays 'open'; last_fired_at advances; next_reminder_at
// is set to the next fire window. Caller computes `next` based on the
// trigger's cadence (e.g. sweep interval).
func (s *SQLStore) FireAndRearm(ctx context.Context, id string, fired, next time.Time) error {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if cur.State != StateOpen {
		return fmt.Errorf("%w: fire-and-rearm on %s (id=%s)", ErrTerminalState, cur.State, id)
	}
	if !isConditionKind(cur.TriggerKind) {
		return fmt.Errorf("%w: FireAndRearm requires condition_* trigger_kind; got %s (id=%s; use Fire for time rows)",
			ErrWrongTriggerKind, cur.TriggerKind, id)
	}
	now := time.Now().UTC().Unix()
	_, err = s.db.ExecContext(ctx, `
		UPDATE reminders
		SET last_fired_at = ?, next_reminder_at = ?, updated_at = ?
		WHERE id = ?
	`, fired.Unix(), next.Unix(), now, id)
	return err
}

func isConditionKind(k TriggerKind) bool {
	return strings.HasPrefix(string(k), "condition_")
}

// MintConditionForAgent will be added by thrum-6qmf.3.24. Stub returns an
// error so the Store interface is still satisfied for compile-time
// checks while the idempotency-match-key logic lands separately.
func (s *SQLStore) MintConditionForAgent(
	_ context.Context,
	_ string,
	_ json.RawMessage,
	_ []string,
	_ string,
	_ time.Time,
) (*Reminder, bool, error) {
	return nil, false, errors.New("MintConditionForAgent not yet implemented; see thrum-6qmf.3.24")
}

// Compile-time interface satisfaction check.
var _ Store = (*SQLStore)(nil)
