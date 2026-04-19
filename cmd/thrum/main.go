package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/leonletto/thrum/internal/backup"
	bridgepeer "github.com/leonletto/thrum/internal/bridge/peer"
	telegram "github.com/leonletto/thrum/internal/bridge/telegram"
	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	agentcontext "github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/cleanup"
	"github.com/leonletto/thrum/internal/daemon/inbox"
	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
	"github.com/leonletto/thrum/internal/daemon/monitor"
	"github.com/leonletto/thrum/internal/daemon/nudge"
	"github.com/leonletto/thrum/internal/daemon/permission"
	"github.com/leonletto/thrum/internal/daemon/reconcile"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/netdetect"
	"github.com/leonletto/thrum/internal/identity/guard"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/process"
	"github.com/leonletto/thrum/internal/restart"
	"github.com/leonletto/thrum/internal/runtime"
	"github.com/leonletto/thrum/internal/subscriptions"
	thrumSync "github.com/leonletto/thrum/internal/sync"
	"github.com/leonletto/thrum/internal/timeparse"
	ttmux "github.com/leonletto/thrum/internal/tmux"
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
	rootCmd := buildRootCmd()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// buildRootCmd assembles the full cobra command tree and returns the
// root. Extracted from main() so tests (command_categories_test.go)
// can walk the tree and enforce the guard-category taxonomy.
func buildRootCmd() *cobra.Command {
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
		if !cmd.Flags().Changed("repo") {
			flagRepo = paths.EffectiveRepoPath(flagRepo)
		}

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
	rootCmd.AddCommand(sentCmd())

	rootCmd.AddCommand(whoamiCmd())
	rootCmd.AddCommand(versionCmd())
	rootCmd.AddCommand(waitCmd())
	rootCmd.AddCommand(cronCmd())

	// Composite commands
	rootCmd.AddCommand(primeCmd())
	rootCmd.AddCommand(quickstartCmd())
	rootCmd.AddCommand(overviewCmd())
	rootCmd.AddCommand(teamCmd())

	// Coordination commands
	rootCmd.AddCommand(whoHasCmd())
	rootCmd.AddCommand(pingCmd())

	// Configuration
	rootCmd.AddCommand(singleAgentModeCmd())
	rootCmd.AddCommand(configGroupCmd())

	// Subcommand groups
	rootCmd.AddCommand(daemonCmd())
	rootCmd.AddCommand(agentCmd())
	rootCmd.AddCommand(sessionCmd())
	rootCmd.AddCommand(messageCmd())
	// subscribeCmd, unsubscribeCmd, subscriptionsCmd removed — use thrum wait for CLI notifications.
	rootCmd.AddCommand(contextCmd())
	// groupCmd() removed — groups are no longer user-facing.
	// Group RPC handlers remain registered for Telegram bridge (tg:* groups).
	rootCmd.AddCommand(runtimeGroupCmd())
	rootCmd.AddCommand(syncCmd())
	rootCmd.AddCommand(backupCmd())
	rootCmd.AddCommand(peerCmd())
	rootCmd.AddCommand(monitorCmd())

	rootCmd.AddCommand(setupCmd())
	rootCmd.AddCommand(mcpCmd())
	rootCmd.AddCommand(rolesCmd())
	rootCmd.AddCommand(purgeCmd())
	rootCmd.AddCommand(telegramCmd())
	rootCmd.AddCommand(tmuxCmd())
	rootCmd.AddCommand(restartCmd())
	rootCmd.AddCommand(worktreeCmd())

	// Apply guard-category annotations to every leaf command under
	// rootCmd. See command_categories.go for the per-path mapping +
	// categorization rationale; command_categories_test.go walks this
	// same tree in-test and fails if any leaf is missing a category.
	tagGuardCategories(rootCmd)

	return rootCmd
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
  thrum init --runtime all --dry-run  # Preview all runtime configs
  thrum init --skills                 # Install thrum skill for detected agent
  thrum init --skills --runtime cursor # Install skill for Cursor specifically`,
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			stealth, _ := cmd.Flags().GetBool("stealth")
			runtimeFlag, _ := cmd.Flags().GetString("runtime")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			skillsOnly, _ := cmd.Flags().GetBool("skills")

			// Validate runtime flag if specified
			if runtimeFlag != "" && !runtime.IsValidRuntime(runtimeFlag) {
				return fmt.Errorf("unknown runtime %q; supported: %s", runtimeFlag, strings.Join(runtime.SupportedRuntimes(), ", "))
			}

			// Identity Guard G2: refuse `thrum init` from a non-git
			// directory unless --force is set. This closes the footgun
			// where `init` silently materialized .thrum/ under $HOME
			// with nonsense supervisor slugs. We only fire the check in
			// full-init mode (skipped for --skills-only, which does not
			// create .thrum/).
			if !skillsOnly {
				initDir := flagRepo
				if initDir == "" {
					initDir = "."
				}
				resolvedDir, err := filepath.Abs(initDir)
				if err != nil {
					resolvedDir = initDir
				}
				if err := guard.G2(loadInitBootstrapMode(resolvedDir), resolvedDir, force, nil); err != nil {
					return err
				}
			}

			// Skills-only mode: install thrum skill without full init
			if skillsOnly {
				return runSkillsInstall(flagRepo, runtimeFlag, force, dryRun)
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
					if err := cli.EnsureWorktreeRedirects(flagRepo, mainRepoRoot); err != nil {
						if strings.Contains(err.Error(), "does not exist") {
							// Worktree path doesn't exist or .thrum missing — fall through to normal init
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
				cfg.Daemon.SingleAgentMode = true // Default: single-agent mode
				if cfg.Worktrees.BasePath == "" {
					cfg.Worktrees = config.WorktreesConfig{
						BasePath:     inferWorktreeBasePath(flagRepo),
						BeadsEnabled: true,
						ThrumEnabled: true,
					}
				}
				if cfg.Orchestration.MergeTarget == "" {
					cfg.Orchestration = config.OrchestrationConfig{
						MergeTarget:     "main",
						DefaultAutonomy: "end_only",
					}
				}
				if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
					return fmt.Errorf("failed to save config: %w", err)
				}
				if !flagQuiet {
					fmt.Printf("✓ Runtime saved to .thrum/config.json (primary: %s)\n", selectedRuntime)
				}
			}

			// Step 3b: Generate project_state.md if it doesn't exist
			if !dryRun {
				thrumDir := filepath.Join(flagRepo, ".thrum")
				projectStatePath := filepath.Join(thrumDir, "context", "project_state.md")
				_ = os.MkdirAll(filepath.Dir(projectStatePath), 0750)
				if _, err := os.Stat(projectStatePath); os.IsNotExist(err) {
					repoName := filepath.Base(flagRepo)
					bgCtx := context.Background()
					branch, _ := safecmd.Git(bgCtx, flagRepo, "branch", "--show-current")
					version, _ := safecmd.Git(bgCtx, flagRepo, "describe", "--tags", "--abbrev=0")
					beads := ""
					if _, err := os.Stat(filepath.Join(flagRepo, ".beads")); err == nil {
						if out, err := exec.Command("bd", "stats", "--short").Output(); err == nil {
							beads = strings.TrimSpace(string(out))
						}
					}
					opts := &agentcontext.ProjectStateOpts{
						RepoName: repoName,
						Language: agentcontext.DetectLanguage(flagRepo),
						Version:  strings.TrimSpace(string(version)),
						Branch:   strings.TrimSpace(string(branch)),
						Beads:    beads,
					}
					content := agentcontext.GenerateProjectState(opts)
					_ = os.WriteFile(projectStatePath, content, 0644) //#nosec G306 -- markdown file
					if !flagQuiet {
						fmt.Println("✓ Generated .thrum/context/project_state.md")
					}
				}
			}

			// Step 4: Runtime config generation (if not cli-only)
			if selectedRuntime != "" && selectedRuntime != "cli-only" {
				rtOpts := cli.RuntimeInitOptions{
					RepoPath: flagRepo,
					Runtime:  selectedRuntime,
					DryRun:   dryRun,
					Force:    force || alreadyInitialized,
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

			if dryRun {
				return nil
			}

			// Start daemon if not already running
			if _, err := getClient(); err != nil {
				if startErr := cli.DaemonStart(flagRepo, false, false); startErr != nil && !strings.Contains(startErr.Error(), "already running") {
					fmt.Fprintf(os.Stderr, "Warning: could not auto-start daemon: %v\n", startErr)
					fmt.Println("Start manually: thrum daemon start")
				} else if !flagQuiet {
					if wsPort := cli.ReadWebSocketPort(flagRepo); wsPort > 0 {
						fmt.Printf("✓ Daemon started — http://localhost:%d\n", wsPort)
					} else {
						fmt.Println("✓ Daemon started")
					}
				}
			} else if !flagQuiet {
				fmt.Println("✓ Daemon already running")
			}

			fmt.Println("\nDone. Run 'thrum quickstart --name <name> --role <role> --module <module>' to register an agent.")

			return nil
		},
	}

	cmd.Flags().Bool("force", false, "Force reinitialization / overwrite existing files")
	cmd.Flags().Bool("stealth", false, "Use .git/info/exclude instead of .gitignore (zero footprint in tracked files)")
	cmd.Flags().Bool("dry-run", false, "Preview changes without writing files")
	cmd.Flags().String("runtime", "", "Generate runtime-specific configs (claude|codex|cursor|gemini|opencode|cli-only|all)")
	cmd.Flags().Bool("skills", false, "Install thrum skill only (no MCP config, no startup script)")

	return cmd
}

// runSkillsInstall handles the --skills flag: detect agent, resolve path, install skill files.
func runSkillsInstall(repoPath, runtimeFlag string, force, dryRun bool) error {
	var selectedAgent string

	if runtimeFlag != "" {
		if runtimeFlag == "all" || runtimeFlag == "cli-only" {
			return fmt.Errorf("--runtime %q is not valid with --skills; specify an agent (claude, cursor, codex, gemini, auggie, amp)", runtimeFlag)
		}
		selectedAgent = runtimeFlag
	} else {
		detected := runtime.DetectAgents(repoPath)
		switch len(detected) {
		case 0:
			if !flagQuiet {
				fmt.Println("No AI coding agent detected.")
				fmt.Println("Installing to .agents/skills/thrum/ (universal path)")
			}
			selectedAgent = "amp" // amp's SkillsDir is .agents/skills
		case 1:
			selectedAgent = detected[0].Name
			if !flagQuiet {
				displayName := detected[0].Name
				if a, ok := runtime.GetAgent(detected[0].Name); ok {
					displayName = a.DisplayName
				}
				fmt.Printf("Detected: %s (%s)\n", displayName, detected[0].Source)
			}
		default:
			if isInteractive() && !flagQuiet {
				fmt.Println("Detected AI coding agents:")
				for i, d := range detected {
					displayName := d.Name
					if a, ok := runtime.GetAgent(d.Name); ok {
						displayName = a.DisplayName
					}
					fmt.Printf("  %d. %-18s (%s)\n", i+1, displayName, d.Source)
				}
				fmt.Printf("  %d. Generic           (.agents/skills/thrum/)\n", len(detected)+1)
				fmt.Printf("\nInstall thrum skill for [1]: ")

				reader := bufio.NewReader(os.Stdin)
				input, _ := reader.ReadString('\n')
				input = strings.TrimSpace(input)

				choice := 1
				if input != "" {
					n, err := strconv.Atoi(input)
					if err != nil || n < 1 || n > len(detected)+1 {
						return fmt.Errorf("invalid selection %q", input)
					}
					choice = n
				}
				if choice <= len(detected) {
					selectedAgent = detected[choice-1].Name
				} else {
					selectedAgent = "amp"
				}
			} else {
				selectedAgent = detected[0].Name
			}
		}
	}

	opts := cli.SkillsInstallOptions{
		RepoPath: repoPath,
		Agent:    selectedAgent,
		Force:    force,
		DryRun:   dryRun,
	}

	result, err := cli.InstallSkills(opts)
	if err != nil {
		return err
	}

	if flagJSON {
		output, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(output))
	} else if !flagQuiet {
		fmt.Print(cli.FormatSkillsInstall(result))
	}

	return nil
}

// isInteractive returns true if stdin is a terminal (not piped/redirected).
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) // #nosec G115 -- file descriptors are small non-negative integers; uintptr->int conversion cannot overflow
}

func singleAgentModeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "single-agent-mode [true|false]",
		Short: "Enable or disable single-agent mode",
		Long: `Toggle single-agent mode. When enabled, Thrum skips all messaging
infrastructure (listener, inbox, stop hook checks) and focuses on
context management features only.

Examples:
  thrum single-agent-mode true    # Enable (no listener, no messaging)
  thrum single-agent-mode false   # Disable (full multi-agent messaging)
  thrum single-agent-mode         # Show current mode`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			thrumDir := filepath.Join(flagRepo, ".thrum")
			if _, err := os.Stat(thrumDir); os.IsNotExist(err) {
				return fmt.Errorf("not in a thrum workspace (run thrum init first)")
			}
			cfg, err := config.LoadThrumConfig(thrumDir)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			if len(args) == 0 {
				if cfg.Daemon.SingleAgentMode {
					fmt.Println("single-agent mode: enabled")
				} else {
					fmt.Println("single-agent mode: disabled (multi-agent)")
				}
				fmt.Println("\nUsage: thrum single-agent-mode [true|false]")
				return nil
			}
			switch strings.ToLower(args[0]) {
			case "true", "on", "1":
				cfg.Daemon.SingleAgentMode = true
			case "false", "off", "0":
				cfg.Daemon.SingleAgentMode = false
			default:
				return fmt.Errorf("expected true or false, got %q", args[0])
			}
			if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}
			if cfg.Daemon.SingleAgentMode {
				fmt.Println("Single-agent mode enabled. Messaging infrastructure disabled.")
			} else {
				fmt.Println("Single-agent mode disabled. Full messaging active after daemon restart.")
			}
			return nil
		},
	}
	return cmd
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

func purgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Remove old messages, sessions, and events",
		Long: `Remove messages, sessions, and events before a cutoff date.

By default, shows a preview of what would be deleted. Use --confirm to execute.

Supports relative durations (2d, 24h), date-only (2026-03-15), and RFC 3339.

Examples:
  thrum purge --before 2d              # preview: what's older than 2 days
  thrum purge --before 2d --confirm    # execute the purge
  thrum purge --before 2026-03-15 --confirm
  thrum purge --all --confirm          # delete all messages/sessions/events`,
		RunE: func(cmd *cobra.Command, args []string) error {
			beforeFlag, _ := cmd.Flags().GetString("before")
			allFlag, _ := cmd.Flags().GetBool("all")
			confirmFlag, _ := cmd.Flags().GetBool("confirm")

			if beforeFlag == "" && !allFlag {
				return fmt.Errorf("either --before or --all is required")
			}
			if beforeFlag != "" && allFlag {
				return fmt.Errorf("--before and --all are mutually exclusive")
			}

			var cutoffStr string
			if allFlag {
				// Use a far-future timestamp to match everything
				cutoffStr = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
			} else {
				cutoff, err := timeparse.ParseBefore(beforeFlag)
				if err != nil {
					return fmt.Errorf("invalid --before value: %w", err)
				}
				cutoffStr = cutoff.Format(time.RFC3339)
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.Purge(client, cli.PurgeOptions{
				Before: cutoffStr,
				DryRun: !confirmFlag,
			})
			if err != nil {
				return err
			}

			fmt.Print(cli.FormatPurge(result))

			// Clean up stale .consumed restart snapshot files
			if confirmFlag {
				thrumDir := filepath.Join(flagRepo, ".thrum")
				restartDir := filepath.Join(thrumDir, "restart")
				if entries, err := os.ReadDir(restartDir); err == nil {
					for _, e := range entries {
						if strings.HasSuffix(e.Name(), ".consumed") {
							_ = os.Remove(filepath.Join(restartDir, e.Name()))
						}
					}
				}
			}

			return nil
		},
	}
	cmd.Flags().String("before", "", "Cutoff: duration (2d, 24h), date (2026-03-15), or RFC 3339")
	cmd.Flags().Bool("all", false, "Purge all messages, sessions, and events")
	cmd.Flags().Bool("confirm", false, "Execute the purge (without this, only preview)")
	return cmd
}

func setupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up Thrum in a worktree",
		Long:  `Set up Thrum for your development environment.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Did you mean 'thrum worktree setup'?")
			fmt.Println("  thrum worktree setup   — Set up a worktree with redirects and agent registration")
			fmt.Println("  thrum worktree create  — Same as worktree setup")
			return nil
		},
	}

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
			structured, _ := cmd.Flags().GetString("structured")
			format, _ := cmd.Flags().GetString("format")
			to, _ := cmd.Flags().GetString("to")

			opts := cli.SendOptions{
				Content:       args[0],
				Scopes:        scopes,
				Refs:          refs,
				Mentions:      mentions,
				Structured:    structured,
				Format:        format,
				To:            to,
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
				if len(result.Audiences) > 0 {
					parts := make([]string, len(result.Audiences))
					for i, audience := range result.Audiences {
						parts[i] = audience.Type + ":" + audience.Value
					}
					fmt.Printf("  To: %s\n", strings.Join(parts, ", "))
				}
				if len(result.Recipients) > 0 {
					names := make([]string, len(result.Recipients))
					for i, recipient := range result.Recipients {
						names[i] = recipient.AgentID
					}
					fmt.Printf("  Recipients: %s\n", strings.Join(names, ", "))
				}
				for _, w := range result.Warnings {
					fmt.Fprintf(os.Stderr, "  warning: %s\n", w)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringSlice("scope", nil, "Add scope (repeatable, format: type:value)")
	cmd.Flags().StringSlice("ref", nil, "Add reference (repeatable, format: type:value)")
	cmd.Flags().StringSlice("mention", nil, "Mention a role (repeatable, format: @role)")
	cmd.Flags().String("structured", "", "Structured payload (JSON)")
	cmd.Flags().String("format", "markdown", "Message format (markdown, plain, json)")
	cmd.Flags().String("to", "", "Recipient (@agent_name or @everyone)")

	return cmd
}

func sentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sent",
		Short: "List messages you sent",
		Long: `List messages authored by the current agent, including recipient snapshots
and durable read state.

Like inbox, sent supports filtering and pagination. Use 'thrum message get <id>'
to inspect a message with full recipient state.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			to, _ := cmd.Flags().GetString("to")
			unread, _ := cmd.Flags().GetBool("unread")
			pageSize, _ := cmd.Flags().GetInt("page-size")
			page, _ := cmd.Flags().GetInt("page")
			if cmd.Flags().Changed("limit") {
				pageSize, _ = cmd.Flags().GetInt("limit")
			}

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.MessageOutbox(client, cli.OutboxOptions{
				CallerAgentID: agentID,
				To:            to,
				Unread:        unread,
				PageSize:      pageSize,
				Page:          page,
			})
			if err != nil {
				return err
			}

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else if !flagQuiet {
				fmt.Print(cli.FormatOutbox(result))
			}

			return nil
		},
	}

	cmd.Flags().Int("page-size", 10, "Results per page")
	cmd.Flags().Int("limit", 0, "Alias for --page-size")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().String("to", "", "Only sent messages addressed to this audience or recipient (format: @agent, @role, @group, @everyone)")
	cmd.Flags().Bool("unread", false, "Only sent messages with at least one unread recipient")

	return cmd
}

// groupCmd and subcommands removed — groups are no longer user-facing.
// Group RPC handlers (group.go) remain for Telegram bridge (tg:* groups).

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
					fmt.Print(cli.LegacyHint("inbox", flagQuiet, flagJSON))
				}
			}

			// Auto mark-as-read: mark all displayed messages as read
			// Skip when --unread is set so agents can peek without consuming messages.
			if !unread && len(result.Messages) > 0 {
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

// cronCmd is the root of the `thrum cron` subtree. Currently houses
// only install-inbox-poll. Subcommands here emit runtime-specific
// scheduling instructions for the calling agent to execute — they do
// not schedule anything directly; the daemon has no cron primitive.
func cronCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage cron-style scheduled tasks (runtime-specific emitters)",
	}
	cmd.AddCommand(cronInstallInboxPollCmd())
	return cmd
}

// cronInstallInboxPollCmd emits an instruction block that the
// SessionStart hook forwards to Claude Code, which reads it and calls
// CronCreate to schedule the 15-minute inbox-check poll. The command
// itself prints to stdout and exits — it does NOT call the daemon,
// touch identity, or invoke any runtime API directly. That's why it's
// classified as bypass.
func cronInstallInboxPollCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install-inbox-poll",
		Short: "Emit a CronCreate instruction to schedule the 15-minute inbox-check poll",
		Long: `Prints an instruction block for the running runtime agent to schedule
the 15-minute inbox-check cron. Emitted to stdout; the SessionStart hook
forwards it into the agent's context, where the runtime acts on it.

This command is print-only. It does NOT call the daemon, touch identity
state, or invoke any runtime scheduling API directly.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), inboxPollCronInstruction)
			return err
		},
	}
}

// inboxPollCronInstruction is the instruction block consumed by
// Claude Code's SessionStart hook → context-injection pipeline.
// The format intentionally mirrors other hook-output instructions:
// a delimited banner, explicit kv lines, and a closing banner so
// the runtime can parse it unambiguously.
const inboxPollCronInstruction = `===== SCHEDULE CRON (hook-inbox-delivery) =====
Please call CronCreate with the following parameters:
  cron: "7,22,37,52 * * * *"
  prompt: "Background inbox check. Run ` + "`thrum inbox --unread`" + ` once. If messages are present, process them; if not, return to idle without further action."
  recurring: true
  durable: false
===============================================`

// printAgentSummaryField emits the bare value of a single field from
// AgentSummary, newline-terminated. Unknown fields return an error so
// scripts fail loudly rather than silently consuming the empty string.
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
		output, err := json.MarshalIndent(summary, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal JSON output: %w", err)
		}
		fmt.Println(string(output))
	} else {
		fmt.Print(cli.FormatAgentSummary(summary))
	}

	return nil
}

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

func waitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for notifications (for hooks)",
		Long: `Block until a matching message arrives or timeout occurs.

Useful for automation and hooks that need to wait for specific messages.

Use --after to filter by relative time (sign convention):
  -30s  = include messages sent up to 30 seconds ago  (negative = "N ago")
  -5m   = include messages sent up to 5 minutes ago   (negative = "N ago")
  +60s  = only messages arriving at least 60 seconds in the future (positive = "N from now")

