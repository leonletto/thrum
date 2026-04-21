package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
	agentcontext "github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/gitctx"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/identity/guard"
	"github.com/leonletto/thrum/internal/jsonl"
	"github.com/leonletto/thrum/internal/process"
	ttmux "github.com/leonletto/thrum/internal/tmux"
	"github.com/leonletto/thrum/internal/types"
	wtpkg "github.com/leonletto/thrum/internal/worktree"
)

// RegisterRequest represents the request for agent.register RPC.
type RegisterRequest struct {
	Name       string `json:"name,omitempty"` // Human-readable agent name (optional)
	Role       string `json:"role"`
	Module     string `json:"module"`
	Display    string `json:"display,omitempty"`
	Force      bool   `json:"force,omitempty"`       // CLI --force: re-register existing agent, overriding stored fields (thrum-ufv5.2)
	ReRegister bool   `json:"re_register,omitempty"` // Same agent returning
	AgentPID   int    `json:"agent_pid,omitempty"`   // Claude process PID for identity resolution
}

// RegisterResponse represents the response from agent.register RPC.
type RegisterResponse struct {
	AgentID        string        `json:"agent_id"`
	Status         string        `json:"status"`                    // "registered", "conflict", "updated"
	SessionID      string        `json:"session_id,omitempty"`      // populated when a session was resurrected
	SessionResumed bool          `json:"session_resumed,omitempty"` // true when ensureActiveSession emitted a fresh session.start (thrum-xir.18)
	Conflict       *ConflictInfo `json:"conflict,omitempty"`
}

