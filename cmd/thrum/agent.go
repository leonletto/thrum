package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/worktree"
	"github.com/spf13/cobra"
)

// printAgentSummaryField emits the bare value of a single field from
// AgentSummary, newline-terminated. Unknown fields return an error so
// scripts fail loudly rather than silently consuming the empty string.
// ORIGIN[thrum-8kxh]: moved from main.go:1381-1388
// Destination: agent.go:27-34
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 70df71eabd
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func printAgentSummaryField(w io.Writer, s *cli.AgentSummary, field string) error {
	value, ok := agentSummaryField(s, field)
	if !ok {
		return fmt.Errorf("unknown field %q (known: agent_id, role, module, display, branch, worktree, intent, repo_id, session_id, session_start, identity_file, updated_at, source, status, host, pid, tmux_session, tmux_alive)", field)
	}
	_, err := fmt.Fprintln(w, value)
	return err
}

// agentSummaryField returns the stringified value of a named field on
// AgentSummary, plus a boolean ok flag. Booleans render as "true"/"false".
// Zero integers render as "0". Empty strings render as the empty string
// (the newline from Fprintln is still emitted so callers can | xargs etc.).
// ORIGIN[thrum-8kxh]: moved from main.go:1394-1435
// Destination: agent.go:46-87
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 70df71eabd
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func agentSummaryField(s *cli.AgentSummary, field string) (string, bool) {
	switch field {
	case "agent_id":
		return s.AgentID, true
	case "role":
		return s.Role, true
	case "module":
		return s.Module, true
	case "display":
		return s.Display, true
	case "branch":
		return s.Branch, true
	case "worktree":
		return s.Worktree, true
	case "intent":
		return s.Intent, true
	case "repo_id":
		return s.RepoID, true
	case "session_id":
		return s.SessionID, true
	case "session_start":
		return s.SessionStart, true
	case "identity_file":
		return s.IdentityFile, true
	case "updated_at":
		return s.UpdatedAt, true
	case "source":
		return s.Source, true
	case "status":
		return s.Status, true
	case "host":
		return s.Host, true
	case "pid":
		return strconv.Itoa(s.PID), true
	case "tmux_session":
		return s.TmuxSession, true
	case "tmux_alive":
		return strconv.FormatBool(s.TmuxAlive), true
	default:
		return "", false
	}
}

