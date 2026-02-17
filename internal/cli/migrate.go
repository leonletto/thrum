package cli

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/sync"
)

// Migrate migrates a repository from the old layout (JSONL tracked on main)
// to the worktree architecture (sync worktree on orphan a-sync branch).
// It is idempotent — running twice is safe.
func Migrate(repoPath string) error {
	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("resolving repo path: %w", err)
	}

	thrumDir := filepath.Join(repoPath, ".thrum")

	// Migrate sync worktree location (old .thrum/sync/ → .git/thrum-sync/a-sync)
	if err := MigrateSyncWorktreeLocation(repoPath, thrumDir); err != nil {
		return fmt.Errorf("migrating sync worktree location: %w", err)
	}

	syncDir, err := paths.SyncWorktreePath(repoPath)
	if err != nil {
		return fmt.Errorf("resolving sync worktree path: %w", err)
	}

	// Migration steps 1-7 below.
	// Note: partial failures are recoverable by re-running migrate (idempotent design).

	// Step 1: Check if migration is needed
	needed, sources := migrationNeeded(repoPath, thrumDir)
	if !needed {
		fmt.Println("No migration needed.")
		return nil
	}

	fmt.Println("Migration needed. Found old-layout data:")
	for _, s := range sources {
		fmt.Printf("  - %s\n", s)
	}

	// Step 2: Ensure sync worktree exists
	if err := ensureSyncWorktree(repoPath, syncDir); err != nil {
		return fmt.Errorf("creating sync worktree: %w", err)
	}

	// Step 3: Copy JSONL data to sync worktree
	copied, err := copyDataToSync(thrumDir, syncDir)
	if err != nil {
		return fmt.Errorf("copying data to sync worktree: %w", err)
	}

	// Step 4: Commit data in sync worktree
	if err := commitSyncData(syncDir); err != nil {
		return fmt.Errorf("committing sync data: %w", err)
	}

	// Step 5: Remove JSONL files from main branch tracking
	removedFromGit := removeFromMainTracking(repoPath)

	// Step 6: Update .gitignore
	gitignoreUpdated, err := migrateGitignore(repoPath)
	if err != nil {
		return fmt.Errorf("updating .gitignore: %w", err)
	}

	// Step 7: Clean up .gitattributes
	gitattrsUpdated, err := cleanGitattributes(repoPath)
	if err != nil {
		return fmt.Errorf("cleaning .gitattributes: %w", err)
	}

	// Step 8: Print summary
	fmt.Println("\nMigration complete:")
	for _, f := range copied {
		fmt.Printf("  Copied: %s\n", f)
	}
	for _, f := range removedFromGit {
		fmt.Printf("  Untracked from main: %s\n", f)
	}
	if gitignoreUpdated {
		fmt.Println("  Updated .gitignore: .thrum/var/ -> .thrum/")
	}
	if gitattrsUpdated {
		fmt.Println("  Cleaned .gitattributes: removed stale .thrum/ merge rules")
	}
	fmt.Println("\nData has been migrated to the sync worktree (a-sync branch).")
	fmt.Println("Original files remain on disk as backup. Commit .gitignore and .gitattributes changes when ready.")

	return nil
}

// migrationNeeded checks whether the repo has old-layout JSONL files.
// Returns true if migration is needed, along with a list of found sources.
func migrationNeeded(repoPath, thrumDir string) (bool, []string) {
	var sources []string

	// Check for files tracked by git
	trackedFiles := []string{".thrum/events.jsonl", ".thrum/messages.jsonl", ".thrum/messages/"}
	for _, f := range trackedFiles {
		cmd := exec.Command("git", "ls-files", f) //nolint:gosec // f from hardcoded trackedFiles list
		cmd.Dir = repoPath
		output, err := cmd.Output()
		if err == nil && strings.TrimSpace(string(output)) != "" {
			sources = append(sources, f+" (tracked by git)")
		}
	}

	// Check for files existing locally (even if untracked)
	localFiles := []string{
		filepath.Join(thrumDir, "events.jsonl"),
		filepath.Join(thrumDir, "messages.jsonl"),
	}
	for _, f := range localFiles {
		if _, err := os.Stat(f); err == nil {
			rel, _ := filepath.Rel(repoPath, f)
			// Only add if not already found via git tracking
			alreadyListed := false
			for _, s := range sources {
				if strings.HasPrefix(s, rel) {
					alreadyListed = true
					break
				}
			}
			if !alreadyListed {
				sources = append(sources, rel+" (local)")
			}
		}
	}

	// Check for messages/ directory with .jsonl files
	messagesDir := filepath.Join(thrumDir, "messages")
	if info, err := os.Stat(messagesDir); err == nil && info.IsDir() {
		entries, _ := os.ReadDir(messagesDir)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
				sources = append(sources, filepath.Join(".thrum/messages", e.Name())+" (local)")
				break // one is enough to confirm
			}
		}
	}

	return len(sources) > 0, sources
}

