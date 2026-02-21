package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
	agentcontext "github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/gitctx"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/types"
)

// RegisterRequest represents the request for agent.register RPC.
type RegisterRequest struct {
	Name       string `json:"name,omitempty"` // Human-readable agent name (optional)
	Role       string `json:"role"`
	Module     string `json:"module"`
	Display    string `json:"display,omitempty"`
	Force      bool   `json:"force,omitempty"`       // Override existing
	ReRegister bool   `json:"re_register,omitempty"` // Same agent returning
}

// RegisterResponse represents the response from agent.register RPC.
type RegisterResponse struct {
	AgentID  string        `json:"agent_id"`
	Status   string        `json:"status"` // "registered", "conflict", "updated"
	Conflict *ConflictInfo `json:"conflict,omitempty"`
}

// ConflictInfo represents information about a registration conflict.
type ConflictInfo struct {
	ExistingAgentID string `json:"existing_agent_id"`
	RegisteredAt    string `json:"registered_at"`
	LastSeenAt      string `json:"last_seen_at"`
}

// ListAgentsRequest represents the request for agent.list RPC.
type ListAgentsRequest struct {
	Role   string `json:"role,omitempty"`   // Filter by role
	Module string `json:"module,omitempty"` // Filter by module
}

// ListAgentsResponse represents the response from agent.list RPC.
type ListAgentsResponse struct {
	Agents []AgentInfo `json:"agents"`
}

// AgentInfo represents information about a registered agent.
type AgentInfo struct {
	AgentID      string `json:"agent_id"`
	Kind         string `json:"kind"`
	Role         string `json:"role"`
	Module       string `json:"module"`
	Display      string `json:"display"`
	RegisteredAt string `json:"registered_at"`
	LastSeenAt   string `json:"last_seen_at,omitempty"`
}

// WhoamiResponse represents the response from agent.whoami RPC.
type WhoamiResponse struct {
	AgentID      string `json:"agent_id"`
	Role         string `json:"role"`
	Module       string `json:"module"`
	Display      string `json:"display"`
	Source       string `json:"source"` // "environment", "flags", "identity_file"
	SessionID    string `json:"session_id,omitempty"`
	SessionStart string `json:"session_start,omitempty"`
	Branch       string `json:"branch,omitempty"`
	Intent       string `json:"intent,omitempty"`
}

// ListContextRequest represents the request for agent.listContext RPC.
type ListContextRequest struct {
	AgentID string `json:"agent_id,omitempty"` // Filter by specific agent
	Branch  string `json:"branch,omitempty"`   // Filter by branch name
	File    string `json:"file,omitempty"`     // Filter by file touched
}

// ListContextResponse represents the response from agent.listContext RPC.
type ListContextResponse struct {
	Contexts []AgentWorkContext `json:"contexts"`
}

// DeleteAgentRequest represents the request for agent.delete RPC.
type DeleteAgentRequest struct {
	Name string `json:"name"` // Agent name to delete
}

// DeleteAgentResponse represents the response from agent.delete RPC.
type DeleteAgentResponse struct {
	AgentID string `json:"agent_id"`
	Deleted bool   `json:"deleted"`
	Message string `json:"message,omitempty"`
}

// CleanupAgentRequest represents the request for agent.cleanup RPC.
type CleanupAgentRequest struct {
	DryRun    bool `json:"dry_run"`
	Force     bool `json:"force"`
	Threshold int  `json:"threshold"` // Days since last seen
}

// OrphanedAgent represents an orphaned agent.
type OrphanedAgent struct {
	AgentID           string `json:"agent_id"`
	Role              string `json:"role"`
	Module            string `json:"module"`
	Worktree          string `json:"worktree"`
	Branch            string `json:"branch"`
	LastSeenAt        string `json:"last_seen_at"`
	WorktreeMissing   bool   `json:"worktree_missing"`
	BranchMissing     bool   `json:"branch_missing"`
	DaysSinceLastSeen int    `json:"days_since_last_seen"`
	MessageCount      int    `json:"message_count"`
}

