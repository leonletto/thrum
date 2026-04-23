package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Initialize git repo
	initGitRepo(t, tmpDir)

	opts := InitOptions{
		RepoPath: tmpDir,
		Force:    false,
	}

	err := Init(opts)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Verify .thrum/ directory exists
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if _, err := os.Stat(thrumDir); os.IsNotExist(err) {
		t.Error(".thrum/ directory was not created")
	}

	// Verify .thrum/var/ directory exists
	varDir := filepath.Join(thrumDir, "var")
	if _, err := os.Stat(varDir); os.IsNotExist(err) {
		t.Error(".thrum/var/ directory was not created")
	}

	// Verify schema_version file
	schemaPath := filepath.Join(thrumDir, "schema_version")
	content, err := os.ReadFile(schemaPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Errorf("Failed to read schema_version: %v", err)
	}
	if strings.TrimSpace(string(content)) != "1" {
		t.Errorf("Expected schema_version to be '1', got %q", string(content))
	}

	// Verify messages.jsonl does NOT exist in main .thrum/ (it's in the worktree now)
	messagesPath := filepath.Join(thrumDir, "messages.jsonl")
	if _, err := os.Stat(messagesPath); err == nil {
		t.Error("messages.jsonl should not exist in main .thrum/ directory (should be in worktree)")
	}

	// Verify .gitignore was updated to ignore all of .thrum/
	gitignorePath := filepath.Join(tmpDir, ".gitignore")
	content, err = os.ReadFile(gitignorePath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Errorf("Failed to read .gitignore: %v", err)
	}
	gitignoreStr := string(content)
	if !strings.Contains(gitignoreStr, ".thrum/") {
		t.Error(".gitignore does not contain .thrum/")
	}
	if !strings.Contains(gitignoreStr, ".thrum.*.json") {
		t.Error(".gitignore does not contain .thrum.*.json")
	}

	// Verify a-sync branch was created
	cmd := exec.Command("git", "rev-parse", "--verify", "a-sync")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Error("a-sync branch was not created")
	}

	// Verify worktree was created at .git/thrum-sync/a-sync
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")
	if _, err := os.Stat(syncDir); os.IsNotExist(err) {
		t.Error("sync worktree directory was not created at .git/thrum-sync/a-sync")
	}

	// Verify worktree has .git file (not directory)
	gitFilePath := filepath.Join(syncDir, ".git")
	info, err := os.Stat(gitFilePath)
	if err != nil {
		t.Errorf("worktree .git file does not exist: %v", err)
	} else if info.IsDir() {
		t.Error("worktree .git should be a file, not a directory")
	}

	// Verify events.jsonl exists in the worktree
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); os.IsNotExist(err) {
		t.Error("events.jsonl was not created in worktree")
	}

	// Verify messages directory exists in the worktree
	messagesDir := filepath.Join(syncDir, "messages")
	if _, err := os.Stat(messagesDir); os.IsNotExist(err) {
		t.Error("messages directory was not created in worktree")
	}
}

