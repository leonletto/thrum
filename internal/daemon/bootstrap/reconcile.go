// Package bootstrap reconciles persistent identity files on disk with the
// daemon's runtime auth state at startup. After daemon restart, the
// session_refs table can be missing rows for agents whose .thrum/identities/
// files still exist on disk; reconcile inserts the missing rows so write
// RPCs from those worktrees succeed without re-running thrum quickstart.
//
// Local-only by design: writes go through safedb directly, NEVER through
// state.WriteEvent. No JSONL events, no cross-machine sync. See spec at
// dev-docs/specs/2026-05-04-identity-reconcile-on-boot-design.md.
package bootstrap

import (
	"context"
	// database/sql is imported for *sql.Tx — the type returned by
	// safedb.DB.BeginTx. safedb has no Tx wrapper; this is the safedb
	// boundary, not a Rule 1 violation.
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// hasActiveSessionRef reports whether (agent_id, worktree) already has an
// active session_ref. Used by the auth pass to skip identities that already
// resolve via peercred.
func hasActiveSessionRef(ctx context.Context, tx *sql.Tx, agentID, worktree string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(
            SELECT 1 FROM session_refs sr
              JOIN sessions s ON s.session_id = sr.session_id
             WHERE s.agent_id = ? AND sr.ref_type = 'worktree'
               AND sr.ref_value = ? AND s.ended_at IS NULL)`,
		agentID, worktree).Scan(&exists)
	return exists, err
}

// Stats reports per-pass counts. Returned by Reconcile for observability.
type Stats struct {
	Scanned              int
	SessionsCreated      int
	RefsCreated          int
	TmuxBindingsRestored int
	Errors               int
}

// Deps groups the inputs required by Reconcile. Splitting these out makes
// the function fully testable without spinning up a full daemon.
//
// IMPORTANT: ThrumDir MUST come from the function-local thrumDir in
// daemonRun, NEVER from os.Getenv("THRUM_HOME") or paths.EffectiveRepoPath.
// The latter is the v0.10.1 regression hazard; this code path is in the
// THRUM_HOME blast radius and must not re-introduce it.
type Deps struct {
	State        *state.State     // SQL via state.DB() (*safedb.DB) ONLY
	ThrumDir     string           // function-local thrumDir from daemonRun
	TmuxHandler  *rpc.TmuxHandler // for the Tmux pass via RestoreBinding
	Now          func() time.Time
	NewSessionID func() string          // ulid.Make().String() in production
	TmuxAlive    func(name string) bool // production: ttmux.HasSession
	Log          *slog.Logger
}

// Reconcile is the boot-time pass. Walk identity files via
// rpc.AllIdentityDirs, and for each file:
//  1. Skip if worktree is not absolute (defensive — fixture has "test").
//  2. Auth pass: if (agent_id, worktree) lacks an active session_ref,
//     insert a new (sessions, session_refs) pair via safedb in a single
//     transaction with INSERT OR IGNORE.
//  3. Tmux pass: if identity has tmux_session AND the session is alive,
//     call deps.TmuxHandler.RestoreBinding to populate the in-memory
//     pane-nudge map.
//
// Per-identity errors are logged at WARN, increment stats.Errors, and the
// loop continues. Returns a non-nil error only on catastrophic failure
// (e.g. ctx cancelled).
func Reconcile(ctx context.Context, deps Deps) (Stats, error) {
	var stats Stats
	if deps.Log == nil {
		deps.Log = slog.Default()
	}

	for _, dir := range rpc.AllIdentityDirs(ctx, deps.ThrumDir) {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			// Missing dir is fine (some worktrees have no .thrum/identities/).
			continue
		}
		for _, de := range entries {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			if de.IsDir() || filepath.Ext(de.Name()) != ".json" {
				continue
			}
			stats.Scanned++
			path := filepath.Join(dir, de.Name())
			data, err := os.ReadFile(path) // #nosec G304 -- internal identity file under known thrumDir, mirrors identity_scan.go:83
			if err != nil {
				deps.Log.Warn("reconcile: read identity", "path", path, "err", err)
				stats.Errors++
				continue
			}
			var idFile config.IdentityFile
			if err := json.Unmarshal(data, &idFile); err != nil {
				deps.Log.Warn("reconcile: parse identity", "path", path, "err", err)
				stats.Errors++
				continue
			}
			if idFile.Agent.Name == "" || idFile.Worktree == "" {
				deps.Log.Warn("reconcile: malformed identity (missing agent/worktree)", "path", path)
				stats.Errors++
				continue
			}
			if !filepath.IsAbs(idFile.Worktree) {
				deps.Log.Warn("reconcile: skipping non-absolute worktree",
					"agent", idFile.Agent.Name, "worktree", idFile.Worktree, "path", path)
				stats.Errors++
				continue
			}

			// Auth pass — single transaction per identity.
			authErr := func() error {
				tx, err := deps.State.DB().BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				committed := false
				defer func() {
					if !committed {
						_ = tx.Rollback()
					}
				}()

				has, err := hasActiveSessionRef(ctx, tx, idFile.Agent.Name, idFile.Worktree)
				if err != nil {
					return err
				}
				if has {
					return nil // no-op, already active
				}

				sessionID := deps.NewSessionID()
				now := deps.Now().UTC().Format(time.RFC3339Nano)
				// sessions schema requires session_id, agent_id, started_at,
				// last_seen_at as NOT NULL. Match projector.go:519 shape.
				if _, err := tx.ExecContext(ctx,
					`INSERT OR IGNORE INTO sessions(session_id, agent_id, started_at, last_seen_at)
                     VALUES (?, ?, ?, ?)`,
					sessionID, idFile.Agent.Name, now, now); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx,
					`INSERT OR IGNORE INTO session_refs(session_id, ref_type, ref_value, added_at)
                     VALUES (?, 'worktree', ?, ?)`,
					sessionID, idFile.Worktree, now); err != nil {
					return err
				}
				if err := tx.Commit(); err != nil {
					return err
				}
				committed = true
				// Only reached when the IIFE returned nil, i.e. the transaction committed.
				stats.SessionsCreated++
				stats.RefsCreated++
				return nil
			}()
			if authErr != nil {
				deps.Log.Warn("reconcile: auth pass failed",
					"agent", idFile.Agent.Name, "worktree", idFile.Worktree, "err", authErr)
				stats.Errors++
			}

			// Tmux pass — restore in-memory pane-nudge binding for live sessions.
			if idFile.TmuxSession != "" && deps.TmuxHandler != nil &&
				deps.TmuxAlive != nil && deps.TmuxAlive(idFile.TmuxSession) {
				deps.TmuxHandler.RestoreBinding(idFile.TmuxSession, idFile.Worktree)
				stats.TmuxBindingsRestored++
			}
		}
	}
	return stats, nil
}
