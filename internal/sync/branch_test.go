package sync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestRepo creates a temporary git repository for testing.
func setupTestRepo(t *testing.T) string {
	t.Helper()

	// Create temp directory
	tmpDir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	// Configure git user (required for commits)
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to config git user.name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to config git user.email: %v", err)
	}

	return tmpDir
}

// setupTestRepoWithCommit creates a test repo with an initial commit.
func setupTestRepoWithCommit(t *testing.T) string {
	t.Helper()

	tmpDir := setupTestRepo(t)

	// Create a file and commit it
	testFile := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(testFile, []byte("# Test\n"), 0600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cmd := exec.Command("git", "add", "README.md")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git add: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git commit: %v", err)
	}

	return tmpDir
}

// setupThrumFiles creates .thrum directory and required files.
// NOTE: With worktree architecture, JSONL files live in .git/thrum-sync/a-sync/ (not .thrum/).
// This helper only creates the minimal structure needed for branch tests.
func setupThrumFiles(t *testing.T, repoPath string) {
	t.Helper()

	thrumDir := filepath.Join(repoPath, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	// Create var/ and identities/ directories
	if err := os.MkdirAll(filepath.Join(thrumDir, "var"), 0750); err != nil {
		t.Fatalf("failed to create .thrum/var: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750); err != nil {
		t.Fatalf("failed to create .thrum/identities: %v", err)
	}

	schemaPath := filepath.Join(thrumDir, "schema_version")
	if err := os.WriteFile(schemaPath, []byte("1\n"), 0600); err != nil {
		t.Fatalf("failed to write schema_version: %v", err)
	}
}

// branchExists checks if a branch exists in the repo.
//
//nolint:unparam // branchName varies in actual usage, fixed in tests
func branchExists(t *testing.T, repoPath string, branchName string) bool {
	t.Helper()

	cmd := exec.Command("git", "rev-parse", "--verify", branchName)
	cmd.Dir = repoPath
	err := cmd.Run()
	return err == nil
}

// getCurrentBranch returns the current branch name.
func getCurrentBranch(t *testing.T, repoPath string) string {
	t.Helper()

	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func TestNewBranchManager(t *testing.T) {
	repoPath := "/test/repo"
	bm := NewBranchManager(repoPath)

	if bm == nil {
		t.Fatal("NewBranchManager returned nil")
	}

	if bm.repoPath != repoPath {
		t.Errorf("repoPath = %q, want %q", bm.repoPath, repoPath)
	}
}

func TestBranchManager_CreateSyncBranch_WithExistingCommits(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	bm := NewBranchManager(repoPath)

	// Save the original branch name
	originalBranch := getCurrentBranch(t, repoPath)

	// Create the sync branch
	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("CreateSyncBranch failed: %v", err)
	}

	// Verify branch exists
	if !branchExists(t, repoPath, SyncBranchName) {
		t.Errorf("branch %s does not exist", SyncBranchName)
	}

	// Verify we're still on the original branch (not switched to a-sync)
	currentBranch := getCurrentBranch(t, repoPath)
	if currentBranch == SyncBranchName {
		t.Errorf("current branch is %s, should not have switched to it", SyncBranchName)
	}
	if currentBranch != originalBranch {
		t.Errorf("current branch changed from %s to %s", originalBranch, currentBranch)
	}
}

func TestBranchManager_CreateSyncBranch_AlreadyExists(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	bm := NewBranchManager(repoPath)

	// Create the sync branch first time
	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("CreateSyncBranch failed: %v", err)
	}

	// Create again - should not error
	if err := bm.CreateSyncBranch(); err != nil {
		t.Errorf("CreateSyncBranch failed on second call: %v", err)
	}

	// Verify branch still exists
	if !branchExists(t, repoPath, SyncBranchName) {
		t.Errorf("branch %s does not exist", SyncBranchName)
	}
}

func TestBranchManager_CreateSyncBranch_NoCommits(t *testing.T) {
	repoPath := setupTestRepo(t)
	setupThrumFiles(t, repoPath)

	bm := NewBranchManager(repoPath)

	// Get initial git status to verify working tree is not modified
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	beforeStatus, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get git status: %v", err)
	}

	// Create the sync branch in empty repo
	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("CreateSyncBranch failed: %v", err)
	}

	// Verify branch exists
	if !branchExists(t, repoPath, SyncBranchName) {
		t.Errorf("branch %s does not exist", SyncBranchName)
	}

	// Verify the orphan branch has a commit
	cmd = exec.Command("git", "rev-list", "--count", SyncBranchName)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to count commits on %s: %v", SyncBranchName, err)
	}

	commitCount := strings.TrimSpace(string(output))
	if commitCount != "1" {
		t.Errorf("commit count on %s = %s, want 1", SyncBranchName, commitCount)
	}

	// Verify working tree was not modified (plumbing commands only)
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	afterStatus, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get git status after: %v", err)
	}
	if string(beforeStatus) != string(afterStatus) {
		t.Errorf("working tree was modified:\nBefore: %s\nAfter: %s", string(beforeStatus), string(afterStatus))
	}
}

