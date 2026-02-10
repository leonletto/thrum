package paths

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// SyncBranchName is the branch name used for the sync worktree.
	// Duplicated from internal/sync to avoid circular imports.
	syncBranchName = "a-sync"

	// SyncWorktreeDir is the directory name inside .git/ for the sync worktree.
	syncWorktreeDir = "thrum-sync"
)

// FindThrumRoot walks up from startPath looking for a directory containing .thrum/.
// This mimics how git traverses parent directories to find .git/.
// Returns the directory containing .thrum/, or an error if none is found.
func FindThrumRoot(startPath string) (string, error) {
	absPath, err := filepath.Abs(startPath)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}

	dir := absPath
	for {
		thrumDir := filepath.Join(dir, ".thrum")
		info, err := os.Stat(thrumDir)
		if err == nil && info.IsDir() {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding .thrum/
			return "", fmt.Errorf("no .thrum/ directory found (searched from %s to /)", absPath)
		}
		dir = parent
	}
}

// ResolveThrumDir returns the effective .thrum/ directory for a repository.
//
// Resolution order:
// 1. Check for .thrum/redirect file in repoPath
// 2. If redirect exists: read it, validate target exists, return target path
// 3. If no redirect: return local .thrum/ path
//
// This enables feature worktrees to share a single daemon/sync worktree
// by pointing to the main worktree's .thrum/ directory.
func ResolveThrumDir(repoPath string) (string, error) {
	localThrumDir := filepath.Join(repoPath, ".thrum")
	redirectPath := filepath.Join(localThrumDir, "redirect")

	// Check for redirect file
	data, err := os.ReadFile(redirectPath) //nolint:gosec // G304 - path from .thrum/redirect, internal config
	if err != nil {
		if os.IsNotExist(err) {
			// No redirect — this is the main worktree, use local .thrum/
			return localThrumDir, nil
		}
		return "", fmt.Errorf("read redirect file: %w", err)
	}

	// Parse redirect target (trim whitespace/newlines)
	target := strings.TrimSpace(string(data))
	if target == "" {
		return "", fmt.Errorf("redirect file is empty: %s", redirectPath)
	}

	// Validate target is an absolute path
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("redirect target must be absolute path, got: %s", target)
	}

	// Validate target directory exists
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("redirect target does not exist: %s", target)
		}
		return "", fmt.Errorf("stat redirect target: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("redirect target is not a directory: %s", target)
	}

	// Guard against redirect chains (only single-hop supported)
	targetRedirect := filepath.Join(target, "redirect")
	if _, err := os.Stat(targetRedirect); err == nil {
		return "", fmt.Errorf("redirect chain detected: %s points to %s which also has a redirect file; only single-hop redirects are supported", redirectPath, target)
	}

	return target, nil
}

// SyncWorktreePath returns the absolute path to the sync worktree.
// Uses git-common-dir for bare repo and nested worktree support.
// For regular repos, returns: <git-common-dir>/thrum-sync/a-sync.
func SyncWorktreePath(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--git-common-dir")
	output, err := cmd.Output()
	if err != nil {
		// Fallback for non-git contexts
		return filepath.Join(repoPath, ".git", syncWorktreeDir, syncBranchName), nil //nolint:nilerr // intentional: fallback to default path for non-git contexts
	}
	gitCommonDir := strings.TrimSpace(string(output))
	if !filepath.IsAbs(gitCommonDir) {
		gitCommonDir = filepath.Join(repoPath, gitCommonDir)
	}
	return filepath.Join(gitCommonDir, syncWorktreeDir, syncBranchName), nil
}

// VarDir returns the path to the runtime directory.
// Contains messages.db (SQLite), thrum.sock, thrum.pid, ws.port, sync.lock.
func VarDir(thrumDir string) string {
	return filepath.Join(thrumDir, "var")
}

// IdentitiesDir returns the path to the identities directory.
// Each worktree has its own identities/ with per-agent JSON files.
// Note: This is relative to the LOCAL .thrum/ (not the redirect target),
// because identities are per-worktree.
func IdentitiesDir(repoPath string) string {
	return filepath.Join(repoPath, ".thrum", "identities")
}

// IsRedirected returns true if the given repo path uses a redirect file.
func IsRedirected(repoPath string) bool {
	redirectPath := filepath.Join(repoPath, ".thrum", "redirect")
	_, err := os.Stat(redirectPath)
	return err == nil
}
