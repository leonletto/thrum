package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/worktree"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:2609-2630
// Destination: worktree.go:26-47
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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

// ORIGIN[thrum-8kxh]: moved from main.go:2632-2798
// Destination: worktree.go:55-221
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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
				basePath = worktree.InferBasePath(repoPath)
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

			// Phase B: call headless primitive (spec §4.1 3-case mapping).
			if detach {
				// Cobra-only detach path: skip worktree.Create, run git
				// worktree add --detach inline + EnsureRedirects. The
				// headless API has no detach mode (B-B1 never needs it).
				if out, err := safecmd.Git(cmd.Context(), repoPath,
					"worktree", "add", "--detach", worktreePath); err != nil {
					return fmt.Errorf("git worktree add --detach: %s\n%s", err, out)
				}
				fmt.Printf("✓ Worktree created at %s (detached)\n", worktreePath)
				if err := cli.EnsureWorktreeRedirects(worktreePath, repoPath); err != nil {
					return fmt.Errorf("redirect setup: %w", err)
				}
			} else {
				// Branch-mode: delegate to worktree.Create with BranchOverride.
				// The cobra layer populates BasePath explicitly from config
				// (already computed above) — redundant with Create's
				// three-tier fallback but makes intent self-evident at the
				// call site. The daemon scheduler may pass BasePath:"" and
				// rely on Create's tier-2/tier-3 fallback.
				if branch == "" {
					branch = "feature/" + name
				}
				result, err := worktree.Create(cmd.Context(), worktree.CreateOpts{
					RepoPath:       repoPath,
					BasePath:       basePath, // explicit; cobra always knows
					AgentName:      name,
					Persistent:     true,
					BranchOverride: branch,
				})
				if err != nil {
					return fmt.Errorf("create worktree: %w", err)
				}
				worktreePath = result.Path
				branch = result.Branch
				if result.Reused {
					fmt.Printf("✓ Worktree reused at %s (branch %s)\n", worktreePath, branch)
				} else {
					fmt.Printf("✓ Worktree created at %s\n", worktreePath)
				}
			}
			cli.PrintRedirectConfirmations(os.Stdout, worktreePath)

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

// ORIGIN[thrum-8kxh]: moved from main.go:2800-2900
// Destination: worktree.go:229-329
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func worktreeTeardownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "teardown <name>",
		Short: "Remove a worktree and clean up thrum/beads artifacts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
				return fmt.Errorf("invalid worktree name %q: must not contain /, \\, or parent references", name)
			}
			deleteBranchFlag, _ := cmd.Flags().GetBool("delete-branch")

			repoPath := paths.EffectiveRepoPath(flagRepo)
			thrumDir := filepath.Join(repoPath, ".thrum")
			cfg, err := config.LoadThrumConfig(thrumDir)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			basePath := cfg.Worktrees.BasePath
			if basePath == "" {
				basePath = worktree.InferBasePath(repoPath)
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

			// Phase C: call headless primitive (spec §4.2 mapping table).
			// Resolve the worktree's HEAD branch when --delete-branch
			// is set so the operator doesn't have to type the branch
			// name. Per Leon Q2 lock (2026-05-15): default flag-absent
			// path passes Branch:"" so the branch stays after removal
			// (pre-refactor parity); flag-on path passes the resolved
			// HEAD short-name so worktree.Destroy deletes it.
			var branchToDelete string
			if deleteBranchFlag {
				out, err := safecmd.Git(cmd.Context(), worktreePath,
					"rev-parse", "--abbrev-ref", "HEAD")
				if err != nil {
					fmt.Fprintf(os.Stderr,
						"  Warning: --delete-branch given but HEAD resolution failed: %v (branch left in place)\n", err)
				} else {
					branchToDelete = strings.TrimSpace(string(out))
				}
			}
			res, err := worktree.Destroy(cmd.Context(), worktree.DestroyOpts{
				RepoPath:     repoPath,
				WorktreePath: worktreePath,
				Branch:       branchToDelete, // "" when flag absent
				Force:        true,
			})
			if err != nil {
				return fmt.Errorf("destroy worktree: %w", err)
			}

			fmt.Printf("✓ Worktree %s removed\n", name)
			if branchToDelete != "" {
				if res.BranchDeleted {
					fmt.Printf("✓ Branch deleted: %s\n", branchToDelete)
				} else {
					fmt.Fprintf(os.Stderr,
						"  Warning: branch %s not deleted (best-effort delete failed; see daemon logs)\n",
						branchToDelete)
				}
			}
			return nil
		},
	}
	cmd.Flags().Bool("delete-branch", false,
		"Delete the worktree's branch after removing the worktree (default: false; branch stays)")
	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:2902-2981
// Destination: worktree.go:337-416
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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

// ORIGIN[thrum-8kxh]: moved from main.go:2983-3036
// Destination: worktree.go:424-477
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
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

	return cli.EmitJSON(worktrees)
}