// runWhoami is the shared implementation for both top-level `thrum whoami`
// and `thrum agent whoami`. It loads identity, optionally enriches from the
// daemon, then prints the result.
// ORIGIN[thrum-8kxh]: moved from main.go:1440-1474
// Destination: agent.go:98-132
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 70df71eabd
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func runWhoami(cmd *cobra.Command, args []string) error {
	identityFile, identityPath, err := config.LoadIdentityWithPath(flagRepo)
	if err != nil {
		thrumDir := filepath.Join(flagRepo, ".thrum")
		if _, statErr := os.Stat(thrumDir); os.IsNotExist(statErr) {
			return fmt.Errorf("thrum not initialized in this repository\n  Run 'thrum init' first")
		}
		identitiesDir := filepath.Join(thrumDir, "identities")
		if _, statErr := os.Stat(identitiesDir); os.IsNotExist(statErr) {
			return fmt.Errorf("no agent identities registered\n  Run 'thrum quickstart --name <agent-name> --role <role> --module <module>' to register")
		}
		return err
	}

	// Try daemon enrichment (non-fatal)
	var daemonInfo *cli.WhoamiResult
	if client, clientErr := getClient(); clientErr == nil {
		defer func() { _ = client.Close() }()
		if result, rpcErr := cli.AgentWhoami(client, identityFile.Agent.Name); rpcErr == nil {
			daemonInfo = result
		}
	}

	summary := cli.BuildAgentSummary(identityFile, identityPath, daemonInfo)

	if field, _ := cmd.Flags().GetString("field"); field != "" {
		return printAgentSummaryField(cmd.OutOrStdout(), summary, field)
	}

	if flagJSON {
		return cli.EmitJSON(summary)
	}
	fmt.Print(cli.FormatAgentSummary(summary))
	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:1476-1495
// Destination: agent.go:140-159
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 70df71eabd
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func whoamiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show current agent identity",
		Long: `Show current agent identity information.

Shows the current agent identity. Reads directly from
.thrum/identities/*.json files.

Examples:
  thrum whoami
  thrum whoami --json
  THRUM_NAME=alice thrum whoami`,
		RunE: runWhoami,
	}

	cmd.Flags().String("field", "", "Print a single field's value (e.g. agent_id, tmux_alive) and exit")

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:1671-2095
// Destination: agent.go:167-591
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 70df71eabd
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func agentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage agent identity",
	}

	registerCmd := &cobra.Command{
		Use:   "register",
		Short: "Register this agent",
		Long: `Register this agent with the specified role and module.

The agent identity is determined from:
1. --name flag (highest priority)
2. THRUM_NAME env var (default when --name is not provided)
3. Environment variables (THRUM_ROLE, THRUM_MODULE for role/module)
4. Identity file in .thrum/identities/ directory`,
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			reRegister, _ := cmd.Flags().GetBool("re-register")
			display, _ := cmd.Flags().GetString("display")
			name, _ := cmd.Flags().GetString("name")

			// Use flagRole and flagModule from global flags
			if flagRole == "" || flagModule == "" {
				return fmt.Errorf("role and module are required (use --role and --module flags or THRUM_ROLE and THRUM_MODULE env vars)")
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

			opts := cli.AgentRegisterOptions{
				Name:       name,
				Role:       flagRole,
				Module:     flagModule,
				Display:    display,
				Force:      force,
				ReRegister: reRegister,
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.AgentRegister(client, opts)
			if err != nil {
				return err
			}

			// Save identity file on successful registration
			if result.Status == "registered" || result.Status == "updated" {
				// Use the daemon-generated agent ID as the name if none was provided.
				// This ensures subsequent CLI calls resolve to the same identity.
				savedName := name
				if savedName == "" {
					savedName = result.AgentID
					// Clean up legacy unnamed identity file (role_module.json)
					// to avoid ambiguity with the new properly-named file.
					legacyFile := filepath.Join(flagRepo, ".thrum", "identities",
						fmt.Sprintf("%s_%s.json", flagRole, flagModule))
					_ = os.Remove(legacyFile)
				}
				wtPath, err := worktree.NormalizeWorktreePath(flagRepo)
				if err != nil {
					return fmt.Errorf("normalize worktree path: %w", err)
				}
				identity := &config.IdentityFile{
					Version: 3,
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
					Intent:   cli.DefaultIntent(flagRole, cli.GetRepoName(flagRepo)),
				}
				thrumDir := filepath.Join(flagRepo, ".thrum")
				if err := config.SaveIdentityFile(thrumDir, identity); err != nil {
					// Warn but don't fail - registration succeeded
					fmt.Fprintf(os.Stderr, "Warning: failed to save identity file: %v\n", err)
				}

				// Apply preamble: role template > default (no --preamble-file for register)
				if err := applyRolePreamble(thrumDir, savedName, flagRole, "", false); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to apply preamble: %v\n", err)
				}
			}

			if flagJSON {
				if err := cli.EmitJSON(result); err != nil {
					return err
				}
			} else {
				// Human-readable formatted output
				fmt.Print(cli.FormatRegisterResponse(result))
				if result.Status == "registered" || result.Status == "updated" {
					fmt.Print(cli.LegacyHint("agent.register", flagQuiet, flagJSON))
				}
			}

			// Exit with error code if there was a conflict
			if result.Status == "conflict" {
				os.Exit(1)
			}

			return nil
		},
	}
	registerCmd.Flags().String("name", "", "Human-readable agent name (optional, defaults to role_hash)")
	registerCmd.Flags().Bool("force", false, "Force registration (override existing)")
	registerCmd.Flags().Bool("re-register", false, "Re-register same agent")
	registerCmd.Flags().String("display", "", "Display name for the agent")
	cmd.AddCommand(registerCmd)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List registered agents",
		Long: `List all registered agents, optionally filtered by role or module.

Use --context to show work context (branch, commits, intent) for each agent.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filterRole, _ := cmd.Flags().GetString("role")
			filterModule, _ := cmd.Flags().GetString("module")
			showContext, _ := cmd.Flags().GetBool("context")

			if showContext {
				// Show work context table instead of agent list
				client, err := getClient()
				if err != nil {
					return fmt.Errorf("failed to connect to daemon: %w", err)
				}
				defer func() { _ = client.Close() }()

				result, err := cli.AgentListContext(client, "", "", "")
				if err != nil {
					return err
				}

				if flagJSON {
					return cli.EmitJSON(result)
				}
				fmt.Print(cli.FormatContextList(result))
				return nil
			}

			opts := cli.AgentListOptions{
				Role:   filterRole,
				Module: filterModule,
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.AgentList(client, opts)
			if err != nil {
				return err
			}

			// Also fetch work contexts for enhanced display
			contexts, err := cli.AgentListContext(client, "", "", "")
			if err != nil {
				// Fallback to basic format if context fetch fails
				contexts = nil
			}

			if flagJSON {
				var body any = result
				if contexts != nil {
					body = map[string]any{
						"agents":   result,
						"contexts": contexts,
					}
				}
				return cli.EmitJSON(body)
			}
			// Human-readable formatted output with enhanced info
			fmt.Print(cli.FormatAgentListWithContext(result, contexts))
			return nil
		},
	}
	listCmd.Flags().String("role", "", "Filter by role")
	listCmd.Flags().String("module", "", "Filter by module")
	listCmd.Flags().Bool("context", false, "Show work context (branch, commits, intent)")
	cmd.AddCommand(listCmd)

	agentWhoamiCmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show current agent identity",
		Long: `Show the current agent identity and active session.

