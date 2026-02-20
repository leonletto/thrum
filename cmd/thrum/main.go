package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	agentcontext "github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/cleanup"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/runtime"
	"github.com/leonletto/thrum/internal/subscriptions"
	thrumSync "github.com/leonletto/thrum/internal/sync"
	"github.com/leonletto/thrum/internal/types"
	"github.com/leonletto/thrum/internal/web"
	"github.com/leonletto/thrum/internal/websocket"
	"github.com/spf13/cobra"
)

var (
	// Build info (set via ldflags).
	Version = "dev"
	Build   = "unknown"
)

var (
	// Global flags.
	flagRole    string
	flagModule  string
	flagRepo    string
	flagJSON    bool
	flagQuiet   bool
	flagVerbose bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "thrum",
		Short: "Git-backed agent messaging",
		Long: `Thrum is a Git-backed messaging system for agent coordination.

It enables agents and humans to communicate persistently across
sessions, worktrees, and machines using Git as the sync layer.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags available to all commands
	rootCmd.PersistentFlags().StringVar(&flagRole, "role", "", "Agent role (or THRUM_ROLE env var)")
	rootCmd.PersistentFlags().StringVar(&flagModule, "module", "", "Agent module (or THRUM_MODULE env var)")
	rootCmd.PersistentFlags().StringVar(&flagRepo, "repo", ".", "Repository path")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "JSON output for scripting")
	rootCmd.PersistentFlags().BoolVar(&flagQuiet, "quiet", false, "Suppress non-essential output")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "Debug output")

	// Set version for --version flag
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate("thrum v{{.Version}} (build: " + Build + ", " + goruntime.Version() + ")\n" +
		"\x1b]8;;https://github.com/leonletto/thrum\x07https://github.com/leonletto/thrum\x1b]8;;\x07\n" +
		"\x1b]8;;https://leonletto.github.io/thrum\x07https://leonletto.github.io/thrum\x1b]8;;\x07\n")

	// Resolve flagRepo to the nearest parent containing .thrum/ (git-style traversal).
	// Skip for "init" which creates .thrum/ and doesn't need it to exist.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Don't traverse for init — it creates .thrum/
		if cmd.Name() == "init" {
			return nil
		}

		// Only traverse if the user didn't explicitly set --repo
		if !cmd.Flags().Changed("repo") {
			if root, err := paths.FindThrumRoot(flagRepo); err == nil {
				flagRepo = root
			}
			// If not found, keep "." — downstream will report the real error
		}
		return nil
	}

	// Add commands
	// Top-level common operations
	rootCmd.AddCommand(initCmd())
	rootCmd.AddCommand(sendCmd())
	rootCmd.AddCommand(replyCmd())
	rootCmd.AddCommand(inboxCmd())
	rootCmd.AddCommand(statusCmd())
	rootCmd.AddCommand(whoamiCmd())
	rootCmd.AddCommand(versionCmd())
	rootCmd.AddCommand(waitCmd())

	// Composite commands
	rootCmd.AddCommand(primeCmd())
	rootCmd.AddCommand(quickstartCmd())
	rootCmd.AddCommand(overviewCmd())
	rootCmd.AddCommand(teamCmd())

	// Coordination commands
	rootCmd.AddCommand(whoHasCmd())
	rootCmd.AddCommand(pingCmd())

	// Configuration
	rootCmd.AddCommand(configGroupCmd())

	// Subcommand groups
	rootCmd.AddCommand(daemonCmd())
	rootCmd.AddCommand(agentCmd())
	rootCmd.AddCommand(sessionCmd())
	rootCmd.AddCommand(messageCmd())
	rootCmd.AddCommand(subscribeCmd())
	rootCmd.AddCommand(unsubscribeCmd())
	rootCmd.AddCommand(subscriptionsCmd())
	rootCmd.AddCommand(contextCmd())
	rootCmd.AddCommand(groupCmd())
	rootCmd.AddCommand(runtimeGroupCmd())
	rootCmd.AddCommand(syncCmd())
	rootCmd.AddCommand(peerCmd())
	rootCmd.AddCommand(migrateCmd())
	rootCmd.AddCommand(setupCmd())
	rootCmd.AddCommand(mcpCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// Placeholder commands - will be implemented in subsequent tasks

func initCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize Thrum in the current repository",
		Long: `Initialize Thrum in the current repository.

Creates the .thrum/ directory structure, sets up the a-sync branch for
message synchronization, and updates .gitignore.

Use --stealth to avoid any footprint in tracked files: exclusions are
written to .git/info/exclude instead of .gitignore.

Detects installed AI runtimes and prompts you to select one (interactive).
When --runtime is specified, uses that runtime directly without prompting.

Examples:
  thrum init                          # Init + interactive runtime selection
  thrum init --stealth                # Init with zero tracked-file footprint
  thrum init --runtime claude         # Init + generate Claude configs
  thrum init --runtime codex --force  # Init + overwrite Codex configs
  thrum init --runtime all --dry-run  # Preview all runtime configs`,
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			stealth, _ := cmd.Flags().GetBool("stealth")
			runtimeFlag, _ := cmd.Flags().GetString("runtime")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			agentName, _ := cmd.Flags().GetString("agent-name")
			agentRole, _ := cmd.Flags().GetString("agent-role")
			agentModule, _ := cmd.Flags().GetString("agent-module")

			// Validate runtime flag if specified
			if runtimeFlag != "" && !runtime.IsValidRuntime(runtimeFlag) {
				return fmt.Errorf("unknown runtime %q; supported: claude, codex, cursor, gemini, auggie, cli-only, all", runtimeFlag)
			}

			// Step 1: Repo initialization (unless dry-run or runtime-only)
			alreadyInitialized := false
			if !dryRun {
				// Detect if we're in a git worktree
				isWorktree, mainRepoRoot, _ := cli.IsGitWorktree(flagRepo)
				if isWorktree {
					// Check if main repo has .thrum/ initialized
					mainThrumDir := filepath.Join(mainRepoRoot, ".thrum")
					if _, err := os.Stat(mainThrumDir); os.IsNotExist(err) {
						return fmt.Errorf("this is a git worktree, but the main repo (%s) is not initialized.\n  Run 'thrum init' in the main repo first, then run 'thrum init' here again", mainRepoRoot)
					}

					// Set up redirect to main repo's .thrum/
					setupOpts := cli.SetupOptions{
						RepoPath: flagRepo,
						MainRepo: mainRepoRoot,
					}
					if err := cli.Setup(setupOpts); err != nil {
						if strings.Contains(err.Error(), "redirect to self") {
							// Not actually a worktree from thrum's perspective, fall through
						} else {
							return fmt.Errorf("worktree setup: %w", err)
						}
					} else {
						if !flagQuiet {
							fmt.Println("✓ Worktree detected — set up redirect to main repo")
							fmt.Printf("  Main repo: %s\n", mainRepoRoot)
							fmt.Printf("  Redirect: .thrum/redirect → %s\n", mainThrumDir)
							fmt.Println("  Created: .thrum/identities/ (local to this worktree)")
						}
						// Skip runtime config for worktrees — they share the main repo's config
						return nil
					}
				}

				opts := cli.InitOptions{
					RepoPath: flagRepo,
					Force:    force,
					Stealth:  stealth,
				}

				if err := cli.Init(opts); err != nil {
					if strings.Contains(err.Error(), "already exists") {
						alreadyInitialized = true
						// Continue to runtime selection/config generation
					} else {
						return err
					}
				} else if !flagQuiet {
					fmt.Println("✓ Thrum initialized successfully")
					fmt.Printf("  Repository: %s\n", flagRepo)
					fmt.Println("  Created: .thrum/ directory structure")
					fmt.Println("  Created: a-sync branch for message sync")
					if stealth {
						fmt.Println("  Updated: .git/info/exclude (stealth mode)")
					} else {
						fmt.Println("  Updated: .gitignore")
					}
				}
			}

			// Step 2: Runtime selection
			selectedRuntime := runtimeFlag
			if selectedRuntime == "" {
				// Detect all runtimes
				detected := runtime.DetectAllRuntimes(flagRepo)

				if len(detected) > 0 && isInteractive() && !flagQuiet {
					// Interactive prompt
					fmt.Println()
					fmt.Println("Detected AI runtimes:")
					for i, d := range detected {
						displayName := d.Name
						if preset, err := runtime.GetPreset(d.Name); err == nil {
							displayName = preset.DisplayName
						}
						fmt.Printf("  %d. %-14s (%s)\n", i+1, displayName, d.Source)
					}
					fmt.Println()
					fmt.Printf("Which is your primary runtime? [1]: ")

					reader := bufio.NewReader(os.Stdin)
					input, _ := reader.ReadString('\n')
					input = strings.TrimSpace(input)

					choice := 1
					if input != "" {
						if n, err := strconv.Atoi(input); err == nil && n >= 1 && n <= len(detected) {
							choice = n
						} else {
							return fmt.Errorf("invalid selection %q; enter a number 1-%d", input, len(detected))
						}
					}
					selectedRuntime = detected[choice-1].Name
				} else if len(detected) > 0 {
					// Non-interactive: use first detected
					selectedRuntime = detected[0].Name
					if !flagQuiet {
						fmt.Printf("✓ Auto-detected runtime: %s\n", selectedRuntime)
					}
				} else {
					// No runtimes detected
					selectedRuntime = "cli-only"
					if !flagQuiet {
						fmt.Println("✓ No AI runtimes detected, using cli-only mode")
					}
				}
			}

			// Step 3: Save runtime selection to config.json
			if !dryRun && selectedRuntime != "" {
				thrumDir := filepath.Join(flagRepo, ".thrum")
				cfg, err := config.LoadThrumConfig(thrumDir)
				if err != nil {
					cfg = &config.ThrumConfig{}
				}
				cfg.Runtime.Primary = selectedRuntime
				if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
					return fmt.Errorf("failed to save config: %w", err)
				}
				if !flagQuiet {
					fmt.Printf("✓ Runtime saved to .thrum/config.json (primary: %s)\n", selectedRuntime)
				}
			}

			// Step 4: Runtime config generation (if not cli-only)
			if selectedRuntime != "" && selectedRuntime != "cli-only" {
				rtOpts := cli.RuntimeInitOptions{
					RepoPath:  flagRepo,
					Runtime:   selectedRuntime,
					DryRun:    dryRun,
					Force:     force || alreadyInitialized,
					AgentName: agentName,
					AgentRole: agentRole,
					AgentMod:  agentModule,
				}

				result, err := cli.RuntimeInit(rtOpts)
				if err != nil {
					return err
				}

				if flagJSON {
					output, _ := json.MarshalIndent(result, "", "  ")
					fmt.Println(string(output))
				} else if !flagQuiet {
					fmt.Print(cli.FormatRuntimeInit(result))
				}
			}

			if !flagQuiet && !dryRun {
				fmt.Println()
				fmt.Println("Config saved to .thrum/config.json — edit anytime to change")
			}

			return nil
		},
	}

	cmd.Flags().Bool("force", false, "Force reinitialization / overwrite existing files")
	cmd.Flags().Bool("stealth", false, "Use .git/info/exclude instead of .gitignore (zero footprint in tracked files)")
	cmd.Flags().Bool("dry-run", false, "Preview changes without writing files")
	cmd.Flags().String("runtime", "", "Generate runtime-specific configs (claude|codex|cursor|gemini|cli-only|all)")
	cmd.Flags().String("agent-name", "", "Agent name for templates (default: default_agent)")
	cmd.Flags().String("agent-role", "", "Agent role for templates (default: implementer)")
	cmd.Flags().String("agent-module", "", "Agent module for templates (default: main)")

	return cmd
}

// isInteractive returns true if stdin is a terminal (not piped/redirected).
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func configGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View and manage configuration",
		Long:  `View and manage Thrum configuration stored in .thrum/config.json.`,
	}

	cmd.AddCommand(configShowCmd())
	return cmd
}

func configShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show effective configuration from all sources",
		Long: `Show the effective Thrum configuration resolved from all sources.

Displays the current configuration values and their sources
(config.json, environment variable, default, auto-detected).

Works whether the daemon is running or not.

Examples:
  thrum config show
  thrum config show --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := cli.ConfigShow(flagRepo)
			if err != nil {
				return err
			}

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatConfigShow(result))
			}

			return nil
		},
	}
}

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Migrate from old layout to worktree architecture",
		Long: `Migrate an existing Thrum repository from the old layout
(JSONL files tracked on main branch) to the new worktree architecture
(JSONL files on a-sync branch via .git/thrum-sync/ worktree).

This is safe to run multiple times — it detects what needs migration
and skips steps that are already done.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cli.Migrate(flagRepo)
		},
	}
}

func setupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up Thrum in a worktree or generate CLAUDE.md",
		Long: `Set up Thrum for your development environment.

Subcommands:
  worktree   Set up redirect for a feature worktree (default)
  claude-md  Generate recommended CLAUDE.md content for thrum

When no subcommand is given, defaults to worktree setup for backwards compatibility.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Default to worktree behavior for backwards compatibility
			mainRepo, _ := cmd.Flags().GetString("main-repo")

			opts := cli.SetupOptions{
				RepoPath: flagRepo,
				MainRepo: mainRepo,
			}

			if err := cli.Setup(opts); err != nil {
				return err
			}

			if !flagQuiet {
				fmt.Println("✓ Thrum worktree setup complete")
			}

			return nil
		},
	}

	cmd.Flags().String("main-repo", ".", "Path to the main repository (where daemon runs)")

	cmd.AddCommand(setupWorktreeCmd(), setupClaudeMdCmd())

	return cmd
}

func setupWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Set up Thrum redirect in a feature worktree",
		Long: `Set up Thrum redirect in a feature worktree so it shares the
