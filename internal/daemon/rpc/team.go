package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/process"
	ttmux "github.com/leonletto/thrum/internal/tmux"
	"github.com/leonletto/thrum/internal/types"
)

// TeamListRequest represents the request for team.list RPC.
type TeamListRequest struct {
	IncludeOffline bool `json:"include_offline,omitempty"`

	// IncludeSystem, when true, surfaces identities marked
	// Reserved=true (e.g. @supervisor_<project>) that are hidden
	// from the default listing. Set via `thrum team --system`.
	IncludeSystem bool `json:"include_system,omitempty"`
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
	AgentPID        int                `json:"agent_pid,omitempty"`
	Runtime         string             `json:"runtime,omitempty"`
	WorktreePath    string             `json:"worktree,omitempty"`
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
	Status          string             `json:"status"` // "active", "offline", or "reserved"
	TmuxSession     string             `json:"tmux_session,omitempty"`
	TmuxState       string             `json:"tmux_state,omitempty"` // alive, stale, dead, or empty

	// Reserved marks a daemon-internal pseudo-agent (e.g.
	// @supervisor_<project>) that is hidden from the default
	// `thrum team` output. Only surfaced when IncludeSystem is
	// set on the request.
	Reserved bool `json:"reserved,omitempty"`
}

// TeamHandler handles team-related RPC methods.
type TeamHandler struct {
	state              *state.State
	thrumDir           string
	supervisorIdentity *config.IdentityFile // synthesized virtual-supervisor identity; nil in tests
}

// NewTeamHandler creates a new team handler.
// SupervisorIdentity is the virtual-supervisor identity synthesized at
// daemon boot; it is wired in here now and consumed by ListAgents in a
// later task. Passing nil is safe — the injection path short-circuits.
func NewTeamHandler(state *state.State, thrumDir string, supervisorIdentity *config.IdentityFile) *TeamHandler {
	return &TeamHandler{
		state:              state,
		thrumDir:           thrumDir,
		supervisorIdentity: supervisorIdentity,
	}
}

// HandleList handles the team.list RPC method.
//
// Three-phase lock discipline:
//
//  1. Phase 1 acquires RLock, runs buildTeamListLocked (queries + enrichment),
//     and collects dead agents (active members whose agent_pid is no longer
//     running) into a local slice, then releases RLock.
//  2. Phase 2 runs with NO lock held and emits session.end events for each
//     dead agent via emitSessionEndForDeadAgent. Anti-pattern 1 forbids
//     holding a read lock across event emission because WriteEvent needs
//     its own write lock and nested RLock→Lock would deadlock.
//  3. Phase 3 rewrites the in-memory response to mark dead agents as
//     offline so the caller sees the self-healed state immediately.
func (h *TeamHandler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	var req TeamListRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	type deadAgent struct {
		SessionID string
		AgentID   string
		PID       int
	}

	// PHASE 1: build team list and collect dead-agent session IDs under RLock.
	h.state.RLock()
	members, shared, identityMap, err := h.buildTeamListLocked(ctx, req)
	if err != nil {
		h.state.RUnlock()
		return nil, err
	}

	var deadAgents []deadAgent
	for _, m := range members {
		if m.Status != "active" ||
			m.AgentPID <= 0 ||
			process.IsRunning(m.AgentPID) ||
			m.SessionID == "" {
			continue
		}

		// Cross-check identity file: if the file reports a live PID that
		// differs from the DB's stored PID, the DB is stale but the agent
		// is actually alive. Skip the self-heal — the next
		// RefreshLocalIdentity call from that agent will reconcile the DB
		// via the always-on Fix C path into agent.register Fix A. Without
		// this guard, a fresh daemon (rebuilt from events) would emit
		// false-positive session.end events against every pre-existing
		// agent whose DB PID predates the refresh feature (thrum-pxz.14
		// Fix B).
		if idFile, ok := identityMap[m.AgentID]; ok && idFile != nil {
			if idFile.AgentPID > 0 && idFile.AgentPID != m.AgentPID && process.IsRunning(idFile.AgentPID) {
				log.Printf("team.list: stale DB PID but identity file reports live PID — skipping self-heal: agent=%s db_pid=%d file_pid=%d",
					m.AgentID, m.AgentPID, idFile.AgentPID)
				continue
			}
		}

		deadAgents = append(deadAgents, deadAgent{
			SessionID: m.SessionID,
			AgentID:   m.AgentID,
			PID:       m.AgentPID,
		})
	}
	h.state.RUnlock()

	// PHASE 2: emit session.end events without holding any lock.
	for _, d := range deadAgents {
		if emitErr := h.emitSessionEndForDeadAgent(ctx, d.SessionID); emitErr != nil {
			log.Printf("team.list: failed to emit session.end: agent=%s session=%s err=%v",
				d.AgentID, d.SessionID, emitErr)
			continue
		}
		log.Printf("team.list: marking dead agent offline: agent=%s pid=%d",
			d.AgentID, d.PID)
	}

	// PHASE 3: rewrite in-memory response so the caller sees status=offline.
	if len(deadAgents) > 0 {
		deadMap := make(map[string]bool, len(deadAgents))
		for _, d := range deadAgents {
			deadMap[d.SessionID] = true
		}
		for i := range members {
			if deadMap[members[i].SessionID] {
				members[i].Status = "offline"
			}
		}
	}

	if members == nil {
		members = []TeamMember{}
	}

	var sharedPtr *SharedMessages
	if shared != nil && (shared.BroadcastTotal > 0 || len(shared.Groups) > 0) {
		sharedPtr = shared
	}
	return &TeamListResponse{Members: members, SharedMessages: sharedPtr}, nil
}

