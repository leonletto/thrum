// Package scheduler implements the v0.11 unified scheduler primitive — the
// daemon-side substrate that consolidates today's independent ticker loops
// (event-loop, sweeper, reminder) plus future ones (scheduled_agent, nudge,
// email-poll, telemetry-poll, etc.) into one abstraction.
//
// Authoritative spec: dev-docs/specs/2026-05-15-thrum-agents-a-b1-design.md.
// Canonical reference (DDL, schedule formats, config keys, state vocab):
// dev-docs/thrum-agents/substrate-canonical-reference.md.
package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
)

// State is the cross-type job-state vocabulary shared by all handler types
// (canonical-ref §5; spec §6.1).
type State string

const (
	StateDisabled           State = "disabled"
	StateScheduled          State = "scheduled"
	StateDispatched         State = "dispatched"
	StateRunning            State = "running"
	StateCompleted          State = "completed"
	StateFailed             State = "failed"
	StateCancelled          State = "cancelled"
	StateOverBudget         State = "over_budget"
	StateOverlappingSkipped State = "overlapping_skipped"
)

// StateRow mirrors one row in scheduler_job_state (canonical-ref §3.2).
//
// NextScheduledAt is nil for one-shot terminal rows (@at / @once jobs
// post-completion) and for recurring jobs temporarily without a derivable
// next-tick. The reactor's tick-loop predicate is `next_scheduled_at IS NOT
// NULL`, so NULL excludes the row from firing.
type StateRow struct {
	JobID               string
	Generation          int
	CurrentState        State
	CurrentStage        string // empty when no stage set
	StageEnteredAt      *time.Time
	LastRunID           string
	LastFiredAt         *time.Time
	LastCompletedAt     *time.Time
	LastCompletionState State // empty for never-completed
	LastError           string
	NextScheduledAt     *time.Time
	ConsecutiveFailures int
	EscalationSent      bool
	TotalRuns           int
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ErrJobNotFound is returned by GetState when no row exists for the job_id.
var ErrJobNotFound = errors.New("scheduler: job not found")

// StateStore abstracts SQLite access for scheduler_job_state +
// scheduler_job_events. Backed by *safedb.DB per project rule
// feedback_safecmd_safedb (all SQL access goes through safedb; never raw
// *sql.DB).
type StateStore struct {
	db *safedb.DB
}

// NewStateStore constructs a StateStore backed by the given safedb handle.
func NewStateStore(db *safedb.DB) *StateStore {
	return &StateStore{db: db}
}

// DB returns the underlying safedb handle. Reserved for the E1.7 cleanup
// path which executes a bulk DELETE; prefer adding a typed helper method on
// StateStore from new call sites.
func (s *StateStore) DB() *safedb.DB { return s.db }

// upsertStateSQL is the INSERT-or-update statement used by both
// UpsertState (auto-commit) and UpsertStateAndEvent (transactional).
const upsertStateSQL = `
INSERT INTO scheduler_job_state (
	job_id, job_generation, current_state, current_stage,
	stage_entered_at, last_run_id, last_fired_at,
	last_completed_at, last_completion_state, last_error,
	next_scheduled_at, consecutive_failures, escalation_sent,
	total_runs, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(job_id) DO UPDATE SET
	job_generation        = excluded.job_generation,
	current_state         = excluded.current_state,
	current_stage         = excluded.current_stage,
	stage_entered_at      = excluded.stage_entered_at,
	last_run_id           = excluded.last_run_id,
	last_fired_at         = excluded.last_fired_at,
	last_completed_at     = excluded.last_completed_at,
	last_completion_state = excluded.last_completion_state,
	last_error            = excluded.last_error,
	next_scheduled_at     = excluded.next_scheduled_at,
	consecutive_failures  = excluded.consecutive_failures,
	escalation_sent       = excluded.escalation_sent,
	total_runs            = excluded.total_runs,
	updated_at            = excluded.updated_at`

// upsertStateArgs builds the SQL parameter list for upsertStateSQL.
func upsertStateArgs(r *StateRow) []any {
	return []any{
		r.JobID, r.Generation, string(r.CurrentState),
		nullStr(r.CurrentStage), nullTime(r.StageEnteredAt),
		nullStr(r.LastRunID), nullTime(r.LastFiredAt),
		nullTime(r.LastCompletedAt), nullStr(string(r.LastCompletionState)),
		nullStr(r.LastError), nullTime(r.NextScheduledAt),
		r.ConsecutiveFailures, boolToInt(r.EscalationSent),
		r.TotalRuns, r.CreatedAt.Unix(), r.UpdatedAt.Unix(),
	}
}

// UpsertState writes a state row, creating-or-updating on job_id conflict.
// The whole row is rewritten on every call; callers are responsible for
// passing the post-transition values (consecutive_failures already
// incremented, etc.).
func (s *StateStore) UpsertState(ctx context.Context, r *StateRow) error {
	_, err := s.db.ExecContext(ctx, upsertStateSQL, upsertStateArgs(r)...)
	if err != nil {
		return fmt.Errorf("upsert state %q: %w", r.JobID, err)
	}
	return nil
}

// UpsertStateAndEvent writes the state row AND an event-log row in a
// single SQLite transaction per spec §8.4.2. The two writes commit
// atomically — a daemon crash between them cannot leave the state row
// updated while the audit-log row is missing.
//
// Used by stateReporter.Transition and stateReporter.Stage so every
// per-run state transition has a matching event-log entry.
func (s *StateStore) UpsertStateAndEvent(ctx context.Context, r *StateRow, ev *Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for %q: %w", r.JobID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, upsertStateSQL, upsertStateArgs(r)...); err != nil {
		return fmt.Errorf("upsert state %q in tx: %w", r.JobID, err)
	}
	eventArgs, err := appendEventArgs(ev)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, appendEventSQL, eventArgs...); err != nil {
		return fmt.Errorf("append event for %q/%q in tx: %w", ev.JobID, ev.RunID, err)
	}
	return tx.Commit()
}

