package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	agentcontext "github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/runtime"
	ttmux "github.com/leonletto/thrum/internal/tmux"
	"github.com/leonletto/thrum/internal/worktree"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:4398-4455
// Destination: sync_cmd.go:26-83
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: b941148d85
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func syncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Control sync operations",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show sync status",
		Long:  `Display the current sync loop status, last sync time, and any errors.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.SyncStatus(client)
			if err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(result)
			}
			fmt.Print(cli.FormatSyncStatus(result))
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "force",
		Short: "Force immediate sync",
		Long: `Trigger an immediate sync operation (non-blocking).

This will fetch new messages from the remote and push local messages.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.SyncForce(client)
			if err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(result)
			}
			fmt.Print(cli.FormatSyncForce(result))
			return nil
		},
	})

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:4457-4737
// Destination: sync_cmd.go:91-371
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: b941148d85
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func quickstartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quickstart",
		Short: "Register, start session, and set intent in one step",
		Long: `Bootstrap an agent session with a single command.

Chains together: runtime detect → config generate → agent register →
session start → set intent. If the agent is already registered, it
re-registers automatically.

Examples:
  thrum quickstart --name implementer_auth --role implementer --module auth
  thrum quickstart --name reviewer_auth --role reviewer --module auth --intent "Reviewing PR #42"
  thrum quickstart --name alice --role impl --module auth --runtime codex
  thrum quickstart --name bob --role tester --module api --dry-run
  thrum quickstart --name planner_core --role planner --module core --no-init`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			display, _ := cmd.Flags().GetString("display")
			intent, _ := cmd.Flags().GetString("intent")
			runtimeFlag, _ := cmd.Flags().GetString("runtime")
			preambleFile, _ := cmd.Flags().GetString("preamble-file")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			noInit, _ := cmd.Flags().GetBool("no-init")
			forceInit, _ := cmd.Flags().GetBool("force")
			noAgentPID, _ := cmd.Flags().GetBool("no-agent-pid")

			// Validate runtime if specified
			if runtimeFlag != "" && !runtime.IsValidRuntime(runtimeFlag) {
				return fmt.Errorf("unknown runtime %q; supported: %s", runtimeFlag, strings.Join(runtime.SupportedRuntimes(), ", "))
			}

			// THRUM_NAME env var sets a default name; explicit --name flag takes precedence.
			if envName := os.Getenv("THRUM_NAME"); envName != "" && !cmd.Flags().Changed("name") {
				name = envName
			}

			// Validate name if provided
			if name != "" {
				if err := identity.ValidateAgentName(name); err != nil {
					return fmt.Errorf("invalid agent name: %w", err)
				}
			}

			// Reuse existing identity if one exists for this worktree.
			// This prevents duplicate agent registration when quickstart is called
			// both from the shell and from an AI agent's session startup.
			if !forceInit {
				existingCfg, err := config.LoadWithPath(flagRepo, "", "")
				if err == nil && existingCfg.Agent.Name != "" {
					switch name {
					case "":
						// No --name given (automated/template call): adopt existing identity,
						// but respect explicitly-passed --role/--module flags.
						name = existingCfg.Agent.Name
						if !cmd.Flags().Changed("role") {
							flagRole = existingCfg.Agent.Role
						}
						if !cmd.Flags().Changed("module") {
							flagModule = existingCfg.Agent.Module
						}
						if display == "" && existingCfg.Agent.Display != "" {
							display = existingCfg.Agent.Display
						}
					case existingCfg.Agent.Name:
						// Same --name as existing: re-register (update role/module if changed).
					default:
						// Different --name than existing: warn about replacing the identity.
						fmt.Fprintf(os.Stderr, "Note: Existing agent @%s found (role=%s, module=%s).\n",
							existingCfg.Agent.Name, existingCfg.Agent.Role, existingCfg.Agent.Module)
						fmt.Fprintf(os.Stderr, "  Registering new agent @%s will replace the existing identity.\n", name)
						fmt.Fprintf(os.Stderr, "  Use --force to skip this warning, or omit --name to reuse @%s.\n\n",
							existingCfg.Agent.Name)
					}
				}
			}

			// Validate after identity reuse so file values can satisfy the requirement
			if flagRole == "" || flagModule == "" {
				return fmt.Errorf("--role and --module are required (or set THRUM_ROLE and THRUM_MODULE env vars)")
			}

			// Compute default intent if none provided
			if intent == "" {
				repoName := cli.GetRepoName(flagRepo)
				intent = cli.DefaultIntent(flagRole, repoName)
			}

			opts := cli.QuickstartOptions{
				Name:         name,
				Role:         flagRole,
				Module:       flagModule,
				Display:      display,
				Intent:       intent,
				PreambleFile: preambleFile,
				Runtime:      runtimeFlag,
				RepoPath:     flagRepo,
				DryRun:       dryRun,
				NoInit:       noInit,
				Force:        forceInit,
				NoAgentPID:   noAgentPID,
			}

			// In dry-run mode, we don't need a daemon connection
			var client *cli.Client
			if !dryRun {
				var err error
				// Use non-refreshing client: quickstart runs its own explicit
				// RefreshLocalIdentity call after SessionStart succeeds (or
				// as part of the identity enrichment block). Running an
				// auto-refresh here would race against the registration
				// the quickstart itself is performing.
				client, err = getClientNoRefresh()
				if err != nil {
					return fmt.Errorf("failed to connect to daemon: %w", err)
				}
				defer func() { _ = client.Close() }()
			}

			result, err := cli.Quickstart(client, opts)
			if err != nil {
				return err
			}

			// Save identity file on successful registration
			if result.Register != nil && (result.Register.Status == "registered" || result.Register.Status == "updated") {
				// Use the daemon-generated agent ID as the name if none was provided.
				// This ensures subsequent CLI calls resolve to the same identity.
				savedName := name
				if savedName == "" {
					savedName = result.Register.AgentID
					// Clean up legacy unnamed identity file (role_module.json)
					// to avoid ambiguity with the new properly-named file.
					legacyFile := filepath.Join(flagRepo, ".thrum", "identities",
						fmt.Sprintf("%s_%s.json", flagRole, flagModule))
					_ = os.Remove(legacyFile)
				}

				thrumDir := filepath.Join(flagRepo, ".thrum")

				// Prefer an existing identity file when its name matches — the
				// library's enrichment block (internal/cli/quickstart.go Step 2.5)
				// only runs when LoadIdentityWithPath succeeds, so on a pre-
				// existing file it will have populated AgentPID and other
				// drift-prone fields we don't want to clobber. For first-time
				// quickstart the load returns "no identity file" and we build
				// a fresh struct below, then the TmuxSession block further down
				// backfills the tmux target (thrum-enlw.8 — without that
				// backfill, findIdentityForSession can't match the session to
				// this identity and the permission-prompt pipeline is silent).
				// Name-mismatch case: a stale identity from `thrum init`
				// (e.g. implementer_main) must not prevent creation of the
				// correct identity file.
				idFile, _, loadErr := config.LoadIdentityWithPath(flagRepo)
				if loadErr != nil || idFile == nil || idFile.Agent.Name != savedName {
					// Create a new identity file: no existing file, or name mismatch
					wtPath, wtErr := worktree.NormalizeWorktreePath(flagRepo)
					if wtErr != nil {
						return fmt.Errorf("normalize worktree path: %w", wtErr)
					}
					idFile = &config.IdentityFile{
						Version: 4,
						RepoID:  cli.GetRepoID(flagRepo),
						Agent: config.AgentConfig{
							Kind:    "agent",
							Name:    savedName,
							Role:    flagRole,
							Module:  flagModule,
							Display: cli.AutoDisplay(flagRole, flagModule),
						},
						Worktree: wtPath,
						Branch:   cli.GetCurrentBranch(flagRepo),
						Intent:   intent,
					}
				}

				// Default agent status to "idle" for new or re-registered agents
				if idFile.AgentStatus == "" {
					idFile.AgentStatus = "idle"
					idFile.AgentStatusUpdatedAt = time.Now().UTC()
				}

				// Update fields that the cobra handler is responsible for
				if display != "" {
					idFile.Agent.Display = display
				}
				if result.Session != nil {
					idFile.SessionID = result.Session.SessionID
				}
				if idFile.ContextFile == "" {
					idFile.ContextFile = fmt.Sprintf("context/%s.md", savedName)
				}
				if runtimeFlag != "" && idFile.PreferredRuntime != runtimeFlag {
					idFile.PreferredRuntime = runtimeFlag
				}

				// Populate TmuxSession from live tmux state when we're running
				// inside a tmux pane. Mirrors guard.reconcileDrift's contract:
				// only writes when a target is resolvable, never clears an
				// existing value. Without this, the FIRST identity-file write
				// on a fresh quickstart (library enrichment skipped because
				// the file didn't exist yet) is missing tmux_session entirely,
				// and findIdentityForSession in daemon/rpc/tmux.go can't match
				// the session name back to this identity. That silently breaks
				// permission-prompt detection for the window between quickstart
				// and the next `thrum` CLI call that would trigger guard.Check
				// → reconcileDrift (thrum-enlw.8).
				//
				// thrum-l9s1: route the resolved target through
				// worktree.PaneTargetForIdentity so a `thrum quickstart` run
				// from a coordinator pane (with cwd resolving to a different
				// worktree) doesn't write the coordinator's pane into the
				// new agent's identity file. flagRepo is the target identity's
				// worktree path; the helper compares the caller's pane
				// session-name against the sanitized basename of flagRepo
				// and refuses cross-worktree writes silently (no field
				// changed when it returns "").
				if ttmux.InTmux() {
					if target, err := ttmux.PaneTarget(cmd.Context()); err == nil {
						safe := worktree.PaneTargetForIdentity(target, flagRepo, worktree.SafeTmuxOpts{})
						if safe != "" && idFile.TmuxSession != safe {
							idFile.TmuxSession = safe
						}
					}
				}

				if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to save identity file: %v\n", err)
				}

				// Bootstrap context files
				// Create empty context file if it doesn't already exist
				ctxPath := agentcontext.ContextPath(thrumDir, savedName)
				if _, statErr := os.Stat(ctxPath); os.IsNotExist(statErr) { // #nosec G703 -- ctxPath is from agentcontext.ContextPath(), an internal .thrum directory path
					if err := agentcontext.Save(thrumDir, savedName, []byte("")); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: failed to create context file: %v\n", err)
					}
				}

				// Apply preamble: --preamble-file > role template > default
				if err := applyRolePreamble(thrumDir, savedName, flagRole, preambleFile, false); err != nil {
					return err
				}
			} else if name != "" && !dryRun {
				// Daemon unreachable or registration skipped — still ensure preamble exists.
				// EnsurePreamble is a no-op when the file already exists, so this is always safe.
				thrumDir := filepath.Join(flagRepo, ".thrum")
				if err := applyRolePreamble(thrumDir, name, flagRole, preambleFile, false); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to ensure preamble: %v\n", err)
				}
			}

			if flagJSON {
				return cli.EmitJSON(result)
			}
			fmt.Print(cli.FormatQuickstart(result))
			if !flagQuiet {
				fmt.Print(cli.LegacyHint("quickstart", flagQuiet, flagJSON))
			}
			return nil
		},
	}

	cmd.Flags().String("name", "", "Human-readable agent name (optional, defaults to role_hash)")
	cmd.Flags().String("display", "", "Display name for the agent")
	cmd.Flags().String("intent", "", "Initial work intent")
	cmd.Flags().String("runtime", "", "Runtime preset (see 'thrum runtime list')")
	cmd.Flags().Bool("dry-run", false, "Preview changes without writing files or registering")
	cmd.Flags().Bool("no-init", false, "Skip runtime config generation, just register agent")
	cmd.Flags().Bool("force", false, "Overwrite existing runtime config files")
	cmd.Flags().String("preamble-file", "", "Custom preamble file to compose with default preamble")
	// --no-agent-pid is intended for `thrum tmux create`'s inline
	// quickstart. The inline caller is a short-lived subshell whose
	// PID dies immediately; persisting it breaks `thrum tmux launch`'s
	// G4 writer-liveness check. Direct shell use is allowed but
	// unusual — first /thrum:prime from the runtime will reclaim the
	// PID via guard.WritePID (thrum-x6e8.6).
	cmd.Flags().Bool("no-agent-pid", false, "Persist agent_pid=0 instead of detecting the runtime ancestor (for inline tmux quickstart; defer PID claim to first /thrum:prime)")

	return cmd
}
