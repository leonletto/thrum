package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/sync"
)

// InitOptions contains options for initializing a Thrum repository.
type InitOptions struct {
	RepoPath string
	Force    bool
}

// IsGitWorktree checks if repoPath is a git worktree (not the main working tree).
// Returns (isWorktree, mainRepoRoot, error).
func IsGitWorktree(repoPath string) (bool, string, error) {
	// Get the repo toplevel (current working tree root)
	topLevelCmd := exec.Command("git", "-C", repoPath, "rev-parse", "--show-toplevel")
	topLevelOut, err := topLevelCmd.Output()
	if err != nil {
		return false, "", fmt.Errorf("not a git repository")
	}
	topLevel := strings.TrimSpace(string(topLevelOut))

	// Get the common git dir (shared across all worktrees)
	commonDirCmd := exec.Command("git", "-C", repoPath, "rev-parse", "--git-common-dir")
	commonDirOut, err := commonDirCmd.Output()
	if err != nil {
		return false, "", nil //nolint:nilerr // can't determine, assume not a worktree
	}
	commonDir := strings.TrimSpace(string(commonDirOut))

	// Make commonDir absolute if relative
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(topLevel, commonDir)
	}
	commonDir = filepath.Clean(commonDir)

	// Get the git dir for this working tree
	gitDirCmd := exec.Command("git", "-C", repoPath, "rev-parse", "--git-dir")
	gitDirOut, err := gitDirCmd.Output()
	if err != nil {
		return false, "", nil //nolint:nilerr // can't determine, assume not a worktree
	}
	gitDir := strings.TrimSpace(string(gitDirOut))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(topLevel, gitDir)
	}
	gitDir = filepath.Clean(gitDir)

	// If git-dir != git-common-dir, this is a worktree
	if gitDir != commonDir {
		// Main repo root is the parent of the common git dir (e.g., /repo/.git -> /repo)
		mainRoot := filepath.Dir(commonDir)
		return true, mainRoot, nil
	}

	return false, "", nil
}

// Init initializes a Thrum repository.
func Init(opts InitOptions) error {
	thrumDir := filepath.Join(opts.RepoPath, ".thrum")
	varDir := filepath.Join(thrumDir, "var")

	// Check if already initialized
	if !opts.Force {
		if _, err := os.Stat(thrumDir); err == nil {
			return fmt.Errorf(".thrum/ already exists. Use --force to reinitialize")
		}
	}

	// Track whether .thrum/ existed before we started, so we can clean up on failure
	thrumDirExisted := true
	if _, err := os.Stat(thrumDir); os.IsNotExist(err) {
		thrumDirExisted = false
	}

	// Use a named return so the deferred cleanup can check for errors
	var retErr error
	defer func() {
		if retErr != nil && !thrumDirExisted {
			// Clean up worktree metadata first
			if syncDir, syncErr := paths.SyncWorktreePath(opts.RepoPath); syncErr == nil {
				rmCmd := exec.Command("git", "worktree", "remove", "--force", syncDir) //nolint:gosec // syncDir from internal paths
				rmCmd.Dir = opts.RepoPath
				_ = rmCmd.Run()
			}

			// Clean up orphan branch ref
			refCmd := exec.Command("git", "update-ref", "-d", "refs/heads/a-sync")
			refCmd.Dir = opts.RepoPath
			_ = refCmd.Run()

			// Remove the .thrum/ directory
			_ = os.RemoveAll(thrumDir)
		}
	}()

	// 1. Create .thrum/ directory
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		retErr = fmt.Errorf("failed to create .thrum/: %w", err)
		return retErr
	}

	// 2. Create .thrum/var/ directory
	if err := os.MkdirAll(varDir, 0750); err != nil {
		retErr = fmt.Errorf("failed to create .thrum/var/: %w", err)
		return retErr
	}

	// 2b. Create .thrum/identities/ directory
	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		retErr = fmt.Errorf("failed to create .thrum/identities/: %w", err)
		return retErr
	}

	// 3. Create .thrum/schema_version with "1"
	schemaVersionPath := filepath.Join(thrumDir, "schema_version")
	if err := os.WriteFile(schemaVersionPath, []byte("1\n"), 0600); err != nil {
		retErr = fmt.Errorf("failed to create schema_version: %w", err)
		return retErr
	}

	// 4. Add .thrum/ to .gitignore
	if err := updateGitignore(opts.RepoPath); err != nil {
		retErr = fmt.Errorf("failed to update .gitignore: %w", err)
		return retErr
	}

	// 5. Write default config.json (local-only by default — user must opt in to remote sync)
	configPath := filepath.Join(thrumDir, "config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		cfg := &config.ThrumConfig{
			Daemon: config.DaemonConfig{
				LocalOnly:    true,
				SyncInterval: config.DefaultSyncInterval,
				WSPort:       config.DefaultWSPort,
			},
		}
		if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
			retErr = fmt.Errorf("failed to write config.json: %w", err)
			return retErr
		}
	}

	// 6. Initialize a-sync branch
	if err := initASyncBranch(opts.RepoPath); err != nil {
		retErr = fmt.Errorf("failed to initialize a-sync branch: %w", err)
		return retErr
	}

	// Note: Daemon start will be implemented when daemon is ready (Epic 2)

	return nil
}