// ensureSyncWorktree creates the sync worktree if it doesn't exist.
// Delegates entirely to BranchManager which handles health checks, idempotency,
// and recreation of broken worktrees.
func ensureSyncWorktree(repoPath, syncDir string) error {
	bm := sync.NewBranchManager(repoPath, false)

	if err := bm.CreateSyncBranch(); err != nil {
		return fmt.Errorf("create sync branch: %w", err)
	}

	if err := bm.CreateSyncWorktree(syncDir); err != nil {
		return fmt.Errorf("create sync worktree: %w", err)
	}

	return nil
}

// copyDataToSync copies old-layout JSONL files into the sync worktree.
// Returns a list of files that were copied.
func copyDataToSync(thrumDir, syncDir string) ([]string, error) {
	var copied []string

	// Ensure messages/ directory exists in sync worktree
	syncMessagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(syncMessagesDir, 0750); err != nil {
		return nil, fmt.Errorf("create messages dir in sync: %w", err)
	}

	// Copy events.jsonl
	eventsPath := filepath.Join(thrumDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); err == nil {
		data, err := os.ReadFile(eventsPath) //nolint:gosec // G304 - path from internal .thrum directory
		if err != nil {
			return nil, fmt.Errorf("reading events.jsonl: %w", err)
		}
		dst := filepath.Join(syncDir, "events.jsonl")
		if err := os.WriteFile(dst, data, 0600); err != nil {
			return nil, fmt.Errorf("writing events.jsonl to sync: %w", err)
		}
		copied = append(copied, ".thrum/events.jsonl -> sync worktree/events.jsonl")
	}

	// Copy messages.jsonl (monolithic file)
	messagesPath := filepath.Join(thrumDir, "messages.jsonl")
	if _, err := os.Stat(messagesPath); err == nil {
		data, err := os.ReadFile(messagesPath) //nolint:gosec // G304 - path from internal .thrum directory
		if err != nil {
			return nil, fmt.Errorf("reading messages.jsonl: %w", err)
		}
		dst := filepath.Join(syncDir, "messages.jsonl")
		if err := os.WriteFile(dst, data, 0600); err != nil {
			return nil, fmt.Errorf("writing messages.jsonl to sync: %w", err)
		}
		copied = append(copied, ".thrum/messages.jsonl -> sync worktree/messages.jsonl")
	}

	// Copy messages/*.jsonl (sharded files)
	messagesDir := filepath.Join(thrumDir, "messages")
	if info, err := os.Stat(messagesDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(messagesDir)
		if err != nil {
			return nil, fmt.Errorf("reading messages dir: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			srcPath := filepath.Join(messagesDir, e.Name())
			data, err := os.ReadFile(srcPath) //nolint:gosec // G304 - path from internal .thrum/messages directory
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
			}
			dstPath := filepath.Join(syncMessagesDir, e.Name())
			if err := os.WriteFile(dstPath, data, 0600); err != nil {
				return nil, fmt.Errorf("writing %s to sync: %w", e.Name(), err)
			}
			copied = append(copied, fmt.Sprintf(".thrum/messages/%s -> sync worktree/messages/%s", e.Name(), e.Name()))
		}
	}

	return copied, nil
}

// commitSyncData stages and commits all data in the sync worktree.
func commitSyncData(syncDir string) error {
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git add in sync worktree: %w", err)
	}

	cmd = exec.Command("git",
		"-c", "user.name=Thrum", "-c", "user.email=thrum@local",
		"commit", "--no-verify", "-m", "migrate: import data from main branch")
	cmd.Dir = syncDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.ToLower(string(output))
		// "nothing to commit" is acceptable (idempotent)
		if !strings.Contains(outStr, "nothing to commit") &&
			!strings.Contains(outStr, "nothing added to commit") {
			return fmt.Errorf("git commit in sync worktree: %w\noutput: %s", err, string(output))
		}
	}

	return nil
}

// removeFromMainTracking removes old JSONL files from git tracking on main.
// Files remain on disk. Returns a list of files that were untracked.
func removeFromMainTracking(repoPath string) []string {
	var removed []string

	targets := []struct {
		path      string
		recursive bool
	}{
		{".thrum/events.jsonl", false},
		{".thrum/messages.jsonl", false},
		{".thrum/schema_version", false},
		{".thrum/messages/", true},
	}

	for _, t := range targets {
		args := []string{"rm", "--cached"}
		if t.recursive {
			args = append(args, "-r")
		}
		args = append(args, t.path)

		cmd := exec.Command("git", args...)
		cmd.Dir = repoPath
		if err := cmd.Run(); err == nil {
			removed = append(removed, t.path)
		}
		// Errors are expected (file not tracked) — ignore them
	}

	return removed
}