// ConflictInfo represents information about a registration conflict.
type ConflictInfo struct {
	ExistingAgentID string `json:"existing_agent_id"`
	RegisteredAt    string `json:"registered_at"`
	LastSeenAt      string `json:"last_seen_at"`
	ConflictPID     int    `json:"conflict_pid,omitempty"` // PID of the conflicting agent
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
	AgentPID     int    `json:"agent_pid,omitempty"` // Claude process PID for identity resolution
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
	// Hook-delivery fields (hook-inbox-delivery design).
	Host        string `json:"host,omitempty"`
	AgentPID    int    `json:"pid,omitempty"`
	TmuxSession string `json:"tmux_session,omitempty"`
	TmuxAlive   bool   `json:"tmux_alive,omitempty"`
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

	// Validate agent name if provided (unnamed agents get hash-based IDs)
	if req.Name != "" {
		if err := identity.ValidateAgentName(req.Name); err != nil {
			return nil, fmt.Errorf("invalid agent name: %w", err)
		}
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

	// thrum-iw42: look up by agent_id (not by role+module). The old
	// role+module uniqueness guard was removed because it rejected every
	// peer-bridge proxy that shared a prefix with a pre-existing proxy
	// (e.g. Telegram's thrum:coordinator_main collided with the peer
	// bridge's thrum:impl_mocksf_s2). Proxy uniqueness is structurally
	// bounded by peer-derived prefix plus remote agent name, so DB-level
	// (role, module) enforcement was redundant.
	//
	// Identity-file enforcement coverage (thrum-33dt):
	//   - tmux.create path: calls worktree.EnforceOneIdentity (tmux.go:286)
	//   - quickstart path: G1a/G1b pre-flight + EnforceOneIdentity post-save
	//     (internal/cli/quickstart.go)
	//   - refresh path: EnforceOneIdentity after identity load
	//     (internal/cli/refresh.go)
	//   - direct agent.register RPC: enforceWorktreeIdentity below, gated
	//     on peercred.Worktree so anonymous bootstraps skip cleanly
	//     (the CLI paths cover them instead). keepName is extended with
	//     the peercred-resolved caller's AgentID (thrum-dw06) so the
	//     caller's own identity never gets swept up when the call
	//     registers a differently named agent.
	existingAgent, err := h.getAgentByID(ctx, agentID)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("check for existing agent by id: %w", err)
	}

	if existingAgent != nil {
		// Same agent returning (agent_id matches by construction).
		// PID self-heal: if the caller provides a PID that differs from
		// the stored one, update the registration even without an
		// explicit ReRegister flag. This lets quickstart and
		// RefreshLocalIdentity correct stale DB state without requiring
		// --force. Without this branch, the first thrum command after a
		// daemon rebuild would fire false-positive dead-agent self-heals
		// on every pre-existing agent whose DB PID predates the refresh
		// feature (thrum-pxz.14 Fix A).
		var resp *RegisterResponse
		var regErr error
		// thrum-ufv5.2: Force is a distinct trigger from ReRegister. --force on
		// an existing agent must refresh the agents projection (role, module,
		// display) so agent.list stays consistent with whoami and the identity
		// file. Without this branch, a re-register with --force updated the
		// identity file but left the DB row stale — two views of the same agent
		// diverged (see SC-04 repro in the linked bug).
		switch {
		case req.AgentPID > 0 && existingAgent.AgentPID != req.AgentPID:
			resp, regErr = h.registerAgent(ctx, agentID, req.Name, req.Role, req.Module, req.Display, worktree, "updated", req.AgentPID)
		case req.ReRegister, req.Force:
			resp, regErr = h.registerAgent(ctx, agentID, req.Name, req.Role, req.Module, req.Display, worktree, "updated", req.AgentPID)
		default:
			// Same agent, same PID (or no PID provided) — no-op return.
			resp = &RegisterResponse{
				AgentID: agentID,
				Status:  "registered",
			}
		}
		if regErr != nil {
			return nil, regErr
		}

		// Auto-resurrect (thrum-xir.18): if the agent has no active
		// session and the caller's PID is alive, emit a fresh
		// agent.session.start inline. Best-effort — log and continue
		// on error so the register RPC stays resilient. Failing
		// register because resurrection failed would break every agent
		// on every command, which is worse than the bug we are fixing.
		// ensureActiveSession runs under the same write lock taken at
		// the top of this method.
		resumedID, resumeErr := h.ensureActiveSession(ctx, agentID, req.AgentPID)
		if resumeErr != nil {
			log.Printf("agent.register: session resurrect failed: agent=%s err=%v", agentID, resumeErr)
		} else if resumedID != "" {
			// thrum-2b2t: ensureActiveSession deliberately keeps the
			// resurrect path minimal (no scope/orphan handling) to stay
			// out of the explicit session.start semantics used by
			// quickstart. That minimalism leaves session_refs empty for
			// resurrected sessions, which breaks peercred's
			// worktree→agent match for mutating RPCs (the session
			// exists but has no worktree ref). Persist a worktree ref
			// here so peercred can resolve this agent on the next RPC.
			h.persistResurrectWorktreeRef(ctx, agentID, resumedID, req.AgentPID)
			resp.SessionID = resumedID
			resp.SessionResumed = true
		}
		h.enforceWorktreeIdentity(ctx, agentIdentityName(req.Name, agentID))
		return resp, nil
	}

	// Fresh agent — no existing row for this agent_id.
	resp, err := h.registerAgent(ctx, agentID, req.Name, req.Role, req.Module, req.Display, worktree, "registered", req.AgentPID)
	if err == nil {
		h.enforceWorktreeIdentity(ctx, agentIdentityName(req.Name, agentID))
	}
	return resp, err
}

// agentIdentityName returns the string used as the per-worktree identity
// file's base name — the human-readable agent name when provided,
// otherwise the generated agent_id. Matches the naming convention used
// by config.SaveIdentityFile so EnforceOneIdentity preserves the right
// file. The agentID fallback is only reached for anonymous
// registrations (no --name supplied).
func agentIdentityName(name, agentID string) string {
	if name != "" {
		return name
	}
	return agentID
}

