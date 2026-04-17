package cli

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestInit_WritesIdentityBlock(t *testing.T) {
	tmp := t.TempDir()
	initGitRepo(t, tmp)

	if err := Init(InitOptions{
		RepoPath: tmp,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	cfgBytes, err := os.ReadFile(filepath.Join(tmp, ".thrum", "config.json")) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	var parsed struct {
		Identity struct {
			DaemonID string `json:"daemon_id"`
			InitAt   string `json:"init_at"`
		} `json:"identity"`
	}
	if err := json.Unmarshal(cfgBytes, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Identity.DaemonID == "" || !strings.HasPrefix(parsed.Identity.DaemonID, "d_") {
		t.Fatalf("daemon_id missing or malformed: %q", parsed.Identity.DaemonID)
	}
	if parsed.Identity.InitAt == "" {
		t.Fatalf("init_at missing")
	}
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

// --- Test helpers for sync-branch reconciliation tests ---

// setupThrumRepo creates a git repo, runs thrum init once (creating .thrum/
// with default config), and returns the repo path. Used to build a "previously
// initialized" baseline for --force tests.
func setupThrumRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)
	if err := Init(InitOptions{RepoPath: tmpDir}); err != nil {
		t.Fatalf("initial Init failed: %v", err)
	}
	return tmpDir
}

// writeLocalASyncWithContent points refs/heads/a-sync at a new commit whose
// tree has the given events.jsonl content. Overwrites any existing a-sync.
func writeLocalASyncWithContent(t *testing.T, repoPath, events string) string {
	t.Helper()
	blob := gitStdin(t, repoPath, []string{"hash-object", "-w", "--stdin"}, events)
	tree := gitStdin(t, repoPath, []string{"mktree"},
		fmt.Sprintf("100644 blob %s\tevents.jsonl\n", blob))
	commit := gitOut(t, repoPath, "commit-tree", tree, "-m", "local a-sync test commit")
	gitRun(t, repoPath, "update-ref", "refs/heads/a-sync", commit)
	return commit
}

// writeRemoteTrackingASyncWithContent points refs/remotes/origin/a-sync at a
// new commit whose tree has the given events.jsonl content. Also adds an
// origin remote pointing to the same repo so set-upstream-to works.
func writeRemoteTrackingASyncWithContent(t *testing.T, repoPath, events string) string {
	t.Helper()
	blob := gitStdin(t, repoPath, []string{"hash-object", "-w", "--stdin"}, events)
	tree := gitStdin(t, repoPath, []string{"mktree"},
		fmt.Sprintf("100644 blob %s\tevents.jsonl\n", blob))
	commit := gitOut(t, repoPath, "commit-tree", tree, "-m", "remote a-sync test commit")
	gitRun(t, repoPath, "update-ref", "refs/remotes/origin/a-sync", commit)
	// Ensure an origin remote exists — idempotent (ignore error if already present)
	_ = exec.Command("git", "-C", repoPath, "remote", "add", "origin", repoPath).Run()
	return commit
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	if err := exec.Command("git", append([]string{"-C", dir}, args...)...).Run(); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func gitStdin(t *testing.T, dir string, args []string, stdin string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v (stdin): %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// --- reconcileSyncBranch tests ---

func TestReconcileSyncBranch_Row4_KeepLocalNoRemote(t *testing.T) {
	tmpDir := setupThrumRepo(t)
	localSHA := writeLocalASyncWithContent(t, tmpDir, `{"e":1}`+"\n")

	recon, err := reconcileSyncBranch(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if recon.AttachToRemoteSHA != "" {
		t.Errorf("row 4: expected no attach, got SHA %q", recon.AttachToRemoteSHA)
	}
	if recon.LocalOnlyOverride != nil {
		t.Errorf("row 4: expected no LocalOnly override, got %v", *recon.LocalOnlyOverride)
	}
	if got := gitOut(t, tmpDir, "rev-parse", "refs/heads/a-sync"); got != localSHA {
		t.Errorf("row 4: local a-sync was modified")
	}
}

func TestReconcileSyncBranch_Row5_AttachLocalEmptyRemoteNonEmpty(t *testing.T) {
	tmpDir := setupThrumRepo(t)
	writeLocalASyncWithContent(t, tmpDir, "")
	remoteSHA := writeRemoteTrackingASyncWithContent(t, tmpDir, `{"e":1}`+"\n")

	recon, err := reconcileSyncBranch(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if recon.AttachToRemoteSHA != remoteSHA {
		t.Errorf("row 5: expected attach to remote SHA %q, got %q", remoteSHA, recon.AttachToRemoteSHA)
	}
	if recon.LocalOnlyOverride == nil || *recon.LocalOnlyOverride != false {
		t.Errorf("row 5: expected LocalOnly override = false")
	}
}

func TestReconcileSyncBranch_Row6_KeepLocalNonEmptyRemoteEmpty(t *testing.T) {
	tmpDir := setupThrumRepo(t)
	localSHA := writeLocalASyncWithContent(t, tmpDir, `{"e":1}`+"\n")
	writeRemoteTrackingASyncWithContent(t, tmpDir, "")

	recon, err := reconcileSyncBranch(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if recon.AttachToRemoteSHA != "" {
		t.Errorf("row 6: expected no attach, got SHA %q", recon.AttachToRemoteSHA)
	}
	if recon.LocalOnlyOverride == nil || *recon.LocalOnlyOverride != false {
		t.Errorf("row 6: expected LocalOnly override = false")
	}
	if got := gitOut(t, tmpDir, "rev-parse", "refs/heads/a-sync"); got != localSHA {
		t.Errorf("row 6: local a-sync was modified")
	}
}

func TestReconcileSyncBranch_Row7_BothEmptyAttach(t *testing.T) {
	tmpDir := setupThrumRepo(t)
	writeLocalASyncWithContent(t, tmpDir, "")
	remoteSHA := writeRemoteTrackingASyncWithContent(t, tmpDir, "")

	recon, err := reconcileSyncBranch(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if recon.AttachToRemoteSHA != remoteSHA {
		t.Errorf("row 7: expected attach to remote SHA %q, got %q", remoteSHA, recon.AttachToRemoteSHA)
	}
	if recon.LocalOnlyOverride == nil || *recon.LocalOnlyOverride != false {
		t.Errorf("row 7: expected LocalOnly override = false")
	}
}

func TestReconcileSyncBranch_Row8_BothNonEmptyReturnsError(t *testing.T) {
	tmpDir := setupThrumRepo(t)
	writeLocalASyncWithContent(t, tmpDir, `{"e":"local"}`+"\n")
	writeRemoteTrackingASyncWithContent(t, tmpDir, `{"e":"remote"}`+"\n")

	_, err := reconcileSyncBranch(context.Background(), tmpDir)
	if err == nil {
		t.Fatal("row 8: expected error when both branches have content, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "git push --force-with-lease origin a-sync") {
		t.Errorf("row 8: error missing 'keep local' command. Got: %s", msg)
	}
	if !strings.Contains(msg, "git update-ref refs/heads/a-sync refs/remotes/origin/a-sync") {
		t.Errorf("row 8: error missing 'keep remote' command. Got: %s", msg)
	}
	if !strings.Contains(msg, "thrum-uvpp.1") {
		t.Errorf("row 8: error missing thrum-uvpp.1 bead reference. Got: %s", msg)
	}
}

func TestReconcileSyncBranch_NoLocalNoRemote_ReturnsEmpty(t *testing.T) {
	// Matrix row 2 via reconcile (no local a-sync). reconcile should return
	// empty directive; CreateSyncBranch handles orphan creation downstream.
	tmpDir := setupThrumRepo(t)
	// The Init inside setupThrumRepo already created a-sync; delete it to
	// simulate "not yet created" for this reconcile call.
	gitRun(t, tmpDir, "update-ref", "-d", "refs/heads/a-sync")

	recon, err := reconcileSyncBranch(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recon.AttachToRemoteSHA != "" || recon.LocalOnlyOverride != nil {
		t.Errorf("expected empty reconciliation, got %+v", recon)
	}
}

func TestReconcileSyncBranch_NoLocalRemotePresent_AttachDirective(t *testing.T) {
	// Matrix row 3 via reconcile. The CLI calls reconcile before worktree setup
	// to know whether to flip LocalOnly; CreateSyncBranch itself also attaches.
	tmpDir := setupThrumRepo(t)
	gitRun(t, tmpDir, "update-ref", "-d", "refs/heads/a-sync")
	remoteSHA := writeRemoteTrackingASyncWithContent(t, tmpDir, `{"e":1}`+"\n")

	recon, err := reconcileSyncBranch(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recon.AttachToRemoteSHA != remoteSHA {
		t.Errorf("row 3 via reconcile: expected attach SHA %q, got %q", remoteSHA, recon.AttachToRemoteSHA)
	}
	if recon.LocalOnlyOverride == nil || *recon.LocalOnlyOverride != false {
		t.Errorf("row 3: expected LocalOnly override = false")
	}
}