func TestBranchManager_CreateSyncBranch_NotGitRepo(t *testing.T) {
	tmpDir := t.TempDir()

	bm := NewBranchManager(tmpDir)

	// Should fail because it's not a git repo
	err := bm.CreateSyncBranch()
	if err == nil {
		t.Error("CreateSyncBranch should fail for non-git directory")
	}

	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error message = %q, want to contain 'not a git repository'", err.Error())
	}
}

func TestBranchManager_GetSyncBranchRef(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	bm := NewBranchManager(repoPath)

	// Create the sync branch
	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("CreateSyncBranch failed: %v", err)
	}

	// Get the ref
	ref, err := bm.GetSyncBranchRef()
	if err != nil {
		t.Fatalf("GetSyncBranchRef failed: %v", err)
	}

	if ref == "" {
		t.Error("GetSyncBranchRef returned empty ref")
	}

	// Verify it's a valid git commit hash (40 hex characters)
	if len(ref) != 40 {
		t.Errorf("ref length = %d, want 40", len(ref))
	}
}

func TestBranchManager_GetSyncBranchRef_NotExists(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)

	bm := NewBranchManager(repoPath)

	// Try to get ref of non-existent branch
	_, err := bm.GetSyncBranchRef()
	if err == nil {
		t.Error("GetSyncBranchRef should fail when branch doesn't exist")
	}
}

func TestBranchManager_EnsureSyncBranch_LocalOnly(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	bm := NewBranchManager(repoPath)

	// Ensure sync branch (no remote configured)
	if err := bm.EnsureSyncBranch(); err != nil {
		t.Fatalf("EnsureSyncBranch failed: %v", err)
	}

	// Verify branch exists locally
	if !branchExists(t, repoPath, SyncBranchName) {
		t.Errorf("branch %s does not exist locally", SyncBranchName)
	}
}

func TestBranchManager_EnsureSyncBranch_WithRemote(t *testing.T) {
	// Create a bare remote repository
	remoteDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create bare remote: %v", err)
	}

	// Create local repository
	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	// Add remote
	cmd = exec.Command("git", "remote", "add", "origin", remoteDir) //nolint:gosec // G204 test uses controlled paths
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to add remote: %v", err)
	}

	// Push main branch to remote first (so remote has commits)
	cmd = exec.Command("git", "push", "-u", "origin", "master")
	cmd.Dir = repoPath
	_ = cmd.Run() // Best effort, might fail if branch isn't called master

	bm := NewBranchManager(repoPath)

	// Ensure sync branch (should push to remote)
	if err := bm.EnsureSyncBranch(); err != nil {
		t.Fatalf("EnsureSyncBranch failed: %v", err)
	}

	// Verify branch exists locally
	if !branchExists(t, repoPath, SyncBranchName) {
		t.Errorf("branch %s does not exist locally", SyncBranchName)
	}

	// Note: We can't easily verify remote push in unit tests without network
	// Integration tests should cover remote push scenarios
}

func TestBranchManager_checkGitRepo(t *testing.T) {
	t.Run("valid git repo", func(t *testing.T) {
		repoPath := setupTestRepo(t)
		bm := NewBranchManager(repoPath)

		if err := bm.checkGitRepo(); err != nil {
			t.Errorf("checkGitRepo failed for valid repo: %v", err)
		}
	})

	t.Run("not a git repo", func(t *testing.T) {
		tmpDir := t.TempDir()
		bm := NewBranchManager(tmpDir)

		err := bm.checkGitRepo()
		if err == nil {
			t.Error("checkGitRepo should fail for non-git directory")
		}
	})
}

