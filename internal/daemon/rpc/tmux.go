package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/restart"
	ttmux "github.com/leonletto/thrum/internal/tmux"
)

// Request/Response types for tmux RPC handlers.

// TmuxCreateRequest is the request to create a new tmux session.
type TmuxCreateRequest struct {
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
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
	thrumDir string
}

// NewTmuxHandler creates a new TmuxHandler.
func NewTmuxHandler(thrumDir string) *TmuxHandler {
	return &TmuxHandler{thrumDir: thrumDir}
}

// HandleCreate creates a new detached tmux session with monitor-silence hook.
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

	name := ttmux.SanitizeSessionName(req.Name)
	if err := ttmux.CreateSession(name, req.Cwd); err != nil {
		return nil, err
	}

	// Set up monitor-silence hook (non-fatal if it fails)
	thrumBin, _ := os.Executable()
	if thrumBin != "" {
		_ = ttmux.SetMonitorSilence(name, 60, thrumBin, h.thrumDir)
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

	if !ttmux.HasSession(req.Name) {
		return nil, fmt.Errorf("session %q does not exist", req.Name)
	}

	runtime := req.Runtime
	if runtime == "" {
		runtime = "claude"
	}

	launchCmd := runtimeToLaunchCmd(runtime)

	target := req.Name + ":0.0"
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
		}()
	}

	// Write tmux_session and runtime to the agent's identity file
	h.writeTmuxToIdentity(req.Name, target, runtime)

	return &TmuxLaunchResponse{Session: req.Name, Runtime: runtime}, nil
}

// HandleKill destroys a tmux session and clears tmux_session from identity files.
func (h *TmuxHandler) HandleKill(ctx context.Context, params json.RawMessage) (any, error) {
	var req TmuxKillRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	name := ttmux.SanitizeSessionName(req.Name)
	h.clearTmuxFromIdentities(name)

	return nil, ttmux.KillSession(name)
}

// HandleSend sends text to a tmux session pane.
func (h *TmuxHandler) HandleSend(ctx context.Context, params json.RawMessage) (any, error) {
	var req TmuxSendRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	name := ttmux.SanitizeSessionName(req.Name)
	target := name + ":0.0"
	if err := ttmux.SendKeys(target, req.Text); err != nil {
		return nil, err
	}
	return nil, ttmux.SendSpecialKey(target, "Enter")
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
	name := ttmux.SanitizeSessionName(req.Name)
	content, err := ttmux.CapturePane(name+":0.0", lines)
	if err != nil {
		return nil, err
	}
	return &TmuxCaptureResponse{Content: content}, nil
}

// HandleStatus scans identity files across all worktrees for managed tmux sessions.
func (h *TmuxHandler) HandleStatus(ctx context.Context, params json.RawMessage) (any, error) {
	dirs := h.allIdentityDirs(ctx)

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
				info.State = "dead"
				h.clearTmuxFromIdentitiesInDir(identitiesDir, session)
			} else if idFile.AgentPID > 0 && !isProcessAlive(idFile.AgentPID) {
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
// Detects idle or permission-blocked agents and logs the event.
func (h *TmuxHandler) HandleCheckPane(ctx context.Context, params json.RawMessage) (any, error) {
	var req CheckPaneRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	state := "idle"
	if req.Reason != "" {
		state = "permission"
	}

	log.Printf("[tmux] check-pane: session=%s state=%s reason=%s", req.Session, state, req.Reason)

	return &CheckPaneResponse{
		Session: req.Session,
		State:   state,
		Reason:  req.Reason,
	}, nil
}

// writeTmuxToIdentity writes tmux_session and runtime to the identity file
// for the agent whose session matches the given name.
func (h *TmuxHandler) writeTmuxToIdentity(sessionName, target, runtime string) {
	identitiesDir := filepath.Join(h.thrumDir, "identities")
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
		// Match by existing tmux_session association
		sess, _, _ := ttmux.ParseTarget(idFile.TmuxSession)
		if sess == sessionName {
			idFile.TmuxSession = target
			idFile.Runtime = runtime
			_ = config.SaveIdentityFile(filepath.Dir(identitiesDir), &idFile)
			return // write to first matching identity
		}
	}
}

// clearTmuxFromIdentities removes tmux_session and runtime from identity files
// matching the given session name, scanning all worktrees.
func (h *TmuxHandler) clearTmuxFromIdentities(sessionName string) {
	for _, dir := range h.allIdentityDirs(context.Background()) {
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

	name := ttmux.SanitizeSessionName(req.Name)
	if !ttmux.HasSession(name) {
		return nil, fmt.Errorf("session %s does not exist", name)
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

	// Extract JSONL snapshot if PID is available and no snapshot already exists.
	// Only extract for Claude — other runtimes don't use this conversation file format.
	if idFile.AgentPID > 0 && !restart.SnapshotExists(wtThrumDir, agentName) &&
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
		}()
	}

	// Write tmux_session and runtime to the agent's identity file
	h.writeTmuxToIdentity(name, target, runtime)

	return &TmuxRestartResponse{
		Session:       name,
		SnapshotLines: snapshotLines,
	}, nil
}

// runtimeToLaunchCmd converts a runtime name to the CLI command to launch it.
func runtimeToLaunchCmd(runtime string) string {
	switch runtime {
	case "claude":
		return "claude"
	case "opencode":
		return "opencode"
	case "aider":
		return "aider"
	case "shell":
		return ""
	default:
		return runtime // best-effort: use the runtime name as the command
	}
}

// primeCommandForRuntime returns the slash command to send after launch
// for each supported runtime. Open Code uses /thrum-prime (command filenames
// are the command names), while Claude Code uses /thrum:prime (plugin namespace).
func primeCommandForRuntime(runtime string) string {
	switch runtime {
	case "opencode":
		return "/thrum-prime"
	default:
		return "/thrum:prime"
	}
}

// resolveWorktreePath uses git worktree list to find the absolute path for a worktree name.
func resolveWorktreePath(ctx context.Context, repoDir, worktreeName string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "worktree", "list", "--porcelain").Output() // #nosec G204 -- repoDir from daemon config
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			path := strings.TrimPrefix(line, "worktree ")
			if filepath.Base(path) == worktreeName {
				return path
			}
		}
	}
	return ""
}

// findIdentityForSession searches all worktree identity dirs for an agent
// associated with the given tmux session name.
func (h *TmuxHandler) findIdentityForSession(ctx context.Context, sessionName string) (string, *config.IdentityFile, string) {
	for _, idDir := range h.allIdentityDirs(ctx) {
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

// allIdentityDirs returns all identity directories across worktrees.
// It looks for .thrum/identities directories in known worktree locations.
func (h *TmuxHandler) allIdentityDirs(ctx context.Context) []string {
	var dirs []string
	// Primary identities dir
	primary := filepath.Join(h.thrumDir, "identities")
	dirs = append(dirs, primary)

	// Also scan worktrees via git
	repoDir := filepath.Dir(h.thrumDir)
	out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "worktree", "list", "--porcelain").Output() // #nosec G204 -- repoDir from daemon config
	if err != nil {
		return dirs
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			wtPath := strings.TrimPrefix(line, "worktree ")
			idDir := filepath.Join(wtPath, ".thrum", "identities")
			if idDir != primary {
				dirs = append(dirs, idDir)
			}
		}
	}
	return dirs
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

func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
