package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
	agentcontext "github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/identity/guard"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/process"
	"github.com/leonletto/thrum/internal/runtime"
	ttmux "github.com/leonletto/thrum/internal/tmux"
	"github.com/leonletto/thrum/internal/types"
	"github.com/leonletto/thrum/internal/worktree"
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

	// Identity Guard G1a + G1b: pre-flight before any daemon round-trip.
	// G1a refuses when the caller already owns another identity in this
	// repo (caller must --force to rename the old one aside); G1b refuses
	// when the requested --name is held by a live foreign PID. Both
	// guards are no-ops when their respective modes are "off" in
	// .thrum/config.json.
	qsRepoPath := opts.RepoPath
	if qsRepoPath == "" {
		qsRepoPath = "."
	}
	idsDir := filepath.Join(paths.EffectiveRepoPath(qsRepoPath), ".thrum", "identities")
	qsChain, _ := guard.WalkAncestors(context.Background(), os.Getpid())
	gcfg := guard.LoadConfigFromDir(paths.EffectiveRepoPath(qsRepoPath))
	qc := &guard.QuickstartContext{
		Mode:          gcfg.QuickstartSelfRename,
		IdentitiesDir: idsDir,
		Chain:         qsChain,
		RequestedName: opts.Name,
		Force:         opts.Force,
		IsPIDAlive:    process.IsRunning,
	}
	if err := guard.G1a(qc); err != nil {
		return nil, err
	}
	qc.Mode = gcfg.QuickstartNameCollision
	if err := guard.G1b(qc); err != nil {
		return nil, err
	}

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

	// Capture agent PID for identity resolution and conflict detection.
	// Runtime/PreferredRuntime/branch/tmux fields are handled by
	// RefreshLocalIdentity in Step 2.6 below.
	agentPID, _ := process.FindClaudeAncestor(context.Background())

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
			if conflictPID > 0 && conflictPID != agentPID && process.IsRunning(conflictPID) && process.IsRuntimeProcess(context.Background(), conflictPID, "") {
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

	// Step 2.5: Populate quickstart-specific identity file fields.
	// Fields that drift between sessions (agent_pid, runtime,
	// preferred_runtime, tmux_session, branch) are handled separately by
	// RefreshLocalIdentity in Step 2.6 below.
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
		if sessResult != nil && sessResult.SessionID != "" && idFile.SessionID != sessResult.SessionID {
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

		// Preserve the --runtime flag override (PreferredRuntime) path —
		// this is user intent, not process detection.
		if opts.Runtime != "" && idFile.PreferredRuntime != opts.Runtime {
			idFile.PreferredRuntime = opts.Runtime
			changed = true
		}

		// Backfill runtime from preferred_runtime when missing. Agents
		// created before the runtime field existed store the user's
		// intent in preferred_runtime only. The daemon's permission-prompt
		// detection reads runtime, so we must populate it here.
		if idFile.Runtime == "" && idFile.PreferredRuntime != "" {
			idFile.Runtime = idFile.PreferredRuntime
			changed = true
		}

		if changed {
			idFile.UpdatedAt = time.Now().UTC()
			_ = config.SaveIdentityFile(thrumDir, idFile)
		}

		// thrum-33dt: enforce the worktree-layer single-identity invariant
		// on the quickstart path. G1a/G1b above already refused live
		// foreign-owned collisions; any residual identity files here are
		// stale siblings from prior registrations that never got cleaned
		// up. EnforceOneIdentity deletes them so the worktree satisfies
		// "one identity per worktree" after every successful quickstart.
		if idFile.Agent.Name != "" {
			worktree.EnforceOneIdentity(repoPath, idFile.Agent.Name)
		}
	}

	// Step 2.6: Refresh drift-prone fields from live process/tmux/git state.
	// This replaces the legacy inline pid/runtime/tmux/branch enrichment.
	if _, refreshErr := RefreshLocalIdentity(client, repoPath); refreshErr != nil {
		fmt.Fprintf(os.Stderr, "thrum: quickstart refresh failed: %v\n", refreshErr)
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
