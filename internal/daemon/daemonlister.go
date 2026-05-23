package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// NewDaemonAgentLister returns a peercred.AgentLister that enumerates registered
// agent worktrees from the daemon's state database.
//
// The lister is the bridge between the pure peercred package (which has no
// storage dependency) and the daemon's state. It is instantiated once during
// daemon boot and passed to peercred.NewResolver.
func NewDaemonAgentLister(st *state.State) peercred.AgentLister {
	return &daemonAgentLister{st: st}
}

// daemonAgentLister implements peercred.AgentLister by joining session_refs
// (where ref_type='worktree' is the canonical agent → worktree mapping) with
// the sessions table to filter to active sessions.
//
// Note on table choice (thrum-2x0p): the original sec.3 implementation queried
// agent_work_contexts.worktree_path. That table is sparsely populated — it's
// a git-state tracker (branch, file changes, intent) maintained by the
// heartbeat / setIntent paths, NOT a registration index. Most agents have
// rows in session_refs (populated unconditionally by every session.start) but
// not in agent_work_contexts. Session_refs is the right source.
//
// 03bc5d8 added a band-aid that seeds agent_work_contexts.worktree_path
// inside HandleStart for new sessions; that seeding remains in place for
// other consumers but the lister no longer depends on it.
type daemonAgentLister struct {
	st *state.State
}

// ListAgentWorktrees returns one entry per distinct (agent_id, worktree_path)
// pair from session_refs joined with sessions, scoped to active sessions
// (ended_at IS NULL) so a recently-ended session's stale worktree path can't
// shadow an active agent's resolution.
//
// A 2-second context timeout is applied because this query runs on every
// inbound unix-socket RPC call, and a slow or wedged DB must not stall the
// accept loop.
func (l *daemonAgentLister) ListAgentWorktrees() ([]peercred.AgentWorktree, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// No RLock: see ListAgentsInWorktree in state_query.go — taking RLock
	// here queues behind HandleRegister's outer Lock() during the concurrent
	// post-restart re-register burst (resolver.Resolve calls this lister on
	// every RPC, including agent.register itself), starving the queue past
	// the 10s CLI deadline. Serialization against writers comes from the
	// single-connection pool — NewState opens the DB with SetMaxOpenConns(1)
	// (see internal/schema/schema.go), so SQLite operations are linearised by
	// the pool regardless of Go-level locking.
	query := `SELECT DISTINCT s.agent_id, sr.ref_value
	          FROM session_refs sr
	          JOIN sessions s ON sr.session_id = s.session_id
	          WHERE sr.ref_type = 'worktree'
	            AND sr.ref_value IS NOT NULL
	            AND sr.ref_value != ''
	            AND s.ended_at IS NULL`
	rows, err := l.st.DB().QueryContext(ctx, query)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("peercred lister: query timed out after 2s", "sqlite_pool_contention_suspected", true)
		}
		return nil, fmt.Errorf("peercred lister: query session_refs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []peercred.AgentWorktree
	for rows.Next() {
		var agentID, worktree string
		if scanErr := rows.Scan(&agentID, &worktree); scanErr != nil {
			return nil, fmt.Errorf("peercred lister: scan row: %w", scanErr)
		}
		result = append(result, peercred.AgentWorktree{
			AgentID:  agentID,
			Worktree: worktree,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("peercred lister: iterate rows: %w", err)
	}
	return result, nil
}
