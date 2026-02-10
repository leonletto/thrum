package sync

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// SyncBranchName is the name of the branch used for message synchronization.
	SyncBranchName = "a-sync"
)

// BranchManager manages the a-sync branch for message synchronization.
type BranchManager struct {
	repoPath  string
	localOnly bool // when true, skip remote operations (ls-remote, push -u)
}

// NewBranchManager creates a new BranchManager for the given repository path.
// When localOnly is true, EnsureSyncBranch skips remote operations.
func NewBranchManager(repoPath string, localOnly bool) *BranchManager {
	return &BranchManager{
		repoPath:  repoPath,
		localOnly: localOnly,
	}
}

// CreateSyncBranch creates the a-sync branch if it doesn't exist.
// The a-sync branch is always created as an orphan (no shared history with main).
func (b *BranchManager) CreateSyncBranch() error {
	// Check if we're in a git repository
	if err := b.checkGitRepo(); err != nil {
		return err
	}

	// Check if a-sync branch already exists
	if exists := b.branchExists(SyncBranchName); exists {
		// Branch already exists, nothing to do
		return nil
	}

	// Always create as orphan — a-sync should never share history with main
	return b.createOrphanBranch()
}

// EnsureSyncBranch ensures the branch exists locally and remotely.
// When localOnly is true, only the local branch is created; remote operations are skipped.
func (b *BranchManager) EnsureSyncBranch() error {
	// First, ensure the branch exists locally
	if err := b.CreateSyncBranch(); err != nil {
		return fmt.Errorf("creating sync branch: %w", err)
	}

	if b.localOnly {
		return nil
	}

	// Check if remote exists
	cmd := exec.Command("git", "remote")
	cmd.Dir = b.repoPath
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("checking for remotes: %w", err)
	}

	remotes := strings.TrimSpace(string(output))
	if remotes == "" {
		// No remote configured, local-only mode
		return nil
	}

	// Check if branch exists on remote (typically origin)
	remote := "origin"
	cmd = exec.Command("git", "ls-remote", "--heads", remote, SyncBranchName)
	cmd.Dir = b.repoPath
	output, err = cmd.Output()
	if err != nil {
		// ls-remote failed, might be no network access
		// Don't fail - allow local-only operation
		return nil //nolint:nilerr // intentionally ignore error for offline support
	}

	remoteExists := strings.TrimSpace(string(output)) != ""
	if !remoteExists {
		// Push branch to remote to establish tracking
		cmd = exec.Command("git", "push", "-u", remote, SyncBranchName)
		cmd.Dir = b.repoPath
		if err := cmd.Run(); err != nil {
			// Push failed, but don't fail the operation
			// User might be offline or remote might not accept pushes yet
			return nil //nolint:nilerr // intentionally ignore error for offline support
		}
	}

	return nil
}

// GetSyncBranchRef returns the current ref of a-sync.
func (b *BranchManager) GetSyncBranchRef() (string, error) {
	cmd := exec.Command("git", "rev-parse", SyncBranchName)
	cmd.Dir = b.repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get %s ref: %w", SyncBranchName, err)
	}

	return strings.TrimSpace(string(output)), nil
}

// checkGitRepo verifies that the path is a git repository.
func (b *BranchManager) checkGitRepo() error {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = b.repoPath
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("not a git repository")
	}
	return nil
}

// branchExists checks if a git branch exists.
func (b *BranchManager) branchExists(branchName string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", branchName)
	cmd.Dir = b.repoPath
	err := cmd.Run()
	return err == nil
}

// createOrphanBranch creates an orphan a-sync branch using git plumbing commands.
// SAFETY: These commands never touch the working tree or index.
// Fully idempotent — safe to call multiple times.
func (b *BranchManager) createOrphanBranch() error {
	// The empty tree SHA is a well-known git constant (same across all repos)
	const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

	// Step 1: Create a commit object pointing to the empty tree.
	// git commit-tree writes to the object database only — no working tree or index changes.
	cmd := exec.Command("git", "commit-tree", emptyTreeSHA, "-m", "Initialize Thrum sync")
	cmd.Dir = b.repoPath
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git commit-tree failed: %w", err)
	}
	commitSHA := strings.TrimSpace(string(output))

	// Step 2: Create the branch ref atomically.
	// git update-ref is atomic — either it succeeds or the ref is unchanged.
	cmd = exec.Command("git", "update-ref", "refs/heads/"+SyncBranchName, commitSHA) //nolint:gosec // commitSHA from git commit-tree output
	cmd.Dir = b.repoPath
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git update-ref failed: %w", err)
	}

	return nil
}

// CreateSyncWorktree creates a git worktree checked out on the a-sync branch.
// SyncDir should be the path returned by paths.SyncWorktreePath() (e.g. .git/thrum-sync/a-sync).
// The worktree uses sparse checkout to only include events.jsonl and messages/.
// If the worktree already exists and is healthy, this is a no-op.
// If it exists but is broken, it is removed and recreated.
func (b *BranchManager) CreateSyncWorktree(syncDir string) error {
	// Prune stale worktree entries first
	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = b.repoPath
	_ = pruneCmd.Run() // Best effort

	// Health check: if worktree already exists and is valid, return early
	if b.isHealthyWorktree(syncDir) {
		return nil
	}

	// Unhealthy or missing — remove any remnants and recreate
	b.removeSyncWorktree(syncDir)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(syncDir), 0750); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}

	// Create worktree with --no-checkout so sparse checkout can be configured first
	cmd := exec.Command("git", "worktree", "add", "-f", "--no-checkout", syncDir, SyncBranchName)
	cmd.Dir = b.repoPath
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git worktree add failed: %w", err)
	}

	// Configure sparse checkout in the worktree
	if err := b.configureSparseCheckout(syncDir); err != nil {
		// Clean up on failure
		b.removeSyncWorktree(syncDir)
		return fmt.Errorf("configure sparse checkout: %w", err)
	}

	// Checkout with sparse active
	cmd = exec.Command("git", "checkout", SyncBranchName)
	cmd.Dir = syncDir
	if err := cmd.Run(); err != nil {
		b.removeSyncWorktree(syncDir)
		return fmt.Errorf("git checkout in worktree: %w", err)
	}

	// Prevent sparse checkout leak to the main repo.
	// Known git 2.38+ bug: running sparse-checkout in a worktree can enable
	// core.sparseCheckout on the main repo as a side effect.
	cmd = exec.Command("git", "config", "core.sparseCheckout", "false")
	cmd.Dir = b.repoPath
	_ = cmd.Run() // Best effort — not fatal if this fails

	return nil
}

