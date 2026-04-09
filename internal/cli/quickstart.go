package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/config"
	agentcontext "github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/process"
	"github.com/leonletto/thrum/internal/runtime"
	ttmux "github.com/leonletto/thrum/internal/tmux"
	"github.com/leonletto/thrum/internal/types"
)

// detectTmuxSession returns the current tmux pane target if running inside tmux,
// or an empty string if not in tmux.
func detectTmuxSession() (string, error) {
	if !ttmux.InTmux() {
		return "", nil
	}
	return ttmux.PaneTarget()
}

// QuickstartOptions contains options for the quickstart command.
type QuickstartOptions struct {
	Name         string
	Role         string
	Module       string
	Display      string
	Intent       string
	PreambleFile string // Path to custom preamble file to compose with default
	ReRegister   bool

	// Runtime options (Epic 2)
	Runtime  string // Runtime preset name (overrides auto-detection)
	RepoPath string // Repository path for runtime init
	DryRun   bool   // Preview changes without writing
	NoInit   bool   // Skip config file generation
	Force    bool   // Overwrite existing files
}

// QuickstartResult contains the combined result of quickstart steps.
type QuickstartResult struct {
	Register    *RegisterResponse     `json:"register,omitempty"`
	Session     *SessionStartResponse `json:"session,omitempty"`
	Intent      *SetIntentResponse    `json:"intent,omitempty"`
	RuntimeInit *RuntimeInitResult    `json:"runtime_init,omitempty"`
	RuntimeName string                `json:"runtime,omitempty"`
	Detected    bool                  `json:"runtime_detected,omitempty"`
}

// Quickstart registers an agent, starts a session, optionally sets intent,
// and optionally generates runtime-specific config files.
func Quickstart(client *Client, opts QuickstartOptions) (*QuickstartResult, error) {
	result := &QuickstartResult{}

	// Step 0: Runtime detection and config generation
	if !opts.NoInit {
		runtimeName := opts.Runtime
		detected := false
		if runtimeName == "" {
			runtimeName = runtime.DetectRuntime(opts.RepoPath)
			detected = true
		}
		result.RuntimeName = runtimeName
		result.Detected = detected

		// Generate config files if runtime is not "cli-only" or explicitly specified
		if runtimeName != "" && runtimeName != "cli-only" || opts.Runtime != "" {
			initOpts := RuntimeInitOptions{
				RepoPath:  opts.RepoPath,
				Runtime:   runtimeName,
				DryRun:    opts.DryRun,
				Force:     opts.Force,
				AgentName: opts.Name,
				AgentRole: opts.Role,
				AgentMod:  opts.Module,
			}
			initResult, err := RuntimeInit(initOpts)
			if err != nil {
				// Non-fatal: log but continue with registration
				result.RuntimeInit = &RuntimeInitResult{
					Runtime: runtimeName,
					DryRun:  opts.DryRun,
				}
			} else {
				result.RuntimeInit = initResult
			}
		}
	}

	// In dry-run mode, don't actually register with daemon
	if opts.DryRun {
		return result, nil
	}

	// Capture agent PID and detected runtime for identity resolution
	agentPID, detectedRuntime := process.FindClaudeAncestor()

	// Step 1: Register agent
	regOpts := AgentRegisterOptions{
		Name:       opts.Name,
		Role:       opts.Role,
		Module:     opts.Module,
		Display:    opts.Display,
		ReRegister: opts.ReRegister,
		AgentPID:   agentPID,
	}

	regResult, err := AgentRegister(client, regOpts)
	if err != nil {
		return nil, fmt.Errorf("register failed: %w", err)
	}

	// If conflict, check PID liveness before retrying
	if regResult.Status == "conflict" {
		if regResult.Conflict != nil {
			conflictPID := regResult.Conflict.ConflictPID
			if conflictPID > 0 && conflictPID != agentPID && process.IsRunning(conflictPID) && process.IsRuntimeProcess(conflictPID, "") {
				return nil, fmt.Errorf("cannot register as %q: name is held by a running agent session (PID %d)", opts.Name, conflictPID)
			}
		}
		// Dead or non-runtime PID — safe to retry
		regOpts.ReRegister = true
		regResult, err = AgentRegister(client, regOpts)
		if err != nil {
			return nil, fmt.Errorf("re-register failed: %w", err)
		}
	}

	result.Register = regResult

	// Step 2: Start session
	sessOpts := SessionStartOptions{
		AgentID: regResult.AgentID,
	}

	// Auto-set worktree ref so heartbeat can extract git context
	repoPathForRef := opts.RepoPath
	if repoPathForRef == "" {
		repoPathForRef = "."
	}
	if worktreeRoot := GitTopLevel(repoPathForRef); worktreeRoot != "" {
		sessOpts.Refs = append(sessOpts.Refs, types.Ref{Type: "worktree", Value: worktreeRoot})
	}

	sessResult, err := SessionStart(client, sessOpts)
	if err != nil {
		return nil, fmt.Errorf("session start failed: %w", err)
	}
	result.Session = sessResult

	// Step 2.5: Enrich identity file with v4 fields
	repoPath := opts.RepoPath
	if repoPath == "" {
		repoPath = "."
	}
	if idFile, _, err := config.LoadIdentityWithPath(repoPath); err == nil {
		thrumDir := filepath.Join(repoPath, ".thrum")
		changed := false

		if idFile.Version < 4 {
			idFile.Version = 4
			changed = true
		}
		if idFile.Branch == "" {
			idFile.Branch = GetCurrentBranch(repoPath)
			changed = true
		}
		if idFile.RepoID == "" {
			if repoID := GetRepoID(repoPath); repoID != "" {
				idFile.RepoID = repoID
				changed = true
			}
		}
		if idFile.Agent.Display == "" {
			idFile.Agent.Display = AutoDisplay(idFile.Agent.Role, idFile.Agent.Module)
			changed = true
		}
		if sessResult != nil && sessResult.SessionID != "" {
			idFile.SessionID = sessResult.SessionID
			changed = true
		}
		if opts.Intent != "" && idFile.Intent != opts.Intent {
			idFile.Intent = opts.Intent
			changed = true
		} else if idFile.Intent == "" {
			repoName := GetRepoName(repoPath)
			idFile.Intent = DefaultIntent(idFile.Agent.Role, repoName)
			changed = true
		}

		// Write AgentPID to identity file for restart save
		if agentPID > 0 && idFile.AgentPID != agentPID {
			idFile.AgentPID = agentPID
			changed = true
		}

		// Write detected runtime to identity file
		if detectedRuntime != "" && idFile.Runtime != detectedRuntime {
			idFile.Runtime = detectedRuntime
			changed = true
		}

		// Write PreferredRuntime from --runtime flag
		if opts.Runtime != "" && idFile.PreferredRuntime != opts.Runtime {
			idFile.PreferredRuntime = opts.Runtime
			changed = true
		}

		// Detect tmux session and write to identity file
		if tmuxTarget, err := detectTmuxSession(); err == nil && tmuxTarget != "" {
			if idFile.TmuxSession != tmuxTarget {
				idFile.TmuxSession = tmuxTarget
				changed = true
			}
		}

		if changed {
			_ = config.SaveIdentityFile(thrumDir, idFile)
		}
	}

	// Ensure preamble exists for this agent
	if repoPath != "" {
		thrumDir := filepath.Join(repoPath, ".thrum")
		if err := agentcontext.EnsurePreamble(thrumDir, regResult.AgentID); err != nil {
			// Non-fatal — log but don't fail quickstart
			fmt.Fprintf(os.Stderr, "Warning: failed to create preamble: %v\n", err)
		}
	}

	// Step 3: Set intent (optional)
	if opts.Intent != "" {
		intentResult, err := SessionSetIntent(client, sessResult.SessionID, opts.Intent)
		if err != nil {
			return nil, fmt.Errorf("set intent failed: %w", err)
		}
		result.Intent = intentResult
	}

	return result, nil
}