When --after is not specified, defaults to "now" (only messages arriving after wait starts).

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
			} else {
				// Default: look back 1s to avoid race between sender and wait startup.
				// Without this, a message sent at the same instant as wait starts
				// would be filtered out by the created_after > threshold check.
				afterTime = time.Now().Add(-1 * time.Second)
			}

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}
			agentRole, err := resolveLocalMentionRole()
			if err != nil {
				return fmt.Errorf("failed to resolve agent role: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}

			opts := cli.WaitOptions{
				Timeout:       timeout,
				Scope:         scope,
				Mention:       mention,
				After:         afterTime,
				CallerAgentID: agentID,
				ForAgent:      agentID,
				ForAgentRole:  agentRole,
				Quiet:         flagQuiet || flagJSON,
			}

			// Resolve PID file path for spawn coordination
			agentName, _ := cmd.Flags().GetString("agent-name")
			if agentName == "" {
				agentName = os.Getenv("THRUM_AGENT_ID")
				if agentName == "" {
					agentName = os.Getenv("THRUM_NAME")
				}
			}
			if agentName != "" {
				thrumDir, err := paths.ResolveThrumDir(flagRepo)
				if err != nil {
					thrumDir = filepath.Join(flagRepo, ".thrum")
				}
				varDir := filepath.Join(thrumDir, "var")
				_ = os.MkdirAll(varDir, 0o750)
				opts.PIDFilePath = filepath.Join(varDir, agentName+"-listener.pid")
			}

			if flagVerbose && !afterTime.IsZero() {
				fmt.Fprintf(os.Stderr, "Listening for messages after %s\n", afterTime.Format(time.RFC3339))
			}

			socketPath := os.Getenv("THRUM_SOCKET")
			if socketPath == "" {
				socketPath = cli.DefaultSocketPath(flagRepo)
			}

			_, err = cli.Wait(socketPath, opts)
			if err != nil {
				if err.Error() == "timeout waiting for message" {
					if !flagQuiet {
						fmt.Fprintln(os.Stderr, "NO_MESSAGES_TIMEOUT — re-run thrum wait to continue listening")
					}
					os.Exit(1)
				}
				return err
			}

			if flagJSON {
				out := map[string]string{
					"status": "received",
					"action": "ACTION REQUIRED: You have unread messages. Run `thrum inbox --unread` now to read and respond to them.",
				}
				output, _ := json.MarshalIndent(out, "", "  ")
				fmt.Println(string(output))
			} else if !flagQuiet {
				fmt.Println("MESSAGES_RECEIVED")
			}

			return nil
		},
	}

	cmd.Flags().String("timeout", "30s", "Max wait time (e.g., 30s, 5m)")
	cmd.Flags().String("scope", "", "Filter by scope (format: type:value)")
	cmd.Flags().String("mention", "", "Wait for mentions of role (format: @role)")
	cmd.Flags().String("after", "", "Only return messages after this relative time (e.g., -30s, -5m, +60s)")
	cmd.Flags().String("agent-name", "", "Agent name for listener PID file (enables spawn coordination)")

	return cmd
}

func daemonCmd() *cobra.Command {
	var flagLocal bool
	var flagForce bool

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the Thrum daemon",
	}

	cmd.PersistentFlags().BoolVar(&flagLocal, "local", false,
		"Local-only mode: skip git push/fetch in sync loop")
	cmd.PersistentFlags().BoolVar(&flagForce, "force", false,
		"Proceed even when the repo directory is not git-anchored (G2 override)")

	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the daemon in the background",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.DaemonStart(flagRepo, flagLocal, flagForce); err != nil {
				return err
			}

			if !flagQuiet {
				if wsPort := cli.ReadWebSocketPort(flagRepo); wsPort > 0 {
					fmt.Printf("✓ Daemon started — http://localhost:%d\n", wsPort)
				} else {
					fmt.Println("✓ Daemon started successfully")
				}
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
				fmt.Println("  All messaging commands will fail until the daemon is restarted:")
				fmt.Println("    thrum daemon start")
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

			// Exit code 1 when daemon is not running (like systemctl status).
			// In JSON mode, always exit 0 — the running status is in the JSON body.
			if !result.Running && !flagJSON {
				os.Exit(1)
			}

			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.DaemonRestart(flagRepo, flagLocal, flagForce); err != nil {
				return err
			}

			if !flagQuiet {
				if wsPort := cli.ReadWebSocketPort(flagRepo); wsPort > 0 {
					fmt.Printf("✓ Daemon restarted — http://localhost:%d\n", wsPort)
				} else {
					fmt.Println("✓ Daemon restarted successfully")
				}
			}

			return nil
		},
	})

	cmd.AddCommand(daemonRunCmd(&flagLocal, &flagForce))
	cmd.AddCommand(daemonLogsCmd())
	// Old tsync/peers commands removed — replaced by top-level "thrum peer" commands

	return cmd
}