// CleanupAgentResponse represents the response from agent.cleanup RPC.
type CleanupAgentResponse struct {
	Orphans []OrphanedAgent `json:"orphans"`
	Deleted []string        `json:"deleted"` // List of deleted agent IDs
	DryRun  bool            `json:"dry_run"`
	Message string          `json:"message,omitempty"`
}

// AgentWorkContext represents an agent's work context.
type AgentWorkContext struct {
	SessionID        string                 `json:"session_id"`
	AgentID          string                 `json:"agent_id"`
	Branch           string                 `json:"branch,omitempty"`
	WorktreePath     string                 `json:"worktree_path,omitempty"`
	UnmergedCommits  []gitctx.CommitSummary `json:"unmerged_commits,omitempty"`
	UncommittedFiles []string               `json:"uncommitted_files,omitempty"`
	ChangedFiles     []string               `json:"changed_files,omitempty"` // Kept for backward compatibility
	FileChanges      []gitctx.FileChange    `json:"file_changes,omitempty"`  // NEW: rich per-file data
	GitUpdatedAt     string                 `json:"git_updated_at,omitempty"`
	CurrentTask      string                 `json:"current_task,omitempty"`
	TaskUpdatedAt    string                 `json:"task_updated_at,omitempty"`
	Intent           string                 `json:"intent,omitempty"`
	IntentUpdatedAt  string                 `json:"intent_updated_at,omitempty"`
}

// AgentHandler handles agent-related RPC methods.
type AgentHandler struct {
	state *state.State
}

// NewAgentHandler creates a new agent handler.
func NewAgentHandler(s *state.State) *AgentHandler {
	return &AgentHandler{state: s}
}