// configureSparseCheckout sets up sparse checkout in the worktree so only
// events.jsonl and messages/ are checked out.
func (b *BranchManager) configureSparseCheckout(syncDir string) error {
	// Initialize sparse checkout (non-cone mode for pattern matching)
	cmd := exec.Command("git", "sparse-checkout", "init", "--no-cone")
	cmd.Dir = syncDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sparse-checkout init: %w", err)
	}

	// Set patterns (messages.jsonl is the old monolithic format, kept for migration support)
	cmd = exec.Command("git", "sparse-checkout", "set", "/events.jsonl", "/messages/", "/messages.jsonl")
	cmd.Dir = syncDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sparse-checkout set: %w", err)
	}

	return nil
}

// isHealthyWorktree checks if syncDir is a valid, healthy worktree.
// Returns true only if all health checks pass.
func (b *BranchManager) isHealthyWorktree(syncDir string) bool {
	// Check 1: .git file exists (worktrees have a .git file, not directory)
	gitFilePath := filepath.Join(syncDir, ".git")
	info, err := os.Stat(gitFilePath)
	if err != nil || info.IsDir() {
		return false
	}

	// Check 2: Listed in git worktree list
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = b.repoPath
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	if !b.isWorktreeRegistered(string(output), syncDir) {
		return false
	}

	// Check 3: HEAD points to a-sync
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = syncDir
	output, err = cmd.Output()
	if err != nil || strings.TrimSpace(string(output)) != SyncBranchName {
		return false
	}

	// Check 4: Sparse checkout includes expected patterns.
	// The .git file in worktrees is a pointer ("gitdir: <path>"), so we resolve
	// the actual git dir to find the sparse-checkout file.
	gitDir := b.resolveWorktreeGitDir(syncDir)
	if gitDir == "" {
		return false
	}
	sparseFile := filepath.Join(gitDir, "info", "sparse-checkout")
	sparseContent, err := os.ReadFile(sparseFile) //nolint:gosec // G304 - path from git worktree metadata
	if err != nil {
		return false
	}
	sparse := string(sparseContent)
	if !strings.Contains(sparse, "events.jsonl") || !strings.Contains(sparse, "messages") {
		return false
	}

	return true
}

// isWorktreeRegistered checks if syncDir appears in git worktree list --porcelain output.
// Uses filepath.EvalSymlinks to handle symlinked paths (e.g., macOS /var -> /private/var).
func (b *BranchManager) isWorktreeRegistered(porcelainOutput, syncDir string) bool {
	// Resolve syncDir symlinks for comparison
	resolvedSyncDir, err := filepath.EvalSymlinks(syncDir)
	if err != nil {
		resolvedSyncDir = syncDir // Fall back to unresolved
	}

	for _, line := range strings.Split(porcelainOutput, "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		wtPath := strings.TrimPrefix(line, "worktree ")
		resolvedWT, err := filepath.EvalSymlinks(wtPath)
		if err != nil {
			resolvedWT = wtPath
		}
		if resolvedWT == resolvedSyncDir {
			return true
		}
	}
	return false
}

// resolveWorktreeGitDir reads the .git file in a worktree and returns the
// actual git directory path (e.g. .git/worktrees/<name>).
func (b *BranchManager) resolveWorktreeGitDir(syncDir string) string {
	gitFilePath := filepath.Join(syncDir, ".git")
	data, err := os.ReadFile(gitFilePath) //nolint:gosec // G304 - path from git worktree metadata
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if !strings.HasPrefix(content, "gitdir: ") {
		return ""
	}
	gitDir := strings.TrimPrefix(content, "gitdir: ")
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(syncDir, gitDir)
	}
	return gitDir
}

// removeSyncWorktree removes a sync worktree, cleaning up git metadata.
// Accepts paths containing .git/ (new location) or .thrum/ (old location, for migration).
func (b *BranchManager) removeSyncWorktree(syncDir string) {
	// Safety: never RemoveAll an empty or suspiciously short path
	if syncDir == "" || len(syncDir) < 5 {
		return
	}
	if !strings.Contains(syncDir, ".git") && !strings.Contains(syncDir, ".thrum") {
		return
	}

	// Try git worktree remove first
	cmd := exec.Command("git", "worktree", "remove", "--force", syncDir)
	cmd.Dir = b.repoPath
	_ = cmd.Run() // Best effort — may fail if not a valid worktree

	// Prune stale worktree references
	cmd = exec.Command("git", "worktree", "prune")
	cmd.Dir = b.repoPath
	_ = cmd.Run()

	// If directory still exists (e.g., broken/corrupted worktree), remove it manually
	_ = os.RemoveAll(syncDir)
}