func daemonLogsCmd() *cobra.Command {
	var (
		follow bool
		lines  int
		since  string
	)

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show daemon log output",
		Long: `Read the daemon log file at .thrum/var/daemon.log.

By default prints the last 50 lines. Use --follow/-f to stream new lines as
they are written. Use --since to filter by timestamp (e.g. "1h", "7d",
"2026-04-09", or RFC3339).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := cli.DaemonLogsOptions{
				Lines:  lines,
				Follow: follow,
			}
			if since != "" {
				t, err := timeparse.ParseBefore(since)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				opts.Since = &t
			}
			return cli.DaemonLogs(cmd.Context(), flagRepo, opts, os.Stdout)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Stream new log lines as they are written")
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "Number of lines to show (0 = all)")
	cmd.Flags().StringVar(&since, "since", "", "Only show lines at or after this time (e.g. 1h, 7d, 2026-04-09)")

	return cmd
}

func daemonRunCmd(flagLocal *bool, flagForce *bool) *cobra.Command {
	return &cobra.Command{
		Use:    "run",
		Short:  "Run the daemon in the foreground (internal use)",
		Hidden: true, // Hidden from help - used internally by daemon start
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemon(flagRepo, *flagLocal, *flagForce)
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
	var addType, addAddress string
	addCmd := &cobra.Command{
		Use:   "add",
		Short: "Start pairing and wait for a peer to connect",
		Long: `Starts a pairing session and displays a peercode.
Share this code with the person running 'thrum peer join' on the other side.
Blocks until a peer connects or the session times out (5 minutes).

--type is required. Run 'thrum peer add' with no flags to see the full
list of transports and a one-line "when to use" for each.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			peerType, parseErr := cli.ParsePeerType(addType)
			if parseErr != nil {
				if errors.Is(parseErr, cli.ErrPeerTypeMissing) {
					return errors.New(cli.MissingTypeMessage)
				}
				return parseErr
			}
			if peerType == cli.PeerTypeRepair {
				return errors.New("--type repair is not valid for 'peer add'.\n" +
					"Use 'thrum peer join --type repair <peer-name>' to reconcile an existing peer.")
			}
			if peerType == cli.PeerTypeNetwork {
				trimmed := strings.TrimSpace(addAddress)
				if trimmed == "" {
					return errors.New("--type network requires --address <ip>")
				}
				if net.ParseIP(trimmed) == nil {
					return fmt.Errorf("--type network --address %q: not a valid IP address", trimmed)
				}
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			// For tailscale: ensure auth key is available unless the daemon
			// already has a healthy tsnet node (xir.26 fix). Other transports
			// never need an auth key.
			pairingParams := &cli.PeerStartPairingParams{
				Type:    string(peerType),
				Address: strings.TrimSpace(addAddress),
			}
			if peerType == cli.PeerTypeTailscale {
				authKey := os.Getenv("THRUM_TS_AUTHKEY")
				var health cli.HealthResult
				var healthPtr *cli.HealthResult
				if hErr := client.Call("health", map[string]any{}, &health); hErr == nil {
					healthPtr = &health
				}
				if cli.AuthKeyPromptNeeded(authKey, healthPtr) {
					fmt.Print("Enter Tailscale auth key: ")
					if _, scanErr := fmt.Scanln(&authKey); scanErr != nil {
						return fmt.Errorf("failed to read auth key: %w", scanErr)
					}
					authKey = strings.TrimSpace(authKey)
					if authKey == "" {
						return errors.New("auth key is required for --type tailscale")
					}
					if flagRepo != "" {
						thrumDir := filepath.Join(flagRepo, ".thrum")
						if saveErr := config.SaveAuthKeyToEnvFile(thrumDir, authKey); saveErr != nil {
							fmt.Fprintf(os.Stderr, "Warning: could not save auth key to .env: %v\n", saveErr)
						}
					}
					pairingParams.AuthKey = authKey
				}
			}

			result, err := cli.PeerStartPairing(client, pairingParams)
			if err != nil {
				return err
			}

			localHostname, _ := os.Hostname()
			if result.Address != "" {
				connStr := daemon.FormatPeercode(localHostname, result.Address, result.Code)
				transportTag := result.Transport
				if transportTag == "" {
					transportTag = string(peerType)
				}
				fmt.Printf("Waiting for connection...\nPairing code (transport=%s): %s\n\n", transportTag, connStr)
				fmt.Printf("Share this with the other side:\n  thrum peer join --type %s --peercode %s\n\n", peerType, connStr)
			} else {
				fmt.Printf("Waiting for connection... Pairing code: %s\n", result.Code)
			}

			waitResult, err := cli.PeerWaitPairing(client)
			if err != nil {
				return err
			}

			if waitResult.Status == "paired" {
				fmt.Printf("Paired with %q (%s). Syncing started.\n", waitResult.PeerName, waitResult.PeerAddress)
				fmt.Println("\nTo enable message routing for an agent on this peer:")
				fmt.Println("  thrum peer configure <peer-name> add-agent <agent-name>")
			} else {
				fmt.Println("Pairing timed out. Run 'thrum peer add --type <transport>' again.")
			}

			return nil
		},
	}
	addCmd.Flags().StringVar(&addType, "type", "", "Transport: tailscale | local | network (REQUIRED)")
	addCmd.Flags().StringVar(&addAddress, "address", "", "LAN IP for --type network (must be assigned to a local NIC)")
	cmd.AddCommand(addCmd)

	// thrum peer join — connect to a remote peer using a peercode (or
	// reconcile an existing peer entry via --type repair).
	var peerCode string
	var repoPath string
	var joinType string
	var joinPeerName string
	var joinAddress string
	joinCmd := &cobra.Command{
		Use:   "join [peercode]",
		Short: "Join a remote peer (or repair an existing one)",
		Long: `Connects to a remote peer using the peercode from 'thrum peer add'.

--type is required. Run 'thrum peer join' with no flags to see the full
list of transports and a one-line "when to use" for each.

Peercode input methods (for --type tailscale|local|network):
  thrum peer join --type T name:ip:port:code              (positional argument)
  thrum peer join --type T --peercode name:ip:port:code   (flag)
  echo "name:ip:port:code" | thrum peer join --type T     (pipe, no flag)
  thrum peer join --type T --peercode -                   (pipe via stdin flag)
  thrum peer join --type T                                 (interactive prompt)

--type repair requires <peer-name> (positional or --peer-name) — uses
stored secrets in peers.json to re-handshake without minting a new token.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			peerType, parseErr := cli.ParsePeerType(joinType)
			if parseErr != nil {
				if errors.Is(parseErr, cli.ErrPeerTypeMissing) {
					return errors.New(cli.MissingTypeMessage)
				}
				return parseErr
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			params := &cli.PeerJoinParams{Type: string(peerType)}

			switch peerType {
			case cli.PeerTypeRepair:
				name := strings.TrimSpace(joinPeerName)
				if name == "" && len(args) > 0 {
					name = strings.TrimSpace(args[0])
				}
				if name == "" {
					return errors.New("--type repair requires a peer name (positional arg or --peer-name)")
				}
				params.PeerName = name

			default: // tailscale, local, network — peercode-based
				code := peerCode
				if code == "-" {
					code = ""
				}
				if code == "" && len(args) > 0 {
					code = strings.TrimSpace(args[0])
				}
				if code == "" {
					stat, _ := os.Stdin.Stat()
					if (stat.Mode() & os.ModeCharDevice) == 0 {
						scanner := bufio.NewScanner(os.Stdin)
						if scanner.Scan() {
							code = strings.TrimSpace(scanner.Text())
						}
					}
				}
				if code == "" {
					fmt.Print("Enter peercode: ")
					var input string
					if _, scanErr := fmt.Scanln(&input); scanErr != nil {
						return fmt.Errorf("failed to read peercode: %w", scanErr)
					}
					code = strings.TrimSpace(input)
				}

				_, ip, port, pairCode, parseErr := daemon.ParseConnectionString(code)
				if parseErr != nil {
					return parseErr
				}
				params.Address = fmt.Sprintf("%s:%d", ip, port)
				params.Code = pairCode
				params.RepoPath = repoPath
				params.LocalAddress = strings.TrimSpace(joinAddress)

				if peerType == cli.PeerTypeLocal && daemon.DetectTransport(params.Address) != "local" {
					fmt.Fprintf(os.Stderr,
						"warning: --type local but peercode address %s is not loopback; "+
							"the peer add side likely emitted a non-local peercode\n", params.Address)
				}
				if peerType == cli.PeerTypeNetwork {
					if daemon.DetectTransport(params.Address) == "local" {
						fmt.Fprintf(os.Stderr,
							"warning: --type network but peercode address %s is loopback; "+
								"did you mean --type local?\n", params.Address)
					}
					if params.LocalAddress == "" {
						return errors.New("--type network requires --address <ip> on this side too " +
							"(the LAN IP this daemon should bind for sync reach-back)")
					}
				}
			}

			result, err := cli.PeerJoin(client, params)
			if err != nil {
				return err
			}

			if result.Status == "paired" {
				name := result.PeerName
				if name == "" {
					name = "<peer>"
				}
				fmt.Printf("Paired with %q. Syncing started.\n", name)
				fmt.Println("\nTo enable message routing for an agent on this peer:")
				fmt.Println("  thrum peer configure <peer-name> add-agent <agent-name>")
			} else {
				fmt.Printf("Pairing failed: %s\n", result.Message)
			}

			return nil
		},
	}
	joinCmd.Flags().StringVar(&joinType, "type", "", "Transport: tailscale | local | network | repair (REQUIRED)")
	joinCmd.Flags().StringVar(&peerCode, "peercode", "", "Connection string from 'thrum peer add' (peercode-based types)")
	joinCmd.Flags().StringVar(&repoPath, "repo-path", "", "Filesystem path to the peer's repo (legacy hint; --type local preferred)")
	joinCmd.Flags().StringVar(&joinPeerName, "peer-name", "", "Existing peer name for --type repair")
	joinCmd.Flags().StringVar(&joinAddress, "address", "", "LAN IP for --type network (this daemon's reach-back address)")
	cmd.AddCommand(joinCmd)

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

	// thrum peer configure <peer-name> <action> <agent-name> — manage proxy agents
	cmd.AddCommand(&cobra.Command{
		Use:   "configure <peer-name> <action> <agent-name>",
		Short: "Configure proxy agents for a peer",
		Long:  "Add or remove proxy agents for a peer. Actions: add-agent, remove-agent",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			peerName, action, agentName := args[0], args[1], args[2]
			var result any
			if err := client.Call("peer.configure", map[string]any{
				"peer_name":  peerName,
				"action":     action,
				"agent_name": agentName,
			}, &result); err != nil {
				return err
			}
			fmt.Printf("✓ %s: %s %s\n", peerName, action, agentName)
			return nil
		},
	})

	return cmd
}

func monitorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Manage long-running monitor jobs",
		Long: `Monitor runs a command, filters its stdout/stderr through a regex, and
delivers matching lines as thrum messages to the specified target.

Examples:
  thrum monitor add --name errors --match "ERROR" --to @team -- tail -F /tmp/app.log
  thrum monitor list
  thrum monitor show <id>
  thrum monitor stop <id>
  thrum monitor restart <id>`,
	}

	// thrum monitor add -- COMMAND ARGS...
	var addName, addMatch, addTo, addCwd string
	var addDebounce time.Duration
	var addEnv []string

	addCmd := &cobra.Command{
		Use:     "start -- COMMAND ARGS...",
		Aliases: []string{"add"},
		Short:   "Start a new monitor job",
		Long: `Start a monitor job that runs COMMAND, filters output through a regex,
and delivers matching lines as messages to the specified target.

The command and its arguments must be separated from monitor flags with '--':
  thrum monitor add --name errors --match "ERROR" --to @team -- tail -F /var/log/app.log`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Extract post-'--' argv using cobra's native mechanism.
			// ArgsLenAtDash() returns the index in args where '--' appeared.
			// If '--' was not present, it returns -1.
			dashPos := cmd.ArgsLenAtDash()
			if dashPos < 0 {
				return fmt.Errorf("monitor add requires a command after '--'\nExample: thrum monitor add --name x --match y --to @t -- /bin/cmd arg1")
			}
			argv := args[dashPos:]
			if len(argv) == 0 {
				return fmt.Errorf("monitor add requires at least one command token after '--'")
			}

			cwd := addCwd
			if cwd == "" {
				var err error
				cwd, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("get working directory: %w", err)
				}
			}

			env := make(map[string]string)
			for _, e := range addEnv {
				k, v, ok := strings.Cut(e, "=")
				if !ok {
					return fmt.Errorf("invalid --env %q: expected KEY=VALUE", e)
				}
				env[k] = v
			}

			req := cli.MonitorStartRequest{
				Name:            addName,
				Argv:            argv,
				Match:           addMatch,
				Target:          addTo,
				Cwd:             cwd,
				Env:             env,
				DebounceSeconds: int(addDebounce.Seconds()),
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.MonitorStart(client, req)
			if err != nil {
				return err
			}
			fmt.Printf("Started monitor %s (%s) — target %s\n", addName, result.ID, addTo)
			return nil
		},
	}
	addCmd.Flags().StringVar(&addName, "name", "", "Unique monitor name (required)")
	addCmd.Flags().StringVar(&addMatch, "match", "", "Regex pattern to filter output (required)")
	addCmd.Flags().StringVar(&addTo, "to", "", "Target agent or group for matched messages (required)")
	addCmd.Flags().StringVar(&addCwd, "cwd", "", "Working directory for the command (default: current directory)")
	addCmd.Flags().DurationVar(&addDebounce, "debounce", 60*time.Second, "Leading-edge debounce window (minimum 30s)")
	addCmd.Flags().StringArrayVar(&addEnv, "env", nil, "Environment variable in KEY=VALUE form (repeatable)")
	_ = addCmd.MarkFlagRequired("name")
	_ = addCmd.MarkFlagRequired("match")
	_ = addCmd.MarkFlagRequired("to")
	cmd.AddCommand(addCmd)

	// thrum monitor list [--all] [--json]
	{
		var includeAll bool
		listCmd := &cobra.Command{
			Use:   "list",
			Short: "List monitor jobs (default: running only; --all shows stopped/dead <1wk)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				client, err := getClient()
				if err != nil {
					return fmt.Errorf("connect to daemon: %w", err)
				}
				defer func() { _ = client.Close() }()
				if flagJSON {
					return cli.MonitorListJSON(client, includeAll, os.Stdout)
				}
				return cli.MonitorList(client, includeAll, os.Stdout)
			},
		}
		listCmd.Flags().BoolVar(&includeAll, "all", false,
			"Include stopped/dead monitors (younger than a week)")
		cmd.AddCommand(listCmd)
	}

	// thrum monitor show <id> [--json]
	cmd.AddCommand(&cobra.Command{
		Use:   "show <id>",
		Short: "Show details of a monitor job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()
			if flagJSON {
				return cli.MonitorShowJSON(client, args[0], os.Stdout)
			}
			return cli.MonitorShow(client, args[0], os.Stdout)
		},
	})

	// thrum monitor stop <id>
	cmd.AddCommand(&cobra.Command{
		Use:   "stop <id>",
		Short: "Stop and remove a monitor job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()
			if err := cli.MonitorStop(client, args[0]); err != nil {
				return err
			}
			fmt.Printf("Stopped monitor %s\n", args[0])
			return nil
		},
	})

	// thrum monitor restart <id>
	cmd.AddCommand(&cobra.Command{
		Use:   "restart <id>",
		Short: "Restart a monitor job (preserves the same ID)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()
			result, err := cli.MonitorRestart(client, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Restarted — ID: %s\n", result.ID)
			return nil
		},
	})

	// thrum monitor logs <id>
	{
		var logsLimit int
		logsCmd := &cobra.Command{
			Use:   "logs <id>",
			Short: "Show the most recent monitor matches (historical lookup)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				client, err := getClient()
				if err != nil {
					return fmt.Errorf("connect to daemon: %w", err)
				}
				defer func() { _ = client.Close() }()
				return cli.MonitorLogs(client, args[0], logsLimit, os.Stdout)
			},
		}
		logsCmd.Flags().IntVarP(&logsLimit, "limit", "n", 20, "Max number of matches to return")
		cmd.AddCommand(logsCmd)
	}

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
					Worktree: cli.GetWorktreeName(flagRepo),
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
				// Output as JSON
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
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

	return cmd
}

// inferWorktreeBasePath returns the conventional worktree base path for a repo.
// Checks ~/.workspaces/<project>; returns it whether or not it exists yet.
func inferWorktreeBasePath(repoPath string) string {
	projectName := filepath.Base(repoPath)
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".workspaces", projectName)
}

func worktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Manage git worktrees with thrum/beads setup",
	}
	createCmd := worktreeCreateCmd()
	cmd.AddCommand(createCmd)

	// setup (alias for create)
	setupCmd := &cobra.Command{
		Use:   "setup <name>",
		Short: "Set up a worktree with redirects and agent (alias for 'worktree create')",
		Args:  cobra.ExactArgs(1),
		RunE:  createCmd.RunE,
	}
	setupCmd.Flags().AddFlagSet(createCmd.Flags())
	cmd.AddCommand(setupCmd)

	cmd.AddCommand(worktreeTeardownCmd())
	cmd.AddCommand(worktreeListCmd())
	return cmd
}

func worktreeCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new worktree with thrum/beads setup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
				return fmt.Errorf("invalid worktree name %q: must not contain /, \\, or parent references", name)
			}
			detach, _ := cmd.Flags().GetBool("detach")
			branch, _ := cmd.Flags().GetString("branch")

			repoPath := paths.EffectiveRepoPath(flagRepo)
			thrumDir := filepath.Join(repoPath, ".thrum")
			cfg, err := config.LoadThrumConfig(thrumDir)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			basePath := cfg.Worktrees.BasePath
			if basePath == "" {
				basePath = inferWorktreeBasePath(repoPath)
			}
			// Ensure base_path includes the repo name to prevent worktrees
			// from different repos colliding in a flat directory. Stale configs
			// from older thrum versions may have base_path without the repo name.
			repoName := filepath.Base(repoPath)
			if filepath.Base(basePath) != repoName {
				basePath = filepath.Join(basePath, repoName)
			}
			worktreePath := filepath.Join(basePath, name)

			// Guard: worktree path must not resolve to the repo root
			resolvedWT, _ := filepath.Abs(worktreePath)
			resolvedRepo, _ := filepath.Abs(repoPath)
			if resolvedWT == resolvedRepo {
				return fmt.Errorf("worktree path %q resolves to the repo root — check worktrees.base_path in config", worktreePath)
			}
			// Guard: base_path itself must not be the repo root
			resolvedBase, _ := filepath.Abs(basePath)
			if resolvedBase == resolvedRepo {
				return fmt.Errorf("worktrees.base_path %q resolves to the repo root — worktrees must be created outside the repo", basePath)
			}

			// 1. Create git worktree
			gitArgs := []string{"worktree", "add"}
			if detach {
				gitArgs = append(gitArgs, "--detach")
			} else {
				if branch == "" {
					branch = "feature/" + name
				}
				gitArgs = append(gitArgs, "-b", branch)
			}
			gitArgs = append(gitArgs, worktreePath)

			out, err := safecmd.Git(cmd.Context(), repoPath, gitArgs...)
			if err != nil {
				return fmt.Errorf("git worktree add: %s\n%s", err, out)
			}
			fmt.Printf("✓ Worktree created at %s\n", worktreePath)

			// 2. Set up redirects (.thrum/ and optionally .beads/)
			if err := cli.EnsureWorktreeRedirects(worktreePath, repoPath); err != nil {
				return fmt.Errorf("redirect setup: %w", err)
			}
			fmt.Println("✓ Thrum redirect configured")

			// 3. Optional: create tmux session with agent quickstart
			agentName, _ := cmd.Flags().GetString("name")
			role, _ := cmd.Flags().GetString("role")
			module, _ := cmd.Flags().GetString("module")

			if agentName != "" && role != "" && module != "" {
				intent, _ := cmd.Flags().GetString("intent")
				runtimeFlag, _ := cmd.Flags().GetString("runtime")

				client, err := getClient()
				if err != nil {
					return fmt.Errorf("connect to daemon: %w", err)
				}
				defer func() { _ = client.Close() }()

				// Delegate to tmux create which handles quickstart via
				// SendKeys (PID-isolated, monitor-silence, window title).
				if _, err := cli.TmuxCreate(client, cli.TmuxCreateOptions{
					Name:      name,
					Cwd:       worktreePath,
					AgentName: agentName,
					Role:      role,
					Module:    module,
					Intent:    intent,
					Runtime:   runtimeFlag,
				}); err != nil {
					return fmt.Errorf("create tmux session: %w", err)
				}

				// Wait for identity file to appear. The daemon retries
				// quickstart at 5s if shell init swallowed the first attempt,
				// so we poll for 12s to cover both attempts.
				idPath := filepath.Join(worktreePath, ".thrum", "identities", agentName+".json")
				deadline := time.Now().Add(12 * time.Second)
				for time.Now().Before(deadline) {
					if _, err := os.Stat(idPath); err == nil {
						break
					}
					time.Sleep(500 * time.Millisecond)
				}
				if _, err := os.Stat(idPath); err != nil {
					// Capture pane so the caller sees what went wrong
					msg := "Warning: agent identity not created after 12s — quickstart may have failed"
					if capture, captureErr := cli.TmuxCapture(client, name, 30); captureErr == nil && capture.Content != "" {
						msg += "\n\nTmux pane output:\n" + capture.Content
					}
					fmt.Fprintln(os.Stderr, msg)
				} else {
					fmt.Printf("✓ Registered @%s in worktree\n", agentName)
					fmt.Printf("  Agent is NOT running yet. Start it with:\n")
					fmt.Printf("    thrum tmux launch %s [--runtime <runtime>]\n", name)
				}
			} else if !flagJSON {
				fmt.Printf("  No agent registered. To set up an agent:\n")
				fmt.Printf("    thrum tmux create %s --cwd %s --name <agent> --role <role> --module <module>\n", name, worktreePath)
			}

			if flagJSON {
				result := map[string]string{
					"worktree_path": worktreePath,
					"branch":        branch,
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			return nil
		},
	}
	cmd.Flags().Bool("detach", false, "Create detached HEAD worktree")
	cmd.Flags().StringP("branch", "b", "", "Branch name (default: feature/<name>)")
	cmd.Flags().String("name", "", "Agent name (triggers quickstart in tmux)")
	cmd.Flags().String("role", "", "Agent role")
	cmd.Flags().String("module", "", "Agent module")
	cmd.Flags().String("intent", "", "Agent intent")
	cmd.Flags().String("runtime", "", "Preferred runtime")
	return cmd
}

func worktreeTeardownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "teardown <name>",
		Short: "Remove a worktree and clean up thrum/beads artifacts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
				return fmt.Errorf("invalid worktree name %q: must not contain /, \\, or parent references", name)
			}
			repoPath := paths.EffectiveRepoPath(flagRepo)
			thrumDir := filepath.Join(repoPath, ".thrum")
			cfg, err := config.LoadThrumConfig(thrumDir)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			basePath := cfg.Worktrees.BasePath
			if basePath == "" {
				basePath = inferWorktreeBasePath(repoPath)
			}
			repoName := filepath.Base(repoPath)
			if filepath.Base(basePath) != repoName {
				basePath = filepath.Join(basePath, repoName)
			}
			worktreePath := filepath.Join(basePath, name)

			// Check worktree exists
			if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
				return fmt.Errorf("worktree not found: %s", worktreePath)
			}

			// Clean up identity files that reference this worktree
			identitiesDir := filepath.Join(worktreePath, ".thrum", "identities")
			if entries, err := os.ReadDir(identitiesDir); err == nil {
				for _, entry := range entries {
					if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
						agentName := strings.TrimSuffix(entry.Name(), ".json")
						idPath := filepath.Join(identitiesDir, entry.Name())
						if err := os.Remove(idPath); err != nil && !os.IsNotExist(err) {
							fmt.Fprintf(os.Stderr, "  Warning: failed to remove identity %s: %v\n", agentName, err)
						} else {
							fmt.Printf("  Removed identity: %s\n", agentName)
						}
					}
				}
			}

			// Kill associated tmux session if any (best-effort, ignore errors)
			if client, err := getClient(); err == nil {
				defer func() { _ = client.Close() }()
				_ = cli.TmuxKill(client, name)
			}

			// Remove git worktree
			out, err := safecmd.Git(cmd.Context(), repoPath, "worktree", "remove", "--force", worktreePath)
			if err != nil {
				return fmt.Errorf("git worktree remove: %s\n%s", err, out)
			}

			fmt.Printf("✓ Worktree %s removed\n", name)
			return nil
		},
	}
}

func worktreeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List worktrees with thrum agent info",
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath := paths.EffectiveRepoPath(flagRepo)

			// JSON output mode
			if flagJSON {
				return worktreeListJSON(repoPath)
			}

			// Get git worktree list
			out, err := safecmd.Git(cmd.Context(), repoPath, "worktree", "list", "--porcelain")
			if err != nil {
				return fmt.Errorf("git worktree list: %w", err)
			}

			type worktreeInfo struct {
				path   string
				branch string
				head   string
			}
			var worktrees []worktreeInfo
			var current worktreeInfo

			for _, line := range strings.Split(string(out), "\n") {
				if p, ok := strings.CutPrefix(line, "worktree "); ok {
					current = worktreeInfo{path: p}
				} else if h, ok := strings.CutPrefix(line, "HEAD "); ok {
					current.head = h[:min(7, len(h))]
				} else if b, ok := strings.CutPrefix(line, "branch "); ok {
					current.branch = strings.TrimPrefix(b, "refs/heads/")
				} else if line == "" && current.path != "" {
					worktrees = append(worktrees, current)
					current = worktreeInfo{}
				}
			}

			if len(worktrees) == 0 {
				fmt.Println("No worktrees found.")
				return nil
			}

			// Print header
			fmt.Printf("%-30s %-25s %-10s %-20s %-10s\n", "WORKTREE", "BRANCH", "HEAD", "AGENT", "STATUS")
			fmt.Println(strings.Repeat("─", 100))

			for _, wt := range worktrees {
				wtName := filepath.Base(wt.path)
				agentName := ""
				agentStatus := ""

				// Check for identity files in this worktree
				idDir := filepath.Join(wt.path, ".thrum", "identities")
				if entries, err := os.ReadDir(idDir); err == nil {
					for _, entry := range entries {
						if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
							data, err := os.ReadFile(filepath.Join(idDir, entry.Name())) // #nosec G304 -- idDir under .thrum/identities/
							if err != nil {
								continue
							}
							var idFile config.IdentityFile
							if err := json.Unmarshal(data, &idFile); err != nil {
								continue
							}
							agentName = idFile.Agent.Name
							agentStatus = idFile.AgentStatus
							break // show first agent
						}
					}
				}

				fmt.Printf("%-30s %-25s %-10s %-20s %-10s\n", wtName, wt.branch, wt.head, agentName, agentStatus)
			}

			return nil
		},
	}
}

func worktreeListJSON(repoPath string) error {
	out, err := safecmd.Git(context.Background(), repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return fmt.Errorf("git worktree list: %w", err)
	}

	type worktreeJSON struct {
		Path   string `json:"path"`
		Branch string `json:"branch"`
		Head   string `json:"head"`
		Agent  string `json:"agent,omitempty"`
		Status string `json:"status,omitempty"`
	}

	var worktrees []worktreeJSON
	var path, branch, head string

	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		} else if h, ok := strings.CutPrefix(line, "HEAD "); ok {
			head = h[:min(7, len(h))]
		} else if b, ok := strings.CutPrefix(line, "branch "); ok {
			branch = strings.TrimPrefix(b, "refs/heads/")
		} else if line == "" && path != "" {
			wt := worktreeJSON{Path: path, Branch: branch, Head: head}

			// Check for agent identity
			idDir := filepath.Join(path, ".thrum", "identities")
			if entries, err := os.ReadDir(idDir); err == nil {
				for _, entry := range entries {
					if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
						data, err := os.ReadFile(filepath.Join(idDir, entry.Name())) //#nosec G304
						if err != nil {
							continue
						}
						var idFile config.IdentityFile
						if err := json.Unmarshal(data, &idFile); err != nil {
							continue
						}
						wt.Agent = idFile.Agent.Name
						wt.Status = idFile.AgentStatus
						break
					}
				}
			}

			worktrees = append(worktrees, wt)
			path, branch, head = "", "", ""
		}
	}

	data, err := json.MarshalIndent(worktrees, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

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

func setLocalAgentStatus(status string) error {
	idFile, _, err := config.LoadIdentityWithPath(flagRepo)
	if err != nil {
		return fmt.Errorf("load identity: %w", err)
	}
	idFile.AgentStatus = status
	idFile.AgentStatusUpdatedAt = time.Now().UTC()
	thrumDir := filepath.Join(paths.EffectiveRepoPath(flagRepo), ".thrum")
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		return fmt.Errorf("save identity: %w", err)
	}
	fmt.Printf("✓ Status set to %s\n", status)
	return nil
}

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
		return fmt.Errorf("failed to get agent identity: %w\n\nHint: Register first with 'thrum quickstart --name <name> --role <role> --module <module>'", err)
	}

	// Parse scope flags (optional for now, will be used in Epic 4)
	// scopes, _ := cmd.Flags().GetStringSlice("scope")
	// TODO: Parse scopes when Epic 4 is implemented

	opts := cli.SessionStartOptions{
		AgentID: whoami.AgentID,
	}

	// Auto-set worktree ref so heartbeat can extract git context
	if worktreeRoot := cli.GitTopLevel("."); worktreeRoot != "" {
		opts.Refs = append(opts.Refs, types.Ref{Type: "worktree", Value: worktreeRoot})
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

	newIntent := args[0]
	result, err := cli.SessionSetIntent(client, whoami.SessionID, newIntent)
	if err != nil {
		return err
	}

	// Write intent back to identity file
	if idFile, _, loadErr := config.LoadIdentityWithPath(flagRepo); loadErr == nil {
		thrumDir := filepath.Join(flagRepo, ".thrum")
		idFile.Intent = newIntent
		_ = config.SaveIdentityFile(thrumDir, idFile)
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

The agent must be registered first (use 'thrum quickstart').
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
It appears in 'thrum agent list --context'.
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
It appears in 'thrum agent list --context'.
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
			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w", err)
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.MessageEdit(client, args[0], args[1], agentID)
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

// subscribeCmd, unsubscribeCmd, subscriptionsCmd removed —
// subscriptions are no longer a concept. Use thrum wait for CLI notifications.

func contextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage agent context",
	}

	cmd.AddCommand(contextSaveCmd())
	cmd.AddCommand(contextShowCmd())
	cmd.AddCommand(contextClearCmd())
	cmd.AddCommand(contextSyncCmd())
	cmd.AddCommand(contextPreambleCmd())

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

			absRepo, _ := filepath.Abs(flagRepo)

			var content []byte
			if flagFile != "" {
				content, err = os.ReadFile(flagFile) // #nosec G304 -- flagFile is user-specified via CLI flag; this is a CLI tool, user controls the path
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
				RepoPath:  absRepo,
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
	var flagProject bool
	var flagSession bool

	cmd := &cobra.Command{
		Use:     "show",
		Aliases: []string{"load"},
		Short:   "Show agent context",
		Long: `Show saved context for the current agent (or --agent NAME).
Also available as 'thrum context load'.

Examples:
  thrum context show                # Show both project state and session context
  thrum context show --project      # Show project state only
  thrum context show --session      # Show session context only
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

			absRepo, _ := filepath.Abs(flagRepo)
			thrumDir := filepath.Join(absRepo, ".thrum")

			// Determine what to show: default is both
			showProject := flagProject || (!flagProject && !flagSession)
			showSession := flagSession || (!flagProject && !flagSession)

			// Show project state
			if showProject {
				projectPath := filepath.Join(thrumDir, "context", "project_state.md")
				if data, err := os.ReadFile(projectPath); err == nil && len(data) > 0 { // #nosec G304 -- internal context file
					if flagRaw {
						fmt.Println("<!-- project_state: .thrum/context/project_state.md -->")
						fmt.Print(string(data))
						if data[len(data)-1] != '\n' {
							fmt.Println()
						}
						fmt.Println("<!-- end project_state -->")
					} else {
						fmt.Println("--- Project State ---")
						fmt.Println()
						fmt.Print(string(data))
						if data[len(data)-1] != '\n' {
							fmt.Println()
						}
					}
					if showSession {
						fmt.Println()
					}
				} else if flagProject {
					// Only show missing message if --project was explicitly requested
					fmt.Println("No project state found (.thrum/context/project_state.md)")
					return nil
				}
			}

			if !showSession {
				return nil
			}

			// Show session context (existing behavior)
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
				RepoPath:        absRepo,
			}, &resp); err != nil {
				return err
			}

			if !resp.HasContext && !resp.HasPreamble {
				if !showProject {
					fmt.Printf("No context saved for %s\n", resp.AgentName)
				}
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
				if showProject {
					fmt.Println("--- Session Context ---")
					fmt.Println()
				}
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
	cmd.Flags().BoolVar(&flagProject, "project", false, "Show project state only")
	cmd.Flags().BoolVar(&flagSession, "session", false, "Show session context only")

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

			absRepo, _ := filepath.Abs(flagRepo)

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			var resp rpc.ContextClearResponse
			if err := client.Call("context.clear", rpc.ContextClearRequest{
				AgentName: agentID,
				RepoPath:  absRepo,
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

			absRepo, _ := filepath.Abs(flagRepo)

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			if flagInit {
				// Reset to role-aware default preamble
				role, _ := resolveLocalMentionRole()
				var resp rpc.PreambleSaveResponse
				if err := client.Call("context.preamble.save", rpc.PreambleSaveRequest{
					AgentName: agentID,
					Content:   agentcontext.RoleAwarePreamble(role),
					RepoPath:  absRepo,
				}, &resp); err != nil {
					return err
				}
				fmt.Println(resp.Message)
				return nil
			}

			if flagFile != "" {
				// Set preamble from file
				data, err := os.ReadFile(flagFile) // #nosec G304 -- flagFile is user-specified via CLI flag; this is a CLI tool, user controls the path
				if err != nil {
					return fmt.Errorf("read preamble file: %w", err)
				}
				var resp rpc.PreambleSaveResponse
				if err := client.Call("context.preamble.save", rpc.PreambleSaveRequest{
					AgentName: agentID,
					Content:   data,
					RepoPath:  absRepo,
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
				RepoPath:  absRepo,
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

// loadInitBootstrapMode returns the NonGitBootstrap guard mode for
// the init dir, falling back to strict when config is absent or the
// field is unset. In the non-git-bootstrap scenario the config file
// typically doesn't exist yet, so the fallback path is the common
// case.
func loadInitBootstrapMode(dir string) guard.Mode {
	m := guard.LoadConfigFromDir(dir).NonGitBootstrap
	if m == "" {
		return guard.ModeStrict
	}
	return m
}

// resolvePrimeIdentityPath resolves the on-disk identity file path for
// the current agent and returns the closest-runtime PID plus the
// stored AgentPID so the caller can compare before writing. Returns
// ok=false when the agent has no identity file yet (first-prime) —
// G5 + WritePID are no-ops in that case.
func resolvePrimeIdentityPath(agentID string) (repoPath, idPath string, runtimePID, storedPID int, ok bool) {
	repoPath = paths.EffectiveRepoPath(".")
	idPath = filepath.Join(repoPath, ".thrum", "identities", agentID+".json")
	// #nosec G304 -- idPath is derived from the agent's own identity dir.
	data, err := os.ReadFile(idPath)
	if err != nil {
		return repoPath, "", 0, 0, false
	}
	var id config.IdentityFile
	if err := json.Unmarshal(data, &id); err != nil {
		// Corrupted file — let G5's own load path surface the error.
		storedPID = 0
	} else {
		storedPID = id.AgentPID
	}
	ctx := context.Background()
	rtPID, _, _ := guard.ClosestRuntimeAncestor(ctx, os.Getpid())
	return repoPath, idPath, rtPID, storedPID, true
}

// loadPrimeOwnershipMode returns the PrimeOwnership guard mode for
// repoPath, falling back to strict when config is absent or the field
// is unset. Guard enforcement defaults on, not off, on malformed
// config.
func loadPrimeOwnershipMode(repoPath string) guard.Mode {
	m := guard.LoadConfigFromDir(repoPath).PrimeOwnership
	if m == "" {
		return guard.ModeStrict
	}
	return m
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

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}

			// Identity Guard G5 (prime_ownership): refuse if the caller
			// is not the topmost runtime that owns the identity file.
			// Dead/absent owners pass through so a first-prime or an
			// orphaned-agent reclaim can proceed. When the closest
			// runtime differs from the stored PID and the stored PID is
			// dead, guard.WritePID refreshes the identity atomically.
			if repoPath, idPath, runtimePID, storedPID, ok := resolvePrimeIdentityPath(agentID); ok {
				pc := &guard.PrimeContext{
					Mode:         loadPrimeOwnershipMode(repoPath),
					IdentityPath: idPath,
					ClosestRtPID: runtimePID,
					IsPIDAlive:   process.IsRunning,
				}
				if err := guard.G5(pc); err != nil {
					return err
				}
				// Only write when the stored PID diverged from the
				// current runtime — unconditional writes on every
				// prime inflate mtime and risk lock contention with
				// concurrent agents in the same repo.
				if runtimePID > 0 && runtimePID != storedPID {
					if err := guard.WritePID(idPath, runtimePID); err != nil {
						fmt.Fprintf(os.Stderr, "thrum: prime WritePID failed: %v\n", err)
					}
				}
			}

			result := cli.ContextPrime(client, agentID)

			// Wire SingleAgentMode from config
			if result.RepoPath != "" {
				thrumDir := filepath.Join(result.RepoPath, ".thrum")
				if cfg, err := config.LoadThrumConfig(thrumDir); err == nil {
					result.SingleAgentMode = cfg.Daemon.SingleAgentMode
				}
				// Wire SavedSessionContext
				if result.Identity != nil {
					ctxPath := filepath.Join(thrumDir, "context", result.Identity.AgentID+".md")
					if data, err := os.ReadFile(ctxPath); err == nil { // #nosec G304 -- internal context file
						result.SavedSessionContext = string(data)
					}
				}

				// Wire RestartSnapshot (consumed on read)
				if result.Identity != nil {
					if snapshot, err := restart.ConsumeInPrime(thrumDir, result.Identity.AgentID); err == nil {
						result.RestartSnapshot = snapshot
					}
				}

				// Identity refresh and TmuxMode detection are now handled
				// inside getClient() → RefreshLocalIdentity and ContextPrime
				// respectively. See thrum-pxz.5 and thrum-pxz.7.
			}

			if flagJSON {
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatPrimeContext(result))
			}

			// Clean up consumed restart snapshot
			if result.RestartSnapshot != "" && result.Identity != nil && result.RepoPath != "" {
				restart.CleanupConsumed(filepath.Join(result.RepoPath, ".thrum"), result.Identity.AgentID)
			}

			return nil
		},
	}
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
			// Also update the repo-level config so 'config show' reflects the change.
			thrumDir := filepath.Join(flagRepo, ".thrum")
			cfg, err := config.LoadThrumConfig(thrumDir)
			if err == nil {
				cfg.Runtime.Primary = args[0]
				_ = config.SaveThrumConfig(thrumDir, cfg)
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
			if err := os.WriteFile(destPath, content, 0644); err != nil { //#nosec G306 -- markdown context file synced to git worktree, not sensitive data
				return fmt.Errorf("write context to sync worktree: %w", err)
			}

			// Stage and commit in sync worktree (safecmd.Git injects the thrum user overrides automatically)
			ctx := cmd.Context()
			if out, err := safecmd.Git(ctx, syncDir, "add", filepath.Join("context", agentID+".md")); err != nil {
				return fmt.Errorf("stage context file: %s: %w", string(out), err)
			}

			if out, err := safecmd.Git(ctx, syncDir, "commit", "--no-verify", "-m", fmt.Sprintf("context: sync %s", agentID), "--allow-empty"); err != nil {
				// "nothing to commit" is OK
				if !strings.Contains(string(out), "nothing to commit") {
					return fmt.Errorf("commit context: %s: %w", string(out), err)
				}
			}

			// Push (skip in local-only mode - check for remote)
			if _, remoteErr := safecmd.Git(ctx, syncDir, "remote", "get-url", "origin"); remoteErr != nil {
				// No remote configured is not an error — local-only sync is valid
				fmt.Printf("Context synced locally for %s (no remote configured).\n", agentID)
				return nil //nolint:nilerr // intentional: no remote means local-only mode, not a failure
			}

			if out, err := safecmd.GitLong(ctx, syncDir, "push", "origin", "a-sync"); err != nil {
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
	data, err := os.ReadFile(path) // #nosec G304 -- path is .thrum/context/<agentName>.md, an internal context file
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
						Worktree: cli.GetWorktreeName(flagRepo),
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
				if ttmux.InTmux() {
					if target, err := ttmux.PaneTarget(); err == nil && target != "" && idFile.TmuxSession != target {
						idFile.TmuxSession = target
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
				output, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(output))
			} else {
				fmt.Print(cli.FormatQuickstart(result))
				if !flagQuiet {
					fmt.Print(cli.LegacyHint("quickstart", flagQuiet, flagJSON))
				}
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

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}
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
					fmt.Print(cli.LegacyHint("overview", flagQuiet, flagJSON))
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
  thrum team --system
  thrum team --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			includeAll, _ := cmd.Flags().GetBool("all")
			includeSystem, _ := cmd.Flags().GetBool("system")
			req := cli.TeamListRequest{
				IncludeOffline: includeAll,
				IncludeSystem:  includeSystem,
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
	cmd.Flags().Bool("system", false, "Include reserved pseudo-agents (@supervisor_*, etc.)")

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
					fmt.Print(cli.LegacyHint("who-has", flagQuiet, flagJSON))
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
					fmt.Print(cli.LegacyHint("ping", flagQuiet, flagJSON))
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

	if flagJSON {
		output, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(output))
	} else {
		fmt.Print(cli.FormatHeartbeat(result))
		if !flagQuiet {
			fmt.Print(cli.LegacyHint("session.heartbeat", flagQuiet, flagJSON))
		}
	}

	return nil
}

// getClient returns a configured RPC client.
// Respects THRUM_SOCKET env var if set, otherwise uses DefaultSocketPath.
// GetClient opens a daemon connection and refreshes the local identity
// file + daemon's agent record from live process/tmux/git state. Use for
// every command except daemon lifecycle, init, and quickstart — those
// should call getClientNoRefresh().
//
// Refresh failures are non-fatal: they log to stderr and the underlying
// command proceeds normally. See RefreshLocalIdentity doc for details.
func getClient() (*cli.Client, error) {
	client, err := getClientNoRefresh()
	if err != nil {
		return nil, err
	}

	repoPath := flagRepo
	if repoPath == "" {
		repoPath = "."
	}
	if _, refreshErr := cli.RefreshLocalIdentity(client, repoPath); refreshErr != nil {
		fmt.Fprintf(os.Stderr, "thrum: identity refresh failed: %v\n", refreshErr)
	}

	return client, nil
}

// getClientNoRefresh opens a daemon connection without running the identity
// refresh. Use for:
//   - daemon lifecycle commands (start/stop/restart/status/logs)
//   - init and quickstart (before/during initial registration)
//   - any test or diagnostic tool that must not side-effect the identity
func getClientNoRefresh() (*cli.Client, error) {
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
	if agentID := strings.TrimSpace(os.Getenv("THRUM_AGENT_ID")); agentID != "" {
		return agentID, nil
	}

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
func runDaemon(repoPath string, flagLocal bool, flagForce bool) error {
	// Resolve to absolute path
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("failed to resolve repo path: %w", err)
	}

	// Identity Guard G2: refuse to start the daemon from a non-git
	// directory unless --force is set. Closes the same footgun as
	// `thrum init` G2 — prevents the daemon from anchoring to an
	// arbitrary cwd (e.g. $HOME) and materializing a .thrum/ there.
	// Mode is loaded from the repo's identity_guard.non_git_bootstrap
	// config; strict is the default when unset.
	if err := guardDaemonBootstrap(absPath, flagForce, nil); err != nil {
		return err
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

	// Install rotating log writer as early as possible so every subsequent
	// log.Printf in daemon startup is captured. lumberjack rotates the file
	// when it exceeds 10MB and keeps 4 compressed backups for 28 days.
	logWriter := daemon.NewLogWriter(varDir)
	defer func() { _ = logWriter.Close() }()
	daemon.InstallLogWriter(logWriter)
	log.Printf("daemon: starting version=%s repo=%s", Version+"+"+Build, absPath)

	// Ensure identities directory exists in the local checkout, not the shared redirect target.
	identitiesDir := filepath.Join(absPath, ".thrum", "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		return fmt.Errorf("failed to create identities directory: %w", err)
	}

	// Backfill runtime from preferred_runtime for any identity files that
	// pre-date the runtime field. Idempotent and non-fatal: silently skips
	// unreadable files, never aborts daemon startup.
	config.BackfillIdentityRuntime(filepath.Join(absPath, ".thrum"))

	// Self-heal embedded reference files (.thrum/strategies/*.md + .thrum/llms.txt).
	// Re-running this is idempotent and covers:
	//   - Repos initialized before this feature landed (backfill on first daemon restart)
	//   - Files accidentally deleted between init and daemon start
	//   - Version bumps: keeps the on-disk reference in sync with the installed binary
	// A failure here must not prevent daemon startup - log and continue.
	// NOTE: thrumDir is the redirect target (shared .thrum/) in worktree setups -
	// correct here, because reference files are binary-version content, not
	// per-checkout data like identities.
	if err := agentcontext.WriteStrategies(thrumDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to refresh embedded reference files: %v\n", err)
	}

	// Generate repo ID (use directory name for now)
	repoID := filepath.Base(absPath)

	// Create peer registry early so we can read the persistent daemon_id
	peersFile := filepath.Join(varDir, "peers.json")
	peerRegistry, err := daemon.NewPeerRegistry(peersFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to create peer registry: %v\n", err)
	}

	// Create state manager. Passing empty daemonID so state.NewState calls
	// identity.Bootstrap against config.json (source of truth) and populates
	// the full Identity block on the state. peer_registry.LocalDaemonID
	// will match because Bootstrap is idempotent — both reads resolve to
	// the same ULID in config.json.
	st, err := state.NewState(thrumDir, syncDir, repoID, "")
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

	// EnsureEveryoneGroup removed — @everyone is now a direct broadcast
	// (scope_type='broadcast'), not a group. Fixes cross-repo sync leak.

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
				LogLevel:     config.DefaultLogLevel,
			},
		}
	}

	// Configure slog with the resolved log level so any subsequent calls
	// to slog.Info/Debug/Warn/Error respect the user's configured threshold.
	// Log.Printf calls continue to write unconditionally through the
	// lumberjack writer for backward compatibility.
	daemon.ConfigureSlog(logWriter, thrumCfg.Daemon.LogLevel)
	log.Printf("daemon: log level=%s", thrumCfg.Daemon.LogLevel)

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

	// Resolve the project name once — used for the "Repo:" display
	// line in nudge bodies via permission.New below.
	projectName := permission.ResolveProjectName(thrumCfg, absPath)

	// Synthesize the virtual supervisor pseudo-agent. Pure resolvers;
	// never persisted to disk. Legacy identity files (from pre-v0.8.3
	// installs that wrote supervisor_<project>.json) are swept here so
	// the daemon presents a single source of truth for its identity set.
	supervisorID := permission.ResolveSupervisorID(thrumCfg, absPath)
	legacySupervisorID := permission.ResolveLegacySupervisorID(thrumCfg, absPath)
	supervisorIdentity := permission.SupervisorIdentity(thrumCfg, absPath)
	permission.CleanupLegacySupervisorFiles(thrumDir)
	log.Printf("daemon: supervisor virtual identity @%s (legacy compat @%s)", supervisorID, legacySupervisorID)

	// Construct the permission package. The reply interceptor (Task
	// 6.2) is wired into the event-write hook further below; the
	// tmux check-pane dispatch (Task 7.1) is wired into the
	// TmuxHandler via SetPermission further below.
	permPkg := permission.New(st, st.RawDB(), supervisorID, projectName, thrumDir)

	// Log the count of non-expired pending nudges for operator
	// visibility. No in-memory rehydration is needed — OnDetection
	// re-reads the permission_nudges table on every check-pane fire,
	// so reminders resume at the correct cadence automatically after
	// a daemon restart. This call just gives operators a
	// "how many nudges were in flight when we bounced?" breadcrumb.
	if rows, reloadErr := permPkg.ReloadOnBoot(context.Background()); reloadErr != nil {
		log.Printf("daemon: permission reload on boot failed: %v", reloadErr)
	} else if len(rows) > 0 {
		log.Printf("daemon: permission found %d pending nudge(s) still in flight", len(rows))
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
		// Route synced events through State.IngestSyncedEvent so the
		// event-write hook fires on cross-repo ingest, not just local
		// writes. Without this, replies arriving via sync from a peer
		// repo never reach the permission reply interceptor.
		syncLoop.SetIngester(st)
		if err := syncLoop.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to start sync loop: %v\n", err)
		} else {
			defer func() { _ = syncLoop.Stop() }()
		}
	}

	// Create Unix socket server
	socketPath := filepath.Join(varDir, "thrum.sock")
	server := daemon.NewServer(socketPath)

	// Wire the peer-credential identity resolver into the server. The
	// resolver consults agent_work_contexts to map connecting PIDs → CWD →
	// registered-agent worktrees, giving the daemon a kernel-verified caller
	// identity on every unix-socket request. This replaces the old
	// client-asserted CallerAgentID trust model and is the core of the v0.9.0
	// security hardening (thrum-u4xv.3).
	identityLister := daemon.NewDaemonAgentLister(st)
	identityResolver := peercred.NewResolver(identityLister)
	server.SetIdentityResolver(identityResolver)

	// Create subscription dispatcher
	dispatcher := subscriptions.NewDispatcher(st.DB())

	// Register RPC handlers
	startTime := time.Now()
	version := Version + "+" + Build

	// Health check
	healthHandler := rpc.NewHealthHandler(startTime, version, repoID)
	server.RegisterHandler("health", healthHandler.Handle)
	healthHandler.SetIdentityProvider(func() *rpc.IdentityInfo {
		ident := st.Identity()
		if ident.DaemonID == "" {
			return nil
		}
		return &rpc.IdentityInfo{
			DaemonID:     ident.DaemonID,
			RepoName:     ident.RepoName,
			Hostname:     ident.Hostname,
			RepoPath:     ident.RepoPath,
			GitOriginURL: ident.GitOriginURL,
			InitAt:       ident.InitAt,
		}
	})

	// Agent management
	agentHandler := rpc.NewAgentHandler(st)
	server.RegisterHandler("agent.register", agentHandler.HandleRegister)
	server.RegisterHandler("agent.list", agentHandler.HandleList)
	server.RegisterHandler("agent.whoami", agentHandler.HandleWhoami)
	server.RegisterHandler("agent.listContext", agentHandler.HandleListContext)
	server.RegisterHandler("agent.delete", agentHandler.HandleDelete)
	server.RegisterHandler("agent.cleanup", agentHandler.HandleCleanup)
	server.RegisterHandler("agent.set-status", agentHandler.HandleSetAgentStatus)

	// Team management
	teamHandler := rpc.NewTeamHandler(st, thrumDir, supervisorIdentity)
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
	messageHandler := rpc.NewMessageHandlerWithDispatcher(st, dispatcher, thrumDir, supervisorID, legacySupervisorID)
	server.RegisterHandler("message.send", messageHandler.HandleSend)
	server.RegisterHandler("message.get", messageHandler.HandleGet)
	server.RegisterHandler("message.list", messageHandler.HandleList)
	server.RegisterHandler("message.outbox", messageHandler.HandleOutbox)
	server.RegisterHandler("message.delete", messageHandler.HandleDelete)
	server.RegisterHandler("message.edit", messageHandler.HandleEdit)
	server.RegisterHandler("message.markRead", messageHandler.HandleMarkRead)
	server.RegisterHandler("message.deleteByScope", messageHandler.HandleDeleteByScope)
	server.RegisterHandler("message.deleteByAgent", messageHandler.HandleDeleteByAgent)
	server.RegisterHandler("message.archive", messageHandler.HandleArchive)

	// Monitor jobs — SECURITY: these handlers spawn child processes with the
	// daemon's privileges, so they are registered on the unix-socket `server`
	// ONLY and NEVER on the WebSocket / peer transport. The trust boundary
	// test at internal/daemon/rpc/monitor_trust_boundary_test.go scans this
	// file and will fail CI if a monitor.* method is ever registered on the
	// WebSocket registry. See dev-docs/specs/2026-04-11-monitor-jobs-design.md
	// §"Trust boundary".
	monitorStore := monitor.NewMonitorStore(st.DB())
	monitorDelivery := monitor.NewDelivery(messageHandler)
	monitorSupervisor := monitor.NewMonitorSupervisor(monitorStore, monitorDelivery)
	monitorHandler := rpc.NewMonitorHandler(monitorSupervisor, monitorStore, st)
	server.RegisterHandler("monitor.start", monitorHandler.HandleStart)
	server.RegisterHandler("monitor.stop", monitorHandler.HandleStop)
	server.RegisterHandler("monitor.list", monitorHandler.HandleList)
	server.RegisterHandler("monitor.show", monitorHandler.HandleShow)
	server.RegisterHandler("monitor.restart", monitorHandler.HandleRestart)
	server.RegisterHandler("monitor.logs", monitorHandler.HandleLogs)

	// Subscription management
	// Subscribe/unsubscribe RPC handlers removed — CLI subscribe commands deleted.
	// Subscription dispatcher kept for push notification infrastructure.

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
	var syncManager *daemon.DaemonSyncManager
	if peerRegistry != nil {
		syncManager = daemon.NewDaemonSyncManager(st, peerRegistry)
	}

	var pairingMgr *daemon.PairingManager
	var tsLocalAddr string           // set when Tailscale listener starts
	var tsnetMu sync.Mutex           // protects tsLocalAddr and tsnetStarted
	var startTsnetFn func(int) error // lazy tsnet start, assigned later
	// wsPort is resolved later (line ~5631) but the peer.start_pairing /
	// peer.join closures need to read it at RPC time for the --type local
	// loopback peercode. Declared here so the closures capture it by
	// reference; the assignment happens before any peer RPC can fire (the
	// closures are wired into the daemon server below, which itself only
	// starts accepting connections once the WS server is up).
	var wsPort string
	// ensureNetworkListenerFn is wired below once wsServer is built. It
	// lazily binds an additional WS listener on a user-supplied LAN IP so
	// `--type network` peers (xir.27 sub-2) can dial a non-loopback address
	// without rebinding the main wsServer to 0.0.0.0. Returns the bound
	// "ip:port" address string. Per-IP idempotent: subsequent calls with
	// the same IP return the existing listener's port.
	var ensureNetworkListenerFn func(addrIP string) (string, error)
	hostname, _ := os.Hostname()

	getTsLocalAddr := func() string {
		tsnetMu.Lock()
		defer tsnetMu.Unlock()
		return tsLocalAddr
	}

	// Register the single event-write hook. *state.State exposes
	// exactly one hook slot (by design — a multi-slot registry was
	// considered and rejected as API-surface bloat for this feature),
	// so both the sync-notify broadcast and the permission reply
	// interceptor share this closure. If a third consumer ever needs
	// to hang off the same hook, either extend State with a slice of
	// callbacks or keep composing here.
	//
	//   - sync notify: fires on every local event write, dispatched
	//     to a background goroutine (fire-and-forget) so the
	//     BroadcastNotify RPC fanout does not block the writer.
	//   - permission intercept: filters for message.create events,
	//     unmarshals them, and dispatches to permPkg.AfterMessageCreate
	//     to resolve reply_to refs into approve/deny keystrokes.
	//
	// IMPORTANT: the EventWriteHook contract is "called synchronously
	// but should not block" (state.go:25). Both branches here must
	// yield quickly — AfterMessageCreate does a DB lookup plus a
	// tmux subprocess exec on the happy path, so it MUST run on its
	// own goroutine with a fresh context and a panic recover. Without
	// the recover, a bug in the reply dispatcher could take down the
	// whole event pipeline via a writer-goroutine panic.
	//
	// This fires on LOCAL writes only. The cross-repo path (events
	// arriving via sync ingest) is bridged through IngestSyncedEvent
	// in Task 6.3, which fires the same hook.
	st.SetOnEventWrite(func(daemonID string, sequence int64, event []byte) {
		if syncManager != nil {
			go syncManager.BroadcastNotify(daemonID, sequence, 1)
		}
		// Cheap type-only unmarshal to filter non-message events
		// BEFORE the larger MessageCreateEvent decode. The double
		// unmarshal is intentional: the head check short-circuits
		// hot paths (agent.register, session.start, etc.) without
		// building a full MessageCreateEvent that would be
		// immediately discarded. Do NOT "optimize" these into a
		// single decode without verifying the non-message traffic
		// volume on a busy daemon.
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(event, &head); err != nil {
			return
		}
		if head.Type != "message.create" {
			return
		}
		var evt types.MessageCreateEvent
		if err := json.Unmarshal(event, &evt); err != nil {
			return
		}
		// Dispatch off the writer goroutine with a fresh context
		// (the caller's ctx may be canceled by the time this runs)
		// and a panic recover so a reply-dispatcher bug can't crash
		// the event pipeline. evt is already a value copy — safe
		// to capture.
		go func(evt types.MessageCreateEvent) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("[permission] intercept panic", "panic", r)
				}
			}()
			permPkg.AfterMessageCreate(context.Background(), evt)
		}(evt)

		// thrum-wvpv: nudge tmux-managed recipients. This branch fires for
		// BOTH local writes (HandleSend) and synced writes (sync_apply →
		// State.WriteEvent), giving cross-machine and cross-repo recipients
		// the same tmux pane notification that local recipients used to
		// get exclusively. nudge.DispatchTmux is fire-and-forget; failures
		// are intentionally swallowed because nudges are advisory.
		nudge.DispatchTmux(thrumDir, evt.Recipients, evt.AgentID)

		// hook-inbox-delivery: write a spool file for every LOCAL recipient.
		// "Local" means the recipient has an identity file reachable from
		// this daemon (matching the implicit rule in nudge.DispatchTmux —
		// cross-machine recipients are a no-op because their identity file
		// isn't on this daemon's disk). The agent-side check-inbox hook
		// reads the spool and decides whether to surface a nudge (tmux dead)
		// or silently consume (tmux alive — tmux path already handled it).
		//
		// Dispatched on its own goroutine (same async pattern as
		// nudge.DispatchTmux) so the SetOnEventWrite writer goroutine
		// doesn't block on the per-recipient git-worktree walk inside
		// HasLocalIdentity. The hook contract is "synchronous but must not
		// block" — a git subprocess per recipient on the hot write path
		// violates that on busy daemons.
		go func(evt types.MessageCreateEvent) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("[inbox] spool dispatch panic", "panic", r)
				}
			}()
			for _, recipient := range evt.Recipients {
				if !nudge.HasLocalIdentity(thrumDir, recipient) {
					continue
				}
				env := inbox.Envelope{
					MsgID:      evt.MessageID,
					From:       "@" + evt.AgentID,
					ReceivedAt: time.Now().UTC(),
				}
				if err := inbox.WriteSpool(thrumDir, recipient, env); err != nil {
					slog.Warn("[inbox] spool write failed",
						"agent", recipient, "msg_id", evt.MessageID, "err", err)
					// Continue — DB is authoritative; cron backstop surfaces
					// unread messages regardless of spool state.
				}
			}
		}(evt)
	})

	// pairHandler is created once, registered on multiple WS registries:
	//   - syncRegistry (tsnet listener) for cross-host Tailscale pairing
	//   - wsRegistry (localhost listener) for same-host loopback pairing
	//     under --type local (xir.27 sub-component 1)
	var pairHandler *rpc.PairRequestHandler
	var repairHandler *rpc.PeerRepairHandler

	if syncManager != nil {
		// Create pairing manager (used by both Unix socket and Tailscale handlers)
		pairingMgr = daemon.NewPairingManager(syncManager.PeerRegistry(), st.Identity(), hostname)

		// Build the shared pair.request handler.
		pairHandler = rpc.NewPairRequestHandler(func(
			code, peerDaemonID, peerName, peerAddress string,
			peerRepoName, peerHostname, peerRepoPath, peerGitOriginURL string,
		) (string, string, string, string, string, string, string, error) {
			token, local, err := pairingMgr.HandlePairRequest(code, daemon.PairMetadata{
				DaemonID:     peerDaemonID,
				Name:         peerName,
				Address:      peerAddress,
				RepoName:     peerRepoName,
				Hostname:     peerHostname,
				RepoPath:     peerRepoPath,
				GitOriginURL: peerGitOriginURL,
			})
			return token, local.DaemonID, local.Name, local.RepoName, local.Hostname, local.RepoPath, local.GitOriginURL, err
		})

		// xir.27 sub-4: build the peer.repair manager + handler (dedicated
		// RPC; intentionally separate from pair.request because verify-
		// stored-token and mint-from-code have opposite trust models).
		// repairMgr is captured by the handler closure — no need for a
		// package-scoped var.
		repairMgr := daemon.NewPeerRepairManager(syncManager.PeerRegistry(), st.Identity(), hostname)
		repairHandler = rpc.NewPeerRepairHandler(func(
			token, dialerDaemonID, dialerName, dialerAddress string,
			dialerRepoName, dialerHostname, dialerRepoPath, dialerGitOriginURL string,
		) (string, string, string, string, string, string, error) {
			local, err := repairMgr.HandleRepairRequest(token, daemon.PairMetadata{
				DaemonID:     dialerDaemonID,
				Name:         dialerName,
				Address:      dialerAddress,
				RepoName:     dialerRepoName,
				Hostname:     dialerHostname,
				RepoPath:     dialerRepoPath,
				GitOriginURL: dialerGitOriginURL,
			})
			return local.DaemonID, local.Name, local.RepoName, local.Hostname, local.RepoPath, local.GitOriginURL, err
		})

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
		// Wrap to ensure port is selected before pairing starts.
		startPairingFn := func(timeout time.Duration, peerType, addressHint string) (string, string, string, error) {
			// xir.27: dispatch on the user-selected transport. An empty Type
			// preserves the legacy implicit-tailscale path for any caller
			// invoking the RPC directly without going through the new CLI
			// surface (which would have errored earlier on missing --type).
			switch peerType {
			case "", "tailscale":
				if peerRegistry.LocalPort() == 0 {
					if p := os.Getenv("THRUM_TS_PORT"); p != "" {
						if port, err := strconv.Atoi(p); err == nil {
							if err := peerRegistry.SetLocalPort(port); err != nil {
								return "", "", "", fmt.Errorf("set local port from env: %w", err)
							}
						}
					} else {
						port, err := daemon.FindRandomAvailablePort(daemon.TsnetPortRangeMin, daemon.TsnetPortRangeMax)
						if err != nil {
							return "", "", "", fmt.Errorf("find available tsnet port: %w", err)
						}
						if err := peerRegistry.SetLocalPort(port); err != nil {
							return "", "", "", fmt.Errorf("set local port: %w", err)
						}
					}
				}
				if startTsnetFn != nil {
					if err := startTsnetFn(peerRegistry.LocalPort()); err != nil {
						return "", "", "", fmt.Errorf("start tailscale for peer add: %w", err)
					}
				}
				code, err := pairingMgr.StartPairing(timeout)
				return code, getTsLocalAddr(), "tailscale", err

			case "local":
				// xir.27 sub-1: emit a loopback peercode anchored at the
				// daemon's own WS port. The peer.join side dials
				// ws://127.0.0.1:<wsPort>/ws?pairing_code=<code> directly,
				// hitting pair.request on wsRegistry — no tsnet bring-up.
				if wsPort == "" {
					return "", "", "", fmt.Errorf("--type local: daemon ws port not yet resolved")
				}
				code, err := pairingMgr.StartPairing(timeout)
				if err != nil {
					return "", "", "", err
				}
				return code, net.JoinHostPort("127.0.0.1", wsPort), "local", nil

			case "network":
				// xir.27 sub-2: anchor the peercode at the user-supplied
				// LAN IP. The daemon validates that the IP is assigned to a
				// local NIC eligible for direct-TCP peer transport (filters
				// loopback / link-local / tsnet / docker / utun / etc.) via
				// internal/netdetect, then binds a SECONDARY WS listener on
				// addressHint:<port>. Per coordinator: scoped second listener
				// (NOT a 0.0.0.0 rebind) keeps blast radius narrow.
				if strings.TrimSpace(addressHint) == "" {
					return "", "", "", fmt.Errorf("--type network requires --address <ip>")
				}
				ip := net.ParseIP(strings.TrimSpace(addressHint))
				if ip == nil {
					return "", "", "", fmt.Errorf("--type network --address %q: not a valid IP", addressHint)
				}
				if _, err := netdetect.SubnetForLocalAddress(ip); err != nil {
					return "", "", "", fmt.Errorf("--type network --address %s: %w", addressHint, err)
				}
				if ensureNetworkListenerFn == nil {
					return "", "", "", fmt.Errorf("--type network: secondary listener helper not initialized")
				}
				bound, err := ensureNetworkListenerFn(ip.String())
				if err != nil {
					return "", "", "", fmt.Errorf("--type network: bind listener on %s: %w", ip, err)
				}
				code, err := pairingMgr.StartPairing(timeout)
				if err != nil {
					return "", "", "", err
				}
				return code, bound, "network", nil

			case "repair":
				return "", "", "", fmt.Errorf("--type repair is not valid for peer add (use 'thrum peer join --type repair <name>')")

			default:
				return "", "", "", fmt.Errorf("unknown peer type %q", peerType)
			}
		}
		server.RegisterHandler("peer.start_pairing",
			rpc.NewPeerStartPairingHandler(startPairingFn).Handle)

		// peer.wait_pairing — block until pairing completes or times out
		waitFn := func(ctx context.Context) (peerName, peerAddr, peerDaemonID string, err error) {
			result, err := pairingMgr.WaitForPairing(ctx)
			if err != nil {
				return "", "", "", err
			}
			return result.PeerName, result.PeerAddress, result.PeerDaemonID, nil
		}
		server.RegisterLongPollHandler("peer.wait_pairing",
			rpc.NewPeerWaitPairingHandler(waitFn).Handle)

		// peer.join — dispatch on the user-selected transport (xir.27).
		joinFn := func(peerAddr, code, repoPath, peerType, peerName, localAddress string) (string, string, error) {
			switch peerType {
			case "", "tailscale":
				if startTsnetFn != nil && getTsLocalAddr() == "" {
					if peerRegistry.LocalPort() == 0 {
						port, portErr := daemon.FindRandomAvailablePort(daemon.TsnetPortRangeMin, daemon.TsnetPortRangeMax)
						if portErr != nil {
							return "", "", fmt.Errorf("find available tsnet port: %w", portErr)
						}
						if portErr := peerRegistry.SetLocalPort(port); portErr != nil {
							return "", "", fmt.Errorf("set local port: %w", portErr)
						}
					}
					if tsErr := startTsnetFn(peerRegistry.LocalPort()); tsErr != nil {
						return "", "", fmt.Errorf("start tailscale for peer join: %w", tsErr)
					}
				}
				localAddr := getTsLocalAddr()
				if localAddr == "" {
					return "", "", fmt.Errorf("tailscale not configured or not started")
				}
				localIdent := st.Identity()
				localMeta := daemon.PairMetadata{
					DaemonID:     localIdent.DaemonID,
					Name:         hostname,
					Address:      localAddr,
					RepoName:     localIdent.RepoName,
					Hostname:     localIdent.Hostname,
					RepoPath:     localIdent.RepoPath,
					GitOriginURL: localIdent.GitOriginURL,
				}
				peer, err := syncManager.JoinPeer(peerAddr, code, localMeta)
				if err != nil {
					return "", "", err
				}
				peer.Role = "dialer"
				if repoPath != "" {
					peer.RepoPath = repoPath
					peer.Transport = "local"
				} else {
					peer.Transport = daemon.DetectTransport(peerAddr)
				}
				if updateErr := peerRegistry.AddPeer(peer); updateErr != nil {
					fmt.Fprintf(os.Stderr, "[peer.join] warning: failed to update peer transport/role: %v\n", updateErr)
				}
				return peer.Name, peer.DaemonID, nil

			case "local":
				// xir.27 sub-1: dial the loopback peercode address directly.
				// No tsnet — RequestPairing builds ws://<peerAddr>/ws?pairing_code=
				// which routes to wsRegistry's pair.request via the localhost
				// WS server on the listener side. localMeta.Address advertises
				// our own loopback WS so post-pair sync (sync.notify) reaches
				// us back on the right port.
				if wsPort == "" {
					return "", "", fmt.Errorf("--type local: daemon ws port not yet resolved")
				}
				localAddr := net.JoinHostPort("127.0.0.1", wsPort)
				localIdent := st.Identity()
				localMeta := daemon.PairMetadata{
					DaemonID:     localIdent.DaemonID,
					Name:         hostname,
					Address:      localAddr,
					RepoName:     localIdent.RepoName,
					Hostname:     localIdent.Hostname,
					RepoPath:     localIdent.RepoPath,
					GitOriginURL: localIdent.GitOriginURL,
				}
				peer, err := syncManager.JoinPeer(peerAddr, code, localMeta)
				if err != nil {
					return "", "", err
				}
				peer.Role = "dialer"
				peer.Transport = "local"
				if repoPath != "" {
					peer.RepoPath = repoPath
				}
				if updateErr := peerRegistry.AddPeer(peer); updateErr != nil {
					fmt.Fprintf(os.Stderr, "[peer.join] warning: failed to update peer transport/role: %v\n", updateErr)
				}
				return peer.Name, peer.DaemonID, nil

			case "network":
				// xir.27 sub-2: dial the peercode address directly (no tsnet),
				// and bind a SECONDARY WS listener on this daemon's user-
				// supplied --address so the listener-side daemon can reach
				// us back for post-pair sync.notify. Symmetric to the add
				// side: both sides must explicitly opt into a LAN address.
				if strings.TrimSpace(localAddress) == "" {
					return "", "", fmt.Errorf("--type network requires --address <ip> on this side too (the LAN IP this daemon should bind for sync reach-back)")
				}
				ip := net.ParseIP(strings.TrimSpace(localAddress))
				if ip == nil {
					return "", "", fmt.Errorf("--type network --address %q: not a valid IP", localAddress)
				}
				if _, err := netdetect.SubnetForLocalAddress(ip); err != nil {
					return "", "", fmt.Errorf("--type network --address %s: %w", localAddress, err)
				}
				if ensureNetworkListenerFn == nil {
					return "", "", fmt.Errorf("--type network: secondary listener helper not initialized")
				}
				localAddr, err := ensureNetworkListenerFn(ip.String())
				if err != nil {
					return "", "", fmt.Errorf("--type network: bind listener on %s: %w", ip, err)
				}
				localIdent := st.Identity()
				localMeta := daemon.PairMetadata{
					DaemonID:     localIdent.DaemonID,
					Name:         hostname,
					Address:      localAddr,
					RepoName:     localIdent.RepoName,
					Hostname:     localIdent.Hostname,
					RepoPath:     localIdent.RepoPath,
					GitOriginURL: localIdent.GitOriginURL,
				}
				peer, err := syncManager.JoinPeer(peerAddr, code, localMeta)
				if err != nil {
					return "", "", err
				}
				peer.Role = "dialer"
				peer.Transport = "network"
				if updateErr := peerRegistry.AddPeer(peer); updateErr != nil {
					fmt.Fprintf(os.Stderr, "[peer.join] warning: failed to update peer transport/role: %v\n", updateErr)
				}
				return peer.Name, peer.DaemonID, nil

			case "repair":
				// xir.27 sub-4: reconcile an existing peer entry using its
				// stored Token as the trust anchor. peerName comes from the
				// CLI (positional arg or --peer-name). We look up the
				// entry, dial via its stored Transport/Address, send
				// peer.repair with the stored token + our current
				// identity, then refresh the entry with the listener's
				// returned metadata.
				name := strings.TrimSpace(peerName)
				if name == "" {
					return "", "", fmt.Errorf("--type repair requires a peer name")
				}
				existing := peerRegistry.FindPeerByName(name)
				if existing == nil {
					return "", "", fmt.Errorf("--type repair: no peer named %q in peers.json", name)
				}
				if existing.Token == "" {
					return "", "", fmt.Errorf("--type repair: peer %q has no stored token (directory-only entry cannot be repaired)", name)
				}
				if existing.Address == "" {
					return "", "", fmt.Errorf("--type repair: peer %q has no stored address", name)
				}

				// Tailscale repair: bring up tsnet if not already started.
				// Symmetric to the JoinPeer path so a dialer coming out of
				// a cold start can still reconcile a tailscale peer.
				if existing.Transport == "tailscale" && startTsnetFn != nil && getTsLocalAddr() == "" {
					if peerRegistry.LocalPort() == 0 {
						port, portErr := daemon.FindRandomAvailablePort(daemon.TsnetPortRangeMin, daemon.TsnetPortRangeMax)
						if portErr != nil {
							return "", "", fmt.Errorf("--type repair: find available tsnet port: %w", portErr)
						}
						if portErr := peerRegistry.SetLocalPort(port); portErr != nil {
							return "", "", fmt.Errorf("--type repair: set local port: %w", portErr)
						}
					}
					if tsErr := startTsnetFn(peerRegistry.LocalPort()); tsErr != nil {
						return "", "", fmt.Errorf("--type repair: start tailscale: %w", tsErr)
					}
				}

				localIdent := st.Identity()
				localAddr := ""
				switch existing.Transport {
				case "tailscale":
					localAddr = getTsLocalAddr()
				case "local":
					if wsPort != "" {
						localAddr = net.JoinHostPort("127.0.0.1", wsPort)
					}
				case "network":
					// For network repair the local address comes from the
					// secondary listener. The dialer may need --address to
					// rebind an ephemeral listener; if unset, we advertise
					// whatever the existing entry carries (best-effort).
					localAddr = existing.Address
				}
				localMeta := daemon.PairMetadata{
					DaemonID:     localIdent.DaemonID,
					Name:         hostname,
					Address:      localAddr,
					RepoName:     localIdent.RepoName,
					Hostname:     localIdent.Hostname,
					RepoPath:     localIdent.RepoPath,
					GitOriginURL: localIdent.GitOriginURL,
				}

				client := daemon.NewSyncClient()
				result, err := client.RequestRepair(existing.Address, existing.Token, localMeta)
				if err != nil {
					return "", "", fmt.Errorf("--type repair: %w", err)
				}
				if result.Status != "repaired" {
					return "", "", fmt.Errorf("--type repair: unexpected status %q", result.Status)
				}

				// Refresh local entry with the listener's current metadata.
				// If the listener's daemon_id rotated, the entry's key
				// changes too: RemovePeer the old key first so both sides
				// settle on the same DaemonID.
				refreshed := *existing
				oldKey := refreshed.DaemonID
				refreshed.DaemonID = result.DaemonID
				if result.RepoName != "" {
					refreshed.RemoteRepoName = result.RepoName
				}
				if result.Hostname != "" {
					refreshed.RemoteHostname = result.Hostname
				}
				if result.RepoPath != "" {
					refreshed.RemoteRepoPath = result.RepoPath
				}
				if result.GitOriginURL != "" {
					refreshed.RemoteGitOriginURL = result.GitOriginURL
				}
				if oldKey != result.DaemonID && oldKey != "" {
					if rmErr := peerRegistry.RemovePeer(oldKey); rmErr != nil {
						fmt.Fprintf(os.Stderr, "[peer.join] warning: failed to remove stale peer entry %s: %v\n", oldKey, rmErr)
					}
				}
				if addErr := peerRegistry.AddPeer(&refreshed); addErr != nil {
					return "", "", fmt.Errorf("--type repair: update local entry: %w", addErr)
				}
				return refreshed.Name, refreshed.DaemonID, nil

			default:
				return "", "", fmt.Errorf("unknown peer type %q", peerType)
			}
		}
		server.RegisterHandler("peer.join",
			rpc.NewPeerJoinHandler(joinFn).Handle)

		// peer.list — compact peer list. xir.29: propagate ReconcileStatus
		// so the CLI can render drift markers.
		peerListFn := func() []rpc.PeerListEntry {
			infos := syncManager.ListPeers()
			entries := make([]rpc.PeerListEntry, len(infos))
			for i, p := range infos {
				entries[i] = rpc.PeerListEntry{
					DaemonID:        p.DaemonID,
					Name:            p.Name,
					Address:         p.Address,
					LastSync:        p.LastSync,
					LastSeq:         p.LastSeq,
					ReconcileStatus: p.ReconcileStatus,
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

		// peer.configure — add/remove proxy agents for a peer
		peerConfigureHandler := rpc.NewPeerConfigureHandler(
			peerRegistry.AddRemoteAgent,
			peerRegistry.RemoveRemoteAgent,
		)
		server.RegisterHandler("peer.configure", peerConfigureHandler.Handle)

		// peer.address_changed — receive address change notifications from peers.
		// xir.29: wrap with a SubnetGuard so cross-subnet address changes
		// are rejected (they indicate a topology shift, not a same-network
		// reshuffle) and escalated to manual `thrum peer join --type repair`.
		// Transport=="local" skips the guard entirely — loopback peers
		// are strictly stronger than same-subnet (I6 review finding).
		addrChangedUpdate := func(peerToken, newIP, newPort string) error {
			p := peerRegistry.FindPeerByToken(peerToken)
			if p == nil {
				return fmt.Errorf("unknown peer token")
			}
			newAddr := net.JoinHostPort(newIP, newPort)
			if err := bridgepeer.ValidateAddressChange(p.Transport, p.Address, newAddr); err != nil {
				return err
			}
			p.Address = newAddr
			return peerRegistry.AddPeer(p)
		}
		subnetGuard := func(transport, oldAddr, newAddr string) error {
			// Local transport: same-host is a stronger property than
			// same-subnet; no check needed (coordinator 2026-04-19 +
			// I6 review finding).
			if transport == "local" {
				return nil
			}
			// Empty oldAddr means "no cached address" (first-boot or
			// lookup-disabled path). Cannot evaluate — accept per M11.
			if oldAddr == "" {
				return nil
			}
			oldIPStr, _, err := net.SplitHostPort(oldAddr)
			if err != nil {
				return nil
			}
			newIPStr, _, err := net.SplitHostPort(newAddr)
			if err != nil {
				return fmt.Errorf("invalid new address %q: %w", newAddr, err)
			}
			oldIP := net.ParseIP(oldIPStr)
			newIP := net.ParseIP(newIPStr)
			if oldIP == nil || newIP == nil {
				return nil
			}
			subnet, err := netdetect.SubnetForLocalAddress(oldIP)
			if err != nil {
				// Old address no longer on any local NIC; cannot
				// evaluate subnet equality → accept (the peer has
				// already moved off this host's LAN).
				return nil
			}
			if !netdetect.SameSubnet(oldIP, newIP, subnet.CIDR) {
				return fmt.Errorf("peer moved from %s to %s (different subnets)",
					oldIP, newIP)
			}
			return nil
		}
		lookupPeer := func(token string) (oldAddr, transport string, err error) {
			p := peerRegistry.FindPeerByToken(token)
			if p == nil {
				return "", "", fmt.Errorf("unknown peer token")
			}
			return p.Address, p.Transport, nil
		}
		addressChangedHandler := rpc.NewPeerAddressChangedHandlerWithGuard(
			addrChangedUpdate, subnetGuard, lookupPeer)
		server.RegisterHandler("peer.address_changed", addressChangedHandler.Handle)
	}

	// User management (for WebSocket connections)
	userHandler := rpc.NewUserHandler(st)
	server.RegisterHandler("user.register", userHandler.HandleRegister)
	server.RegisterHandler("user.identify", userHandler.HandleIdentify)

	// Purge
	purgeHandler := rpc.NewPurgeHandler(st)
	server.RegisterHandler("purge.execute", purgeHandler.Handle)

	// Resolve WS port: env var > config.json > default ("auto" = find free port)
	wsPort = os.Getenv("THRUM_WS_PORT")
	if wsPort == "" {
		wsPort = thrumCfg.Daemon.WSPort
	}
	if wsPort == "" || wsPort == "auto" {
		// Try to reuse the previous port so the URL stays stable across restarts
		if prevPort := cli.ReadWebSocketPort(absPath); prevPort > 0 {
			prevPortStr := strconv.Itoa(prevPort)
			listener, listenErr := net.Listen("tcp", "localhost:"+prevPortStr)
			if listenErr == nil {
				// Previous port is available — reuse it
				_ = listener.Close()
				wsPort = prevPortStr
			}
		}

		// If no previous port or it's unavailable, find a free one
		if wsPort == "" || wsPort == "auto" {
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
	wsRegistry.Register("agent.delete", websocket.Handler(agentHandler.HandleDelete))
	wsRegistry.Register("agent.cleanup", websocket.Handler(agentHandler.HandleCleanup))
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
	wsRegistry.Register("message.outbox", websocket.Handler(messageHandler.HandleOutbox))
	wsRegistry.Register("message.delete", websocket.Handler(messageHandler.HandleDelete))
	wsRegistry.Register("message.edit", websocket.Handler(messageHandler.HandleEdit))
	wsRegistry.Register("message.markRead", websocket.Handler(messageHandler.HandleMarkRead))
	// SECURITY (sec.8): message.deleteByAgent and message.deleteByScope are
	// NOT registered on the WS transport. They are admin/system operations
	// restricted to daemon-internal callers (sec.8). The WS transport has no
	// peercred injection, so the daemon-internal check would be bypassed —
	// any localhost browser page could invoke bulk hard-deletes.
	// See internal/daemon/rpc/monitor_trust_boundary_test.go for the
	// structural guard pattern that enforces this on the monitor.* handlers.
	wsRegistry.Register("message.archive", websocket.Handler(messageHandler.HandleArchive))
	// Subscribe/unsubscribe WS handlers removed — CLI subscribe commands deleted.
	wsRegistry.Register("user.register", websocket.Handler(userHandler.HandleRegister))
	wsRegistry.Register("user.identify", websocket.Handler(userHandler.HandleIdentify))
	if syncLoop != nil {
		wsRegistry.Register("sync.force", websocket.Handler(syncForceHandler.Handle))
		wsRegistry.Register("sync.status", websocket.Handler(syncStatusHandler.Handle))
	}

	// xir.27 sub-1: pair.request on the localhost WS so --type local peers
	// can complete the handshake without going through tsnet. Same handler
	// instance as the tsnet-side registration; the pairing-code-active gate
	// in WithPairingValidator below ensures only an active pairing session
	// accepts pair-code connections, mirroring the tsnet-side gate.
	if pairHandler != nil {
		wsRegistry.Register("pair.request", websocket.Handler(pairHandler.Handle))
	}

	// xir.27 sub-4: peer.repair on the localhost WS so --type repair flows
	// for local/network peers complete without requiring tsnet. The RPC is
	// token-authenticated via Bearer header (existing post-pair auth path);
	// no pairing-code validator is required because repair reuses the
	// trust anchor established during the original pair.
	if repairHandler != nil {
		wsRegistry.Register("peer.repair", websocket.Handler(repairHandler.Handle))
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

	// Create PeerManager before the WS server so we can wire the accept handler.
	var peerManager *daemon.PeerManager
	var wsOpts []websocket.ServerOption
	if peerRegistry != nil {
		peerManager = daemon.NewPeerManager(peerRegistry, wsPort, nil)
		defer peerManager.StopAll()
		wsOpts = append(wsOpts, websocket.WithPeerAcceptHandler(func(token string) {
			p := peerRegistry.FindPeerByToken(token)
			if p != nil {
				peerManager.AcceptPeer(ctx, p)
			}
		}))
	}

	// xir.27 sub-1: localhost WS accepts pair-code connections (?pairing_code=)
	// without a token while a pairing session is active. Same gate semantics
	// as the tsnet-side validator. Without this, --type local peer.join calls
	// would be rejected at the WS handshake before ever reaching the
	// pair.request handler.
	if pairingMgr != nil {
		wsOpts = append(wsOpts, websocket.WithPairingValidator(func(code string) bool {
			return pairingMgr.HasActiveSession()
		}))
	}

	wsServer := websocket.NewServer(wsAddr, wsRegistry, uiFS, wsOpts...)

	// xir.27 sub-2: lazy per-IP secondary WS listener for --type network.
	// Reuses wsServer.HTTPHandler() so all RPC handlers + the pairing /
	// peer-accept gates are identical to the localhost listener; only the
	// bind address differs. Per-IP idempotent — multiple --type network
	// pairs anchored at the same LAN IP share one listener.
	//
	// NOT a 0.0.0.0 rebind by design: the user explicitly types the LAN IP
	// they want to expose; binding to that specific IP keeps the blast
	// radius narrow and auditable. Other interfaces on the host (vpn,
	// docker, secondary NICs) stay invisible from this daemon's pairing
	// surface unless the user opts each one in.
	var (
		networkListenersMu sync.Mutex
		networkListeners   = map[string]string{} // ip → "ip:port"
	)
	ensureNetworkListenerFn = func(addrIP string) (string, error) {
		networkListenersMu.Lock()
		defer networkListenersMu.Unlock()
		if existing, ok := networkListeners[addrIP]; ok {
			return existing, nil
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(addrIP, "0"))
		if err != nil {
			return "", fmt.Errorf("listen on %s: %w", addrIP, err)
		}
		bound := ln.Addr().String()
		// Serve the same handler the main wsServer uses so all RPCs +
		// validators are reachable on the LAN listener too.
		go func() { // #nosec G114 -- intentional fire-and-forget; lifecycle ends with daemon process
			if serveErr := http.Serve(ln, wsServer.HTTPHandler()); serveErr != nil && serveErr != http.ErrServerClosed {
				slog.Warn("[network-listener] serve ended", "addr", bound, "err", serveErr)
			}
		}()
		networkListeners[addrIP] = bound
		slog.Info("[network-listener] bound", "addr", bound)
		return bound, nil
	}

	// Wire the WebSocket client registry into the message handler so it can
	// broadcast notification.message to ALL connected clients (including the
	// browser UI which never registers a subscription row in the DB).
	messageHandler.SetWSBroadcaster(wsServer.GetClients())

	// Wire the dispatcher's client notifier so subscription-based push
	// notifications (thrum subscribe) also work for CLI agents connected via WS.
	dispatcher.SetClientNotifier(daemon.NewBroadcaster(nil, wsServer.GetClients()))

	// Clean up subscriptions when a WebSocket client disconnects (thrum-pgoc fix)
	subSvc := subscriptions.NewService(st.DB())
	wsServer.SetDisconnectHook(func(sessionID string) {
		_, _ = subSvc.ClearBySession(context.Background(), sessionID)
	})

	// Tailscale tsnet listener (optional — daemon works fine without it)
	// Lazy start: tsnet starts only when peers exist (local.port > 0) or
	// THRUM_TS_PORT is explicitly set. No more THRUM_TS_ENABLED gate.
	tsCfg := config.LoadTailscaleConfig(thrumDir)
	var tsnetStarted bool
	var tsListenerCleanup func()

	// startTsnet starts the tsnet listener on the given port. Safe to call
	// concurrently — subsequent calls are no-ops after the first success.
	startTsnet := func(port int) error {
		tsnetMu.Lock()
		defer tsnetMu.Unlock()
		if tsnetStarted {
			return nil
		}
		tsCfg.Port = port
		tsCfg.Enabled = true
		if tsCfg.Hostname == "" {
			tsCfg.Hostname = hostname + "-thrum"
		}
		// Re-read auth key from env — may have been set by peer.start_pairing RPC
		if tsCfg.AuthKey == "" {
			tsCfg.AuthKey = os.Getenv("THRUM_TS_AUTHKEY")
		}
		if tsCfg.AuthKey == "" {
			return fmt.Errorf("tailscale auth key not available — run 'thrum peer add' to configure")
		}

		tsListener, err := daemon.NewTsnetServer(tsCfg)
		if err != nil {
			return fmt.Errorf("start tsnet: %w", err)
		}
		tsListenerCleanup = func() { _ = tsListener.Close() }

		// Create sync-only registry for Tailscale connections
		syncRegistry := daemon.NewSyncRegistry()
		if syncManager != nil {
			syncRegistry.SetPeerRegistry(syncManager.PeerRegistry())
		}

		// Set Tailscale address so peer.join knows our reachable address.
		// Use the Tailscale IP — regular DNS cannot resolve tsnet hostnames.
		tsHost := tsListener.ReachableAddr(tsCfg.Hostname)
		tsLocalAddr = fmt.Sprintf("%s:%d", tsHost, tsCfg.Port)

		// Register sync handlers
		syncPullHandler := rpc.NewSyncPullHandler(st)
		syncPeerInfoHandler := rpc.NewPeerInfoHandler(st.DaemonID(), hostname)
		_ = syncRegistry.Register("sync.pull", syncPullHandler.Handle)
		_ = syncRegistry.Register("sync.peer_info", syncPeerInfoHandler.Handle)

		if syncManager != nil {
			syncNotifyHandler := rpc.NewSyncNotifyHandler(syncManager.SyncFromPeerByID)
			_ = syncRegistry.Register("sync.notify", syncNotifyHandler.Handle)
		}
		if pairHandler != nil {
			_ = syncRegistry.Register("pair.request", pairHandler.Handle)
		}
		if repairHandler != nil {
			_ = syncRegistry.Register("peer.repair", repairHandler.Handle)
		}

		// Build WebSocket server options for the Tailscale sync endpoint.
		// Token auth validates that the connecting peer has a stored token.
		// Pairing connections arrive with ?pairing_code= and must be allowed
		// without a token — the pairing manager validates the code.
		var tsWSOpts []websocket.ServerOption
		if syncManager != nil {
			tsWSOpts = append(tsWSOpts, websocket.WithTokenValidator(func(token string) bool {
				return syncManager.PeerRegistry().FindPeerByToken(token) != nil
			}))
		}
		if pairingMgr != nil {
			tsWSOpts = append(tsWSOpts, websocket.WithPairingValidator(func(code string) bool {
				return pairingMgr.HasActiveSession()
			}))
		}

		// Serve WebSocket on the tsnet listener (replaces raw TCP accept loop).
		// The SyncRegistry implements websocket.HandlerRegistry via GetHandler.
		// NewServer with empty addr + nil uiFS registers the WS handler at "/".
		tsWSServer := websocket.NewServer("", syncRegistry, nil, tsWSOpts...)
		go tsListener.ServeHTTP(ctx, tsWSServer.HTTPHandler())

		// Start periodic sync with Tailscale-optimized intervals
		if syncManager != nil {
			periodicSync := daemon.NewPeriodicSyncScheduler(syncManager, st)
			periodicSync.SetInterval(daemon.TailscaleSyncInterval)
			periodicSync.SetRecentThreshold(daemon.TailscaleRecentSyncThreshold)
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

		tsnetStarted = true
		return nil
	}
	defer func() {
		if tsListenerCleanup != nil {
			tsListenerCleanup()
		}
	}()

	// Wire startTsnet into the lazy start callback for peer.start_pairing
	startTsnetFn = startTsnet

	// Determine whether to start tsnet at boot.
	// Priority: THRUM_TS_PORT env > peers.json local.port > skip (lazy start on peer add)
	var tsBootPort int
	if p := os.Getenv("THRUM_TS_PORT"); p != "" {
		if port, err := strconv.Atoi(p); err == nil {
			tsBootPort = port
		}
	} else if peerRegistry != nil && peerRegistry.LocalPort() > 0 {
		tsBootPort = peerRegistry.LocalPort()
	}

	if tsBootPort > 0 {
		if err := startTsnet(tsBootPort); err != nil {
			fmt.Fprintf(os.Stderr, "Tailscale sync disabled: %v\n", err)
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

	// Start scheduled backup if configured
	if thrumCfg.Backup.Schedule != "" {
		backupInterval, parseErr := time.ParseDuration(thrumCfg.Backup.Schedule)
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: invalid backup schedule %q: %v\n", thrumCfg.Backup.Schedule, parseErr)
		} else if backupInterval > 0 {
			backupDir := thrumCfg.Backup.Dir
			if backupDir == "" {
				backupDir = filepath.Join(thrumDir, "backup")
			}
			repoName := cli.GetRepoName(absPath)
			scheduler := backup.NewBackupScheduler(backupInterval, func() backup.BackupOptions {
				syncDirForBackup, _ := paths.SyncWorktreePath(absPath)
				return backup.BackupOptions{
					BackupDir:    backupDir,
					RepoName:     repoName,
					SyncDir:      syncDirForBackup,
					ThrumDir:     thrumDir,
					DBPath:       filepath.Join(thrumDir, "var", "messages.db"),
					ThrumVersion: Version,
					Retention:    &thrumCfg.Backup.Retention,
					Plugins:      thrumCfg.Backup.Plugins,
					PostBackup:   thrumCfg.Backup.PostBackup,
					RepoPath:     absPath,
				}
			})
			go scheduler.Start(ctx)
			fmt.Fprintf(os.Stderr, "  Backup:      every %s\n", backupInterval)
		}
	}

	// Monitor jobs supervisor — launches runner goroutines for every monitor
	// in the DB with status=running and blocks on ctx.Done(). Must start
	// AFTER the backup scheduler and BEFORE lifecycle.Run(ctx).
	//
	// EnsureAllMonitorSenders must be called BEFORE Start() reloads and
	// launches runners so that any monitor persisted from a pre-fix daemon
	// run has its synthetic agent+session row in place before its first
	// match delivery fires.  New monitors (created via HandleStart after
	// this fix) already have their rows inserted by ensureMonitorSender.
	monitorHandler.EnsureAllMonitorSenders(ctx)
	go monitorSupervisor.Start(ctx)

	// hook-inbox-delivery: reconcile spool files against DB read-state hourly.
	// Pattern mirrors PeriodicSyncScheduler — own goroutine, own ticker.
	spoolJanitor := inbox.NewSpoolJanitor(
		thrumDir,
		func() []string { return nudge.LocalAgentNames(thrumDir) },
		func(msgID, agentID string) inbox.ReadState {
			return queryMessageReadState(ctx, st, msgID, agentID)
		},
	)
	go spoolJanitor.Start(ctx)

	// Telegram bridge RPC handlers + goroutine
	telegramHandler := rpc.NewTelegramHandler(absPath)
	server.RegisterHandler("telegram.configure", telegramHandler.HandleConfigure)
	server.RegisterHandler("telegram.status", telegramHandler.HandleStatus)
	server.RegisterHandler("telegram.pair", telegramHandler.HandlePair)
	// Also register on WebSocket registry so the web UI settings panel can
	// call telegram.status even when the bridge is not yet configured.
	wsRegistry.Register("telegram.configure", websocket.Handler(telegramHandler.HandleConfigure))
	wsRegistry.Register("telegram.status", websocket.Handler(telegramHandler.HandleStatus))
	wsRegistry.Register("telegram.pair", websocket.Handler(telegramHandler.HandlePair))

	if thrumCfg.Telegram.TelegramEnabled() {
		tgBridge := telegram.New(thrumCfg.Telegram, wsPort)
		telegramHandler.SetBridge(tgBridge)
		go tgBridge.Run(ctx)
		fmt.Fprintf(os.Stderr, "  Telegram:    bridge enabled (target: %s)\n", thrumCfg.Telegram.Target)
	}

	// Tmux session management handlers
	tmuxHandler := rpc.NewTmuxHandler(thrumDir, st)
	// Wire the permission scheduler so HandleCheckPane can dispatch
	// to OnDetection / OnRecovery. Without this, the permission
	// branch of HandleCheckPane is a no-op and nudges never fire.
	tmuxHandler.SetPermission(permPkg)
	server.RegisterHandler("tmux.create", tmuxHandler.HandleCreate)
	server.RegisterHandler("tmux.launch", tmuxHandler.HandleLaunch)
	server.RegisterHandler("tmux.status", tmuxHandler.HandleStatus)
	server.RegisterHandler("tmux.kill", tmuxHandler.HandleKill)
	server.RegisterHandler("tmux.send", tmuxHandler.HandleSend)
	server.RegisterHandler("tmux.capture", tmuxHandler.HandleCapture)
	server.RegisterHandler("tmux.check-pane", tmuxHandler.HandleCheckPane)
	server.RegisterHandler("tmux.restart", tmuxHandler.HandleRestart)
	server.RegisterHandler("tmux.queue", tmuxHandler.HandleQueue)
	server.RegisterHandler("tmux.cancel", tmuxHandler.HandleCancel)
	server.RegisterHandler("tmux.queue-status", tmuxHandler.HandleQueueStatus)
	server.RegisterLongPollHandler("tmux.queue-wait", tmuxHandler.HandleQueueWait)

	// Recover queue state after restart — mark interrupted commands, reload queued.
	if err := tmuxHandler.RecoverQueueState(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "[queue] recovery failed: %v\n", err)
	}

	// Auto-connect to dialer-role peers after the WS server is ready.
	// xir.29: build a single reconcile.Manager shared by the boot-time
	// scan below and the send-time OnDialError hook wired via
	// peerManager.SetReconcileManager (Phase 5). Defined here so both
	// consumers observe the same attempt/lock state.
	var reconcileMgr *reconcile.Manager
	if peerManager != nil && peerRegistry != nil {
		localIdent := st.Identity()
		// I7 review finding: Address was previously empty here, so the
		// peer.repair request sent an empty address field and the
		// listener could not update its cached view of us. Supply the
		// WS port (same format peers store as PeerInfo.Address).
		localDialer := reconcile.DialerIdentity{
			DaemonID:     peerRegistry.LocalDaemonID(),
			Address:      ":" + wsPort,
			RepoName:     localIdent.RepoName,
			Hostname:     localIdent.Hostname,
			RepoPath:     localIdent.RepoPath,
			GitOriginURL: localIdent.GitOriginURL,
		}
		reconcileMgr = reconcile.NewManager(peerRegistry, reconcile.WSDial, localDialer)
		peerManager.SetReconcileManager(reconcileMgr)
	}

	if peerManager != nil && thrumCfg.Peers.AutoConnect {
		go func() {
			time.Sleep(500 * time.Millisecond) // Wait for WS server to start
			peerManager.ConnectAll(ctx)
		}()
	}

	// xir.29: one-shot boot-time reconcile scan. NOT periodic — fires
	// once after bridges have had a chance to attempt their initial
	// dial via ConnectAll, then exits. Per-peer drift is surfaced via
	// PeerInfo.ReconcileStatus → `thrum peer list`.
	//
	// The 2s settling window gives bridge.ConnectAll (scheduled at
	// 500ms) time to complete its first dial round; any drift indicators
	// (daemon_id rotation, etc.) surface before reconcile kicks in.
	// peer.repair is registered statically before lifecycle.Run (see
	// the tsnet+WS registration sites above) so there is no handler-
	// registration race.
	if reconcileMgr != nil {
		go func() {
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return
			}
			results := reconcileMgr.ReconcileAll(ctx)
			var attempted, succeeded, failed int
			for _, r := range results {
				attempted++
				if r.OK {
					succeeded++
				} else {
					failed++
					// Emit per-peer detail at DEBUG only when a
					// plumbing failure surfaced (r.Err != nil). Known
					// categories (CatUnreachable / CatTokenRejected)
					// without r.Err are already reflected in the
					// registry's ReconcileStatus marker — no need to
					// double-report at log level (M8 review finding).
					if r.Err != nil {
						slog.Debug("reconcile peer failed",
							"peer", r.PeerName,
							"category", int(r.Category),
							"err", r.Err)
					}
				}
			}
			slog.Info("reconcile boot scan complete",
				"attempted", attempted,
				"succeeded", succeeded,
				"failed", failed)
		}()
	}

	return lifecycle.Run(ctx)
}

// queryMessageReadState checks whether a message is read by a specific
// recipient agent, missing entirely, or still unread. Used by the
// SpoolJanitor to reconcile per-agent spool files against DB state.
//
// Semantics (derived from the unread-filter clause in
// internal/daemon/rpc/message.go): a message is "read by agent A"
// when a row exists in message_deliveries with recipient_agent_id=A
// and read_at IS NOT NULL. "Missing" means no row in messages.
func queryMessageReadState(ctx context.Context, st *state.State, msgID, agentID string) inbox.ReadState {
	var exists int
	err := st.DB().QueryRowContext(ctx,
		`SELECT 1 FROM messages WHERE message_id = ?`,
		msgID,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return inbox.StateMissing
	}
	if err != nil {
		// On DB error, be conservative — keep the file.
		return inbox.StateUnread
	}

	var readExists int
	err = st.DB().QueryRowContext(ctx,
		`SELECT 1 FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ? AND read_at IS NOT NULL LIMIT 1`,
		msgID, agentID,
	).Scan(&readExists)
	if err == nil {
		return inbox.StateRead
	}
	if err != sql.ErrNoRows {
		// Unexpected DB error on the read-state probe. Keep the file
		// (conservative default) but surface the error so persistent
		// DB trouble is visible in the janitor logs.
		slog.Warn("[inbox_janitor] message_deliveries probe failed",
			"agent", agentID, "msg_id", msgID, "err", err)
	}
	return inbox.StateUnread
}

func rolesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "roles",
		Short: "Manage role-based preamble templates",
		Long: `Manage role-based preamble templates in .thrum/role_templates/.

Role templates are Go text/template files that automatically generate
agent preambles during registration. Templates are rendered with agent
identity data (AgentName, Role, Module, WorktreePath, RepoRoot, CoordinatorName).`,
	}

	cmd.AddCommand(rolesListCmd())
	cmd.AddCommand(rolesDeployCmd())

	return cmd
}

func rolesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show configured templates and matching agents",
		Long: `List all role templates in .thrum/role_templates/ and show which
registered agents match each template.

Examples:
  thrum roles list`,
		RunE: func(cmd *cobra.Command, args []string) error {
			thrumDir := filepath.Join(flagRepo, ".thrum")

			templates, err := agentcontext.ListRoleTemplates(thrumDir)
			if err != nil {
				return fmt.Errorf("list role templates: %w", err)
			}

			if len(templates) == 0 {
				fmt.Println("No role templates found in .thrum/role_templates/")
				fmt.Println("  Create templates manually or use: /thrum:configure-roles")
				return nil
			}

			for name, agents := range templates {
				if len(agents) == 0 {
					fmt.Printf("%s    (0 agents)\n", name)
				} else {
					fmt.Printf("%s    (%d agents: %s)\n", name, len(agents), strings.Join(agents, ", "))
				}
			}

			return nil
		},
	}
}

func rolesDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Re-render preambles for registered agents from role templates",
		Long: `Re-render preambles for all registered agents that have matching
role templates. Templates in .thrum/role_templates/ are rendered with
each agent's identity data and written to .thrum/context/{agent}_preamble.md.

This is a full overwrite — templates are the source of truth.

Examples:
  thrum roles deploy              # Deploy for all agents
  thrum roles deploy --agent foo  # Deploy for a specific agent
  thrum roles deploy --dry-run    # Preview what would change`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentFilter, _ := cmd.Flags().GetString("agent")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			thrumDir := filepath.Join(flagRepo, ".thrum")

			result, err := agentcontext.DeployAll(thrumDir, agentFilter, dryRun)
			if err != nil {
				return fmt.Errorf("deploy role templates: %w", err)
			}

			if dryRun {
				fmt.Println("Dry run — no files written")
			}

			totalProcessed := len(result.Updated) + len(result.Skipped)
			if totalProcessed == 0 {
				fmt.Println("No agents found")
				return nil
			}

			if len(result.Updated) > 0 {
				verb := "Updated"
				if dryRun {
					verb = "Would update"
				}
				fmt.Printf("%s %d/%d agents", verb, len(result.Updated), totalProcessed)
				if len(result.Skipped) > 0 {
					fmt.Printf(" (no template for: %s)", strings.Join(result.Skipped, ", "))
				}
				fmt.Println()
			} else {
				fmt.Printf("No matching templates for %d agents\n", totalProcessed)
			}

			return nil
		},
	}

	cmd.Flags().String("agent", "", "Deploy for a specific agent only")
	cmd.Flags().Bool("dry-run", false, "Preview changes without writing files")

	return cmd
}

