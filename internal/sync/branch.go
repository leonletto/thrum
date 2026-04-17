package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
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
func (b *BranchManager) CreateSyncBranch(ctx context.Context) error {
	// Check if we're in a git repository
	if err := b.checkGitRepo(ctx); err != nil {
		return err
	}

	// Check if a-sync branch already exists
	if exists := b.branchExists(ctx, SyncBranchName); exists {
		// Branch already exists, nothing to do
		return nil
	}

	// Always create as orphan — a-sync should never share history with main
	return b.createOrphanBranch(ctx)
}

// EnsureSyncBranch ensures the branch exists locally and remotely.
// When localOnly is true, only the local branch is created; remote operations are skipped.
func (b *BranchManager) EnsureSyncBranch(ctx context.Context) error {
	// First, ensure the branch exists locally
	if err := b.CreateSyncBranch(ctx); err != nil {
		return fmt.Errorf("creating sync branch: %w", err)
	}

	if b.localOnly {
		return nil
	}

	// Check if remote exists
	output, err := safecmd.Git(ctx, b.repoPath, "remote")
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
	output, err = safecmd.GitLong(ctx, b.repoPath, "ls-remote", "--heads", remote, SyncBranchName)
	if err != nil {
		// ls-remote failed, might be no network access
		// Don't fail - allow local-only operation
		return nil //nolint:nilerr // intentionally ignore error for offline support
	}

	remoteExists := strings.TrimSpace(string(output)) != ""
	if !remoteExists {
		// Push branch to remote to establish tracking
		if _, err := safecmd.GitLong(ctx, b.repoPath, "push", "-u", remote, SyncBranchName); err != nil {
			// Push failed, but don't fail the operation
			// User might be offline or remote might not accept pushes yet
			return nil //nolint:nilerr // intentionally ignore error for offline support
		}
	}

	return nil
}

// GetSyncBranchRef returns the current ref of a-sync.
func (b *BranchManager) GetSyncBranchRef(ctx context.Context) (string, error) {
	output, err := safecmd.Git(ctx, b.repoPath, "rev-parse", SyncBranchName)
	if err != nil {
		return "", fmt.Errorf("failed to get %s ref: %w", SyncBranchName, err)
	}

	return strings.TrimSpace(string(output)), nil
}

// RemoteTrackingSyncSHA returns the SHA of refs/remotes/origin/a-sync if it
// exists locally (populated by git clone or git fetch). Returns ("", false)
// if the ref is not present. Never performs a network call — this reads the
// already-fetched remote-tracking ref.
func (b *BranchManager) RemoteTrackingSyncSHA(ctx context.Context) (string, bool) {
	output, err := safecmd.Git(ctx, b.repoPath, "rev-parse", "--verify",
		"refs/remotes/origin/"+SyncBranchName)
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(string(output))
	if sha == "" {
		return "", false
	}
	return sha, true
}

// checkGitRepo verifies that the path is a git repository.
func (b *BranchManager) checkGitRepo(ctx context.Context) error {
	if _, err := safecmd.Git(ctx, b.repoPath, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("not a git repository")
	}
	return nil
}

// branchExists checks if a git branch exists.
func (b *BranchManager) branchExists(ctx context.Context, branchName string) bool {
	_, err := safecmd.Git(ctx, b.repoPath, "rev-parse", "--verify", branchName)
	return err == nil
}

// createOrphanBranch creates an orphan a-sync branch using git plumbing commands.
// SAFETY: These commands never touch the working tree or index.
// Fully idempotent — safe to call multiple times.
func (b *BranchManager) createOrphanBranch(ctx context.Context) error {
	// The empty tree SHA is a well-known git constant (same across all repos)
	const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

	// Step 1: Create a commit object pointing to the empty tree.
	// git commit-tree writes to the object database only — no working tree or index changes.
	output, err := safecmd.Git(ctx, b.repoPath, "commit-tree", emptyTreeSHA, "-m", "Initialize Thrum sync")
	if err != nil {
		return fmt.Errorf("git commit-tree failed: %w", err)
	}
	commitSHA := strings.TrimSpace(string(output))

	// Step 2: Create the branch ref atomically.
	// git update-ref is atomic — either it succeeds or the ref is unchanged.
	if _, err := safecmd.Git(ctx, b.repoPath, "update-ref", "refs/heads/"+SyncBranchName, commitSHA); err != nil {
		return fmt.Errorf("git update-ref failed: %w", err)
	}

	return nil
}

