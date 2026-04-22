package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/permission"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity/guard"
	"github.com/leonletto/thrum/internal/process"
	"github.com/leonletto/thrum/internal/restart"
	trt "github.com/leonletto/thrum/internal/runtime"
	ttmux "github.com/leonletto/thrum/internal/tmux"
	"github.com/leonletto/thrum/internal/worktree"
)

// Request/Response types for tmux RPC handlers.

// TmuxCreateRequest is the request to create a new tmux session.
type TmuxCreateRequest struct {
	Name      string `json:"name"`
	Cwd       string `json:"cwd"`
	AgentName string `json:"agent_name,omitempty"`
	Role      string `json:"role,omitempty"`
	Module    string `json:"module,omitempty"`
	Intent    string `json:"intent,omitempty"`
	Runtime   string `json:"runtime,omitempty"`
	Force     bool   `json:"force,omitempty"`
	NoAgent   bool   `json:"no_agent,omitempty"`
}

// TmuxCreateResponse is the response after creating a tmux session.
type TmuxCreateResponse struct {
	Session  string               `json:"session"`
	Identity *config.IdentityFile `json:"identity,omitempty"`
}

// TmuxLaunchRequest is the request to launch a runtime in a tmux session.
type TmuxLaunchRequest struct {
	Name    string `json:"name"`
	Runtime string `json:"runtime"`
}

// TmuxLaunchResponse is the response after launching a runtime.
type TmuxLaunchResponse struct {
	Session string `json:"session"`
	Runtime string `json:"runtime"`
}

// TmuxKillRequest is the request to kill a tmux session.
type TmuxKillRequest struct {
	Name string `json:"name"`
}

// TmuxSendRequest is the request to send text to a tmux session.
type TmuxSendRequest struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// TmuxCaptureRequest is the request to capture pane content.
type TmuxCaptureRequest struct {
	Name  string `json:"name"`
	Lines int    `json:"lines"`
}

// TmuxCaptureResponse is the response with captured pane content.
type TmuxCaptureResponse struct {
	Content string `json:"content"`
}

// TmuxSessionInfo describes the state of a managed tmux session.
type TmuxSessionInfo struct {
	Name    string `json:"name"`
	Agent   string `json:"agent,omitempty"`
	Role    string `json:"role,omitempty"`
	Module  string `json:"module,omitempty"`
	State   string `json:"state"` // alive, stale, dead
	Runtime string `json:"runtime,omitempty"`
	Branch  string `json:"branch,omitempty"`
}

// TmuxStatusResponse contains information about all managed tmux sessions.
type TmuxStatusResponse struct {
	Sessions []TmuxSessionInfo `json:"sessions"`
}

// TmuxHandler handles tmux session lifecycle RPC methods.
type TmuxHandler struct {
	thrumDir  string
	state     *state.State
	queues    map[string]*SessionQueue
	queuesMu  sync.Mutex
	sessionMu sync.RWMutex // protects sessionCwds and cwdSessions
	// sessionCwds maps session name → cwd. Populated by HandleCreate
	// from req.Cwd (CLI-supplied, trusted: local-socket-only threat
	// model; the daemon does not serve remote clients). Read by
	// ensureSession (auto-create) and writeTmuxByWorktreeCwd
	// (thrum-51cg Pass 0).
	sessionCwds map[string]string
	cwdSessions map[string]string // cwd → session name, for single-session-per-worktree

	// permission is the optional permission-prompt scheduler. Wired
	// in production via SetPermission right after construction so
	// existing test call sites don't need to thread a real instance
	// through NewTmuxHandler. HandleCheckPane guards every use with
	// a nil-check so tests without permission wiring still pass.
	permission *permission.Permission

	// poller is the optional silence-hash poller that bypasses tmux's
	// unreliable alert-silence hook for detached sessions (tmux issue
	// #1384). Wired via SetPoller in daemon bootstrap. HandleLaunch
	// enrolls sessions; HandleKill unenrolls. Nil-safe for tests.
	poller *permission.SessionPoller
}

// NewTmuxHandler creates a new TmuxHandler.
func NewTmuxHandler(thrumDir string, st *state.State) *TmuxHandler {
	return &TmuxHandler{
		thrumDir:    thrumDir,
		state:       st,
		queues:      make(map[string]*SessionQueue),
		sessionCwds: make(map[string]string),
		cwdSessions: make(map[string]string),
	}
}

// SetPermission installs the permission scheduler. Production daemon
// boot calls this right after NewTmuxHandler to connect the tmux
// check-pane dispatch path to the nudge scheduler. Tests that don't
// need permission semantics can skip this call and HandleCheckPane
// will treat the permission path as a no-op.
func (h *TmuxHandler) SetPermission(p *permission.Permission) {
	h.permission = p
}

// SetPoller installs the silence-hash poller that bypasses tmux's
// alert-silence hook. Production daemon boot calls this so HandleLaunch
// can enroll new sessions and HandleKill can unenroll terminated ones.
// Tests that don't exercise the poller can skip this call — all
// enroll/unenroll sites guard with a nil-check.
func (h *TmuxHandler) SetPoller(p *permission.SessionPoller) {
	h.poller = p
}

// ensureSession checks whether a tmux session exists and auto-creates it
// if the daemon has a stored cwd from a prior HandleCreate. Returns the
// sanitized session name and target (name:0.0). Returns an error if the
// session doesn't exist and no stored cwd is available.
func (h *TmuxHandler) ensureSession(name string) (string, string, error) {
	sanitized := ttmux.SanitizeSessionName(name)
	target := sanitized + ":0.0"

	if ttmux.HasSession(sanitized) {
		return sanitized, target, nil
	}

	// Look up stored cwd from prior create
	h.sessionMu.RLock()
	cwd, ok := h.sessionCwds[sanitized]
	h.sessionMu.RUnlock()
	if !ok {
		return "", "", fmt.Errorf("session %q not found and no stored cwd available", sanitized)
	}

	// Auto-create from stored cwd
	if err := ttmux.CreateSession(sanitized, cwd); err != nil {
		return "", "", fmt.Errorf("auto-create session %q: %w", sanitized, err)
	}

	log.Printf("[tmux] auto-created session %q from stored cwd %s", sanitized, cwd)
	return sanitized, target, nil
}

// getOrCreateQueue returns the queue for a session, creating it if necessary.
func (h *TmuxHandler) getOrCreateQueue(session string) *SessionQueue {
	h.queuesMu.Lock()
	defer h.queuesMu.Unlock()
	q, ok := h.queues[session]
	if !ok {
		q = NewSessionQueue(session)
		h.queues[session] = q
	}
	return q
}

// getQueue returns the queue for a session, or nil if none exists.
func (h *TmuxHandler) getQueue(session string) *SessionQueue {
	h.queuesMu.Lock()
	defer h.queuesMu.Unlock()
	return h.queues[session]
}