daemon, database, and sync state with the main repository.

Creates a .thrum/redirect file pointing to the main repo's .thrum/ directory
and a local .thrum/identities/ directory for per-worktree agent identities.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			mainRepo, _ := cmd.Flags().GetString("main-repo")

			opts := cli.SetupOptions{
				RepoPath: flagRepo,
				MainRepo: mainRepo,
			}

			if err := cli.Setup(opts); err != nil {
				return err
			}

			if !flagQuiet {
				fmt.Println("✓ Thrum worktree setup complete")
			}

			return nil
		},
	}

	cmd.Flags().String("main-repo", ".", "Path to the main repository (where daemon runs)")

	return cmd
}

func setupClaudeMdCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claude-md",
		Short: "Generate recommended CLAUDE.md content for thrum",
		Long: `Generates Thrum agent coordination instructions for your CLAUDE.md.
Prints to stdout by default. Use --apply to append to CLAUDE.md.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			apply, _ := cmd.Flags().GetBool("apply")
			force, _ := cmd.Flags().GetBool("force")

			opts := cli.ClaudeMdOptions{
				RepoPath: flagRepo,
				Apply:    apply,
				Force:    force,
			}

			result, err := cli.GenerateClaudeMd(opts)
			if err != nil {
				return err
			}

			if result.Skipped {
				fmt.Fprintf(os.Stderr, "Skipped: %s\n", result.SkipReason)
				return nil
			}

			if result.Applied {
				if !flagQuiet {
					fmt.Fprintf(os.Stderr, "✓ Thrum section written to %s\n", result.FilePath)
				}
			} else {
				fmt.Print(result.Content)
			}

			return nil
		},
	}

	cmd.Flags().Bool("apply", false, "Append to CLAUDE.md (create if missing)")
	cmd.Flags().Bool("force", false, "Overwrite existing Thrum section")

	return cmd
}

func sendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send MESSAGE",
		Short: "Send a message",
		Long: `Send a message to the Thrum messaging system.

Messages can include scopes (context), refs (references), and mentions.
The daemon must be running and you must have an active session.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scopes, _ := cmd.Flags().GetStringSlice("scope")
			refs, _ := cmd.Flags().GetStringSlice("ref")
			mentions, _ := cmd.Flags().GetStringSlice("mention")
			thread, _ := cmd.Flags().GetString("thread")
			structured, _ := cmd.Flags().GetString("structured")
			priority, _ := cmd.Flags().GetString("priority")
			format, _ := cmd.Flags().GetString("format")
			to, _ := cmd.Flags().GetString("to")
			broadcast, _ := cmd.Flags().GetBool("broadcast")
			everyone, _ := cmd.Flags().GetBool("everyone")

			// --everyone is an alias for --to @everyone
			if everyone {
				to = "@everyone"
			}

			opts := cli.SendOptions{
				Content:       args[0],
				Scopes:        scopes,
				Refs:          refs,
				Mentions:      mentions,
				Thread:        thread,
				Structured:    structured,
				Priority:      priority,
				Format:        format,
				To:            to,
				Broadcast:     broadcast,
				CallerAgentID: "", // set below
			}

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}
			opts.CallerAgentID = agentID

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.Send(client, opts)
			if err != nil {
				return err
			}

			if flagJSON {
				// Output as JSON
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else if !flagQuiet {
				// Human-readable output
				fmt.Printf("✓ Message sent: %s\n", result.MessageID)
				if result.ThreadID != "" {
					fmt.Printf("  Thread: %s\n", result.ThreadID)
				}
				fmt.Printf("  Created: %s\n", result.CreatedAt)
			}

			return nil
		},
	}

	cmd.Flags().StringSlice("scope", nil, "Add scope (repeatable, format: type:value)")
	cmd.Flags().StringSlice("ref", nil, "Add reference (repeatable, format: type:value)")
	cmd.Flags().StringSlice("mention", nil, "Mention a role (repeatable, format: @role)")
	cmd.Flags().String("thread", "", "Reply to thread")
	cmd.Flags().String("structured", "", "Structured payload (JSON)")
	cmd.Flags().StringP("priority", "p", "normal", "Message priority (low, normal, high)")
	cmd.Flags().String("format", "markdown", "Message format (markdown, plain, json)")
	cmd.Flags().String("to", "", "Direct recipient (format: @role)")
	cmd.Flags().Bool("everyone", false, "Send to all agents (alias for --to @everyone)")
	cmd.Flags().BoolP("broadcast", "b", false, "Send to all agents (alias for --to @everyone)")

	return cmd
}

func groupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "Manage messaging groups",
		Long: `Manage named messaging groups for multi-recipient addressing.

Groups allow sending messages to multiple agents at once without
individual mentions. Members see group messages in their inbox.`,
	}

	cmd.AddCommand(groupCreateCmd())
	cmd.AddCommand(groupDeleteCmd())
	cmd.AddCommand(groupAddCmd())
	cmd.AddCommand(groupRemoveCmd())
	cmd.AddCommand(groupListCmd())
	cmd.AddCommand(groupInfoCmd())
	cmd.AddCommand(groupMembersCmd())

	return cmd
}

func groupCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a new group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			description, _ := cmd.Flags().GetString("description")

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w", err)
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.GroupCreate(client, cli.GroupCreateOptions{
				Name:          args[0],
				Description:   description,
				CallerAgentID: agentID,
			})
			if err != nil {
				return err
			}

			if flagJSON {
				fmt.Println(cli.MarshalJSONIndent(result))
			} else if !flagQuiet {
				fmt.Printf("✓ Group created: %s (%s)\n", result.Name, result.GroupID)
			}
			return nil
		},
	}

	cmd.Flags().String("description", "", "Group description")
	return cmd
}

func groupDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w", err)
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.GroupDelete(client, cli.GroupDeleteOptions{
				Name:          args[0],
				CallerAgentID: agentID,
			})
			if err != nil {
				return err
			}

			if flagJSON {
				fmt.Println(cli.MarshalJSONIndent(result))
			} else if !flagQuiet {
				fmt.Printf("✓ Group deleted: %s\n", result.Name)
			}
			return nil
		},
	}
}

func groupAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add GROUP MEMBER",
		Short: "Add a member to a group",
		Long: `Add a member to a group. Members can be agents, roles, or nested groups.

By default, the member is treated as an agent name (strip @ prefix).
Use --role to add a role-based member.

Examples:
  thrum group add reviewers @alice          # Add agent alice
  thrum group add reviewers --role reviewer # Add all agents with role "reviewer"`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			roleFlag, _ := cmd.Flags().GetString("role")

			// Determine member from args or flags
			var member string
			if len(args) >= 2 {
				member = args[1]
			}

			// Validate: must have member arg or --role flag
			if member == "" && roleFlag == "" {
				return fmt.Errorf("provide a member argument or --role flag")
			}

			memberType, memberValue := cli.ResolveMemberType(member, roleFlag)

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w", err)
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.GroupAdd(client, cli.GroupAddOptions{
				Group:         args[0],
				MemberType:    memberType,
				MemberValue:   memberValue,
				CallerAgentID: agentID,
			})
			if err != nil {
				return err
			}

			if flagJSON {
				fmt.Println(cli.MarshalJSONIndent(result))
			} else if !flagQuiet {
				fmt.Printf("✓ Added %s to %s\n", cli.FormatMemberDisplay(result.MemberType, result.MemberValue), result.Group)
			}
			return nil
		},
	}

	cmd.Flags().String("role", "", "Add a role-based member")
	return cmd
}

func groupRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove GROUP MEMBER",
		Short: "Remove a member from a group",
		Long: `Remove a member from a group.

By default, the member is treated as an agent name (strip @ prefix).
Use --role to specify role-based members.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			roleFlag, _ := cmd.Flags().GetString("role")

			var member string
			if len(args) >= 2 {
				member = args[1]
			}

			if member == "" && roleFlag == "" {
				return fmt.Errorf("provide a member argument or --role flag")
			}

			memberType, memberValue := cli.ResolveMemberType(member, roleFlag)

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w", err)
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.GroupRemove(client, cli.GroupRemoveOptions{
				Group:         args[0],
				MemberType:    memberType,
				MemberValue:   memberValue,
				CallerAgentID: agentID,
			})
			if err != nil {
				return err
			}

			if flagJSON {
				fmt.Println(cli.MarshalJSONIndent(result))
			} else if !flagQuiet {
				fmt.Printf("✓ Removed %s from %s\n", cli.FormatMemberDisplay(result.MemberType, result.MemberValue), result.Group)
			}
			return nil
		},
	}

	cmd.Flags().String("role", "", "Remove a role-based member")
	return cmd
}

func groupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all groups",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.GroupList(client, cli.GroupListOptions{})
			if err != nil {
				return err
			}

			if flagJSON {
				fmt.Println(cli.MarshalJSONIndent(result))
			} else if !flagQuiet {
				fmt.Print(cli.FormatGroupList(result.Groups))
			}
			return nil
		},
	}
}

func groupInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info NAME",
		Short: "Show detailed group info",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.GroupInfo(client, cli.GroupInfoOptions{Name: args[0]})
			if err != nil {
				return err
			}

			if flagJSON {
				fmt.Println(cli.MarshalJSONIndent(result))
			} else if !flagQuiet {
				fmt.Print(cli.FormatGroupInfo(result))
			}
			return nil
		},
	}
}

func groupMembersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "members NAME",
		Short: "List group members",
		Long: `List members of a group. Use --expand to resolve nested groups
and roles to individual agent IDs.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			expand, _ := cmd.Flags().GetBool("expand")

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.GroupMembers(client, cli.GroupMembersOptions{
				Name:   args[0],
				Expand: expand,
			})
			if err != nil {
				return err
			}

			if flagJSON {
				fmt.Println(cli.MarshalJSONIndent(result))
			} else if !flagQuiet {
				fmt.Print(cli.FormatGroupMembers(result))
			}
			return nil
		},
	}

	cmd.Flags().Bool("expand", false, "Resolve nested groups and roles to agent IDs")
	return cmd
}

func inboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "List messages in your inbox",
		Long: `List messages in your inbox with filtering and pagination.

By default, inbox auto-filters to show messages addressed to you (via --to)
plus broadcasts and general messages. Use --all to see all messages.

The daemon must be running and you must have an active session.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, _ := cmd.Flags().GetString("scope")
			mentions, _ := cmd.Flags().GetBool("mentions")
			unread, _ := cmd.Flags().GetBool("unread")
			showAll, _ := cmd.Flags().GetBool("all")
			pageSize, _ := cmd.Flags().GetInt("page-size")
			page, _ := cmd.Flags().GetInt("page")

			// --limit is an alias for --page-size
			if cmd.Flags().Changed("limit") {
				pageSize, _ = cmd.Flags().GetInt("limit")
			}

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}
			agentRole, err := resolveLocalMentionRole()
			if err != nil {
				return fmt.Errorf("failed to resolve agent role: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}

			opts := cli.InboxOptions{
				Scope:             scope,
				Mentions:          mentions,
				Unread:            unread,
				PageSize:          pageSize,
				Page:              page,
				CallerAgentID:     agentID,
				CallerMentionRole: agentRole,
			}

			// Auto-filter: when identity is resolved and --all is not set,
			// show only messages addressed to this agent + broadcasts
			if !showAll && agentID != "" {
				opts.ForAgent = agentID
				opts.ForAgentRole = agentRole
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.Inbox(client, opts)
			if err != nil {
				return err
			}

			if flagJSON {
				// Output as JSON
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				// Human-readable formatted output with filter context
				fmtOpts := cli.InboxFormatOptions{
					ActiveScope: scope,
					ForAgent:    opts.ForAgent,
					Quiet:       flagQuiet,
					JSON:        flagJSON,
				}
				fmt.Print(cli.FormatInboxWithOptions(result, fmtOpts))
				if !flagQuiet {
					fmt.Print(cli.Hint("inbox", flagQuiet, flagJSON))
				}
			}

			// Auto mark-as-read: mark all displayed messages as read
			if len(result.Messages) > 0 {
				ids := make([]string, len(result.Messages))
				for i, m := range result.Messages {
					ids[i] = m.MessageID
				}
				// Best-effort: don't fail the command if mark-read fails
				_, _ = cli.MessageMarkRead(client, ids, agentID)
			}

			return nil
		},
	}

	cmd.Flags().String("scope", "", "Filter by scope (format: type:value)")
	cmd.Flags().Bool("mentions", false, "Only messages mentioning me")
	cmd.Flags().Bool("unread", false, "Only unread messages")
	cmd.Flags().BoolP("all", "a", false, "Show all messages (disable auto-filtering)")
	cmd.Flags().Int("page-size", 10, "Results per page")
	cmd.Flags().Int("limit", 0, "Alias for --page-size")
	cmd.Flags().Int("page", 1, "Page number")

	return cmd
}

func versionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show thrum version",
		Long:  `Display version information including version number, build hash, repository URL, and documentation URL.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagJSON {
				// JSON output
				output := map[string]string{
					"version":     Version,
					"build":       Build,
					"go_version":  goruntime.Version(),
					"repo_url":    "https://github.com/leonletto/thrum",
					"website_url": "https://leonletto.github.io/thrum",
				}
				data, err := json.MarshalIndent(output, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			} else {
				// Human-readable output with OSC 8 hyperlinks
				// Format: ESC ] 8 ; ; URL ESC \ TEXT ESC ] 8 ; ; ESC \
				fmt.Printf("thrum v%s (build: %s, %s)\n", Version, Build, goruntime.Version())
				fmt.Printf("\x1b]8;;https://github.com/leonletto/thrum\x07https://github.com/leonletto/thrum\x1b]8;;\x07\n")
				fmt.Printf("\x1b]8;;https://leonletto.github.io/thrum\x07https://leonletto.github.io/thrum\x1b]8;;\x07\n")
			}
			return nil
		},
	}
	return cmd
}

func statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current agent status and session info",
		Long: `Show current agent identity, session, inbox counts, and sync state.

The daemon must be running to check status.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			agentID, _ := resolveLocalAgentID()
			result, err := cli.Status(client, agentID)
			if err != nil {
				return err
			}

			// Read WebSocket port from port file
			result.WebSocketPort = cli.ReadWebSocketPort(flagRepo)

			if flagJSON {
				// Output as JSON
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				// Human-readable formatted output
				fmt.Print(cli.FormatStatus(result))
			}

			return nil
		},
	}

	return cmd
}

func whoamiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show current agent identity",
		Long: `Show current agent identity information.

This is a lightweight alternative to 'thrum status' - it shows just the
identity without needing a daemon connection. Reads directly from
.thrum/identities/*.json files.

Examples:
  thrum whoami
  thrum whoami --json
  THRUM_NAME=alice thrum whoami`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load identity from .thrum/identities/ directory
			identityFile, identityPath, err := config.LoadIdentityWithPath(flagRepo)
			if err != nil {
				// Check if .thrum/ directory exists
				thrumDir := filepath.Join(flagRepo, ".thrum")
				if _, statErr := os.Stat(thrumDir); os.IsNotExist(statErr) {
					return fmt.Errorf("thrum not initialized in this repository\n  Run 'thrum init' first")
				}
				// Check if identities directory exists
				identitiesDir := filepath.Join(thrumDir, "identities")
				if _, statErr := os.Stat(identitiesDir); os.IsNotExist(statErr) {
					return fmt.Errorf("no agent identities registered\n  Run 'thrum quickstart --role <role> --module <module>' to register")
				}
				return err
			}

			if flagJSON {
				// JSON output
				output, err := json.MarshalIndent(map[string]any{
					"agent_id":      identityFile.Agent.Name,
					"role":          identityFile.Agent.Role,
					"module":        identityFile.Agent.Module,
					"display":       identityFile.Agent.Display,
					"worktree":      identityFile.Worktree,
					"identity_file": identityPath,
					"repo_id":       identityFile.RepoID,
					"updated_at":    identityFile.UpdatedAt.Format(time.RFC3339),
				}, "", "  ")
				if err != nil {
					return fmt.Errorf("marshal JSON output: %w", err)
				}
				fmt.Println(string(output))
			} else {
				// Human-readable output
				fmt.Printf("@%s (%s @ %s)\n", identityFile.Agent.Name, identityFile.Agent.Role, identityFile.Agent.Module)
				if identityFile.Agent.Display != "" {
					fmt.Printf("Display:  %s\n", identityFile.Agent.Display)
				}
				fmt.Printf("Identity: %s\n", identityPath)
				if identityFile.Worktree != "" {
					fmt.Printf("Worktree: %s\n", identityFile.Worktree)
				}
			}

			return nil
		},
	}

	return cmd
}

func waitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for notifications (for hooks)",
		Long: `Block until a matching message arrives or timeout occurs.

Useful for automation and hooks that need to wait for specific messages.

Use --all to subscribe to all messages including broadcasts.
Use --after to only accept messages created after a relative time offset:
  -30s  = messages from the last 30 seconds
  -5m   = messages from the last 5 minutes
  +60s  = messages arriving at least 60 seconds from now

When --all is used without --after, defaults to "now" (only new messages).

Exit codes:
  0 = message received
  1 = timeout
  2 = error`,
		RunE: func(cmd *cobra.Command, args []string) error {
			timeoutStr, _ := cmd.Flags().GetString("timeout")
			timeout, err := time.ParseDuration(timeoutStr)
			if err != nil {
				return fmt.Errorf("invalid timeout: %w", err)
			}

			scope, _ := cmd.Flags().GetString("scope")
			mention, _ := cmd.Flags().GetString("mention")
			allMsgs, _ := cmd.Flags().GetBool("all")
			afterStr, _ := cmd.Flags().GetString("after")

			// Parse --after relative time
			var afterTime time.Time
			if afterStr != "" {
				// Parse as relative duration from now
				// "-30s" = 30s ago, "30s" or "+30s" = 30s from now
				durationStr := afterStr
				negate := false
				if strings.HasPrefix(durationStr, "-") {
					negate = true
					durationStr = durationStr[1:]
				} else if strings.HasPrefix(durationStr, "+") {
					durationStr = durationStr[1:]
				}
				d, parseErr := time.ParseDuration(durationStr)
				if parseErr != nil {
					return fmt.Errorf("invalid --after duration %q: %w (examples: -30s, -5m, +60s)", afterStr, parseErr)
				}
				if negate {
					afterTime = time.Now().Add(-d)
				} else {
					afterTime = time.Now().Add(d)
				}
			} else if allMsgs {
				// Default: when --all without --after, default to now
				// (only show messages arriving after this point)
				afterTime = time.Now()
			}

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}

			opts := cli.WaitOptions{
				Timeout:       timeout,
				Scope:         scope,
				Mention:       mention,
				All:           allMsgs,
				After:         afterTime,
				CallerAgentID: agentID,
			}

			if flagVerbose && !afterTime.IsZero() {
				fmt.Fprintf(os.Stderr, "Listening for messages after %s\n", afterTime.Format(time.RFC3339))
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			message, err := cli.Wait(client, opts)
			if err != nil {
				if err.Error() == "timeout waiting for message" {
					if !flagQuiet {
						fmt.Fprintln(os.Stderr, "Timeout: no matching messages received")
					}
					os.Exit(1)
				}
				return err
			}

			if flagJSON {
				// Output as JSON
				output, _ := json.MarshalIndent(message, "", "  ")
				fmt.Println(string(output))
			} else if !flagQuiet {
				// Brief message summary
				agentName := extractAgentName(message.AgentID)
				fmt.Printf("✓ Message received: %s from %s\n", message.MessageID, agentName)
				fmt.Printf("  %s\n", message.Body.Content)
			}

			return nil
		},
	}

	cmd.Flags().String("timeout", "30s", "Max wait time (e.g., 30s, 5m)")
	cmd.Flags().String("scope", "", "Filter by scope (format: type:value)")
	cmd.Flags().String("mention", "", "Wait for mentions of role (format: @role)")
	cmd.Flags().Bool("all", false, "Subscribe to all messages (broadcasts + directed)")
	cmd.Flags().String("after", "", "Only return messages after this relative time (e.g., -30s, -5m, +60s)")

	return cmd
}

// extractAgentName is a helper to extract agent name from ID for display.
func extractAgentName(agentID string) string {
	return identity.ExtractDisplayName(agentID)
}

func daemonCmd() *cobra.Command {
	var flagLocal bool

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the Thrum daemon",
	}

	cmd.PersistentFlags().BoolVar(&flagLocal, "local", false,
		"Local-only mode: skip git push/fetch in sync loop")

	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the daemon in the background",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.DaemonStart(flagRepo, flagLocal); err != nil {
				return err
			}

			if !flagQuiet {
				fmt.Println("✓ Daemon started successfully")
			}

			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon gracefully",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.DaemonStop(flagRepo); err != nil {
				return err
			}

			if !flagQuiet {
				fmt.Println("✓ Daemon stopped successfully")
			}

			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := cli.DaemonStatus(flagRepo)
			if err != nil {
				return err
			}

			if flagJSON {
				// Output as JSON
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				// Human-readable formatted output
				fmt.Print(cli.FormatDaemonStatus(result))
			}

			// Exit code 1 when daemon is not running (like systemctl status)
			if !result.Running {
				os.Exit(1)
			}

			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.DaemonRestart(flagRepo, flagLocal); err != nil {
				return err
			}

			if !flagQuiet {
				fmt.Println("✓ Daemon restarted successfully")
			}

			return nil
		},
	})

	cmd.AddCommand(daemonRunCmd(&flagLocal))
	// Old tsync/peers commands removed — replaced by top-level "thrum peer" commands

	return cmd
}

func daemonRunCmd(flagLocal *bool) *cobra.Command {
	return &cobra.Command{
		Use:    "run",
		Short:  "Run the daemon in the foreground (internal use)",
		Hidden: true, // Hidden from help - used internally by daemon start
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemon(flagRepo, *flagLocal)
		},
	}
}

func peerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peer",
		Short: "Manage sync peers",
		Long:  `Pair, list, and manage Tailscale sync peers.`,
	}

	// thrum peer add — start pairing on this machine
	cmd.AddCommand(&cobra.Command{
		Use:   "add",
		Short: "Start pairing and wait for a peer to connect",
		Long: `Starts a pairing session and displays a 4-digit code.
Share this code with the person running 'thrum peer join' on the other machine.
Blocks until a peer connects or the session times out (5 minutes).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.PeerStartPairing(client)
			if err != nil {
				return err
			}

			fmt.Printf("Waiting for connection... Pairing code: %s\n", result.Code)

			waitResult, err := cli.PeerWaitPairing(client)
			if err != nil {
				return err
			}

			if waitResult.Status == "paired" {
				fmt.Printf("Paired with %q (%s). Syncing started.\n", waitResult.PeerName, waitResult.PeerAddress)
			} else {
				fmt.Println("Pairing timed out. Run 'thrum peer add' again.")
			}

			return nil
		},
	})

	// thrum peer join <address> — connect to a remote peer
	cmd.AddCommand(&cobra.Command{
		Use:   "join <address>",
		Short: "Join a remote peer by entering a pairing code",
		Long: `Connects to a remote daemon at the given Tailscale address.
Prompts for the 4-digit pairing code displayed on the other machine.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			address := args[0]

			// Prompt for pairing code
			fmt.Print("Enter pairing code: ")
			var code string
			if _, err := fmt.Scanln(&code); err != nil {
				return fmt.Errorf("failed to read code: %w", err)
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.PeerJoin(client, address, code)
			if err != nil {
				return err
			}

			if result.Status == "paired" {
				fmt.Printf("Paired with %q. Syncing started.\n", result.PeerName)
			} else {
				fmt.Printf("Pairing failed: %s\n", result.Message)
			}

			return nil
		},
	})

	// thrum peer list — show all peers
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List paired peers",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			peers, err := cli.PeerList(client)
			if err != nil {
				return err
			}

			if flagJSON {
				output, _ := json.MarshalIndent(peers, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatPeerList(peers))
			}

			return nil
		},
	})

	// thrum peer remove <name> — remove a peer
	cmd.AddCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a paired peer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			if err := cli.PeerRemove(client, args[0]); err != nil {
				return err
			}

			fmt.Printf("Removed peer %q. Sync stopped.\n", args[0])
			return nil
		},
	})

	// thrum peer status — detailed health per peer
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show detailed sync status for all peers",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			peers, err := cli.PeerStatus(client)
			if err != nil {
				return err
			}

			if flagJSON {
				output, _ := json.MarshalIndent(peers, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatPeerStatus(peers))
			}

			return nil
		},
	})

	return cmd
}

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
1. THRUM_NAME env var (highest priority - overrides --name flag)
2. --name flag
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

			// Priority: THRUM_NAME env var > --name flag
			if envName := os.Getenv("THRUM_NAME"); envName != "" {
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
				identity := &config.IdentityFile{
					Version: 1,
					Agent: config.AgentConfig{
						Kind:    "agent",
						Name:    savedName,
						Role:    flagRole,
						Module:  flagModule,
						Display: display,
					},
					Worktree:  getWorktreeName(flagRepo),
					UpdatedAt: time.Now(),
				}
				thrumDir := filepath.Join(flagRepo, ".thrum")
				if err := config.SaveIdentityFile(thrumDir, identity); err != nil {
					// Warn but don't fail - registration succeeded
					fmt.Fprintf(os.Stderr, "Warning: failed to save identity file: %v\n", err)
				}
			}

			if flagJSON {
				// Output as JSON
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				// Human-readable formatted output
				fmt.Print(cli.FormatRegisterResponse(result))
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
					output, _ := json.MarshalIndent(result, "", "  ")
					fmt.Println(string(output))
				} else {
					fmt.Print(cli.FormatContextList(result))
				}

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
				// Output as JSON (combine both if contexts available)
				if contexts != nil {
					combined := map[string]any{
						"agents":   result,
						"contexts": contexts,
					}
					output, _ := json.MarshalIndent(combined, "", "  ")
					fmt.Println(string(output))
				} else {
					output, _ := json.MarshalIndent(result, "", "  ")
					fmt.Println(string(output))
				}
			} else {
				// Human-readable formatted output with enhanced info
				fmt.Print(cli.FormatAgentListWithContext(result, contexts))
			}

			return nil
		},
	}
	listCmd.Flags().String("role", "", "Filter by role")
	listCmd.Flags().String("module", "", "Filter by module")
	listCmd.Flags().Bool("context", false, "Show work context (branch, commits, intent)")
	cmd.AddCommand(listCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "whoami",
		Short: "Show current agent identity",
		Long: `Show the current agent identity and active session.

Identity is resolved from:
1. Command-line flags (--role, --module)
2. Environment variables (THRUM_ROLE, THRUM_MODULE, THRUM_NAME)
3. Identity files in .thrum/identities/ directory`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}
			result, err := cli.AgentWhoami(client, agentID)
			if err != nil {
				return err
			}

			if flagJSON {
				// Output as JSON
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				// Human-readable formatted output
				fmt.Print(cli.FormatWhoami(result))
			}

			return nil
		},
	})

	// Context subcommand - detailed work context for agents
	contextCmd := &cobra.Command{
		Use:   "context [AGENT]",
		Short: "Show agent work context",
		Long: `Show detailed work context for agents.

Without arguments, lists all active work contexts (same as 'agent list --context').
With an agent argument (e.g., @planner), shows detailed context for that agent.

Examples:
  thrum agent context                    # List all contexts
  thrum agent context @planner           # Detail for @planner
  thrum agent context --branch feature/auth  # Filter by branch
  thrum agent context --file auth.go     # Filter by file`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filterAgent, _ := cmd.Flags().GetString("agent")
			filterBranch, _ := cmd.Flags().GetString("branch")
			filterFile, _ := cmd.Flags().GetString("file")

			// If positional arg given, treat as agent filter
			if len(args) > 0 {
				filterAgent = strings.TrimPrefix(args[0], "@")
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.AgentListContext(client, filterAgent, filterBranch, filterFile)
			if err != nil {
				return err
			}

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else if len(args) > 0 && len(result.Contexts) == 1 {
				// Single agent detail view
				fmt.Print(cli.FormatContextDetail(&result.Contexts[0]))
			} else {
				// Table view
				fmt.Print(cli.FormatContextList(result))
			}

			return nil
		},
	}
	contextCmd.Flags().String("agent", "", "Filter by agent role")
	contextCmd.Flags().String("branch", "", "Filter by branch")
	contextCmd.Flags().String("file", "", "Filter by changed file")
	cmd.AddCommand(contextCmd)

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
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatAgentDelete(result))
			}

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
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
				return nil
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
The agent must be registered first (use 'thrum agent register').`,
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

	return cmd
}

// sessionStartRunE is the shared RunE for 'session start' and 'agent start'.
func sessionStartRunE(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Get current agent ID from whoami
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
	}
	whoami, err := cli.AgentWhoami(client, agentID)
	if err != nil {
		return fmt.Errorf("failed to get agent identity: %w\n\nHint: Register first with 'thrum agent register'", err)
	}

	// Parse scope flags (optional for now, will be used in Epic 4)
	// scopes, _ := cmd.Flags().GetStringSlice("scope")
	// TODO: Parse scopes when Epic 4 is implemented

	opts := cli.SessionStartOptions{
		AgentID: whoami.AgentID,
	}

	result, err := cli.SessionStart(client, opts)
	if err != nil {
		return err
	}

	if flagJSON {
		// Output as JSON
		output, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(output))
	} else {
		// Human-readable formatted output
		fmt.Print(cli.FormatSessionStart(result))
	}

	return nil
}

// sessionEndRunE is the shared RunE for 'session end' and 'agent end'.
func sessionEndRunE(cmd *cobra.Command, args []string) error {
	reason, _ := cmd.Flags().GetString("reason")
	sessionID, _ := cmd.Flags().GetString("session-id")

	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	// If no session ID provided, get current session from whoami
	if sessionID == "" {
		agentID, err := resolveLocalAgentID()
		if err != nil {
			return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
		}
		whoami, err := cli.AgentWhoami(client, agentID)
		if err != nil {
			return fmt.Errorf("failed to get agent identity: %w", err)
		}

		if whoami.SessionID == "" {
			return fmt.Errorf("no active session to end")
		}

		sessionID = whoami.SessionID
	}

	opts := cli.SessionEndOptions{
		SessionID: sessionID,
		Reason:    reason,
	}

	result, err := cli.SessionEnd(client, opts)
	if err != nil {
		return err
	}

	if flagJSON {
		// Output as JSON
		output, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(output))
	} else {
		// Human-readable formatted output
		fmt.Print(cli.FormatSessionEnd(result))
	}

	return nil
}

// sessionSetIntentRunE is the shared RunE for 'session set-intent' and 'agent set-intent'.
func sessionSetIntentRunE(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Get current session from whoami
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
	}
	whoami, err := cli.AgentWhoami(client, agentID)
	if err != nil {
		return fmt.Errorf("failed to get agent identity: %w", err)
	}
	if whoami.SessionID == "" {
		return fmt.Errorf("no active session - start one with 'thrum session start'")
	}

	result, err := cli.SessionSetIntent(client, whoami.SessionID, args[0])
	if err != nil {
		return err
	}

	if flagJSON {
		output, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(output))
	} else if !flagQuiet {
		fmt.Print(cli.FormatSetIntent(result))
	}

	return nil
}

// sessionSetTaskRunE is the shared RunE for 'session set-task' and 'agent set-task'.
func sessionSetTaskRunE(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Get current session from whoami
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
	}
	whoami, err := cli.AgentWhoami(client, agentID)
	if err != nil {
		return fmt.Errorf("failed to get agent identity: %w", err)
	}
	if whoami.SessionID == "" {
		return fmt.Errorf("no active session - start one with 'thrum session start'")
	}

	result, err := cli.SessionSetTask(client, whoami.SessionID, args[0])
	if err != nil {
		return err
	}

	if flagJSON {
		output, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(output))
	} else if !flagQuiet {
		fmt.Print(cli.FormatSetTask(result))
	}

	return nil
}

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage sessions",
	}

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start a new session",
		Long: `Start a new work session for the current agent.

The agent must be registered first (use 'thrum agent register').
Starting a new session will automatically recover any orphaned sessions.`,
		RunE: sessionStartRunE,
	}
	// startCmd.Flags().StringSlice("scope", nil, "Session scope (repeatable, format: type:value)")
	cmd.AddCommand(startCmd)

	endCmd := &cobra.Command{
		Use:   "end",
		Short: "End current session",
		Long: `End the current active session.

Specify the session ID to end, or use 'thrum agent whoami' to find your current session.`,
		RunE: sessionEndRunE,
	}
	endCmd.Flags().String("reason", "normal", "End reason (normal|crash)")
	endCmd.Flags().String("session-id", "", "Session ID to end (defaults to current session)")
	cmd.AddCommand(endCmd)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List sessions",
		Long: `List all sessions (active and ended).

Use --active to show only active sessions.
Use --agent to filter by agent ID.

Examples:
  thrum session list
  thrum session list --active
  thrum session list --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			activeOnly, _ := cmd.Flags().GetBool("active")
			agentID, _ := cmd.Flags().GetString("agent")

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			opts := cli.SessionListOptions{
				AgentID:    agentID,
				ActiveOnly: activeOnly,
			}

			result, err := cli.SessionList(client, opts)
			if err != nil {
				return err
			}

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatSessionList(result))
			}

			return nil
		},
	}
	listCmd.Flags().Bool("active", false, "Show only active sessions")
	listCmd.Flags().String("agent", "", "Filter by agent ID")
	cmd.AddCommand(listCmd)

	// heartbeat subcommand
	heartbeatCmd := &cobra.Command{
		Use:   "heartbeat",
		Short: "Send a session heartbeat",
		Long: `Send a heartbeat for the current session.

This triggers git context extraction and updates the agent's last-seen time.
Optionally add or remove scopes and refs.

Examples:
  thrum session heartbeat
  thrum session heartbeat --add-scope module:auth
  thrum session heartbeat --remove-ref pr:42`,
		RunE: sessionHeartbeatRunE,
	}
	heartbeatCmd.Flags().StringSlice("add-scope", nil, "Add scope (repeatable, format: type:value)")
	heartbeatCmd.Flags().StringSlice("remove-scope", nil, "Remove scope (repeatable, format: type:value)")
	heartbeatCmd.Flags().StringSlice("add-ref", nil, "Add ref (repeatable, format: type:value)")
	heartbeatCmd.Flags().StringSlice("remove-ref", nil, "Remove ref (repeatable, format: type:value)")
	cmd.AddCommand(heartbeatCmd)

	// set-intent subcommand
	cmd.AddCommand(&cobra.Command{
		Use:   "set-intent TEXT",
		Short: "Set session intent (what you're working on)",
		Long: `Set the work intent for the current session.

This is a free-text description of what the agent is currently working on.
It appears in 'thrum agent list --context' and 'thrum agent context'.
Pass an empty string to clear the intent.

Examples:
  thrum session set-intent "Fixing memory leak in connection pool"
  thrum session set-intent "Refactoring login flow"
  thrum session set-intent ""   # clear intent`,
		Args: cobra.ExactArgs(1),
		RunE: sessionSetIntentRunE,
	})

	// set-task subcommand
	cmd.AddCommand(&cobra.Command{
		Use:   "set-task TASK",
		Short: "Set current task (e.g., beads issue ID)",
		Long: `Set the current task identifier for the session.

This links the session to a task tracker (e.g., beads issue).
It appears in 'thrum agent list --context' and 'thrum agent context'.
Pass an empty string to clear the task.

Examples:
  thrum session set-task beads:thrum-xyz
  thrum session set-task "JIRA-1234"
  thrum session set-task ""   # clear task`,
		Args: cobra.ExactArgs(1),
		RunE: sessionSetTaskRunE,
	})

	return cmd
}

func replyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reply MSG_ID TEXT",
		Short: "Reply to a message with same audience",
		Long: `Reply to a message, copying the parent message's audience (mentions/scopes).

The reply will include a reply_to reference to the parent message and will be sent
to the same recipients as the parent message.

Examples:
  thrum reply msg_01HXE... "Good idea, let's do that"
  thrum reply msg_01HXE... "Acknowledged" --format plain`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, _ := cmd.Flags().GetString("format")

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}

			opts := cli.ReplyOptions{
				MessageID:     args[0],
				Content:       args[1],
				Format:        format,
				CallerAgentID: agentID,
			}

			result, err := cli.Reply(client, opts)
			if err != nil {
				return err
			}

			// Auto mark-as-read: mark the replied-to message as read
			_, _ = cli.MessageMarkRead(client, []string{opts.MessageID}, agentID)

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else if !flagQuiet {
				fmt.Printf("✓ Reply sent: %s\n", result.MessageID)
			}

			return nil
		},
	}

	cmd.Flags().String("format", "markdown", "Message format (markdown, plain, json)")

	return cmd
}

func messageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "message",
		Short: "Manage individual messages",
	}

	getCmd := &cobra.Command{
		Use:   "get MSG_ID",
		Short: "Get a single message with full details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.MessageGet(client, args[0])
			if err != nil {
				return err
			}

			// Auto mark-as-read (best effort — don't fail if identity resolution fails)
			agentID, err := resolveLocalAgentID()
			if err != nil {
				if !flagQuiet {
					fmt.Fprintf(os.Stderr, "Warning: Could not mark as read (no identity): %v\n", err)
				}
			} else {
				_, _ = cli.MessageMarkRead(client, []string{args[0]}, agentID)
			}

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatMessageGet(result))
			}

			return nil
		},
	}
	cmd.AddCommand(getCmd)

	editCmd := &cobra.Command{
		Use:   "edit MSG_ID TEXT",
		Short: "Edit a message (full replacement)",
		Long: `Edit a message by replacing its content entirely.

Only the message author can edit their own messages.

Examples:
  thrum message edit msg_01HXE... "Updated text here"`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.MessageEdit(client, args[0], args[1])
			if err != nil {
				return err
			}

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else if !flagQuiet {
				fmt.Print(cli.FormatMessageEdit(result))
			}

			return nil
		},
	}
	cmd.AddCommand(editCmd)

	deleteCmd := &cobra.Command{
		Use:   "delete MSG_ID",
		Short: "Delete a message",
		Long: `Delete a message by ID.

Requires --force flag to confirm deletion.

Examples:
  thrum message delete msg_01HXE... --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			if !force {
				return fmt.Errorf("use --force to confirm deletion")
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.MessageDelete(client, args[0])
			if err != nil {
				return err
			}

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else if !flagQuiet {
				fmt.Print(cli.FormatMessageDelete(result))
			}

			return nil
		},
	}
	deleteCmd.Flags().Bool("force", false, "Confirm deletion")
	cmd.AddCommand(deleteCmd)

	readCmd := &cobra.Command{
		Use:   "read [MSG_ID...]",
		Short: "Mark messages as read",
		Long: `Mark one or more messages as read, or all unread messages with --all.

Examples:
  thrum message read msg_01HXE...
  thrum message read msg_01 msg_02 msg_03
  thrum message read --all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if !all && len(args) == 0 {
				return fmt.Errorf("requires at least 1 arg(s) or --all flag")
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			messageIDs := args
			if all {
				agentID, err := resolveLocalAgentID()
				if err != nil {
					return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
				}
				agentRole, err := resolveLocalMentionRole()
				if err != nil {
					return fmt.Errorf("failed to resolve agent role: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
				}

				// Fetch all unread message IDs (capped at 100 per page)
				inboxResult, err := cli.Inbox(client, cli.InboxOptions{
					Unread:            true,
					PageSize:          100,
					CallerAgentID:     agentID,
					CallerMentionRole: agentRole,
				})
				if err != nil {
					return fmt.Errorf("failed to list unread messages: %w", err)
				}
				if len(inboxResult.Messages) == 0 {
					if !flagQuiet {
						fmt.Println("No unread messages.")
					}
					return nil
				}
				messageIDs = make([]string, len(inboxResult.Messages))
				for i, m := range inboxResult.Messages {
					messageIDs[i] = m.MessageID
				}

				result, err := cli.MessageMarkRead(client, messageIDs, agentID)
				if err != nil {
					return err
				}

				remaining := inboxResult.Unread - result.MarkedCount
				if flagJSON {
					output, _ := json.MarshalIndent(result, "", "  ")
					fmt.Println(string(output))
				} else if !flagQuiet {
					fmt.Print(cli.FormatMarkRead(result))
					if remaining > 0 {
						fmt.Printf("  %d unread messages remaining (run again to mark more)\n", remaining)
					}
				}
				return nil
			}

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}
			result, err := cli.MessageMarkRead(client, messageIDs, agentID)
			if err != nil {
				return err
			}

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else if !flagQuiet {
				fmt.Print(cli.FormatMarkRead(result))
			}

			return nil
		},
	}
	readCmd.Flags().Bool("all", false, "Mark all unread messages as read")
	cmd.AddCommand(readCmd)

	return cmd
}

func subscribeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subscribe",
		Short: "Subscribe to notifications",
		Long: `Subscribe to push notifications for messages.

Subscription types (mutually exclusive):
  --scope type:value    Subscribe to messages with specific scope
  --mention @role       Subscribe to messages mentioning a role
  --all                 Subscribe to all messages (firehose)

Examples:
  thrum subscribe --scope module:auth
  thrum subscribe --mention @reviewer
  thrum subscribe --all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, _ := cmd.Flags().GetString("scope")
			mention, _ := cmd.Flags().GetString("mention")
			all, _ := cmd.Flags().GetBool("all")

			// Validate mutually exclusive options
			optionsSet := 0
			if scope != "" {
				optionsSet++
			}
			if mention != "" {
				optionsSet++
			}
			if all {
				optionsSet++
			}

			if optionsSet == 0 {
				return fmt.Errorf("must specify one of: --scope, --mention, or --all")
			}
			if optionsSet > 1 {
				return fmt.Errorf("--scope, --mention, and --all are mutually exclusive")
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			opts := cli.SubscribeOptions{}

			// Parse scope
			if scope != "" {
				parts := strings.SplitN(scope, ":", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid scope format, expected type:value")
				}
				opts.Scope = &types.Scope{
					Type:  parts[0],
					Value: parts[1],
				}
			}

			// Parse mention (remove @ prefix if present)
			if mention != "" {
				mention = strings.TrimPrefix(mention, "@")
				opts.MentionRole = &mention
			}

			// Set all flag
			opts.All = all

			result, err := cli.Subscribe(client, opts)
			if err != nil {
				return err
			}

			if flagJSON {
				// Output as JSON
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				// Human-readable formatted output
				fmt.Print(cli.FormatSubscribe(result))
			}

			return nil
		},
	}

	cmd.Flags().String("scope", "", "Subscribe to scope (format: type:value)")
	cmd.Flags().String("mention", "", "Subscribe to mentions of role")
	cmd.Flags().Bool("all", false, "Subscribe to all messages")

	return cmd
}

func unsubscribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unsubscribe SUBSCRIPTION_ID",
		Short: "Unsubscribe from notifications",
		Long: `Remove a subscription by ID.

Use 'thrum subscriptions' to list your active subscriptions.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			subscriptionID := 0
			if _, err := fmt.Sscanf(args[0], "%d", &subscriptionID); err != nil {
				return fmt.Errorf("invalid subscription ID: %s", args[0])
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.Unsubscribe(client, subscriptionID)
			if err != nil {
				return err
			}

			if flagJSON {
				// Output as JSON
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				// Human-readable formatted output
				fmt.Print(cli.FormatUnsubscribe(subscriptionID, result))
			}

			return nil
		},
	}
}

func subscriptionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subscriptions",
		Short: "List active subscriptions",
		Long:  `List all active subscriptions for the current session.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.ListSubscriptions(client, agentID)
			if err != nil {
				return err
			}

			if flagJSON {
				// Output as JSON
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				// Human-readable formatted output
				fmt.Print(cli.FormatSubscriptionsList(result))
			}

			return nil
		},
	}
}

func contextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage agent context",
	}

	cmd.AddCommand(contextSaveCmd())
	cmd.AddCommand(contextShowCmd())
	cmd.AddCommand(contextClearCmd())
	cmd.AddCommand(contextSyncCmd())
	cmd.AddCommand(contextUpdateCmd())
	cmd.AddCommand(contextPreambleCmd())
	cmd.AddCommand(contextPrimeCmd())

	return cmd
}

func contextUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update agent context (delegates to /update-context skill)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Determine search paths for the skill file
			var searchPaths []string

			// Project-level: relative to repo root (.thrum/ directory)
			cwd, err := os.Getwd()
			if err == nil {
				if repoRoot, err := paths.FindThrumRoot(cwd); err == nil {
					searchPaths = append(searchPaths, filepath.Join(repoRoot, ".claude", "commands", "update-context.md"))
				}
			}

			// Global: ~/.claude/commands/
			if homeDir, err := os.UserHomeDir(); err == nil {
				searchPaths = append(searchPaths, filepath.Join(homeDir, ".claude", "commands", "update-context.md"))
			}

			for _, p := range searchPaths {
				if _, err := os.Stat(p); err == nil {
					fmt.Printf("Context update skill found at %s\n", p)
					fmt.Println("Run /update-context in Claude Code to update your agent context.")
					return nil
				}
			}

			// Check if the thrum plugin is installed (provides /thrum:update-context)
			if homeDir, err := os.UserHomeDir(); err == nil {
				pluginPath := filepath.Join(homeDir, ".claude", "plugins", "cache", "thrum-marketplace", "thrum")
				if matches, _ := filepath.Glob(pluginPath + "/*/commands/update-context.md"); len(matches) > 0 {
					fmt.Println("The thrum plugin provides /thrum:update-context.")
					fmt.Println("Run /thrum:update-context in Claude Code to update your agent context.")
					return nil
				}
			}

			// Not found — print installation instructions
			fmt.Println("The /update-context skill is not installed.")
			fmt.Println()
			fmt.Println("If the thrum Claude Code plugin is installed, use /thrum:update-context instead.")
			fmt.Println()
			fmt.Println("Otherwise, install the standalone skill from the thrum toolkit:")
			fmt.Println()
			fmt.Println("  # Project-level (this repo only)")
			fmt.Println("  mkdir -p .claude/commands")
			fmt.Println("  cp toolkit/commands/update-context.md .claude/commands/")
			fmt.Println()
			fmt.Println("  # Global (all projects)")
			fmt.Println("  mkdir -p ~/.claude/commands")
			fmt.Println("  cp toolkit/commands/update-context.md ~/.claude/commands/")
			return nil
		},
	}
	return cmd
}

