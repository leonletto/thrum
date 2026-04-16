package daemon

import (
	"context"
	"fmt"
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

// daemonAgentLister implements peercred.AgentLister by querying the
// agent_work_contexts table, which holds the (agent_id, worktree_path) mapping
// for every agent that has registered a session with the daemon.
type daemonAgentLister struct {
	st *state.State
}

// ListAgentWorktrees returns one entry per distinct (agent_id, worktree_path)
// pair from agent_work_contexts, filtered to non-empty worktree paths.
//
// A 2-second context timeout is applied because this query runs on every
// inbound unix-socket RPC call, and a slow or wedged DB must not stall the
// accept loop.
func (l *daemonAgentLister) ListAgentWorktrees() ([]peercred.AgentWorktree, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	l.st.RLock()
	defer l.st.RUnlock()

	query := `SELECT DISTINCT agent_id, worktree_path
	          FROM agent_work_contexts
	          WHERE worktree_path IS NOT NULL AND worktree_path != ''`
	rows, err := l.st.DB().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("peercred lister: query agent_work_contexts: %w", err)
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