// HandleCreate creates a new detached tmux session with monitor-silence hook.
// If quickstart flags are provided (agent_name, role, module), it also sets up
// worktree redirects and runs quickstart inside the pane for PID isolation.
func (h *TmuxHandler) HandleCreate(ctx context.Context, params json.RawMessage) (any, error) {
	var req TmuxCreateRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if req.Cwd == "" {
		return nil, fmt.Errorf("cwd is required")
	}
	if !filepath.IsAbs(req.Cwd) {
		return nil, fmt.Errorf("cwd must be an absolute path, got: %q", req.Cwd)
	}

	// Validate quickstart flags unless --no-agent
	if !req.NoAgent {
		if req.AgentName == "" || req.Role == "" || req.Module == "" {
			return nil, fmt.Errorf("quickstart flags required (agent_name, role, module); set no_agent=true to skip")
		}
	}

	// Ensure worktree redirects (before creating session)
	if !req.NoAgent {
		// Validate cwd is a git worktree (has a .git file, not a .git directory)
		gitPath := filepath.Join(req.Cwd, ".git")
		if info, err := os.Stat(gitPath); err != nil || info.IsDir() {
			return nil, fmt.Errorf("path at %s is not a git worktree", req.Cwd)
		}
		// daemon's thrumDir is <main-repo>/.thrum — strip to get repo root
		mainRepoRoot := filepath.Dir(h.thrumDir)
		if err := worktree.EnsureRedirects(req.Cwd, mainRepoRoot); err != nil {
			return nil, fmt.Errorf("redirect setup: %w", err)
		}
	}

	name := ttmux.SanitizeSessionName(req.Name)

	// Single-session-per-worktree: kill any existing session for this cwd
	h.sessionMu.Lock()
	if existingSession, ok := h.cwdSessions[req.Cwd]; ok && existingSession != name {
		log.Printf("[tmux] single-session-per-worktree: killing %q (cwd %s reassigned to %q)", existingSession, req.Cwd, name)
		_ = ttmux.KillSession(existingSession)
		delete(h.sessionCwds, existingSession)
		delete(h.cwdSessions, req.Cwd)
	}
	h.sessionMu.Unlock()

	// Check for existing session by name
	if ttmux.HasSession(name) {
		if !req.Force {
			return nil, fmt.Errorf("session %q already exists; use --force to kill and recreate", name)
		}
		_ = ttmux.KillSession(name)
	}

	if err := ttmux.CreateSession(name, req.Cwd); err != nil {
		return nil, err
	}

	// Track session→cwd mapping for auto-create and single-session enforcement
	h.sessionMu.Lock()
	h.sessionCwds[name] = req.Cwd
	h.cwdSessions[req.Cwd] = name
	h.sessionMu.Unlock()

	// Set window name and terminal title for tab identification.
	// Uses agent name if provided, falls back to session name.
	// Non-fatal — session is usable without titles.
	windowTitle := name
	if req.AgentName != "" {
		windowTitle = "@" + req.AgentName
	}
	if err := ttmux.RenameWindow(name, windowTitle); err != nil {
		slog.Warn("tmux rename-window failed", "session", name, "title", windowTitle, "error", err)
	}
	if err := ttmux.SetSessionTitle(name, windowTitle); err != nil {
		slog.Warn("tmux set-titles failed", "session", name, "title", windowTitle, "error", err)
	}

	// Set up monitor-silence hook (non-fatal if it fails)
	thrumBin, _ := os.Executable()
	if thrumBin != "" {
		_ = ttmux.SetMonitorSilence(name, 60, thrumBin, h.thrumDir)
	}

	// Run quickstart inside the pane for PID isolation
	if !req.NoAgent {
		if err := h.runInlineQuickstart(ctx, req, name); err != nil {
			return nil, err
		}
	} else {
		// For bare sessions, still write tmux_session to any existing identity
		target := name + ":0.0"
		h.writeTmuxToIdentity(name, target, "")
	}

	// Try to read identity file from worktree
	var identity *config.IdentityFile
	if idFile, _, err := config.LoadIdentityWithPath(req.Cwd); err == nil {
		identity = idFile
	}

	return &TmuxCreateResponse{Session: name, Identity: identity}, nil
}

// HandleLaunch sends a runtime command into an existing tmux session.
func (h *TmuxHandler) HandleLaunch(ctx context.Context, params json.RawMessage) (any, error) {
	var req TmuxLaunchRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	name, target, err := h.ensureSession(req.Name)
	if err != nil {
		return nil, err
	}

	runtime := req.Runtime
	if runtime == "" {
		runtime = "claude"
	}

	// Validate runtime name to prevent shell injection via SendKeys
	if !isValidRuntimeName(runtime) {
		return nil, fmt.Errorf("invalid runtime name %q: must contain only alphanumeric, hyphen, or underscore characters", runtime)
	}

	launchCmd := runtimeToLaunchCmd(runtime)
	if launchCmd != "" {
		if err := ttmux.SendKeys(target, launchCmd); err != nil {
			return nil, fmt.Errorf("launch send-keys: %w", err)
		}
		if err := ttmux.SendSpecialKey(target, "Enter"); err != nil {
			return nil, fmt.Errorf("launch enter: %w", err)
		}

		// Send the runtime-appropriate prime command after startup delay.
		// Runs in a goroutine so the RPC returns immediately.
		primeCmd := primeCommandForRuntime(runtime)
		go func() {
			time.Sleep(10 * time.Second)
			_ = ttmux.SendKeys(target, primeCmd)
			_ = ttmux.SendSpecialKey(target, "Enter")
			// TUI runtimes (e.g. OpenCode) may swallow the first Enter during
			// startup. Retry after a brief pause as a fallback.
			time.Sleep(3 * time.Second)
			_ = ttmux.SendSpecialKey(target, "Enter")
		}()
	}

	// Preamble: null the stored agent_pid if it belongs to an exited
	// process. Without this, writeTmuxToIdentity's Pass 0 trips G4
	// strict writer-liveness on the tmux-create inline-quickstart
	// subshell PID and tmux_session never gets written. After the
	// clear, Pass 0's subjectPID=0 skip applies and the write succeeds.
	// First /thrum:prime then reclaims the PID via guard.WritePID at
	// cmd/thrum/main.go:4060-4064 (thrum-x6e8.6).
	h.clearStalePIDForLaunch(name)

	// Write tmux_session and runtime to the agent's identity file
	h.writeTmuxToIdentity(name, target, runtime)

	// Part 3 regression guard: if tmux_session is still empty after the
	// full write pass, emit an observable warn so future breakage of
	// this invariant doesn't silently drift back in.
	h.warnIfTmuxSessionEmpty(name)

	// Enroll the session in the silence-hash poller. Runs detached from
	// tmux's alert-silence hook (which is unreliable on detached
	// sessions — tmux issue #1384). Nil-safe: tests without poller
	// wiring proceed unchanged.
	if h.poller != nil {
		h.poller.Enroll(name, runtime, target)
	}

	return &TmuxLaunchResponse{Session: name, Runtime: runtime}, nil
}