// stateColumns is the column list returned by every SELECT that fills a
// StateRow. Centralised so callers (GetState, NonTerminalAtBoot, future
// per-handler queries) cannot drift.
const stateColumns = `
		job_id, job_generation, current_state, current_stage,
		stage_entered_at, last_run_id, last_fired_at,
		last_completed_at, last_completion_state, last_error,
		next_scheduled_at, consecutive_failures, escalation_sent,
		total_runs, created_at, updated_at`

// rowScanner is the subset of *sql.Row / *sql.Rows that scanStateRow needs.
// GetState passes the former; NonTerminalAtBoot passes the latter.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanStateRow decodes one StateRow from a query result. Shared between
// GetState (single-row) and NonTerminalAtBoot (bulk-row iteration).
func scanStateRow(rs rowScanner) (*StateRow, error) {
	var (
		r                                                             StateRow
		currentState                                                  string
		currentStage, lastRunID, lastError, lastCompletionState       sql.NullString
		stageEnteredAt, lastFiredAt, lastCompletedAt, nextScheduledAt sql.NullInt64
		escalationSent                                                int
		createdAt, updatedAt                                          int64
	)
	if err := rs.Scan(
		&r.JobID, &r.Generation, &currentState, &currentStage,
		&stageEnteredAt, &lastRunID, &lastFiredAt,
		&lastCompletedAt, &lastCompletionState, &lastError,
		&nextScheduledAt, &r.ConsecutiveFailures, &escalationSent,
		&r.TotalRuns, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	r.CurrentState = State(currentState)
	r.CurrentStage = currentStage.String
	r.LastRunID = lastRunID.String
	r.LastError = lastError.String
	if lastCompletionState.Valid {
		r.LastCompletionState = State(lastCompletionState.String)
	}
	if stageEnteredAt.Valid {
		t := time.Unix(stageEnteredAt.Int64, 0)
		r.StageEnteredAt = &t
	}
	if lastFiredAt.Valid {
		t := time.Unix(lastFiredAt.Int64, 0)
		r.LastFiredAt = &t
	}
	if lastCompletedAt.Valid {
		t := time.Unix(lastCompletedAt.Int64, 0)
		r.LastCompletedAt = &t
	}
	if nextScheduledAt.Valid {
		t := time.Unix(nextScheduledAt.Int64, 0)
		r.NextScheduledAt = &t
	}
	r.EscalationSent = escalationSent != 0
	r.CreatedAt = time.Unix(createdAt, 0)
	r.UpdatedAt = time.Unix(updatedAt, 0)
	return &r, nil
}

// GetState reads one state row by job_id, or returns ErrJobNotFound.
func (s *StateStore) GetState(ctx context.Context, jobID string) (*StateRow, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+stateColumns+` FROM scheduler_job_state WHERE job_id = ?`,
		jobID)
	r, err := scanStateRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get state %q: %w", jobID, err)
	}
	return r, nil
}

