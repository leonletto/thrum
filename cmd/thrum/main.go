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

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/backup"
	email "github.com/leonletto/thrum/internal/bridge/email"
	bridgepeer "github.com/leonletto/thrum/internal/bridge/peer"
	telegram "github.com/leonletto/thrum/internal/bridge/telegram"
	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	agentcontext "github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/agentdispatch"
	"github.com/leonletto/thrum/internal/daemon/agenthealth"
	"github.com/leonletto/thrum/internal/daemon/backstop"
	"github.com/leonletto/thrum/internal/daemon/bootstrap"
	"github.com/leonletto/thrum/internal/daemon/cleanup"
	"github.com/leonletto/thrum/internal/daemon/contextpoll"
	"github.com/leonletto/thrum/internal/daemon/escalation"
	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
	"github.com/leonletto/thrum/internal/daemon/inbox"
	"github.com/leonletto/thrum/internal/daemon/monitor"
	"github.com/leonletto/thrum/internal/daemon/nudge"
	"github.com/leonletto/thrum/internal/daemon/permission"
	"github.com/leonletto/thrum/internal/daemon/reconcile"
	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/daemon/sweep"
	"github.com/leonletto/thrum/internal/gitctx"
	"github.com/leonletto/thrum/internal/identity/guard"
	"github.com/leonletto/thrum/internal/netdetect"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/projection"
	"github.com/leonletto/thrum/internal/restart"
	"github.com/leonletto/thrum/internal/runtime"
	"github.com/leonletto/thrum/internal/skills"
	"github.com/leonletto/thrum/internal/subscriptions"
	thrumSync "github.com/leonletto/thrum/internal/sync"
	syncCompact "github.com/leonletto/thrum/internal/sync/compact"
	syncPending "github.com/leonletto/thrum/internal/sync/pending"
	syncSnapshot "github.com/leonletto/thrum/internal/sync/snapshot"
	syncState "github.com/leonletto/thrum/internal/sync/state"
	"github.com/leonletto/thrum/internal/timeparse"
	ttmux "github.com/leonletto/thrum/internal/tmux"
	"github.com/leonletto/thrum/internal/types"
	"github.com/leonletto/thrum/internal/web"
	"github.com/leonletto/thrum/internal/websocket"
	"github.com/leonletto/thrum/internal/worktree"
	"github.com/oklog/ulid/v2"
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

	// currentCobraCmd is set by rootCmd.PersistentPreRunE before every
	// leaf RunE so getClient() can consult the leaf's
	// cross_worktree_response annotation. cobra invokes one leaf per
	// execve so a package-level handle is safe across the request
	// lifecycle (thrum-7b84.6).
	currentCobraCmd *cobra.Command
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
sessions, worktrees, and machines using Git as the sync layer.

Environment variables:
  THRUM_NO_HINTS=1   Suppress all CLI hints (both stderr trailers and JSON
                     'hints' field). Useful in CI or scripted pipelines.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags available to all commands
	rootCmd.PersistentFlags().StringVar(&flagRole, "role", "", "Agent role (or THRUM_ROLE env var)")
	rootCmd.PersistentFlags().StringVar(&flagModule, "module", "", "Agent module (or THRUM_MODULE env var)")
	// --repo: intentionally hidden from --help. The flag is for testing
	// helpers (tests/resilience, tests/e2e) and ad-hoc scripting only. It
	// is NOT a user-facing operator override and MUST NOT appear in
	// user-facing documentation (CLI reference, llms.txt, website docs)
	// — advertising it would recruit agents to bypass the cross_worktree
	// guard from a wrong cwd, which is the exact anti-pattern the guard
	// exists to prevent. Source-divers can discover + use it for tests
	// and scripts; that's fine. See thrum-7b84.6 cycle 2026-05-16 for
	// the full context.
	rootCmd.PersistentFlags().StringVar(&flagRepo, "repo", ".", "Repository path")
	_ = rootCmd.PersistentFlags().MarkHidden("repo")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "JSON output for scripting")
	rootCmd.PersistentFlags().BoolVar(&flagQuiet, "quiet", false, "Suppress non-essential output")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "Debug output")

	// Set version for --version flag
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate("thrum v{{.Version}} (build: " + Build + ", " + goruntime.Version() + ")\n" +
		"\x1b]8;;https://github.com/leonletto/thrum\x07https://github.com/leonletto/thrum\x1b]8;;\x07\n" +
		"\x1b]8;;https://thrum.team\x07https://thrum.team\x1b]8;;\x07\n")

	// Resolve flagRepo to the nearest parent containing .thrum/ (git-style traversal).
	// Skip for "init" which creates .thrum/ and doesn't need it to exist.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// thrum-7b84.6: stash the current command so getClient() can
		// inspect its cross_worktree_response annotation. cobra's
		// flow is single-command-per-invocation so a package-level
		// var is safe; PersistentPreRunE on rootCmd fires before
		// every leaf RunE.
		currentCobraCmd = cmd

		// Install slog bridge FIRST so any code running during repo
		// resolution (worktree lookups, identity refresh) already has the
		// correct stderr/JSON routing — in --json mode slog.Warn records
		// accumulate into the hint buffer instead of corrupting stdout.
		installSlogBridge(flagJSON, os.Stderr)

		// THRUM_HOME pins runtime commands to the agent's bound checkout
		// even when cwd moves. For commands that REGISTER a new agent
		// (init, quickstart) the user's actual cwd is the explicit
		// "this is where I want the new agent" signal — applying
		// EffectiveRepoPath here silently rewrites the identity-file
		// destination to THRUM_HOME, producing cross-worktree identity
		// files (thrum-tc4w). Skip the substitution for those commands;
		// downstream still uses cwd-rooted FindThrumRoot below.
		//
		// cobra's cmd.Name() returns the leaf word, so this match also
		// catches `thrum tmux quickstart` (the alias for `thrum tmux
		// create` defined further down). Catching it is intentional and
		// correct: the inline-quickstart route through HandleCreate
		// passes --repo req.Cwd explicitly, so flagRepo's value at the
		// cobra root is only used for the daemon-connection lookup,
		// which still resolves correctly via FindThrumRoot below.
		// Future readers: if a new `tmux init` or `tmux quickstart`
		// subcommand is added with different semantics, audit this
		// branch — the leaf-word match will catch it too.
		registers := cmd.Name() == "init" || cmd.Name() == "quickstart"
		if !registers && !cmd.Flags().Changed("repo") {
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
	// thrum-7b84.3.5: `thrum cron install-inbox-poll` deprecated — the
	// daemon-side backstop ticker (internal/daemon/backstop) now handles
	// the 15-minute stale-unread reminder for all alive agents. No
	// per-agent CronCreate required; removing the registration retires
	// the durable:false cron on each runtime's next session boot.

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
	rootCmd.AddCommand(jobCmd())
	rootCmd.AddCommand(rolesCmd())
	rootCmd.AddCommand(purgeCmd())
	rootCmd.AddCommand(telegramCmd())
	rootCmd.AddCommand(emailCmd())
	rootCmd.AddCommand(tmuxCmd())
	rootCmd.AddCommand(restartCmd())
	rootCmd.AddCommand(worktreeCmd())
	rootCmd.AddCommand(skillCmd())

	// Apply guard-category annotations to every leaf command under
	// rootCmd. See command_categories.go for the per-path mapping +
	// categorization rationale; command_categories_test.go walks this
	// same tree in-test and fails if any leaf is missing a category.
	tagGuardCategories(rootCmd)

	return rootCmd
}

// installSlogBridge wires slog.Default based on whether the current command
// is running in --json mode. In JSON mode, records route through the
// cli.SlogHintHandler into the pushed-hints buffer so EmitJSON can graft
// them into the response body — stdout stays pure JSON. In human mode we
// install a plain text handler writing to stderr so users running
// `thrum ... 2> log.txt` still see warnings as before.
//
// Contract: called once per CLI invocation from rootCmd.PersistentPreRunE,
// before any code that may emit slog records. The CLI is short-lived and
// process-global slog.Default mutation is intentional. Tests that exercise
// the cobra command tree multiple times in one process MUST save and
// restore slog.Default themselves to avoid bleeding the bridge handler
// into unrelated test cases — see main_sloghint_integration_test.go for
// the pattern.
func installSlogBridge(jsonMode bool, stderr io.Writer) {
	if jsonMode {
		slog.SetDefault(slog.New(cli.NewSlogHintHandler()))
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))
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

				// Dispatch to interactive wizard on a TTY unless the user
				// asked for legacy silent mode via --non-interactive. The
				// wizard runs Init, identity registration, worktrees-root,
				// role-template, and daemon-start in one guided flow, so it
				// returns directly on success — the legacy post-init steps
				// (runtime selection, project_state, runtime configs) are
				// owned by the silent path. dry-run preserves the silent
				// preview path because the wizard always materializes state.
				nonInteractive, _ := cmd.Flags().GetBool("non-interactive")
				thrumDirExists := dirExists(filepath.Join(flagRepo, ".thrum"))
				if !dryRun && shouldUseWizard(isInteractive(), nonInteractive, thrumDirExists, force) {
					name, _ := cmd.Flags().GetString("name")
					role, _ := cmd.Flags().GetString("role")
					module, _ := cmd.Flags().GetString("module")
					worktreesRoot, _ := cmd.Flags().GetString("worktrees-root")
					rolesChoice, _ := cmd.Flags().GetString("roles")
					noDaemon, _ := cmd.Flags().GetBool("no-daemon")

					// Resolve to absolute so the wizard's later
					// `thrum --repo <path> quickstart` subprocess passes
					// an absolute path (the cobra handler's
					// worktree.NormalizeWorktreePath rejects relative
					// values like "." that the init PreRunE leaves alone).
					wizardRepo, absErr := filepath.Abs(flagRepo)
					if absErr != nil {
						wizardRepo = flagRepo
					}

					return cli.RunWizard(&cli.WizardConfig{
						RepoPath:      wizardRepo,
						Prompter:      cli.NewScannerPrompter(os.Stdin, os.Stderr),
						NameFlag:      name,
						RoleFlag:      role,
						ModuleFlag:    module,
						WorktreesRoot: worktreesRoot,
						RolesChoice:   rolesChoice,
						NoDaemon:      noDaemon,
						Force:         force,
						Stealth:       stealth,
						Runtime:       runtimeFlag,
					})
				}

				yesFlag, _ := cmd.Flags().GetBool("yes")
				opts := cli.InitOptions{
					RepoPath: flagRepo,
					Force:    force,
					Stealth:  stealth,
					Yes:      yesFlag || nonInteractive,
				}
				// Skills bootstrap (E8.3) prompts only on the v0.10.x →
				// v0.11 upgrade path. Wire a Prompter when stdin is a TTY
				// and the user hasn't asked for silent mode; otherwise
				// Yes (or its absence with no Prompter) drives auto-apply.
				if !opts.Yes && isInteractive() && cli.SkillsBootstrapNeeded(flagRepo) {
					opts.Prompter = cli.NewScannerPrompter(os.Stdin, os.Stderr)
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
				// Do NOT force SingleAgentMode here — that destructively overwrites the
				// user's existing setting on `thrum init --force` and silently breaks
				// messaging after upgrade. cfg already carries the loaded value from
				// LoadThrumConfig; zero-value (false) applies for fresh installs.
				if cfg.Worktrees.BasePath == "" {
					cfg.Worktrees = config.WorktreesConfig{
						BasePath:     worktree.InferBasePath(flagRepo),
						BeadsEnabled: true,
						ThrumEnabled: true,
					}
				}
				if cfg.Orchestration.MergeTarget == "" {
					// Detect the repo's current branch rather than
					// hardcoding "main". Agents need to merge back into
					// the branch active work is happening on — in this
					// repo that's thrum-dev, but for generic users it's
					// whatever the repo's default is. Falls back to
					// "main" only when detection fails (bare repo,
					// detached HEAD, etc.).
					mergeTarget := "main"
					if out, err := safecmd.Git(cmd.Context(), flagRepo, "symbolic-ref", "--short", "HEAD"); err == nil {
						if branch := strings.TrimSpace(string(out)); branch != "" && branch != "HEAD" {
							mergeTarget = branch
						}
					}
					cfg.Orchestration = config.OrchestrationConfig{
						MergeTarget:     mergeTarget,
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
					if err := cli.EmitJSON(result); err != nil {
						return err
					}
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

			fmt.Println()
			fmt.Println("Tip: To enable repo-knowledge queries from other agents, register a researcher:")
			fmt.Println("  thrum tmux start --role researcher")

			// Post-action hint: tip the operator to register via quickstart
			// when this machine has no identity yet. Runs only on the
			// full-init success path (the worktree-redirect branch at
			// main.go line ~285 returns earlier by design — spec §4 point 6).
			// Skip in dry-run: nothing actually got initialized.
			if !dryRun && !alreadyInitialized {
				state := cli.NewFSOnlyStateAccessor()
				postCtx := cli.HintCtx{
					Command: "init",
					Flags:   map[string]any{"repo": flagRepo},
					Post:    true,
					State:   state,
				}
				cli.EmitStderr(cli.Collect(postCtx), flagQuiet, flagJSON)
			}

			return nil
		},
	}

	cmd.Flags().Bool("force", false, "Force reinitialization / overwrite existing files")
	cmd.Flags().Bool("stealth", false, "Use .git/info/exclude instead of .gitignore (zero footprint in tracked files)")
	cmd.Flags().Bool("dry-run", false, "Preview changes without writing files")
	cmd.Flags().String("runtime", "", "Generate runtime-specific configs (claude|codex|cursor|gemini|opencode|cli-only|all)")
	cmd.Flags().Bool("skills", false, "Install thrum skill only (no MCP config, no startup script)")

	// Wizard-related flags. The wizard fires on a TTY for fresh repos (or
	// with --force) unless suppressed by --non-interactive; the per-prompt
	// flags below pre-fill answers so the wizard can be partially or fully
	// scripted. Spec: dev-docs/specs/2026-05-02-thrum-init-wizard-design.md.
	cmd.Flags().Bool("non-interactive", false, "Force silent (legacy) mode even on a TTY")
	cmd.Flags().Bool("yes", false, "Auto-confirm any safety prompts (e.g. the v0.10.x → v0.11 .gitignore upgrade)")
	cmd.Flags().String("name", "", "Pre-fill identity name (skips wizard prompt)")
	cmd.Flags().String("role", "", "Pre-fill role")
	cmd.Flags().String("module", "", "Pre-fill module")
	cmd.Flags().String("worktrees-root", "", "Pre-fill worktrees root path")
	cmd.Flags().String("roles", "", "Pre-fill role-template choice (enhanced|default|skip)")
	cmd.Flags().Bool("no-daemon", false, "Skip auto-starting daemon at end of wizard")

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
		return cli.EmitJSON(result)
	}
	if !flagQuiet {
		fmt.Print(cli.FormatSkillsInstall(result))
	}
	return nil
}