// HandleKill destroys a tmux session and clears tmux_session from identity files.
func (h *TmuxHandler) HandleKill(ctx context.Context, params json.RawMessage) (any, error) {
	var req TmuxKillRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	name := ttmux.SanitizeSessionName(req.Name)
	h.drainQueueOnKill(ctx, name)
	h.clearTmuxFromIdentities(name)

	// Clean up session tracking maps
	h.sessionMu.Lock()
	if cwd, ok := h.sessionCwds[name]; ok {
		delete(h.cwdSessions, cwd)
	}
	delete(h.sessionCwds, name)
	h.sessionMu.Unlock()

	// Remove from the silence-hash poller. Safe if never enrolled or
	// if the poller isn't wired (tests).
	if h.poller != nil {
		h.poller.Unenroll(name)
	}

	return nil, ttmux.KillSession(name)
}

// HandleSend sends text to a tmux session pane by routing through the queue
// for unified safe dispatch.
func (h *TmuxHandler) HandleSend(ctx context.Context, params json.RawMessage) (any, error) {
	var req TmuxSendRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	// Route through queue for unified safe dispatch
	queueParams, _ := json.Marshal(map[string]any{
		"session":   req.Name,
		"text":      req.Text,
		"requester": "tmux-send",
	})
	return h.HandleQueue(ctx, queueParams)
}

// HandleCapture captures the visible content of a tmux session pane.
func (h *TmuxHandler) HandleCapture(ctx context.Context, params json.RawMessage) (any, error) {
	var req TmuxCaptureRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	lines := req.Lines
	if lines <= 0 {
		lines = 50
	}
	_, target, err := h.ensureSession(req.Name)
	if err != nil {
		return nil, err
	}
	content, err := ttmux.CapturePane(target, lines)
	if err != nil {
		return nil, err
	}
	return &TmuxCaptureResponse{Content: content}, nil
}

// HandleStatus scans identity files across all worktrees for managed tmux sessions.
func (h *TmuxHandler) HandleStatus(ctx context.Context, params json.RawMessage) (any, error) {
	dirs := AllIdentityDirs(ctx, h.thrumDir)

	seen := make(map[string]bool) // deduplicate by session name
	var sessions []TmuxSessionInfo

	for _, identitiesDir := range dirs {
		entries, err := os.ReadDir(identitiesDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}

			data, err := os.ReadFile(filepath.Join(identitiesDir, entry.Name())) // #nosec G304 -- path is .thrum/identities/<name>.json
			if err != nil {
				continue
			}
			var idFile config.IdentityFile
			if err := json.Unmarshal(data, &idFile); err != nil {
				continue
			}
			if idFile.TmuxSession == "" {
				continue
			}

			session, _, _ := ttmux.ParseTarget(idFile.TmuxSession)
			if seen[session] {
				continue
			}
			seen[session] = true

			info := TmuxSessionInfo{
				Name:    session,
				Agent:   idFile.Agent.Name,
				Role:    idFile.Agent.Role,
				Module:  idFile.Agent.Module,
				Runtime: idFile.Runtime,
				Branch:  idFile.Branch,
			}

			if !ttmux.HasSession(session) {
				// Report as dead but do NOT clear identity — status is a read
				// operation. HandleKill/HandleRestart own the write paths.
				info.State = "dead"
			} else if idFile.AgentPID > 0 && !process.IsRunning(idFile.AgentPID) {
				info.State = "stale"
			} else {
				info.State = "alive"
			}

			sessions = append(sessions, info)
		}
	}

	return &TmuxStatusResponse{Sessions: sessions}, nil
}

// CheckPaneRequest is the request from the tmux silence hook.
type CheckPaneRequest struct {
	Session string `json:"session"`
	Reason  string `json:"reason"`
	Content string `json:"content"`
}

// CheckPaneResponse reports what the check-pane handler detected.
type CheckPaneResponse struct {
	Session string `json:"session"`
	State   string `json:"state"` // idle, permission, normal
	Reason  string `json:"reason,omitempty"`
}

