package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/leonletto/thrum/internal/daemon/cleanup"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/gitctx"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/types"
)

// SessionStartRequest represents the request for session.start RPC.
type SessionStartRequest struct {
	AgentID string        `json:"agent_id"` // Required: which agent is starting the session
	Scopes  []types.Scope `json:"scopes,omitempty"`
	Refs    []types.Ref   `json:"refs,omitempty"`
}

// SessionStartResponse represents the response from session.start RPC.
type SessionStartResponse struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	StartedAt string `json:"started_at"`
}

// SessionEndRequest represents the request for session.end RPC.
type SessionEndRequest struct {
	SessionID string `json:"session_id"`       // Required: which session to end
	Reason    string `json:"reason,omitempty"` // "normal", "crash", "superseded"
}

// SessionEndResponse represents the response from session.end RPC.
type SessionEndResponse struct {
	SessionID string `json:"session_id"`
	EndedAt   string `json:"ended_at"`
	Duration  int64  `json:"duration_ms"`
}

// HeartbeatRequest represents the request for session.heartbeat RPC.
type HeartbeatRequest struct {
	SessionID    string        `json:"session_id"`
	AddScopes    []types.Scope `json:"add_scopes,omitempty"`
	RemoveScopes []types.Scope `json:"remove_scopes,omitempty"`
	AddRefs      []types.Ref   `json:"add_refs,omitempty"`
	RemoveRefs   []types.Ref   `json:"remove_refs,omitempty"`
}

// HeartbeatResponse represents the response from session.heartbeat RPC.
type HeartbeatResponse struct {
	SessionID  string `json:"session_id"`
	LastSeenAt string `json:"last_seen_at"`
}

// SetIntentRequest represents the request for session.setIntent RPC.
type SetIntentRequest struct {
	SessionID string `json:"session_id"` // Required: which session
	Intent    string `json:"intent"`     // Free text, e.g., "Refactoring auth flow"
}

// SetIntentResponse represents the response from session.setIntent RPC.
type SetIntentResponse struct {
	SessionID       string `json:"session_id"`
	Intent          string `json:"intent"`
	IntentUpdatedAt string `json:"intent_updated_at"`
}

// SetTaskRequest represents the request for session.setTask RPC.
type SetTaskRequest struct {
	SessionID   string `json:"session_id"`   // Required: which session
	CurrentTask string `json:"current_task"` // e.g., "beads:thrum-xyz" or free text
}

// SetTaskResponse represents the response from session.setTask RPC.
type SetTaskResponse struct {
	SessionID     string `json:"session_id"`
	CurrentTask   string `json:"current_task"`
	TaskUpdatedAt string `json:"task_updated_at"`
}

// ListSessionsRequest represents the request for session.list RPC.
type ListSessionsRequest struct {
	AgentID    string `json:"agent_id,omitempty"`    // Filter by agent
	ActiveOnly bool   `json:"active_only,omitempty"` // Only active sessions
}

// ListSessionsResponse represents the response from session.list RPC.
type ListSessionsResponse struct {
	Sessions []SessionSummary `json:"sessions"`
}

// SessionSummary represents a session in the list.
type SessionSummary struct {
	SessionID  string `json:"session_id"`
	AgentID    string `json:"agent_id"`
	StartedAt  string `json:"started_at"`
	EndedAt    string `json:"ended_at,omitempty"`
	EndReason  string `json:"end_reason,omitempty"`
	LastSeenAt string `json:"last_seen_at"`
	Intent     string `json:"intent,omitempty"`
	Status     string `json:"status"` // "active" or "ended"
}

// SessionHandler handles session-related RPC methods.
type SessionHandler struct {
	state *state.State
}

// NewSessionHandler creates a new session handler.
func NewSessionHandler(state *state.State) *SessionHandler {
	return &SessionHandler{state: state}
}

