package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
)

// sqliteRegistry is the canonical AgentRegistry implementation backed
// by *safedb.DB per project rule feedback_safecmd_safedb. All SQL goes
// through the safedb wrapper; sql.NullString / sql.NullInt64 appear
// only at the column-encoding boundary.
type sqliteRegistry struct {
	db *safedb.DB
}

// NewSQLiteRegistry constructs an AgentRegistry over the agents table
// post-migration 26. Caller owns the *safedb.DB lifecycle.
func NewSQLiteRegistry(db *safedb.DB) AgentRegistry {
	return &sqliteRegistry{db: db}
}

const lookupQuery = `
	SELECT
		agent_id, kind, role, module, display, hostname,
		agent_pid, registered_at, origin_daemon, last_seen_at,
		mode, identity,
		auto_respawn_enabled, auto_respawn_disabled_at,
		state_md_parse_failed_at, last_pane_alive_at
	  FROM agents
	 WHERE agent_id = ?`

func (r *sqliteRegistry) Lookup(ctx context.Context, name string) (Agent, error) {
	var (
		a                       Agent
		lastSeenAt              sql.NullString
		autoRespawnEnabled      int
		autoRespawnDisabledUnix sql.NullInt64
		stateMdFailedUnix       sql.NullInt64
		lastPaneAliveUnix       sql.NullInt64
	)
	err := r.db.QueryRowContext(ctx, lookupQuery, name).Scan(
		&a.AgentID, &a.Kind, &a.Role, &a.Module, &a.Display, &a.Hostname,
		&a.AgentPID, &a.RegisteredAt, &a.OriginDaemon, &lastSeenAt,
		&a.Mode, &a.Identity,
		&autoRespawnEnabled, &autoRespawnDisabledUnix,
		&stateMdFailedUnix, &lastPaneAliveUnix,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Agent{}, fmt.Errorf("agent.Lookup(%q): %w", name, ErrAgentNotFound)
		}
		return Agent{}, fmt.Errorf("agent.Lookup(%q): %w", name, err)
	}

	a.AutoRespawnEnabled = autoRespawnEnabled != 0

	// last_seen_at is TEXT (existing column) — parse leniently. Empty
	// string and unparseable values both yield a nil pointer rather
	// than blocking the lookup; this matches the existing precedent in
	// internal/daemon/rpc/agent.go's read paths.
	if lastSeenAt.Valid && lastSeenAt.String != "" {
		if ts, parseErr := time.Parse(time.RFC3339, lastSeenAt.String); parseErr == nil {
			ts = ts.UTC()
			a.LastSeenAt = &ts
		}
	}
	if autoRespawnDisabledUnix.Valid {
		ts := time.Unix(autoRespawnDisabledUnix.Int64, 0).UTC()
		a.AutoRespawnDisabledAt = &ts
	}
	if stateMdFailedUnix.Valid {
		ts := time.Unix(stateMdFailedUnix.Int64, 0).UTC()
		a.StateMdParseFailedAt = &ts
	}
	if lastPaneAliveUnix.Valid {
		ts := time.Unix(lastPaneAliveUnix.Int64, 0).UTC()
		a.LastPaneAliveAt = &ts
	}

	return a, nil
}

// listAutoRespawnEnabledQuery yields the same column set as
// lookupQuery so the Scan logic mirrors. Filters apply the
// canonical gate predicate from spec §3.4 + canonical-ref §6.3:
// auto_respawn_enabled=1 AND no loop-guard trip AND no parse-
// failure banner. Result ordering is implementation-defined
// (callers must not depend on it).
const listAutoRespawnEnabledQuery = `
	SELECT
		agent_id, kind, role, module, display, hostname,
		agent_pid, registered_at, origin_daemon, last_seen_at,
		mode, identity,
		auto_respawn_enabled, auto_respawn_disabled_at,
		state_md_parse_failed_at, last_pane_alive_at
	  FROM agents
	 WHERE auto_respawn_enabled = 1
	   AND auto_respawn_disabled_at IS NULL
	   AND state_md_parse_failed_at IS NULL`