func TestInit_AlreadyInitialized(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	opts := InitOptions{
		RepoPath: tmpDir,
		Force:    false,
	}

	// First init should succeed
	if err := Init(opts); err != nil {
		t.Fatalf("First init failed: %v", err)
	}

	// Second init should fail
	err := Init(opts)
	if err == nil {
		t.Fatal("Expected error when reinitializing without --force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("Expected 'already exists' error, got: %v", err)
	}
}

func TestInit_ForceReinitialize(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	opts := InitOptions{
		RepoPath: tmpDir,
		Force:    false,
	}

	// First init
	if err := Init(opts); err != nil {
		t.Fatalf("First init failed: %v", err)
	}

	// Force reinit should succeed
	opts.Force = true
	if err := Init(opts); err != nil {
		t.Errorf("Force reinit failed: %v", err)
	}
}

func TestInit_NotGitRepo(t *testing.T) {
	tmpDir := t.TempDir()
	// Don't initialize git repo

	opts := InitOptions{
		RepoPath: tmpDir,
		Force:    false,
	}

	err := Init(opts)
	if err == nil {
		t.Fatal("Expected error when initializing in non-git repo")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("Expected 'not a git repository' error, got: %v", err)
	}
}

func TestUpdateGitignore_NewFile(t *testing.T) {
	tmpDir := t.TempDir()

	err := updateGitignore(tmpDir)
	if err != nil {
		t.Fatalf("updateGitignore failed: %v", err)
	}

	gitignorePath := filepath.Join(tmpDir, ".gitignore")
	content, err := os.ReadFile(gitignorePath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, ".thrum/") {
		t.Error(".gitignore does not contain .thrum/")
	}
	if !strings.Contains(contentStr, ".thrum.*.json") {
		t.Error(".gitignore does not contain .thrum.*.json")
	}
}

func TestUpdateGitignore_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	gitignorePath := filepath.Join(tmpDir, ".gitignore")

	// Create existing .gitignore
	existing := "# Existing content\nnode_modules/\n"
	if err := os.WriteFile(gitignorePath, []byte(existing), 0600); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	err := updateGitignore(tmpDir)
	if err != nil {
		t.Fatalf("updateGitignore failed: %v", err)
	}

	content, err := os.ReadFile(gitignorePath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	contentStr := string(content)
	// Should preserve existing content
	if !strings.Contains(contentStr, "node_modules/") {
		t.Error(".gitignore lost existing content")
	}
	// Should add new content
	if !strings.Contains(contentStr, ".thrum/") {
		t.Error(".gitignore does not contain .thrum/")
	}
}

func TestUpdateGitignore_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()

	// First update
	if err := updateGitignore(tmpDir); err != nil {
		t.Fatalf("First updateGitignore failed: %v", err)
	}

	gitignorePath := filepath.Join(tmpDir, ".gitignore")
	firstContent, err := os.ReadFile(gitignorePath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	// Second update
	if err := updateGitignore(tmpDir); err != nil {
		t.Fatalf("Second updateGitignore failed: %v", err)
	}

	secondContent, err := os.ReadFile(gitignorePath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Failed to read .gitignore after second update: %v", err)
	}

	// Content should be identical
	if string(firstContent) != string(secondContent) {
		t.Error("updateGitignore is not idempotent - content changed on second run")
	}
}

func TestIsGitWorktree(t *testing.T) {
	// Create main repo
	mainDir := t.TempDir()
	initGitRepo(t, mainDir)

	t.Run("main repo is not a worktree", func(t *testing.T) {
		isWT, _, err := IsGitWorktree(mainDir)
		if err != nil {
			t.Fatalf("IsGitWorktree error: %v", err)
		}
		if isWT {
			t.Error("main repo should not be detected as a worktree")
		}
	})

	t.Run("git worktree is detected", func(t *testing.T) {
		// Create a worktree
		wtDir := filepath.Join(t.TempDir(), "worktree")
		cmd := exec.Command("git", "worktree", "add", wtDir, "-b", "test-branch")
		cmd.Dir = mainDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git worktree add: %v", err)
		}

		isWT, mainRoot, err := IsGitWorktree(wtDir)
		if err != nil {
			t.Fatalf("IsGitWorktree error: %v", err)
		}
		if !isWT {
			t.Error("worktree should be detected as a worktree")
		}

		// mainRoot should point to the main repo (resolve symlinks for macOS /var → /private/var)
		absMainDir, _ := filepath.Abs(mainDir)
		realMainDir, _ := filepath.EvalSymlinks(absMainDir)
		realMainRoot, _ := filepath.EvalSymlinks(mainRoot)
		if realMainRoot != realMainDir {
			t.Errorf("expected main root %s, got %s", realMainDir, realMainRoot)
		}
	})

	t.Run("non-git directory returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		_, _, err := IsGitWorktree(tmpDir)
		if err == nil {
			t.Error("expected error for non-git directory")
		}
	})
}

// Helper function to initialize a git repository.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	// git init
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Configure git user (required for commits)
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = dir
	_ = cmd.Run()

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	_ = cmd.Run()

	// Create initial commit
	readmePath := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test Repo\n"), 0600); err != nil {
		t.Fatalf("Failed to create README: %v", err)
	}

	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to add README: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create initial commit: %v", err)
	}
}