// buildTeamListLocked runs the three SQL queries and identity-file enrichment
// pass. The caller MUST hold h.state.RLock() (or Lock()) for the duration of
// this call. It does not acquire, release, upgrade, or downgrade any lock.
//
// Returns the enriched member list, the shared-messages summary, and the
// identity map used for enrichment so callers (HandleList) can cross-check
// file-vs-DB state without re-walking worktrees.
func (h *TeamHandler) buildTeamListLocked(ctx context.Context, req TeamListRequest) ([]TeamMember, *SharedMessages, map[string]*config.IdentityFile, error) {
	// Query 1: Agents + sessions + work contexts
	query := `SELECT
		a.agent_id, a.role, a.module, a.display, a.hostname, a.agent_pid,
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

	rows, err := h.state.DB().QueryContext(ctx, query)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query team members: %w", err)
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
			&m.AgentID, &m.Role, &m.Module, &display, &hostname, &m.AgentPID,
			&sessionID, &sessionStart, &lastSeen,
			&branch, &worktreePath, &intent, &currentTask,
			&unmergedCommitsJSON, &fileChangesJSON,
		); err != nil {
			return nil, nil, nil, fmt.Errorf("scan team member: %w", err)
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
		return nil, nil, nil, fmt.Errorf("iterate team members: %w", err)
	}

	// Enrich with identity file data from ALL worktrees. The identity file
	// is authoritative for runtime, tmux_session, and tmux_state; the DB is
	// authoritative for agent_pid. The identityMap is returned to the
	// caller so Phase 1's dead-agent cross-check can reuse it without a
	// second worktree scan.
	var identityMap map[string]*config.IdentityFile
	if h.thrumDir != "" {
		identityMap = ReadIdentitiesAcrossWorktrees(ctx, h.thrumDir)
		for i := range members {
			m := &members[i]
			idFile := identityMap[m.AgentID]
			if idFile == nil {
				continue
			}

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

		// When IncludeSystem is set, synthesize TeamMember entries for
		// Reserved identities that are NOT in the agents table. The
		// permission supervisor pseudo-agent is the canonical case: it
		// exists only as a reply-capable sender for nudges, never
		// registers an agent.register event, and therefore never has
		// an agents row. Without this synthesis step, `thrum team
		// --system` would return nothing for it.
		//
		// Synthesized members get Status="reserved" (distinct from
		// "active" or "offline") to make them visually distinguishable
		// in the output, and their AgentID is the identity file's
		// Agent.Name so downstream listing code sees a stable ID.
		if req.IncludeSystem {
			for name, idFile := range identityMap {
				if !idFile.Reserved {
					continue
				}
				if _, exists := memberIndex[name]; exists {
					// Already in the list from the agents-table query; the
					// enrichment loop above already populated Reserved.
					continue
				}
				synthetic := TeamMember{
					AgentID:  name,
					Role:     idFile.Agent.Role,
					Module:   idFile.Agent.Module,
					Display:  idFile.Agent.Display,
					Runtime:  idFile.Runtime,
					Status:   "reserved",
					Reserved: true,
				}
				memberIndex[name] = len(members)
				members = append(members, synthetic)
			}
		}

		// Filter out Reserved entries when IncludeSystem is NOT set.
		// This covers both (a) future agents registered via
		// agent.register that happen to have Reserved=true in their
		// identity file, and (b) paranoid defense-in-depth: if a
		// reserved synthesis ever landed by mistake without the
		// IncludeSystem flag, the filter still hides it.
		if !req.IncludeSystem {
			filtered := members[:0]
			newIndex := make(map[string]int, len(members))
			for _, m := range members {
				if m.Reserved {
					continue
				}
				newIndex[m.AgentID] = len(filtered)
				filtered = append(filtered, m)
			}
			members = filtered
			memberIndex = newIndex
		}
	}
	// Inject the virtual supervisor pseudo-agent when IncludeSystem is
	// set. After Task 7 (thrum-kqna.3) removed the Reserved=true
	// identity file from disk, the file walk above cannot find a
	// supervisor entry; the daemon carries its synthesized identity
	// in-memory and injects it here. Injection runs outside the
	// `h.thrumDir != ""` block so it works even when the file walk is
	// disabled (e.g. unit-test fixtures).
	if req.IncludeSystem && h.supervisorIdentity != nil {
		name := h.supervisorIdentity.Agent.Name
		if _, exists := memberIndex[name]; !exists {
			synthetic := TeamMember{
				AgentID:  name,
				Role:     h.supervisorIdentity.Agent.Role,
				Module:   h.supervisorIdentity.Agent.Module,
				Display:  h.supervisorIdentity.Agent.Display,
				Status:   "reserved",
				Reserved: true,
			}
			memberIndex[name] = len(members)
			members = append(members, synthetic)
		}
	}

	// memberIndex is used by downstream logic below and by the caller's
	// dead-agent self-heal in HandleList, which keys off agent_id
	// (still valid after the optional filter above).
	_ = memberIndex

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
		_ = h.state.DB().QueryRowContext(ctx, mentionQuery, args...).Scan(&members[i].InboxTotal)

		// Unread: same filter, minus messages already read
		unreadQuery := mentionQuery + " AND m.message_id NOT IN (SELECT message_id FROM message_reads WHERE agent_id = ?)"
		unreadArgs := append(args, m.AgentID)
		_ = h.state.DB().QueryRowContext(ctx, unreadQuery, unreadArgs...).Scan(&members[i].InboxUnread)
	}

	// Query 3: Shared message counts (broadcasts + per-group)
	shared := &SharedMessages{}

	// Broadcasts: messages with no mention refs and no group scopes
	_ = h.state.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM messages m
		WHERE m.deleted = 0
		AND m.message_id NOT IN (SELECT mr.message_id FROM message_refs mr WHERE mr.ref_type = 'mention')
		AND m.message_id NOT IN (SELECT ms.message_id FROM message_scopes ms WHERE ms.scope_type = 'group')`).Scan(&shared.BroadcastTotal)

	// Per-group message counts
	groupRows, err := h.state.DB().QueryContext(ctx, `SELECT ms.scope_value, COUNT(DISTINCT m.message_id)
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

	return members, shared, identityMap, nil
}

// emitSessionEndForDeadAgent writes an agent.session.end event to the
// daemon's event log and projector. The caller MUST NOT hold h.state's
// RLock or Lock when calling — this function acquires the write lock
// internally to coordinate with other event writers.
//
// Idempotence: applySessionEnd in the projector unconditionally updates
// sessions.ended_at. Successive calls within the same team.list request
// are prevented by Phase 1's collector check (Status == "active") — the
// second team.list query sees the session as ended and does not re-queue
// it. Duplicate emissions from concurrent callers are absorbed as a
// no-op write (same session_id, same end_reason).
func (h *TeamHandler) emitSessionEndForDeadAgent(ctx context.Context, sessionID string) error {
	h.state.Lock()
	defer h.state.Unlock()

	event := types.AgentSessionEndEvent{
		Type:      "agent.session.end",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: sessionID,
		Reason:    "dead_pid",
	}
	if err := h.state.WriteEvent(ctx, event); err != nil {
		return fmt.Errorf("write session.end event: %w", err)
	}
	return nil
}

// parseSessionName extracts the tmux session name portion from a
// "session:window.pane" target string.
func parseSessionName(target string) string {
	name, _, _ := ttmux.ParseTarget(target)
	return name
}