// HandleCheckPane is the handler for the tmux check-pane silence hook.
// It distinguishes four outcomes per session fire:
//
//  1. permission — req.Reason is a "permission:<runtime>.<name>"
//     pattern key. Dispatches to permission.OnDetection to schedule
//     or advance a supervisor nudge.
//  2. command_completed / command_sent — queue-aware dispatch for
//     the silence-based command pipeline.
//  3. working_but_idle — agent self-reported "working" but the pane
//     is silent; sends a nudge to the agent to resync.
//  4. idle — true idle. Triggers permission.OnRecovery to clear any
//     pending nudge and stuck marker from a prior prompt the agent
//     has since resolved on its own.
func (h *TmuxHandler) HandleCheckPane(ctx context.Context, params json.RawMessage) (any, error) {
	var req CheckPaneRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Resolve identity once at the top — the CLI does not (and cannot
	// reliably) know the agent's runtime because the tmux hook fires
	// from the tmux-server cwd, not the agent's worktree. The daemon
	// is the only layer that can authoritatively map
	// session → identity → runtime. Result is reused by the detection
	// fallback below and by the permission + idle branches further
	// down to avoid repeated identity file scans.
	agentName, idFile, _ := h.findIdentityForSession(ctx, req.Session)

	// Detection fallback: if the CLI did not pre-compute a reason
	// (the production path — CLI just forwards session+content now),
	// run DetectPaneState here using the runtime from the identity
	// file. Empty runtime or empty content falls through to idle,
	// matching DetectPaneState's contract for pre-quickstart or
	// legacy agents.
	if req.Reason == "" && idFile != nil && idFile.Runtime != "" && req.Content != "" {
		req.Reason = permission.DetectPaneState(idFile.Runtime, req.Content)
	}

	// Named paneState rather than `state` so the local doesn't
	// shadow the conceptual "h.state field" readability anchor —
	// future code adding h.state.WriteEvent() inside this function
	// used to hit a type error on the shadowed local string.
	paneState := "idle"
	if req.Reason != "" {
		paneState = "permission"
	}

	log.Printf("[tmux] check-pane: session=%s state=%s reason=%s", req.Session, paneState, req.Reason)

	// Permission branch: parse the reason string back into a runtime
	// + pattern name, resolve the Pattern, and hand off to the
	// scheduler. If anything in the parse/lookup path fails, log and
	// fall through with state="permission" — the daemon has nothing
	// actionable to do, but the CheckPaneResponse still reflects
	// that a permission prompt was detected.
	if paneState == "permission" && h.permission != nil {
		runtime, patternName, ok := parsePermissionReason(req.Reason)
		if !ok {
			log.Printf("[tmux] check-pane: malformed permission reason %q", req.Reason)
		} else if matched := permission.LookupPattern(runtime, patternName); matched == nil {
			log.Printf("[tmux] check-pane: unknown pattern %q", req.Reason)
		} else {
			tmuxTarget := req.Session + ":0.0"
			if idFile != nil && idFile.TmuxSession != "" {
				tmuxTarget = idFile.TmuxSession
			}
			if err := h.permission.OnDetection(ctx, req.Session, runtime, tmuxTarget, agentName, matched, req.Content); err != nil {
				log.Printf("[tmux] check-pane: OnDetection failed: %v", err)
			}
		}
	}

	// Queue-aware dispatch: check for active command or queued command waiting
	if paneState == "idle" {
		if queue := h.getQueue(req.Session); queue != nil {
			// Case 1: active command → silence means it completed
			if active := queue.Active(); active != nil {
				h.completeCommand(ctx, req.Session, queue, active)
				paneState = "command_completed"
			} else if waiting := queue.Peek(); waiting != nil {
				// Case 2: front-of-queue waiting for silence → safe to send it
				h.sendQueuedCommand(ctx, req.Session, queue, waiting)
				paneState = "command_sent"
			}
		}
	}

	// Check for status mismatch: agent says "working" but pane is idle.
	// Runs only if no queue action was taken above. Reuses agentName
	// and idFile resolved at the top of the function.
	if paneState == "idle" {
		if idFile != nil && idFile.AgentStatus == "working" {
			paneState = "working_but_idle"
			target := resolveNudgeTarget(h.thrumDir, agentName)
			if target != "" {
				_ = ttmux.Nudge(target, "daemon")
			}
		}
	}

	// Recovery path: run OnRecovery whenever the pane is NOT in an
	// active permission-prompt state. This covers idle, command_completed,
	// command_sent, and working_but_idle — all cases where the agent has
	// cleared the prompt on its own. The original guard gated this on
	// paneState=="idle", which skipped cleanup when the command_completed
	// branch had already flipped paneState (thrum-4ten regression).
	// Explicitly excluding paneState=="permission" prevents wiping a row
	// that OnDetection just inserted in this same handler invocation.
	// OnRecovery is idempotent (no-op when no row exists). Best-effort;
	// errors are logged but don't fail the RPC.
	if h.permission != nil && paneState != "permission" {
		if err := h.permission.OnRecovery(ctx, req.Session, agentName); err != nil {
			log.Printf("[tmux] check-pane: OnRecovery failed: %v", err)
		}
	}

	return &CheckPaneResponse{
		Session: req.Session,
		State:   paneState,
		Reason:  req.Reason,
	}, nil
}

// parsePermissionReason splits a reason string of the form
// "permission:<runtime>.<pattern_name>" into its two components.
// Returns (runtime, name, true) on success and ("", "", false) on
// any malformation (missing prefix, missing dot, empty halves).
func parsePermissionReason(reason string) (runtime, name string, ok bool) {
	rest, hasPrefix := strings.CutPrefix(reason, "permission:")
	if !hasPrefix {
		return "", "", false
	}
	runtime, name, found := strings.Cut(rest, ".")
	if !found || runtime == "" || name == "" {
		return "", "", false
	}
	return runtime, name, true
}

// writeTmuxToIdentity writes tmux_session and runtime to the identity file
// for the agent whose session matches the given name, scanning all worktrees.
//
// thrum-51cg: a new worktree-path pass runs first when HandleCreate stored
// the session→cwd mapping in sessionCwds. Post-γ-reset, the identity file's
// stale TmuxSession value may point at a dead session and the agent name
// often doesn't sanitize to the new session name — so Pass 1 and Pass 2
// both fail to match and the stale value persists. The worktree-path pass
// fixes that by binding the session to the identity file colocated in the
// session's cwd, which is the user's mental model (worktree IS the binding).
// EnforceOneIdentity guarantees a single identity per worktree; if that
// invariant is violated at runtime, the new pass logs a warning and falls
// back to Pass 1/2 so we don't mass-flap mis-registered files.
//
// Legacy passes preserved:
//
//	Pass 1: match by existing tmux_session association.
//	Pass 2: match by agent name (first launch, no tmux_session yet).
func (h *TmuxHandler) writeTmuxToIdentity(sessionName, target, runtime string) {
	// Pass 0 (thrum-51cg): worktree-path pass via sessionCwds.
	if h.writeTmuxByWorktreeCwd(sessionName, target, runtime) {
		return
	}

	var nameMatch *config.IdentityFile
	var nameMatchDir string

	for _, identitiesDir := range AllIdentityDirs(context.Background(), h.thrumDir) {
		entries, _ := os.ReadDir(identitiesDir)
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			path := filepath.Join(identitiesDir, entry.Name())
			data, err := os.ReadFile(path) // #nosec G304 -- path is .thrum/identities/<name>.json
			if err != nil {
				continue
			}
			var idFile config.IdentityFile
			if err := json.Unmarshal(data, &idFile); err != nil {
				continue
			}
			// Pass 1: Match by existing tmux_session association
			if idFile.TmuxSession != "" {
				sess, _, _ := ttmux.ParseTarget(idFile.TmuxSession)
				if sess == sessionName {
					if gErr := h.checkWriterLiveness(idFile.AgentPID); gErr != nil {
						return
					}
					idFile.TmuxSession = target
					idFile.Runtime = runtime
					_ = config.SaveIdentityFile(filepath.Dir(identitiesDir), &idFile)
					return // write to first matching identity
				}
			}
			// Pass 2 candidate: match by agent name
			if nameMatch == nil && ttmux.SanitizeSessionName(idFile.Agent.Name) == sessionName {
				copied := idFile
				nameMatch = &copied
				nameMatchDir = identitiesDir
			}
		}
	}
	// Fallback: match by agent name (first launch, no tmux_session yet)
	if nameMatch != nil {
		if gErr := h.checkWriterLiveness(nameMatch.AgentPID); gErr != nil {
			return
		}
		nameMatch.TmuxSession = target
		nameMatch.Runtime = runtime
		_ = config.SaveIdentityFile(filepath.Dir(nameMatchDir), nameMatch)
	}
}