// updateGitignore adds Thrum-related entries to .gitignore.
func updateGitignore(repoPath string) error {
	gitignorePath := filepath.Join(repoPath, ".gitignore")

	// Entries to add
	entries := []string{
		"# Thrum data directory (all data lives on a-sync branch via worktree)",
		".thrum/",
		".thrum.*.json",
	}

	// Read existing .gitignore if it exists
	var existing []byte
	var err error
	if _, statErr := os.Stat(gitignorePath); statErr == nil {
		existing, err = os.ReadFile(gitignorePath) //nolint:gosec // G304 - path derived from repo root
		if err != nil {
			return err
		}
	}

	existingStr := string(existing)

	// Check if entries already exist (line-by-line to avoid substring false positives)
	needsUpdate := false
	existingLines := strings.Split(existingStr, "\n")
	for _, entry := range entries {
		// Skip comment line when checking
		if strings.HasPrefix(entry, "#") {
			continue
		}
		found := false
		for _, line := range existingLines {
			if strings.TrimSpace(line) == entry {
				found = true
				break
			}
		}
		if !found {
			needsUpdate = true
			break
		}
	}

	if !needsUpdate {
		return nil
	}

	// Append entries
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec // G304 - path derived from repo root
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// Add newline if file doesn't end with one
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	// Add a blank line before our section if file has content
	if len(existing) > 0 {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	// Write entries
	for _, entry := range entries {
		if _, err := f.WriteString(entry + "\n"); err != nil {
			return err
		}
	}

	return nil
}

// initASyncBranch creates the a-sync branch and worktree for message synchronization.
func initASyncBranch(repoPath string) error {
	bm := sync.NewBranchManager(repoPath, true)

	// Create orphan a-sync branch (safe plumbing — no working tree touch)
	if err := bm.CreateSyncBranch(); err != nil {
		return fmt.Errorf("create sync branch: %w", err)
	}

	// Create worktree at .git/thrum-sync/a-sync
	syncDir, err := paths.SyncWorktreePath(repoPath)
	if err != nil {
		return fmt.Errorf("resolve sync worktree path: %w", err)
	}
	if err := bm.CreateSyncWorktree(syncDir); err != nil {
		return fmt.Errorf("create sync worktree: %w", err)
	}

	// Create initial data files in the worktree
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); os.IsNotExist(err) {
		if err := os.WriteFile(eventsPath, []byte{}, 0600); err != nil {
			return fmt.Errorf("create events.jsonl: %w", err)
		}
	}

	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(messagesDir, 0750); err != nil {
		return fmt.Errorf("create messages dir: %w", err)
	}

	// Stage and commit initial files in the worktree
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git add in sync worktree: %w", err)
	}

	cmd = exec.Command("git",
		"-c", "user.name=Thrum", "-c", "user.email=thrum@local",
		"commit", "--no-verify", "-m", "Initialize Thrum sync data")
	cmd.Dir = syncDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.ToLower(string(output))
		// "nothing to commit" is acceptable (idempotent re-init)
		if !strings.Contains(outStr, "nothing to commit") &&
			!strings.Contains(outStr, "nothing added to commit") {
			return fmt.Errorf("git commit in sync worktree: %w\noutput: %s", err, strings.TrimSpace(string(output)))
		}
	}

	return nil
}
