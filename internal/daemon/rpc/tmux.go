package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/permission"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity/guard"
	"github.com/leonletto/thrum/internal/identitybanner"
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

// RestoreBinding writes both the (session→cwd) and (cwd→session) entries
// under the handler mutex. Used by the boot reconcile pass to rebuild the
// tmux pane-nudge target map from identity files after daemon restart.
//
// Identity files store `tmux_session` as the full tmux target ("name:N.M")
// but every reader of sessionCwds / cwdSessions — HandleCreate at
// session-cwd lookup (tmux.go:182), HandleRestart's stored-cwd read
// (tmux.go:1961), emitIdentityBanner's session→cwd resolution
// (tmux.go:2145), and HandleKill's mapping cleanup (tmux.go:513) — uses
// the bare session name with no suffix. Normalizing at this API boundary
// keeps the map keys canonical regardless of caller. Pre-thrum-8dl3 the
// reconcile pass populated `sessionCwds["email-brainstorm:0.0"]` and the
// banner emitter then looked up `sessionCwds["email-brainstorm"]` →
// miss → silent no-op → no banner in scrollback → watchdog's top anchor
// missing → conservative-true → no nudge → idle agent post-restart.
func (h *TmuxHandler) RestoreBinding(session, cwd string) {
	if i := strings.IndexByte(session, ':'); i >= 0 {
		session = session[:i]
	}
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	h.sessionCwds[session] = cwd
	h.cwdSessions[cwd] = session
}