// writeTmuxByWorktreeCwd (thrum-51cg Pass 0) binds a tmux session to the
// single identity file in the session's cwd when sessionCwds has that
// mapping. Returns true if a write happened (caller should stop); false on
// any fallback condition (no cwd mapping, empty identities dir, >1 file in
// the dir, or G4 refusal) so the caller proceeds to Pass 1/2.
func (h *TmuxHandler) writeTmuxByWorktreeCwd(sessionName, target, runtime string) bool {
	cwd, ok := h.sessionCwd(sessionName)
	if !ok {
		return false
	}

	// Enumerate first (rather than calling soleIdentityFile directly) so
	// the multi-file invariant-violation warn still fires on this path —
	// HandleLaunch's preamble uses soleIdentityFile silently because
	// ClearPIDIfDead is best-effort, but Pass 0 wants operator signal.
	files := identityFilesInWorktree(cwd)
	if len(files) == 0 {
		return false
	}
	if len(files) > 1 {
		slog.Warn("tmux 51cg: multiple identity files in worktree — falling back to name/session match",
			"worktree_cwd", cwd, "identity_files", files)
		return false
	}

	path := files[0]
	data, err := os.ReadFile(path) // #nosec G304 -- path is .thrum/identities/<name>.json under a cwd we already trust
	if err != nil {
		return false
	}
	var idFile config.IdentityFile
	if err := json.Unmarshal(data, &idFile); err != nil {
		return false
	}
	if gErr := h.checkWriterLiveness(idFile.AgentPID); gErr != nil {
		// G4 refusal: dead PID. Fall through to Pass 1/2 rather than
		// claiming we handled the write — Pass 1/2 will hit the same
		// gate and refuse for the same reason, but returning false
		// keeps the caller's code path explicit and leaves room for a
		// future matching pass that might legitimately succeed under a
		// different G4 mode. Addresses dual-review finding.
		return false
	}
	idFile.TmuxSession = target
	idFile.Runtime = runtime
	// Write directly to the known path (atomic rename via temp file)
	// rather than re-deriving via SaveIdentityFile(filepath.Dir(idDir)),
	// which relies on an implicit path-building convention. The atomic
	// rename also closes the TOCTOU window against concurrent readers
	// (e.g., the Option B self-heal in team.list running at the same
	// time as a HandleLaunch). Addresses dual-review findings.
	if werr := writeIdentityFileAtomic(path, &idFile); werr != nil {
		log.Printf("[tmux 51cg] write identity %s failed: %v", path, werr)
	}
	return true
}