func TestBranchManager_branchExists(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)
	bm := NewBranchManager(repoPath)

	t.Run("existing branch", func(t *testing.T) {
		// Get the current branch name (HEAD)
		currentBranch := getCurrentBranch(t, repoPath)
		if currentBranch == "" {
			t.Skip("no current branch found")
		}

		exists := bm.branchExists(currentBranch)
		if !exists {
			t.Errorf("branchExists returned false for existing branch %s", currentBranch)
		}
	})

	t.Run("non-existing branch", func(t *testing.T) {
		exists := bm.branchExists("non-existent-branch")
		if exists {
			t.Error("branchExists returned true for non-existent branch")
		}
	})
}

func TestBranchManager_CreateSyncBranch_AlwaysOrphan(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	bm := NewBranchManager(repoPath)

	// Create the sync branch
	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("CreateSyncBranch failed: %v", err)
	}

	// Verify branch exists
	if !branchExists(t, repoPath, SyncBranchName) {
		t.Errorf("branch %s does not exist", SyncBranchName)
	}

	// Verify a-sync has no shared history with main (orphan branch)
	// Get merge-base between main and a-sync - should fail for orphan branches
	cmd := exec.Command("git", "merge-base", "HEAD", SyncBranchName)
	cmd.Dir = repoPath
	err := cmd.Run()
	if err == nil {
		t.Error("a-sync branch shares history with main - not an orphan branch")
	}

	// Verify a-sync has exactly 1 commit (the initial empty tree commit)
	cmd = exec.Command("git", "rev-list", "--count", SyncBranchName)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to count commits on %s: %v", SyncBranchName, err)
	}

	commitCount := strings.TrimSpace(string(output))
	if commitCount != "1" {
		t.Errorf("commit count on %s = %s, want 1 (orphan with single empty commit)", SyncBranchName, commitCount)
	}
}

func TestBranchManager_CreateSyncWorktree(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	bm := NewBranchManager(repoPath)

	// Create the sync branch first
	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("CreateSyncBranch failed: %v", err)
	}

	// Create worktree at the new .git/thrum-sync/a-sync location
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	if err := bm.CreateSyncWorktree(syncDir); err != nil {
		t.Fatalf("CreateSyncWorktree failed: %v", err)
	}

	// Verify worktree directory exists
	if _, err := os.Stat(syncDir); os.IsNotExist(err) {
		t.Errorf("worktree directory does not exist: %s", syncDir)
	}

	// Verify .git file exists (worktrees have a .git file, not directory)
	gitFilePath := filepath.Join(syncDir, ".git")
	info, err := os.Stat(gitFilePath)
	if err != nil {
		t.Fatalf("worktree .git file does not exist: %v", err)
	}
	if info.IsDir() {
		t.Error("worktree .git should be a file, not a directory")
	}

	// Verify worktree is on the correct branch
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = syncDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get branch in worktree: %v", err)
	}

	currentBranch := strings.TrimSpace(string(output))
	if currentBranch != SyncBranchName {
		t.Errorf("worktree branch = %s, want %s", currentBranch, SyncBranchName)
	}
}

func TestBranchManager_CreateSyncWorktree_SparseCheckout(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	bm := NewBranchManager(repoPath)

	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("CreateSyncBranch failed: %v", err)
	}

	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	if err := bm.CreateSyncWorktree(syncDir); err != nil {
		t.Fatalf("CreateSyncWorktree failed: %v", err)
	}

	// Resolve the worktree git dir to find sparse-checkout config
	gitDir := bm.resolveWorktreeGitDir(syncDir)
	if gitDir == "" {
		t.Fatal("failed to resolve worktree git dir")
	}

	sparseFile := filepath.Join(gitDir, "info", "sparse-checkout")
	data, err := os.ReadFile(sparseFile) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("sparse-checkout file not found: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "events.jsonl") {
		t.Errorf("sparse-checkout missing events.jsonl pattern, got:\n%s", content)
	}
	if !strings.Contains(content, "messages") {
		t.Errorf("sparse-checkout missing messages pattern, got:\n%s", content)
	}
}

func TestBranchManager_CreateSyncWorktree_NoSparseCheckoutLeak(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	bm := NewBranchManager(repoPath)

	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("CreateSyncBranch failed: %v", err)
	}

	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	if err := bm.CreateSyncWorktree(syncDir); err != nil {
		t.Fatalf("CreateSyncWorktree failed: %v", err)
	}

	// Verify main repo's core.sparseCheckout is false (not leaked)
	cmd := exec.Command("git", "config", "--get", "core.sparseCheckout")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err == nil {
		val := strings.TrimSpace(string(output))
		if val == "true" {
			t.Errorf("main repo core.sparseCheckout = %q, want false or unset", val)
		}
	}
	// err != nil means the config key is unset, which is also fine
}