// HandleStart handles the session.start RPC method.
func (h *SessionHandler) HandleStart(ctx context.Context, params json.RawMessage) (any, error) {
	var req SessionStartRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}

	h.state.Lock()
	defer h.state.Unlock()

	// Check if agent exists
	if err := h.verifyAgentExists(req.AgentID); err != nil {
		return nil, fmt.Errorf("agent not found: %w", err)
	}

	// Check for orphaned sessions and recover them
	if err := h.recoverOrphanedSessions(req.AgentID); err != nil {
		return nil, fmt.Errorf("recover orphaned sessions: %w", err)
	}

	// Generate new session ID
	sessionID := identity.GenerateSessionID()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Create session.start event
	event := types.AgentSessionStartEvent{
		Type:      "agent.session.start",
		Timestamp: now,
		SessionID: sessionID,
		AgentID:   req.AgentID,
	}

	// Write event to JSONL and SQLite
	if err := h.state.WriteEvent(event); err != nil {
		return nil, fmt.Errorf("write session.start event: %w", err)
	}

	// Store initial scopes
	for _, scope := range req.Scopes {
		_, err := h.state.DB().Exec(`
			INSERT OR IGNORE INTO session_scopes (session_id, scope_type, scope_value, added_at)
			VALUES (?, ?, ?, ?)
		`, sessionID, scope.Type, scope.Value, now)
		if err != nil {
			return nil, fmt.Errorf("add scope: %w", err)
		}
	}

	// Store initial refs
	for _, ref := range req.Refs {
		_, err := h.state.DB().Exec(`
			INSERT OR IGNORE INTO session_refs (session_id, ref_type, ref_value, added_at)
			VALUES (?, ?, ?, ?)
		`, sessionID, ref.Type, ref.Value, now)
		if err != nil {
			return nil, fmt.Errorf("add ref: %w", err)
		}
	}

	return &SessionStartResponse{
		SessionID: sessionID,
		AgentID:   req.AgentID,
		StartedAt: now,
	}, nil
}

// HandleEnd handles the session.end RPC method.
func (h *SessionHandler) HandleEnd(ctx context.Context, params json.RawMessage) (any, error) {
	var req SessionEndRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	h.state.Lock()
	defer h.state.Unlock()

	// Get session info to calculate duration
	session, err := h.getSession(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)

	// Parse start time to calculate duration
	startTime, err := time.Parse(time.RFC3339Nano, session.StartedAt)
	if err != nil {
		return nil, fmt.Errorf("parse start time: %w", err)
	}
	duration := now.Sub(startTime).Milliseconds()

	// Default reason to "normal" if not provided
	reason := req.Reason
	if reason == "" {
		reason = "normal"
	}

	// Create session.end event
	event := types.AgentSessionEndEvent{
		Type:      "agent.session.end",
		Timestamp: nowStr,
		SessionID: req.SessionID,
		Reason:    reason,
	}

	// Write event to JSONL and SQLite
	if err := h.state.WriteEvent(event); err != nil {
		return nil, fmt.Errorf("write session.end event: %w", err)
	}

	// Sync work contexts for this agent
	if err := h.syncWorkContexts(session.AgentID); err != nil {
		// Log error but don't fail the session end
		fmt.Fprintf(os.Stderr, "Warning: failed to sync work contexts: %v\n", err)
	}

	return &SessionEndResponse{
		SessionID: req.SessionID,
		EndedAt:   nowStr,
		Duration:  duration,
	}, nil
}