// populateSessionCwdFromIdentity looks up the agent identity associated with
// the given tmux session name and, if found, writes the (session, cwd)
// binding to sessionCwds / cwdSessions via RestoreBinding. Idempotent: no-op
// when sessionCwds[name] is already set (preserves HandleCreate's canonical
// populate byte-for-byte for thrum-managed sessions).
//
// Used by HandleLaunch (and indirectly by HandleRestart via RestoreBinding
// after its own cwd resolution) to defensively populate the binding for
// externally-created tmux sessions — i.e., a raw `tmux new-session` followed
// by `thrum tmux launch`, where HandleCreate's canonical populate site never
// ran. Without this, runPostLaunchInject's emitIdentityBanner bails with
// map_hit=false (no banner, no /thrum:prime, no SessionStart hook post-
// restart loud preamble) for every session that wasn't created by
// `thrum tmux create`.
//
// Returns true if a binding was populated by this call, false if the
// binding already existed or the identity lookup found nothing.
func (h *TmuxHandler) populateSessionCwdFromIdentity(ctx context.Context, name string) bool {
	h.sessionMu.RLock()
	_, hasCwd := h.sessionCwds[name]
	h.sessionMu.RUnlock()
	if hasCwd {
		return false
	}
	agentName, idFile, _ := h.findIdentityForSession(ctx, name)
	if agentName == "" || idFile == nil || idFile.Worktree == "" {
		return false
	}
	cwd := resolveWorktreePath(ctx, filepath.Dir(h.thrumDir), idFile.Worktree)
	if cwd == "" {
		return false
	}
	h.RestoreBinding(name, cwd)
	return true
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

	if hasSessionFn(sanitized) {
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
//
// THRUM_* and TMUX/TMUX_PANE env scrubbing happens in safecmd.cleanTmuxEnv
// (covered by safecmd/cleantmuxenv_test.go). All daemon-spawned tmux execs
// route through safecmd.{Tmux,TmuxRun,TmuxExec}, so the chokepoint catches
// every code path here without per-callsite vigilance. See thrum-8nro.4.
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

	// thrum-jj0a.1/.2: plumb per-session identity env so the new
	// session's initial shell sees its own THRUM_* values rather than
	// inheriting whatever the shared tmux server captured at server-
	// start time. Empty for --no-agent sessions; the scrub-only path
	// in CreateSessionWithEnv preserves prior behavior in that case.
	envOverrides := map[string]string{}
	if !req.NoAgent {
		envOverrides["THRUM_NAME"] = req.AgentName
		envOverrides["THRUM_AGENT_ID"] = req.AgentName
		envOverrides["THRUM_ROLE"] = req.Role
		envOverrides["THRUM_MODULE"] = req.Module
		envOverrides["THRUM_HOME"] = req.Cwd
		if req.Intent != "" {
			envOverrides["THRUM_INTENT"] = req.Intent
		}
	}
	if err := ttmux.CreateSessionWithEnv(name, req.Cwd, envOverrides); err != nil {
		return nil, err
	}

	// Tag the session so HandleStatus can discover it via tmux state
	// alone (no identity file required). For agent-managed sessions
	// this is redundant with the identity file scan, but for
	// --no-agent sessions the tag is the ONLY discovery path — if
	// the tag fails to stick, the session becomes invisible to
	// `thrum tmux status` for its lifetime. Roll back by killing the
	// session and returning an error rather than silently orphaning
	// it (thrum-ufv5.11 review #4).
	//
	// Two writes happen in sequence: @thrum-managed first, then
	// @thrum-thrum-dir below. Between the two calls a concurrent
	// HandleStatus pass 2 would see the session as managed but
	// un-scoped (empty @thrum-thrum-dir) and skip it via the
	// graceful-skip path — safe by construction, no races to manage.
	// Both rollbacks kill the partially-tagged session.
	if err := setUserOptionFn(name, "@thrum-managed", "1"); err != nil {
		slog.Error("tmux set-option @thrum-managed failed; rolling back session create",
			"session", name, "error", err)
		_ = killSessionFn(name)
		return nil, fmt.Errorf("tag session %q as thrum-managed: %w", name, err)
	}

	// Stamp the session with this daemon's thrum_dir so HandleStatus
	// pass 2 can filter out sessions belonging to other daemons on the
	// same tmux socket. Without this, every thrum-managed session
	// machine-wide leaks through pass 2, polluting `thrum tmux status`,
	// the `thrum tmux connect` picker, and breaking test isolation
	// (thrum-zuz5).
	if err := setUserOptionFn(name, "@thrum-thrum-dir", h.thrumDir); err != nil {
		slog.Error("tmux set-option @thrum-thrum-dir failed; rolling back session create",
			"session", name, "error", err)
		_ = killSessionFn(name)
		return nil, fmt.Errorf("tag session %q with thrum-dir: %w", name, err)
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

	// Defensive sessionCwds populate for externally-created tmux sessions.
	// HandleCreate is the only canonical populate site; sessions created via
	// raw `tmux new-session` + `thrum tmux launch` (e.g. the release-harness
	// coord pane, where $REPO is a .git dir that fails HandleCreate's
	// worktree-validity guard) reach here with no map entry, and the
	// post-launch inject's emitIdentityBanner then bails out with
	// map_hit=false. No-op for thrum-managed sessions where HandleCreate
	// already populated the binding.
	h.populateSessionCwdFromIdentity(ctx, name)

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
		// Send the runtime launch keystrokes. Routed through the
		// sendKeysFn / sendSpecialKeyFn seams so unit tests can capture
		// the sequence; production behavior is identical to ttmux.SendKeys
		// + ttmux.SendSpecialKey.
		if err := sendKeysFn(target, launchCmd); err != nil {
			return nil, fmt.Errorf("launch send-keys: %w", err)
		}
		if err := sendSpecialKeyFn(target, "Enter"); err != nil {
			return nil, fmt.Errorf("launch enter: %w", err)
		}

		// Post-launch slot, in a goroutine so the RPC returns
		// immediately. After waitForPaneReady reports the runtime is
		// input-ready, hook runtimes get the pane-side identity banner
		// emitted INTO the running runtime's input prompt (printf is
		// treated as a user turn — the runtime echoes / responds with
		// the banner text, which is how PrimeTruncationSentinel reaches
		// the pane in a form paneAgentEngaged can find), while non-hook
		// runtimes get `/thrum:prime`. The silence watchdog runs in
		// either case so an agent that never engages still gets a nudge.
		// thrum-8dl3 (post-revert ordering — the pre-launch banner emit
		// attempted in the original Fix #1 was eaten by claude's
		// alt-screen takeover; logs confirmed the printf never reached
		// the pane).
		go h.runPostLaunchInject("launch", name, target, runtime,
			"Finish reading the prime output and follow your instructions if you have not")
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

// HandleSend sends text to a tmux session pane. Agent-managed sessions
// route through the queue for unified @system completion notification
// and silence-detection sequencing. --no-agent sessions bypass the
// queue and do a raw SendKeys — queue semantics rely on an agent being
// registered (see queue_rpc.go:64-70) and do not apply when no agent
// exists. This preserves manual/scripted keystroke injection into
// daemon-managed bare sessions, which is what Step 10D.11 (check-pane)
// and the `--no-agent` tmux-first workflow need.
func (h *TmuxHandler) HandleSend(ctx context.Context, params json.RawMessage) (any, error) {
	var req TmuxSendRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if req.Text == "" {
		return nil, fmt.Errorf("text is required")
	}

	// Bypass the queue for sessions without a registered agent. The
	// queue exists to wait for @system completion signals from an
	// agent; on bare sessions no such agent exists and commands would
	// otherwise pile up in "queued" forever (see queue_rpc.go:64-70).
	agentName, _, _ := h.findIdentityForSession(ctx, req.Name)
	if agentName == "" {
		_, target, err := h.ensureSession(req.Name)
		if err != nil {
			return nil, err
		}
		if err := sendKeysFn(target, req.Text); err != nil {
			return nil, fmt.Errorf("send-keys: %w", err)
		}
		// Return an empty QueueResponse so clients that ignore the
		// body keep working and JSON-consumers see a consistent shape.
		return &QueueResponse{}, nil
	}

	// Agent-managed session: preserve queue semantics.
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

	// Second pass: surface thrum-managed sessions that have no identity
	// file (--no-agent) by reading tmux state directly. HandleCreate
	// tags every session it creates with @thrum-managed=1 AND
	// @thrum-thrum-dir=<this daemon's thrum_dir>, so we can scope the
	// filter to sessions owned by THIS daemon. Without the daemon-dir
	// scope, every thrum-managed session on the tmux socket leaks
	// through — across worktrees, projects, and unrelated thrum repos
	// (thrum-zuz5).
	names, _ := listSessionsFn()
	// h.thrumDir is canonical at construction (filepath.Join cleans,
	// ResolveThrumDir enforces absolute); the Clean here is a safety
	// belt for future callers and matches the Clean applied to the
	// per-session ownerDir below.
	ownDir := filepath.Clean(h.thrumDir)
	for _, sessName := range names {
		// Defensive: tmux names beginning with "-" would be mis-parsed
		// as flags by the subsequent show-option -t <name> call. Skip
		// untrusted names (may come from sessions created outside
		// thrum on a shared socket). thrum-ufv5.11 review #3.
		if sessName == "" || strings.HasPrefix(sessName, "-") {
			continue
		}
		if seen[sessName] {
			continue
		}
		val, err := getUserOptionFn(sessName, "@thrum-managed")
		if err != nil || val != "1" {
			continue
		}
		// Daemon-scope filter: only surface sessions whose owning
		// daemon is THIS one. Pre-zuz5 sessions (no @thrum-thrum-dir)
		// are skipped — they are not broken, just not surfaced. The
		// tag is written by HandleCreate, so visibility restores when
		// the session is next recreated.
		ownerDir, err := getUserOptionFn(sessName, "@thrum-thrum-dir")
		if err != nil || ownerDir == "" {
			continue
		}
		if filepath.Clean(ownerDir) != ownDir {
			continue
		}
		seen[sessName] = true
		sessions = append(sessions, TmuxSessionInfo{
			Name:  sessName,
			State: "alive", // listed by tmux ⇒ present
		})
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
	// idFile.Worktree may be an absolute path (post thrum-x6e8.2 / nu16)
	// OR a bare name like "team-fix" (legacy — rewritten on next
	// guard.Check via reconcileDrift). resolveWorktreePath handles both.
	repoDir := filepath.Dir(h.thrumDir)
	cwd := resolveWorktreePath(ctx, repoDir, idFile.Worktree)
	if cwd == "" {
		// Legacy-only fallback: caller is inside the main repo itself
		// and stored a bare name matching filepath.Base(repoDir). This
		// branch is dead for the abs-path shape (an absolute path can
		// never equal a basename) but remains valid for bare-name
		// identity files that haven't yet been reconciled.
		if filepath.Base(repoDir) == idFile.Worktree || idFile.Worktree == "" {
			cwd = repoDir
		}
	}
	if cwd == "" {
		return nil, fmt.Errorf("cannot resolve worktree %q to a path for %s", idFile.Worktree, agentName)
	}

	// Re-establish the (session, cwd) binding cleared earlier by HandleKill
	// (tmux.go:494). Without this re-bind, the post-launch inject path's
	// emitIdentityBanner lookup bails with map_hit=false on every restart of
	// an externally-created session — same root cause as the HandleLaunch
	// populate above, surfacing on the restart path.
	h.RestoreBinding(name, cwd)

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

	// thrum-8dl3: shell-readiness probe. HandleRestart's CreateSession
	// returns BEFORE the new pane's shell finishes init (zsh/oh-my-zsh
	// sources dotfiles, wires prompt). Any SendKeys arriving during init
	// is silently swallowed. HandleCreate masks this via
	// runInlineQuickstart's resend-on-fail; without an equivalent here
	// the banner SendKeys from launchRuntimeWithBanner (Fix #1) is the
	// first keystroke after CreateSession — exactly the one most at
	// risk of being eaten. Probe: re-run the agent's quickstart and
	// wait for the identity file to materialize. Side-effect: refreshes
	// runtime/branch fields on the identity for the new pane. Failure
	// here is logged but non-fatal — restart has already done the
	// destructive snapshot+kill work; best-effort proceed.
	if err := h.ensureShellReadyAfterCreate(ctx, target, cwd, agentName, idFile.Agent.Role, idFile.Agent.Module, runtime); err != nil {
		slog.Warn("[restart] shell readiness probe failed; proceeding cautiously — banner emit may be swallowed",
			"session", name, "target", target, "err", err)
	}

	launchCmd := runtimeToLaunchCmd(runtime)
	if launchCmd != "" {
		// Send the runtime launch keystrokes via the test seams — see
		// HandleLaunch for the full rationale. ensureShellReadyAfterCreate
		// above has already proven the new pane's shell processes
		// commands, so the launchCmd itself won't be swallowed by zsh
		// init. The pane-side identity banner is emitted POST-launch
		// from the goroutine below (post-revert of the original Fix #1:
		// the alt-screen takeover by claude eats anything sent into the
		// pre-launch shell, including printf).
		if err := sendKeysFn(target, launchCmd); err != nil {
			return nil, fmt.Errorf("send launch command: %w", err)
		}
		if err := sendSpecialKeyFn(target, "Enter"); err != nil {
			return nil, fmt.Errorf("send enter: %w", err)
		}

		go h.runPostLaunchInject("restart", name, target, runtime,
			"Finish reading the prime output, which includes your resume plan, and then follow your instructions if you have not")
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

// waitForPaneReady polls the target tmux pane until two consecutive
// captures (1s apart) return identical content. That's the signal
// "TUI has finished rendering, the runtime is at an input-ready
// state." Replaces the legacy hard-coded Sleep(10s) at the HandleLaunch
// / HandleRestart post-action site: launchers that boot fast unblock
// quickly, launchers that boot slow (large repo init, codex first run)
// don't have their first keystroke swallowed by a still-rendering TUI.
//
// stableFor: consecutive identical captures required to declare ready
// (default 2 → ~2s of no change after capture cadence).
// ceilingSeconds: hard cap on total wait so a never-stable pane
// (continuous animation, agent already engaged) doesn't block forever.
//
// On capture errors the function falls back to a short fixed sleep so
// a transient tmux glitch doesn't break launch — better to inject
// late than to skip injection entirely.
// waitForPaneReady returns when the pane has been byte-identical across
// stableFor consecutive captures (1s apart) OR ceilingSeconds elapses.
//
// The returned bool reports whether the pane is SAFE TO TYPE: false
// when permission.IsPaneSafeToType detects a permission prompt or trust
// gate (thrum-puhr.10 cluster 8). Callers MUST consult the result
// before any SendKeys — a "ready but in a trust dialog" pane will
// destroy the runtime if typed into.
//
// runtime is required for permission-prompt detection (per-runtime
// patterns); trust-gate detection works either way but is more
// precise when runtime is known.
//
//nolint:unparam // stableFor is preserved as a parameter for the readiness-tuning seam future runtime presets will use; the silence-driven implementation does not consume it today.
func waitForPaneReady(target, runtime string, stableFor int, ceilingSeconds int) bool {
	_ = stableFor // preserved at call sites; silence-driven path doesn't use it
	if ceilingSeconds <= 0 {
		ceilingSeconds = 60
	}
	deadline := timeNowFn().Add(time.Duration(ceilingSeconds) * time.Second)

	// Silence-driven readiness detection: poll tmux #{window_activity} until
	// the pane has been silent for at least silenceThreshold (5s). This
	// replaces the legacy byte-equality compare, which was defeated by Claude
	// Code's animated thinking spinner (1Hz updates → consecutive captures
	// never byte-equal). After silence is detected, an additional
	// paneSettleAfterReady (2s) sleep gives the runtime time to wire up its
	// keyboard handler before keystroke injection — Claude in particular
	// will accept a long string into its input box but swallow the
	// immediately-following Enter when not yet input-ready (thrum-84xc).
	var consecutiveErrors int
	for {
		activity, err := tmuxLastActivityFn(target)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= watchdogMaxConsecutiveErrors {
				slog.Info("[readiness] target gone (repeated activity errors); skipping",
					"target", target, "err", err)
				return false
			}
			sleepFn(watchdogTickInterval)
			continue
		}
		consecutiveErrors = 0

		silenceFor := timeNowFn().Sub(activity)
		if silenceFor >= silenceThreshold {
			// Pane silent long enough — runtime done rendering. Settle
			// pause, then capture for the final safety check.
			sleepFn(paneSettleAfterReady)
			cur, err := capturePaneFn(target, 50)
			if err != nil {
				slog.Info("[readiness] capture after silence failed; skipping inject",
					"target", target, "err", err)
				return false
			}
			return permission.IsPaneSafeToType(runtime, cur)
		}
		if timeNowFn().After(deadline) {
			slog.Info("[readiness] pane never went silent within ceiling; proceeding cautiously",
				"target", target, "ceiling_s", ceilingSeconds)
			// Last-capture safety check so a long-rendering pane that
			// ended up at a trust dialog doesn't get typed into.
			cur, err := capturePaneFn(target, 50)
			if err != nil {
				return false
			}
			return permission.IsPaneSafeToType(runtime, cur)
		}
		sleepFn(watchdogTickInterval)
	}
}

// sendKeysAndSubmit sends `text` to a tmux pane via send-keys, pauses
// briefly (paneInputSubmitGap), then sends Enter. The pause exists because
// modern TUI runtimes like Claude Code interpret a long string immediately
// followed by Enter as "Enter within paste" rather than "submit", swallowing
// the submission. The gap lets the input widget exit paste-mode before the
// Enter arrives so it's processed as a discrete keypress.
//
// Returns the SendKeys error if it fails; the Enter is best-effort (any
// error is silently ignored — the keystroke either landed or it didn't, and
// retrying is more likely to compound damage than fix it).
func sendKeysAndSubmit(target, text string) error {
	if err := sendKeysFn(target, text); err != nil {
		return err
	}
	sleepFn(paneInputSubmitGap)
	_ = sendSpecialKeyFn(target, "Enter")
	return nil
}

// printfAckLineRe matches the one-line ack pattern an agent emits in response
// to the identity printf at the end of the launch banner (e.g.
// `@impl_v0105 primed (implementer/v0105). Standing by.`). Runtimes can print
// this line WITHOUT having Read the (possibly truncated) prime briefing — the
// printf body looks like a complete prompt and the model acknowledges it
// without following the embedded "must read it now" directive. paneAgentEngaged
// treats matches of this regex as "no real output" so the post-launch silence
// watchdog still nudges the agent into running the prime. thrum-qpw7.
//
// The pattern requires the canonical `primed (` opener — all 5 runtime plugins
// (claude/cursor/opencode/codex/kiro) emit the ack as
// `@<name> primed (<role>[/<module>]). <intent>. Standing by.`, so anchoring
// on the literal `(` eliminates false-positives on unrelated prose like
// `@impl_v0105 primed the database` while keeping every canonical ack matched.
var printfAckLineRe = regexp.MustCompile(`@\S+\s+primed\s*\(`)

// paneAgentEngaged reports whether a captured pane snapshot contains real
// agent output in the decision region bounded by two anchors. It is used by
// nudgeSilentPaneAfter (thrum-84xc) to replace the naive byte-equality diff
// that was defeated by Claude Code's 1Hz animated spinner.
//
// The top anchor is identitybanner.PrimeTruncationSentinel — the last line of
// the identity banner injected at launch/restart. The bottom anchor is the
// FIRST occurrence of the runtime's spinner pattern after the top anchor;
// spinner-as-bottom-anchor cleanly excludes Claude's footer-region tip lines
// (e.g. "tmux focus-events off · add ...") that appear above the divider but
// below the spinner. If no spinner has rendered yet, fall back to the
// runtime's bottom-anchor regex (horizontal-rule chrome separator).
//
// Decision algorithm:
//  1. Find the LAST line containing the banner sentinel → topIdx.
//  2. Find the FIRST line matching spinnerRe after topIdx → bottomIdx.
//     If no spinner found, find the FIRST line matching bottomAnchorRe
//     after topIdx → bottomIdx (fallback for fresh panes pre-spinner).
//  3. If neither anchor found → return true (conservative: can't reason
//     confidently, don't nudge).
//  4. For each line in [topIdx+1 .. bottomIdx-1]:
//     - Trim whitespace; if empty → ignore.
//     - If matches printfAckLineRe (printf-mandated ack with no Read of the
//     prime briefing — thrum-qpw7) → ignore.
//     - Else → real agent output → return true (engaged, no nudge).
//  5. Loop completes with no real output → return false (not engaged, nudge).
//
// Why spinner-first: Claude renders chronologically — agent content appears
// ABOVE the spinner (which marks the most-recent action), and chrome (tips,
// divider, input box) appears BELOW. Using spinner as the bottom anchor puts
// tips outside the decision region. Divider fallback covers the brief window
// before the runtime has rendered any spinner yet.
//
// bottomAnchorRe and spinnerRe may be nil (e.g. runtimes without TUI chrome).
// If both are nil → no anchor found path (step 3) → true.
func paneAgentEngaged(captured string, bottomAnchorRe, spinnerRe *regexp.Regexp) bool {
	topAnchor := identitybanner.PrimeTruncationSentinel
	lines := strings.Split(captured, "\n")

	topIdx := -1
	for i, l := range lines {
		if strings.Contains(l, topAnchor) {
			topIdx = i
		}
	}
	if topIdx < 0 {
		return true // top anchor not found — conservative
	}

	// Prefer spinner as bottom anchor (excludes footer-region tip lines).
	// Fall back to divider/horizontal-rule when no spinner has rendered yet.
	bottomIdx := -1
	if spinnerRe != nil {
		for i := topIdx + 1; i < len(lines); i++ {
			if spinnerRe.MatchString(strings.TrimSpace(lines[i])) {
				bottomIdx = i
				break
			}
		}
	}
	if bottomIdx < 0 && bottomAnchorRe != nil {
		for i := topIdx + 1; i < len(lines); i++ {
			if bottomAnchorRe.MatchString(strings.TrimRight(lines[i], " \t")) {
				bottomIdx = i
				break
			}
		}
	}
	if bottomIdx < 0 {
		return true // no bottom anchor found — conservative
	}

	for _, l := range lines[topIdx+1 : bottomIdx] {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		if printfAckLineRe.MatchString(trimmed) {
			// thrum-qpw7: agent printed the printf-mandated ack but
			// did NOT Read the prime briefing. Not real output —
			// let the watchdog fire the corrective nudge.
			continue
		}
		return true // real agent output found
	}
	return false // only blanks (and/or ack lines) between sentinel and spinner/divider — not engaged
}

// nudgeSilentPaneAfter implements thrum-puhr.10 + thrum-84xc: after a
// post-launch / post-restart inject (e.g. identity banner), schedule a
// one-shot silence watchdog. Instead of a fixed sleep + byte diff, it polls
// tmux window_activity every 500ms, waiting for a sustained silence window of
// at least silenceThreshold (5s). Once silence is detected, it captures the
// pane and uses paneAgentEngaged (two-anchor semantic check) to decide whether
// to nudge. If the agent is busy past the hard deadline
// (restart.silence_watchdog_seconds, default 30s), the watchdog exits without
// nudging — the agent is genuinely working.
//
// The threshold is read fresh per-call from .thrum/config.json. Set the
// config key to a negative value to disable the watchdog entirely.
//
// Runs in the caller's goroutine; callers always invoke this from a
// detached goroutine so the RPC handler returns immediately.
func nudgeSilentPaneAfter(target, runtimeName, thrumDir, nudge string) {
	cfg, _ := config.LoadThrumConfig(thrumDir)
	deadlineSecs, enabled := cfg.Restart.SilenceWatchdog()
	if !enabled {
		return
	}
	deadline := timeNowFn().Add(time.Duration(deadlineSecs) * time.Second)

	// Poll window_activity until we observe silenceThreshold consecutive
	// seconds of pane silence, or the hard deadline fires.
	var consecutiveErrors int
	for {
		activity, err := tmuxLastActivityFn(target)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= watchdogMaxConsecutiveErrors {
				slog.Info("[watchdog] target gone (repeated activity errors); skipping",
					"target", target, "err", err)
				return
			}
			sleepFn(watchdogTickInterval)
			continue
		}
		consecutiveErrors = 0

		silenceFor := timeNowFn().Sub(activity)
		if silenceFor >= silenceThreshold {
			// Pane has been quiet long enough — run the semantic check.
			break
		}
		if timeNowFn().After(deadline) {
			slog.Info("[watchdog] no nudge: agent busy past deadline (no silence window)",
				"target", target, "deadline_s", deadlineSecs)
			return
		}
		sleepFn(watchdogTickInterval)
	}

	after, err := capturePaneFn(target, 50)
	if err != nil {
		slog.Info("[watchdog] capture failed; skipping",
			"target", target, "err", err)
		return
	}

	// Resolve per-runtime anchor/spinner regexes for the semantic engagement check.
	var bottomAnchorRe, spinnerRe *regexp.Regexp
	if preset, err := trt.GetPreset(runtimeName); err == nil {
		bottomAnchorRe = preset.BottomAnchorRegex
		spinnerRe = preset.SpinnerRegex
	}

	engaged := paneAgentEngaged(after, bottomAnchorRe, spinnerRe)
	hasSentinel := strings.Contains(after, identitybanner.PrimeTruncationSentinel)
	if engaged {
		slog.Info("[watchdog] no nudge: paneAgentEngaged=true",
			"target", target, "runtime", runtimeName,
			"top_anchor_found", hasSentinel,
			"pane_tail_excerpt", truncate(tailLines(after, 6), 240))
		return
	}

	// thrum-puhr.10 cluster 8: guard against typing into a trust
	// dialog or permission prompt.
	if !permission.IsPaneSafeToType(runtimeName, after) {
		slog.Info("[watchdog] skipping nudge: pane in detected prompt/trust-gate state",
			"target", target, "runtime", runtimeName,
			"pane_excerpt", truncate(after, 120))
		return
	}
	slog.Info("[watchdog] nudging: only spinner/blanks below banner sentinel",
		"target", target, "runtime", runtimeName, "deadline_s", deadlineSecs,
		"top_anchor_found", hasSentinel,
		"pane_tail_excerpt", truncate(tailLines(after, 6), 240))
	if err := sendKeysFn(target, nudge); err != nil {
		slog.Warn("[watchdog] SendKeys nudge failed", "target", target, "err", err)
		return
	}
	_ = sendSpecialKeyFn(target, "Enter")
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
		//nolint:staticcheck // QF1001: explicit positive-range form is clearer for character classes
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

// resolveWorktreePath returns the absolute worktree path for the
// stored identifier, which may be either an absolute path (post
// thrum-x6e8.2 / nu16 identity files) or a bare basename (legacy
// identity files written before nu16, rewritten to absolute by
// reconcileDrift on next guard.Check).
//
// For the path shape: if stored stat()s, return filepath.Clean(stored).
// Otherwise return "" — don't fall back to basename lookup, since the
// caller stored a specific path and expects an unambiguous answer.
//
// For the bare-name shape: fall back to `git worktree list` and match
// by basename (legacy behavior).
//
// Returns "" if neither form resolves.
func resolveWorktreePath(ctx context.Context, repoDir, stored string) string {
	if stored == "" {
		return ""
	}
	if filepath.IsAbs(stored) {
		if _, err := os.Stat(stored); err == nil {
			return filepath.Clean(stored)
		}
		return ""
	}
	// Legacy bare-name fallback — consult git worktree list.
	out, err := safecmd.Git(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			if filepath.Base(path) == stored {
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
//
// Seams hasSessionFn / listSessionsFn / getUserOptionFn /
// setUserOptionFn were added for HandleSend's no-agent bypass path,
// HandleStatus's second pass, and HandleCreate's tag-failure rollback
// (thrum-ufv5.11/12) so all three can be exercised end-to-end without
// shelling out to tmux. thrum-zuz5 added a second use of getUserOptionFn
// (read @thrum-thrum-dir) and a second setUserOptionFn write
// (stamp @thrum-thrum-dir) for daemon-scoped pass 2 filtering.
var (
	sendKeysFn         = ttmux.SendKeys
	sendSpecialKeyFn   = ttmux.SendSpecialKey
	killSessionFn      = ttmux.KillSession
	hasSessionFn       = ttmux.HasSession
	listSessionsFn     = ttmux.ListSessions
	getUserOptionFn    = ttmux.GetUserOption
	setUserOptionFn    = ttmux.SetUserOption
	capturePaneFn      = ttmux.CapturePane
	sleepFn            = time.Sleep
	tmuxLastActivityFn = ttmux.LastActivity
	timeNowFn          = time.Now
)

// silenceThreshold is the consecutive-silence window the watchdog waits for
// before running the engagement check. 5 seconds of pane silence means the
// spinner has stopped ticking, so we can do a stable snapshot. thrum-84xc.
const silenceThreshold = 5 * time.Second

// watchdogTickInterval is how often the watchdog polls tmux window_activity
// while waiting for a silence window. 500ms keeps latency low without
// hammering the tmux server.
const watchdogTickInterval = 500 * time.Millisecond

// watchdogMaxConsecutiveErrors is how many consecutive tmuxLastActivityFn
// errors the watchdog tolerates before assuming the target is gone and exiting.
const watchdogMaxConsecutiveErrors = 3

// paneSettleAfterReady is the additional pause after silence-detection reports
// the pane is rendered, before keystroke injection. Some TUI runtimes (Claude
// Code in particular) accept text input into their input box but swallow the
// immediately-following Enter when not yet input-ready. Two seconds is enough
// for Claude to finish wiring its keyboard handler. thrum-84xc.
const paneSettleAfterReady = 2 * time.Second

// paneInputSubmitGap is the pause between sending text and sending Enter when
// submitting input to a TUI pane. Modern runtimes detect a long string + Enter
// pair as a paste-with-newline, not a submission. The gap lets the input
// widget exit paste-mode before Enter arrives. thrum-84xc.
const paneInputSubmitGap = 200 * time.Millisecond

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
//
// req.Cwd is forwarded as --repo so the quickstart cobra handler resolves
// flagRepo to the daemon-known target worktree, not whatever THRUM_HOME
// the pane's shell inherited from the daemon's environ (thrum-tc4w).
func buildInlineQuickstartCmd(req TmuxCreateRequest) string {
	return worktree.BuildQuickstartCmd(
		req.Cwd,
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

// runtimeHasSessionStartHook returns true when the named runtime ships an
// inject-prime-context.sh SessionStart hook (claude-plugin / cursor-plugin)
// that auto-injects the briefing on every pane start. Single source of
// truth for the prime-skip gate in HandleLaunch + HandleRestart — read
// from the runtime preset's HasSessionStartHook field rather than
// hard-coding runtime names. Falls back to false for unknown runtimes
// (safer default: a runtime not in the preset registry can't be assumed
// to have the hook). thrum-6hqy.
func runtimeHasSessionStartHook(runtime string) bool {
	preset, err := trt.GetPreset(runtime)
	if err != nil {
		return false
	}
	return preset.HasSessionStartHook
}

// ensureShellReadyAfterCreate proves the freshly-created pane's shell
// is past init and accepting keystrokes by re-running the agent's
// inline quickstart and waiting for the identity file to materialize.
// The probe doubles as an identity refresh — quickstart rewrites
// runtime/branch/tmux_session for the new pane.
//
// Strategy: move the existing identity file aside before sending the
// probe, then wait for a fresh file to appear (reuses
// runInlineQuickstart's existence-based waitForIdentityFile path).
// On success the aside backup is removed; on failure (timeout, send
// error) the backup is restored so the agent's pre-restart identity
// survives a wedged restart.
//
// Returns an error on timeout or send failure but does NOT kill the
// session — HandleRestart has already done destructive snapshot+kill
// work; the caller logs the error and proceeds. The downstream banner
// emit may still land at the shell prompt if the shell becomes
// responsive between probe-timeout and banner-send (rare but possible
// on a transient init stall). thrum-8dl3.
//
//nolint:revive // many args: each is independent identity-file state and packaging into a struct would obscure the production call site.
func (h *TmuxHandler) ensureShellReadyAfterCreate(ctx context.Context, target, cwd, agentName, role, module, runtime string) error {
	slog.Info("[restart] shell-ready probe: entry",
		"target", target, "runtime", runtime, "agent", agentName, "cwd", cwd, "role", role, "module", module)

	idDir := filepath.Join(cwd, ".thrum", "identities")
	if resolved, err := filepath.EvalSymlinks(idDir); err == nil {
		idDir = resolved
	}
	idPath := filepath.Join(idDir, agentName+".json")
	backupPath := idPath + ".pre-restart-probe"

	// Clean any leftover from a prior wedged restart attempt before
	// moving the current identity aside, otherwise os.Rename would fail
	// on the second restart of a daemon-crashed session.
	_ = os.Remove(backupPath)
	movedAside := false
	if err := os.Rename(idPath, backupPath); err == nil {
		movedAside = true
	}
	slog.Info("[restart] shell-ready probe: rename-aside",
		"target", target, "runtime", runtime, "agent", agentName, "moved_aside", movedAside, "id_path", idPath)

	// Restore-or-cleanup the backup. Deferred so any return path
	// (send error, ctx cancel, timeout, success) leaves the filesystem
	// in a coherent state. The restore only fires if quickstart didn't
	// produce a fresh file — a successful probe means idPath exists
	// with the new content and we just clean up the backup.
	defer func() {
		if !movedAside {
			_ = os.Remove(backupPath) // safety cleanup
			return
		}
		if _, err := os.Stat(idPath); err != nil {
			// Probe didn't produce a fresh file — restore so the agent
			// retains its pre-restart identity.
			_ = os.Rename(backupPath, idPath)
			slog.Info("[restart] shell-ready probe: backup restored on probe failure",
				"target", target, "agent", agentName)
			return
		}
		// Fresh file present — drop the backup.
		_ = os.Remove(backupPath)
	}()

	quickstartCmd := worktree.BuildQuickstartCmd(cwd, agentName, role, module, "", runtime, true)
	slog.Info("[restart] shell-ready probe: sending quickstart",
		"target", target, "runtime", runtime, "agent", agentName,
		"cmd_preview", truncate(quickstartCmd, 120))

	if err := sendKeysFn(target, quickstartCmd); err != nil {
		slog.Warn("[restart] shell-ready probe: send-keys quickstart failed",
			"target", target, "runtime", runtime, "agent", agentName, "err", err)
		return fmt.Errorf("send quickstart probe: %w", err)
	}
	if err := sendSpecialKeyFn(target, "Enter"); err != nil {
		slog.Warn("[restart] shell-ready probe: send-enter quickstart failed",
			"target", target, "runtime", runtime, "agent", agentName, "err", err)
		return fmt.Errorf("send enter: %w", err)
	}

	resend := func() error {
		slog.Info("[restart] shell-ready probe: resending (first send-keys likely swallowed by shell init)",
			"target", target, "runtime", runtime, "agent", agentName,
		)
		if err := sendKeysFn(target, quickstartCmd); err != nil {
			return err
		}
		return sendSpecialKeyFn(target, "Enter")
	}
	err := waitForIdentityFile(ctx, idPath, shellReadyInitialWait, shellReadyRetryWait, resend)
	if err != nil {
		slog.Warn("[restart] shell-ready probe: identity file never appeared",
			"target", target, "runtime", runtime, "agent", agentName, "err", err)
		return err
	}
	slog.Info("[restart] shell-ready probe: identity file written — shell is responsive",
		"target", target, "runtime", runtime, "agent", agentName, "id_path", idPath)
	return nil
}

// truncate returns s clipped to maxLen runes with a trailing "…" if it
// was longer. Used for log previews of potentially long strings like
// quickstart commands and pane captures. Operates on runes (not bytes)
// so unicode in pane content (✻, ─, etc.) doesn't split mid-codepoint.
func truncate(s string, maxLen int) string {
	rs := []rune(s)
	if len(rs) <= maxLen {
		return s
	}
	return string(rs[:maxLen]) + "…"
}

// tailLines returns the last n lines of s joined with " / " for compact
// single-line log emission. Used by watchdog logging so reviewers can
// see exactly what region paneAgentEngaged was inspecting without
// reading a multi-line pane dump.
func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return strings.Join(lines, " / ")
	}
	return strings.Join(lines[len(lines)-n:], " / ")
}

// shellReadyInitialWait / shellReadyRetryWait govern the two-stage budget
// ensureShellReadyAfterCreate uses while waiting for the quickstart probe
// to land an identity file. Mirrors runInlineQuickstart's 5s+5s budget.
// Tests shrink these via t.Cleanup'd assignment so timeout cases don't
// burn 10+ real seconds.
var (
	shellReadyInitialWait = 5 * time.Second
	shellReadyRetryWait   = 5 * time.Second
)

// runPostLaunchInject is the body of the goroutine spawned by both
// HandleLaunch and HandleRestart after the runtime launch keystrokes.
// Waits for the pane to be input-ready, then either emits the identity
// banner (hook runtimes — printf is sent into the running runtime's
// input prompt as a user message; the runtime echoes / responds with the
// banner text including `PrimeTruncationSentinel`, which is the form
// `paneAgentEngaged` can find in the captured pane) or sends
// `/thrum:prime` (non-hook runtimes whose context isn't auto-loaded).
// Finally schedules the silence watchdog regardless of branch.
//
// `site` is "launch" or "restart" and is woven into the slog logs for
// post-hoc correlation. `nudgeText` is the SendKeys payload the watchdog
// fires when the agent doesn't engage on its own.
//
// History: the original thrum-8dl3 Fix #1 hoisted `emitIdentityBanner`
// to BEFORE the launch keystrokes on the theory that the post-launch
// printf was landing inside claude's input box and silently corrupting
// the banner. End-to-end retesting with observability logs proved the
// opposite — claude's alt-screen takeover (ESC[?1049h + ESC[2J + ESC[3J)
// CLEARS pre-launch shell output, so the pre-launch printf vanishes.
// The working mechanism is the post-launch emit: claude treats the
// printf as a user turn and responds with the banner content, which is
// how the sentinel reaches the captured pane in a form the watchdog can
// find. release-dashboard's scrollback shows this in action.
func (h *TmuxHandler) runPostLaunchInject(site, name, target, runtime, nudgeText string) {
	slog.Info("["+site+"] post-launch inject: entry",
		"name", name, "target", target, "runtime", runtime)
	// thrum-puhr.10 (cluster 5+8): silence-driven readiness probe with
	// trust-gate / permission-prompt guard. False return means the pane
	// is in a non-typable state (or never went silent) — skip all
	// SendKeys to avoid corrupting a dialog.
	if !waitForPaneReady(target, runtime, 2, 60) {
		slog.Info("["+site+"] skipping post-launch inject: pane in detected prompt/trust-gate state or never settled",
			"target", target, "runtime", runtime)
		return
	}
	if runtimeHasSessionStartHook(runtime) {
		// Banner emitted INTO the running runtime's input prompt. See
		// emitIdentityBanner for why this is the working mechanism.
		h.emitIdentityBanner(name, target)
	} else {
		// Defense-in-depth: re-check safety just before send. The
		// readiness gate's exit window is brief but not atomic with
		// this send.
		if cur, err := capturePaneFn(target, 50); err == nil && !permission.IsPaneSafeToType(runtime, cur) {
			slog.Info("["+site+"] skipping /thrum:prime send: pane in detected prompt/trust-gate state",
				"target", target, "runtime", runtime,
				"pane_excerpt", truncate(cur, 120))
			return
		}
		primeCmd := primeCommandForRuntime(runtime)
		_ = sendKeysAndSubmit(target, primeCmd)
	}
	// Silence watchdog: nudge the agent if the pane stays quiet past
	// the configured threshold (default 30s). Independent of the
	// banner / prime path above.
	nudgeSilentPaneAfter(target, runtime, h.thrumDir, nudgeText)
}

// emitIdentityBanner sends the identity banner for the agent registered
// at the session's stored cwd into the pane via tmux send-keys + Enter.
// Best-effort: silently no-ops when no cwd is stored, no identity is
// found, or identitybanner.ShellCommand returns empty (e.g. a bare
// session created with --no-agent). Called from runPostLaunchInject
// (HandleLaunch + HandleRestart) AFTER waitForPaneReady reports the
// runtime is input-ready.
//
// Mechanism: for hook runtimes (claude / codex / cursor) the runtime is
// already running and showing its input prompt when this fires. The
// printf SendKeys lands INSIDE the runtime's input box and the runtime
// treats it as a user turn — claude in particular echoes the input as
// `❯ printf '%s\n' ...` then responds with `⏺ Agent: @<name> ...`
// containing the full banner text including the PrimeTruncationSentinel.
// That response in the captured pane is what `paneAgentEngaged` uses as
// its top anchor.
//
// Pre-launch banner emission (the original thrum-8dl3 Fix #1) DOES NOT
// WORK: claude's alt-screen entry clears the visible region AND the
// in-flight pre-launch printf SendKeys, so the banner never reaches the
// pane in any form. End-to-end retesting on stalled-sweep-brainstorm and
// skills-brainstorm confirmed the pre-launch path silently drops the
// banner; the post-launch path is the working production mechanism (see
// release-dashboard's scrollback, which has the banner sentinel in
// claude's response form).
// thrum-6hqy / thrum-8dl3.
func (h *TmuxHandler) emitIdentityBanner(session, target string) {
	slog.Info("[banner] emitIdentityBanner: entry", "session", session, "target", target)

	h.sessionMu.RLock()
	cwd, ok := h.sessionCwds[session]
	h.sessionMu.RUnlock()
	if !ok || cwd == "" {
		slog.Info("[banner] skipping identity banner: no cwd registered for session",
			"session", session, "target", target, "map_hit", ok)
		return
	}
	idFile, _, err := config.LoadIdentityWithPath(cwd)
	if err != nil {
		slog.Info("[banner] skipping identity banner: identity load error",
			"session", session, "target", target, "cwd", cwd, "err", err)
		return
	}
	if idFile == nil {
		slog.Info("[banner] skipping identity banner: no identity file for cwd",
			"session", session, "target", target, "cwd", cwd)
		return
	}
	cmdLine := identitybanner.ShellCommand(idFile)
	if cmdLine == "" {
		slog.Info("[banner] skipping identity banner: ShellCommand returned empty (no agent name)",
			"session", session, "target", target, "cwd", cwd, "agent_name", idFile.Agent.Name)
		return
	}
	// thrum-puhr.10 cluster 8: guard against typing into a trust gate
	// or permission prompt. Banner SendKeys would corrupt either by
	// injecting a shell-prompt command (the cmdLine starts with `:`
	// or similar) into a dialog input field.
	if cur, err := capturePaneFn(target, 50); err == nil && !permission.IsPaneSafeToType(idFile.Runtime, cur) {
		slog.Info("[banner] skipping identity banner: pane in detected prompt/trust-gate state",
			"session", session, "target", target, "runtime", idFile.Runtime,
			"pane_excerpt", truncate(cur, 120))
		return
	}
	slog.Info("[banner] emitting identity banner via sendKeysAndSubmit",
		"session", session, "target", target, "runtime", idFile.Runtime,
		"agent_name", idFile.Agent.Name, "cmd_preview", truncate(cmdLine, 120))
	// Best-effort: SendKeys errors here shouldn't fail the launch.
	// sendKeysAndSubmit inserts paneInputSubmitGap between text and Enter so
	// Claude doesn't swallow the Enter as paste-newline (thrum-84xc).
	if err := sendKeysAndSubmit(target, cmdLine); err != nil {
		slog.Warn("[banner] sendKeysAndSubmit returned error (banner may not have landed)",
			"session", session, "target", target, "err", err)
		return
	}
	slog.Info("[banner] emitIdentityBanner: sendKeysAndSubmit completed",
		"session", session, "target", target, "runtime", idFile.Runtime)
}