// HandleRegister handles the agent.register RPC method.
func (h *AgentHandler) HandleRegister(ctx context.Context, params json.RawMessage) (any, error) {
	var req RegisterRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.Role == "" {
		return nil, errors.New("role is required")
	}
	if req.Module == "" {
		return nil, errors.New("module is required")
	}

	// Generate agent ID
	repoID := h.state.RepoID()
	agentID := identity.GenerateAgentID(repoID, req.Role, req.Module, req.Name)

	// Extract worktree name from repo path
	worktree := h.getWorktreeName()

	// Lock for conflict detection and registration
	h.state.Lock()
	defer h.state.Unlock()

	// Validate name≠role: these checks prevent addressing ambiguity.
	// Skip during re-registration since the agent already exists.
	if !req.ReRegister {
		// Check 1: name == own role
		if req.Name != "" && req.Name == req.Role {
			return nil, fmt.Errorf("agent name %q cannot be the same as its role — use a distinct name (e.g., '%s_main')", req.Name, req.Role)
		}

		// Check 2: name matches an existing role in the agents table
		if req.Name != "" {
			var roleCount int
			_ = h.state.DB().QueryRowContext(ctx,
				`SELECT COUNT(*) FROM agents WHERE role = ?`, req.Name,
			).Scan(&roleCount)
			if roleCount > 0 {
				return nil, fmt.Errorf("agent name %q conflicts with existing role '%s' — choose a different name", req.Name, req.Name)
			}
		}

		// Check 3: role matches an existing agent name/ID
		if req.Role != "" {
			var nameCount int
			_ = h.state.DB().QueryRowContext(ctx,
				`SELECT COUNT(*) FROM agents WHERE agent_id = ?`, req.Role,
			).Scan(&nameCount)
			if nameCount > 0 {
				return nil, fmt.Errorf("role %q conflicts with existing agent name '%s' — choose a different role", req.Role, req.Role)
			}
		}
	}

	// Check for duplicate agent name (name must be unique across all agents)
	if req.Name != "" {
		existingByName, err := h.getAgentByID(ctx, req.Name)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("check for existing agent by name: %w", err)
		}
		if existingByName != nil && existingByName.AgentID != agentID {
			// Another agent already has this name
			return &RegisterResponse{
				AgentID: "",
				Status:  "conflict",
				Conflict: &ConflictInfo{
					ExistingAgentID: existingByName.AgentID,
					RegisteredAt:    existingByName.RegisteredAt,
					LastSeenAt:      existingByName.LastSeenAt,
				},
			}, fmt.Errorf("agent name '%s' already in use by %s", req.Name, existingByName.AgentID)
		}
	}

	// Check for existing agent with same role+module
	existingAgent, err := h.getAgentByRoleModule(ctx, req.Role, req.Module)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("check for existing agent: %w", err)
	}

	// Handle conflicts
	if existingAgent != nil {
		// Same agent returning (ID matches)
		if existingAgent.AgentID == agentID {
			if req.ReRegister {
				// Update registration
				return h.registerAgent(ctx, agentID, req.Name, req.Role, req.Module, req.Display, worktree, "updated")
			}
			// Return existing agent info without conflict
			return &RegisterResponse{
				AgentID: agentID,
				Status:  "registered",
			}, nil
		}

		// Different agent, same role+module
		if !req.Force && !req.ReRegister {
			// Return conflict info
			return &RegisterResponse{
				AgentID: "",
				Status:  "conflict",
				Conflict: &ConflictInfo{
					ExistingAgentID: existingAgent.AgentID,
					RegisteredAt:    existingAgent.RegisteredAt,
					LastSeenAt:      existingAgent.LastSeenAt,
				},
			}, nil
		}

		// Force override - remove old agent entry to prevent duplicates
		_, _ = h.state.DB().ExecContext(ctx, "DELETE FROM agents WHERE agent_id = ?", existingAgent.AgentID)

		// Force override - register new agent
		return h.registerAgent(ctx, agentID, req.Name, req.Role, req.Module, req.Display, worktree, "registered")
	}

	// No conflict - register new agent
	return h.registerAgent(ctx, agentID, req.Name, req.Role, req.Module, req.Display, worktree, "registered")
}

// HandleList handles the agent.list RPC method.
func (h *AgentHandler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	var req ListAgentsRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	h.state.RLock()
	defer h.state.RUnlock()

	// Build query with optional filters
	query := `SELECT agent_id, kind, role, module, display, registered_at, last_seen_at
	          FROM agents WHERE 1=1`
	args := []any{}

	if req.Role != "" {
		query += " AND role = ?"
		args = append(args, req.Role)
	}
	if req.Module != "" {
		query += " AND module = ?"
		args = append(args, req.Module)
	}

	query += " ORDER BY registered_at DESC"

	rows, err := h.state.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query agents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	agents := []AgentInfo{}
	for rows.Next() {
		var agent AgentInfo
		var display, lastSeenAt sql.NullString

		if err := rows.Scan(
			&agent.AgentID,
			&agent.Kind,
			&agent.Role,
			&agent.Module,
			&display,
			&agent.RegisteredAt,
			&lastSeenAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}

		if display.Valid {
			agent.Display = display.String
		}
		if lastSeenAt.Valid {
			agent.LastSeenAt = lastSeenAt.String
		}

		agents = append(agents, agent)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agents: %w", err)
	}

	return &ListAgentsResponse{Agents: agents}, nil
}