// MOVED[thrum-8kxh]: isInteractive → helpers.go:25-27
// Original range: main.go:774-776
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: d558385f83
// Phase: 1
// Remove once refactor verified green.

// shouldUseWizard returns true when `thrum init` should dispatch to the
// interactive wizard rather than the legacy silent init flow. The wizard
// fires on a TTY (unless explicitly suppressed by --non-interactive) when
// either the project has no .thrum/ yet, or the user passed --force to
// re-initialize. Spec: dev-docs/specs/2026-05-02-thrum-init-wizard-design.md.
func shouldUseWizard(isTTY, nonInteractive, thrumDirExists, force bool) bool {
	if !isTTY || nonInteractive {
		return false
	}
	return !thrumDirExists || force
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
				return cli.EmitJSON(result)
			}
			fmt.Print(cli.FormatConfigShow(result))
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
			fmt.Println("  thrum setup claude-md  — Print or install the Thrum block for CLAUDE.md")
			return nil
		},
	}
	cmd.AddCommand(setupClaudeMDCmd())
	return cmd
}

func setupClaudeMDCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claude-md",
		Short: "Print or install the Thrum-managed block for CLAUDE.md",
		Long: `Print or install a Thrum-managed block into CLAUDE.md.

Without flags the template is written to stdout for inspection or piping.
With --apply the block is installed into ./CLAUDE.md — the file is created
if missing, or the block is appended if no Thrum block exists. If a Thrum
block is already present, --apply refuses without --force. --apply --force
is idempotent: running it twice produces byte-identical file content.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			apply, _ := cmd.Flags().GetBool("apply")
			force, _ := cmd.Flags().GetBool("force")

			res, err := cli.SetupClaudeMD(cli.SetupClaudeMDOptions{
				Apply: apply,
				Force: force,
				Out:   cmd.OutOrStdout(),
			})
			if err != nil {
				return err
			}
			switch res.Mode {
			case cli.ModeCreated:
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Created %s with Thrum block.\n", res.Path)
			case cli.ModeAppended:
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Appended Thrum block to %s.\n", res.Path)
			case cli.ModeReplaced:
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Replaced Thrum block in %s.\n", res.Path)
			}
			return nil
		},
	}
	cmd.Flags().Bool("apply", false, "Write to ./CLAUDE.md instead of stdout")
	cmd.Flags().Bool("force", false, "With --apply: replace an existing Thrum block")
	return cmd
}

// MOVED[thrum-8kxh]: sendCmd → messaging.go:21-160
// Original range: main.go:1026-1165
// Tests: cmd/thrum/main_test.go (indirect via Execute()); cmd/thrum/send_test.go (t698)
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: sentCmd → messaging.go:168-225
// Original range: main.go:1167-1224
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// groupCmd and subcommands removed — groups are no longer user-facing.
// Group RPC handlers (group.go) remain for Telegram bridge (tg:* groups).

// MOVED[thrum-8kxh]: inboxCmd → messaging.go:233-371
// Original range: main.go:1229-1367
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: versionCmd → version_cmd.go:17-41
// Original range: main.go:1331-1355
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 8bca6129d7
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: printAgentSummaryField → agent.go:27-34
// Original range: main.go:1381-1388
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: agentSummaryField → agent.go:46-87
// Original range: main.go:1394-1435
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runWhoami → agent.go:98-132
// Original range: main.go:1440-1474
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: whoamiCmd → agent.go:140-159
// Original range: main.go:1476-1495
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: waitCmd → messaging.go:379-516
// Original range: main.go:1404-1541
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: daemonCmd → daemon_cmd.go:18-123
// Original range: main.go:1615-1720
// Tests: cmd/thrum/main_test.go (indirect via Execute()); cmd/thrum/daemon_bootstrap_test.go (sibling, unaffected)
// Commit: 69a0f569a9
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: daemonLogsCmd → daemon_cmd.go:131-167
// Original range: main.go:1722-1758
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 69a0f569a9
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: daemonRunCmd → daemon_cmd.go:175-184
// Original range: main.go:1760-1769
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 69a0f569a9
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: peerCmd → peer.go:24-374
// Original range: main.go:1771-2121
// Tests: cmd/thrum/peer_cli_test.go (references peerCmd by name; stays package main); cmd/thrum/main_test.go (indirect via Execute())
// Commit: 05e04ad25f
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: monitorCmd → monitor.go:19-225
// Original range: main.go:2123-2329
// Tests: cmd/thrum/main_test.go (indirect via Execute()); internal/daemon/rpc/monitor_trust_boundary_test.go (Phase 3 hazard — RPC handlers still in runDaemon, unaffected by this Phase 1 CLI-surface move)
// Commit: 4217e1fc89
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: agentCmd → agent.go:167-591
// Original range: main.go:1671-2095
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: reminderCmd → reminder.go:25-58
// Original range: main.go:2103-2136
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runReminderLookup → reminder.go:71-123
// Original range: main.go:2143-2195
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: formatReminderLookup → reminder.go:140-215
// Original range: main.go:2206-2281
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: formatLookupElapsed → reminder.go:226-252
// Original range: main.go:2286-2312
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: lastNLines → reminder.go:263-269
// Original range: main.go:2317-2323
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: reminderListCmd → reminder.go:281-303
// Original range: main.go:2329-2351
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: reminderListFlags → reminder.go:313-319
// Original range: main.go:2355-2361
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: buildReminderListOpts → reminder.go:333-347
// Original range: main.go:2369-2383
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runReminderList → reminder.go:355-399
// Original range: main.go:2385-2429
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: formatReminderListRow → reminder.go:410-422
// Original range: main.go:2434-2446
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: fireStateLabel → reminder.go:437-453
// Original range: main.go:2455-2471
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: truncateBody → reminder.go:464-472
// Original range: main.go:2476-2484
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: reminderSetCmd → reminder.go:483-509
// Original range: main.go:2489-2515
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runReminderSet → reminder.go:517-573
// Original range: main.go:2517-2573
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: parseFutureDuration → reminder.go:591-613
// Original range: main.go:2585-2607
// Tests: cmd/thrum/main_reminder_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: worktreeCmd → worktree.go:26-47
// Original range: main.go:2609-2630
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: worktreeCreateCmd → worktree.go:55-221
// Original range: main.go:2632-2798
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: worktreeTeardownCmd → worktree.go:229-329
// Original range: main.go:2800-2900
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: worktreeListCmd → worktree.go:337-416
// Original range: main.go:2902-2981
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: worktreeListJSON → worktree.go:424-477
// Original range: main.go:2983-3036
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: agentSetStatusCmd → agent.go:599-628
// Original range: main.go:2237-2266
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: setLocalAgentStatus → agent.go:636-655
// Original range: main.go:2268-2287
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: setRemoteAgentStatus → agent.go:663-678
// Original range: main.go:2289-2304
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: sessionStartRunE → session.go:21-66
// Original range: main.go:3108-3153
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: sessionEndRunE → session.go:75-118
// Original range: main.go:3156-3199
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: sessionSetIntentRunE → session.go:127-167
// Original range: main.go:3202-3242
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: sessionSetTaskRunE → session.go:176-208
// Original range: main.go:3245-3277
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: sessionCmd → session.go:216-347
// Original range: main.go:3279-3410
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: replyCmd → messaging.go:524-580
// Original range: main.go:1781-1837
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: messageCmd → messaging.go:588-828
// Original range: main.go:1839-2079
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// subscribeCmd, unsubscribeCmd, subscriptionsCmd removed —
// subscriptions are no longer a concept. Use thrum wait for CLI notifications.

// MOVED[thrum-8kxh]: contextCmd → context.go:30-43
// Original range: main.go:2086-2099
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: contextSaveCmd → context.go:51-112
// Original range: main.go:2101-2162
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: contextShowCmd → context.go:120-265
// Original range: main.go:2164-2309
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: contextClearCmd → context.go:273-317
// Original range: main.go:2311-2355
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: contextPreambleCmd → context.go:325-407
// Original range: main.go:2357-2439
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: preambleRPCCaller → context.go:417-419
// Original range: main.go:2443-2445
// Tests: cmd/thrum/preamble_init_test.go (interface fake)
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runPreambleInit → context.go:431-476
// Original range: main.go:2451-2496
// Tests: cmd/thrum/preamble_init_test.go
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

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

// MOVED[thrum-8kxh]: resolvePrimeIdentityPath → context.go:489-507
// Original range: main.go:2516-2534
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: loadPrimeOwnershipMode → context.go:519-525
// Original range: main.go:2540-2546
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: primeCmd → context.go:533-633
// Original range: main.go:2548-2648
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runtimeGroupCmd → runtime_cmd.go:18-89
// Original range: main.go:4939-5010
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: ccad4a6acc
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: contextSyncCmd → context.go:641-738
// Original range: main.go:2657-2754
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: readContextFile → context.go:747-757
// Original range: main.go:2757-2767
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: syncCmd → sync_cmd.go:26-83
// Original range: main.go:4398-4455
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: quickstartCmd → sync_cmd.go:91-371
// Original range: main.go:4457-4737
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: overviewCmd → presence.go:17-67
// Original range: main.go:5465-5515
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 8e856b779d
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: teamCmd → presence.go:75-206
// Original range: main.go:5517-5648
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 8e856b779d
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: probeAgentListFiles → presence.go:220-243
// Original range: main.go:5656-5679
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 8e856b779d
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: whoHasCmd → presence.go:251-288
// Original range: main.go:5681-5718
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 8e856b779d
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: pingCmd → presence.go:296-345
// Original range: main.go:5720-5769
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 8e856b779d
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: sessionHeartbeatRunE → session.go:356-431
// Original range: main.go:4448-4523
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: getClient → helpers.go:64-94
// Original range: main.go:5878-5908
// Tests: cmd/thrum/cross_worktree_response_test.go (indirect via classifyRefreshError); cmd/thrum/job_test.go; cmd/thrum/hints_integration_test.go
// Commit: d558385f83
// Phase: 1
// Remove once refactor verified green.
// Note (thrum-dev forward-merge): thrum-dev modified getClient's docstring
// + body (thrum-tgqx.1 refresh policy, thrum-7b84.6 per-leaf response
// classes) in-place on main.go. Those changes are transplanted onto
// cmd/thrum/helpers.go where Phase 1 moved the function — see helpers.go
// for the current implementation. Keep this tombstone intact.

// MOVED[thrum-8kxh]: classifyRefreshError → helpers.go:201-223
// Original range: main.go:5932-5954
// Tests: cmd/thrum/cross_worktree_response_test.go (TestClassifyRefreshError, TestRepoFlag_AbsorbsCrossWorktree)
// Commit: d558385f83
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: explicitRepoFlag → helpers.go:234-243
// Original range: main.go:5959-5968
// Tests: cmd/thrum/cross_worktree_response_test.go (TestExplicitRepoFlag)
// Commit: d558385f83
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: crossWorktreeResponseFor → helpers.go:255-263
// Original range: main.go:5974-5982
// Tests: cmd/thrum/cross_worktree_response_test.go (TestCrossWorktreeResponseFor)
// Commit: d558385f83
// Phase: 1
// Remove once refactor verified green.
// Note (thrum-dev forward-merge): thrum-dev modified classifyRefreshError's
// docstring + body (brainstorm §4.5 fail-closed policy + --repo escape
// hatch) in-place on main.go. Those changes are transplanted onto
// cmd/thrum/helpers.go where Phase 1 moved the function — see helpers.go
// for the current implementation. Keep this tombstone intact.

// MOVED[thrum-8kxh]: emitCrossWorktreeBanner → helpers.go:279-297
// Original range: main.go:5992-6010
// Tests: cmd/thrum/cross_worktree_response_test.go (TestEmitBanner_*)
// Commit: d558385f83
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: getClientNoRefresh → helpers.go:107-113
// Original range: main.go:6017-6023
// Tests: cmd/thrum/job_test.go (indirect via daemon RPC bind)
// Commit: d558385f83
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: resolveLocalAgentID → helpers.go:139-155
// Original range: main.go:6043-6059
// Tests: cmd/thrum/email_test.go; cmd/thrum/job_test.go; cmd/thrum/hints_integration_test.go
// Commit: d558385f83
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: resolveLocalMentionRole → helpers.go:165-171
// Original range: main.go:6063-6069
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: d558385f83
// Phase: 1
// Remove once refactor verified green.

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

	// Load config.json (used for local-only, WS port)
	thrumCfg, cfgErr := config.LoadThrumConfig(thrumDir)
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to read config.json: %v\n", cfgErr)
		thrumCfg = &config.ThrumConfig{
			Daemon: config.DaemonConfig{
				WSPort:   config.DefaultWSPort,
				LogLevel: config.DefaultLogLevel,
			},
		}
	}

	// Configure slog with the resolved log level so any subsequent calls
	// to slog.Info/Debug/Warn/Error respect the user's configured threshold.
	// Log.Printf calls continue to write unconditionally through the
	// lumberjack writer for backward compatibility.
	daemon.ConfigureSlog(logWriter, thrumCfg.Daemon.LogLevel)
	log.Printf("daemon: log level=%s", thrumCfg.Daemon.LogLevel)

	// Validate permission_supervisors invariant: the array is authoritative
	// routing for permission-prompt nudges (thrum-zmsk). If an operator
	// sets the array but forgets a coordinator-role recipient, prompts
	// can land in dead mailboxes — warn loudly so they see it on boot.
	if warn := config.ValidatePermissionSupervisors(thrumCfg.PermissionSupervisors); warn != "" {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warn)
		slog.Warn("[config] permission_supervisors validation", "issue", warn)
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

	// Create sync loop for event-triggered git sync
	ctx := context.Background()
	var syncLoop *thrumSync.SyncLoop
	var pendingPool *syncPending.Pool // thrum-s6os: nil when syncDir is absent
	if _, err := os.Stat(syncDir); err == nil {
		syncer := thrumSync.NewSyncer(absPath, syncDir, localOnly)
		syncLoop = thrumSync.NewSyncLoop(syncer, st.Projector(), absPath, syncDir, thrumDir, localOnly)
		// Route synced events through State.IngestSyncedEvent so the
		// event-write hook fires on cross-repo ingest, not just local
		// writes. Without this, replies arriving via sync from a peer
		// repo never reach the permission reply interceptor.
		syncLoop.SetIngester(st)

		// thrum-s6os v0.10.6 — wire the structural-event sync path.
		// Order is load-bearing:
		//   1. Construct triggers + sync-state writer + snapshot
		//      writers + walker.
		//   2. Triggers.SetWalker so SyncOnWrite drives the walker.
		//   3. State.SetSyncTrigger so WriteEvent fires SyncOnWrite on
		//      a structural event (spec §3.2 whitelist).
		//   4. CompactAll once before serving so the local journal +
		//      messages-v2/receipts are within retention. Non-fatal:
		//      a stale journal is recoverable; the rearchitect
		//      should still come up.
		// All wiring happens BEFORE syncLoop.Start() so there is no
		// race window where WriteEvent fires but the trigger is
		// unwired.
		triggers := thrumSync.NewTriggers(syncLoop)

		stateOwnerResolver := func(agentID string) (string, error) {
			var od string
			err := st.DB().QueryRowContext(context.Background(),
				"SELECT origin_daemon FROM agents WHERE agent_id = ?", agentID).Scan(&od)
			if errors.Is(err, sql.ErrNoRows) {
				// Unknown agent → not owned by anyone yet; the writer
				// treats this as not-owned-by-caller per its
				// ("", nil) contract.
				return "", nil
			}
			return od, err
		}
		stateBranchResolver := func(ctx context.Context, worktree string) string {
			wc, err := gitctx.ExtractWorkContext(ctx, worktree)
			if err != nil || wc == nil {
				return ""
			}
			return wc.Branch
		}
		stateWriter := syncState.NewWriter(syncDir, st.DaemonID(), stateOwnerResolver, stateBranchResolver)
		msgWriter := syncSnapshot.NewMessageStateWriter(syncDir, st.DaemonID())
		recWriter := syncSnapshot.NewReceiptStateWriter(syncDir, st.DaemonID())
		walker := syncSnapshot.NewWalker(st.DB(), stateWriter, msgWriter, recWriter, syncDir, st.DaemonID())
		triggers.SetWalker(walker)
		st.SetSyncTrigger(triggers.SyncOnWrite)

		// Bootstrap-ingest legacy events.jsonl from the sync worktree into
		// the local journal + SQLite on first daemon run after upgrade to
		// v0.10.6. Idempotent via sentinel file (.thrum/legacy_ingested).
		// Runs BEFORE CompactAll so legacy events are present before the
		// retention cutoff scan (spec §4.6, plan Task 14 anti-pattern §3).
		if rows, err := thrumSync.BootstrapIngestLegacyEvents(ctx, thrumDir, syncDir, st.DB()); err != nil {
			log.Printf("sync: legacy events bootstrap-ingest failed: %v", err)
			slog.Warn("sync.legacy_ingest_failed", "err", err)
		} else if rows > 0 {
			log.Printf("sync: bootstrap-ingested %d legacy events from sync worktree", rows)
		}

		compactor := syncCompact.New(thrumDir, syncDir,
			thrumCfg.Daemon.EventsRetentionDays,
			thrumCfg.Daemon.CompactionSizeThresholdMB)
		if err := compactor.CompactAll(ctx, st.DB()); err != nil {
			// Non-fatal — log + slog and proceed. A failed compaction
			// at startup leaves the journal in its previous state,
			// which is recoverable on the next sync-trigger.
			log.Printf("sync: startup CompactAll failed: %v", err)
			slog.Warn("compaction.startup_failed", "err", err)
		}
		// Per spec §5.3, CompactAll fires at sync-trigger time in
		// addition to daemon startup. Wire the closure so
		// Triggers.SyncOnWrite invokes compaction after the walker
		// writes succeed and before TriggerSync — any rewrite of
		// messages-v2/<id>.jsonl / receipts/<id>.jsonl folds into
		// the same commit as the walker's appends.
		triggers.SetCompactor(func(ctx context.Context) error {
			return compactor.CompactAll(ctx, st.DB())
		})

		// Construct the orphan pool and wire it into the projector so
		// applyMessageCreate can flag orphaned messages and register them
		// with the pool (thrum-s6os E11 / Task 16). The resolver must be
		// wired BEFORE syncLoop.Start so the catch-up sync on first boot
		// sees the pool-integration path.
		pendingPool = syncPending.New()
		projResolver := projection.NewProjectionResolver(st.Projector())
		st.Projector().SetPendingPool(syncDir, pendingPool)
		st.Projector().SetPendingResolver(projResolver)

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
	// B-B1 E6.11 Task 52: wire the agent_lifecycle_events store so
	// agent.ack_respawn_alert can append respawn_ack_cleared events.
	// Same setter-injection pattern as teamHandler.SetLifecycleStore
	// (Migration 27 table always present post-B-B1).
	agentHandler.SetLifecycleStore(state.NewAgentLifecycleStore(st.DB()))
	server.RegisterHandler("agent.register", agentHandler.HandleRegister)
	server.RegisterHandler("agent.list", agentHandler.HandleList)
	server.RegisterHandler("agent.whoami", agentHandler.HandleWhoami)
	server.RegisterHandler("agent.listContext", agentHandler.HandleListContext)
	server.RegisterHandler("agent.delete", agentHandler.HandleDelete)
	server.RegisterHandler("agent.cleanup", agentHandler.HandleCleanup)
	server.RegisterHandler("agent.set-status", agentHandler.HandleSetAgentStatus)
	server.RegisterHandler("agent.ack_respawn_alert", agentHandler.HandleAckRespawnAlert)

	// B-B1 E6.2 Task 26: agent.mark_state_corruption RPC for the
	// /thrum:recover-agent-state skill flow per spec §6.5. Router is
	// nil at v0.11 boot since escalation.RouteEscalation isn't yet
	// production-wired (first-call-site epic; see
	// internal/daemon/escalation/route.go). The handler falls back
	// to DB-writes-only when router is nil (per its docstring), so
	// the corruption-flag + lifecycle-event side is functional even
	// without operator paging. Escalation wiring lands when D-B1
	// + supervisor config promote past v0.11.
	stateCorruptionHandler := rpc.NewAgentStateCorruptionHandler(st, nil)
	server.RegisterHandler("agent.mark_state_corruption", stateCorruptionHandler.HandleMarkStateCorruption)

	// Reminder substrate (A-B4, v0.11) — constructed before the team
	// handler because team.list decorates each member with open reminder
	// IDs via remindersStore.OpenForAgent. Dispatcher wiring (via A-B1's
	// scheduler.RegisterInternal) lands separately in thrum-6qmf.3.27.
	remindersStore := reminders.NewSQLStore(safedb.New(st.RawDB()))
	remindersHandler := reminders.NewHandler(remindersStore)
	server.RegisterHandler("reminder.set", remindersHandler.HandleSet)
	server.RegisterHandler("reminder.get", remindersHandler.HandleGet)
	server.RegisterHandler("reminder.list", remindersHandler.HandleList)
	server.RegisterHandler("reminder.defer", remindersHandler.HandleDefer)
	server.RegisterHandler("reminder.clear", remindersHandler.HandleClear)
	server.RegisterHandler("reminder.cancel", remindersHandler.HandleCancel)

	// Team management
	teamHandler := rpc.NewTeamHandler(st, thrumDir, supervisorIdentity, remindersStore)
	// B-B1 E6.8 Task 56: wire the agent_lifecycle_events store so
	// team.list renders the §7.6 transitions field. Setter pattern
	// keeps NewTeamHandler's signature stable for tests; production
	// boot supplies the lifecycle store unconditionally since the
	// Migration 27 table is always present post-B-B1.
	teamHandler.SetLifecycleStore(state.NewAgentLifecycleStore(st.DB()))
	// E6.8 .89 body fallback chain (per spec §7.6): wire the live-pane
	// capture and outbound-message lookup so the expanded single-agent
	// view (`thrum team @<name>`) can resolve branches 1 + 3. nil-safe
	// — RenderBodyFallbackChain falls through to branch 2 (summary.md)
	// or branch 4 (no summary) if either dep yields no result.
	teamHandler.SetPaneCapture(rpc.NewTmuxPaneCapture())
	teamHandler.SetOutboundLookup(rpc.NewMessagesOutboundLookup(st))
	server.RegisterHandler("team.list", teamHandler.HandleList)
	server.RegisterHandler("team.journal", teamHandler.HandleJournal)

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

	// Session archive (thrum-6qmf.15 / v0.11): persist /thrum:restart
	// snapshots into .thrum/agents/<id>/sessions/ instead of deleting
	// them at prime time.
	sessionArchiveHandler := rpc.NewSessionArchiveHandler(st, thrumDir)
	server.RegisterHandler("session.archive", sessionArchiveHandler.HandleArchive)

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

	// Skill registration substrate (C-B1, v0.11). Constructs the full
	// substrate (Worker, Staleness, Watcher) and re-passes the live
	// instances to the SkillHandler so the C-B1 surface is INERT NO
	// MORE — promote events fan to worktree mirrors, proposed-skills
	// changes trigger staleness reminders + coordinator notifications,
	// cancel-on-promote retracts the reminder. Worker + Watcher run
	// for the daemon lifetime; their Stop calls are deferred so SIGTERM
	// triggers clean shutdown.
	//
	// reminders.Store integration (the prerequisite that was missing at
	// E10.9-commit time) landed at A-B4; this is the wiring that flips
	// the substrate from "constructed but dead-code" to live.
	skillLibrary := skills.NewLibrary(st.RepoPath())
	// Resolve the staleness-reminder window from operator config with
	// a 48h fallback per canonical default. Phase 3 dual-reviewer
	// finding — the config field existed but wasn't being read.
	skillPendingAfter := 48 * time.Hour
	pendingStr := thrumCfg.Skills.PendingReminderAfter
	if pendingStr == "" {
		pendingStr = config.DefaultSkillsPendingReminderAfter
	}
	if d, parseErr := time.ParseDuration(pendingStr); parseErr == nil {
		skillPendingAfter = d
	} else {
		slog.Warn("skill substrate: invalid skills.pending_reminder_after; falling back to 48h",
			"value", pendingStr, "err", parseErr)
	}
	skillSubstrate, err := buildSkillSubstrate(ctx, skillSubstrateOpts{
		RepoPath:       st.RepoPath(),
		ThrumDir:       thrumDir,
		Library:        skillLibrary,
		Permission:     permPkg,
		RemindersStore: remindersStore,
		DB:             st.DB(),
		PendingAfter:   skillPendingAfter,
	})
	if err != nil {
		return fmt.Errorf("build skill substrate: %w", err)
	}
	defer func() {
		// Shut down in reverse order: Watcher first (stops emitting new
		// events into the Worker), then Worker (drains in-flight applies
		// up to StopTimeout). Stop errors are surfaced via slog so
		// operators can diagnose fsnotify-close failures rather than
		// having the shutdown silently swallow them. Phase 3 finding.
		if skillSubstrate.Watcher != nil {
			if stopErr := skillSubstrate.Watcher.Stop(); stopErr != nil {
				slog.Warn("skill watcher shutdown", "err", stopErr)
			}
		}
		if skillSubstrate.Worker != nil {
			if stopErr := skillSubstrate.Worker.Stop(); stopErr != nil {
				slog.Warn("skill mirror worker shutdown", "err", stopErr)
			}
		}
	}()

	skillHandler := rpc.NewSkillHandler(
		skillLibrary,
		skills.NewValidator(),
		permPkg,
		skillSubstrate.Staleness,
		skillSubstrate.Worker,
		st.RawDB(),
	)
	server.RegisterHandler("skill.list", skillHandler.HandleList)
	server.RegisterHandler("skill.show", skillHandler.HandleShow)
	server.RegisterHandler("skill.check", skillHandler.HandleCheck)
	server.RegisterHandler("skill.check_status", skillHandler.HandleCheckStatus)
	server.RegisterHandler("skill.promote", skillHandler.HandlePromote)
	server.RegisterHandler("skill.revise", skillHandler.HandleRevise)
	server.RegisterHandler("skill.delete", skillHandler.HandleDelete)
	server.RegisterHandler("skill.sync", skillHandler.HandleSync)
	server.RegisterHandler("skill.validate", skillHandler.HandleValidate)

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

	// thrum-s6os v0.10.6: pending-pool diagnostics surface.
	// Read-only RPC; returns the current orphan list + count for
	// CLI inspection. Authentication piggybacks on the daemon's
	// existing per-connection identity resolver — no privileged
	// caller required (spec §5.4 + plan Task 13 anti-pattern #2).
	if pendingPool != nil {
		pool := pendingPool
		server.RegisterHandler("sync.pending_pool.list", func(_ context.Context, _ json.RawMessage) (any, error) {
			orphans := pool.List()
			return map[string]any{
				"size":    len(orphans),
				"orphans": orphans,
			}, nil
		})
	}

	// Tailscale peer sync management
	var syncManager *daemon.DaemonSyncManager
	var peerManager *daemon.PeerManager
	if peerRegistry != nil {
		syncManager = daemon.NewDaemonSyncManager(st, peerRegistry)
		// Create the PeerManager up front so peer.join's post-AddPeer hook
		// can bind to ConnectPeer before the peer.join RPC handler is
		// registered below. wsPort is still unresolved here; we call
		// SetLocalWSPort once it binds (see the WS server setup further
		// down). Without this ordering, peer.join would silently fall
		// through the nil-guard on the hook and the original thrum-1f4y
		// bug would quietly regress on any future WS-start reshuffle.
		peerManager = daemon.NewPeerManager(peerRegistry, "", nil)
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
	// spawnPeerBridgeFn is called by peer.join immediately after AddPeer so a
	// freshly paired dialer peer's bridge starts without waiting for the next
	// daemon restart (thrum-1f4y). Wired here (before the peer.join RPC
	// handler registers) so a future reshuffle that starts the WS server
	// earlier cannot race past a nil hook. nil-case means peerRegistry is
	// nil; peer.join is not registered in that mode either.
	var spawnPeerBridgeFn func(*daemon.PeerInfo)
	if peerManager != nil {
		spawnPeerBridgeFn = func(p *daemon.PeerInfo) {
			peerManager.ConnectPeer(ctx, p)
		}
	}
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
		//
		// NO origin_daemon filter here (unlike NotifyMessageCreate
		// below — see thrum-xfsb). Cross-repo reply delivery depends on
		// this interceptor firing for peer-synced events: when a user
		// replies to a nudge on daemon B, the reply message syncs to
		// daemon A, and daemon A's IngestSyncedEvent hook must invoke
		// AfterMessageCreate so daemon A's pending_nudges row gets
		// resolved. The AC2 guarantee "reply resolves against the
		// owning daemon's pending-nudge map only" is preserved
		// structurally: pending_nudges is per-daemon SQLite state that
		// does not replicate, so a peer daemon's AfterMessageCreate
		// call finds no matching row and silently no-ops.
		go func(evt types.MessageCreateEvent) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("[permission] intercept panic", "panic", r)
				}
			}()
			permPkg.AfterMessageCreate(context.Background(), evt)
		}(evt)

		// thrum-48kt.1: broadcast notification.message to connected
		// WebSocket clients (including OutboundRelay → Telegram). Moved
		// here from HandleSend so the broadcast covers writers that
		// bypass the RPC layer (permission.SendSupervisorMessage).
		// Without this move, nudges stayed DB-only and never forwarded
		// to Telegram. thrum-xfsb refines the policy: NotifyMessageCreate
		// itself short-circuits when evt.OriginDaemon points at a peer,
		// so synced-in replicas don't fan out to THIS daemon's local
		// bridge (which caused duplicate bot delivery in multi-daemon
		// setups).
		// Async because BroadcastAll does per-client network sends;
		// must not block the state.WriteEvent writer.
		go func(evt types.MessageCreateEvent) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("[rpc] NotifyMessageCreate panic", "panic", r)
				}
			}()
			messageHandler.NotifyMessageCreate(evt)
		}(evt)

		// thrum-wvpv: nudge tmux-managed recipients. This branch fires for
		// BOTH local writes (HandleSend) and synced writes (sync_apply →
		// State.WriteEvent), giving cross-machine and cross-repo recipients
		// the same tmux pane notification that local recipients used to
		// get exclusively. nudge.DispatchTmux is fire-and-forget; failures
		// are intentionally swallowed because nudges are advisory.
		//
		// kfn3 instrumentation: capture every dispatch attempt so phantom
		// self-echoes show up in slog with sender + recipients + origin.
		slog.Info("[nudge] nudge.dispatch entry",
			"site", "main.go:SetOnEventWrite",
			"msg_id", evt.MessageID,
			"sender", evt.AgentID,
			"recipients", evt.Recipients,
			"origin_daemon", evt.OriginDaemon,
			"session_id", evt.SessionID,
			"thrum_dir", thrumDir,
		)
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
				// thrum-kfn3: never write a spool entry to the sender's
				// own dir. HandleSend is supposed to exclude callerID
				// from Recipients, but role-group expansion and cross-
				// daemon sync mirrors have leaked it in the past. This
				// is a cheap last-line guard so a regression upstream
				// can't reach the user-visible self-echo nudge again.
				if recipient == evt.AgentID {
					slog.Info("[nudge] spool.skip self",
						"site", "main.go:WriteSpool",
						"msg_id", evt.MessageID,
						"sender", evt.AgentID,
					)
					continue
				}
				if !nudge.HasLocalIdentity(thrumDir, recipient) {
					slog.Info("[nudge] spool.skip non-local",
						"site", "main.go:WriteSpool",
						"msg_id", evt.MessageID,
						"sender", evt.AgentID,
						"recipient", recipient,
						"origin_daemon", evt.OriginDaemon,
					)
					continue
				}
				env := inbox.Envelope{
					MsgID:      evt.MessageID,
					From:       "@" + evt.AgentID,
					ReceivedAt: time.Now().UTC(),
				}
				slog.Info("[nudge] spool.write attempt",
					"site", "main.go:WriteSpool",
					"msg_id", evt.MessageID,
					"sender", evt.AgentID,
					"recipient", recipient,
					"origin_daemon", evt.OriginDaemon,
					"session_id", evt.SessionID,
				)
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
	//
	// thrum-lgv9: sync.pull / sync.peer_info / sync.notify follow the same
	// pattern — must be reachable on wsRegistry for --type local peers so
	// post-pair sync and periodic event-log replication can reach each other
	// over loopback WS. Before lgv9 only the tsnet syncRegistry had them,
	// so local peers saw persistent "Method not found" and never synced.
	var pairHandler *rpc.PairRequestHandler
	var repairHandler *rpc.PeerRepairHandler
	var syncPullHandler *rpc.SyncPullHandler
	var syncPeerInfoHandler *rpc.PeerInfoHandler
	var syncNotifyHandler *rpc.SyncNotifyHandler

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

		// thrum-lgv9: build sync handlers once so they can be registered on
		// both wsRegistry (for --type local loopback reach-back) and
		// syncRegistry (for tsnet cross-host).
		syncPullHandler = rpc.NewSyncPullHandler(st)
		syncPeerInfoHandler = rpc.NewPeerInfoHandler(st.DaemonID(), hostname)
		syncNotifyHandler = rpc.NewSyncNotifyHandler(syncManager.SyncFromPeerByID)

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
				// thrum-b6yv: stamp proxy_prefix before persisting.
				// RemoteRepoName is already populated on `peer` by
				// syncManager.JoinPeer above; DeriveProxyPrefix reads
				// it (fallback to Name) and sanitizes.
				peer.ProxyPrefix = daemon.DeriveProxyPrefix(peer)
				if updateErr := peerRegistry.AddPeer(peer); updateErr != nil {
					fmt.Fprintf(os.Stderr, "[peer.join] warning: failed to update peer transport/role: %v\n", updateErr)
				}
				// thrum-1f4y: spawn the bridge for this new peer immediately;
				// previously a daemon restart was required for ConnectAll to
				// pick it up.
				if spawnPeerBridgeFn != nil {
					spawnPeerBridgeFn(peer)
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
				// thrum-b6yv: stamp proxy_prefix before persisting.
				// RemoteRepoName is already populated on `peer` by
				// syncManager.JoinPeer above; DeriveProxyPrefix reads
				// it (fallback to Name) and sanitizes.
				peer.ProxyPrefix = daemon.DeriveProxyPrefix(peer)
				if updateErr := peerRegistry.AddPeer(peer); updateErr != nil {
					fmt.Fprintf(os.Stderr, "[peer.join] warning: failed to update peer transport/role: %v\n", updateErr)
				}
				// thrum-1f4y: spawn the bridge for this new peer immediately;
				// previously a daemon restart was required for ConnectAll to
				// pick it up.
				if spawnPeerBridgeFn != nil {
					spawnPeerBridgeFn(peer)
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
				// thrum-b6yv: stamp proxy_prefix before persisting.
				// RemoteRepoName is already populated on `peer` by
				// syncManager.JoinPeer above; DeriveProxyPrefix reads
				// it (fallback to Name) and sanitizes.
				peer.ProxyPrefix = daemon.DeriveProxyPrefix(peer)
				if updateErr := peerRegistry.AddPeer(peer); updateErr != nil {
					fmt.Fprintf(os.Stderr, "[peer.join] warning: failed to update peer transport/role: %v\n", updateErr)
				}
				// thrum-1f4y: spawn the bridge for this new peer immediately;
				// previously a daemon restart was required for ConnectAll to
				// pick it up.
				if spawnPeerBridgeFn != nil {
					spawnPeerBridgeFn(peer)
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
				// Unparseable cached old address: cannot evaluate
				// subnet equality → accept per M11 (treat like an
				// empty cached address).
				//nolint:nilerr // intentional accept-path; see comment
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
				//nolint:nilerr // intentional accept-path; see comment
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
	wsRegistry.Register("agent.mark_state_corruption", websocket.Handler(stateCorruptionHandler.HandleMarkStateCorruption))
	wsRegistry.Register("session.start", websocket.Handler(sessionHandler.HandleStart))
	wsRegistry.Register("session.end", websocket.Handler(sessionHandler.HandleEnd))
	wsRegistry.Register("session.list", websocket.Handler(sessionHandler.HandleList))
	wsRegistry.Register("session.heartbeat", websocket.Handler(sessionHandler.HandleHeartbeat))
	wsRegistry.Register("session.setIntent", websocket.Handler(sessionHandler.HandleSetIntent))
	wsRegistry.Register("session.setTask", websocket.Handler(sessionHandler.HandleSetTask))
	wsRegistry.Register("session.archive", websocket.Handler(sessionArchiveHandler.HandleArchive))
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

	// thrum-lgv9: sync.pull / sync.peer_info / sync.notify on the localhost
	// WS so --type local peers can complete post-pair sync over loopback.
	// Previously these were registered only on the tsnet syncRegistry, so
	// local peers got "RPC error -32601: Method not found" on every periodic
	// sync and sync.notify push, leaving event-log replication silently
	// broken even after the handshake succeeded.
	if syncPullHandler != nil {
		wsRegistry.Register("sync.pull", websocket.Handler(syncPullHandler.Handle))
	}
	if syncPeerInfoHandler != nil {
		wsRegistry.Register("sync.peer_info", websocket.Handler(syncPeerInfoHandler.Handle))
	}
	if syncNotifyHandler != nil {
		wsRegistry.Register("sync.notify", websocket.Handler(syncNotifyHandler.Handle))
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

	// PeerManager was created up-front (line ~5278); now that wsPort is
	// resolved, finish wiring it and register the WS accept handler.
	var wsOpts []websocket.ServerOption
	if peerManager != nil {
		peerManager.SetLocalWSPort(wsPort)
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

		// Register sync handlers on syncRegistry (tsnet). Handler instances
		// were created up-front (thrum-lgv9) so they can be shared with the
		// localhost wsRegistry; we just register them here too.
		if syncPullHandler != nil {
			_ = syncRegistry.Register("sync.pull", syncPullHandler.Handle)
		}
		if syncPeerInfoHandler != nil {
			_ = syncRegistry.Register("sync.peer_info", syncPeerInfoHandler.Handle)
		}
		if syncNotifyHandler != nil {
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

	// A-B1 scheduler primitive: construct + register 10 JSON-RPC methods
	// on both transports + the canonical internal.scheduler_event_cleanup
	// housekeeping job. wireScheduler keeps the surface small so downstream
	// epics (A-B4, D-B1, C-B1, MB-1.S6, A-B2) all RegisterInternal against
	// one canonical *Scheduler. EventRetentionDays==0 is the documented
	// "use default" sentinel (NewCleanupHandler clamps to 7 days).
	sched := wireScheduler(server, wsRegistry, st.DB(), st.Identity().DaemonID,
		thrumCfg.Daemon.Scheduler.EventRetentionDays)

	// B-B1 agent-lifecycle housekeeper: prunes agent_lifecycle_events
	// rows older than retention once per day. First B-B1 consumer of
	// scheduler.RegisterInternal; downstream B-B1 tasks (auto-respawn
	// guard, crash detection) write events that this handler bounds.
	// NewCleanupHandler clamps zero/negative retention to 7 days per
	// canonical §6.3.
	lifecycleStore := state.NewAgentLifecycleStore(st.DB())
	sched.RegisterInternal(
		"internal.agent_lifecycle_cleanup", "@daily",
		scheduler.InternalOpts{RunAtStart: false, CatchUp: "skip"},
		agentdispatch.NewCleanupHandler(lifecycleStore, thrumCfg.Daemon.AgentLifecycle.EventRetentionDays),
	)

	// A-B4 sweep config sanity check before any wiring: an AlertChain
	// containing only email entries would cause the dispatcher to
	// infinite-loop on every fire tick while EmailQueue is unwired.
	// Email delivery is now wired when thrumCfg.Email.Enabled (D-B1
	// bridge starts a queue worker that drains email_outbound_queue);
	// when disabled, rows would enqueue but never send, so the guard
	// still rejects email-only chains. Fail-loud at boot so operators
	// fix config rather than ship a daemon that retries every 30
	// seconds forever.
	hasEmailDelivery := thrumCfg.Email.Enabled
	if err := sweep.ValidateChainConfig(sweep.ChainConfig{
		AlertChain:          thrumCfg.Daemon.Sweep.AlertChain,
		SupervisorAgentName: thrumCfg.Daemon.Escalation.SupervisorAgentName,
	}, hasEmailDelivery); err != nil {
		return fmt.Errorf("invalid A-B4 sweep config: %w", err)
	}

	// A-B4 stalled-agent sweep: register internal.stalled_agent_sweep
	// before sched.Start so the scheduler picks it up on its first tick.
	// Cadence + chain config come from thrumCfg.Daemon (canonical §4.4
	// keys); zero values fall back to documented defaults.
	wireSweep(sched, remindersStore, st.DB(), thrumDir, &thrumCfg.Daemon)

	// A-B4 reminder dispatcher: register internal.reminder_dispatch with
	// the real DeliverySink (swaps the NoopFireSink placeholder from
	// thrum-6qmf.3.27). MessageHandler is the canonical agent-delivery
	// path — reminders deliver via inbox just like any agent-to-agent
	// message. EmailQueue wraps D-B1's *email.Queue when the bridge is
	// enabled; nil otherwise (DeliverySink log-and-skips email chain
	// entries). SupervisorMaybeRouter remains nil — B-B1 pane registry
	// pending; DeliverySink falls through to normal inbox send.
	//
	// Wired before LoadEmailSecrets (later in this function); the
	// internal.reminder_dispatch job is registered with RunAtStart=false
	// and a 30s minimum cadence (canonical §4.4), so the dispatcher
	// can't fire before the secrets guard either succeeds or aborts
	// the boot. Rows enqueued from the first fire wait briefly on the
	// queue worker (started in the same Email.Enabled branch below).
	var remindersEmailQueue reminders.EmailQueue
	if hasEmailDelivery {
		remindersEmailQueue = &reminderEmailQueue{
			queue:     email.NewQueue(st.DB().Raw()),
			fromAgent: supervisorID,
		}
	}
	wireReminders(sched, remindersStore, messageHandler, remindersEmailQueue, supervisorID, &thrumCfg.Daemon)

	// B-B1 E6.6 Task 63: feature-detect plumbing for stage-8 drain.
	// Probes server.HasHandler("agent.listFiles") at boot — when the
	// MB-1.S2 file-streaming substrate hasn't shipped, flips the
	// tracker into skip-drain mode so teardownGracefully's drain step
	// short-circuits (returns immediately, no 50ms polling against a
	// tracker that would never see Begin). agentDispatchDrainer flows
	// into wireScheduledAgentHandlers (line ~7990 below) which injects
	// it into ScheduledAgentHandler.Deps.Drainer — the real drain
	// path is now wired end-to-end. agentInflightTracker stays parked
	// until MB-1.S2 ships the agent.listFiles RPC adapter that wires
	// Begin/End around the in-flight RPC handler.
	agentDispatchDrainer, agentInflightTracker := wireAgentDispatch(server)

	if err := sched.Start(ctx); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}

	// B-B1 E6.5 Task 42b registration is deferred until after the
	// tmux + permission + email + agent-registry pieces are
	// constructed below — wireScheduledAgentHandlers needs them all
	// as Deps. Registration MUST happen after sched.Start (per the
	// reconcile-loop ordering invariant), which it does — the call
	// site is ~250 lines down in this same daemon-boot function.

	// wirePaneHealthCheck (B-B1 thrum-fvhs / thrum-6qmf.4.88) is
	// wired after wireScheduledAgentHandlers — see below — so
	// the real RestarterAdapter has tmuxHandler in scope.

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

	// thrum-7b84.3 E3: backstop ticker. Every 15 minutes, scan
	// message_deliveries for unread rows older than the AgeCutoff for
	// alive agents, and re-fire the existing tmux nudge. Catches the
	// push-delivery cases that tmux/spool missed (wedged pane, hook
	// didn't fire, agent in a long bash invocation that didn't yield).
	// Pattern mirrors PeriodicSyncScheduler + BackupScheduler — own
	// goroutine, own ticker. The Dispatcher is a thin shim around
	// nudge.DispatchTmux + inbox.WriteSpool that explicitly bypasses
	// OutboundRelay/Telegram (this is a forgotten-mail reminder, not a
	// paging signal).
	bs := &backstop.Backstop{
		DB:        st.DB(),
		Dispatch:  newBackstopDispatcher(thrumDir),
		AgeCutoff: 15 * time.Minute,
		Interval:  15 * time.Minute,
	}
	go bs.Run(ctx)

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

	// thrum-48kt.6: periodically reap telegram_msg_map rows that have
	// aged past the TTL and are not pinned by a live permission_nudges
	// row. Runs regardless of whether the Telegram bridge is enabled
	// in this session — the durable table persists across restarts,
	// so a daemon booted without the bridge should still compact any
	// leftover rows rather than letting them accumulate until the
	// next bridge-enabled boot. SweepLoop runs one leading sweep
	// immediately, then once per interval, until ctx is canceled.
	go telegram.SweepLoop(ctx, st.RawDB(), telegram.DefaultMapTTL, telegram.DefaultSweepInterval)

	if thrumCfg.Telegram.TelegramEnabled() {
		tgBridge := telegram.New(thrumCfg.Telegram, wsPort)
		// Wire the SQLite handle so telegram.MessageMap persists the
		// Telegram↔Thrum mapping across daemon restarts (thrum-48kt.2).
		// Without this, supervisor replies after restart silently
		// drop reply_to and TryResolve never fires.
		tgBridge.SetDB(st.RawDB())
		// Wire the pending-nudge lookup so fresh Telegram DMs
		// (y/n/yes/no/allow/deny not reply-threaded to the nudge) still
		// resolve the supervisor's most-recent pending nudge
		// (thrum-48kt.3). Keyed on the relay's userID inside the
		// InboundRelay, so a fresh 'y' from a DIFFERENT human cannot
		// inadvertently resolve someone else's pending nudge.
		pStore := permPkg.Store()
		tgBridge.SetPendingNudgeLookup(func(ctx context.Context, supervisorAgentID string) (string, error) {
			row, err := pStore.LookupMostRecentPendingNudgeByRecipient(ctx, supervisorAgentID)
			if err != nil || row == nil {
				return "", err
			}
			return row.MessageID, nil
		})
		telegramHandler.SetBridge(tgBridge)
		go tgBridge.Run(ctx)
		fmt.Fprintf(os.Stderr, "  Telegram:    bridge enabled (target: %s)\n", thrumCfg.Telegram.Target)
	}

	// Email bridge RPC handlers + goroutine (D-B1.17)
	//
	// Secrets are loaded before the handler is constructed so the daemon can
	// gate on missing/mis-permissioned secrets and fail fast rather than
	// silently degrading after startup.
	emailSecretsPath := filepath.Join(thrumDir, "secrets", "email.json")
	emailSecrets, emailSecretsErr := config.LoadEmailSecrets(emailSecretsPath, thrumCfg.Email.Enabled)
	if emailSecretsErr != nil {
		// A secrets-mode error (0644 file) is always fatal — it is a security
		// incident regardless of bridge state. A missing-file error when
		// Email.Enabled=true is also fatal: the operator explicitly asked for
		// the bridge but didn't provision credentials.
		return fmt.Errorf("email bridge startup: %w", emailSecretsErr)
	}
	emailBridge := email.New(thrumCfg.Email, emailSecrets, wsPort)
	emailBridge.SetDB(st.RawDB())
	emailHandler := rpc.NewEmailHandler(emailBridge, st.RawDB())
	server.RegisterHandler("email.send", emailHandler.HandleSend)
	server.RegisterHandler("email.peer.pair", emailHandler.HandlePeerPair)
	server.RegisterHandler("email.peer.list", emailHandler.HandlePeerList)
	server.RegisterHandler("email.peer.revoke", emailHandler.HandlePeerRevoke)
	server.RegisterHandler("email.peer.rebind", emailHandler.HandlePeerRebind)
	server.RegisterHandler("email.status", emailHandler.HandleStatus)
	server.RegisterHandler("email.unblock", emailHandler.HandleUnblock)
	if thrumCfg.Email.Enabled {
		// thrum-6qmf.8: substrate-adopt the email-bridge tickers. Internal
		// jobs are only registered when the bridge is enabled so a
		// disabled-email daemon doesn't fire no-op ticks every 5s.
		wireEmailInternal(sched, emailBridge, thrumCfg.Email)
		go emailBridge.Run(ctx)
		fmt.Fprintf(os.Stderr, "  Email:       bridge enabled (handle: %s)\n", thrumCfg.Email.DaemonHandle)
	}

	// Tmux session management handlers
	tmuxHandler := rpc.NewTmuxHandler(thrumDir, st)
	// Wire the permission scheduler so HandleCheckPane can dispatch
	// to OnDetection / OnRecovery. Without this, the permission
	// branch of HandleCheckPane is a no-op and nudges never fire.
	tmuxHandler.SetPermission(permPkg)

	// Wire the silence-hash poller. tmux's alert-silence hook is
	// unreliable on detached sessions (tmux issue #1384 — alerts are
	// processed per-session-per-client; a detached session typically
	// does not fire the hook). Thrum agents run detached by design, so
	// the daemon polls enrolled sessions directly, hashes the pane
	// tail (excluding runtime-specific volatile lines like codex's
	// "Working (Ns)" timer), and dispatches HandleCheckPane when the
	// hash stabilizes. This is the direct-control replacement for the
	// unreliable tmux hook.
	paneSilencePoller := permission.NewSessionPoller(permission.SessionPollerConfig{
		CaptureLines:   30,
		StabilityCount: 2, // 2 consecutive unchanged polls at 10s = 20s silence window
		Capture:        ttmux.CapturePane,
		OnStable: func(ctx context.Context, session, content string) error {
			params, err := json.Marshal(rpc.CheckPaneRequest{
				Session: session,
				Content: content,
			})
			if err != nil {
				return fmt.Errorf("marshal CheckPaneRequest: %w", err)
			}
			_, err = tmuxHandler.HandleCheckPane(ctx, params)
			return err
		},
	})
	tmuxHandler.SetPoller(paneSilencePoller)
	// Cold-start reconciliation: enroll any currently-live thrum-managed
	// tmux sessions that existed before daemon restart.
	if n := tmuxHandler.ReconcilePoller(ctx); n > 0 {
		slog.Info("[poller] cold-start reconciliation complete", "enrolled", n)
	}

	// Boot reconcile pass: restore (sessions, session_refs) rows for every
	// identity file on disk so write RPCs from any registered worktree
	// succeed without re-running thrum quickstart. Local-only by design;
	// see dev-docs/specs/2026-05-04-identity-reconcile-on-boot-design.md.
	//
	// The 10s timeout bounds boot in case `git worktree list` hangs (lock
	// contention, NFS). Failure is non-fatal — daemon proceeds.
	{
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		// thrumDir is the function-local variable resolved earlier in
		// daemonRun. DO NOT switch to os.Getenv("THRUM_HOME") or
		// paths.EffectiveRepoPath — that re-introduces the v0.10.1
		// regression hazard. *state.State has no ThrumDir() accessor;
		// use the local variable directly.
		rstats, rerr := bootstrap.Reconcile(rctx, bootstrap.Deps{
			State:        st,
			ThrumDir:     thrumDir,
			TmuxHandler:  tmuxHandler,
			Now:          time.Now,
			NewSessionID: func() string { return ulid.Make().String() },
			TmuxAlive:    ttmux.HasSession,
			Log:          slog.Default(),
		})
		cancel()
		if rerr != nil {
			slog.Warn("[reconcile] boot reconcile failed", "err", rerr)
		} else {
			slog.Info("[reconcile] boot reconcile complete",
				"scanned", rstats.Scanned,
				"sessions_created", rstats.SessionsCreated,
				"refs_created", rstats.RefsCreated,
				"tmux_bindings_restored", rstats.TmuxBindingsRestored,
				"errors", rstats.Errors)
		}
	}

	// Start the poll loop. Stops when ctx is canceled by the daemon
	// shutdown sequence.
	go paneSilencePoller.Run(ctx, 10*time.Second)

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

	// Context-usage poller (CR.2 / thrum-6qmf.1.13). Per-runtime context-window
	// polling: warns agents at WarnThreshold (default 70%), pre-fires at
	// AutoThreshold (default 80%) with a 3-minute countdown before the daemon
	// would force-fire a restart. OnFire is wired as a slog.Info stub for T2.3;
	// CR.4 T4.3 (thrum-6qmf.1.18) swaps in the real RestartTrigger adapter.
	//
	// tmuxHandler.SendSystemNudge is the exported dual-signal helper added in
	// T2.3 — the plan example called the unexported tmuxHandler.sendSystemMessage
	// directly from main.go, which doesn't compile across the package boundary.
	// The wrapper at internal/daemon/rpc/queue_rpc.go bundles sendSystemMessage +
	// resolveNudgeTarget + ttmux.InterruptNudge into one exported entry point so
	// these callback closures stay readable. Surfaced for cr_spec as a plan v1.3
	// erratum candidate.
	warnCfg := thrumCfg.Restart
	ctxPoller := contextpoll.NewPoller(contextpoll.PollerConfig{
		PollInterval:    30 * time.Second,
		PreFireWait:     3 * time.Minute,
		InFlightMaxWait: 5 * time.Minute,
		WarnThreshold:   warnCfg.WarnThresholdValue(),
		AutoThreshold:   warnCfg.AutoThresholdValue(),
	})
	// RestartTrigger adapter (CR.4 T4.1 / thrum-6qmf.1.16). Wraps the
	// snapshot-write + tmuxHandler.RestartSession + ctxPoller.PostRestart
	// sequence so OnFire can invoke a single Restart(ctx, agentName, reason)
	// call without the contextpoll package gaining a dependency on the rpc
	// package (spec §5.2 import-cycle constraint).
	//
	// Snapshot semantics: writes the YAML-frontmatter snapshot to
	// thrumDir/restart/<agent>.md BEFORE invoking RestartSession. Because
	// RestartSession's force-flow fallback (rpc/tmux.go:1198) checks
	// SnapshotExists and skips re-extraction if a snapshot is already
	// present, our pre-write WINS — the agent's resume plan will record
	// reason="automatic context-threshold restart at N%" rather than
	// RestartSession's hardcoded "external". Snapshot write is best-effort:
	// a failure to locate JSONL or extract content does not block the
	// restart itself; the new session simply starts without a prose
	// continuation.
	restartTrigger := contextpoll.RestartTriggerFunc(func(ctx context.Context, agentName, reason string) error {
		// Resolve identity from the shared identities dir. For worktrees
		// that DON'T share thrumDir via redirect this lookup misses; the
		// adapter then falls through to RestartSession with no snapshot
		// pre-write, and RestartSession's own force-flow extraction takes
		// over (with reason="external"). Most production agents share the
		// redirect, so this is the hot path.
		idPath := filepath.Join(thrumDir, "identities", agentName+".json")
		if data, readErr := os.ReadFile(idPath); readErr == nil { // #nosec G304 -- identity path within thrumDir
			var idFile config.IdentityFile
			if jsonErr := json.Unmarshal(data, &idFile); jsonErr == nil {
				homeDir, _ := os.UserHomeDir()
				claudeDir := filepath.Join(homeDir, ".claude")
				var jsonlPath string
				if idFile.AgentPID > 0 {
					jsonlPath, _ = restart.FindSessionJSONL(claudeDir, idFile.AgentPID)
				}
				if jsonlPath == "" && idFile.Worktree != "" {
					jsonlPath, _ = restart.FindLatestJSONLForCwd(claudeDir, idFile.Worktree)
				}
				if jsonlPath != "" {
					maxLines := warnCfg.RestartMaxLines()
					if conversation, extractErr := restart.ExtractConversation(jsonlPath, maxLines); extractErr == nil {
						snapshot := restart.FormatRestartSnapshot(agentName, idFile.SessionID, reason, conversation)
						if saveErr := restart.SaveSnapshot(thrumDir, agentName, snapshot); saveErr != nil {
							// Best-effort: log and proceed to RestartSession. The
							// new session starts without a prose continuation
							// — recoverable.
							slog.Warn("[contextpoll] save snapshot failed; restart proceeds without prose continuation",
								"agent", agentName, "reason", reason, "err", saveErr)
						}
					}
				}
			}
		}
		// Trigger the restart. RestartSession sees the snapshot we just wrote
		// (if any) and skips its own extraction. Force=true bypasses the
		// graceful-snapshot wait.
		if _, err := tmuxHandler.RestartSession(ctx, agentName, rpc.RestartSessionOpts{Force: true}); err != nil {
			return fmt.Errorf("restart %s (reason: %s): %w", agentName, reason, err)
		}
		// Clear poller state so the new session is re-evaluated from a fresh
		// baseline. PostRestart drops the sticky parser choice + threshold
		// debounce flags + restartInFlight guard; the next poll observes the
		// new session's transcript path (via the wiring's re-Enroll path on
		// identity refresh) and starts cycling from 0%.
		ctxPoller.PostRestart(agentName)
		return nil
	})
	onWarn, onPreFire, onFire := buildContextPollCallbacks(warnCfg, tmuxHandler, restartTrigger)
	ctxPoller.OnWarn(onWarn)
	ctxPoller.OnPreFire(onPreFire)
	ctxPoller.OnFire(onFire)
	// Register per-runtime parsers. Order is first-Matches-wins — parsers
	// must have non-overlapping first-line anchors. OpenCodeParserV1 keys
	// on the SQLite magic in the first 16 bytes, CodexParserV1 keys on
	// type=="session_meta" + payload.originator=="codex_cli_rs", and
	// ClaudeParserV2x keys on any non-empty top-level "type" field. The
	// more-specific anchors are registered first so they win when both
	// would match — Claude's bare "type" presence would also match a
	// Codex session_meta line, so Codex must come before Claude. OpenCode
	// is binary (SQLite) and disjoint from both JSONL anchors.
	ctxPoller.RegisterParser(contextpoll.OpenCodeParserV1{})
	ctxPoller.RegisterParser(contextpoll.CodexParserV1{})
	ctxPoller.RegisterParser(contextpoll.ClaudeParserV2x{})

	// Enroll pre-existing sessions at boot. Walks .thrum/identities/ rather
	// than reusing the bootstrap.Reconcile pass above because the poller is
	// authoritative for its own enrollment set — keeping the two walks
	// independent means a future identity-format change touches one site, not
	// both. TranscriptPath may be empty here if the agent's session hasn't
	// produced a JSONL file yet; the Poller silently skips empty paths and the
	// wiring re-Enrolls when identity refresh lands the resolved path.
	{
		homeDir, _ := os.UserHomeDir()
		claudeDir := filepath.Join(homeDir, ".claude")
		identitiesDir := filepath.Join(thrumDir, "identities")
		if entries, err := os.ReadDir(identitiesDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
					continue
				}
				idPath := filepath.Join(identitiesDir, entry.Name())
				data, err := os.ReadFile(idPath) // #nosec G304 -- path is .thrum/identities/<name>.json
				if err != nil {
					continue
				}
				var idFile config.IdentityFile
				if err := json.Unmarshal(data, &idFile); err != nil {
					continue
				}
				if idFile.Reserved || idFile.Agent.Name == "" {
					continue
				}
				var transcript string
				if idFile.AgentPID > 0 {
					transcript, _ = restart.FindSessionJSONL(claudeDir, idFile.AgentPID)
				}
				if transcript == "" && idFile.Worktree != "" {
					transcript, _ = restart.FindLatestJSONLForCwd(claudeDir, idFile.Worktree)
				}
				ctxPoller.Enroll(idFile.Agent.Name, contextpoll.AgentEnrollment{
					TranscriptPath: transcript,
					AgentPID:       idFile.AgentPID,
					AgentCwd:       idFile.Worktree,
					Runtime:        idFile.Runtime,
					SessionID:      idFile.SessionID,
				})
			}
		}
	}

	// Wire ContextProvider into TeamHandler. CR.6 T6.1 (thrum-6qmf.1.20)
	// consumes the cached usage to render the context-% column in
	// `thrum team`; T2.3 only wires the setter.
	teamHandler.SetContextProvider(ctxPoller)

	// Start the poll loop. Stops when ctx is canceled by the daemon
	// shutdown sequence. PollInterval is taken from PollerConfig (30s).
	go ctxPoller.Run(ctx, 30*time.Second)

	// B-B1 E6.5 Task 42b: register the REAL ScheduledAgentHandler +
	// NudgeHandler with their full Deps adapter chain. Replaces the
	// 42a placeholder pattern — every fire now hits real business
	// logic (worktree.Create at Stage 3, tmux session create at Stage
	// 4, etc.). Consumes the E6.6 parked-vars (agentDispatchDrainer
	// for stage-8 RPC drain; agentInflightTracker stays in the
	// closure scope for future MB-1.S2 wiring once agent.listFiles
	// ships).
	//
	// Registration ordering is honored: this fires AFTER sched.Start
	// (line above) so the per-handler-registration reconcile loop
	// in scheduler.go walks any non-terminal rows under these types
	// and routes them through the real Reconcile (stubbed to
	// StateFailed via reconcilerStub until E6.9 ships the real
	// recovery logic).
	//
	// Email is left nil in escalation.Deps — the bridge exists but
	// agentdispatch escalations fall back to the supervisor agent
	// for v0.11; D-B1's email-route lands at a follow-on dispatch
	// once the operator-address config plumbing is finalized.
	agentRegistry := agent.NewSQLiteRegistry(safedb.New(st.DB().Raw()))
	// Single MessageRPCAdapter instance — agentdispatch.MessageRPC
	// and escalation.MessageRPC have the identical signature, so one
	// concrete adapter satisfies both interface boundaries (avoids
	// constructing the same adapter twice with slightly different
	// types).
	escalationMessage := agentdispatch.NewMessageRPCAdapter(messageHandler, supervisorID)
	bootReconciler, err := wireScheduledAgentHandlers(sched, scheduledAgentDeps{
		RepoPath:       absPath,
		TmuxHandler:    tmuxHandler,
		MessageHandler: messageHandler,
		CallerAgentID:  supervisorID,
		AgentRegistry:  agentRegistry,
		MirrorWorker:   skillSubstrate.Worker,
		EscalationDeps: escalation.Deps{
			Message: escalationMessage,
			Config: escalation.Config{
				EmailEnabled:        false,
				SupervisorAgentName: thrumCfg.Daemon.Escalation.SupervisorAgentName,
			},
		},
		Drainer:     agentDispatchDrainer,
		DaemonState: st,
	})
	if err != nil {
		return fmt.Errorf("wire scheduled-agent handlers: %w", err)
	}
	// agentInflightTracker stays parked: MB-1.S2 will wire the
	// agent.listFiles RPC's Begin/End calls into it once that
	// substrate ships.
	_ = agentInflightTracker

	// B-B1 E6.7 / thrum-fvhs / thrum-6qmf.4.88: register the periodic
	// pane-health monitor. Iterates every auto-respawn-eligible agent
	// each tick (30s cadence per dispatch), probes its tmux pane, and
	// routes pane-gone events through agentdispatch.Respawner.OnPaneGone
	// — the canonical 5-step flow that appends crash_detected, runs
	// the gate predicate + loop guard, and fires respawn via the real
	// RestarterAdapter wrapping tmuxHandler.RestartSession.
	//
	// thrum-fvhs shipped DETECTION (placeholderRestarter returning
	// ErrHandlerWiringPending); thrum-6qmf.4.88 closes the RESTART
	// half by wiring the real adapter via agentdispatch.NewRestarterAdapter.
	// Closes spec §9.8.4 PARTIAL → FULL PASS.
	if err := wirePaneHealthCheck(sched, agentRegistry, st, tmuxHandler); err != nil {
		return fmt.Errorf("wire pane-health check: %w", err)
	}

	// B-B1 E6.9 / thrum-6qmf.4.15-.19: orphan-worktree filesystem
	// sweep + boot-time pane-health pass. Per spec §7.7 + plan
	// §3408-3429 Option B sequencing — runs synchronously AFTER
	// wireScheduledAgentHandlers' per-handler reconcile walk
	// completes (which transitioned non-terminal scheduler rows
	// through BootReconciler.ReconcileRun), so the
	// NonTerminalWorktrees set the sweep cross-references reflects
	// the post-reconcile state. Both calls are best-effort; errors
	// are logged at Warn level and do not fail boot.
	if err := bootReconciler.SweepOrphans(ctx); err != nil {
		slog.Warn("E6.9 orphan sweep returned error at boot; continuing",
			"err", err)
	}
	bootRespawner := buildPaneHealthRespawner(agentRegistry, st, tmuxHandler)
	if err := agenthealth.BootPass(ctx, agentRegistry, tmuxPaneProber{},
		agenthealth.WrapAgentdispatchRespawner(bootRespawner), nil); err != nil {
		slog.Warn("E6.9 boot pane-health pass returned error; continuing",
			"err", err)
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

// contextpollNudger is the narrow surface buildContextPollCallbacks needs
// from TmuxHandler. Extracting it as an interface lets the CR.3 T3.1 / T3.2
// (thrum-6qmf.1.14 / .15) unit tests inject a fake nudger and inspect the
// recorded message bodies + per-tier IsAutoDisabled gating without standing
// up a daemon.
type contextpollNudger interface {
	SendSystemNudge(ctx context.Context, recipient, body string)
}

// buildContextPollCallbacks returns the three callback closures that the
// contextpoll.Poller invokes at threshold crossings. Factored out of
// daemonRun's body for CR.3 T3.1 / T3.2 + CR.4 T4.2 testability:
//
//   - OnWarn (§3.4.1 + spec §3.1.4): fires for every agent that crosses the
//     warn tier, INCLUDING agents listed in AutoDisabledAgents. Operator
//     visibility is preserved — they still see the discipline reminder even
//     though force-fire is suppressed for them.
//   - OnPreFire (§3.4.2 + spec §3.1.4): suppressed for AutoDisabledAgents.
//     The pre-fire nudge is the last warning before force-fire; if
//     force-fire is disabled, the pre-fire message would be false-urgency.
//   - OnFire (§3.1.4 + spec §3.5): suppressed for AutoDisabledAgents. For
//     enabled agents, delegates to the supplied RestartTrigger which performs
//     the snapshot-write + RestartSession + PostRestart sequence. A failure
//     is logged at Warn level so the in-flight guard still trips (preventing
//     a tight retry loop) and the InFlightMaxWait backstop eventually clears
//     it. A nil trigger short-circuits to the T2.3 slog.Info breadcrumb —
//     useful for the small set of tests that don't care about the force-fire
//     side of the contract.
//
// Body texts are the canonical plan §3.4.1 + brainstorm §Q2 / §Q4 prose,
// inlined byte-for-byte. Future body refinements live here, not in the
// daemon wiring.
func buildContextPollCallbacks(
	warnCfg config.RestartConfig,
	sender contextpollNudger,
	trigger contextpoll.RestartTrigger,
) (
	contextpoll.WarnCallback,
	contextpoll.PreFireCallback,
	contextpoll.FireCallback,
) {
	onWarn := func(ctx context.Context, agentName string, usage contextpoll.ContextUsage) {
		// Disabled agents still receive the warn nudge per spec §3.1.4.
		body := fmt.Sprintf(
			"Context at %d%%. Wrap up your current sub-task and run `/thrum:restart`.\n\n"+
				"Do NOT dispatch sub-agents (Agent, Explore, etc.).\n"+
				"Do NOT re-read large files.\n"+
				"Do NOT spawn web fetches.\n\n"+
				"Write your continuation from working context directly. If you don't "+
				"self-restart by %d%%, the daemon will force-restart you in three "+
				"minutes and your new session will receive a 200-line transcript tail "+
				"instead of your prose continuation.",
			usage.UsedPercentage, warnCfg.AutoThresholdValue(),
		)
		sender.SendSystemNudge(ctx, agentName, body)
	}
	onPreFire := func(ctx context.Context, agentName string, _ contextpoll.ContextUsage) {
		if warnCfg.IsAutoDisabled(agentName) {
			return
		}
		body := "Restart imminent in three minutes. Last chance to self-restart.\n" +
			"Run `/thrum:restart` now to preserve your prose continuation."
		sender.SendSystemNudge(ctx, agentName, body)
	}
	onFire := func(ctx context.Context, agentName string, usage contextpoll.ContextUsage) {
		if warnCfg.IsAutoDisabled(agentName) {
			return
		}
		if trigger == nil {
			// T2.3-shape stub: no trigger wired. Log the breadcrumb so an
			// operator running an older daemon (pre-CR.4 land) still sees
			// the auto path would have fired. Also covers tests that
			// build the callbacks with a nil trigger to assert only the
			// warn / pre-fire halves of the contract.
			slog.Info("[contextpoll] OnFire stub — no RestartTrigger wired",
				"agent", agentName, "usage_pct", usage.UsedPercentage)
			return
		}
		reason := fmt.Sprintf("automatic context-threshold restart at %d%%", usage.UsedPercentage)
		if err := trigger.Restart(ctx, agentName, reason); err != nil {
			// Don't return the error — there's no caller to surface it to.
			// The in-flight guard set by the Poller will prevent a tight
			// retry loop; the InFlightMaxWait backstop (default 5min)
			// re-arms the callback after the wall-clock window.
			slog.Warn("[contextpoll] OnFire: RestartTrigger failed",
				"agent", agentName, "usage_pct", usage.UsedPercentage,
				"reason", reason, "err", err)
		}
	}
	return onWarn, onPreFire, onFire
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

// MOVED[thrum-8kxh]: rolesCmd → roles.go:23-41
// Original range: main.go:8574-8592
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: rolesSaveConfigCmd → roles.go:53-66
// Original range: main.go:8598-8611
// Tests: cmd/thrum/roles_save_config_test.go; cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runRolesSaveConfig → roles.go:78-107
// Original range: main.go:8617-8646
// Tests: cmd/thrum/roles_save_config_test.go
// Commit: 9946f64a8c
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: rolesTemplatesCmd → roles.go:119-126
// Original range: main.go:8652-8659
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: rolesTemplatesPrintCmd → roles.go:134-154
// Original range: main.go:8661-8681
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: rolesRefreshCmd → roles.go:167-183
// Original range: main.go:8688-8704
// Tests: cmd/thrum/roles_refresh_test.go; cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runRolesRefresh → roles.go:196-242
// Original range: main.go:8711-8757
// Tests: cmd/thrum/roles_refresh_test.go
// Commit: 9946f64a8c
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: rolesListCmd → roles.go:250-284
// Original range: main.go:8759-8793
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: rolesDeployCmd → roles.go:292-349
// Original range: main.go:8795-8852
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 9946f64a8c
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: applyRolePreamble → roles.go:359-402
// Original range: main.go:8856-8899
// Tests: cmd/thrum/main_test.go (indirect via Execute()); cross-phase caller from Phase 2's quickstartCmd (sync_cmd.go)
// Commit: 9946f64a8c
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: backupCmd → backup.go:24-75
// Original range: main.go:8901-8952
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 56560885ec
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: scheduleCmd → backup.go:83-128
// Original range: main.go:8954-8999
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 56560885ec
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runBackupSchedule → backup.go:136-205
// Original range: main.go:9001-9070
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 56560885ec
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runBackupCreate → backup.go:213-277
// Original range: main.go:9072-9136
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 56560885ec
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runBackupStatus → backup.go:285-362
// Original range: main.go:9138-9215
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 56560885ec
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runBackupConfig → backup.go:370-408
// Original range: main.go:9217-9255
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 56560885ec
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runBackupRestore → backup.go:416-507
// Original range: main.go:9257-9348
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 56560885ec
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: pluginCmd → plugin.go:19-65
// Original range: main.go:9350-9396
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 184cf28e7f
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runPluginAdd → plugin.go:73-110
// Original range: main.go:9398-9435
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 184cf28e7f
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runPluginList → plugin.go:118-147
// Original range: main.go:9437-9466
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 184cf28e7f
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runPluginRemove → plugin.go:155-176
// Original range: main.go:9468-9489
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 184cf28e7f
// Phase: 1
// Remove once refactor verified green.

// Cleanup: removed dangling orphan doc comment formerly at main.go:9491 — documented a long-inlined-or-removed helper; no associated function body existed.

// MOVED[thrum-8kxh]: telegramCmd → telegram.go:23-32
// Original range: main.go:9493-9502
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: c1143d7462
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: telegramConfigureCmd → telegram.go:40-80
// Original range: main.go:9504-9544
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: c1143d7462
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runTelegramConfigure → telegram.go:88-206
// Original range: main.go:9546-9664
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: c1143d7462
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: telegramStatusCmd → telegram.go:214-222
// Original range: main.go:9666-9674
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: c1143d7462
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runTelegramStatus → telegram.go:230-300
// Original range: main.go:9676-9746
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: c1143d7462
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: telegramPairCmd → telegram.go:308-331
// Original range: main.go:9748-9771
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: c1143d7462
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: runTelegramPair → telegram.go:339-400
// Original range: main.go:9773-9834
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: c1143d7462
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: isValidBotToken → telegram.go:408-426
// Original range: main.go:9836-9854
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: c1143d7462
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: maskToken → telegram.go:434-439
// Original range: main.go:9856-9861
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: c1143d7462
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: detectGitUser → telegram.go:447-458
// Original range: main.go:9863-9874
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: c1143d7462
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: restartCmd → restart_cmd.go:21-29
// Original range: main.go:9876-9884
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: f516ec027e
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: restartSnapshotSubcmds → restart_cmd.go:39-213
// Original range: main.go:9888-10062
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: f516ec027e
// Phase: 1
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: tmuxCmd → tmux.go:26-691
// Original range: main.go:5140-5805
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.

// MOVED[thrum-8kxh]: tmuxAttach → tmux.go:699-704
// Original range: main.go:5807-5812
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove once refactor verified green.