// CreateSyncWorktree creates a git worktree checked out on the a-sync branch.
// SyncDir should be the path returned by paths.SyncWorktreePath() (e.g. .git/thrum-sync/a-sync).
// The worktree uses sparse checkout to only include events.jsonl and messages/.
// If the worktree already exists and is healthy, this is a no-op.
// If it exists but is broken, it is removed and recreated.
func (b *BranchManager) CreateSyncWorktree(ctx context.Context, syncDir string) error {
	// Prune stale worktree entries first (best effort)
	_, _ = safecmd.Git(ctx, b.repoPath, "worktree", "prune")

	// Health check: if worktree already exists and is valid, return early
	if b.isHealthyWorktree(ctx, syncDir) {
		return nil
	}

	// Unhealthy or missing — remove any remnants and recreate
	b.removeSyncWorktree(ctx, syncDir)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(syncDir), 0750); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}

	// Create worktree with --no-checkout so sparse checkout can be configured first
	if _, err := safecmd.Git(ctx, b.repoPath, "worktree", "add", "-f", "--no-checkout", syncDir, SyncBranchName); err != nil {
		return fmt.Errorf("git worktree add failed: %w", err)
	}

	// Configure sparse checkout in the worktree
	if err := b.configureSparseCheckout(ctx, syncDir); err != nil {
		// Clean up on failure
		b.removeSyncWorktree(ctx, syncDir)
		return fmt.Errorf("configure sparse checkout: %w", err)
	}

	// Checkout with sparse active
	if _, err := safecmd.Git(ctx, syncDir, "checkout", SyncBranchName); err != nil {
		b.removeSyncWorktree(ctx, syncDir)
		return fmt.Errorf("git checkout in worktree: %w", err)
	}

	// Prevent sparse checkout leak to the main repo.
	// Known git 2.38+ bug: running sparse-checkout in a worktree can enable
	// core.sparseCheckout on the main repo as a side effect.
	// Best effort — not fatal if this fails.
	_, _ = safecmd.Git(ctx, b.repoPath, "config", "core.sparseCheckout", "false")

	return nil
}

// configureSparseCheckout sets up sparse checkout in the worktree so only
// events.jsonl and messages/ are checked out.
func (b *BranchManager) configureSparseCheckout(ctx context.Context, syncDir string) error {
	// Initialize sparse checkout (non-cone mode for pattern matching)
	if _, err := safecmd.Git(ctx, syncDir, "sparse-checkout", "init", "--no-cone"); err != nil {
		return fmt.Errorf("sparse-checkout init: %w", err)
	}

	// Set patterns (messages.jsonl is the old monolithic format, kept for migration support)
	if _, err := safecmd.Git(ctx, syncDir, "sparse-checkout", "set", "/events.jsonl", "/messages/", "/messages.jsonl"); err != nil {
		return fmt.Errorf("sparse-checkout set: %w", err)
	}

	return nil
}

// isHealthyWorktree checks if syncDir is a valid, healthy worktree.
// Returns true only if all health checks pass.
func (b *BranchManager) isHealthyWorktree(ctx context.Context, syncDir string) bool {
	// Check 1: .git file exists (worktrees have a .git file, not directory)
	gitFilePath := filepath.Join(syncDir, ".git")
	info, err := os.Stat(gitFilePath)
	if err != nil || info.IsDir() {
		return false
	}

	// Check 2: Listed in git worktree list
	output, err := safecmd.Git(ctx, b.repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return false
	}
	if !b.isWorktreeRegistered(string(output), syncDir) {
		return false
	}

	// Check 3: HEAD points to a-sync
	output, err = safecmd.Git(ctx, syncDir, "rev-parse", "--abbrev-ref", "HEAD")
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
	sparseContent, err := os.ReadFile(sparseFile) // #nosec G304 -- path is constructed from git worktree's own .git directory metadata
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
		wtPath, ok := strings.CutPrefix(line, "worktree ")
		if !ok {
			continue
		}
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
	data, err := os.ReadFile(gitFilePath) // #nosec G304 -- gitFilePath is syncDir/.git, a known internal git metadata path
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	gitDir, ok := strings.CutPrefix(content, "gitdir: ")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(syncDir, gitDir)
	}
	return gitDir
}

// removeSyncWorktree removes a sync worktree, cleaning up git metadata.
// Accepts paths containing .git/ (new location) or .thrum/ (old location, for migration).
func (b *BranchManager) removeSyncWorktree(ctx context.Context, syncDir string) {
	// Safety: never RemoveAll an empty or suspiciously short path
	if syncDir == "" || len(syncDir) < 5 {
		return
	}
	if !strings.Contains(syncDir, ".git") && !strings.Contains(syncDir, ".thrum") {
		return
	}

	// Try git worktree remove first (best effort — may fail if not a valid worktree)
	_, _ = safecmd.Git(ctx, b.repoPath, "worktree", "remove", "--force", syncDir)

	// Prune stale worktree references (best effort)
	_, _ = safecmd.Git(ctx, b.repoPath, "worktree", "prune")

	// If directory still exists (e.g., broken/corrupted worktree), remove it manually
	_ = os.RemoveAll(syncDir)
}