// FormatQuickstart formats the quickstart result for display.
func FormatQuickstart(result *QuickstartResult) string {
	var output strings.Builder

	// Runtime info
	if result.RuntimeName != "" {
		if result.Detected {
			fmt.Fprintf(&output, "✓ Detected runtime: %s\n", result.RuntimeName)
		} else {
			fmt.Fprintf(&output, "✓ Using runtime: %s\n", result.RuntimeName)
		}
	}

	// Runtime init files
	if result.RuntimeInit != nil && len(result.RuntimeInit.Files) > 0 {
		for _, f := range result.RuntimeInit.Files {
			if f.Skipped {
				continue
			}
			if result.RuntimeInit.DryRun {
				fmt.Fprintf(&output, "  Would %s: %s\n", f.Action, f.Path)
			} else {
				fmt.Fprintf(&output, "  ✓ %s\n", f.Path)
			}
		}
	}

	// Registration
	if result.Register != nil {
		fmt.Fprintf(&output, "✓ Registered as @%s (%s)\n",
			extractRoleFromID(result.Register.AgentID), result.Register.AgentID)
	}

	// Session
	if result.Session != nil {
		fmt.Fprintf(&output, "✓ Session started: %s\n", result.Session.SessionID)
	}

	// Intent
	if result.Intent != nil && result.Intent.Intent != "" {
		fmt.Fprintf(&output, "✓ Intent set: %s\n", result.Intent.Intent)
	}

	return output.String()
}

// extractRoleFromID extracts the role from an agent ID (agent:role:module).
func extractRoleFromID(agentID string) string {
	parts := strings.Split(agentID, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return agentID
}
