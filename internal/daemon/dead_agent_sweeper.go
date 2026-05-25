// internal/daemon/dead_agent_sweeper.go
//
// thrum-1nkt.6: background sweeper that emits agent.session.end events
// for active agents whose PID is no longer running. Supersedes the
// in-line Phase 2 self-heal that team.list HandleList used to perform
// on every request: team.list is now pure-read; the sweeper is the
// authoritative writer of dead-agent session.end events.
//
// Detection logic mirrors team.go's Phase 1 collection (RLock query +
// PID liveness check + identity-file PID cross-check + local-origin
// guard) so behavior is unchanged from the caller's perspective; only
// the WRITE side moves off the read RPC's hot path. Pattern mirrors
// internal/daemon/inbox/janitor.go: own goroutine, own ticker, no
// concurrent invocations.
package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/process"
	"github.com/leonletto/thrum/internal/types"
)

// DefaultDeadAgentSweepInterval is the cadence for the dead-agent
// self-heal sweep. Smaller than the inbox janitor's hourly cadence
// because dead-agent staleness shows up directly in `thrum team`
// output and operators expect it to converge in seconds, not hours.
const DefaultDeadAgentSweepInterval = 10 * time.Second

// DeadAgentSweeper periodically scans the agents table for active
// agents whose recorded PID is no longer running and emits
// agent.session.end events for them. Replaces the per-team.list
// Phase 2 self-heal (see internal/daemon/rpc/team.go).
type DeadAgentSweeper struct {
	state    *state.State
	thrumDir string
	interval time.Duration
}

// NewDeadAgentSweeper constructs a sweeper. thrumDir is optional;
// when non-empty the sweeper performs the identity-file PID
// cross-check (matches the team.list Phase 1 file-PID guard).
func NewDeadAgentSweeper(s *state.State, thrumDir string) *DeadAgentSweeper {
	return &DeadAgentSweeper{
		state:    s,
		thrumDir: thrumDir,
		interval: DefaultDeadAgentSweepInterval,
	}
}

// SetInterval overrides the default cadence (for tests).
func (sw *DeadAgentSweeper) SetInterval(d time.Duration) {
	sw.interval = d
}

// Start blocks until the context is canceled, running Sweep once
// immediately and then on every tick.
func (sw *DeadAgentSweeper) Start(ctx context.Context) {
	log.Printf("dead_agent_sweeper: starting with interval=%s", sw.interval)
	sw.Sweep(ctx)
	ticker := time.NewTicker(sw.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("dead_agent_sweeper: stopping")
			return
		case <-ticker.C:
			sw.Sweep(ctx)
		}
	}
}

// deadAgentRow is the per-row buffer for the sweeper's Phase-1 SELECT.
type deadAgentRow struct {
	AgentID      string
	AgentPID     int
	SessionID    string
	OriginDaemon string
}

// Sweep performs one detection-and-emit pass. Safe to call manually
// from tests. Emits session.end at most once per dead-active session
// per call; subsequent calls observe the now-offline session and
// skip it.
func (sw *DeadAgentSweeper) Sweep(ctx context.Context) {
	candidates := sw.collectDeadAgents(ctx)
	if len(candidates) == 0 {
		return
	}
	for _, d := range candidates {
		if emitErr := sw.emitSessionEnd(ctx, d.SessionID); emitErr != nil {
			log.Printf("dead_agent_sweeper: emit session.end failed: agent=%s session=%s err=%v",
				d.AgentID, d.SessionID, emitErr)
			continue
		}
		log.Printf("dead_agent_sweeper: marked dead agent offline: agent=%s pid=%d",
			d.AgentID, d.AgentPID)
	}
}