// writeIdentityFileAtomic writes idFile to path via a temp-file + rename
// sequence so concurrent readers either see the pre-update contents or
// the post-update contents, never a partial write. Used by the
// thrum-51cg Pass 0 writer and clearDeadTmuxSessionInIdentity.
func writeIdentityFileAtomic(path string, idFile *config.IdentityFile) error {
	data, err := json.MarshalIndent(idFile, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".identity-*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if rename doesn't happen.
	defer func() {
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// clearDeadTmuxSessionInIdentity (thrum-51cg Option B) clears the
// TmuxSession and Runtime fields from the identity file at path when the
// TmuxSession points at a dead (non-existent) tmux session. Idempotent —
// a file whose TmuxSession is already empty is a no-op.
//
// Used by team.list enrichment as defense-in-depth self-heal: external
// kills (γ reset via raw `tmux kill-session`, or a pane exit) bypass
// HandleKill's clearTmuxFromIdentities, leaving stale bindings in the
// identity file. Self-healing on the next team.list read catches those
// without requiring an explicit reconciliation RPC.
//
// Does NOT run the G4 liveness gate — the subject agent may still be
// alive (the session died underneath them), and we want to unstick that
// scenario. The write scope is intentionally narrow (two tmux fields);
// no other identity data is mutated.
func clearDeadTmuxSessionInIdentity(path string) error {
	data, err := os.ReadFile(path) // #nosec G304 -- path is a discovered identity file
	if err != nil {
		return err
	}
	var idFile config.IdentityFile
	if err := json.Unmarshal(data, &idFile); err != nil {
		return err
	}
	if idFile.TmuxSession == "" && idFile.Runtime == "" {
		return nil
	}
	idFile.TmuxSession = ""
	idFile.Runtime = ""
	// Atomic rename closes the TOCTOU window against concurrent
	// writers (Pass 0 via HandleLaunch) so Option B self-heal cannot
	// silently overwrite a just-completed Pass 0 write with its
	// cleared snapshot.
	return writeIdentityFileAtomic(path, &idFile)
}

// checkWriterLiveness gates a daemon-side identity mutation through G4.
// Writes are refused when the subject agent's PID has exited. The daemon
// only writes through this path for locally-managed agents; cross-daemon
// mirror writes arrive via event replay elsewhere, so OriginDaemon is
// left unset (treated as "local") and liveness is always checked.
// Returns nil to proceed, *guard.Error to abort.
func (h *TmuxHandler) checkWriterLiveness(subjectPID int) error {
	// AgentPID=0 means the agent has not been primed yet; G4 applies to
	// dead-after-alive state transitions, not pre-prime. Skip the gate
	// so first-launch tmux wire-up still works.
	if subjectPID == 0 {
		return nil
	}
	// TmuxHandler.thrumDir is the .thrum directory itself; identitiesDir
	// under it provides the anchor guardConfigForIdentityDir expects.
	mode := guard.ConfigForIdentityDir(filepath.Join(h.thrumDir, "identities")).DaemonWriterLiveness
	if mode == "" {
		mode = guard.ModeStrict
	}
	return guard.G4(&guard.WriterContext{
		Mode:       mode,
		SubjectPID: subjectPID,
		IsPIDAlive: func(pid int) bool { return process.IsRunning(pid) },
	})
}

// clearTmuxFromIdentities removes tmux_session and runtime from identity files
// matching the given session name, scanning all worktrees.
func (h *TmuxHandler) clearTmuxFromIdentities(sessionName string) {
	for _, dir := range AllIdentityDirs(context.Background(), h.thrumDir) {
		h.clearTmuxFromIdentitiesInDir(dir, sessionName)
	}
}

// TmuxRestartRequest is the request to restart a tmux-managed agent session.
type TmuxRestartRequest struct {
	Name    string `json:"name"`
	Force   bool   `json:"force,omitempty"`
	Runtime string `json:"runtime,omitempty"`
}

// TmuxRestartResponse is the response after a restart.
type TmuxRestartResponse struct {
	Session       string `json:"session"`
	SnapshotLines int    `json:"snapshot_lines"`
}

// HandleRestart orchestrates the full restart cycle: snapshot → kill → relaunch.
func (h *TmuxHandler) HandleRestart(ctx context.Context, params json.RawMessage) (any, error) {
	var req TmuxRestartRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	name, _, err := h.ensureSession(req.Name)
	if err != nil {
		return nil, err
	}

	// Find the agent's identity file to get PID and runtime
	agentName, idFile, idDir := h.findIdentityForSession(ctx, name)
	if agentName == "" {
		return nil, fmt.Errorf("no identity file found for session %s", name)
	}

	runtime := req.Runtime
	if runtime == "" {
		runtime = idFile.Runtime
	}
	if runtime == "" {
		runtime = "claude"
	}

	// Resolve worktree to path BEFORE killing — if resolution fails, keep the session alive.
	// IdentityFile.Worktree is a bare name like "team-fix".
	repoDir := filepath.Dir(h.thrumDir)
	cwd := resolveWorktreePath(ctx, repoDir, idFile.Worktree)
	if cwd == "" {
		// Fallback: if worktree matches the repo itself
		if filepath.Base(repoDir) == idFile.Worktree || idFile.Worktree == "" {
			cwd = repoDir
		}
	}
	if cwd == "" {
		return nil, fmt.Errorf("cannot resolve worktree %q to a path for %s", idFile.Worktree, agentName)
	}

	snapshotLines := 0
	wtThrumDir := filepath.Dir(idDir) // identities/ parent is .thrum/

	// Graceful flow: ask agent to save its own snapshot before killing.
	// Force flow: extract snapshot directly from JSONL conversation logs.
	if !req.Force && !restart.SnapshotExists(wtThrumDir, agentName) {
		// Delete any stale snapshot/consumed so we can detect a fresh one.
		restart.DeleteSnapshot(wtThrumDir, agentName)

		// Send a message requesting the agent save its snapshot.
		h.sendSystemMessage(ctx, agentName,
			"Restart requested. Please save your context now using `/thrum:restart`.")

		// Nudge the tmux session so the agent sees the message immediately.
		// InterruptNudge is intentional here: the restart flow needs to
		// interrupt in-progress work so the agent can save its snapshot.
		target := name + ":0.0"
		_ = ttmux.InterruptNudge(target, "system")

		// Poll for the snapshot to appear, up to GracefulTimeout.
		cfg, _ := config.LoadThrumConfig(wtThrumDir)
		timeout := time.Duration(cfg.Restart.RestartGracefulTimeout()) * time.Second
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if restart.SnapshotExists(wtThrumDir, agentName) {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}

	// Force flow fallback: extract JSONL snapshot if no snapshot exists yet.
	// Only for Claude — other runtimes don't use this conversation file format.
	if !restart.SnapshotExists(wtThrumDir, agentName) &&
		idFile.AgentPID > 0 &&
		(idFile.Runtime == "" || idFile.Runtime == "claude") {
		homeDir, _ := os.UserHomeDir()
		claudeDir := filepath.Join(homeDir, ".claude")
		if jsonlPath, err := restart.FindSessionJSONL(claudeDir, idFile.AgentPID); err == nil {
			cfg, _ := config.LoadThrumConfig(wtThrumDir)
			maxLines := cfg.Restart.RestartMaxLines()
			if conversation, err := restart.ExtractConversation(jsonlPath, maxLines); err == nil {
				snapshot := restart.FormatRestartSnapshot(agentName, idFile.SessionID, "external", conversation)
				if err := restart.SaveSnapshot(wtThrumDir, agentName, snapshot); err == nil {
					snapshotLines = strings.Count(snapshot, "\n")
				}
			}
		}
	}

	// Count snapshot lines if one was saved (either gracefully or by force).
	if snapshotLines == 0 && restart.SnapshotExists(wtThrumDir, agentName) {
		if data, err := os.ReadFile(filepath.Join(wtThrumDir, "restart", agentName+".md")); err == nil { // #nosec G304
			snapshotLines = strings.Count(string(data), "\n")
		}
	}

	// Kill existing session
	h.clearTmuxFromIdentitiesInDir(idDir, name)
	if err := ttmux.KillSession(name); err != nil {
		return nil, fmt.Errorf("kill session: %w", err)
	}

	// Create and launch new session
	if err := ttmux.CreateSession(name, cwd); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	thrumBin, _ := os.Executable()
	_ = ttmux.SetMonitorSilence(name, 60, thrumBin, h.thrumDir)

	target := name + ":0.0"
	launchCmd := runtimeToLaunchCmd(runtime)
	if launchCmd != "" {
		if err := ttmux.SendKeys(target, launchCmd); err != nil {
			return nil, fmt.Errorf("send launch command: %w", err)
		}
		if err := ttmux.SendSpecialKey(target, "Enter"); err != nil {
			return nil, fmt.Errorf("send enter: %w", err)
		}

		// Send the runtime-appropriate prime command after startup delay.
		// For restart, this is critical — prime loads the restart snapshot.
		primeCmd := primeCommandForRuntime(runtime)
		go func() {
			time.Sleep(10 * time.Second)
			_ = ttmux.SendKeys(target, primeCmd)
			_ = ttmux.SendSpecialKey(target, "Enter")
			// TUI runtimes (e.g. OpenCode) may swallow the first Enter during
			// startup. Retry after a brief pause as a fallback.
			time.Sleep(3 * time.Second)
			_ = ttmux.SendSpecialKey(target, "Enter")
		}()
	}

	// Write tmux_session and runtime to the agent's identity file
	h.writeTmuxToIdentity(name, target, runtime)

	// Re-enroll the session in the silence-hash poller. HandleKill
	// earlier in the restart sequence unenrolled the old session; the
	// new session needs a fresh enrollment to resume polling. Without
	// this, any thrum tmux restart silently disables permission-prompt
	// detection until the daemon next restarts. Nil-safe for tests.
	if h.poller != nil {
		h.poller.Enroll(name, runtime, target)
	}

	return &TmuxRestartResponse{
		Session:       name,
		SnapshotLines: snapshotLines,
	}, nil
}

// runtimeToLaunchCmd converts a runtime name to the CLI command to launch it.
// Presets in internal/runtime are the single source of truth for the binary
// name — this function delegates to GetPreset so launch and detection stay in
// sync. The "shell" runtime is a special case that launches no tool (returns
// empty string so the caller skips send-keys).
func runtimeToLaunchCmd(runtime string) string {
	if runtime == "shell" {
		return ""
	}
	if preset, err := trt.GetPreset(runtime); err == nil {
		return preset.Command
	}
	// Unknown runtime: best-effort fallback to the runtime name.
	return runtime
}

// isValidRuntimeName checks that a runtime name contains only safe characters.
func isValidRuntimeName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// primeCommandForRuntime returns the command to send after launch for each
// supported runtime. Each runtime has its own skill-invocation syntax:
//   - Claude Code: /thrum:prime (plugin namespace, colon-separated)
//   - Open Code: /thrum-prime (slash + skill name)
//   - Codex: $thrum-prime (dollar-prefix skill invocation; slash commands
//     with colons are rejected as unrecognized)
func primeCommandForRuntime(runtime string) string {
	switch runtime {
	case "opencode":
		return "/thrum-prime"
	case "codex":
		return "$thrum-prime"
	default:
		return "/thrum:prime"
	}
}

// resolveWorktreePath uses git worktree list to find the absolute path for a worktree name.
func resolveWorktreePath(ctx context.Context, repoDir, worktreeName string) string {
	out, err := safecmd.Git(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			if filepath.Base(path) == worktreeName {
				return path
			}
		}
	}
	return ""
}

// ReconcilePoller enrolls all currently-live thrum-managed tmux
// sessions in the silence-hash poller. Called by daemon bootstrap
// after SetPoller so the poller picks up sessions that existed across
// daemon restart. Safe to call with nil poller (tests).
//
// Enrolls any identity file where: tmux_session is non-empty AND
// runtime is non-empty AND the tmux session exists on this host. Other
// identity files are skipped.
func (h *TmuxHandler) ReconcilePoller(ctx context.Context) int {
	if h.poller == nil {
		return 0
	}
	count := 0
	for _, idDir := range AllIdentityDirs(ctx, h.thrumDir) {
		entries, _ := os.ReadDir(idDir)
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			path := filepath.Join(idDir, entry.Name())
			data, err := os.ReadFile(path) // #nosec G304
			if err != nil {
				continue
			}
			var idFile config.IdentityFile
			if err := json.Unmarshal(data, &idFile); err != nil {
				continue
			}
			if idFile.TmuxSession == "" || idFile.Runtime == "" {
				continue
			}
			sess, _, _ := ttmux.ParseTarget(idFile.TmuxSession)
			if sess == "" {
				continue
			}
			if !ttmux.HasSession(sess) {
				continue
			}
			h.poller.Enroll(sess, idFile.Runtime, idFile.TmuxSession)
			count++
		}
	}
	return count
}

// findIdentityForSession searches all worktree identity dirs for an agent
// associated with the given tmux session name.
func (h *TmuxHandler) findIdentityForSession(ctx context.Context, sessionName string) (string, *config.IdentityFile, string) {
	for _, idDir := range AllIdentityDirs(ctx, h.thrumDir) {
		entries, _ := os.ReadDir(idDir)
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			path := filepath.Join(idDir, entry.Name())
			data, err := os.ReadFile(path) // #nosec G304
			if err != nil {
				continue
			}
			var idFile config.IdentityFile
			if err := json.Unmarshal(data, &idFile); err != nil {
				continue
			}
			sess, _, _ := ttmux.ParseTarget(idFile.TmuxSession)
			if sess == sessionName {
				agentName := strings.TrimSuffix(entry.Name(), ".json")
				return agentName, &idFile, idDir
			}
		}
	}
	return "", nil, ""
}

// identityPollInterval is the cadence used by waitForIdentityFile to
// stat the target path. Exposed as a var for test overrides; production
// callers should not mutate at runtime.
var identityPollInterval = 500 * time.Millisecond

// tmux-side-effect seams. Package vars so unit tests can exercise
// runInlineQuickstart end-to-end without a real tmux daemon. Tests
// must restore the original values via t.Cleanup.
var (
	sendKeysFn       = ttmux.SendKeys
	sendSpecialKeyFn = ttmux.SendSpecialKey
	killSessionFn    = ttmux.KillSession
)

// waitForIdentityFile blocks until the identity file at idPath appears
// on disk, or the combined initial+retry window expires. Between the
// two windows, `resend` is invoked once — shell init (oh-my-zsh etc.)
// can swallow the first send-keys, and a second attempt usually lands.
//
// The caller's ctx is honored: cancellation returns ctx.Err() even
// while we would otherwise still be waiting. This matters for daemon
// graceful-shutdown and client disconnects — without ctx-awareness a
// client that drops mid-create would burn the full 10s budget.
//
// Returns nil on success. On resend-function failure the wrapped error
// is returned immediately. On combined-timeout a descriptive error is
// returned including the total window waited.
//
// Pre thrum-ns0b this logic ran in a background goroutine, so
// HandleCreate returned before the identity file existed. That raced
// a back-to-back `thrum tmux launch` which could not find any identity
// files to bind tmux_session to (writeTmuxToIdentity fell through all
// three passes). Running synchronously closes the race at the cost of
// up to ~10s latency on HandleCreate when the shell is slow.
func waitForIdentityFile(ctx context.Context, idPath string, initial, retry time.Duration, resend func() error) error {
	if ok, ctxErr := waitUntilExists(ctx, idPath, initial); ok {
		return nil
	} else if ctxErr != nil {
		return ctxErr
	}
	if resend != nil {
		if err := resend(); err != nil {
			return fmt.Errorf("re-send quickstart after initial %s wait: %w", initial, err)
		}
	}
	if ok, ctxErr := waitUntilExists(ctx, idPath, retry); ok {
		return nil
	} else if ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("quickstart did not write identity file %s within %s", idPath, initial+retry)
}

// waitUntilExists polls idPath every identityPollInterval up to the
// given duration. Returns (true, nil) as soon as os.Stat succeeds,
// (false, nil) on deadline, and (false, ctx.Err()) on context
// cancellation. Never overshoots the caller's deadline by more than
// the time of a single os.Stat call.
func waitUntilExists(ctx context.Context, path string, within time.Duration) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return true, nil
	}
	deadline := time.After(within)
	ticker := time.NewTicker(identityPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline:
			return false, nil
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				return true, nil
			}
		}
	}
}

