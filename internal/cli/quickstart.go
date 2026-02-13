package cli

import (
	"fmt"
	"strings"

	"github.com/leonletto/thrum/internal/runtime"
)

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

	// Step 1: Register agent
	regOpts := AgentRegisterOptions{
		Name:    opts.Name,
		Role:    opts.Role,
		Module:  opts.Module,
		Display: opts.Display,
	}

	regResult, err := AgentRegister(client, regOpts)
	if err != nil {
		return nil, fmt.Errorf("register failed: %w", err)
	}

	// If conflict, try re-register automatically
	if regResult.Status == "conflict" {
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

	sessResult, err := SessionStart(client, sessOpts)
	if err != nil {
		return nil, fmt.Errorf("session start failed: %w", err)
	}
	result.Session = sessResult

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