// enforceWorktreeIdentity applies the "one identity per worktree"
// invariant for the caller's worktree when peercred resolved a caller.
// Anonymous bootstraps (resolved==nil) are skipped — the CLI-side
// quickstart path enforces the invariant at identity-write time for
// those. For direct agent.register RPCs from a registered caller,
// this cleans up residual identity files left by prior registrations
// under the same worktree. See thrum-33dt.
//
// Thrum-dw06 / thrum-0pos: enforcement only fires when the CALLER is
// registering themselves (resolved.AgentID == keepName) AND it
// preserves every agent registered in the caller's worktree (not
// just keepName), so co-located multi-agent scenarios survive.
// A caller that bootstraps a differently named agent — test
// harnesses, peer-bridge proxies — is NOT authorized to scrub
// siblings in this worktree, because those siblings may be other
// legitimately registered agents co-located with the caller. The
// single-keeper form of thrum-33dt treated every non-keepName
// sibling as stale, including other live agents. Ajmd softened the
// blast radius from delete→quarantine; dw06 narrowed the firing
// condition so bootstrap calls leave other files alone; 0pos
// finishes the job by preserving every co-located registered agent
// in the self-rename case too.
//
// Self-rename + stale cleanup: when the caller renames themselves
// via direct RPC, enforcement still runs but the keeper set now
// includes keepName + resolved.AgentID + every agent registered in
// this worktree. Only .json files whose agent_id is NOT registered
// in this worktree at all (truly stale — abandoned prior
// registrations) get quarantined. This preserves production
// housekeeping while letting multi-agent test harnesses coexist.
func (h *AgentHandler) enforceWorktreeIdentity(ctx context.Context, keepName string) {
	if keepName == "" {
		return
	}
	// ok dropped: both (nil, true) [peercred ran, anonymous] and
	// (nil, false) [peercred did not run — tests / non-unix stubs]
	// correctly skip enforcement, so discriminating between them
	// would produce identical branches.
	resolved, _ := peercred.FromContext(ctx)
	// The Worktree == "" guard also covers resolved identities coming
	// from non-unix stub platforms (where the resolver returns an
	// AgentID but leaves Worktree unpopulated).
	if resolved == nil || resolved.Worktree == "" {
		return
	}
	// Self-rename only. Collapses three cases correctly:
	//   - AgentID == "" (anonymous + populated Worktree, rare but
	//     possible from future non-unix stubs): skip — no caller to
	//     self-rename against, so no authorization to scrub siblings.
	//   - AgentID != keepName (bootstrap / multi-agent worktree):
	//     skip — caller is registering a different agent; the
	//     sibling .json files may belong to other co-located agents.
	//   - AgentID == keepName (self-rename): enforce — legitimate
	//     stale-sibling cleanup on the caller's own identity.
	if resolved.AgentID != keepName {
		return
	}
	// Self-rename only (enforced by the guard above): resolved.AgentID
	// and keepName are the same string on this path, so listing both
	// in keepers would duplicate an entry with no semantic gain.
	// ListAgentsInWorktree covers every co-located agent including the
	// caller.
	keepers := []string{keepName}
	if h.state != nil {
		keepers = append(keepers, h.state.ListAgentsInWorktree(ctx, resolved.Worktree)...)
	}
	// thrum-182j defenses:
	//   (a) IsPIDAlive — refuse to quarantine a file whose AgentPID
	//       is currently running. Backstops a stale keeper list.
	//   (b) CallerCwd + !AllowCrossWorktree — CWD-match gate. The
	//       self-rename path's caller and target are the same
	//       worktree by construction (both are resolved.Worktree
	//       from the same peercred resolution), so the match
	//       passes naturally. Populating CallerCwd makes that an
	//       active invariant: any future refactor that lets the
	//       two diverge will hit the gate and refuse.
	wtpkg.EnforceOneIdentityWith(resolved.Worktree, wtpkg.EnforceOpts{
		IsPIDAlive: func(pid int) bool { return process.IsRunning(pid) },
		CallerCwd:  resolved.Worktree,
	}, keepers...)
}