// applyRolePreamble applies the preamble for an agent using the priority:
// preambleFile > role template > default. Called from both quickstart and agent register.
func applyRolePreamble(thrumDir, agentName, role, preambleFile string, force bool) error {
	if preambleFile != "" {
		// --preamble-file takes precedence over everything
		customContent, err := os.ReadFile(preambleFile) // #nosec G304 -- preambleFile is user-specified via --preamble-file CLI flag; this is a CLI tool, user controls the path
		if err != nil {
			return fmt.Errorf("failed to read preamble file %q: %w", preambleFile, err)
		}
		composed := append(agentcontext.DefaultPreamble(), []byte("\n---\n\n")...)
		composed = append(composed, customContent...)
		if err := agentcontext.SavePreamble(thrumDir, agentName, composed); err != nil {
			return fmt.Errorf("failed to save composed preamble: %w", err)
		}
		return nil
	}

	rendered, renderErr := agentcontext.RenderRoleTemplate(thrumDir, agentName, role)
	if renderErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to render role template for %q: %v (using default)\n", role, renderErr) // #nosec G705 -- stderr diagnostic, not web output
	} else if rendered != nil {
		// Role template found — use it as the preamble
		if err := agentcontext.SavePreamble(thrumDir, agentName, rendered); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save role template preamble: %v\n", err)
		}
		return nil
	}

	// Fall back to role-aware default preamble
	preamble := agentcontext.RoleAwarePreamble(role)
	if force {
		// Force mode: always overwrite with current default
		if err := agentcontext.SavePreamble(thrumDir, agentName, preamble); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save preamble: %v\n", err)
		}
	} else {
		// Only write if no preamble exists yet
		path := agentcontext.PreamblePath(thrumDir, agentName)
		if _, err := os.Stat(path); os.IsNotExist(err) { // #nosec G703 -- path from PreamblePath, not user input
			if err := agentcontext.SavePreamble(thrumDir, agentName, preamble); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create preamble: %v\n", err)
			}
		}
	}
	return nil
}

