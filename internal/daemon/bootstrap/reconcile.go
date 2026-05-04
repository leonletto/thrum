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
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
)

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
	State        *state.State           // SQL via state.DB() (*safedb.DB) ONLY
	ThrumDir     string                 // function-local thrumDir from daemonRun
	TmuxHandler  *rpc.TmuxHandler       // for the Tmux pass via RestoreBinding
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
		entries, err := os.ReadDir(dir)
		if err != nil {
			// Missing dir is fine (some worktrees have no .thrum/identities/).
			continue
		}
		for _, de := range entries {
			if de.IsDir() || filepath.Ext(de.Name()) != ".json" {
				continue
			}
			stats.Scanned++
			path := filepath.Join(dir, de.Name())
			data, err := os.ReadFile(path)
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
			// Auth + Tmux passes arrive in later tasks.
		}
	}
	return stats, nil
}