// HandleWhoami handles the agent.whoami RPC method.
func (h *AgentHandler) HandleWhoami(ctx context.Context, params json.RawMessage) (any, error) {
	// Parse optional caller identity from request
	var req struct {
		CallerAgentID string `json:"caller_agent_id,omitempty"`
	}
	_ = json.Unmarshal(params, &req) // Ignore errors — params may be empty

	var agentID string
	var role, module, agentName string
	source := "identity_file"

	if req.CallerAgentID != "" {
		// Use caller-provided identity (worktree-aware)
		agentID = req.CallerAgentID
		source = "caller"

		// Look up role/module from the agents table
		h.state.RLock()
		var dbRole, dbModule sql.NullString
		_ = h.state.DB().QueryRowContext(ctx, "SELECT role, module FROM agents WHERE agent_id = ?", agentID).Scan(&dbRole, &dbModule)
		h.state.RUnlock()
		if dbRole.Valid {
			role = dbRole.String
		}
		if dbModule.Valid {
			module = dbModule.String
		}
	} else {
		// Fallback: resolve from daemon's config
		log.Printf("WARNING: CallerAgentID not provided in whoami request, falling back to daemon repo path: %s (CLI should resolve identity)", h.state.RepoPath())
		cfg, err := config.LoadWithPath(h.state.RepoPath(), "", "")
		if err != nil {
			return nil, fmt.Errorf("resolve identity: %w", err)
		}
		agentID = identity.GenerateAgentID(h.state.RepoID(), cfg.Agent.Role, cfg.Agent.Module, cfg.Agent.Name)
		role = cfg.Agent.Role
		module = cfg.Agent.Module
		agentName = cfg.Agent.Name

		if os.Getenv("THRUM_ROLE") != "" || os.Getenv("THRUM_MODULE") != "" {
			source = "environment"
		}
	}

	// Check for active session for this agent
	h.state.RLock()
	defer h.state.RUnlock()

	var sessionID, sessionStart sql.NullString
	query := `SELECT session_id, started_at
	          FROM sessions
	          WHERE agent_id = ? AND ended_at IS NULL
	          ORDER BY started_at DESC
	          LIMIT 1`
	sessionErr := h.state.DB().QueryRowContext(ctx, query, agentID).Scan(&sessionID, &sessionStart)
	if sessionErr != nil && sessionErr != sql.ErrNoRows {
		return nil, fmt.Errorf("query active session: %w", sessionErr)
	}

	// Query work context for branch and intent
	var branch, intent sql.NullString
	ctxQuery := `SELECT branch, intent
	             FROM agent_work_contexts
	             WHERE agent_id = ?
	             ORDER BY intent_updated_at DESC
	             LIMIT 1`
	ctxErr := h.state.DB().QueryRowContext(ctx, ctxQuery, agentID).Scan(&branch, &intent)
	if ctxErr != nil && ctxErr != sql.ErrNoRows {
		return nil, fmt.Errorf("query work context: %w", ctxErr)
	}

	response := &WhoamiResponse{
		AgentID: agentID,
		Role:    role,
		Module:  module,
		Display: agentName,
		Source:  source,
	}

	if sessionID.Valid {
		response.SessionID = sessionID.String
		response.SessionStart = sessionStart.String
	}
	if branch.Valid {
		response.Branch = branch.String
	}
	if intent.Valid {
		response.Intent = intent.String
	}

	return response, nil
}

