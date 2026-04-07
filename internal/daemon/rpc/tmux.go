package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/leonletto/thrum/internal/config"
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

	var launchCmd string
	switch runtime {
	case "claude":
		launchCmd = "claude"
	case "opencode":
		launchCmd = "opencode"
	case "aider":
		launchCmd = "aider"
	case "shell":
		launchCmd = "" // already has a shell
	default:
		return nil, fmt.Errorf("unsupported runtime %q (supported: claude, opencode, aider, shell)", runtime)
	}

	target := req.Name + ":0.0"
	if launchCmd != "" {
		if err := ttmux.SendKeys(target, launchCmd); err != nil {
			return nil, fmt.Errorf("launch send-keys: %w", err)
		}
		if err := ttmux.SendSpecialKey(target, "Enter"); err != nil {
			return nil, fmt.Errorf("launch enter: %w", err)
		}
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

// HandleStatus scans identity files for managed tmux sessions and reports their state.
func (h *TmuxHandler) HandleStatus(ctx context.Context, params json.RawMessage) (any, error) {
	identitiesDir := filepath.Join(h.thrumDir, "identities")
	entries, err := os.ReadDir(identitiesDir)
	if err != nil {
		return &TmuxStatusResponse{Sessions: []TmuxSessionInfo{}}, nil
	}

	var sessions []TmuxSessionInfo
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
			// Clean stale tmux_session from identity file
			h.clearTmuxFromIdentities(session)
		} else if idFile.ClaudePID > 0 && !isProcessAlive(idFile.ClaudePID) {
			info.State = "stale"
		} else {
			info.State = "alive"
		}

		sessions = append(sessions, info)
	}

	return &TmuxStatusResponse{Sessions: sessions}, nil
}

// HandleCheckPane is the handler for the tmux check-pane silence hook.
// Receives session/reason/content from the CLI check-pane command.
// Currently logs the event; full coordinator notification is deferred.
func (h *TmuxHandler) HandleCheckPane(ctx context.Context, params json.RawMessage) (any, error) {
	// Skeletal handler — accepts the request without error so the tmux
	// silence hook doesn't produce noise. Full notification flow deferred.
	return nil, nil
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
// matching the given session name.
func (h *TmuxHandler) clearTmuxFromIdentities(sessionName string) {
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
		sess, _, _ := ttmux.ParseTarget(idFile.TmuxSession)
		if sess == sessionName {
			idFile.TmuxSession = ""
			idFile.Runtime = ""
			updated, _ := json.MarshalIndent(idFile, "", "  ")
			_ = os.WriteFile(path, updated, 0600) // #nosec G306 -- identity file permissions
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