func contextSaveCmd() *cobra.Command {
	var flagFile string
	var flagAgent string

	cmd := &cobra.Command{
		Use:   "save",
		Short: "Save agent context from file or stdin",
		Long: `Save context for the current agent (or --agent NAME).

Examples:
  thrum context save --file dev-docs/Continuation_Prompt.md
  echo "context" | thrum context save
  thrum context save --agent other_agent --file context.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID, err := resolveLocalAgentID()
			if err != nil && flagAgent == "" {
				return fmt.Errorf("failed to resolve agent identity: %w", err)
			}
			if flagAgent != "" {
				agentID = flagAgent
			}

			var content []byte
			if flagFile != "" {
				content, err = os.ReadFile(flagFile) //nolint:gosec // G304 - user-specified file path from CLI flag
				if err != nil {
					return fmt.Errorf("read context file: %w", err)
				}
			} else {
				content, err = io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			var resp rpc.ContextSaveResponse
			if err := client.Call("context.save", rpc.ContextSaveRequest{
				AgentName: agentID,
				Content:   content,
			}, &resp); err != nil {
				return err
			}

			fmt.Println(resp.Message)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagFile, "file", "", "Read context from file (default: stdin)")
	cmd.Flags().StringVar(&flagAgent, "agent", "", "Override agent name")

	return cmd
}

func contextShowCmd() *cobra.Command {
	var flagAgent string
	var flagRaw bool
	var flagNoPreamble bool

	cmd := &cobra.Command{
		Use:     "show",
		Aliases: []string{"load"},
		Short:   "Show agent context",
		Long: `Show saved context for the current agent (or --agent NAME).
Also available as 'thrum context load'.

Examples:
  thrum context show
  thrum context load
  thrum context show --agent coordinator
  thrum context show --raw
  thrum context show --no-preamble`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID, err := resolveLocalAgentID()
			if err != nil && flagAgent == "" {
				return fmt.Errorf("failed to resolve agent identity: %w", err)
			}
			if flagAgent != "" {
				agentID = flagAgent
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			includePreamble := !flagNoPreamble
			var resp rpc.ContextShowResponse
			if err := client.Call("context.show", rpc.ContextShowRequest{
				AgentName:       agentID,
				IncludePreamble: &includePreamble,
			}, &resp); err != nil {
				return err
			}

			if !resp.HasContext && !resp.HasPreamble {
				fmt.Printf("No context saved for %s\n", resp.AgentName)
				return nil
			}

			if flagRaw {
				// Raw mode: no header, file boundary markers
				if resp.HasPreamble {
					fmt.Printf("<!-- preamble: .thrum/context/%s_preamble.md -->\n", resp.AgentName)
					fmt.Print(string(resp.Preamble))
					if len(resp.Preamble) > 0 && resp.Preamble[len(resp.Preamble)-1] != '\n' {
						fmt.Println()
					}
					fmt.Println("<!-- end preamble -->")
					if resp.HasContext {
						fmt.Println()
					}
				}
				if resp.HasContext {
					fmt.Print(string(resp.Content))
				}
			} else {
				// Normal mode: header + seamless content
				if resp.HasContext {
					fmt.Printf("# Context for %s (%d bytes, updated %s)\n\n", resp.AgentName, resp.Size, resp.UpdatedAt)
				} else {
					fmt.Printf("# Context for %s\n\n", resp.AgentName)
				}
				if resp.HasPreamble {
					fmt.Print(string(resp.Preamble))
					if len(resp.Preamble) > 0 && resp.Preamble[len(resp.Preamble)-1] != '\n' {
						fmt.Println()
					}
					if resp.HasContext {
						fmt.Println()
					}
				}
				if resp.HasContext {
					fmt.Print(string(resp.Content))
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagAgent, "agent", "", "Override agent name")
	cmd.Flags().BoolVar(&flagRaw, "raw", false, "Raw output with file boundary markers, no header")
	cmd.Flags().BoolVar(&flagNoPreamble, "no-preamble", false, "Exclude preamble from output")

	return cmd
}

func contextClearCmd() *cobra.Command {
	var flagAgent string

	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear agent context",
		Long: `Clear saved context for the current agent (or --agent NAME).

Examples:
  thrum context clear
  thrum context clear --agent coordinator`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID, err := resolveLocalAgentID()
			if err != nil && flagAgent == "" {
				return fmt.Errorf("failed to resolve agent identity: %w", err)
			}
			if flagAgent != "" {
				agentID = flagAgent
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			var resp rpc.ContextClearResponse
			if err := client.Call("context.clear", rpc.ContextClearRequest{
				AgentName: agentID,
			}, &resp); err != nil {
				return err
			}

			fmt.Println(resp.Message)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagAgent, "agent", "", "Override agent name")

	return cmd
}

func contextPreambleCmd() *cobra.Command {
	var flagAgent string
	var flagInit bool
	var flagFile string

	cmd := &cobra.Command{
		Use:   "preamble",
		Short: "Manage agent preamble",
		Long: `Show or manage the preamble for the current agent (or --agent NAME).

The preamble is a stable, user-editable header prepended when showing context.
It persists across context save operations.

Examples:
  thrum context preamble              Show current preamble
  thrum context preamble --init       Create/reset to default preamble
  thrum context preamble --file PATH  Set preamble from file
  thrum context preamble --agent NAME Override agent name`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID, err := resolveLocalAgentID()
			if err != nil && flagAgent == "" {
				return fmt.Errorf("failed to resolve agent identity: %w", err)
			}
			if flagAgent != "" {
				agentID = flagAgent
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			if flagInit {
				// Reset to default preamble
				var resp rpc.PreambleSaveResponse
				if err := client.Call("context.preamble.save", rpc.PreambleSaveRequest{
					AgentName: agentID,
					Content:   agentcontext.DefaultPreamble(),
				}, &resp); err != nil {
					return err
				}
				fmt.Println(resp.Message)
				return nil
			}

			if flagFile != "" {
				// Set preamble from file
				data, err := os.ReadFile(flagFile) //nolint:gosec // G304 - user-specified file path
				if err != nil {
					return fmt.Errorf("read preamble file: %w", err)
				}
				var resp rpc.PreambleSaveResponse
				if err := client.Call("context.preamble.save", rpc.PreambleSaveRequest{
					AgentName: agentID,
					Content:   data,
				}, &resp); err != nil {
					return err
				}
				fmt.Println(resp.Message)
				return nil
			}

			// Show current preamble
			var resp rpc.PreambleShowResponse
			if err := client.Call("context.preamble.show", rpc.PreambleShowRequest{
				AgentName: agentID,
			}, &resp); err != nil {
				return err
			}

			if !resp.HasPreamble {
				fmt.Printf("No preamble for %s (use --init to create default)\n", resp.AgentName)
				return nil
			}

			fmt.Print(string(resp.Content))
			return nil
		},
	}

	cmd.Flags().StringVar(&flagAgent, "agent", "", "Override agent name")
	cmd.Flags().BoolVar(&flagInit, "init", false, "Create or reset to default preamble")
	cmd.Flags().StringVar(&flagFile, "file", "", "Set preamble from file")

	return cmd
}

func primeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prime",
		Short: "Gather session context for agent initialization",
		Long: `Collect all context needed for agent session initialization or recovery.

Gathers identity, session info, team, inbox, git context, and daemon
health into a single AI-optimized output. Used by plugin hooks on
SessionStart and PreCompact to re-prime agent context.

Gracefully degrades if daemon is not running.

Examples:
  thrum prime          # Human-readable summary
  thrum prime --json   # Structured JSON output`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				// Graceful degradation: output helpful message instead of error
				fmt.Println("Thrum not initialized (run: thrum init && thrum daemon start)")
				fmt.Println()
				fmt.Println("Commands:")
				fmt.Println("  thrum init                     Initialize thrum in this repo")
				fmt.Println("  thrum daemon start             Start the daemon")
				fmt.Println("  thrum quickstart               Register + start session")
				return nil
			}
			defer func() { _ = client.Close() }()

			agentID, _ := resolveLocalAgentID()
			result := cli.ContextPrime(client, agentID)

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatPrimeContext(result))
			}

			return nil
		},
	}
}