// runInlineQuickstart sends the inline thrum-quickstart command into
// the pane's shell and blocks until the identity file is written (or
// the combined 10s budget expires). Factored out of HandleCreate so
// the synchronous-wait invariant can be asserted at the RPC boundary
// in unit tests: a future refactor back to a background goroutine
// would fail these tests even if the waitForIdentityFile helper stayed
// in place.
//
// Failure modes:
//   - sendKeysFn/sendSpecialKeyFn error → session untouched, error returned.
//   - EnforceOneIdentityWith → best-effort cleanup, non-fatal.
//   - Combined 10s budget exhausted → tmux session is killed so callers
//     don't see a half-initialized pane, structured error returned.
//
// ctx is honored in the wait loop: daemon shutdown / client disconnect
// mid-create returns ctx.Err() instead of burning the full budget.
func (h *TmuxHandler) runInlineQuickstart(ctx context.Context, req TmuxCreateRequest, name string) error {
	quickstartCmd := buildInlineQuickstartCmd(req)
	target := name + ":0.0"
	if err := sendKeysFn(target, quickstartCmd); err != nil {
		return fmt.Errorf("send quickstart: %w", err)
	}
	if err := sendSpecialKeyFn(target, "Enter"); err != nil {
		return fmt.Errorf("send enter: %w", err)
	}

	// Enforce single identity AFTER quickstart command is sent
	// successfully. Quickstart runs asynchronously in the pane —
	// this cleans pre-existing stale identities. The new identity
	// will be written by quickstart.
	//
	// thrum-182j: mirror agent.go's keeper-list expansion so every
	// agent registered against this worktree in session_refs survives
	// enforcement. Liveness gate (IsPIDAlive) refuses to quarantine a
	// file whose owning agent is currently running.
	//
	// AllowCrossWorktree is intentionally true: HandleCreate
	// legitimately operates on a target worktree (req.Cwd) that is NOT
	// the caller's own cwd — the coordinator creating a pane in a
	// sibling worktree is the canonical use case.
	keepers := []string{req.AgentName}
	if h.state != nil {
		keepers = append(keepers, h.state.ListAgentsInWorktree(ctx, req.Cwd)...)
	}
	worktree.EnforceOneIdentityWith(req.Cwd, worktree.EnforceOpts{
		IsPIDAlive:         func(pid int) bool { return process.IsRunning(pid) },
		AllowCrossWorktree: true,
	}, keepers...)

	// Shell init (oh-my-zsh, etc.) can swallow the first send-keys.
	// Block until the identity file lands on disk: a back-to-back
	// `thrum tmux launch` would otherwise find zero identity files
	// (pre-fix this ran in a goroutine — thrum-ns0b).
	idDir := filepath.Join(req.Cwd, ".thrum", "identities")
	idPath := filepath.Join(idDir, req.AgentName+".json")
	if resolved, err := filepath.EvalSymlinks(idDir); err == nil {
		idPath = filepath.Join(resolved, req.AgentName+".json")
	}
	resend := func() error {
		slog.Info("tmux.create.quickstart-resending",
			slog.String("agent", req.AgentName), slog.String("session", name),
		)
		if err := sendKeysFn(target, quickstartCmd); err != nil {
			return err
		}
		return sendSpecialKeyFn(target, "Enter")
	}
	if err := waitForIdentityFile(ctx, idPath, 5*time.Second, 5*time.Second, resend); err != nil {
		slog.Warn("tmux.create.quickstart-timeout",
			slog.String("agent", req.AgentName),
			slog.String("session", name),
			slog.String("err", err.Error()),
		)
		_ = killSessionFn(name)
		return fmt.Errorf("tmux create: %w", err)
	}
	return nil
}