func backupCmd() *cobra.Command {
	var flagDir string

	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Backup thrum data",
		Long:  "Snapshot all thrum data (events, messages, config, identities) to a backup directory.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackupCreate(flagDir)
		},
	}

	cmd.PersistentFlags().StringVar(&flagDir, "dir", "", "Override backup directory")

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show last backup info",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackupStatus(flagDir)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "config",
		Short: "Show effective backup config",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackupConfig()
		},
	})

	var flagYes bool
	restoreCmd := &cobra.Command{
		Use:   "restore [archive.zip]",
		Short: "Restore from backup",
		Long:  "Restore thrum data from the latest backup or a specific archive zip.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var archivePath string
			if len(args) > 0 {
				archivePath = args[0]
			}
			return runBackupRestore(flagDir, archivePath, flagYes)
		},
	}
	restoreCmd.Flags().BoolVar(&flagYes, "yes", false, "Skip confirmation prompt")
	cmd.AddCommand(restoreCmd)

	cmd.AddCommand(pluginCmd())
	cmd.AddCommand(scheduleCmd())

	return cmd
}

func scheduleCmd() *cobra.Command {
	var flagScheduleDir string

	cmd := &cobra.Command{
		Use:   "schedule [interval|off]",
		Short: "Configure automatic backup schedule",
		Long: `View or set the automatic backup schedule. The daemon runs backups at the
configured interval when running.

Examples:
  thrum backup schedule            Show current schedule
  thrum backup schedule 24h        Back up every 24 hours
  thrum backup schedule 12h        Back up every 12 hours
  thrum backup schedule 6h         Back up every 6 hours
  thrum backup schedule 30m        Back up every 30 minutes
  thrum backup schedule off        Disable scheduled backups
  thrum backup schedule 24h --dir /path/to/backups

Intervals use Go duration format: "24h", "12h", "6h30m", "168h" (1 week).

The schedule is stored in .thrum/config.json under backup.schedule. The daemon
must be restarted for schedule changes to take effect.

Third-party backup plugins can be configured manually in .thrum/config.json:

  {
    "backup": {
      "schedule": "24h",
      "plugins": [
        {"name": "beads", "command": "bd backup --force", "include": [".beads/backup/*"]}
      ],
      "post_backup": "echo backup done"
    }
  }

Use 'thrum backup plugin add' to manage plugins via CLI.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackupSchedule(args, flagScheduleDir)
		},
	}

	cmd.Flags().StringVar(&flagScheduleDir, "dir", "", "Set backup directory")

	return cmd
}

func runBackupSchedule(args []string, dirOverride string) error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Show mode: no args
	if len(args) == 0 {
		if cfg.Backup.Schedule == "" {
			fmt.Println("Backup schedule: disabled")
		} else {
			fmt.Printf("Backup schedule: every %s\n", cfg.Backup.Schedule)
		}
		backupDir := cfg.Backup.Dir
		if backupDir == "" {
			backupDir = filepath.Join(thrumDir, "backup")
		}
		fmt.Printf("Backup directory: %s\n", backupDir)

		// Show last backup time from manifest
		repoName := cli.GetRepoName(flagRepo)
		manifestPath := filepath.Join(backupDir, repoName, "current", "manifest.json")
		if data, readErr := os.ReadFile(filepath.Clean(manifestPath)); readErr == nil {
			var manifest map[string]any
			if json.Unmarshal(data, &manifest) == nil {
				if ts, ok := manifest["timestamp"].(string); ok {
					fmt.Printf("Last backup: %s\n", ts)
				}
			}
		}

		fmt.Println("\nRestart the daemon for schedule changes to take effect.")
		return nil
	}

	// Set mode
	interval := args[0]
	if interval == "off" || interval == "disable" || interval == "none" {
		cfg.Backup.Schedule = ""
		fmt.Println("Backup schedule: disabled")
	} else {
		// Validate it's a valid Go duration
		d, parseErr := time.ParseDuration(interval)
		if parseErr != nil {
			return fmt.Errorf("invalid interval %q: use Go duration format (e.g., 24h, 12h, 6h30m): %w", interval, parseErr)
		}
		if d <= 0 {
			return fmt.Errorf("interval must be positive, got %s", d)
		}
		cfg.Backup.Schedule = interval
		fmt.Printf("Backup schedule: every %s\n", interval)
	}

	if dirOverride != "" {
		cfg.Backup.Dir = dirOverride
		fmt.Printf("Backup directory: %s\n", dirOverride)
	}

	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Println("\nRestart the daemon for schedule changes to take effect.")
	return nil
}

func runBackupCreate(dirOverride string) error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Resolve backup dir: CLI flag > config > default
	backupDir := dirOverride
	if backupDir == "" {
		backupDir = cfg.Backup.Dir
	}
	if backupDir == "" {
		backupDir = filepath.Join(thrumDir, "backup")
	}

	// Resolve sync worktree
	syncDir, err := paths.SyncWorktreePath(flagRepo)
	if err != nil {
		syncDir = "" // non-fatal: sync dir may not exist yet
	}

	dbPath := filepath.Join(thrumDir, "var", "messages.db")
	repoName := cli.GetRepoName(flagRepo)

	result, err := backup.RunBackup(backup.BackupOptions{
		BackupDir:    backupDir,
		RepoName:     repoName,
		SyncDir:      syncDir,
		ThrumDir:     thrumDir,
		DBPath:       dbPath,
		ThrumVersion: Version,
		Retention:    &cfg.Backup.Retention,
		Plugins:      cfg.Backup.Plugins,
		PostBackup:   cfg.Backup.PostBackup,
		RepoPath:     flagRepo,
	})
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	if flagJSON {
		data, _ := json.MarshalIndent(result.Manifest, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Printf("Backup complete: %s\n", result.CurrentDir)
		fmt.Printf("  Events: %d lines\n", result.SyncResult.EventLines)
		fmt.Printf("  Message files: %d\n", result.SyncResult.MessageFiles)
		fmt.Printf("  Local tables: %d\n", len(result.LocalResult.Tables))
		fmt.Printf("  Config files: %d\n", result.Manifest.Counts.ConfigFiles)
		if pluginSummary := backup.FormatPluginResults(result.PluginResults); pluginSummary != "" {
			fmt.Printf("  Plugins:\n%s", pluginSummary)
		}
		if result.PostHookResult != nil {
			if result.PostHookResult.Error != "" {
				fmt.Printf("  Post-backup hook: FAILED (%s)\n", result.PostHookResult.Error)
			} else {
				fmt.Printf("  Post-backup hook: ok\n")
			}
		}
	}

	return nil
}

func runBackupStatus(dirOverride string) error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	backupDir := dirOverride
	if backupDir == "" {
		backupDir = cfg.Backup.Dir
	}
	if backupDir == "" {
		backupDir = filepath.Join(thrumDir, "backup")
	}

	repoName := cli.GetRepoName(flagRepo)
	currentDir := filepath.Join(backupDir, repoName, "current")

	manifest, err := backup.ReadManifest(currentDir)
	if err != nil {
		return fmt.Errorf("no backup found (looked in %s): %w", currentDir, err)
	}

	if flagJSON {
		data, _ := json.MarshalIndent(manifest, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Printf("Last backup: %s\n", manifest.Timestamp.Local().Format("2006-01-02 15:04:05"))
		fmt.Printf("  Thrum version: %s\n", manifest.ThrumVersion)
		fmt.Printf("  Repo: %s\n", manifest.RepoName)
		fmt.Printf("  Events: %d\n", manifest.Counts.Events)
		fmt.Printf("  Message files: %d\n", manifest.Counts.MessageFiles)
		fmt.Printf("  Local tables: %d\n", manifest.Counts.LocalTables)
		fmt.Printf("  Config files: %d\n", manifest.Counts.ConfigFiles)
		if len(manifest.Counts.Plugins) > 0 {
			fmt.Printf("  Plugins: %v\n", manifest.Counts.Plugins)
		}
		fmt.Printf("  Location: %s\n", currentDir)

		// Show archive rotation stats
		archivesDir := filepath.Join(backupDir, repoName, "archives")
		if entries, err := os.ReadDir(archivesDir); err == nil {
			var archiveCount int
			var totalSize int64
			var oldest, newest time.Time
			for _, e := range entries {
				if e.IsDir() || strings.HasPrefix(e.Name(), "pre-restore-") {
					continue
				}
				archiveCount++
				if info, err := e.Info(); err == nil {
					totalSize += info.Size()
				}
				// Parse timestamp from filename (2006-01-02T150405.zip)
				name := strings.TrimSuffix(e.Name(), ".zip")
				if ts, err := time.Parse("2006-01-02T150405", name); err == nil {
					if oldest.IsZero() || ts.Before(oldest) {
						oldest = ts
					}
					if newest.IsZero() || ts.After(newest) {
						newest = ts
					}
				}
			}
			if archiveCount > 0 {
				fmt.Printf("Archives: %d (%.1f MB)\n", archiveCount, float64(totalSize)/(1024*1024))
				if !oldest.IsZero() {
					fmt.Printf("  Oldest: %s\n", oldest.Local().Format("2006-01-02 15:04:05"))
					fmt.Printf("  Newest: %s\n", newest.Local().Format("2006-01-02 15:04:05"))
				}
			}
		}
	}

	return nil
}

func runBackupConfig() error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	effectiveDir := cfg.Backup.Dir
	if effectiveDir == "" {
		effectiveDir = filepath.Join(thrumDir, "backup") + " (default)"
	}

	if flagJSON {
		data, _ := json.MarshalIndent(cfg.Backup, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Printf("Backup directory: %s\n", effectiveDir)
		fmt.Printf("Retention:\n")
		fmt.Printf("  Daily: %d\n", cfg.Backup.Retention.RetentionDaily())
		fmt.Printf("  Weekly: %d\n", cfg.Backup.Retention.RetentionWeekly())
		monthly := fmt.Sprintf("%d", cfg.Backup.Retention.RetentionMonthly())
		if cfg.Backup.Retention.RetentionMonthly() == -1 {
			monthly = "forever"
		}
		fmt.Printf("  Monthly: %s\n", monthly)
		if len(cfg.Backup.Plugins) > 0 {
			fmt.Printf("Plugins:\n")
			for _, p := range cfg.Backup.Plugins {
				fmt.Printf("  %s: %s\n", p.Name, p.Command)
			}
		}
		if cfg.Backup.PostBackup != "" {
			fmt.Printf("Post-backup: %s\n", cfg.Backup.PostBackup)
		}
	}

	return nil
}

func runBackupRestore(dirOverride, archivePath string, skipConfirm bool) error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	backupDir := dirOverride
	if backupDir == "" {
		backupDir = cfg.Backup.Dir
	}
	if backupDir == "" {
		backupDir = filepath.Join(thrumDir, "backup")
	}

	repoName := cli.GetRepoName(flagRepo)

	if !skipConfirm {
		fmt.Printf("This will restore thrum data from backup.\n")
		fmt.Printf("  Backup dir: %s\n", backupDir)
		fmt.Printf("  Repo: %s\n", repoName)
		if archivePath != "" {
			fmt.Printf("  Archive: %s\n", archivePath)
		} else {
			fmt.Printf("  Source: current/\n")
		}
		fmt.Printf("A safety backup will be created first.\n")
		fmt.Printf("Continue? [y/N] ")

		var answer string
		_, _ = fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" && answer != "yes" {
			fmt.Println("Restore canceled.")
			return nil
		}
	}

	// Stop daemon before restore to avoid file handle conflicts
	daemonWasRunning := false
	if stopErr := cli.DaemonStop(flagRepo); stopErr == nil {
		daemonWasRunning = true
		fmt.Println("Daemon stopped for restore.")
	}

	syncDir, err := paths.SyncWorktreePath(flagRepo)
	if err != nil {
		syncDir = ""
	}

	dbPath := filepath.Join(thrumDir, "var", "messages.db")

	result, err := backup.RunRestore(backup.RestoreOptions{
		BackupDir:   backupDir,
		RepoName:    repoName,
		ArchivePath: archivePath,
		SyncDir:     syncDir,
		ThrumDir:    thrumDir,
		DBPath:      dbPath,
		Plugins:     cfg.Backup.Plugins,
		RepoPath:    flagRepo,
	})
	if err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}

	if flagJSON {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	} else {
		if result.SafetyBackup != "" {
			fmt.Printf("Safety backup: %s\n", result.SafetyBackup)
		}
		fmt.Printf("Restored from: %s\n", result.Source)
	}

	// Restart daemon if it was running before restore
	if daemonWasRunning {
		if restartErr := cli.DaemonRestart(flagRepo, cfg.Daemon.LocalOnly, false); restartErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not restart daemon: %v\n", restartErr)
			fmt.Println("Restart manually: thrum daemon start")
		} else {
			fmt.Println("Daemon restarted. SQLite will rebuild from restored JSONL.")
		}
	}

	return nil
}

func pluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage backup plugins",
	}

	// plugin add
	var addName, addCommand, addPreset string
	var addIncludes []string
	addCmd := &cobra.Command{
		Use:   "add",
		Short: "Add a backup plugin",
		Long:  "Add a plugin by name/command/include or use --preset for built-in plugins.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginAdd(addName, addCommand, addIncludes, addPreset)
		},
	}
	addCmd.Flags().StringVar(&addName, "name", "", "Plugin name")
	addCmd.Flags().StringVar(&addCommand, "command", "", "Command to run before collecting files")
	addCmd.Flags().StringSliceVar(&addIncludes, "include", nil, "File patterns to collect (glob)")
	addCmd.Flags().StringVar(&addPreset, "preset", "", "Use built-in preset (beads, beads-rust)")
	cmd.AddCommand(addCmd)

	// plugin list
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginList()
		},
	})

	// plugin remove
	var removeName string
	removeCmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove a backup plugin",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginRemove(removeName)
		},
	}
	removeCmd.Flags().StringVar(&removeName, "name", "", "Plugin name to remove")
	_ = removeCmd.MarkFlagRequired("name")
	cmd.AddCommand(removeCmd)

	return cmd
}

func runPluginAdd(name, command string, includes []string, preset string) error {
	if preset != "" {
		p, ok := backup.PluginPresets[preset]
		if !ok {
			return fmt.Errorf("unknown preset %q (available: beads, beads-rust)", preset)
		}
		name = p.Name
		command = p.Command
		includes = p.Include
	}

	if name == "" {
		return fmt.Errorf("--name or --preset is required")
	}

	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	cfg.AddPlugin(config.PluginConfig{
		Name:    name,
		Command: command,
		Include: includes,
	})

	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("Plugin %q added.\n", name)
	return nil
}

func runPluginList() error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if len(cfg.Backup.Plugins) == 0 {
		fmt.Println("No plugins configured.")
		return nil
	}

	if flagJSON {
		data, _ := json.MarshalIndent(cfg.Backup.Plugins, "", "  ")
		fmt.Println(string(data))
	} else {
		for _, p := range cfg.Backup.Plugins {
			fmt.Printf("  %s\n", p.Name)
			if p.Command != "" {
				fmt.Printf("    command: %s\n", p.Command)
			}
			if len(p.Include) > 0 {
				fmt.Printf("    include: %v\n", p.Include)
			}
		}
	}

	return nil
}

func runPluginRemove(name string) error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if !cfg.RemovePlugin(name) {
		return fmt.Errorf("plugin %q not found", name)
	}

	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("Plugin %q removed.\n", name)
	return nil
}

// getWorktreeName extracts the worktree name from the repo path.

func telegramCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Manage Telegram bridge",
	}
	cmd.AddCommand(telegramConfigureCmd())
	cmd.AddCommand(telegramStatusCmd())
	cmd.AddCommand(telegramPairCmd())
	return cmd
}

func telegramConfigureCmd() *cobra.Command {
	var (
		flagToken       string
		flagTarget      string
		flagUser        string
		flagYes         bool
		flagAllowFrom   int64
		flagChatID      int64
		flagPairTimeout time.Duration
		flagSkipPair    bool
	)

	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Configure the Telegram bridge",
		Long: `Configure the Telegram bridge connection.

Set the bot token from BotFather, the target agent that receives Telegram
messages, and your Thrum user ID.

Examples:
  thrum telegram configure --token 123456789:AAH... --target @coordinator_main --user leon-letto
  thrum telegram configure  # interactive mode
  thrum telegram configure --token 123456789:AAH... --skip-pair
  thrum telegram configure --allow-from 987654321`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTelegramConfigure(flagToken, flagTarget, flagUser, flagYes, flagAllowFrom, flagChatID, flagPairTimeout, flagSkipPair)
		},
	}

	cmd.Flags().StringVar(&flagToken, "token", "", "Telegram bot token from BotFather")
	cmd.Flags().StringVar(&flagTarget, "target", "", "Target agent for incoming messages (e.g., @coordinator_main)")
	cmd.Flags().StringVar(&flagUser, "user", "", "Your Thrum username (e.g., leon-letto)")
	cmd.Flags().BoolVar(&flagYes, "yes", false, "Skip confirmation prompts")
	cmd.Flags().Int64Var(&flagAllowFrom, "allow-from", 0, "Telegram user ID to whitelist (skips pairing)")
	cmd.Flags().Int64Var(&flagChatID, "chat-id", 0, "Telegram chat ID for outbound (defaults to --allow-from)")
	cmd.Flags().DurationVar(&flagPairTimeout, "pair-timeout", 60*time.Second, "How long to wait for a pairing message")
	cmd.Flags().BoolVar(&flagSkipPair, "skip-pair", false, "Write config only, don't pair")

	return cmd
}

func runTelegramConfigure(token, target, userID string, skipConfirm bool, allowFrom, chatID int64, pairTimeout time.Duration, skipPair bool) error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Interactive prompts for missing fields
	if token == "" {
		if cfg.Telegram.Token != "" {
			fmt.Printf("Current token: %s...\n", cfg.Telegram.MaskedToken())
		}
		fmt.Print("Bot token (from @BotFather): ")
		if _, err := fmt.Scanln(&token); err != nil {
			return fmt.Errorf("read token: %w", err)
		}
	}

	// Validate token format: numeric:alphanumeric
	if !isValidBotToken(token) {
		return fmt.Errorf("invalid token format (expected: 123456789:AAH...)")
	}

	if target == "" {
		target = "@coordinator_main"
		fmt.Printf("Target agent [%s]: ", target)
		var input string
		if _, err := fmt.Scanln(&input); err == nil && input != "" {
			target = input
		}
	}

	// Validate target starts with @
	if len(target) == 0 || target[0] != '@' {
		return fmt.Errorf("target must start with @ (e.g., @coordinator_main)")
	}

	if userID == "" {
		// Auto-detect from git config
		userID = detectGitUser()
		if userID != "" {
			fmt.Printf("User ID [%s]: ", userID)
			var input string
			if _, err := fmt.Scanln(&input); err == nil && input != "" {
				userID = input
			}
		} else {
			fmt.Print("User ID (your Thrum username): ")
			if _, err := fmt.Scanln(&userID); err != nil {
				return fmt.Errorf("read user ID: %w", err)
			}
		}
	}

	if userID == "" {
		return fmt.Errorf("user ID is required")
	}

	// Confirm if replacing existing token
	if cfg.Telegram.Token != "" && !skipConfirm {
		fmt.Printf("Existing token will be replaced (%s... → %s...)\n",
			cfg.Telegram.MaskedToken(), maskToken(token))
		fmt.Print("Continue? [y/N]: ")
		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if confirm != "y" && confirm != "Y" {
			fmt.Println("Canceled.")
			return nil
		}
	}

	// Update config (preserve existing fields like AllowFrom, ChatID)
	cfg.Telegram.Token = token
	cfg.Telegram.Target = target
	cfg.Telegram.UserID = userID

	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("Telegram bridge configured:\n")
	fmt.Printf("  Token:  %s...\n", maskToken(token))
	fmt.Printf("  Target: %s\n", target)
	fmt.Printf("  User:   %s\n", userID)

	// Path 1: --allow-from provided — write directly, skip pairing
	if allowFrom != 0 {
		chatIDVal := chatID
		if chatIDVal == 0 {
			chatIDVal = allowFrom // personal chat: chat_id == user_id
		}
		cfg.Telegram.AllowFrom = []int64{allowFrom}
		cfg.Telegram.ChatID = chatIDVal
		if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Println("\nRestart the daemon to apply: thrum daemon restart")
		return nil
	}

	// Path 2: --skip-pair — just save and instruct restart
	if skipPair {
		fmt.Println("\nRestart the daemon to apply: thrum daemon restart")
		return nil
	}

	// Path 3: Auto-pair flow
	fmt.Println("\nStarting daemon with new config...")
	if err := cli.DaemonRestart(flagRepo, false, false); err != nil {
		return fmt.Errorf("daemon restart: %w", err)
	}
	fmt.Println("Daemon restarted")

	return runTelegramPair(pairTimeout, skipConfirm)
}

func telegramStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show Telegram bridge status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTelegramStatus()
		},
	}
}

func runTelegramStatus() error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	tg := cfg.Telegram

	if flagJSON {
		status := map[string]any{
			"configured": tg.Token != "",
			"enabled":    tg.TelegramEnabled(),
			"target":     tg.Target,
			"user_id":    tg.UserID,
			"chat_id":    tg.ChatID,
			"allow_all":  tg.AllowAll,
		}
		if tg.Token != "" {
			status["token"] = tg.MaskedToken() + "..."
		}
		if len(tg.AllowFrom) > 0 {
			status["allow_from"] = tg.AllowFrom
		}
		data, _ := json.MarshalIndent(status, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if tg.Token == "" {
		fmt.Println("Telegram bridge: not configured")
		fmt.Println("\nRun 'thrum telegram configure' to set up.")
		return nil
	}

	fmt.Println("Telegram Bridge")
	fmt.Println("───────────────")
	fmt.Printf("  Token:   %s...\n", tg.MaskedToken())
	fmt.Printf("  Target:  %s\n", tg.Target)
	fmt.Printf("  User:    %s\n", tg.UserID)
	if tg.ChatID != 0 {
		fmt.Printf("  Chat ID: %d\n", tg.ChatID)
	}

	if tg.Enabled != nil && !*tg.Enabled {
		fmt.Printf("  Enabled: no (explicitly disabled)\n")
	} else {
		fmt.Printf("  Enabled: yes\n")
	}

	// Access control
	if tg.AllowAll {
		fmt.Printf("  Access:  allow all\n")
	} else if len(tg.AllowFrom) > 0 {
		fmt.Printf("  Access:  %d allowed user(s)\n", len(tg.AllowFrom))
	} else {
		fmt.Printf("  Access:  block all (no AllowFrom configured)\n")
	}

	// Check daemon
	wsPort := cli.ReadWebSocketPort(flagRepo)
	if wsPort > 0 {
		fmt.Printf("  Daemon:  running (port %d)\n", wsPort)
	} else {
		fmt.Printf("  Daemon:  not running\n")
	}

	return nil
}

func telegramPairCmd() *cobra.Command {
	var (
		flagPairTimeout time.Duration
		flagYes         bool
	)

	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Pair your Telegram account with the bridge",
		Long: `Start a pairing session that waits for a Telegram message to identify
your account. Send any message to the bot from Telegram, then confirm
the sender to set up the allow list.

The daemon must be running with a configured Telegram token.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTelegramPair(flagPairTimeout, flagYes)
		},
	}

	cmd.Flags().DurationVar(&flagPairTimeout, "pair-timeout", 60*time.Second, "How long to wait for a pairing message")
	cmd.Flags().BoolVar(&flagYes, "yes", false, "Auto-accept the first sender without prompting")

	return cmd
}