// migrateGitignore updates .gitignore for the new architecture.
// Replaces old partial entries like .thrum/var/ with the all-encompassing .thrum/.
// Returns true if .gitignore was modified.
func migrateGitignore(repoPath string) (bool, error) {
	gitignorePath := filepath.Join(repoPath, ".gitignore")

	data, err := os.ReadFile(gitignorePath) //nolint:gosec // G304 - path derived from repo root
	if err != nil {
		if os.IsNotExist(err) {
			// No .gitignore — use the standard updateGitignore from init
			return true, updateGitignore(repoPath)
		}
		return false, err
	}

	lines := strings.Split(string(data), "\n")
	modified := false

	// Old entries to remove (they'll be superseded by .thrum/)
	oldEntries := map[string]bool{
		".thrum/var/":         true,
		".thrum/events.jsonl": true,
		".thrum/messages/":    true,
	}

	hasThrumDir := false
	var newLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if oldEntries[trimmed] {
			// Replace .thrum/var/ with .thrum/, skip others
			if trimmed == ".thrum/var/" {
				newLines = append(newLines, ".thrum/")
				hasThrumDir = true
			}
			// Other old entries are just removed (covered by .thrum/)
			modified = true
			continue
		}

		if trimmed == ".thrum/" {
			hasThrumDir = true
		}

		newLines = append(newLines, line)
	}

	// If .thrum/ wasn't present and we didn't just add it, add it
	if !hasThrumDir {
		newLines = append(newLines, ".thrum/")
		modified = true
	}

	if !modified {
		return false, nil
	}

	result := strings.Join(newLines, "\n")
	if err := os.WriteFile(gitignorePath, []byte(result), 0600); err != nil {
		return false, err
	}

	return true, nil
}

// cleanGitattributes removes stale merge=union entries for .thrum/ files.
// Returns true if .gitattributes was modified.
func cleanGitattributes(repoPath string) (bool, error) {
	gitattrsPath := filepath.Join(repoPath, ".gitattributes")

	data, err := os.ReadFile(gitattrsPath) //nolint:gosec // G304 - path derived from repo root
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // No .gitattributes, nothing to clean
		}
		return false, err
	}

	lines := strings.Split(string(data), "\n")
	modified := false

	// Lines to remove
	stalePatterns := []string{
		".thrum/events.jsonl merge=union",
		".thrum/messages/*.jsonl merge=union",
		"# Use union merge for thrum JSONL files (dedup by event_id)",
	}

	var newLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isStale := false
		for _, pattern := range stalePatterns {
			if trimmed == pattern {
				isStale = true
				modified = true
				break
			}
		}
		if !isStale {
			newLines = append(newLines, line)
		}
	}

	if !modified {
		return false, nil
	}

	// Clean up consecutive blank lines that may result from removal
	result := cleanConsecutiveBlanks(strings.Join(newLines, "\n"))

	if err := os.WriteFile(gitattrsPath, []byte(result), fs.FileMode(0600)); err != nil {
		return false, err
	}

	return true, nil
}

// cleanConsecutiveBlanks reduces multiple consecutive blank lines to at most one.
func cleanConsecutiveBlanks(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	prevBlank := false

	for _, line := range lines {
		blank := strings.TrimSpace(line) == ""
		if blank && prevBlank {
			continue
		}
		result = append(result, line)
		prevBlank = blank
	}

	return strings.Join(result, "\n")
}

// MigrateSyncWorktreeLocation migrates the sync worktree from the old location
// (.thrum/sync/) to the new location (.git/thrum-sync/a-sync).
// Idempotent — safe to call multiple times. No-op if old location doesn't exist.
func MigrateSyncWorktreeLocation(repoPath, thrumDir string) error {
	oldSyncDir := filepath.Join(thrumDir, "sync")
	oldGitFile := filepath.Join(oldSyncDir, ".git")

	// Check if old worktree exists
	if _, err := os.Stat(oldGitFile); os.IsNotExist(err) {
		return nil // Nothing to migrate
	}

	fmt.Fprintf(os.Stderr, "Migrating sync worktree from %s to .git/thrum-sync/...\n", oldSyncDir)

	// Remove old worktree via git
	cmd := exec.Command("git", "worktree", "remove", "--force", oldSyncDir) //nolint:gosec // oldSyncDir from internal path construction
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		// Fallback: manual removal + prune
		_ = os.RemoveAll(oldSyncDir)
		pruneCmd := exec.Command("git", "worktree", "prune")
		pruneCmd.Dir = repoPath
		_ = pruneCmd.Run()
	}

	// New worktree is created by the normal init path (CreateSyncWorktree)
	// — no need to create it here
	return nil
}
