package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	agentcontext "github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/identity/guard"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/process"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:2086-2099
// Destination: context.go:30-43
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 157524b058
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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

// ORIGIN[thrum-8kxh]: moved from main.go:2101-2162
// Destination: context.go:51-112
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 157524b058
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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

// ORIGIN[thrum-8kxh]: moved from main.go:2164-2309
// Destination: context.go:120-265
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 157524b058
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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

// ORIGIN[thrum-8kxh]: moved from main.go:2311-2355
// Destination: context.go:273-317
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 157524b058
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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

// ORIGIN[thrum-8kxh]: moved from main.go:2357-2439
// Destination: context.go:325-407
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 157524b058
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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
				role, _ := resolveLocalMentionRole()
				return runPreambleInit(client, agentID, role, absRepo, agentID)
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

// preambleRPCCaller is the minimal RPC surface runPreambleInit needs.
// Defined as an interface so tests can supply a fake.
// ORIGIN[thrum-8kxh]: moved from main.go:2443-2445
// Destination: context.go:417-419
// Tests: cmd/thrum/preamble_init_test.go (interface fake)
// Commit: 157524b058
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
type preambleRPCCaller interface {
	Call(method string, params any, result any) error
}

// runPreambleInit resets the agent's preamble to the role-aware default,
// preferring the rendered role template at .thrum/role_templates/<role>.md
// when present. Falls back to the generic RoleAwarePreamble when no
// rendered template is available (or rendering fails).
// ORIGIN[thrum-8kxh]: moved from main.go:2451-2496
// Destination: context.go:431-476
// Tests: cmd/thrum/preamble_init_test.go
// Commit: 157524b058
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func runPreambleInit(client preambleRPCCaller, agentID, role, repoPath, agentName string) error {
	if strings.ContainsAny(agentName, "/\\") || strings.Contains(agentName, "..") {
		return fmt.Errorf("invalid agent name %q: must not contain /, \\, or parent references", agentName)
	}
	thrumDir := filepath.Join(repoPath, ".thrum")
	// Resolve the strategies root by following .thrum/redirect when
	// present. Worktrees only carry redirect + identities/ + context/
	// + restart/; strategies/ and llms.txt live at the MAIN repo's
	// .thrum/. Without this redirect-follow, a worktree-side caller
	// would substitute its OWN path into the strategies bullets and
	// produce file paths that don't exist (thrum-5hhx). Mirrors the
	// gate in buildTemplateData (internal/context/context.go) so the
	// fallback path and the rendered-template path agree on which
	// directory hosts the strategies block.
	strategiesRoot := repoPath
	if _, err := os.Stat(filepath.Join(thrumDir, "redirect")); err == nil {
		if resolved, rerr := paths.ResolveThrumDir(repoPath); rerr == nil {
			strategiesRoot = filepath.Dir(resolved)
		}
	}
	// Initial fallback content uses RoleAwarePreambleWithRoot so the
	// no-role-template path renders absolute strategies/llms.txt paths.
	// strategiesRoot is the MAIN repo (post-redirect) so worktree
	// agents that hit this fallback get paths under the main repo's
	// .thrum/ where the files actually live (thrum-rm4x + thrum-5hhx).
	content := agentcontext.RoleAwarePreambleWithRoot(role, strategiesRoot)
	if rendered, renderErr := agentcontext.RenderRoleTemplate(thrumDir, agentName, role); renderErr == nil && rendered != nil {
		content = rendered
	} else if renderErr != nil && !os.IsNotExist(renderErr) {
		// Surface genuine render failures (parse errors, permission issues) as
		// hints via slog → installSlogBridge, then fall through to the generic
		// RoleAwarePreamble. IsNotExist is the no-template-deployed case and
		// is not surfaced.
		slog.Warn("context.preamble.render-failed Falling back to generic preamble.", "error", renderErr)
	}
	var resp rpc.PreambleSaveResponse
	if err := client.Call("context.preamble.save", rpc.PreambleSaveRequest{
		AgentName: agentID,
		Content:   content,
		RepoPath:  repoPath,
	}, &resp); err != nil {
		return err
	}
	fmt.Println(resp.Message)
	return nil
}

// resolvePrimeIdentityPath resolves the on-disk identity file path for
// the current agent and returns the closest-runtime PID plus the
// stored AgentPID so the caller can compare before writing. Returns
// ok=false when the agent has no identity file yet (first-prime) —
// G5 + WritePID are no-ops in that case.
// ORIGIN[thrum-8kxh]: moved from main.go:2516-2534
// Destination: context.go:489-507
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 157524b058
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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
// ORIGIN[thrum-8kxh]: moved from main.go:2540-2546
// Destination: context.go:519-525
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 157524b058
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func loadPrimeOwnershipMode(repoPath string) guard.Mode {
	m := guard.LoadConfigFromDir(repoPath).PrimeOwnership
	if m == "" {
		return guard.ModeStrict
	}
	return m
}

// ORIGIN[thrum-8kxh]: moved from main.go:2548-2648
// Destination: context.go:533-633
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 157524b058
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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

				// Wire RestartSnapshot + SessionDiscoveryHint via
				// session.archive RPC (Q-Spec-1 adaptation per Task 7).
				// Extracted into wireSessionArchiveResponse so the
				// load-bearing CLI wire is testable in isolation; see
				// prime_session_archive.go + prime_session_archive_test.go.
				wireSessionArchiveResponse(client, result)

				// Identity refresh and TmuxMode detection are now handled
				// inside getClient() → RefreshLocalIdentity and ContextPrime
				// respectively. See thrum-pxz.5 and thrum-pxz.7.
			}

			if flagJSON {
				if err := cli.EmitJSON(result); err != nil {
					return err
				}
			} else {
				fmt.Print(cli.FormatPrimeContext(result))
			}

			return nil
		},
	}
}

// ORIGIN[thrum-8kxh]: moved from main.go:2657-2754
// Destination: context.go:641-738
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 157524b058
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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
// ORIGIN[thrum-8kxh]: moved from main.go:2757-2767
// Destination: context.go:747-757
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 157524b058
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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