// HandleList handles the agent.list RPC method.
func (h *AgentHandler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	var req ListAgentsRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// thrum-7nuj: agent.list is on the hot path for `thrum team` and
	// other agent-invoked lookups. Touch last_seen if we can resolve
	// the caller from peercred; silently skip if not (anonymous /
	// synthetic callers don't signal liveness).
	if resolved, _ := peercred.FromContext(ctx); resolved != nil && resolved.AgentID != "" {
		_ = h.state.TouchAgentLastSeen(ctx, resolved.AgentID)
	}

	h.state.RLock()
	defer h.state.RUnlock()

	// Build query with optional filters
	query := `SELECT agent_id, kind, role, module, display, registered_at, last_seen_at, agent_pid
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
			&agent.AgentPID,
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

	resolved, peercredRan := peercred.FromContext(ctx)
	dreq := guard.DaemonResolveRequest{
		CallerAgentID: req.CallerAgentID,
		PeercredRan:   peercredRan,
	}
	if resolved != nil {
		dreq.PeercredAgentID = resolved.AgentID
		dreq.PeercredWorktree = resolved.Worktree
	}
	connPID, _ := peercred.ConnectingPIDFromContext(ctx)
	dreq.ConnectingPID = connPID
	dreq.IdentitiesDir = identitiesDirFor(h.state.RepoPath())
	if h.state != nil {
		st := h.state
		dreq.IsAgentInWorktree = func(agentID, worktree string) bool {
			return st.IsAgentInWorktree(context.Background(), agentID, worktree)
		}
	}
	caller, err := guard.DaemonResolve(ctx, loadDaemonGuardConfig(h.state.RepoPath()), dreq, slog.Default())
	if err != nil {
		return nil, fmt.Errorf("resolve identity: %w", err)
	}
	if caller.AgentID == "" {
		return nil, fmt.Errorf("resolve identity: no CallerAgentID and no peercred identity")
	}
	agentID = caller.AgentID
	if resolved != nil && resolved.AgentID == agentID {
		source = "peercred"
	} else if req.CallerAgentID != "" {
		source = "caller"
	}

	// thrum-7nuj: whoami is an explicit liveness probe — advance
	// last_seen so send.recipient-stale hints don't false-positive.
	_ = h.state.TouchAgentLastSeen(ctx, agentID)

	// Look up role/module/display from the agents table.
	h.state.RLock()
	var dbRole, dbModule, dbDisplay sql.NullString
	_ = h.state.DB().QueryRowContext(ctx, "SELECT role, module, display FROM agents WHERE agent_id = ?", agentID).Scan(&dbRole, &dbModule, &dbDisplay)
	h.state.RUnlock()
	if dbRole.Valid {
		role = dbRole.String
	}
	if dbModule.Valid {
		module = dbModule.String
	}
	if dbDisplay.Valid {
		agentName = dbDisplay.String
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

	// hook-inbox-delivery: populate host + identity-file-backed fields.
	response.Host = resolveHostname()

	idsDir := identitiesDirFor(h.state.RepoPath())
	if data, err := os.ReadFile(filepath.Join(idsDir, agentID+".json")); err == nil { // #nosec G304 -- path is .thrum/identities/<agentID>.json
		var idFile config.IdentityFile
		if jsonErr := json.Unmarshal(data, &idFile); jsonErr == nil {
			response.AgentPID = idFile.AgentPID
			response.TmuxSession = idFile.TmuxSession
			if idFile.TmuxSession != "" {
				session, _, _ := ttmux.ParseTarget(idFile.TmuxSession)
				response.TmuxAlive = ttmux.HasSession(session)
			}
		}
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
func (h *AgentHandler) registerAgent(ctx context.Context, agentID, name, role, module, display, worktree, status string, agentPID int) (*RegisterResponse, error) {
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
		AgentPID:  agentPID,
	}

	// Write event to JSONL and SQLite
	if err := h.state.WriteEvent(ctx, event); err != nil {
		return nil, fmt.Errorf("write agent.register event: %w", err)
	}

	// Auto role group creation removed — role-based filtering uses
	// `agent list --role` (direct SQL WHERE role = ?) instead of groups.

	return &RegisterResponse{
		AgentID: agentID,
		Status:  status,
	}, nil
}

// ensureActiveSession checks whether the agent has a row in sessions with
// ended_at IS NULL. If not, and the provided PID is alive, emits a fresh
// agent.session.start event and returns the new session ID.
//
// Returns "" if an active session already exists (idempotent no-op).
// Returns "" if pid is zero or dead (the team.list self-heal path owns
// dead-PID cleanup; resurrect must not race against it).
//
// Must be called under h.state.Lock() held by the caller. This method
// writes a JSONL event via h.state.WriteEvent and does not acquire the
// state lock itself — acquiring a second write lock would deadlock.
//
// Cross-verification discipline (thrum-xir.18, mirroring thrum-pxz.14
// Fix B): both the DB's active-session state and process.IsRunning(pid)
// must agree the agent is alive before any state change is written.
// A single-source decision is the pxz.14 anti-pattern.
func (h *AgentHandler) ensureActiveSession(ctx context.Context, agentID string, pid int) (string, error) {
	// Source of truth #1: DB active-session state.
	var existingID sql.NullString
	err := h.state.DB().QueryRowContext(ctx,
		`SELECT session_id FROM sessions
		 WHERE agent_id = ? AND ended_at IS NULL
		 ORDER BY started_at DESC LIMIT 1`,
		agentID,
	).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("check active session: %w", err)
	}
	if existingID.Valid && existingID.String != "" {
		// Happy path: session already active. Write nothing.
		return "", nil
	}

	// Source of truth #2: live process check. Skip resurrect if PID is
	// missing or dead — the self-heal path owns that case.
	if pid <= 0 || !process.IsRunning(pid) {
		return "", nil
	}

	// Both sources agree: no active session and the caller's process is
	// alive. Emit a minimal agent.session.start event. Deliberately omit
	// scope/orphan-recovery handling that HandleStart performs — those
	// belong to the explicit session.start RPC (used by quickstart), not
	// the lightweight resurrect path.
	sessionID := identity.GenerateSessionID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	event := types.AgentSessionStartEvent{
		Type:      "agent.session.start",
		Timestamp: now,
		SessionID: sessionID,
		AgentID:   agentID,
	}
	if err := h.state.WriteEvent(ctx, event); err != nil {
		return "", fmt.Errorf("write session.start event: %w", err)
	}
	return sessionID, nil
}

// resolveCallerWorktreeFn indirects peercred.ResolveCallerWorktree so tests
// can inject a fault into the primary-resolution path without standing up a
// real /proc entry. Defaults to the production helper. Tests must swap
// back under t.Cleanup.
var resolveCallerWorktreeFn = peercred.ResolveCallerWorktree

// persistResurrectWorktreeRef (thrum-2b2t) resolves the caller's worktree
// via their PID and writes a worktree session_ref + agent_work_contexts
// seed for the resurrected session. Mirrors the rows HandleStart writes
// during the quickstart path.
//
// Primary source: peercred.ResolveCallerWorktree(pid) — walks /proc/<pid>/cwd
// upward to a git root. Most accurate; self-correcting when the agent moves
// worktrees.
//
// Fallback: the most-recent prior session's worktree ref for this agent.
// Logged at debug level when used so future debugging can distinguish
// "fresh PID-based resolution" from "stale fallback-to-prior" (the latter
// is correct graceful degradation when the PID is unreachable, but observably
// different). Known limitation: if the agent has historically lived in
// multiple worktrees, the fallback returns the most-recent one — which may
// no longer match the agent's current location. Pre-fix the agent was
// always anonymous; post-fix + stale-fallback it may resolve to the wrong
// worktree. Acceptable graceful degradation; the debug log captures the
// divergence.
//
// Failure is best-effort — mirrors the resurrect path's own "log and
// continue" discipline (breaking register because a worktree ref can't
// be resolved is worse than leaving the agent briefly anonymous).
//
// Trust boundary: pid is supplied in the RegisterRequest JSON payload,
// not extracted from kernel peer credentials. The daemon listens only on
// the local Unix socket, so only same-user processes can connect — the
// existing G4 liveness check uses this same trusted PID. A malicious local
// caller could supply an arbitrary live PID and get this helper to return
// the git root of another process's CWD, but that discloses only the
// ancestor directory of a same-user process, which is already readable via
// standard POSIX APIs. No privilege escalation.
//
// Concurrency: must be called with h.state.Lock() held (matches
// ensureActiveSession's contract — the two DB writes are performed
// atomically relative to other register/session mutations on the same
// agent). INSERT OR IGNORE + ON CONFLICT DO UPDATE are additionally safe
// against cross-process races via the peer daemon sync layer.
func (h *AgentHandler) persistResurrectWorktreeRef(ctx context.Context, agentID, sessionID string, pid int) {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Primary: resolve from caller's live CWD via their PID.
	worktreePath, err := resolveCallerWorktreeFn(pid)
	if err != nil || worktreePath == "" {
		// Fallback: most-recent prior session's worktree ref for this agent.
		// The JOIN + ref_value filter naturally excludes the newly-created
		// resurrected session (no session_refs row for it yet).
		var prior string
		qErr := h.state.DB().QueryRowContext(ctx, `
			SELECT sr.ref_value
			FROM session_refs sr
			JOIN sessions s ON s.session_id = sr.session_id
			WHERE s.agent_id = ? AND sr.ref_type = 'worktree' AND sr.ref_value != ''
			ORDER BY s.started_at DESC
			LIMIT 1
		`, agentID).Scan(&prior)
		if qErr != nil || prior == "" {
			slog.Debug("thrum-2b2t: worktree ref resolution failed, no fallback",
				"agent", agentID, "session", sessionID, "pid", pid, "primary_err", err, "fallback_err", qErr)
			return
		}
		slog.Debug("thrum-2b2t: using prior-session worktree as fallback",
			"agent", agentID, "session", sessionID, "pid", pid, "fallback_worktree", prior, "primary_err", err)
		worktreePath = prior
	}

	// Persist the worktree session_ref. INSERT OR IGNORE keeps this
	// idempotent if a concurrent caller raced us to the same ref.
	if _, werr := h.state.DB().ExecContext(ctx, `
		INSERT OR IGNORE INTO session_refs (session_id, ref_type, ref_value, added_at)
		VALUES (?, 'worktree', ?, ?)
	`, sessionID, worktreePath, now); werr != nil {
		log.Printf("thrum-2b2t: write session_refs failed: agent=%s session=%s err=%v",
			agentID, sessionID, werr)
		return
	}

	// Seed agent_work_contexts for immediate peercred matching. Mirrors
	// HandleStart at session.go:186-199.
	if _, werr := h.state.DB().ExecContext(ctx, `
		INSERT INTO agent_work_contexts (session_id, agent_id, worktree_path)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET worktree_path = excluded.worktree_path
	`, sessionID, agentID, worktreePath); werr != nil {
		log.Printf("thrum-2b2t: seed agent_work_contexts failed: agent=%s session=%s err=%v",
			agentID, sessionID, werr)
	}
}

// getAgentByID queries for an existing agent with the given agent ID.
func (h *AgentHandler) getAgentByID(ctx context.Context, agentID string) (*AgentInfo, error) {
	query := `SELECT agent_id, kind, role, module, display, registered_at, last_seen_at, agent_pid
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
		&agent.AgentPID,
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

	// Build query with filters — only return contexts for active (non-ended) sessions
	query := `SELECT wc.session_id, wc.agent_id, wc.branch, wc.worktree_path,
	                 wc.unmerged_commits, wc.uncommitted_files, wc.changed_files, wc.file_changes, wc.git_updated_at,
	                 wc.current_task, wc.task_updated_at, wc.intent, wc.intent_updated_at
	          FROM agent_work_contexts wc
	          JOIN sessions s ON wc.session_id = s.session_id AND s.ended_at IS NULL
	          WHERE 1=1`

	args := []any{}

	// Filter by agent_id
	if req.AgentID != "" {
		query += " AND wc.agent_id = ?"
		args = append(args, req.AgentID)
	}

	// Filter by branch
	if req.Branch != "" {
		query += " AND wc.branch = ?"
		args = append(args, req.Branch)
	}

	// Filter by file (in changed_files or uncommitted_files)
	if req.File != "" {
		query += ` AND (wc.changed_files LIKE ? OR wc.uncommitted_files LIKE ?)`
		filePattern := fmt.Sprintf("%%\"%s\"%%", req.File)
		args = append(args, filePattern, filePattern)
	}

	query += " ORDER BY wc.git_updated_at DESC"

	rows, err := h.state.DB().QueryContext(ctx, query, args...)
	if err != nil {
		h.state.Unlock()
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
			h.state.Unlock()
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
		h.state.Unlock()
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	h.state.Unlock()

	// Live git extraction: re-extract from worktree paths so callers see
	// current uncommitted_files / changed_files instead of stale heartbeat data.
	for i := range contexts {
		wc := &contexts[i]
		if wc.WorktreePath == "" {
			continue
		}
		live, err := gitctx.ExtractWorkContext(ctx, wc.WorktreePath)
		if err != nil || live == nil || live.WorktreePath == "" {
			continue // not a valid git repo (e.g. test env), keep cached data
		}
		wc.Branch = live.Branch
		wc.UnmergedCommits = live.UnmergedCommits
		wc.UncommittedFiles = live.UncommittedFiles
		wc.ChangedFiles = live.ChangedFiles
		wc.FileChanges = live.FileChanges
		wc.GitUpdatedAt = live.ExtractedAt.Format(time.RFC3339Nano)
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

	// Remove agent lifecycle events from events.jsonl
	eventsPath := filepath.Join(h.state.SyncDir(), "events.jsonl")
	if _, err := jsonl.RemoveByField(eventsPath, "agent_id", req.Name); err != nil {
		log.Printf("warning: failed to filter events.jsonl for agent %s: %v", req.Name, err)
	}

	// Re-lock for DB delete + event write
	h.state.Lock()

	// Delete orphaned messages for this agent before removing the agent row.
	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_edits WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.Name)
	if err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete message edits for agent: %w", err)
	}
	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_reads WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.Name)
	if err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete message reads for agent: %w", err)
	}
	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_deliveries WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.Name)
	if err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete message deliveries for agent: %w", err)
	}
	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_refs WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.Name)
	if err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete message refs for agent: %w", err)
	}
	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_scopes WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.Name)
	if err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete message scopes for agent: %w", err)
	}
	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM messages WHERE agent_id = ?", req.Name)
	if err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete messages for agent: %w", err)
	}

	// Delete orphaned sessions for this agent.
	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM session_refs WHERE session_id IN (SELECT session_id FROM sessions WHERE agent_id = ?)", req.Name)
	if err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete session refs for agent: %w", err)
	}
	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM session_scopes WHERE session_id IN (SELECT session_id FROM sessions WHERE agent_id = ?)", req.Name)
	if err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete session scopes for agent: %w", err)
	}
	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM sessions WHERE agent_id = ?", req.Name)
	if err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete sessions for agent: %w", err)
	}

	// Delete events referencing this agent.
	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM events WHERE event_json LIKE ?", "%\"agent_id\":\""+req.Name+"\"%")
	if err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete events for agent: %w", err)
	}

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
		identityData, err := os.ReadFile(identityPath) // #nosec G304 -- identityPath is under .thrum/identities/, an internal directory
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