// HandleList handles the session.list RPC method.
func (h *SessionHandler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	var req ListSessionsRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	h.state.RLock()
	defer h.state.RUnlock()

	// Build query
	query := `SELECT s.session_id, s.agent_id, s.started_at, s.ended_at, s.end_reason, s.last_seen_at,
	                 COALESCE(wc.intent, '') as intent
	          FROM sessions s
	          LEFT JOIN agent_work_contexts wc ON s.session_id = wc.session_id
	          WHERE 1=1`
	args := []any{}

	if req.AgentID != "" {
		query += " AND s.agent_id = ?"
		args = append(args, req.AgentID)
	}

	if req.ActiveOnly {
		query += " AND s.ended_at IS NULL"
	}

	query += " ORDER BY s.started_at DESC"

	rows, err := h.state.DB().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	sessions := []SessionSummary{}
	for rows.Next() {
		var s SessionSummary
		var endedAt, endReason, lastSeenAt sql.NullString
		var intent string

		if err := rows.Scan(
			&s.SessionID,
			&s.AgentID,
			&s.StartedAt,
			&endedAt,
			&endReason,
			&lastSeenAt,
			&intent,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}

		if endedAt.Valid {
			s.EndedAt = endedAt.String
			s.Status = "ended"
		} else {
			s.Status = "active"
		}

		if endReason.Valid {
			s.EndReason = endReason.String
		}

		if lastSeenAt.Valid {
			s.LastSeenAt = lastSeenAt.String
		}

		s.Intent = intent

		sessions = append(sessions, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}

	return &ListSessionsResponse{Sessions: sessions}, nil
}

// HandleHeartbeat handles the session.heartbeat RPC method.
func (h *SessionHandler) HandleHeartbeat(ctx context.Context, params json.RawMessage) (any, error) {
	var req HeartbeatRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	h.state.Lock()
	defer h.state.Unlock()

	// Verify session exists and is active
	session, err := h.getSession(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	if session.EndedAt != "" {
		return nil, fmt.Errorf("session %s has already ended", req.SessionID)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Update last_seen_at
	_, err = h.state.DB().Exec(`UPDATE sessions SET last_seen_at = ? WHERE session_id = ?`, now, req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("update last_seen_at: %w", err)
	}

	// Remove scopes
	for _, scope := range req.RemoveScopes {
		_, err := h.state.DB().Exec(`
			DELETE FROM session_scopes
			WHERE session_id = ? AND scope_type = ? AND scope_value = ?
		`, req.SessionID, scope.Type, scope.Value)
		if err != nil {
			return nil, fmt.Errorf("remove scope: %w", err)
		}
	}

	// Add scopes
	for _, scope := range req.AddScopes {
		_, err := h.state.DB().Exec(`
			INSERT OR IGNORE INTO session_scopes (session_id, scope_type, scope_value, added_at)
			VALUES (?, ?, ?, ?)
		`, req.SessionID, scope.Type, scope.Value, now)
		if err != nil {
			return nil, fmt.Errorf("add scope: %w", err)
		}
	}

	// Remove refs
	for _, ref := range req.RemoveRefs {
		_, err := h.state.DB().Exec(`
			DELETE FROM session_refs
			WHERE session_id = ? AND ref_type = ? AND ref_value = ?
		`, req.SessionID, ref.Type, ref.Value)
		if err != nil {
			return nil, fmt.Errorf("remove ref: %w", err)
		}
	}

	// Add refs
	for _, ref := range req.AddRefs {
		_, err := h.state.DB().Exec(`
			INSERT OR IGNORE INTO session_refs (session_id, ref_type, ref_value, added_at)
			VALUES (?, ?, ?, ?)
		`, req.SessionID, ref.Type, ref.Value, now)
		if err != nil {
			return nil, fmt.Errorf("add ref: %w", err)
		}
	}

	// Extract and store git work context
	if worktreePath := h.getWorktreePath(req.SessionID); worktreePath != "" {
		if gitCtx, err := gitctx.ExtractWorkContext(worktreePath); err == nil {
			// Ignore error - work context is optional/best-effort
			_ = h.updateWorkContext(req.SessionID, session.AgentID, gitCtx)
		}
	}

	return &HeartbeatResponse{
		SessionID:  req.SessionID,
		LastSeenAt: now,
	}, nil
}

// recoverOrphanedSessions finds sessions for the agent with no end event and marks them as recovered.
func (h *SessionHandler) recoverOrphanedSessions(agentID string) error {
	// Find sessions with no end time for this agent
	query := `SELECT session_id, started_at
	          FROM sessions
	          WHERE agent_id = ? AND ended_at IS NULL`

	rows, err := h.state.DB().Query(query, agentID)
	if err != nil {
		return fmt.Errorf("query orphaned sessions: %w", err)
	}

	// Collect all orphaned sessions first (to avoid holding read lock while writing)
	var orphanedSessions []string
	for rows.Next() {
		var sessionID, startedAt string
		if err := rows.Scan(&sessionID, &startedAt); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan orphaned session: %w", err)
		}
		orphanedSessions = append(orphanedSessions, sessionID)
	}
	_ = rows.Close()

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate orphaned sessions: %w", err)
	}

	// Now write recovery events for each orphaned session
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, sessionID := range orphanedSessions {
		// Create session.end event with crash_recovered reason
		event := types.AgentSessionEndEvent{
			Type:      "agent.session.end",
			Timestamp: now,
			SessionID: sessionID,
			Reason:    "crash_recovered",
		}

		// Write event to JSONL and SQLite
		if err := h.state.WriteEvent(event); err != nil {
			return fmt.Errorf("write crash recovery event for session %s: %w", sessionID, err)
		}
	}

	return nil
}

// verifyAgentExists checks if an agent with the given ID exists.
func (h *SessionHandler) verifyAgentExists(agentID string) error {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM agents WHERE agent_id = ?)`
	err := h.state.DB().QueryRow(query, agentID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check agent existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("agent %s not registered", agentID)
	}
	return nil
}

// getSession retrieves session information.
func (h *SessionHandler) getSession(sessionID string) (*sessionInfo, error) {
	query := `SELECT session_id, agent_id, started_at, ended_at
	          FROM sessions
	          WHERE session_id = ?`

	var session sessionInfo
	var endedAt sql.NullString

	err := h.state.DB().QueryRow(query, sessionID).Scan(
		&session.SessionID,
		&session.AgentID,
		&session.StartedAt,
		&endedAt,
	)

	if err != nil {
		return nil, err
	}

	if endedAt.Valid {
		session.EndedAt = endedAt.String
	}

	return &session, nil
}

// sessionInfo represents session information from the database.
type sessionInfo struct {
	SessionID string
	AgentID   string
	StartedAt string
	EndedAt   string
}

// getWorktreePath returns the worktree path for a session from session_refs.
// Returns empty string if no worktree ref is found.
func (h *SessionHandler) getWorktreePath(sessionID string) string {
	var worktreePath string
	query := `SELECT ref_value FROM session_refs
	          WHERE session_id = ? AND ref_type = 'worktree'
	          LIMIT 1`

	err := h.state.DB().QueryRow(query, sessionID).Scan(&worktreePath)
	if err != nil {
		return ""
	}

	return worktreePath
}

// updateWorkContext upserts the agent_work_contexts table with git-derived context.
func (h *SessionHandler) updateWorkContext(sessionID, agentID string, ctx *gitctx.WorkContext) error {
	// Marshal JSON fields
	unmergedCommitsJSON, err := json.Marshal(ctx.UnmergedCommits)
	if err != nil {
		return fmt.Errorf("marshal unmerged commits: %w", err)
	}

	uncommittedFilesJSON, err := json.Marshal(ctx.UncommittedFiles)
	if err != nil {
		return fmt.Errorf("marshal uncommitted files: %w", err)
	}

	changedFilesJSON, err := json.Marshal(ctx.ChangedFiles)
	if err != nil {
		return fmt.Errorf("marshal changed files: %w", err)
	}

	gitUpdatedAt := ctx.ExtractedAt.Format(time.RFC3339Nano)

	// Upsert work context
	_, err = h.state.DB().Exec(`
		INSERT INTO agent_work_contexts (
			session_id, agent_id, branch, worktree_path,
			unmerged_commits, uncommitted_files, changed_files, git_updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			branch = excluded.branch,
			worktree_path = excluded.worktree_path,
			unmerged_commits = excluded.unmerged_commits,
			uncommitted_files = excluded.uncommitted_files,
			changed_files = excluded.changed_files,
			git_updated_at = excluded.git_updated_at
	`, sessionID, agentID, ctx.Branch, ctx.WorktreePath,
		string(unmergedCommitsJSON), string(uncommittedFilesJSON), string(changedFilesJSON), gitUpdatedAt)

	return err
}

// HandleSetIntent handles the session.setIntent RPC method.
func (h *SessionHandler) HandleSetIntent(ctx context.Context, params json.RawMessage) (any, error) {
	var req SetIntentRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	h.state.Lock()
	defer h.state.Unlock()

	// Verify session exists and is active
	session, err := h.getSession(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	if session.EndedAt != "" {
		return nil, fmt.Errorf("session %s has already ended", req.SessionID)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Upsert work context with intent
	_, err = h.state.DB().Exec(`
		INSERT INTO agent_work_contexts (session_id, agent_id, intent, intent_updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			intent = excluded.intent,
			intent_updated_at = excluded.intent_updated_at
	`, req.SessionID, session.AgentID, req.Intent, now)
	if err != nil {
		return nil, fmt.Errorf("update intent: %w", err)
	}

	return &SetIntentResponse{
		SessionID:       req.SessionID,
		Intent:          req.Intent,
		IntentUpdatedAt: now,
	}, nil
}

// HandleSetTask handles the session.setTask RPC method.
func (h *SessionHandler) HandleSetTask(ctx context.Context, params json.RawMessage) (any, error) {
	var req SetTaskRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	h.state.Lock()
	defer h.state.Unlock()

	// Verify session exists and is active
	session, err := h.getSession(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	if session.EndedAt != "" {
		return nil, fmt.Errorf("session %s has already ended", req.SessionID)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Upsert work context with task
	_, err = h.state.DB().Exec(`
		INSERT INTO agent_work_contexts (session_id, agent_id, current_task, task_updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			current_task = excluded.current_task,
			task_updated_at = excluded.task_updated_at
	`, req.SessionID, session.AgentID, req.CurrentTask, now)
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}

	return &SetTaskResponse{
		SessionID:     req.SessionID,
		CurrentTask:   req.CurrentTask,
		TaskUpdatedAt: now,
	}, nil
}

// syncWorkContexts syncs work contexts for an agent to JSONL.
func (h *SessionHandler) syncWorkContexts(agentID string) error {
	// Collect all work contexts for this agent
	query := `SELECT session_id, branch, worktree_path,
	                 unmerged_commits, uncommitted_files, changed_files, git_updated_at,
	                 current_task, task_updated_at, intent, intent_updated_at
	          FROM agent_work_contexts
	          WHERE agent_id = ?
	          ORDER BY git_updated_at DESC`

	rows, err := h.state.DB().Query(query, agentID)
	if err != nil {
		return fmt.Errorf("query work contexts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var contexts []types.SessionWorkContext
	now := time.Now().UTC()

	for rows.Next() {
		var ctx types.SessionWorkContext
		var branch, worktreePath, unmergedCommitsJSON, uncommittedFilesJSON, changedFilesJSON, gitUpdatedAt sql.NullString
		var currentTask, taskUpdatedAt, intent, intentUpdatedAt sql.NullString

		err := rows.Scan(
			&ctx.SessionID,
			&branch,
			&worktreePath,
			&unmergedCommitsJSON,
			&uncommittedFilesJSON,
			&changedFilesJSON,
			&gitUpdatedAt,
			&currentTask,
			&taskUpdatedAt,
			&intent,
			&intentUpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("scan row: %w", err)
		}

		// Set optional fields
		if branch.Valid {
			ctx.Branch = branch.String
		}
		if worktreePath.Valid {
			ctx.WorktreePath = worktreePath.String
		}
		if gitUpdatedAt.Valid {
			ctx.GitUpdatedAt = gitUpdatedAt.String
		}
		if currentTask.Valid {
			ctx.CurrentTask = currentTask.String
		}
		if taskUpdatedAt.Valid {
			ctx.TaskUpdatedAt = taskUpdatedAt.String
		}
		if intent.Valid {
			ctx.Intent = intent.String
		}
		if intentUpdatedAt.Valid {
			ctx.IntentUpdatedAt = intentUpdatedAt.String
		}

		// Unmarshal JSON fields
		if unmergedCommitsJSON.Valid && unmergedCommitsJSON.String != "" {
			var commits []types.CommitSummary
			if err := json.Unmarshal([]byte(unmergedCommitsJSON.String), &commits); err == nil {
				ctx.UnmergedCommits = commits
			}
		}

		if uncommittedFilesJSON.Valid && uncommittedFilesJSON.String != "" {
			var files []string
			if err := json.Unmarshal([]byte(uncommittedFilesJSON.String), &files); err == nil {
				ctx.UncommittedFiles = files
			}
		}

		if changedFilesJSON.Valid && changedFilesJSON.String != "" {
			var files []string
			if err := json.Unmarshal([]byte(changedFilesJSON.String), &files); err == nil {
				ctx.ChangedFiles = files
			}
		}

		contexts = append(contexts, ctx)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate rows: %w", err)
	}

	// Filter stale contexts before sync
	cleanupContexts := make([]cleanup.SessionWorkContext, len(contexts))
	for i, ctx := range contexts {
		var gitUpdated *time.Time
		if ctx.GitUpdatedAt != "" {
			t, err := time.Parse(time.RFC3339, ctx.GitUpdatedAt)
			if err == nil {
				gitUpdated = &t
			}
		}

		// Note: We don't have session ended info here, so we rely on the periodic cleanup
		cleanupContexts[i] = cleanup.SessionWorkContext{
			SessionID:       ctx.SessionID,
			GitUpdatedAt:    gitUpdated,
			UnmergedCommits: string(mustMarshalJSON(ctx.UnmergedCommits)),
		}
	}

	filteredCleanup := cleanup.FilterStaleContexts(cleanupContexts, now)

	// Convert back to types.SessionWorkContext
	filtered := make([]types.SessionWorkContext, 0, len(filteredCleanup))
	for _, cleanupCtx := range filteredCleanup {
		// Find original context
		for _, origCtx := range contexts {
			if origCtx.SessionID == cleanupCtx.SessionID {
				filtered = append(filtered, origCtx)
				break
			}
		}
	}

	// Create and write agent.update event
	event := types.AgentUpdateEvent{
		Type:         "agent.update",
		Timestamp:    now.Format(time.RFC3339Nano),
		AgentID:      agentID,
		WorkContexts: filtered,
	}

	if err := h.state.WriteEvent(event); err != nil {
		return fmt.Errorf("write agent.update event: %w", err)
	}

	return nil
}

// mustMarshalJSON marshals v to JSON, panicking on error (for internal use only).
func mustMarshalJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal JSON: %v", err))
	}
	return data
}