func TestBranchManager_CreateSyncWorktree_Idempotent(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	bm := NewBranchManager(repoPath)

	// Create the sync branch
	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("CreateSyncBranch failed: %v", err)
	}

	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")

	// Create worktree first time
	if err := bm.CreateSyncWorktree(syncDir); err != nil {
		t.Fatalf("CreateSyncWorktree (first) failed: %v", err)
	}

	// Create worktree second time - should be idempotent
	if err := bm.CreateSyncWorktree(syncDir); err != nil {
		t.Errorf("CreateSyncWorktree (second) failed: %v", err)
	}

	// Verify worktree still exists and is valid
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = syncDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get branch in worktree after second create: %v", err)
	}

	currentBranch := strings.TrimSpace(string(output))
	if currentBranch != SyncBranchName {
		t.Errorf("worktree branch = %s, want %s", currentBranch, SyncBranchName)
	}
}

func TestBranchManager_CreateSyncWorktree_RecoverBroken(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)
	setupThrumFiles(t, repoPath)

	bm := NewBranchManager(repoPath)

	// Create the sync branch
	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("CreateSyncBranch failed: %v", err)
	}

	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")

	// Create worktree
	if err := bm.CreateSyncWorktree(syncDir); err != nil {
		t.Fatalf("CreateSyncWorktree failed: %v", err)
	}

	// Corrupt the worktree by removing it manually (simulating broken state)
	cmd := exec.Command("git", "worktree", "remove", "--force", syncDir) //nolint:gosec // G204 test uses controlled paths
	cmd.Dir = repoPath
	_ = cmd.Run()

	// But leave the directory structure to simulate partial corruption
	if err := os.MkdirAll(syncDir, 0750); err != nil {
		t.Fatalf("failed to create corrupt directory: %v", err)
	}
	// Create a fake .git file
	gitFilePath := filepath.Join(syncDir, ".git")
	if err := os.WriteFile(gitFilePath, []byte("broken"), 0600); err != nil {
		t.Fatalf("failed to create fake .git file: %v", err)
	}

	// Try to create worktree again - should recover
	if err := bm.CreateSyncWorktree(syncDir); err != nil {
		t.Fatalf("CreateSyncWorktree (recovery) failed: %v", err)
	}

	// Verify worktree is now valid
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = syncDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get branch in recovered worktree: %v", err)
	}

	currentBranch := strings.TrimSpace(string(output))
	if currentBranch != SyncBranchName {
		t.Errorf("recovered worktree branch = %s, want %s", currentBranch, SyncBranchName)
	}
}

func TestBranchManager_removeSyncWorktree_AcceptsBothPaths(t *testing.T) {
	repoPath := setupTestRepoWithCommit(t)
	bm := NewBranchManager(repoPath)

	// Test that removeSyncWorktree doesn't panic for .git paths
	gitPath := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	bm.removeSyncWorktree(gitPath) // Should not panic

	// Test that removeSyncWorktree doesn't panic for .thrum paths
	thrumPath := filepath.Join(repoPath, ".thrum", "sync")
	bm.removeSyncWorktree(thrumPath) // Should not panic

	// Test that removeSyncWorktree refuses arbitrary paths
	bm.removeSyncWorktree("/tmp/some-random-path") // Should be a no-op (safety guard)
}

func TestBranchManager_isWorktreeRegistered(t *testing.T) {
	bm := NewBranchManager("/tmp/test")

	porcelain := "worktree /tmp/main\nHEAD abc123\nbranch refs/heads/main\n\nworktree /tmp/sync\nHEAD def456\nbranch refs/heads/a-sync\n"

	t.Run("exact match", func(t *testing.T) {
		if !bm.isWorktreeRegistered(porcelain, "/tmp/sync") {
			t.Error("expected /tmp/sync to be registered")
		}
	})

	t.Run("no match", func(t *testing.T) {
		if bm.isWorktreeRegistered(porcelain, "/tmp/other") {
			t.Error("expected /tmp/other to NOT be registered")
		}
	})

	t.Run("empty output", func(t *testing.T) {
		if bm.isWorktreeRegistered("", "/tmp/sync") {
			t.Error("expected no match in empty output")
		}
	})
}
