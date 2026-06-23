package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/process"
	ttmux "github.com/leonletto/thrum/internal/tmux"
)

// AgentLookupRequest is the params for the agent.lookup RPC.
type AgentLookupRequest struct {
	Name string `json:"name"`
}

// AgentLookupResponse is the response from agent.lookup.
// Member is nil when no agent matches Name (a legitimate negative answer,
// not an error). The struct mirrors the TeamMember fields that the
// `send.recipient-stale` hint pipeline actually reads, so callers do not
// need to walk the whole team list to answer a single-name query.
type AgentLookupResponse struct {
	Member *TeamMember `json:"member"`
}

// AgentLookupHandler handles the agent.lookup RPC method.
//
// agent.lookup is a single-agent variant of team.list scoped to the
// callers actually used (CLI hint pipeline at sendHints). Compared to
// team.list it:
//   - issues one SQL SELECT (no per-agent mention-count fan-out, no
//     broadcast/group queries),
//   - reads at most one identity file (no full walk over
//     .thrum/identities/),
//   - skips Phase 2 dead-agent self-heal entirely (callers do not need it
//     and would otherwise drag the same hot-path emit cost the team.list
//     handler bears under burst).
//
// Used by sendHints (thrum-1nkt.4) so `thrum send` does not need to fire
// a team.list RPC just to look up the recipient's last_seen + is_local +
// tmux_session for the recipient-stale hint.
type AgentLookupHandler struct {
	state *state.State
}

// NewAgentLookupHandler creates a new agent.lookup handler.
func NewAgentLookupHandler(s *state.State) *AgentLookupHandler {
	return &AgentLookupHandler{state: s}
}

// HandleLookup handles the agent.lookup RPC method.
func (h *AgentLookupHandler) HandleLookup(ctx context.Context, params json.RawMessage) (any, error) {
	var req AgentLookupRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	h.state.RLock()
	defer h.state.RUnlock()

	// Single-row variant of buildTeamListLocked Query 1. Same column
	// projection so the resulting TeamMember matches what team.list would
	// have returned for this agent, minus the Phase-2 self-heal rewrite.
	const query = `SELECT
		a.agent_id, a.role, a.module, a.display, a.hostname, a.origin_daemon, a.agent_pid,
		s.session_id, s.started_at, s.last_seen_at,
		wc.branch,
		COALESCE(NULLIF(wc.worktree_path, ''), (
			SELECT ref_value FROM session_refs
			WHERE session_id = s.session_id AND ref_type = 'worktree'
			ORDER BY added_at DESC
			LIMIT 1
		)) AS worktree_path,
		wc.intent, wc.current_task
	FROM agents a
	LEFT JOIN sessions s ON s.agent_id = a.agent_id AND s.ended_at IS NULL
	LEFT JOIN agent_work_contexts wc ON wc.session_id = s.session_id
	WHERE a.agent_id = ?
	LIMIT 1`

	row := h.state.DB().QueryRowContext(ctx, query, req.Name)

	var m TeamMember
	var display, hostname, originDaemon sql.NullString
	var sessionID, sessionStart, lastSeen sql.NullString
	var branch, worktreePath, intent, currentTask sql.NullString

	if err := row.Scan(
		&m.AgentID, &m.Role, &m.Module, &display, &hostname, &originDaemon, &m.AgentPID,
		&sessionID, &sessionStart, &lastSeen,
		&branch, &worktreePath, &intent, &currentTask,
	); err != nil {
		if err == sql.ErrNoRows {
			return &AgentLookupResponse{Member: nil}, nil
		}
		return nil, fmt.Errorf("query agent: %w", err)
	}

	if display.Valid {
		m.Display = display.String
	}
	if hostname.Valid {
		m.Hostname = hostname.String
	}
	if originDaemon.Valid {
		m.OriginDaemon = originDaemon.String
	}
	if sessionID.Valid {
		m.SessionID = sessionID.String
		m.Status = "active"
	} else {
		m.Status = "offline"
	}
	if sessionStart.Valid {
		m.SessionStart = sessionStart.String
	}
	if lastSeen.Valid {
		m.LastSeen = lastSeen.String
	}
	if branch.Valid {
		m.Branch = branch.String
	}
	if worktreePath.Valid {
		m.WorktreePath = worktreePath.String
	}
	if intent.Valid {
		m.Intent = intent.String
	}
	if currentTask.Valid {
		m.CurrentTask = currentTask.String
	}

	localDaemonID := h.state.DaemonID()
	m.IsLocal = m.OriginDaemon == "" || m.OriginDaemon == localDaemonID

	// Identity-file enrichment: load the single agent's file directly
	// instead of walking the whole identities dir. The os.ReadFile guard
	// below handles the "no identities dir" / "no file for this agent"
	// case implicitly — both surface as errors that drop us out of the
	// enrichment block with the SQL-derived TeamMember unchanged.
	path := filepath.Join(identitiesDirFor(h.state.RepoPath()), m.AgentID+".json")
	if data, err := os.ReadFile(path); err == nil { // #nosec G304 -- path scoped to .thrum/identities/<agentID>.json
		var idFile config.IdentityFile
		if jsonErr := json.Unmarshal(data, &idFile); jsonErr == nil {
			m.Runtime = idFile.Runtime
			m.TmuxSession = idFile.TmuxSession
			m.Reserved = idFile.Reserved

			switch {
			case idFile.TmuxSession == "":
				m.TmuxState = ""
			case !ttmux.HasSession(parseSessionName(idFile.TmuxSession)):
				m.TmuxState = "dead"
			case m.AgentPID > 0 && !process.IsRunning(m.AgentPID):
				m.TmuxState = "stale"
			default:
				m.TmuxState = "alive"
			}
		}
	}

	return &AgentLookupResponse{Member: &m}, nil
}
