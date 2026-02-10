package gitctx_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/gitctx"
)

// setupGitRepo creates a temporary git repository for testing.
func setupGitRepo(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()

	// Initialize git repo
	runGit(t, tmpDir, "init")
	runGit(t, tmpDir, "config", "user.name", "Test User")
	runGit(t, tmpDir, "config", "user.email", "test@example.com")

	// Create initial commit on main
	runGit(t, tmpDir, "checkout", "-b", "main")
	writeFile(t, tmpDir, "README.md", "# Test Repo")
	runGit(t, tmpDir, "add", "README.md")
	runGit(t, tmpDir, "commit", "-m", "Initial commit")

	return tmpDir
}

// runGit runs a git command in the specified directory.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\nOutput: %s", strings.Join(args, " "), err, output)
	}
}

// writeFile writes content to a file.
func writeFile(t *testing.T, dir, filename, content string) {
	t.Helper()

	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

func TestExtractWorkContext_ValidRepo(t *testing.T) {
	repoPath := setupGitRepo(t)

	ctx, err := gitctx.ExtractWorkContext(repoPath)
	if err != nil {
		t.Fatalf("ExtractWorkContext failed: %v", err)
	}

	// Verify basic fields
	if ctx.Branch != "main" {
		t.Errorf("Expected branch 'main', got '%s'", ctx.Branch)
	}

	// Resolve paths to handle symlinks (e.g., /var -> /private/var on macOS)
	expectedPath, _ := filepath.EvalSymlinks(repoPath)
	actualPath, _ := filepath.EvalSymlinks(ctx.WorktreePath)
	if actualPath != expectedPath {
		t.Errorf("Expected worktree_path '%s', got '%s'", expectedPath, actualPath)
	}

	if time.Since(ctx.ExtractedAt) > 5*time.Second {
		t.Errorf("ExtractedAt timestamp too old: %v", ctx.ExtractedAt)
	}

	// Should have no unmerged commits (we're on main with no remote)
	if len(ctx.UnmergedCommits) != 0 {
		t.Errorf("Expected 0 unmerged commits, got %d", len(ctx.UnmergedCommits))
	}
}

func TestExtractWorkContext_NotGitRepo(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, err := gitctx.ExtractWorkContext(tmpDir)
	if err != nil {
		t.Fatalf("ExtractWorkContext should not error on non-git repo: %v", err)
	}

	// Should return empty context
	if ctx.Branch != "" {
		t.Errorf("Expected empty branch, got '%s'", ctx.Branch)
	}

	if ctx.WorktreePath != "" {
		t.Errorf("Expected empty worktree_path, got '%s'", ctx.WorktreePath)
	}
}

func TestExtractWorkContext_UncommittedFiles(t *testing.T) {
	repoPath := setupGitRepo(t)

	// Create some uncommitted files
	writeFile(t, repoPath, "staged.txt", "staged content")
	runGit(t, repoPath, "add", "staged.txt")

	writeFile(t, repoPath, "modified.txt", "modified content")

	ctx, err := gitctx.ExtractWorkContext(repoPath)
	if err != nil {
		t.Fatalf("ExtractWorkContext failed: %v", err)
	}

	// Should detect uncommitted files
	if len(ctx.UncommittedFiles) != 2 {
		t.Errorf("Expected 2 uncommitted files, got %d: %v", len(ctx.UncommittedFiles), ctx.UncommittedFiles)
	}

	// Verify files are in the list
	hasStaged := false
	hasModified := false
	for _, file := range ctx.UncommittedFiles {
		if file == "staged.txt" {
			hasStaged = true
		}
		if file == "modified.txt" {
			hasModified = true
		}
	}

	if !hasStaged {
		t.Error("staged.txt not found in uncommitted files")
	}
	if !hasModified {
		t.Error("modified.txt not found in uncommitted files")
	}
}

func TestExtractWorkContext_UnmergedCommits(t *testing.T) {
	repoPath := setupGitRepo(t)

	// Create a remote-tracking branch (simulate origin/main)
	runGit(t, repoPath, "branch", "origin/main")

	// Create a feature branch with commits
	runGit(t, repoPath, "checkout", "-b", "feature/test")
	writeFile(t, repoPath, "feature1.txt", "feature 1")
	runGit(t, repoPath, "add", "feature1.txt")
	runGit(t, repoPath, "commit", "-m", "Add feature 1")

	writeFile(t, repoPath, "feature2.txt", "feature 2")
	runGit(t, repoPath, "add", "feature2.txt")
	runGit(t, repoPath, "commit", "-m", "Add feature 2")

	ctx, err := gitctx.ExtractWorkContext(repoPath)
	if err != nil {
		t.Fatalf("ExtractWorkContext failed: %v", err)
	}

	// Should detect unmerged commits
	if len(ctx.UnmergedCommits) != 2 {
		t.Errorf("Expected 2 unmerged commits, got %d", len(ctx.UnmergedCommits))
	}

	// Verify commit messages (in reverse chronological order)
	if len(ctx.UnmergedCommits) >= 1 {
		if !strings.Contains(ctx.UnmergedCommits[0].Message, "Add feature 2") {
			t.Errorf("Expected first commit to be 'Add feature 2', got '%s'", ctx.UnmergedCommits[0].Message)
		}
	}

	if len(ctx.UnmergedCommits) >= 2 {
		if !strings.Contains(ctx.UnmergedCommits[1].Message, "Add feature 1") {
			t.Errorf("Expected second commit to be 'Add feature 1', got '%s'", ctx.UnmergedCommits[1].Message)
		}
	}

	// Verify files in commits
	if len(ctx.UnmergedCommits) >= 1 {
		if len(ctx.UnmergedCommits[0].Files) != 1 || ctx.UnmergedCommits[0].Files[0] != "feature2.txt" {
			t.Errorf("Expected commit 1 to have [feature2.txt], got %v", ctx.UnmergedCommits[0].Files)
		}
	}
}

func TestExtractWorkContext_ChangedFiles(t *testing.T) {
	repoPath := setupGitRepo(t)

	// Create origin/main
	runGit(t, repoPath, "branch", "origin/main")

	// Create feature branch and modify files
	runGit(t, repoPath, "checkout", "-b", "feature/changes")
	writeFile(t, repoPath, "file1.txt", "change 1")
	runGit(t, repoPath, "add", "file1.txt")
	runGit(t, repoPath, "commit", "-m", "Change 1")

	writeFile(t, repoPath, "file2.txt", "change 2")
	runGit(t, repoPath, "add", "file2.txt")
	runGit(t, repoPath, "commit", "-m", "Change 2")

	ctx, err := gitctx.ExtractWorkContext(repoPath)
	if err != nil {
		t.Fatalf("ExtractWorkContext failed: %v", err)
	}

	// Should detect changed files
	if len(ctx.ChangedFiles) != 2 {
		t.Errorf("Expected 2 changed files, got %d: %v", len(ctx.ChangedFiles), ctx.ChangedFiles)
	}

	// Verify files
	hasFile1 := false
	hasFile2 := false
	for _, file := range ctx.ChangedFiles {
		if file == "file1.txt" {
			hasFile1 = true
		}
		if file == "file2.txt" {
			hasFile2 = true
		}
	}

	if !hasFile1 {
		t.Error("file1.txt not found in changed files")
	}
	if !hasFile2 {
		t.Error("file2.txt not found in changed files")
	}
}

func TestExtractWorkContext_NoRemote(t *testing.T) {
	repoPath := setupGitRepo(t)

	// Create multiple commits on main (no origin/main or origin/master)
	for i := range 5 {
		writeFile(t, repoPath, "file"+string(rune('1'+i))+".txt", "content")
		runGit(t, repoPath, "add", ".")
		runGit(t, repoPath, "commit", "-m", "Commit "+string(rune('1'+i)))
	}

	ctx, err := gitctx.ExtractWorkContext(repoPath)
	if err != nil {
		t.Fatalf("ExtractWorkContext failed: %v", err)
	}

	// Should fall back to HEAD~10 (but we only have 6 commits total, so might show some)
	// The key is that it doesn't error
	if ctx.Branch != "main" {
		t.Errorf("Expected branch 'main', got '%s'", ctx.Branch)
	}
}

func TestExtractWorkContext_EmptyRepo(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize empty repo (no commits)
	runGit(t, tmpDir, "init")

	ctx, err := gitctx.ExtractWorkContext(tmpDir)
	if err != nil {
		t.Fatalf("ExtractWorkContext should not error on empty repo: %v", err)
	}

	// Resolve paths to handle symlinks
	expectedPath, _ := filepath.EvalSymlinks(tmpDir)
	actualPath, _ := filepath.EvalSymlinks(ctx.WorktreePath)
	if actualPath != expectedPath {
		t.Errorf("Expected worktree_path '%s', got '%s'", expectedPath, actualPath)
	}
}

func TestExtractWorkContext_Performance(t *testing.T) {
	repoPath := setupGitRepo(t)

	// Create some activity
	runGit(t, repoPath, "branch", "origin/main")
	runGit(t, repoPath, "checkout", "-b", "feature/perf")
	for i := 0; i < 3; i++ {
		writeFile(t, repoPath, "file"+string(rune('0'+i))+".txt", "content")
		runGit(t, repoPath, "add", ".")
		runGit(t, repoPath, "commit", "-m", "Commit "+string(rune('0'+i)))
	}

	// Benchmark extraction time
	start := time.Now()
	_, err := gitctx.ExtractWorkContext(repoPath)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ExtractWorkContext failed: %v", err)
	}

	// Should complete in less than 150ms (generous upper bound for CI/slower systems)
	if elapsed > 150*time.Millisecond {
		t.Errorf("ExtractWorkContext took too long: %v (expected < 150ms)", elapsed)
	}

	t.Logf("ExtractWorkContext completed in %v", elapsed)
}

func BenchmarkExtractWorkContext(b *testing.B) {
	// Setup repo once
	tmpDir := b.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		b.Fatalf("git init: %v", err)
	}

	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		b.Fatalf("git config name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		b.Fatalf("git config email: %v", err)
	}

	// Create initial commit
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("test"), 0600); err != nil {
		b.Fatalf("write file: %v", err)
	}
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		b.Fatalf("git add: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		b.Fatalf("git commit: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = gitctx.ExtractWorkContext(tmpDir)
	}
}