func runTelegramPair(pairTimeout time.Duration, autoAccept bool) error {
	// Check config has a token
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}
	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Telegram.Token == "" {
		return fmt.Errorf("telegram not configured — run 'thrum telegram configure' first")
	}

	// Connect to daemon
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("daemon not running — start with 'thrum daemon start'")
	}
	defer client.Close() //nolint:errcheck

	// Call telegram.pair RPC with extended timeout
	fmt.Printf("Pairing — send any message to your bot from Telegram (timeout: %s)...\n", pairTimeout)

	var result rpc.TelegramPairResponse
	req := rpc.TelegramPairRequest{TimeoutSeconds: int(pairTimeout.Seconds())}
	if err := client.CallWithTimeout("telegram.pair", req, &result, pairTimeout+5*time.Second); err != nil {
		return fmt.Errorf("pairing failed: %w", err)
	}

	// Display sender info
	name := result.FirstName
	if result.LastName != "" {
		name += " " + result.LastName
	}
	fmt.Printf("\nMessage from: %s (ID: %d)\n", name, result.UserID)

	// Confirm
	if !autoAccept {
		fmt.Print("  Allow this user? [y/n]: ")
		var answer string
		_, _ = fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Println("Pairing skipped. Run 'thrum telegram pair' to retry.")
			return nil
		}
	}

	// Set allow_from and chat_id via telegram.configure RPC
	chatID := result.ChatID
	configReq := rpc.TelegramConfigureRequest{
		AllowFrom: []int64{result.UserID},
		ChatID:    &chatID,
	}
	var configResult rpc.TelegramConfigureResponse
	if err := client.Call("telegram.configure", configReq, &configResult); err != nil {
		return fmt.Errorf("failed to save pairing config: %w", err)
	}

	fmt.Printf("\nPaired! Allowed users: [%d]\n", result.UserID)
	return nil
}