// HandleSetAgentStatus handles the agent.set-status RPC method.
// It finds the target agent's identity file across all worktrees and updates the status.
func (h *AgentHandler) HandleSetAgentStatus(ctx context.Context, params json.RawMessage) (any, error) {
	var req struct {
		Agent  string `json:"agent"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.Agent == "" {
		return nil, errors.New("agent name is required")
	}
	if req.Status != "working" && req.Status != "idle" && req.Status != "blocked" {
		return nil, fmt.Errorf("invalid status %q: must be working, idle, or blocked", req.Status)
	}

	// Search identity dirs across worktrees for the target agent
	idFile, idPath, err := h.findAgentIdentity(ctx, req.Agent)
	if err != nil {
		return nil, fmt.Errorf("find agent %s: %w", req.Agent, err)
	}

	// G4: refuse writes targeting a dead agent's identity file.
	// Mode is loaded from the agent's own .thrum/config.json (the
	// worktree the identity lives under), matching where the agent's
	// other guard decisions anchor. AgentPID=0 means the agent has not
	// been primed yet; G4 applies to dead-after-alive transitions, not
	// pre-prime, so skip the gate for zero PIDs.
	idDir := filepath.Dir(idPath)
	thrumDir := filepath.Dir(idDir) // identities dir is inside .thrum
	if idFile.AgentPID != 0 {
		mode := guard.ConfigForIdentityDir(idDir).DaemonWriterLiveness
		if mode == "" {
			mode = guard.ModeStrict
		}
		if gErr := guard.G4(&guard.WriterContext{
			Mode:       mode,
			SubjectPID: idFile.AgentPID,
			IsPIDAlive: func(pid int) bool { return process.IsRunning(pid) },
		}); gErr != nil {
			return nil, fmt.Errorf("set-status refused for %s: %w", req.Agent, gErr)
		}
	}

	idFile.AgentStatus = req.Status
	idFile.AgentStatusUpdatedAt = time.Now().UTC()

	// Save back to the same directory the file was found in
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		return nil, fmt.Errorf("save identity for %s: %w", req.Agent, err)
	}

	return map[string]string{
		"agent":  req.Agent,
		"status": req.Status,
	}, nil
}

// findAgentIdentity searches all worktree identity directories for the named agent.
func (h *AgentHandler) findAgentIdentity(ctx context.Context, agentName string) (*config.IdentityFile, string, error) {
	filename := agentName + ".json"

	// Check primary identities dir first
	primaryDir := filepath.Join(h.state.RepoPath(), ".thrum", "identities")
	if idFile, path, err := h.tryLoadIdentity(primaryDir, filename); err == nil {
		return idFile, path, nil
	}

	// Scan worktrees via git
	output, err := safecmd.Git(ctx, h.state.RepoPath(), "worktree", "list", "--porcelain")
	if err != nil {
		return nil, "", fmt.Errorf("agent %s not found in primary identity dir", agentName)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			idDir := filepath.Join(path, ".thrum", "identities")
			if idDir == primaryDir {
				continue
			}
			if idFile, idPath, err := h.tryLoadIdentity(idDir, filename); err == nil {
				return idFile, idPath, nil
			}
		}
	}

	return nil, "", fmt.Errorf("agent %s not found in any worktree", agentName)
}

// tryLoadIdentity attempts to load an identity file from a directory.
func (h *AgentHandler) tryLoadIdentity(idDir, filename string) (*config.IdentityFile, string, error) {
	path := filepath.Join(idDir, filename)
	data, err := os.ReadFile(path) // #nosec G304 -- path under .thrum/identities/
	if err != nil {
		return nil, "", err
	}
	var idFile config.IdentityFile
	if err := json.Unmarshal(data, &idFile); err != nil {
		return nil, "", err
	}
	return &idFile, path, nil
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
