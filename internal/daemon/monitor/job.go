package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
)

// Status represents a monitor's lifecycle state.
type Status string

const (
	StatusRunning Status = "running"
	StatusDead    Status = "dead"
	StatusStopped Status = "stopped"
)

// MonitorJob is the persisted specification and runtime state of a monitor.
type MonitorJob struct {
	ID              string
	Name            string
	Argv            []string
	MatchPattern    string
	Target          string
	Cwd             string
	Env             map[string]string
	DebounceSeconds int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Status          Status
	LastExitCode    *int
	LastExitAt      *time.Time
	PID             *int
}

// ErrNotFound is returned when a monitor lookup finds no row.
var ErrNotFound = errors.New("monitor not found")

// MonitorStore is the DB-backed persistence layer for MonitorJob records.
type MonitorStore struct {
	db *safedb.DB
}

// NewMonitorStore constructs a store over the given safedb handle.
func NewMonitorStore(db *safedb.DB) *MonitorStore {
	return &MonitorStore{db: db}
}

// Insert persists a new monitor job. Fails on duplicate ID or name.
func (s *MonitorStore) Insert(ctx context.Context, job *MonitorJob) error {
	argvJSON, err := json.Marshal(job.Argv)
	if err != nil {
		return fmt.Errorf("marshal argv: %w", err)
	}
	envJSON, err := json.Marshal(job.Env)
	if err != nil {
		return fmt.Errorf("marshal env: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO monitors (
			id, name, argv, match_pattern, target, cwd, env,
			debounce_seconds, created_at, updated_at, status,
			last_exit_code, last_exit_at, pid
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL)
	`,
		job.ID, job.Name, string(argvJSON), job.MatchPattern, job.Target,
		job.Cwd, string(envJSON), job.DebounceSeconds,
		job.CreatedAt.UTC().Format(time.RFC3339),
		job.UpdatedAt.UTC().Format(time.RFC3339),
		string(job.Status),
	)
	if err != nil {
		return fmt.Errorf("insert monitor: %w", err)
	}
	return nil
}

// Update replaces all mutable columns of a monitor row by ID.
func (s *MonitorStore) Update(ctx context.Context, job *MonitorJob) error {
	argvJSON, err := json.Marshal(job.Argv)
	if err != nil {
		return fmt.Errorf("marshal argv: %w", err)
	}
	envJSON, err := json.Marshal(job.Env)
	if err != nil {
		return fmt.Errorf("marshal env: %w", err)
	}

	var lastExitCode *int
	var lastExitAt *string
	var pid *int

	if job.LastExitCode != nil {
		lastExitCode = job.LastExitCode
	}
	if job.LastExitAt != nil {
		s := job.LastExitAt.UTC().Format(time.RFC3339)
		lastExitAt = &s
	}
	if job.PID != nil {
		pid = job.PID
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE monitors SET
			name = ?, argv = ?, match_pattern = ?, target = ?, cwd = ?, env = ?,
			debounce_seconds = ?, updated_at = ?, status = ?,
			last_exit_code = ?, last_exit_at = ?, pid = ?
		WHERE id = ?
	`,
		job.Name, string(argvJSON), job.MatchPattern, job.Target, job.Cwd, string(envJSON),
		job.DebounceSeconds, job.UpdatedAt.UTC().Format(time.RFC3339), string(job.Status),
		lastExitCode, lastExitAt, pid,
		job.ID,
	)
	if err != nil {
		return fmt.Errorf("update monitor: %w", err)
	}
	return nil
}

// Delete permanently removes a monitor row.
func (s *MonitorStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM monitors WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete monitor: %w", err)
	}
	return nil
}

// GetByID fetches a single monitor by its ID. Returns ErrNotFound if missing.
func (s *MonitorStore) GetByID(ctx context.Context, id string) (*MonitorJob, error) {
	return s.scanOne(ctx, `WHERE id = ?`, id)
}

// GetByName fetches a single monitor by its user-chosen name.
func (s *MonitorStore) GetByName(ctx context.Context, name string) (*MonitorJob, error) {
	return s.scanOne(ctx, `WHERE name = ?`, name)
}

// ListByStatus returns all monitors in the given status.
func (s *MonitorStore) ListByStatus(ctx context.Context, status Status) ([]*MonitorJob, error) {
	return s.scanMany(ctx, `WHERE status = ?`, string(status))
}

// ListAll returns every monitor row, regardless of status.
func (s *MonitorStore) ListAll(ctx context.Context) ([]*MonitorJob, error) {
	return s.scanMany(ctx, ``)
}

// MarkDead updates a monitor to status=dead and records exit metadata.
func (s *MonitorStore) MarkDead(ctx context.Context, id string, exitCode int, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE monitors
		SET status = ?, last_exit_code = ?, last_exit_at = ?, updated_at = ?, pid = NULL
		WHERE id = ?
	`, string(StatusDead), exitCode, at.UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("mark dead: %w", err)
	}
	return nil
}

const selectBase = `
	SELECT id, name, argv, match_pattern, target, cwd, env,
		debounce_seconds, created_at, updated_at, status,
		last_exit_code, last_exit_at, pid
	FROM monitors `

func (s *MonitorStore) scanOne(ctx context.Context, where string, args ...any) (*MonitorJob, error) {
	row := s.db.QueryRowContext(ctx, selectBase+where, args...)
	job, err := scanJob(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get monitor: %w", err)
	}
	return job, nil
}

func (s *MonitorStore) scanMany(ctx context.Context, where string, args ...any) ([]*MonitorJob, error) {
	rows, err := s.db.QueryContext(ctx, selectBase+where+" ORDER BY created_at ASC", args...)
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var jobs []*MonitorJob
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan monitor: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate monitors: %w", err)
	}
	return jobs, nil
}

// scanner is implemented by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanJob(s scanner) (*MonitorJob, error) {
	var job MonitorJob
	var argvJSON, envJSON string
	var status string
	var createdAt, updatedAt string
	var lastExitCode sql.NullInt64
	var lastExitAt sql.NullString
	var pid sql.NullInt64

	err := s.Scan(
		&job.ID, &job.Name, &argvJSON, &job.MatchPattern, &job.Target, &job.Cwd, &envJSON,
		&job.DebounceSeconds, &createdAt, &updatedAt, &status,
		&lastExitCode, &lastExitAt, &pid,
	)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(argvJSON), &job.Argv); err != nil {
		return nil, fmt.Errorf("unmarshal argv: %w", err)
	}
	if err := json.Unmarshal([]byte(envJSON), &job.Env); err != nil {
		return nil, fmt.Errorf("unmarshal env: %w", err)
	}

	job.Status = Status(status)

	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	job.CreatedAt = t.UTC()

	t, err = time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	job.UpdatedAt = t.UTC()

	if lastExitCode.Valid {
		v := int(lastExitCode.Int64)
		job.LastExitCode = &v
	}
	if lastExitAt.Valid && lastExitAt.String != "" {
		t, err := time.Parse(time.RFC3339, lastExitAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse last_exit_at: %w", err)
		}
		utc := t.UTC()
		job.LastExitAt = &utc
	}
	if pid.Valid {
		v := int(pid.Int64)
		job.PID = &v
	}

	return &job, nil
}