func contextPrimeCmd() *cobra.Command {
	// Reuse primeCmd — `thrum context prime` is an alias for `thrum prime`
	cmd := primeCmd()
	cmd.Long = `Collect all context needed for agent session initialization or recovery.

Gathers identity, session info, agent list, unread messages, git context,
and daemon health into a single output. Gracefully handles missing sections.

This is an alias for 'thrum prime'.

Examples:
  thrum context prime          # Human-readable summary
  thrum context prime --json   # Structured JSON output`
	return cmd
}

func runtimeGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Manage runtime presets",
		Long: `Manage AI coding runtime presets.

Thrum supports multiple AI coding runtimes (Claude, Codex, Cursor,
Gemini, Auggie, Amp). Each runtime has a preset with configuration
defaults. Use these commands to list, inspect, and configure runtimes.`,
	}

	// thrum runtime list
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all runtime presets",
		RunE: func(cmd *cobra.Command, args []string) error {
			result := cli.RuntimeList()

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatRuntimeList(result))
			}
			return nil
		},
	}

	// thrum runtime show <name>
	showCmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show details for a runtime preset",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			preset, err := cli.RuntimeShow(args[0])
			if err != nil {
				return err
			}

			if flagJSON {
				output, _ := json.MarshalIndent(preset, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatRuntimeShow(preset))
			}
			return nil
		},
	}

	// thrum runtime set-default <name>
	setDefaultCmd := &cobra.Command{
		Use:   "set-default <name>",
		Short: "Set the default runtime preset",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.RuntimeSetDefault(args[0]); err != nil {
				return err
			}
			fmt.Printf("✓ Default runtime set to: %s\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(listCmd)
	cmd.AddCommand(showCmd)
	cmd.AddCommand(setDefaultCmd)

	return cmd
}

func contextSyncCmd() *cobra.Command {
	var flagAgent string

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync agent context to the a-sync branch",
		Long: `Copy context file to the a-sync branch for sharing across worktrees.

This copies .thrum/context/{agent}.md to the sync worktree, commits, and pushes.
No-op when no remote is configured (local-only mode).

Examples:
  thrum context sync
  thrum context sync --agent coordinator`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID, err := resolveLocalAgentID()
			if err != nil && flagAgent == "" {
				return fmt.Errorf("failed to resolve agent identity: %w", err)
			}
			if flagAgent != "" {
				agentID = flagAgent
			}

			// Resolve paths
			repoPath := flagRepo
			if repoPath == "" {
				repoPath, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("get working directory: %w", err)
				}
			}

			syncDir, err := paths.SyncWorktreePath(repoPath)
			if err != nil {
				return fmt.Errorf("resolve sync worktree: %w", err)
			}

			// Check if sync worktree exists
			if _, err := os.Stat(syncDir); os.IsNotExist(err) {
				fmt.Println("No sync worktree found. Context sync requires 'thrum sync' to be configured.")
				return nil
			}

			// Read context file
			thrumDir := filepath.Join(repoPath, ".thrum")
			content, loadErr := readContextFile(thrumDir, agentID)
			if loadErr != nil {
				return loadErr
			}
			if content == nil {
				fmt.Printf("No context file for %s, nothing to sync.\n", agentID)
				return nil
			}

			// Write to sync worktree
			syncContextDir := filepath.Join(syncDir, "context")
			if err := os.MkdirAll(syncContextDir, 0750); err != nil {
				return fmt.Errorf("create sync context directory: %w", err)
			}

			destPath := filepath.Join(syncContextDir, agentID+".md")
			if err := os.WriteFile(destPath, content, 0644); err != nil { //nolint:gosec // G306 - markdown file
				return fmt.Errorf("write context to sync worktree: %w", err)
			}

			// Stage and commit in sync worktree
			stageCmd := exec.Command("git", "-C", syncDir, "add", filepath.Join("context", agentID+".md")) //nolint:gosec // G204 - internal path construction
			if out, err := stageCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("stage context file: %s: %w", string(out), err)
			}

			commitCmd := exec.Command("git", "-C", syncDir, "-c", "user.name=Thrum", "-c", "user.email=thrum@local", "commit", "--no-verify", "-m", fmt.Sprintf("context: sync %s", agentID), "--allow-empty") //nolint:gosec // G204 - internal path construction
			if out, err := commitCmd.CombinedOutput(); err != nil {
				// "nothing to commit" is OK
				if !strings.Contains(string(out), "nothing to commit") {
					return fmt.Errorf("commit context: %s: %w", string(out), err)
				}
			}

			// Push (skip in local-only mode - check for remote)
			remoteCmd := exec.Command("git", "-C", syncDir, "remote", "get-url", "origin") //nolint:gosec // G204 - internal path construction
			if _, remoteErr := remoteCmd.Output(); remoteErr != nil {
				// No remote configured is not an error — local-only sync is valid
				fmt.Printf("Context synced locally for %s (no remote configured).\n", agentID)
				return nil //nolint:nilerr // intentional: no remote means local-only mode, not a failure
			}

			pushCmd := exec.Command("git", "-C", syncDir, "push", "origin", "a-sync") //nolint:gosec // G204 - internal path construction
			if out, err := pushCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("push context: %s: %w", string(out), err)
			}

			fmt.Printf("Context synced for %s.\n", agentID)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagAgent, "agent", "", "Override agent name")

	return cmd
}