func isValidBotToken(token string) bool {
	// Token format: numeric_id:alphanumeric_secret
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return false
		}
	}
	for _, c := range parts[1] {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func maskToken(token string) string {
	if len(token) <= 10 {
		return token
	}
	return token[:10]
}

func detectGitUser() string {
	// Use GitConfig (not Git) so we read the real user.name, not the
	// thrum injected override that Git/GitLong apply.
	name, err := safecmd.GitConfig(context.Background(), ".", "user.name")
	if err != nil || name == "" {
		return ""
	}
	// Convert "Leon Letto" → "leon-letto"
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	return name
}

func restartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Moved to 'thrum tmux snapshot'",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("restart commands moved to 'thrum tmux snapshot save/restore/check'")
		},
	}
}

// restartSnapshotSubcmds returns the save, restore, and check subcommands for
// use under 'thrum tmux snapshot'.
func restartSnapshotSubcmds() []*cobra.Command {
	saveCmd := &cobra.Command{
		Use:   "save",
		Short: "Save conversation snapshot for session restart",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve identity from local identity file first (avoids PID-based
			// daemon resolution which can find the wrong agent in multi-worktree setups)
			idFile, _, err := config.LoadIdentityWithPath(flagRepo)
			if err != nil {
				return fmt.Errorf("load identity file: %w", err)
			}
			agentName := idFile.Agent.Name
			sessionID := idFile.SessionID
			thrumDir := filepath.Join(flagRepo, ".thrum")

			pid := idFile.AgentPID
			if pid == 0 {
				// Fallback: query daemon for the agent's AgentPID
				client, err := getClient()
				if err != nil {
					return fmt.Errorf("connect to daemon: %w", err)
				}
				defer func() { _ = client.Close() }()

				var agents []struct {
					AgentID  string `json:"agent_id"`
					AgentPID int    `json:"agent_pid"`
				}
				if err := client.Call("agent.list", nil, &agents); err == nil {
					for _, a := range agents {
						if a.AgentID == agentName && a.AgentPID > 0 {
							pid = a.AgentPID
							break
						}
					}
				}
			}
			if pid == 0 {
				return fmt.Errorf("no agent PID found for %s — ensure agent is registered with an agent PID", agentName)
			}

			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home directory: %w", err)
			}
			claudeDir := filepath.Join(homeDir, ".claude")
			jsonlPath, err := restart.FindSessionJSONL(claudeDir, pid)
			if err != nil {
				return fmt.Errorf("find session JSONL: %w", err)
			}

			cfg, _ := config.LoadThrumConfig(thrumDir)
			maxLines := cfg.Restart.RestartMaxLines()

			conversation, err := restart.ExtractConversation(jsonlPath, maxLines)
			if err != nil {
				return fmt.Errorf("extract conversation: %w", err)
			}

			reason, _ := cmd.Flags().GetString("reason")
			if reason == "" {
				reason = "self-initiated"
			}

			snapshot := restart.FormatRestartSnapshot(agentName, sessionID, reason, conversation)
			if err := restart.SaveSnapshot(thrumDir, agentName, snapshot); err != nil {
				return err
			}

			lines := strings.Count(snapshot, "\n")
			if !flagQuiet {
				fmt.Printf("Restart snapshot saved for %s (%d lines)\n", agentName, lines)
			}
			return nil
		},
	}
	saveCmd.Flags().String("reason", "self-initiated", "Reason for restart")

	restoreCmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore and output a restart snapshot",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			whoami, err := cli.AgentWhoami(client)
			if err != nil {
				return fmt.Errorf("resolve identity: %w", err)
			}

			thrumDir := filepath.Join(flagRepo, ".thrum")
			content, err := restart.Restore(thrumDir, whoami.AgentID)
			if err != nil {
				fmt.Fprintln(os.Stderr, "No restart snapshot found")
				os.Exit(1)
			}
			fmt.Print(content)
			return nil
		},
	}

	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "Check if a restart snapshot exists (exit 0 = yes, exit 1 = no)",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			whoami, err := cli.AgentWhoami(client)
			if err != nil {
				return fmt.Errorf("resolve identity: %w", err)
			}

			thrumDir := filepath.Join(flagRepo, ".thrum")
			if !restart.SnapshotExists(thrumDir, whoami.AgentID) {
				os.Exit(1)
			}
			return nil
		},
	}

	return []*cobra.Command{saveCmd, restoreCmd, checkCmd}
}

func tmuxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tmux",
		Short: "Manage tmux sessions for agents",
	}

	// create
	createCmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a tmux session for an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := cmd.Flags().GetString("cwd")
			agentName, _ := cmd.Flags().GetString("name")
			role, _ := cmd.Flags().GetString("role")
			module, _ := cmd.Flags().GetString("module")
			intent, _ := cmd.Flags().GetString("intent")
			runtimeFlag, _ := cmd.Flags().GetString("runtime")
			noAgent, _ := cmd.Flags().GetBool("no-agent")
			force, _ := cmd.Flags().GetBool("force")

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.TmuxCreate(client, cli.TmuxCreateOptions{
				Name:      args[0],
				Cwd:       cwd,
				AgentName: agentName,
				Role:      role,
				Module:    module,
				Intent:    intent,
				Runtime:   runtimeFlag,
				Force:     force,
				NoAgent:   noAgent,
			})
			if err != nil {
				return err
			}
			if flagJSON {
				out, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(out))
			} else {
				fmt.Print(cli.FormatTmuxCreate(result))
			}
			return nil
		},
	}
	createCmd.Flags().String("cwd", "", "Working directory for the session")
	_ = createCmd.MarkFlagRequired("cwd")
	createCmd.Flags().String("name", "", "Agent name for quickstart registration")
	createCmd.Flags().String("role", "", "Agent role for quickstart registration")
	createCmd.Flags().String("module", "", "Agent module for quickstart registration")
	createCmd.Flags().String("intent", "", "Agent intent")
	createCmd.Flags().String("runtime", "", "Preferred runtime")
	createCmd.Flags().Bool("no-agent", false, "Skip agent registration (create bare session)")
	createCmd.Flags().Bool("force", false, "Re-register even if agent exists; kill+recreate existing session")
	cmd.AddCommand(createCmd)

	// quickstart (alias for create)
	quickstartCmd := &cobra.Command{
		Use:   "quickstart <session-name>",
		Short: "Create a tmux session and register an agent (alias for 'tmux create')",
		Args:  cobra.ExactArgs(1),
		RunE:  createCmd.RunE,
	}
	quickstartCmd.Flags().AddFlagSet(createCmd.Flags())
	cmd.AddCommand(quickstartCmd)

	// launch
	launchCmd := &cobra.Command{
		Use:   "launch <name>",
		Short: "Start an AI tool inside a tmux session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rtOverride, _ := cmd.Flags().GetString("runtime")
			rt := "claude"
			// Resolution: --runtime flag > identity PreferredRuntime > config > "claude"
			out, err := exec.Command("tmux", "display-message", // #nosec G204 -- args are session name from CLI
				"-t", args[0], "-p", "#{pane_current_path}").Output()
			if err == nil {
				sessionCwd := strings.TrimSpace(string(out))
				thrumDir := filepath.Join(sessionCwd, ".thrum")
				if _, statErr := os.Stat(thrumDir); statErr == nil {
					cfg, _ := config.LoadThrumConfig(thrumDir)
					if cfg.Runtime.Primary != "" {
						rt = cfg.Runtime.Primary
					}
				}
				// Use LoadIdentityFromWorktree (not LoadIdentityWithPath) to bypass
				// THRUM_HOME/THRUM_NAME env vars from the calling shell. The launch
				// command resolves the target worktree's identity, not the caller's.
				if idFile, loadErr := config.LoadIdentityFromWorktree(sessionCwd); loadErr == nil && idFile != nil {
					if idFile.PreferredRuntime != "" {
						rt = idFile.PreferredRuntime
					}
				} else {
					return fmt.Errorf("no agent identity found in %s\n  Register first: thrum quickstart --name <agent> --role <role> --module <module>", sessionCwd)
				}
			}
			if rtOverride != "" {
				rt = rtOverride
			}
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.TmuxLaunch(client, cli.TmuxLaunchOptions{
				Name: args[0], Runtime: rt,
			})
			if err != nil {
				return err
			}
			if flagJSON {
				out, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(out))
			} else {
				fmt.Printf("Launched %s in session %s\n", result.Runtime, result.Session)
			}
			return nil
		},
	}
	launchCmd.Flags().String("runtime", "", "AI tool to launch (default: from config or claude)")
	cmd.AddCommand(launchCmd)

	// status (primary) + list (alias)
	statusCmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"list"},
		Short:   "Show tmux-managed sessions with state",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.TmuxStatus(client)
			if err != nil {
				return err
			}
			if flagJSON {
				out, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(out))
			} else {
				fmt.Print(cli.FormatTmuxStatus(result))
			}
			return nil
		},
	}
	cmd.AddCommand(statusCmd)

	// kill
	killCmd := &cobra.Command{
		Use:   "kill <name>",
		Short: "Tear down a tmux session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()
			if err := cli.TmuxKill(client, args[0]); err != nil {
				return err
			}
			if !flagQuiet {
				fmt.Printf("Session %s killed\n", args[0])
			}
			return nil
		},
	}
	cmd.AddCommand(killCmd)

	// send
	sendCmd := &cobra.Command{
		Use:   "send <name> <text>",
		Short: "Send text into a tmux session",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()
			return cli.TmuxSend(client, args[0], args[1])
		},
	}
	cmd.AddCommand(sendCmd)

	// capture
	captureCmd := &cobra.Command{
		Use:   "capture <name>",
		Short: "Capture pane content from a tmux session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lines, _ := cmd.Flags().GetInt("lines")
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.TmuxCapture(client, args[0], lines)
			if err != nil {
				return err
			}
			fmt.Print(result.Content)
			return nil
		},
	}
	captureCmd.Flags().Int("lines", 50, "Number of lines to capture")
	cmd.AddCommand(captureCmd)

	// check-pane (hidden — called by tmux silence hooks)
	checkPaneCmd := &cobra.Command{
		Use:    "check-pane <session>",
		Short:  "Check a tmux pane for permission prompts or idle state (called by tmux hooks)",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			session := args[0]
			target := session + ":0.0"

			content, err := ttmux.CapturePane(target, 5)
			if err != nil {
				return err
			}

			// Runtime resolution and permission-prompt detection both
			// live on the daemon side (HandleCheckPane). The CLI used to
			// load .thrum/identities/*.json from cwd to resolve runtime,
			// but tmux's alert-silence run-shell fires from the tmux
			// server's cwd — not the agent's worktree — so identity
			// lookup was unreliable. The daemon has authoritative
			// session → identity mapping via findIdentityForSession, so
			// we send only (session, content) and let the daemon handle
			// detection as a single source of truth.
			client, err := getClient()
			if err != nil {
				return nil // Daemon not running, silently skip
			}
			defer func() { _ = client.Close() }()

			req := map[string]string{
				"session": session,
				"content": content,
			}
			var result any
			_ = client.Call("tmux.check-pane", req, &result)
			return nil
		},
	}
	// --repo is kept as a flag for backward compatibility with baked-in
	// tmux hooks from older thrum binaries. The new CLI ignores it —
	// the daemon is the single source of truth for runtime resolution.
	checkPaneCmd.Flags().String("repo", "", "Repository path (deprecated — unused; daemon resolves identity)")
	cmd.AddCommand(checkPaneCmd)

	// connect
	connectCmd := &cobra.Command{
		Use:   "connect [name]",
		Short: "Attach to a running agent's tmux session",
		Long: `Attach to a running agent's tmux session.

With a session name argument, attaches directly.
Without arguments, shows a numbered list of alive sessions to choose from.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				// Direct attach by name
				return tmuxAttach(args[0])
			}

			// Interactive: list alive sessions and let user pick
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.TmuxStatus(client)
			if err != nil {
				return err
			}

			// Filter to alive sessions only
			var alive []cli.TmuxSessionInfo
			for _, s := range result.Sessions {
				if s.State == "alive" {
					alive = append(alive, s)
				}
			}
			if len(alive) == 0 {
				fmt.Println("No alive tmux sessions")
				return nil
			}

			// Show numbered list
			fmt.Printf("%-4s %-25s %-20s %-10s %s\n", "#", "SESSION", "AGENT", "RUNTIME", "BRANCH")
			for i, s := range alive {
				agentDisplay := s.Agent
				if agentDisplay != "" {
					agentDisplay = "@" + agentDisplay
				}
				fmt.Printf("%-4d %-25s %-20s %-10s %s\n",
					i+1, s.Name, agentDisplay, s.Runtime, s.Branch)
			}

			// Read selection
			fmt.Printf("\nEnter number (1-%d): ", len(alive))
			scanner := bufio.NewScanner(os.Stdin)
			if !scanner.Scan() {
				return nil
			}
			input := strings.TrimSpace(scanner.Text())
			var choice int
			if _, err := fmt.Sscanf(input, "%d", &choice); err != nil || choice < 1 || choice > len(alive) {
				return fmt.Errorf("invalid selection: %s", input)
			}

			return tmuxAttach(alive[choice-1].Name)
		},
	}
	cmd.AddCommand(connectCmd)

	// restart
	tmuxRestartCmd := &cobra.Command{
		Use:   "restart <name>",
		Short: "Restart a tmux-managed agent session with context snapshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			rt, _ := cmd.Flags().GetString("runtime")

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			req := map[string]any{
				"name":  args[0],
				"force": force,
			}
			if rt != "" {
				req["runtime"] = rt
			}
			var result cli.TmuxRestartResponse
			if err := client.Call("tmux.restart", req, &result); err != nil {
				return err
			}

			if flagJSON {
				out, _ := json.MarshalIndent(result, "", "  ")
				fmt.Println(string(out))
			} else {
				fmt.Printf("Session %s restarted (%d snapshot lines)\n", result.Session, result.SnapshotLines)
				if result.SnapshotLines == 0 {
					fmt.Println("  ⚠ No conversation history captured — agent will start without prior context")
				}
			}
			return nil
		},
	}
	tmuxRestartCmd.Flags().Bool("force", false, "Skip graceful signal, force restart")
	tmuxRestartCmd.Flags().String("runtime", "", "Runtime override (default: same as before)")
	cmd.AddCommand(tmuxRestartCmd)

	// start — one-command launch: create session, start runtime, prime, attach
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Launch an agent session in the current directory and attach",
		Long: `Creates a tmux session, launches the configured runtime (default: claude),
runs /thrum:prime for agent registration, and attaches to the session.

The session name is derived from the current directory name.
The runtime is read from the repo's config (runtime.primary), defaulting to claude.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			// Derive session name from directory basename
			sessionName := filepath.Base(cwd)
			nameOverride, _ := cmd.Flags().GetString("name")
			if nameOverride != "" {
				sessionName = nameOverride
			}

			// Check if session already exists
			if ttmux.HasSession(sessionName) {
				fmt.Printf("Session %s already exists — attaching\n", sessionName)
				return tmuxAttach(sessionName)
			}

			// Determine runtime: --runtime flag > identity PreferredRuntime > config > "claude"
			thrumDir := filepath.Join(cwd, ".thrum")
			runtime := "claude"
			if _, err := os.Stat(thrumDir); err == nil {
				cfg, _ := config.LoadThrumConfig(thrumDir)
				if cfg.Runtime.Primary != "" {
					runtime = cfg.Runtime.Primary
				}
			}
			if idFile, _, err := config.LoadIdentityWithPath(cwd); err == nil && idFile != nil {
				if idFile.PreferredRuntime != "" {
					runtime = idFile.PreferredRuntime
				}
			}
			rtOverride, _ := cmd.Flags().GetString("runtime")
			if rtOverride != "" {
				runtime = rtOverride
			}

			// Create session via daemon
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			if _, err := cli.TmuxCreate(client, cli.TmuxCreateOptions{
				Name: sessionName, Cwd: cwd, NoAgent: true,
			}); err != nil {
				return fmt.Errorf("create session: %w", err)
			}

			// Launch runtime
			if _, err := cli.TmuxLaunch(client, cli.TmuxLaunchOptions{
				Name: sessionName, Runtime: runtime,
			}); err != nil {
				return fmt.Errorf("launch runtime: %w", err)
			}

			fmt.Printf("Session %s created with %s — waiting for startup...\n", sessionName, runtime)

			// HandleLaunch sends the prime command via a background goroutine;
			// just wait for the runtime to initialize before attaching.
			time.Sleep(10 * time.Second)
			return tmuxAttach(sessionName)
		},
	}
	startCmd.Flags().String("name", "", "Override session name (default: directory name)")
	startCmd.Flags().String("runtime", "", "Override runtime (default: from config or claude)")
	cmd.AddCommand(startCmd)

	// queue
	queueCmd := &cobra.Command{
		Use:   "queue <session> <command>",
		Short: "Submit a command to a tmux session's queue",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			session := args[0]
			text := strings.Join(args[1:], " ")
			timeoutSecs, _ := cmd.Flags().GetInt("timeout")
			wait, _ := cmd.Flags().GetBool("wait")
			silenceSecs, _ := cmd.Flags().GetFloat64("silence")

			idFile, _, err := config.LoadIdentityWithPath(flagRepo)
			if err != nil || idFile == nil {
				return fmt.Errorf("resolve requester identity: %w", err)
			}
			requester := idFile.Agent.Name

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			opts := cli.TmuxQueueOptions{
				Session:   session,
				Text:      text,
				TimeoutMs: int64(timeoutSecs) * 1000,
				Requester: requester,
			}
			if silenceSecs > 0 {
				opts.SilenceMs = int64(silenceSecs * 1000)
			}
			if wait {
				// --wait mode: caller reads result from queue-wait response,
				// so suppress the @system inbox notification.
				f := false
				opts.NotifyOnComplete = &f
			}

			resp, err := cli.TmuxQueue(client, opts)
			if err != nil {
				return err
			}
			fmt.Printf("Queued %s (position %d)\n", resp.CommandID, resp.Position)

			if wait {
				// Long-poll for the result. Buffer the RPC timeout past the
				// queue own timeout so the socket deadline does not fire first.
				waitOpts := cli.TmuxQueueWaitOptions{
					CommandID: resp.CommandID,
					TimeoutMs: int64(timeoutSecs+10) * 1000,
				}
				result, err := cli.TmuxQueueWait(client, waitOpts)
				if err != nil {
					return err
				}
				fmt.Printf("State: %s\nElapsed: %dms\n\n%s\n", result.State, result.ElapsedMs, result.Output)
			}
			return nil
		},
	}
	queueCmd.Flags().Int("timeout", 120, "Command timeout in seconds")
	queueCmd.Flags().Bool("wait", false, "Block until the command reaches a terminal state")
	queueCmd.Flags().Float64("silence", 0, "Silence threshold in seconds (fractional OK; default 5.0 server-side)")
	cmd.AddCommand(queueCmd)

	// queue-status
	queueStatusCmd := &cobra.Command{
		Use:   "queue-status <session>",
		Short: "Show the command queue for a tmux session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			resp, err := cli.TmuxQueueStatus(client, args[0])
			if err != nil {
				return err
			}
			if flagJSON {
				out, _ := json.MarshalIndent(resp, "", "  ")
				fmt.Println(string(out))
				return nil
			}
			fmt.Printf("Session: %s\n", resp.Session)
			if resp.Active != nil {
				fmt.Printf("Active: %s \"%.40s\" (%s)\n", resp.Active.ID, resp.Active.Text, resp.Active.State)
			} else {
				fmt.Println("Active: (none)")
			}
			if len(resp.Queued) == 0 {
				fmt.Println("Queued: (empty)")
			} else {
				fmt.Printf("Queued: %d commands\n", len(resp.Queued))
				for i, q := range resp.Queued {
					fmt.Printf("  [%d] %s \"%.40s\"\n", i+1, q.ID, q.Text)
				}
			}
			return nil
		},
	}
	cmd.AddCommand(queueStatusCmd)

	// cancel
	cancelCmd := &cobra.Command{
		Use:   "cancel <command-id>",
		Short: "Cancel a queued or active command",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			resp, err := cli.TmuxCancel(client, args[0])
			if err != nil {
				return err
			}
			if flagJSON {
				out, _ := json.MarshalIndent(resp, "", "  ")
				fmt.Println(string(out))
				return nil
			}
			fmt.Printf("Canceled %s (state: %s)\n", resp.CommandID, resp.State)
			return nil
		},
	}
	cmd.AddCommand(cancelCmd)

	// snapshot — save/restore/check subcommands (moved from top-level 'restart')
	snapshotCmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage conversation snapshots for session restart",
	}
	for _, sub := range restartSnapshotSubcmds() {
		snapshotCmd.AddCommand(sub)
	}
	cmd.AddCommand(snapshotCmd)

	return cmd
}

func tmuxAttach(session string) error {
	// Use safecmd.TmuxExec to replace the thrum process with tmux.
	// This makes the terminal see "tmux" as the process, which then
	// propagates session/window titles to the terminal tab correctly.
	return safecmd.TmuxExec("attach-session", "-t", session)
}