// buildInlineQuickstartCmd returns the shell-safe quickstart command
// HandleCreate sends into the tmux pane's shell. Factored so unit tests
// can assert the exact emission (notably: --no-agent-pid must always be
// present — the inline subshell exits immediately after registration,
// persisting its PID breaks HandleLaunch's G4 writer-liveness check).
//
// The noAgentPID=true argument is fixed: HandleCreate has no legitimate
// reason to emit an inline quickstart without the flag. If a future
// caller needs the flag-less shape, route through a separate entry
// point rather than parameterizing this one.
func buildInlineQuickstartCmd(req TmuxCreateRequest) string {
	return worktree.BuildQuickstartCmd(
		req.AgentName, req.Role, req.Module, req.Intent, req.Runtime,
		true, // --no-agent-pid: thrum-x6e8.6
	)
}

// sessionCwd returns the cwd registered for a tmux session by
// HandleCreate. False when the map has no entry or the entry is empty.
// Shared between Pass 0 (writeTmuxByWorktreeCwd) and HandleLaunch's
// preamble so both operate on the same source of truth under the same
// lock discipline.
func (h *TmuxHandler) sessionCwd(sessionName string) (string, bool) {
	h.sessionMu.RLock()
	defer h.sessionMu.RUnlock()
	cwd, ok := h.sessionCwds[sessionName]
	if !ok || cwd == "" {
		return "", false
	}
	return cwd, true
}

// identityFilesInWorktree enumerates .json identity files under
// <cwd>/.thrum/identities/. Returns an empty slice for any read error or
// empty directory; callers decide how to handle len==0 / len>1.
func identityFilesInWorktree(cwd string) []string {
	idDir := filepath.Join(cwd, ".thrum", "identities")
	entries, err := os.ReadDir(idDir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		files = append(files, filepath.Join(idDir, entry.Name()))
	}
	return files
}

// soleIdentityFile returns the single .json identity file in
// <cwd>/.thrum/identities/, or "" + false when zero or >1 files exist.
// EnforceOneIdentity guarantees exactly one under normal operation;
// callers that want an observable signal on the pathological >1 case
// should enumerate directly via identityFilesInWorktree.
func soleIdentityFile(cwd string) (string, bool) {
	files := identityFilesInWorktree(cwd)
	if len(files) != 1 {
		return "", false
	}
	return files[0], true
}

// clearStalePIDForLaunch is HandleLaunch's preamble: if the session's
// registered cwd maps to a single identity file whose stored agent_pid
// belongs to an exited process, null it via guard.ClearPIDIfDead.
// Best-effort — any enumeration or clear error is logged and swallowed;
// the launch proceeds regardless.
func (h *TmuxHandler) clearStalePIDForLaunch(sessionName string) {
	cwd, ok := h.sessionCwd(sessionName)
	if !ok {
		return
	}
	idPath, ok := soleIdentityFile(cwd)
	if !ok {
		return
	}
	if _, err := guard.ClearPIDIfDead(idPath); err != nil {
		slog.Warn("tmux.launch.clear-pid-failed",
			slog.String("path", idPath),
			slog.String("err", err.Error()),
		)
	}
}

// warnIfTmuxSessionEmpty inspects the session's identity file after
// writeTmuxToIdentity has run and emits a regression warn when the
// TmuxSession field is still empty. Surfaces Part 3 cascade regressions
// that would otherwise be silent.
func (h *TmuxHandler) warnIfTmuxSessionEmpty(sessionName string) {
	cwd, ok := h.sessionCwd(sessionName)
	if !ok {
		return
	}
	idPath, ok := soleIdentityFile(cwd)
	if !ok {
		return
	}
	b, err := os.ReadFile(idPath) // #nosec G304 -- idPath is .thrum/identities/<name>.json under our own sessionCwd map
	if err != nil {
		return
	}
	var id config.IdentityFile
	if err := json.Unmarshal(b, &id); err != nil {
		return
	}
	if id.TmuxSession == "" {
		slog.Warn("tmux.launch.tmux-session-still-empty",
			slog.String("path", idPath),
			slog.String("session", sessionName),
		)
	}
}

// clearTmuxFromIdentitiesInDir removes tmux_session and runtime from identity files
// in a specific identities directory matching the given session name.
func (h *TmuxHandler) clearTmuxFromIdentitiesInDir(idDir, sessionName string) {
	entries, _ := os.ReadDir(idDir)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(idDir, entry.Name())
		data, err := os.ReadFile(path) // #nosec G304
		if err != nil {
			continue
		}
		var idFile config.IdentityFile
		if err := json.Unmarshal(data, &idFile); err != nil {
			continue
		}
		sess, _, _ := ttmux.ParseTarget(idFile.TmuxSession)
		if sess == sessionName {
			idFile.TmuxSession = ""
			idFile.Runtime = ""
			updated, _ := json.MarshalIndent(idFile, "", "  ")
			_ = os.WriteFile(path, updated, 0600) // #nosec G306
		}
	}
}