// readContextFile reads a context file from the thrum directory.
func readContextFile(thrumDir, agentName string) ([]byte, error) {
	path := filepath.Join(thrumDir, "context", agentName+".md")
	data, err := os.ReadFile(path) //nolint:gosec // G304 - path from internal context directory
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read context file: %w", err)
	}
	return data, nil
}

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
				// Output as JSON
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				// Human-readable formatted output
				fmt.Print(cli.FormatSyncStatus(result))
			}

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
				// Output as JSON
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				// Human-readable formatted output
				fmt.Print(cli.FormatSyncForce(result))
			}

			return nil
		},
	})

	return cmd
}

func quickstartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quickstart",
		Short: "Register, start session, and set intent in one step",
		Long: `Bootstrap an agent session with a single command.

Chains together: runtime detect → config generate → agent register →
session start → set intent. If the agent is already registered, it
re-registers automatically.

Examples:
  thrum quickstart --role implementer --module auth
  thrum quickstart --role reviewer --module auth --intent "Reviewing PR #42"
  thrum quickstart --name alice --role impl --module auth --runtime codex
  thrum quickstart --name bob --role tester --module api --dry-run
  thrum quickstart --role planner --module core --no-init`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			display, _ := cmd.Flags().GetString("display")
			intent, _ := cmd.Flags().GetString("intent")
			runtimeFlag, _ := cmd.Flags().GetString("runtime")
			preambleFile, _ := cmd.Flags().GetString("preamble-file")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			noInit, _ := cmd.Flags().GetBool("no-init")
			forceInit, _ := cmd.Flags().GetBool("force")

			if flagRole == "" || flagModule == "" {
				return fmt.Errorf("--role and --module are required (or set THRUM_ROLE and THRUM_MODULE env vars)")
			}

			// Validate runtime if specified
			if runtimeFlag != "" && !runtime.IsValidRuntime(runtimeFlag) {
				return fmt.Errorf("unknown runtime %q; supported: claude, codex, cursor, gemini, auggie, cli-only", runtimeFlag)
			}

			// Priority: THRUM_NAME env var > --name flag
			if envName := os.Getenv("THRUM_NAME"); envName != "" {
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
						// No --name given (automated/template call): fully adopt existing identity.
						name = existingCfg.Agent.Name
						flagRole = existingCfg.Agent.Role
						flagModule = existingCfg.Agent.Module
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
			}

			// In dry-run mode, we don't need a daemon connection
			var client *cli.Client
			if !dryRun {
				var err error
				client, err = getClient()
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
				idFile := &config.IdentityFile{
					Version: 1,
					Agent: config.AgentConfig{
						Kind:    "agent",
						Name:    savedName,
						Role:    flagRole,
						Module:  flagModule,
						Display: display,
					},
					Worktree:  getWorktreeName(flagRepo),
					UpdatedAt: time.Now(),
				}

				// Populate context_file with the agent's context file path
				idFile.ContextFile = fmt.Sprintf("context/%s.md", savedName)

				thrumDir := filepath.Join(flagRepo, ".thrum")
				if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to save identity file: %v\n", err)
				}

				// Bootstrap context files
				// Create empty context file if it doesn't already exist
				ctxPath := agentcontext.ContextPath(thrumDir, savedName)
				if _, statErr := os.Stat(ctxPath); os.IsNotExist(statErr) {
					if err := agentcontext.Save(thrumDir, savedName, []byte("")); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: failed to create context file: %v\n", err)
					}
				}

				// Create default preamble if it doesn't already exist
				if err := agentcontext.EnsurePreamble(thrumDir, savedName); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to create preamble: %v\n", err)
				}

				// If --preamble-file provided, compose default + custom preamble
				if preambleFile != "" {
					customContent, err := os.ReadFile(preambleFile) //nolint:gosec // G304 - user-provided flag path
					if err != nil {
						return fmt.Errorf("failed to read preamble file %q: %w", preambleFile, err)
					}
					composed := append(agentcontext.DefaultPreamble(), []byte("\n---\n\n")...)
					composed = append(composed, customContent...)
					if err := agentcontext.SavePreamble(thrumDir, savedName, composed); err != nil {
						return fmt.Errorf("failed to save composed preamble: %w", err)
					}
				}
			}

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatQuickstart(result))
				if !flagQuiet {
					fmt.Print(cli.Hint("quickstart", flagQuiet, flagJSON))
				}
			}

			return nil
		},
	}

	cmd.Flags().String("name", "", "Human-readable agent name (optional, defaults to role_hash)")
	cmd.Flags().String("display", "", "Display name for the agent")
	cmd.Flags().String("intent", "", "Initial work intent")
	cmd.Flags().String("runtime", "", "Runtime preset (claude, codex, cursor, gemini, auggie, cli-only)")
	cmd.Flags().Bool("dry-run", false, "Preview changes without writing files or registering")
	cmd.Flags().Bool("no-init", false, "Skip runtime config generation, just register agent")
	cmd.Flags().Bool("force", false, "Overwrite existing runtime config files")
	cmd.Flags().String("preamble-file", "", "Custom preamble file to compose with default preamble")

	return cmd
}

func overviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "overview",
		Short: "Show combined status, team, and inbox view",
		Long: `Show a comprehensive overview of your agent, team, and inbox.

Combines identity, work context, team activity, inbox counts,
and sync status into a single orientation view.

Examples:
  thrum overview
  thrum overview --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			agentID, _ := resolveLocalAgentID()
			result, err := cli.Overview(client, agentID)
			if err != nil {
				return err
			}

			// Add WebSocket port for UI URL display
			result.WebSocketPort = cli.ReadWebSocketPort(flagRepo)

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatOverview(result))
				if !flagQuiet {
					fmt.Print(cli.Hint("overview", flagQuiet, flagJSON))
				}
			}

			return nil
		},
	}
}

func teamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Show status of all active agents",
		Long: `Show a rich, multi-line status report for every active agent.

Displays session info, work context, inbox counts, branch status,
and per-file change details for all agents with active sessions.

Examples:
  thrum team
  thrum team --all
  thrum team --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			includeAll, _ := cmd.Flags().GetBool("all")
			req := cli.TeamListRequest{
				IncludeOffline: includeAll,
			}

			var result cli.TeamListResponse
			if err := client.Call("team.list", req, &result); err != nil {
				return fmt.Errorf("team.list RPC failed: %w", err)
			}

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatTeam(&result))
			}

			return nil
		},
	}

	cmd.Flags().Bool("all", false, "Include offline agents")

	return cmd
}

func whoHasCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "who-has FILE",
		Short: "Check which agents are editing a file",
		Long: `Check which agents are currently editing a file.

Shows agents with the file in their uncommitted changes or changed files,
along with branch and change count information.

Examples:
  thrum who-has auth.go
  thrum who-has internal/cli/agent.go`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := args[0]

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.AgentListContext(client, "", "", file)
			if err != nil {
				return err
			}

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatWhoHas(file, result))
				if !flagQuiet {
					fmt.Print(cli.Hint("who-has", flagQuiet, flagJSON))
				}
			}

			return nil
		},
	}
}

func pingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ping AGENT",
		Short: "Check if an agent is online",
		Long: `Check the presence status of an agent.

Shows whether the agent is active or offline, along with their current
intent, task, and branch information if active.

The agent can be specified with or without the @ prefix.

Examples:
  thrum ping @reviewer
  thrum ping planner`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimPrefix(args[0], "@")

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			// Get agent list to find the agent
			agents, err := cli.AgentList(client, cli.AgentListOptions{})
			if err != nil {
				return err
			}

			// Get contexts for active status
			contexts, err := cli.AgentListContext(client, "", "", "")
			if err != nil {
				contexts = nil // Non-fatal, show what we can
			}

			if flagJSON {
				combined := map[string]any{
					"agents":   agents,
					"contexts": contexts,
				}
				output, _ := json.MarshalIndent(combined, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatPing(name, agents, contexts))
				if !flagQuiet {
					fmt.Print(cli.Hint("ping", flagQuiet, flagJSON))
				}
			}

			return nil
		},
	}
}