// NonTerminalAtBoot returns every state row in scheduled / dispatched /
// running. E1.3's reconciliation walker calls this at daemon start to
// identify jobs that need recovery — the daemon crashed mid-flight so the
// row's last reactor-observed state can't be trusted as live.
func (s *StateStore) NonTerminalAtBoot(ctx context.Context) ([]*StateRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+stateColumns+`
		   FROM scheduler_job_state
		  WHERE current_state IN ('scheduled','dispatched','running')`)
	if err != nil {
		return nil, fmt.Errorf("non-terminal scan: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*StateRow
	for rows.Next() {
		r, err := scanStateRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan non-terminal row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate non-terminal rows: %w", err)
	}
	return out, nil
}

// nullStr returns nil for empty strings so SQLite stores NULL rather than ”.
// Keeps NULLability semantics consistent with PRAGMA-declared columns.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullTime returns nil for unset time pointers so SQLite stores NULL.
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Event mirrors one row in scheduler_job_events (canonical-ref §3.2). The
// table is append-only: every state transition appends a row carrying the
// run_id that produced it. Reads are paginated DESC by event_time for the
// future job.show / job.history RPCs.
type Event struct {
	ID        int64
	JobID     string
	RunID     string
	EventTime time.Time
	FromState State // empty on the first event of a run (column is NULLable)
	ToState   State
	Reason    string
	Details   map[string]any
}

// appendEventSQL is the INSERT used by both AppendEvent (auto-commit) and
// UpsertStateAndEvent (transactional).
const appendEventSQL = `
INSERT INTO scheduler_job_events
	(job_id, run_id, event_time, from_state, to_state, reason, details)
VALUES (?, ?, ?, ?, ?, ?, ?)`

// appendEventArgs builds the SQL parameter list for appendEventSQL.
// JSON-marshals the Details map; nil maps store SQL NULL.
func appendEventArgs(e *Event) ([]any, error) {
	var detailsJSON []byte
	if e.Details != nil {
		var err error
		detailsJSON, err = json.Marshal(e.Details)
		if err != nil {
			return nil, fmt.Errorf("marshal details for %q/%q: %w", e.JobID, e.RunID, err)
		}
	}
	return []any{
		e.JobID, e.RunID, e.EventTime.Unix(),
		nullStr(string(e.FromState)), string(e.ToState),
		nullStr(e.Reason), nullJSON(detailsJSON),
	}, nil
}

// AppendEvent inserts one row into scheduler_job_events.
func (s *StateStore) AppendEvent(ctx context.Context, e *Event) error {
	args, err := appendEventArgs(e)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, appendEventSQL, args...); err != nil {
		return fmt.Errorf("append event for %q/%q: %w", e.JobID, e.RunID, err)
	}
	return nil
}

// RecentEvents reads the most-recent `limit` events for a job, DESC by
// event_time (ties broken by id DESC).
func (s *StateStore) RecentEvents(ctx context.Context, jobID string, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, job_id, run_id, event_time, from_state, to_state, reason, details
		  FROM scheduler_job_events
		 WHERE job_id = ?
		 ORDER BY event_time DESC, id DESC
		 LIMIT ?
	`, jobID, limit)
	if err != nil {
		return nil, fmt.Errorf("recent events for %q: %w", jobID, err)
	}
	defer func() { _ = rows.Close() }()

	var events []Event
	for rows.Next() {
		var (
			e                          Event
			eventTime                  int64
			toState                    string
			fromState, reason, details sql.NullString
		)
		if err := rows.Scan(&e.ID, &e.JobID, &e.RunID, &eventTime,
			&fromState, &toState, &reason, &details); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		e.EventTime = time.Unix(eventTime, 0)
		e.FromState = State(fromState.String)
		e.ToState = State(toState)
		e.Reason = reason.String
		if details.Valid && details.String != "" {
			if err := json.Unmarshal([]byte(details.String), &e.Details); err != nil {
				return nil, fmt.Errorf("unmarshal details for event %d: %w", e.ID, err)
			}
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return events, nil
}

// nullJSON returns nil for empty byte slices so SQLite stores NULL rather
// than an empty string. Mirrors nullStr / nullTime for the JSON-encoded
// details column.
func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
