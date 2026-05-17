// Package state's agent_lifecycle.go provides the Go-level read/write API
// over the agent_lifecycle_events SQLite table (canonical-ref §3.4).
//
// The table is the append-only journal that powers B-B1's auto-respawn
// loop guard (the "3 respawn_fired events within window?" predicate),
// crash-detection observability, and operator-ack flows. Writes are
// idempotent only at the AUTOINCREMENT id level — callers are expected
// to enforce semantic dedup at the event-source layer.
package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
)

// AgentLifecycleEventKind pins the canonical event vocabulary per
// substrate-canonical-reference.md §3.4. Any other value passed to
// Append will be persisted as-is — the SQL CHECK constraint only
// guards detection_method, not event_kind.
type AgentLifecycleEventKind string

const (
	EventRespawnFired            AgentLifecycleEventKind = "respawn_fired"
	EventRespawnSkippedLoopguard AgentLifecycleEventKind = "respawn_skipped_loopguard"
	EventCrashDetected           AgentLifecycleEventKind = "crash_detected"
	EventStateMdParseFailed      AgentLifecycleEventKind = "state_md_parse_failed"
	EventStateMdAckCleared       AgentLifecycleEventKind = "state_md_ack_cleared"
	EventRespawnAckCleared       AgentLifecycleEventKind = "respawn_ack_cleared"
)

// DetectionMethod is the canonical-vocabulary value persisted in the
// agent_lifecycle_events.detection_method column. Empty string maps to
// SQL NULL (the column is nullable for event_kinds that carry no
// detection_method, e.g. operator ack events).
type DetectionMethod string

const (
	DetectionHealthCheckTick       DetectionMethod = "health_check_tick"
	DetectionRestartReconciliation DetectionMethod = "restart_reconciliation"
	DetectionRPCObservation        DetectionMethod = "rpc_observation"
)

// AgentLifecycleEvent is the Go-level view of one row in
// agent_lifecycle_events. Details is opaque JSON the caller controls;
// nil is persisted as the SQL NULL string "null" so JSON consumers
// always get valid JSON.
type AgentLifecycleEvent struct {
	ID              int64
	AgentName       string
	EventKind       AgentLifecycleEventKind
	EventTime       time.Time
	DetectionMethod DetectionMethod
	Reason          string
	Details         json.RawMessage
}

// AgentLifecycleStore is the read/write API over the
// agent_lifecycle_events table.
//
// All implementations are concurrency-safe at the *safedb.DB level
// (SQLite serializes per-connection writes).
type AgentLifecycleStore interface {
	// Append inserts a new row and returns its AUTOINCREMENT id. No
	// semantic dedup; callers enforce that.
	Append(ctx context.Context, e AgentLifecycleEvent) (int64, error)

	// ListByAgent returns the most-recent `limit` events for agentName
	// ordered by event_time DESC. limit <= 0 means "no cap".
	ListByAgent(ctx context.Context, agentName string, limit int) ([]AgentLifecycleEvent, error)

	// LoopGuardCount returns the number of events with (agent_name,
	// event_kind) matching, with event_time in the half-open interval
	// (now-windowSeconds, now]. Used by the B-B1 auto-respawn guard.
	LoopGuardCount(ctx context.Context, agentName string, kind AgentLifecycleEventKind, windowSeconds int) (int, error)

	// PruneOlderThan deletes every row with event_time < cutoff.
	// Returns the number of rows deleted. Used by the
	// internal.agent_lifecycle_cleanup daily handler.
	PruneOlderThan(ctx context.Context, cutoff time.Time) (rowsDeleted int64, err error)
}

type agentLifecycleStore struct {
	db *safedb.DB
}

// NewAgentLifecycleStore constructs an AgentLifecycleStore backed by db.
func NewAgentLifecycleStore(db *safedb.DB) AgentLifecycleStore {
	return &agentLifecycleStore{db: db}
}

// nullString converts an empty string to sql.NullString{Valid: false}
// so the column stores SQL NULL rather than empty-string. The CHECK
// constraint on detection_method requires this — passing "" would fail
// the IN-list guard.
func nullString(s string) sql.NullString {
	return sql.NullString{Valid: s != "", String: s}
}

func (s *agentLifecycleStore) Append(ctx context.Context, e AgentLifecycleEvent) (int64, error) {
	detailsJSON := e.Details
	if len(detailsJSON) == 0 {
		detailsJSON = json.RawMessage("null")
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_lifecycle_events
			(agent_name, event_kind, event_time, detection_method, reason, details)
		VALUES (?, ?, ?, ?, ?, ?)
	`, e.AgentName, string(e.EventKind), e.EventTime.Unix(),
		nullString(string(e.DetectionMethod)),
		nullString(e.Reason),
		string(detailsJSON))
	if err != nil {
		return 0, fmt.Errorf("insert agent_lifecycle_event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

func (s *agentLifecycleStore) ListByAgent(ctx context.Context, agentName string, limit int) ([]AgentLifecycleEvent, error) {
	query := `
		SELECT id, agent_name, event_kind, event_time, detection_method, reason, details
		  FROM agent_lifecycle_events
		 WHERE agent_name = ?
	  ORDER BY event_time DESC, id DESC`
	args := []any{agentName}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list agent_lifecycle_events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]AgentLifecycleEvent, 0)
	for rows.Next() {
		var (
			ev       AgentLifecycleEvent
			ts       int64
			kind     string
			detMthd  sql.NullString
			reason   sql.NullString
			detsText sql.NullString
		)
		if err := rows.Scan(&ev.ID, &ev.AgentName, &kind, &ts, &detMthd, &reason, &detsText); err != nil {
			return nil, fmt.Errorf("scan agent_lifecycle_event: %w", err)
		}
		ev.EventKind = AgentLifecycleEventKind(kind)
		ev.EventTime = time.Unix(ts, 0).UTC()
		if detMthd.Valid {
			ev.DetectionMethod = DetectionMethod(detMthd.String)
		}
		if reason.Valid {
			ev.Reason = reason.String
		}
		if detsText.Valid && detsText.String != "" && detsText.String != "null" {
			ev.Details = json.RawMessage(detsText.String)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent_lifecycle_events: %w", err)
	}
	return out, nil
}

func (s *agentLifecycleStore) LoopGuardCount(ctx context.Context, agentName string, kind AgentLifecycleEventKind, windowSeconds int) (int, error) {
	now := time.Now().Unix()
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM agent_lifecycle_events
		 WHERE agent_name = ?
		   AND event_kind = ?
		   AND event_time > ? - ?
		   AND event_time <= ?
	`, agentName, string(kind), now, windowSeconds, now).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("loop-guard count: %w", err)
	}
	return count, nil
}

func (s *agentLifecycleStore) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM agent_lifecycle_events WHERE event_time < ?`, cutoff.Unix())
	if err != nil {
		return 0, fmt.Errorf("prune agent_lifecycle_events: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return rows, nil
}