func (r *sqliteRegistry) ListAutoRespawnEnabled(ctx context.Context) ([]Agent, error) {
	rows, err := r.db.QueryContext(ctx, listAutoRespawnEnabledQuery)
	if err != nil {
		return nil, fmt.Errorf("agent.ListAutoRespawnEnabled: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var agents []Agent
	for rows.Next() {
		var (
			a                       Agent
			lastSeenAt              sql.NullString
			autoRespawnEnabled      int
			autoRespawnDisabledUnix sql.NullInt64
			stateMdFailedUnix       sql.NullInt64
			lastPaneAliveUnix       sql.NullInt64
		)
		if err := rows.Scan(
			&a.AgentID, &a.Kind, &a.Role, &a.Module, &a.Display, &a.Hostname,
			&a.AgentPID, &a.RegisteredAt, &a.OriginDaemon, &lastSeenAt,
			&a.Mode, &a.Identity,
			&autoRespawnEnabled, &autoRespawnDisabledUnix,
			&stateMdFailedUnix, &lastPaneAliveUnix,
		); err != nil {
			return nil, fmt.Errorf("agent.ListAutoRespawnEnabled scan: %w", err)
		}
		a.AutoRespawnEnabled = autoRespawnEnabled != 0
		if lastSeenAt.Valid && lastSeenAt.String != "" {
			if ts, parseErr := time.Parse(time.RFC3339, lastSeenAt.String); parseErr == nil {
				ts = ts.UTC()
				a.LastSeenAt = &ts
			}
		}
		if autoRespawnDisabledUnix.Valid {
			ts := time.Unix(autoRespawnDisabledUnix.Int64, 0).UTC()
			a.AutoRespawnDisabledAt = &ts
		}
		if stateMdFailedUnix.Valid {
			ts := time.Unix(stateMdFailedUnix.Int64, 0).UTC()
			a.StateMdParseFailedAt = &ts
		}
		if lastPaneAliveUnix.Valid {
			ts := time.Unix(lastPaneAliveUnix.Int64, 0).UTC()
			a.LastPaneAliveAt = &ts
		}
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agent.ListAutoRespawnEnabled iter: %w", err)
	}
	return agents, nil
}

// nullableTimestampSetter is the shared shape for "UPDATE agents SET
// <col> = ? WHERE agent_id = ?". Returning ErrAgentNotFound when no row
// matches lets callers distinguish "you tried to set state on an agent
// that doesn't exist" from "DB error".
func (r *sqliteRegistry) setNullableUnix(ctx context.Context, name, col string, val sql.NullInt64) error {
	// Column whitelist guards against SQL injection — we ONLY accept
	// the four known columns from this package's API surface.
	switch col {
	case "auto_respawn_disabled_at", "state_md_parse_failed_at":
		// OK
	default:
		return fmt.Errorf("agent.setNullableUnix: unsupported column %q", col)
	}
	// #nosec G201 -- col is validated against a closed set immediately above.
	query := "UPDATE agents SET " + col + " = ? WHERE agent_id = ?"
	res, err := r.db.ExecContext(ctx, query, val, name)
	if err != nil {
		return fmt.Errorf("agent.set %s(%q): %w", col, name, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("agent.set %s(%q) rows: %w", col, name, err)
	}
	if rows == 0 {
		return fmt.Errorf("agent.set %s(%q): %w", col, name, ErrAgentNotFound)
	}
	return nil
}

func (r *sqliteRegistry) SetAutoRespawnDisabledAt(ctx context.Context, name string, at time.Time) error {
	return r.setNullableUnix(ctx, name, "auto_respawn_disabled_at",
		sql.NullInt64{Int64: at.Unix(), Valid: true})
}

func (r *sqliteRegistry) ClearAutoRespawnDisabledAt(ctx context.Context, name string) error {
	return r.setNullableUnix(ctx, name, "auto_respawn_disabled_at",
		sql.NullInt64{Valid: false})
}

func (r *sqliteRegistry) SetStateMdParseFailedAt(ctx context.Context, name string, at time.Time) error {
	return r.setNullableUnix(ctx, name, "state_md_parse_failed_at",
		sql.NullInt64{Int64: at.Unix(), Valid: true})
}

func (r *sqliteRegistry) ClearStateMdParseFailedAt(ctx context.Context, name string) error {
	return r.setNullableUnix(ctx, name, "state_md_parse_failed_at",
		sql.NullInt64{Valid: false})
}
