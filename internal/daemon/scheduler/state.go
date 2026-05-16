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

// UpsertState writes a state row, creating-or-updating on job_id conflict.
// The whole row is rewritten on every call; callers are responsible for
// passing the post-transition values (consecutive_failures already
// incremented, etc.).
func (s *StateStore) UpsertState(ctx context.Context, r *StateRow) error {
	_, err := s.db.ExecContext(ctx, `
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
			updated_at            = excluded.updated_at
	`,
		r.JobID, r.Generation, string(r.CurrentState),
		nullStr(r.CurrentStage), nullTime(r.StageEnteredAt),
		nullStr(r.LastRunID), nullTime(r.LastFiredAt),
		nullTime(r.LastCompletedAt), nullStr(string(r.LastCompletionState)),
		nullStr(r.LastError), nullTime(r.NextScheduledAt),
		r.ConsecutiveFailures, boolToInt(r.EscalationSent),
		r.TotalRuns, r.CreatedAt.Unix(), r.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("upsert state %q: %w", r.JobID, err)
	}
	return nil
}

// GetState reads one state row by job_id, or returns ErrJobNotFound.
func (s *StateStore) GetState(ctx context.Context, jobID string) (*StateRow, error) {
	var (
		r                                                              StateRow
		currentState                                                   string
		currentStage, lastRunID, lastError, lastCompletionState        sql.NullString
		stageEnteredAt, lastFiredAt, lastCompletedAt, nextScheduledAt  sql.NullInt64
		escalationSent                                                 int
		createdAt, updatedAt                                           int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT job_id, job_generation, current_state, current_stage,
		       stage_entered_at, last_run_id, last_fired_at,
		       last_completed_at, last_completion_state, last_error,
		       next_scheduled_at, consecutive_failures, escalation_sent,
		       total_runs, created_at, updated_at
		  FROM scheduler_job_state WHERE job_id = ?
	`, jobID).Scan(
		&r.JobID, &r.Generation, &currentState, &currentStage,
		&stageEnteredAt, &lastRunID, &lastFiredAt,
		&lastCompletedAt, &lastCompletionState, &lastError,
		&nextScheduledAt, &r.ConsecutiveFailures, &escalationSent,
		&r.TotalRuns, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get state %q: %w", jobID, err)
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

// nullStr returns nil for empty strings so SQLite stores NULL rather than ''.
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