Identity is resolved from:
1. Command-line flags (--role, --module)
2. Environment variables (THRUM_ROLE, THRUM_MODULE, THRUM_NAME)
3. Identity files in .thrum/identities/ directory`,
		RunE: runWhoami,
	}
	agentWhoamiCmd.Flags().String("field", "", "Print a single field's value (e.g. agent_id, tmux_alive) and exit")
	cmd.AddCommand(agentWhoamiCmd)

	deleteCmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an agent",
		Long: `Delete an agent and all its associated data.

This removes:
- Agent identity file (identities/<name>.json)
- Agent message file (messages/<name>.jsonl)
- Agent record from the database

Examples:
  thrum agent delete furiosa
  thrum agent delete coordinator_1B9K
  thrum agent delete coordinator_1B9K --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := args[0]
			force, _ := cmd.Flags().GetBool("force")

			// Confirm deletion (unless --force)
			if !force {
				fmt.Printf("Delete agent '%s' and all associated data? [y/N] ", agentName)
				var response string
				_, _ = fmt.Scanln(&response)
				if response != "y" && response != "Y" {
					fmt.Println("Deletion canceled.")
					return nil
				}
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.AgentDelete(client, cli.AgentDeleteOptions{Name: agentName})
			if err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(result)
			}
			fmt.Print(cli.FormatAgentDelete(result))
			return nil
		},
	}
	deleteCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	cmd.AddCommand(deleteCmd)

	cleanupCmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Clean up orphaned agents",
		Long: `Detect and remove orphaned agents whose worktrees or branches no longer exist.

This command scans all registered agents and identifies orphans based on:
- Missing worktree (deleted from filesystem)
- Missing branch (deleted from git)
- Stale agents (not seen in a long time)

For each orphan found, you'll be prompted to confirm deletion.

Examples:
  thrum agent cleanup                  # Interactive cleanup
  thrum agent cleanup --dry-run        # List orphans without deleting
  thrum agent cleanup --force          # Delete all orphans without prompting`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			force, _ := cmd.Flags().GetBool("force")
			threshold, _ := cmd.Flags().GetInt("threshold")

			if dryRun && force {
				return fmt.Errorf("--dry-run and --force are mutually exclusive")
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.AgentCleanup(client, cli.AgentCleanupOptions{
				DryRun:    dryRun,
				Force:     force,
				Threshold: threshold,
			})
			if err != nil {
				return err
			}

			// Handle JSON output
			if flagJSON {
				return cli.EmitJSON(result)
			}

			// Display results
			fmt.Print(cli.FormatAgentCleanup(result))

			// If interactive mode (not force, not dry-run) and orphans found, prompt for deletion
			if !force && !dryRun && len(result.Orphans) > 0 {
				fmt.Println("\nDelete these orphaned agents? [y/N]")
				var response string
				_, _ = fmt.Scanln(&response)
				if response == "y" || response == "Y" {
					// Delete each orphan
					deleted := 0
					for _, orphan := range result.Orphans {
						_, err := cli.AgentDelete(client, cli.AgentDeleteOptions{Name: orphan.AgentID})
						if err != nil {
							fmt.Printf("✗ Failed to delete %s: %v\n", orphan.AgentID, err)
						} else {
							fmt.Printf("✓ Deleted %s\n", orphan.AgentID)
							deleted++
						}
					}
					fmt.Printf("\n✓ Deleted %d orphaned agent(s)\n", deleted)
				} else {
					fmt.Println("Cleanup canceled.")
				}
			}

			return nil
		},
	}
	cleanupCmd.Flags().Bool("dry-run", false, "List orphans without deleting")
	cleanupCmd.Flags().Bool("force", false, "Delete all orphans without prompting")
	cleanupCmd.Flags().Int("threshold", 30, "Days since last seen to consider agent stale")
	cmd.AddCommand(cleanupCmd)

	// Agent-centric aliases for session commands
	// These share RunE functions with session commands (no code duplication)

	agentStartCmd := &cobra.Command{
		Use:   "start",
		Short: "Start a new session (alias for 'thrum session start')",
		Long: `Start a new work session for the current agent.

This is an alias for 'thrum session start'.
The agent must be registered first (use 'thrum quickstart').`,
		RunE: sessionStartRunE,
	}
	cmd.AddCommand(agentStartCmd)

	agentEndCmd := &cobra.Command{
		Use:   "end",
		Short: "End current session (alias for 'thrum session end')",
		Long: `End the current active session.

This is an alias for 'thrum session end'.`,
		RunE: sessionEndRunE,
	}
	agentEndCmd.Flags().String("reason", "normal", "End reason (normal|crash)")
	agentEndCmd.Flags().String("session-id", "", "Session ID to end (defaults to current session)")
	cmd.AddCommand(agentEndCmd)

	agentSetIntentCmd := &cobra.Command{
		Use:   "set-intent TEXT",
		Short: "Set work intent (alias for 'thrum session set-intent')",
		Long: `Set the work intent for the current session.

This is an alias for 'thrum session set-intent'.
Pass an empty string to clear the intent.

Examples:
  thrum agent set-intent "Fixing memory leak in connection pool"
  thrum agent set-intent ""   # clear intent`,
		Args: cobra.ExactArgs(1),
		RunE: sessionSetIntentRunE,
	}
	cmd.AddCommand(agentSetIntentCmd)

	agentHeartbeatCmd := &cobra.Command{
		Use:   "heartbeat",
		Short: "Send heartbeat (alias for 'thrum session heartbeat')",
		Long: `Send a heartbeat for the current session.

This is an alias for 'thrum session heartbeat'.
Triggers git context extraction and updates the agent's last-seen time.`,
		RunE: sessionHeartbeatRunE,
	}
	agentHeartbeatCmd.Flags().StringSlice("add-scope", nil, "Add scope (repeatable, format: type:value)")
	agentHeartbeatCmd.Flags().StringSlice("remove-scope", nil, "Remove scope (repeatable, format: type:value)")
	agentHeartbeatCmd.Flags().StringSlice("add-ref", nil, "Add ref (repeatable, format: type:value)")
	agentHeartbeatCmd.Flags().StringSlice("remove-ref", nil, "Remove ref (repeatable, format: type:value)")
	cmd.AddCommand(agentHeartbeatCmd)

	agentSetTaskCmd := &cobra.Command{
		Use:   "set-task TASK",
		Short: "Set current task (alias for 'thrum session set-task')",
		Long: `Set the current task identifier for the session.

This is an alias for 'thrum session set-task'.
Pass an empty string to clear the task.

Examples:
  thrum agent set-task beads:thrum-xyz
  thrum agent set-task ""   # clear task`,
		Args: cobra.ExactArgs(1),
		RunE: sessionSetTaskRunE,
	}
	cmd.AddCommand(agentSetTaskCmd)
	cmd.AddCommand(agentSetStatusCmd())
	cmd.AddCommand(reminderCmd())
	cmd.AddCommand(agentSessionsCmd())
	cmd.AddCommand(agentStateCmd())

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:2237-2266
// Destination: agent.go:599-628
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 70df71eabd
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func agentSetStatusCmd() *cobra.Command {
	var targetAgent string
	cmd := &cobra.Command{
		Use:   "set-status <working|idle|blocked>",
		Short: "Set agent operational status",
		Long: `Set the operational status for an agent.

Valid statuses: working, idle, blocked.

Without --agent, updates the local agent's identity file directly.
With --agent, sends a daemon RPC to update a remote agent's status.

Examples:
  thrum agent set-status working
  thrum agent set-status idle --agent impl_team_fix`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status := args[0]
			if status != "working" && status != "idle" && status != "blocked" {
				return fmt.Errorf("invalid status %q: must be working, idle, or blocked", status)
			}
			if targetAgent != "" {
				return setRemoteAgentStatus(targetAgent, status)
			}
			return setLocalAgentStatus(status)
		},
	}
	cmd.Flags().StringVar(&targetAgent, "agent", "", "Target agent name (uses daemon RPC for remote write)")
	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:2268-2287