// sessionHeartbeatRunE is the shared RunE for 'session heartbeat' and 'agent heartbeat'.
func sessionHeartbeatRunE(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Get current session from whoami
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
	}
	whoami, err := cli.AgentWhoami(client, agentID)
	if err != nil {
		return fmt.Errorf("failed to get agent identity: %w", err)
	}
	if whoami.SessionID == "" {
		return fmt.Errorf("no active session - start one with 'thrum session start'")
	}

	// Parse scope/ref flags
	addScopes, _ := cmd.Flags().GetStringSlice("add-scope")
	removeScopes, _ := cmd.Flags().GetStringSlice("remove-scope")
	addRefs, _ := cmd.Flags().GetStringSlice("add-ref")
	removeRefs, _ := cmd.Flags().GetStringSlice("remove-ref")

	opts := cli.HeartbeatOptions{
		SessionID: whoami.SessionID,
	}

	// Parse scopes (type:value format)
	for _, s := range addScopes {
		parts := strings.SplitN(s, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid scope format %q, expected type:value", s)
		}
		opts.AddScopes = append(opts.AddScopes, types.Scope{Type: parts[0], Value: parts[1]})
	}
	for _, s := range removeScopes {
		parts := strings.SplitN(s, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid scope format %q, expected type:value", s)
		}
		opts.RemoveScopes = append(opts.RemoveScopes, types.Scope{Type: parts[0], Value: parts[1]})
	}

	// Parse refs (type:value format)
	for _, r := range addRefs {
		parts := strings.SplitN(r, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid ref format %q, expected type:value", r)
		}
		opts.AddRefs = append(opts.AddRefs, types.Ref{Type: parts[0], Value: parts[1]})
	}
	for _, r := range removeRefs {
		parts := strings.SplitN(r, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid ref format %q, expected type:value", r)
		}
		opts.RemoveRefs = append(opts.RemoveRefs, types.Ref{Type: parts[0], Value: parts[1]})
	}

	result, err := cli.SessionHeartbeat(client, opts)
	if err != nil {
		return err
	}

	// Optionally fetch work context to show git summary
	var workCtx *cli.AgentWorkContext
	ctxResp, err := cli.AgentListContext(client, whoami.AgentID, "", "")
	if err == nil && len(ctxResp.Contexts) > 0 {
		workCtx = &ctxResp.Contexts[0]
	}

	if flagJSON {
		output, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(output))
	} else {
		fmt.Print(cli.FormatHeartbeat(result, workCtx))
		if !flagQuiet {
			fmt.Print(cli.Hint("session.heartbeat", flagQuiet, flagJSON))
		}
	}

	return nil
}

// getClient returns a configured RPC client.
// Respects THRUM_SOCKET env var if set, otherwise uses DefaultSocketPath.
func getClient() (*cli.Client, error) {
	socketPath := os.Getenv("THRUM_SOCKET")
	if socketPath == "" {
		socketPath = cli.DefaultSocketPath(flagRepo)
	}
	return cli.NewClient(socketPath)
}

// resolveLocalAgentID resolves the agent ID from the local worktree's identity file.
// This is used to pass caller identity to the daemon, which may be running in a
// different worktree (via .thrum/redirect). Returns empty string if resolution fails.
func resolveLocalAgentID() (string, error) {
	cfg, err := config.LoadWithPath(flagRepo, flagRole, flagModule)
	if err != nil {
		return "", err
	}
	// For named agents, GenerateAgentID returns the name directly.
	// For unnamed agents, it generates a deterministic hash-based ID.
	repoID := cfg.RepoID
	return identity.GenerateAgentID(repoID, cfg.Agent.Role, cfg.Agent.Module, cfg.Agent.Name), nil
}

// resolveLocalMentionRole resolves the agent's role from the local worktree's identity file.
// Used for the --mentions filter so the daemon filters by the correct role.
func resolveLocalMentionRole() (string, error) {
	cfg, err := config.LoadWithPath(flagRepo, flagRole, flagModule)
	if err != nil {
		return "", err
	}
	return cfg.Agent.Role, nil
}