// collectDeadAgents queries for active agents whose PID is dead. The
// query + filter set mirrors team.go HandleList Phase 1 so the two
// agree on what counts as "dead-active". Locking discipline: RLock
// held only during the query + scan, released before emit.
func (sw *DeadAgentSweeper) collectDeadAgents(ctx context.Context) []deadAgentRow {
	sw.state.RLock()
	defer sw.state.RUnlock()

	const query = `SELECT a.agent_id, a.agent_pid, COALESCE(a.origin_daemon, ''), s.session_id
		FROM agents a
		JOIN sessions s ON s.agent_id = a.agent_id AND s.ended_at IS NULL
		WHERE a.agent_pid > 0`

	rows, err := sw.state.DB().QueryContext(ctx, query)
	if err != nil {
		log.Printf("dead_agent_sweeper: query failed: %v", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	localDaemonID := sw.state.DaemonID()

	// Load identity files once per sweep so the file-PID cross-check
	// does not hit the disk per row. nil when thrumDir is empty (test
	// path) — the cross-check is then skipped, matching the team.go
	// `if h.thrumDir != ""` gate.
	idFiles := sw.loadIdentityFiles()

	var dead []deadAgentRow
	for rows.Next() {
		var d deadAgentRow
		if scanErr := rows.Scan(&d.AgentID, &d.AgentPID, &d.OriginDaemon, &d.SessionID); scanErr != nil {
			log.Printf("dead_agent_sweeper: row scan failed: %v", scanErr)
			continue
		}

		// Match team.go Phase 1 filter set exactly.
		if d.AgentPID <= 0 || d.SessionID == "" {
			continue
		}
		if process.IsRunning(d.AgentPID) {
			continue
		}
		// Skip cross-daemon agents (their PID lives on a remote host;
		// local IsRunning is meaningless). See thrum-pxz.14.
		if d.OriginDaemon != "" && d.OriginDaemon != localDaemonID {
			continue
		}
		// File-PID cross-check (thrum-pxz.14 Fix B): if the identity
		// file reports a live PID different from the DB's stored PID,
		// the DB is stale but the agent is actually alive.
		if idFile, ok := idFiles[d.AgentID]; ok && idFile != nil {
			if idFile.AgentPID > 0 && idFile.AgentPID != d.AgentPID && process.IsRunning(idFile.AgentPID) {
				log.Printf("dead_agent_sweeper: stale DB PID but identity file reports live PID — skipping: agent=%s db_pid=%d file_pid=%d",
					d.AgentID, d.AgentPID, idFile.AgentPID)
				continue
			}
		}

		dead = append(dead, d)
	}
	if err := rows.Err(); err != nil {
		log.Printf("dead_agent_sweeper: row iteration: %v", err)
	}
	return dead
}

// loadIdentityFiles reads .thrum/identities/*.json and returns a
// per-agent map. Returns nil when thrumDir is empty (test path).
func (sw *DeadAgentSweeper) loadIdentityFiles() map[string]*config.IdentityFile {
	if sw.thrumDir == "" {
		return nil
	}
	dir := filepath.Join(sw.thrumDir, "identities")
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Missing dir is a benign condition during early bootstrap.
		return nil
	}
	out := make(map[string]*config.IdentityFile, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- path scoped to .thrum/identities/
		if readErr != nil {
			continue
		}
		var idFile config.IdentityFile
		if err := json.Unmarshal(data, &idFile); err != nil {
			continue
		}
		out[idFile.Agent.Name] = &idFile
	}
	return out
}

// emitSessionEnd writes an agent.session.end event for the dead session
// via state.WriteEvent, then schedules the postCommit (walker+
// compactor sync) through state.GoPostCommit so this sweep iteration
// does not block on walker latency. Matches the team.go
// emitSessionEndForDeadAgent contract that .6 supersedes.
func (sw *DeadAgentSweeper) emitSessionEnd(ctx context.Context, sessionID string) error {
	event := types.AgentSessionEndEvent{
		Type:      "agent.session.end",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: sessionID,
		Reason:    "dead_pid",
	}
	sw.state.Lock()
	postCommit, err := sw.state.WriteEvent(ctx, event)
	sw.state.Unlock()
	if err != nil {
		return err
	}
	sw.state.GoPostCommit(postCommit)
	return nil
}

// DB returns the database handle for tests.
func (sw *DeadAgentSweeper) DB() *sql.DB {
	return sw.state.RawDB()
}
