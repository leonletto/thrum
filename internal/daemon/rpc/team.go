package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
)

// TeamListRequest represents the request for team.list RPC.
type TeamListRequest struct {
	IncludeOffline bool `json:"include_offline,omitempty"`
}

// TeamListResponse represents the response from team.list RPC.
type TeamListResponse struct {
	Members        []TeamMember    `json:"members"`
	SharedMessages *SharedMessages `json:"shared_messages,omitempty"`
}

// SharedMessages contains team-wide message counts (broadcasts + groups).
type SharedMessages struct {
	BroadcastTotal int                 `json:"broadcast_total"`
	Groups         []GroupMessageCount `json:"groups,omitempty"`
}

// GroupMessageCount contains message counts for an agent group.
type GroupMessageCount struct {
	Name  string `json:"name"`
	Total int    `json:"total"`
}

// TeamMember represents a team member's full status.
type TeamMember struct {
	AgentID         string             `json:"agent_id"`
	Role            string             `json:"role"`
	Module          string             `json:"module"`
	Display         string             `json:"display,omitempty"`
	Hostname        string             `json:"hostname,omitempty"`
	WorktreePath    string             `json:"worktree_path,omitempty"`
	SessionID       string             `json:"session_id,omitempty"`
	SessionStart    string             `json:"session_start,omitempty"`
	LastSeen        string             `json:"last_seen,omitempty"`
	Intent          string             `json:"intent,omitempty"`
	CurrentTask     string             `json:"current_task,omitempty"`
	Branch          string             `json:"branch,omitempty"`
	UnmergedCommits int                `json:"unmerged_commits"`
	FileChanges     []types.FileChange `json:"file_changes,omitempty"`
	InboxTotal      int                `json:"inbox_total"`
	InboxUnread     int                `json:"inbox_unread"`
	Status          string             `json:"status"` // "active", "offline"
}

// TeamHandler handles team-related RPC methods.
type TeamHandler struct {
	state *state.State
}

// NewTeamHandler creates a new team handler.
func NewTeamHandler(state *state.State) *TeamHandler {
	return &TeamHandler{state: state}
}

// HandleList handles the team.list RPC method.
func (h *TeamHandler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	var req TeamListRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	h.state.RLock()
	defer h.state.RUnlock()

	// Query 1: Agents + sessions + work contexts
	query := `SELECT
		a.agent_id, a.role, a.module, a.display, a.hostname,
		s.session_id, s.started_at, s.last_seen_at,
		wc.branch, wc.worktree_path, wc.intent, wc.current_task,
		wc.unmerged_commits, wc.file_changes
	FROM agents a
	LEFT JOIN sessions s ON s.agent_id = a.agent_id AND s.ended_at IS NULL
	LEFT JOIN agent_work_contexts wc ON wc.session_id = s.session_id
	WHERE 1=1`

	if !req.IncludeOffline {
		query += " AND s.session_id IS NOT NULL"
	}

	query += " ORDER BY s.started_at DESC NULLS LAST"

	rows, err := h.state.DB().Query(query)
	if err != nil {
		return nil, fmt.Errorf("query team members: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var members []TeamMember
	memberIndex := make(map[string]int) // agent_id → index in members

	for rows.Next() {
		var m TeamMember
		var display, hostname sql.NullString
		var sessionID, sessionStart, lastSeen sql.NullString
		var branch, worktreePath, intent, currentTask sql.NullString
		var unmergedCommitsJSON, fileChangesJSON sql.NullString

		if err := rows.Scan(
			&m.AgentID, &m.Role, &m.Module, &display, &hostname,
			&sessionID, &sessionStart, &lastSeen,
			&branch, &worktreePath, &intent, &currentTask,
			&unmergedCommitsJSON, &fileChangesJSON,
		); err != nil {
			return nil, fmt.Errorf("scan team member: %w", err)
		}

		if display.Valid {
			m.Display = display.String
		}
		if hostname.Valid {
			m.Hostname = hostname.String
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

		// Unmarshal unmerged commits to get count
		if unmergedCommitsJSON.Valid && unmergedCommitsJSON.String != "" {
			var commits []json.RawMessage
			if err := json.Unmarshal([]byte(unmergedCommitsJSON.String), &commits); err == nil {
				m.UnmergedCommits = len(commits)
			}
		}

		// Unmarshal file changes
		if fileChangesJSON.Valid && fileChangesJSON.String != "" {
			if err := json.Unmarshal([]byte(fileChangesJSON.String), &m.FileChanges); err != nil {
				m.FileChanges = nil
			}
		}

		memberIndex[m.AgentID] = len(members)
		members = append(members, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate team members: %w", err)
	}

	// Query 2: Per-agent directed message counts (mentions only, not broadcasts/groups)
	for i, m := range members {
		values := buildForAgentValues(m.AgentID, m.Role)
		if len(values) == 0 {
			continue
		}
		placeholders := strings.Repeat("?,", len(values))
		placeholders = placeholders[:len(placeholders)-1]

		mentionQuery := fmt.Sprintf(
			`SELECT COUNT(*) FROM messages m
			 WHERE m.deleted = 0 AND m.agent_id != ?
			 AND m.message_id IN (
				SELECT mr.message_id FROM message_refs mr
				WHERE mr.ref_type = 'mention' AND mr.ref_value IN (%s)
			 )`, placeholders)
		args := []any{m.AgentID}
		for _, v := range values {
			args = append(args, v)
		}
		_ = h.state.DB().QueryRow(mentionQuery, args...).Scan(&members[i].InboxTotal)

		// Unread: same filter, minus messages already read
		unreadQuery := mentionQuery + " AND m.message_id NOT IN (SELECT message_id FROM message_reads WHERE agent_id = ?)"
		unreadArgs := append(args, m.AgentID)
		_ = h.state.DB().QueryRow(unreadQuery, unreadArgs...).Scan(&members[i].InboxUnread)
	}

	// Query 3: Shared message counts (broadcasts + per-group)
	shared := &SharedMessages{}

	// Broadcasts: messages with no mention refs and no group scopes
	_ = h.state.DB().QueryRow(`SELECT COUNT(*) FROM messages m
		WHERE m.deleted = 0
		AND m.message_id NOT IN (SELECT mr.message_id FROM message_refs mr WHERE mr.ref_type = 'mention')
		AND m.message_id NOT IN (SELECT ms.message_id FROM message_scopes ms WHERE ms.scope_type = 'group')`).Scan(&shared.BroadcastTotal)

	// Per-group message counts
	groupRows, err := h.state.DB().Query(`SELECT ms.scope_value, COUNT(DISTINCT m.message_id)
		FROM messages m
		JOIN message_scopes ms ON m.message_id = ms.message_id AND ms.scope_type = 'group'
		WHERE m.deleted = 0
		GROUP BY ms.scope_value
		ORDER BY COUNT(DISTINCT m.message_id) DESC`)
	if err == nil {
		defer func() { _ = groupRows.Close() }()
		for groupRows.Next() {
			var gc GroupMessageCount
			if err := groupRows.Scan(&gc.Name, &gc.Total); err == nil {
				shared.Groups = append(shared.Groups, gc)
			}
		}
	}

	if members == nil {
		members = []TeamMember{}
	}

	// Only include shared messages if there are any
	var sharedPtr *SharedMessages
	if shared.BroadcastTotal > 0 || len(shared.Groups) > 0 {
		sharedPtr = shared
	}

	return &TeamListResponse{Members: members, SharedMessages: sharedPtr}, nil
}