// runDaemon runs the daemon server in the foreground.
func runDaemon(repoPath string, flagLocal bool) error {
	// Resolve to absolute path
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("failed to resolve repo path: %w", err)
	}

	// Resolve effective .thrum/ directory (follows redirect if in feature worktree)
	thrumDir, err := paths.ResolveThrumDir(absPath)
	if err != nil {
		return fmt.Errorf("failed to resolve .thrum directory: %w", err)
	}

	// Get sync worktree path (.git/thrum-sync/a-sync - JSONL data on a-sync branch)
	syncDir, err := paths.SyncWorktreePath(absPath)
	if err != nil {
		return fmt.Errorf("failed to resolve sync worktree path: %w", err)
	}

	varDir := filepath.Join(thrumDir, "var")

	// Validate .thrum directory exists
	if _, err := os.Stat(thrumDir); os.IsNotExist(err) {
		return fmt.Errorf("thrum not initialized - run 'thrum init' first")
	}

	// Ensure var directory exists
	if err := os.MkdirAll(varDir, 0750); err != nil {
		return fmt.Errorf("failed to create var directory: %w", err)
	}

	// Ensure identities directory exists
	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		return fmt.Errorf("failed to create identities directory: %w", err)
	}

	// Generate repo ID (use directory name for now)
	repoID := filepath.Base(absPath)

	// Create state manager
	st, err := state.NewState(thrumDir, syncDir, repoID)
	if err != nil {
		return fmt.Errorf("failed to create state: %w", err)
	}
	defer func() { _ = st.Close() }()

	// Run initial cleanup of stale work contexts
	if deleted, err := cleanup.CleanupStaleContexts(context.Background(), st.DB(), time.Now().UTC()); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cleanup failed: %v\n", err)
	} else if deleted > 0 {
		fmt.Fprintf(os.Stderr, "Cleaned up %d stale work context(s)\n", deleted)
	}

	// Ensure @everyone group exists (auto-created on first startup)
	if err := rpc.EnsureEveryoneGroup(context.Background(), st); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to ensure @everyone group: %v\n", err)
	}

	// Validate sync worktree exists
	if _, err := os.Stat(syncDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Warning: sync worktree not found at %s (sync disabled)\n", syncDir)
	}

	// Load config.json (used for local-only, sync interval, WS port)
	thrumCfg, cfgErr := config.LoadThrumConfig(thrumDir)
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to read config.json: %v\n", cfgErr)
		thrumCfg = &config.ThrumConfig{
			Daemon: config.DaemonConfig{
				SyncInterval: config.DefaultSyncInterval,
				WSPort:       config.DefaultWSPort,
			},
		}
	}

	// Resolve local-only mode: CLI flag > env var > config file > default
	localOnly := flagLocal
	localOnlyFromExplicit := flagLocal // track if set via flag or env (not config)
	if !localOnly {
		if env := os.Getenv("THRUM_LOCAL"); env == "1" || env == "true" {
			localOnly = true
			localOnlyFromExplicit = true
		}
	}
	if !localOnly && thrumCfg.Daemon.LocalOnly {
		localOnly = true
	}
	// Persist to config.json when set explicitly via flag or env var
	if localOnlyFromExplicit {
		thrumCfg.Daemon.LocalOnly = true
		if err := config.SaveThrumConfig(thrumDir, thrumCfg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save config.json: %v\n", err)
		}
	}
	if localOnly {
		fmt.Fprintf(os.Stderr, "  Mode:        local-only (remote sync disabled)\n")
	}

	// Resolve sync interval: env var > config.json > default
	syncInterval := time.Duration(thrumCfg.Daemon.SyncInterval) * time.Second
	if envInterval := os.Getenv("THRUM_SYNC_INTERVAL"); envInterval != "" {
		if n, err := strconv.Atoi(envInterval); err == nil && n > 0 {
			syncInterval = time.Duration(n) * time.Second
		}
	}

	// Create sync loop for periodic git sync
	ctx := context.Background()
	var syncLoop *thrumSync.SyncLoop
	if _, err := os.Stat(syncDir); err == nil {
		syncer := thrumSync.NewSyncer(absPath, syncDir, localOnly)
		syncLoop = thrumSync.NewSyncLoop(syncer, st.Projector(), absPath, syncDir, thrumDir, syncInterval, localOnly)
		if err := syncLoop.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to start sync loop: %v\n", err)
		} else {
			defer func() { _ = syncLoop.Stop() }()
		}
	}

	// Create Unix socket server
	socketPath := filepath.Join(varDir, "thrum.sock")
	server := daemon.NewServer(socketPath)

	// Create subscription dispatcher
	dispatcher := subscriptions.NewDispatcher(st.DB())

	// Register RPC handlers
	startTime := time.Now()
	version := Version + "+" + Build

	// Health check
	healthHandler := rpc.NewHealthHandler(startTime, version, repoID)
	server.RegisterHandler("health", healthHandler.Handle)

	// Agent management
	agentHandler := rpc.NewAgentHandler(st)
	server.RegisterHandler("agent.register", agentHandler.HandleRegister)
	server.RegisterHandler("agent.list", agentHandler.HandleList)
	server.RegisterHandler("agent.whoami", agentHandler.HandleWhoami)
	server.RegisterHandler("agent.listContext", agentHandler.HandleListContext)
	server.RegisterHandler("agent.delete", agentHandler.HandleDelete)
	server.RegisterHandler("agent.cleanup", agentHandler.HandleCleanup)

	// Team management
	teamHandler := rpc.NewTeamHandler(st)
	server.RegisterHandler("team.list", teamHandler.HandleList)

	// Context management
	contextHandler := rpc.NewContextHandler(st)
	server.RegisterHandler("context.save", contextHandler.HandleSave)
	server.RegisterHandler("context.show", contextHandler.HandleShow)
	server.RegisterHandler("context.clear", contextHandler.HandleClear)
	server.RegisterHandler("context.preamble.show", contextHandler.HandlePreambleShow)
	server.RegisterHandler("context.preamble.save", contextHandler.HandlePreambleSave)

	// Session management
	sessionHandler := rpc.NewSessionHandler(st)
	server.RegisterHandler("session.start", sessionHandler.HandleStart)
	server.RegisterHandler("session.end", sessionHandler.HandleEnd)
	server.RegisterHandler("session.list", sessionHandler.HandleList)
	server.RegisterHandler("session.heartbeat", sessionHandler.HandleHeartbeat)
	server.RegisterHandler("session.setIntent", sessionHandler.HandleSetIntent)
	server.RegisterHandler("session.setTask", sessionHandler.HandleSetTask)

	// Group management
	groupHandler := rpc.NewGroupHandler(st)
	server.RegisterHandler("group.create", groupHandler.HandleCreate)
	server.RegisterHandler("group.delete", groupHandler.HandleDelete)
	server.RegisterHandler("group.member.add", groupHandler.HandleMemberAdd)
	server.RegisterHandler("group.member.remove", groupHandler.HandleMemberRemove)
	server.RegisterHandler("group.list", groupHandler.HandleList)
	server.RegisterHandler("group.info", groupHandler.HandleInfo)
	server.RegisterHandler("group.members", groupHandler.HandleMembers)

	// Message management
	messageHandler := rpc.NewMessageHandlerWithDispatcher(st, dispatcher)
	server.RegisterHandler("message.send", messageHandler.HandleSend)
	server.RegisterHandler("message.get", messageHandler.HandleGet)
	server.RegisterHandler("message.list", messageHandler.HandleList)
	server.RegisterHandler("message.delete", messageHandler.HandleDelete)
	server.RegisterHandler("message.edit", messageHandler.HandleEdit)
	server.RegisterHandler("message.markRead", messageHandler.HandleMarkRead)

	// Subscription management
	subscriptionHandler := rpc.NewSubscriptionHandler(st)
	server.RegisterHandler("subscribe", subscriptionHandler.HandleSubscribe)
	server.RegisterHandler("unsubscribe", subscriptionHandler.HandleUnsubscribe)
	server.RegisterHandler("subscriptions.list", subscriptionHandler.HandleList)

	// Sync management
	var syncForceHandler *rpc.SyncForceHandler
	var syncStatusHandler *rpc.SyncStatusHandler
	if syncLoop != nil {
		syncForceHandler = rpc.NewSyncForceHandler(syncLoop)
		syncStatusHandler = rpc.NewSyncStatusHandler(syncLoop)
		server.RegisterHandler("sync.force", syncForceHandler.Handle)
		server.RegisterHandler("sync.status", syncStatusHandler.Handle)
	}

	// Tailscale peer sync management
	syncManager, err := daemon.NewDaemonSyncManager(st, varDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to create sync manager: %v\n", err)
	}

	var pairingMgr *daemon.PairingManager
	var tsLocalAddr string // set when Tailscale listener starts
	hostname, _ := os.Hostname()

	if syncManager != nil {
		// Hook into event writes to broadcast sync.notify to peers
		st.SetOnEventWrite(func(daemonID string, sequence int64, eventCount int) {
			go syncManager.BroadcastNotify(daemonID, sequence, eventCount)
		})

		// Create pairing manager (used by both Unix socket and Tailscale handlers)
		pairingMgr = daemon.NewPairingManager(syncManager.PeerRegistry(), st.DaemonID(), hostname)

		// Adapter: convert daemon.PeerStatusInfo → rpc.PeerStatus
		listPeersFn := func() []rpc.PeerStatus {
			infos := syncManager.ListPeers()
			peers := make([]rpc.PeerStatus, len(infos))
			for i, p := range infos {
				peers[i] = rpc.PeerStatus{
					DaemonID: p.DaemonID,
					Name:     p.Name,
					Address:  p.Address,
					LastSync: p.LastSync,
					LastSeq:  p.LastSeq,
				}
			}
			return peers
		}

		tsyncForceHandler := rpc.NewTsyncForceHandler(syncManager.SyncFromPeer, listPeersFn)
		tsyncPeersListHandler := rpc.NewTsyncPeersListHandler(listPeersFn)
		tsyncPeersAddHandler := rpc.NewTsyncPeersAddHandler(syncManager.AddPeer)

		server.RegisterHandler("tsync.force", tsyncForceHandler.Handle)
		server.RegisterHandler("tsync.peers.list", tsyncPeersListHandler.Handle)
		server.RegisterHandler("tsync.peers.add", tsyncPeersAddHandler.Handle)

		// --- Peer management RPCs (CLI: thrum peer add/join/list/remove/status) ---

		// peer.start_pairing — begin pairing, return code
		server.RegisterHandler("peer.start_pairing",
			rpc.NewPeerStartPairingHandler(pairingMgr.StartPairing).Handle)

		// peer.wait_pairing — block until pairing completes or times out
		waitFn := func(ctx context.Context) (peerName, peerAddr, peerDaemonID string, err error) {
			result, err := pairingMgr.WaitForPairing(ctx)
			if err != nil {
				return "", "", "", err
			}
			return result.PeerName, result.PeerAddress, result.PeerDaemonID, nil
		}
		server.RegisterHandler("peer.wait_pairing",
			rpc.NewPeerWaitPairingHandler(waitFn).Handle)

		// peer.join — send pairing code to remote peer
		joinFn := func(peerAddr, code string) (peerName, peerDaemonID string, err error) {
			if tsLocalAddr == "" {
				return "", "", fmt.Errorf("tailscale not configured or not started")
			}
			peer, err := syncManager.JoinPeer(peerAddr, code, st.DaemonID(), hostname, tsLocalAddr)
			if err != nil {
				return "", "", err
			}
			return peer.Name, peer.DaemonID, nil
		}
		server.RegisterHandler("peer.join",
			rpc.NewPeerJoinHandler(joinFn).Handle)

		// peer.list — compact peer list
		peerListFn := func() []rpc.PeerListEntry {
			infos := syncManager.ListPeers()
			entries := make([]rpc.PeerListEntry, len(infos))
			for i, p := range infos {
				entries[i] = rpc.PeerListEntry{
					DaemonID: p.DaemonID,
					Name:     p.Name,
					Address:  p.Address,
					LastSync: p.LastSync,
					LastSeq:  p.LastSeq,
				}
			}
			return entries
		}
		server.RegisterHandler("peer.list",
			rpc.NewPeerListHandler(peerListFn).Handle)

		// peer.remove — remove a peer by name or daemon ID
		removeFn := func(daemonID string) error {
			return syncManager.PeerRegistry().RemovePeer(daemonID)
		}
		findByNameFn := func(name string) (string, bool) {
			peer := syncManager.PeerRegistry().FindPeerByName(name)
			if peer == nil {
				return "", false
			}
			return peer.DaemonID, true
		}
		server.RegisterHandler("peer.remove",
			rpc.NewPeerRemoveHandler(removeFn, findByNameFn).Handle)

		// peer.status — detailed per-peer status
		statusFn := func() []rpc.PeerDetailedStatus {
			infos := syncManager.DetailedPeerStatus()
			statuses := make([]rpc.PeerDetailedStatus, len(infos))
			for i, p := range infos {
				statuses[i] = rpc.PeerDetailedStatus{
					DaemonID: p.DaemonID,
					Name:     p.Name,
					Address:  p.Address,
					Token:    p.HasToken,
					PairedAt: p.PairedAt,
					LastSync: p.LastSync,
					LastSeq:  p.LastSeq,
				}
			}
			return statuses
		}
		server.RegisterHandler("peer.status",
			rpc.NewPeerStatusHandler(statusFn).Handle)
	}

	// User management (for WebSocket connections)
	userHandler := rpc.NewUserHandler(st)
	server.RegisterHandler("user.register", userHandler.HandleRegister)
	server.RegisterHandler("user.identify", userHandler.HandleIdentify)

	// Resolve WS port: env var > config.json > default ("auto" = find free port)
	wsPort := os.Getenv("THRUM_WS_PORT")
	if wsPort == "" {
		wsPort = thrumCfg.Daemon.WSPort
	}
	if wsPort == "" || wsPort == "auto" {
		// Find a free port
		listener, listenErr := net.Listen("tcp", "localhost:0")
		if listenErr != nil {
			return fmt.Errorf("failed to find free port for WebSocket: %w", listenErr)
		}
		tcpAddr, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			return fmt.Errorf("failed to get TCP address from listener")
		}
		wsPort = strconv.Itoa(tcpAddr.Port)
		_ = listener.Close()
	}
	wsAddr := "localhost:" + wsPort

	// Create a handler adapter for WebSocket server
	wsRegistry := websocket.NewSimpleRegistry()

	// Register same handlers on WebSocket registry
	wsRegistry.Register("health", websocket.Handler(healthHandler.Handle))
	wsRegistry.Register("agent.register", websocket.Handler(agentHandler.HandleRegister))
	wsRegistry.Register("agent.list", websocket.Handler(agentHandler.HandleList))
	wsRegistry.Register("agent.whoami", websocket.Handler(agentHandler.HandleWhoami))
	wsRegistry.Register("agent.listContext", websocket.Handler(agentHandler.HandleListContext))
	wsRegistry.Register("session.start", websocket.Handler(sessionHandler.HandleStart))
	wsRegistry.Register("session.end", websocket.Handler(sessionHandler.HandleEnd))
	wsRegistry.Register("session.list", websocket.Handler(sessionHandler.HandleList))
	wsRegistry.Register("session.heartbeat", websocket.Handler(sessionHandler.HandleHeartbeat))
	wsRegistry.Register("session.setIntent", websocket.Handler(sessionHandler.HandleSetIntent))
	wsRegistry.Register("session.setTask", websocket.Handler(sessionHandler.HandleSetTask))
	wsRegistry.Register("group.create", websocket.Handler(groupHandler.HandleCreate))
	wsRegistry.Register("group.delete", websocket.Handler(groupHandler.HandleDelete))
	wsRegistry.Register("group.member.add", websocket.Handler(groupHandler.HandleMemberAdd))
	wsRegistry.Register("group.member.remove", websocket.Handler(groupHandler.HandleMemberRemove))
	wsRegistry.Register("group.list", websocket.Handler(groupHandler.HandleList))
	wsRegistry.Register("group.info", websocket.Handler(groupHandler.HandleInfo))
	wsRegistry.Register("group.members", websocket.Handler(groupHandler.HandleMembers))
	wsRegistry.Register("message.send", websocket.Handler(messageHandler.HandleSend))
	wsRegistry.Register("message.get", websocket.Handler(messageHandler.HandleGet))
	wsRegistry.Register("message.list", websocket.Handler(messageHandler.HandleList))
	wsRegistry.Register("message.delete", websocket.Handler(messageHandler.HandleDelete))
	wsRegistry.Register("message.edit", websocket.Handler(messageHandler.HandleEdit))
	wsRegistry.Register("message.markRead", websocket.Handler(messageHandler.HandleMarkRead))
	wsRegistry.Register("subscribe", websocket.Handler(subscriptionHandler.HandleSubscribe))
	wsRegistry.Register("unsubscribe", websocket.Handler(subscriptionHandler.HandleUnsubscribe))
	wsRegistry.Register("subscriptions.list", websocket.Handler(subscriptionHandler.HandleList))
	wsRegistry.Register("user.register", websocket.Handler(userHandler.HandleRegister))
	wsRegistry.Register("user.identify", websocket.Handler(userHandler.HandleIdentify))
	if syncLoop != nil {
		wsRegistry.Register("sync.force", websocket.Handler(syncForceHandler.Handle))
		wsRegistry.Register("sync.status", websocket.Handler(syncStatusHandler.Handle))
	}

	// Resolve UI filesystem (embedded or dev mode)
	var uiFS fs.FS
	if devPath := os.Getenv("THRUM_UI_DEV"); devPath != "" {
		// Dev mode: serve from disk for hot reload
		uiFS = os.DirFS(devPath)
		fmt.Fprintf(os.Stderr, "  UI (dev):    serving from %s\n", devPath)
	} else {
		// Production: use embedded files
		sub, err := fs.Sub(web.Files, "dist")
		if err == nil {
			// Check if the embedded FS has real content (not just .gitkeep)
			if _, err := fs.Stat(sub, "index.html"); err == nil {
				uiFS = sub
			}
		}
	}

	wsServer := websocket.NewServer(wsAddr, wsRegistry, uiFS)

	// Clean up subscriptions when a WebSocket client disconnects (thrum-pgoc fix)
	subSvc := subscriptions.NewService(st.DB())
	wsServer.SetDisconnectHook(func(sessionID string) {
		_, _ = subSvc.ClearBySession(context.Background(), sessionID)
	})

	// Tailscale tsnet listener (optional — daemon works fine without it)
	tsCfg := config.LoadTailscaleConfig(thrumDir)
	if tsCfg.Enabled {
		if err := tsCfg.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Tailscale config invalid: %v (tsnet disabled)\n", err)
		} else {
			tsListener, tsErr := daemon.NewTsnetServer(tsCfg)
			if tsErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to start tsnet: %v (tsnet disabled)\n", tsErr)
			} else {
				defer func() { _ = tsListener.Close() }()

				// Create sync-only registry for Tailscale connections
				syncRegistry := daemon.NewSyncRegistry()
				if syncManager != nil {
					syncRegistry.SetPeerRegistry(syncManager.PeerRegistry())
				}

				// Set Tailscale address so peer.join knows our reachable address
				tsLocalAddr = fmt.Sprintf("%s:%d", tsCfg.Hostname, tsCfg.Port)

				// Register sync handlers
				syncPullHandler := rpc.NewSyncPullHandler(st)
				syncPeerInfoHandler := rpc.NewPeerInfoHandler(st.DaemonID(), hostname)

				_ = syncRegistry.Register("sync.pull", syncPullHandler.Handle)
				_ = syncRegistry.Register("sync.peer_info", syncPeerInfoHandler.Handle)

				// Register sync.notify handler (triggers pull sync on notification)
				if syncManager != nil {
					syncNotifyHandler := rpc.NewSyncNotifyHandler(syncManager.SyncFromPeerByID)
					_ = syncRegistry.Register("sync.notify", syncNotifyHandler.Handle)
				}

				// Register pair.request handler (uses PairingManager created earlier)
				if pairingMgr != nil {
					pairHandler := rpc.NewPairRequestHandler(pairingMgr.HandlePairRequest)
					_ = syncRegistry.Register("pair.request", pairHandler.Handle)
				}

				// Accept loop for sync connections
				go func() {
					for {
						conn, err := tsListener.Accept()
						if err != nil {
							return // Listener closed
						}

						go func(c net.Conn) {
							defer func() { _ = c.Close() }()
							peerID := c.RemoteAddr().String()
							syncRegistry.ServeSyncRPC(ctx, c, peerID)
						}(conn)
					}
				}()

				// Start periodic sync fallback (safety net for missed notifications)
				if syncManager != nil {
					periodicSync := daemon.NewPeriodicSyncScheduler(syncManager, st)
					go periodicSync.Start(ctx)
				}

				fmt.Fprintf(os.Stderr, "  Tailscale:   %s:%d\n", tsCfg.Hostname, tsCfg.Port)

				// Wire Tailscale sync info into health handler
				if syncManager != nil {
					tsHostname := tsCfg.Hostname
					healthHandler.SetTailscaleInfoProvider(func() *rpc.TailscaleSyncInfo {
						count, peers := syncManager.TailscaleSyncStatus(tsHostname)
						tsPeers := make([]rpc.TailscalePeer, len(peers))
						for i, p := range peers {
							tsPeers[i] = rpc.TailscalePeer{
								DaemonID: p.DaemonID,
								Name:     p.Name,
								LastSync: p.LastSync,
							}
						}
						return &rpc.TailscaleSyncInfo{
							Enabled:        true,
							Hostname:       tsHostname,
							ConnectedPeers: count,
							Peers:          tsPeers,
							SyncStatus:     "idle",
						}
					})
				}
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Thrum daemon starting...\n")
	fmt.Fprintf(os.Stderr, "  Unix socket: %s\n", socketPath)
	fmt.Fprintf(os.Stderr, "  WebSocket:   ws://localhost:%s/ws\n", wsPort)
	if uiFS != nil {
		fmt.Fprintf(os.Stderr, "  UI:          http://localhost:%s\n", wsPort)
	}

	// Create lifecycle manager and run
	// Lifecycle handles starting/stopping both Unix socket and WebSocket servers
	pidFile := filepath.Join(varDir, "thrum.pid")
	wsPortFile := filepath.Join(varDir, "ws.port")
	lockFile := filepath.Join(varDir, "thrum.lock")
	lifecycle := daemon.NewLifecycle(server, pidFile, wsServer, wsPortFile)

	// Set repo info for PID file metadata
	lifecycle.SetRepoInfo(absPath, socketPath)

	// Set lock file for SIGKILL resilience
	lifecycle.SetLockFile(lockFile)

	return lifecycle.Run(ctx)
}

// getWorktreeName extracts the worktree name from the repo path.
// Returns the basename of the repo path (e.g., "daemon", "foundation", "main").
// Uses git rev-parse --show-toplevel to get the actual worktree root, falling back
// to filepath.Base if not in a git repo.
func getWorktreeName(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		// Fallback to current behavior if not in a git repo
		absPath, _ := filepath.Abs(repoPath)
		return filepath.Base(absPath)
	}
	return filepath.Base(strings.TrimSpace(string(out)))
}