// Destination: agent.go:636-655
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 70df71eabd
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func setLocalAgentStatus(status string) error {
	idFile, _, err := config.LoadIdentityWithPath(flagRepo)
	if err != nil {
		return fmt.Errorf("load identity: %w", err)
	}
	idFile.AgentStatus = status
	idFile.AgentStatusUpdatedAt = time.Now().UTC()
	// flagRepo was already resolved by PersistentPreRunE upstream; do not
	// re-wrap in EffectiveRepoPath here. Load via LoadIdentityWithPath
	// (post-v0.10.1) no longer applies the inner resolution, so re-wrapping
	// the save path was a load/save asymmetry that would silently target
	// different directories if a future caller bypassed PersistentPreRunE.
	// See thrum-8nro.1.
	thrumDir := filepath.Join(flagRepo, ".thrum")
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		return fmt.Errorf("save identity: %w", err)
	}
	fmt.Printf("✓ Status set to %s\n", status)
	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:2289-2304
// Destination: agent.go:663-678
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 70df71eabd
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func setRemoteAgentStatus(agentName, status string) error {
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("daemon not running: %w", err)
	}
	defer func() { _ = client.Close() }()
	var result map[string]any
	if err := client.Call("agent.set-status", map[string]string{
		"agent":  agentName,
		"status": status,
	}, &result); err != nil {
		return fmt.Errorf("set remote status: %w", err)
	}
	fmt.Printf("✓ Status for %s set to %s\n", agentName, status)
	return nil
}