// resolveHostname returns a human-friendly hostname for this machine.
// Prefers THRUM_HOSTNAME env var, otherwise uses os.Hostname() with .local suffix stripped.
func resolveHostname() string {
	if h := os.Getenv("THRUM_HOSTNAME"); h != "" {
		return h
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(h, ".local")
}

// registerAgent writes an agent.register event and returns the response.
func (h *AgentHandler) registerAgent(ctx context.Context, agentID, name, role, module, display, worktree, status string) (*RegisterResponse, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Create agent.register event
	event := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: now,
		AgentID:   agentID,
		Kind:      "agent", // Default to "agent"
		Name:      name,
		Role:      role,
		Module:    module,
		Worktree:  worktree,
		Display:   display,
		Hostname:  resolveHostname(),
	}

	// Write event to JSONL and SQLite
	if err := h.state.WriteEvent(ctx, event); err != nil {
		return nil, fmt.Errorf("write agent.register event: %w", err)
	}

	// Auto-create role group if role is non-empty and group doesn't exist yet.
	// This makes role-based addressing work through the explicit group system.
	if role != "" {
		var groupExists bool
		_ = h.state.DB().QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM groups WHERE name = ?)`, role,
		).Scan(&groupExists)
		if !groupExists {
			groupID := "grp_role_" + role
			_, err := h.state.DB().ExecContext(ctx,
				`INSERT OR IGNORE INTO groups (group_id, name, description, created_at, created_by)
				 VALUES (?, ?, ?, ?, 'system')`,
				groupID, role,
				fmt.Sprintf("Auto-created group for role '%s'", role),
				now,
			)
			if err == nil {
				_, _ = h.state.DB().ExecContext(ctx,
					`INSERT OR IGNORE INTO group_members (group_id, member_type, member_value, added_at)
					 VALUES (?, 'role', ?, ?)`,
					groupID, role, now,
				)
			}
		}
	}

	return &RegisterResponse{
		AgentID: agentID,
		Status:  status,
	}, nil
}

// getAgentByRoleModule queries for an existing agent with the given role and module.
func (h *AgentHandler) getAgentByRoleModule(ctx context.Context, role, module string) (*AgentInfo, error) {
	query := `SELECT agent_id, kind, role, module, display, registered_at, last_seen_at
	          FROM agents
	          WHERE role = ? AND module = ?
	          LIMIT 1`

	var agent AgentInfo
	var display, lastSeenAt sql.NullString

	err := h.state.DB().QueryRowContext(ctx, query, role, module).Scan(
		&agent.AgentID,
		&agent.Kind,
		&agent.Role,
		&agent.Module,
		&display,
		&agent.RegisteredAt,
		&lastSeenAt,
	)

	if err != nil {
		return nil, err
	}

	if display.Valid {
		agent.Display = display.String
	}
	if lastSeenAt.Valid {
		agent.LastSeenAt = lastSeenAt.String
	}

	return &agent, nil
}

// getAgentByID queries for an existing agent with the given agent ID.
func (h *AgentHandler) getAgentByID(ctx context.Context, agentID string) (*AgentInfo, error) {
	query := `SELECT agent_id, kind, role, module, display, registered_at, last_seen_at
	          FROM agents
	          WHERE agent_id = ?
	          LIMIT 1`

	var agent AgentInfo
	var display, lastSeenAt sql.NullString

	err := h.state.DB().QueryRowContext(ctx, query, agentID).Scan(
		&agent.AgentID,
		&agent.Kind,
		&agent.Role,
		&agent.Module,
		&display,
		&agent.RegisteredAt,
		&lastSeenAt,
	)

	if err != nil {
		return nil, err
	}

	if display.Valid {
		agent.Display = display.String
	}
	if lastSeenAt.Valid {
		agent.LastSeenAt = lastSeenAt.String
	}

	return &agent, nil
}

// HandleListContext handles the agent.listContext RPC method.
func (h *AgentHandler) HandleListContext(ctx context.Context, params json.RawMessage) (any, error) {
	var req ListContextRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	h.state.Lock()
	defer h.state.Unlock()

	// Build query with filters
	query := `SELECT session_id, agent_id, branch, worktree_path,
	                 unmerged_commits, uncommitted_files, changed_files, file_changes, git_updated_at,
	                 current_task, task_updated_at, intent, intent_updated_at
	          FROM agent_work_contexts
	          WHERE 1=1`

	args := []any{}

	// Filter by agent_id
	if req.AgentID != "" {
		query += " AND agent_id = ?"
		args = append(args, req.AgentID)
	}

	// Filter by branch
	if req.Branch != "" {
		query += " AND branch = ?"
		args = append(args, req.Branch)
	}

	// Filter by file (in changed_files or uncommitted_files)
	if req.File != "" {
		query += ` AND (changed_files LIKE ? OR uncommitted_files LIKE ?)`
		filePattern := fmt.Sprintf("%%\"%s\"%%", req.File)
		args = append(args, filePattern, filePattern)
	}

	query += " ORDER BY git_updated_at DESC"

	rows, err := h.state.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query work contexts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	contexts := []AgentWorkContext{}

	for rows.Next() {
		var ctx AgentWorkContext
		var branch, worktreePath, unmergedCommitsJSON, uncommittedFilesJSON, changedFilesJSON, fileChangesJSON, gitUpdatedAt sql.NullString
		var currentTask, taskUpdatedAt, intent, intentUpdatedAt sql.NullString

		err := rows.Scan(
			&ctx.SessionID,
			&ctx.AgentID,
			&branch,
			&worktreePath,
			&unmergedCommitsJSON,
			&uncommittedFilesJSON,
			&changedFilesJSON,
			&fileChangesJSON,
			&gitUpdatedAt,
			&currentTask,
			&taskUpdatedAt,
			&intent,
			&intentUpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		// Unmarshal JSON fields
		if unmergedCommitsJSON.Valid && unmergedCommitsJSON.String != "" {
			if err := json.Unmarshal([]byte(unmergedCommitsJSON.String), &ctx.UnmergedCommits); err != nil {
				// Ignore unmarshal errors, leave empty
				ctx.UnmergedCommits = []gitctx.CommitSummary{}
			}
		} else {
			ctx.UnmergedCommits = []gitctx.CommitSummary{}
		}

		if uncommittedFilesJSON.Valid && uncommittedFilesJSON.String != "" {
			if err := json.Unmarshal([]byte(uncommittedFilesJSON.String), &ctx.UncommittedFiles); err != nil {
				ctx.UncommittedFiles = []string{}
			}
		} else {
			ctx.UncommittedFiles = []string{}
		}

		if changedFilesJSON.Valid && changedFilesJSON.String != "" {
			if err := json.Unmarshal([]byte(changedFilesJSON.String), &ctx.ChangedFiles); err != nil {
				ctx.ChangedFiles = []string{}
			}
		} else {
			ctx.ChangedFiles = []string{}
		}

		if fileChangesJSON.Valid && fileChangesJSON.String != "" {
			if err := json.Unmarshal([]byte(fileChangesJSON.String), &ctx.FileChanges); err != nil {
				ctx.FileChanges = []gitctx.FileChange{}
			}
		} else {
			ctx.FileChanges = []gitctx.FileChange{}
		}

		// Set optional string fields
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

		contexts = append(contexts, ctx)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return &ListContextResponse{
		Contexts: contexts,
	}, nil
}

// HandleDelete handles the agent.delete RPC method.
func (h *AgentHandler) HandleDelete(ctx context.Context, params json.RawMessage) (any, error) {
	var req DeleteAgentRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.Name == "" {
		return nil, errors.New("agent name is required")
	}

	// Validate agent name format
	if err := identity.ValidateAgentName(req.Name); err != nil {
		return nil, fmt.Errorf("invalid agent name: %w", err)
	}

	// Lock for DB query to get agent
	h.state.Lock()
	agent, err := h.getAgentByID(ctx, req.Name)
	if err != nil {
		h.state.Unlock()
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("agent not found: %s", req.Name)
		}
		return nil, fmt.Errorf("check agent existence: %w", err)
	}
	h.state.Unlock()

	// File I/O without lock
	thrumDir := filepath.Join(h.state.RepoPath(), ".thrum")
	identityPath := filepath.Join(thrumDir, "identities", req.Name+".json")
	messagePath := filepath.Join(h.state.SyncDir(), "messages", req.Name+".jsonl")
	contextPath := filepath.Join(thrumDir, "context", req.Name+".md")
	preamblePath := agentcontext.PreamblePath(thrumDir, req.Name)

	// Delete identity file
	if err := os.Remove(identityPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("delete identity file: %w", err)
	}

	// Delete message file
	if err := os.Remove(messagePath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("delete message file: %w", err)
	}

	// Delete context file (if exists)
	if err := os.Remove(contextPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("delete context file: %w", err)
	}

	// Delete preamble file (if exists)
	if err := os.Remove(preamblePath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("delete preamble file: %w", err)
	}

	// Re-lock for DB delete + event write
	h.state.Lock()
	_, err = h.state.DB().ExecContext(ctx, "DELETE FROM agents WHERE agent_id = ?", req.Name)
	if err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete agent from database: %w", err)
	}

	// Emit agent.cleanup event
	now := time.Now().UTC().Format(time.RFC3339Nano)
	event := types.AgentCleanupEvent{
		Type:      "agent.cleanup",
		Timestamp: now,
		AgentID:   req.Name,
		Reason:    "manual deletion",
		Method:    "manual",
	}

	// Write event to events.jsonl
	if err := h.state.WriteEvent(ctx, event); err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("write agent.cleanup event: %w", err)
	}
	h.state.Unlock()

	return &DeleteAgentResponse{
		AgentID: agent.AgentID,
		Deleted: true,
		Message: fmt.Sprintf("Agent %s deleted successfully", req.Name),
	}, nil
}

// HandleCleanup handles the agent.cleanup RPC method.
func (h *AgentHandler) HandleCleanup(ctx context.Context, params json.RawMessage) (any, error) {
	var req CleanupAgentRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Lock for DB query to get agent list
	h.state.RLock()
	query := `SELECT agent_id, kind, role, module, last_seen_at FROM agents ORDER BY agent_id`
	rows, err := h.state.DB().QueryContext(ctx, query)
	if err != nil {
		h.state.RUnlock()
		return nil, fmt.Errorf("query agents: %w", err)
	}

	// Scan all agents into a slice
	type agentRecord struct {
		agentID    string
		kind       string
		role       string
		module     string
		lastSeenAt sql.NullString
	}
	var agents []agentRecord

	for rows.Next() {
		var rec agentRecord
		if err := rows.Scan(&rec.agentID, &rec.kind, &rec.role, &rec.module, &rec.lastSeenAt); err != nil {
			_ = rows.Close()
			h.state.RUnlock()
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		agents = append(agents, rec)
	}
	_ = rows.Close()

	if err := rows.Err(); err != nil {
		h.state.RUnlock()
		return nil, fmt.Errorf("iterate agents: %w", err)
	}
	h.state.RUnlock()

	// Check identity files and worktrees without lock (file I/O + git commands)
	var orphans []OrphanedAgent
	thrumDir := filepath.Join(h.state.RepoPath(), ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")

	for _, agent := range agents {
		// Skip users (kind == "user")
		if agent.kind == "user" {
			continue
		}

		// Check if identity file exists
		identityPath := filepath.Join(identitiesDir, agent.agentID+".json")
		if _, err := os.Stat(identityPath); os.IsNotExist(err) {
			// Identity file missing - orphan
			orphans = append(orphans, OrphanedAgent{
				AgentID:         agent.agentID,
				Role:            agent.role,
				Module:          agent.module,
				LastSeenAt:      agent.lastSeenAt.String,
				WorktreeMissing: true,
				BranchMissing:   true,
			})
			continue
		}

		// Read identity file to get worktree and branch info
		identityData, err := os.ReadFile(identityPath) //nolint:gosec // G304 - path from internal identities directory
		if err != nil {
			continue // Skip if can't read
		}

		var identity struct {
			Agent struct {
				Name string `json:"name"`
			} `json:"agent"`
			Worktree string `json:"worktree"`
		}
		if err := json.Unmarshal(identityData, &identity); err != nil {
			continue // Skip if can't parse
		}

		// Check worktree exists (calls git - no lock held)
		worktreeMissing := false
		if identity.Worktree != "" {
			worktreeMissing = !h.worktreeExists(ctx, identity.Worktree)
		}

		// Check if agent is stale (based on last_seen_at)
		daysSinceLastSeen := 9999
		isStale := false
		if agent.lastSeenAt.Valid {
			lastSeen, err := time.Parse(time.RFC3339, agent.lastSeenAt.String)
			if err == nil {
				daysSinceLastSeen = int(time.Since(lastSeen).Hours() / 24)
				isStale = daysSinceLastSeen > req.Threshold
			}
		}

		// If worktree is missing or agent is stale, mark as orphan
		if worktreeMissing || isStale {
			// Count messages (DB query without lock - SQLite handles its own concurrency)
			messageCount := h.getMessageCount(ctx, agent.agentID)

			orphans = append(orphans, OrphanedAgent{
				AgentID:           agent.agentID,
				Role:              agent.role,
				Module:            agent.module,
				Worktree:          identity.Worktree,
				LastSeenAt:        agent.lastSeenAt.String,
				WorktreeMissing:   worktreeMissing,
				DaysSinceLastSeen: daysSinceLastSeen,
				MessageCount:      messageCount,
			})
		}
	}

	// If dry-run, just return the orphans
	if req.DryRun {
		return &CleanupAgentResponse{
			Orphans: orphans,
			Deleted: []string{},
			DryRun:  true,
			Message: fmt.Sprintf("Found %d orphaned agent(s)", len(orphans)),
		}, nil
	}

	// If not force mode, return orphans for interactive confirmation
	// (The CLI will handle interactive confirmation and call agent.delete for each)
	if !req.Force {
		return &CleanupAgentResponse{
			Orphans: orphans,
			Deleted: []string{},
			DryRun:  false,
			Message: "Use --force to delete all orphans without prompting",
		}, nil
	}

	// Force mode: delete all orphans
	deleted := []string{}
	for _, orphan := range orphans {
		// Call HandleDelete for each orphan
		deleteReq := DeleteAgentRequest{Name: orphan.AgentID}
		deleteJSON, _ := json.Marshal(deleteReq)

		// HandleDelete manages its own locks
		_, err := h.HandleDelete(ctx, deleteJSON)
		if err == nil {
			deleted = append(deleted, orphan.AgentID)
		}
	}

	return &CleanupAgentResponse{
		Orphans: orphans,
		Deleted: deleted,
		DryRun:  false,
		Message: fmt.Sprintf("Deleted %d orphaned agent(s)", len(deleted)),
	}, nil
}

// worktreeExists checks if a worktree exists via git worktree list.
func (h *AgentHandler) worktreeExists(ctx context.Context, worktreeName string) bool {
	// Run git worktree list and check if worktree name appears
	output, err := safecmd.Git(ctx, h.state.RepoPath(), "worktree", "list", "--porcelain")
	if err != nil {
		return false
	}

	// Parse output to find worktree
	for line := range strings.SplitSeq(string(output), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			// Check if path ends with worktree name
			if strings.HasSuffix(path, "/"+worktreeName) || strings.HasSuffix(path, "\\"+worktreeName) || filepath.Base(path) == worktreeName {
				return true
			}
		}
	}

	return false
}

// getMessageCount returns the number of messages for an agent.
func (h *AgentHandler) getMessageCount(ctx context.Context, agentID string) int {
	var count int
	err := h.state.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE agent_id = ?", agentID).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

// getWorktreeName extracts the worktree name from the repo path.
// Returns the basename of the repo path (e.g., "daemon", "foundation", "main").
func (h *AgentHandler) getWorktreeName() string {
	repoPath := h.state.RepoPath()
	// Extract basename (last component of path)
	// This works for: /path/to/thrum -> "thrum", ~/.workspaces/thrum/daemon -> "daemon"
	parts := strings.Split(repoPath, string(os.PathSeparator))
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return ""
}
