package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveThrumDir_NoRedirect(t *testing.T) {
	// Create temp dir with .thrum/ but no redirect file
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.Mkdir(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	got, err := ResolveThrumDir(tmpDir)
	if err != nil {
		t.Fatalf("ResolveThrumDir failed: %v", err)
	}

	expected := thrumDir
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestResolveThrumDir_WithValidRedirect(t *testing.T) {
	// Create two temp dirs: main worktree and feature worktree
	mainDir := t.TempDir()
	mainThrumDir := filepath.Join(mainDir, ".thrum")
	if err := os.Mkdir(mainThrumDir, 0750); err != nil {
		t.Fatalf("failed to create main .thrum dir: %v", err)
	}

	featureDir := t.TempDir()
	featureThrumDir := filepath.Join(featureDir, ".thrum")
	if err := os.Mkdir(featureThrumDir, 0750); err != nil {
		t.Fatalf("failed to create feature .thrum dir: %v", err)
	}

	// Create redirect file pointing to main's .thrum/
	redirectPath := filepath.Join(featureThrumDir, "redirect")
	if err := os.WriteFile(redirectPath, []byte(mainThrumDir), 0600); err != nil {
		t.Fatalf("failed to write redirect file: %v", err)
	}

	got, err := ResolveThrumDir(featureDir)
	if err != nil {
		t.Fatalf("ResolveThrumDir failed: %v", err)
	}

	if got != mainThrumDir {
		t.Errorf("expected %s, got %s", mainThrumDir, got)
	}
}

func TestResolveThrumDir_RedirectTargetDoesNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.Mkdir(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	// Create redirect pointing to non-existent path
	redirectPath := filepath.Join(thrumDir, "redirect")
	nonExistentPath := "/tmp/thrum-test-nonexistent-12345"
	if err := os.WriteFile(redirectPath, []byte(nonExistentPath), 0600); err != nil {
		t.Fatalf("failed to write redirect file: %v", err)
	}

	_, err := ResolveThrumDir(tmpDir)
	if err == nil {
		t.Fatal("expected error for non-existent redirect target")
	}

	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected error to contain 'does not exist', got: %v", err)
	}
}

func TestResolveThrumDir_RedirectFileIsEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.Mkdir(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	// Create empty redirect file
	redirectPath := filepath.Join(thrumDir, "redirect")
	if err := os.WriteFile(redirectPath, []byte(""), 0600); err != nil {
		t.Fatalf("failed to write redirect file: %v", err)
	}

	_, err := ResolveThrumDir(tmpDir)
	if err == nil {
		t.Fatal("expected error for empty redirect file")
	}

	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected error to contain 'empty', got: %v", err)
	}
}

func TestResolveThrumDir_RedirectWithRelativePath(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.Mkdir(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	// Create redirect with relative path
	redirectPath := filepath.Join(thrumDir, "redirect")
	if err := os.WriteFile(redirectPath, []byte("../other/.thrum"), 0600); err != nil {
		t.Fatalf("failed to write redirect file: %v", err)
	}

	_, err := ResolveThrumDir(tmpDir)
	if err == nil {
		t.Fatal("expected error for relative path in redirect")
	}

	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("expected error to contain 'absolute', got: %v", err)
	}
}

func TestResolveThrumDir_RedirectWithTrailingWhitespace(t *testing.T) {
	// Create two temp dirs: main worktree and feature worktree
	mainDir := t.TempDir()
	mainThrumDir := filepath.Join(mainDir, ".thrum")
	if err := os.Mkdir(mainThrumDir, 0750); err != nil {
		t.Fatalf("failed to create main .thrum dir: %v", err)
	}

	featureDir := t.TempDir()
	featureThrumDir := filepath.Join(featureDir, ".thrum")
	if err := os.Mkdir(featureThrumDir, 0750); err != nil {
		t.Fatalf("failed to create feature .thrum dir: %v", err)
	}

	// Create redirect file with trailing whitespace/newline
	redirectPath := filepath.Join(featureThrumDir, "redirect")
	content := mainThrumDir + "\n  \t\n"
	if err := os.WriteFile(redirectPath, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write redirect file: %v", err)
	}

	got, err := ResolveThrumDir(featureDir)
	if err != nil {
		t.Fatalf("ResolveThrumDir failed: %v", err)
	}

	if got != mainThrumDir {
		t.Errorf("expected %s, got %s", mainThrumDir, got)
	}
}

func TestResolveThrumDir_NoThrumDirectory(t *testing.T) {
	// Empty temp dir, no .thrum/ directory
	tmpDir := t.TempDir()

	got, err := ResolveThrumDir(tmpDir)
	if err != nil {
		t.Fatalf("ResolveThrumDir failed: %v", err)
	}

	expected := filepath.Join(tmpDir, ".thrum")
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestResolveThrumDir_RedirectTargetIsFile(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.Mkdir(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	// Create a regular file to use as redirect target
	targetFile := filepath.Join(tmpDir, "not-a-dir.txt")
	if err := os.WriteFile(targetFile, []byte("content"), 0600); err != nil {
		t.Fatalf("failed to create target file: %v", err)
	}

	// Create redirect pointing to the file
	redirectPath := filepath.Join(thrumDir, "redirect")
	if err := os.WriteFile(redirectPath, []byte(targetFile), 0600); err != nil {
		t.Fatalf("failed to write redirect file: %v", err)
	}

	_, err := ResolveThrumDir(tmpDir)
	if err == nil {
		t.Fatal("expected error when redirect target is a file")
	}

	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected error to contain 'not a directory', got: %v", err)
	}
}

func TestSyncWorktreePath_RealGitRepo(t *testing.T) {
	// Create a real git repo in a temp directory
	tmpDir := t.TempDir()
	cmd := exec.Command("git", "init", tmpDir) //nolint:gosec // G204 test uses controlled paths
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	got, err := SyncWorktreePath(tmpDir)
	if err != nil {
		t.Fatalf("SyncWorktreePath failed: %v", err)
	}

	// Should be inside .git/thrum-sync/a-sync
	expected := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")
	if got != expected {
		t.Errorf("SyncWorktreePath(%q) = %q, want %q", tmpDir, got, expected)
	}
}

func TestSyncWorktreePath_FallbackNonGit(t *testing.T) {
	// A temp directory that is NOT a git repo
	tmpDir := t.TempDir()

	got, err := SyncWorktreePath(tmpDir)
	if err != nil {
		t.Fatalf("SyncWorktreePath failed: %v", err)
	}

	// Should fallback to .git/thrum-sync/a-sync relative to the path
	expected := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")
	if got != expected {
		t.Errorf("SyncWorktreePath(%q) = %q, want %q", tmpDir, got, expected)
	}
}

func TestSyncWorktreePath_RelativeGitCommonDir(t *testing.T) {
	// Create a real git repo — git-common-dir returns relative "." for regular repos
	tmpDir := t.TempDir()
	cmd := exec.Command("git", "init", tmpDir) //nolint:gosec // G204 test uses controlled paths
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	got, err := SyncWorktreePath(tmpDir)
	if err != nil {
		t.Fatalf("SyncWorktreePath failed: %v", err)
	}

	// Result must be an absolute path
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}

	// Must end with thrum-sync/a-sync
	if !strings.HasSuffix(got, filepath.Join("thrum-sync", "a-sync")) {
		t.Errorf("expected path to end with thrum-sync/a-sync, got %q", got)
	}
}

func TestSyncWorktreePath_GitWorktree(t *testing.T) {
	// Create main repo (resolve symlinks for macOS /var -> /private/var)
	mainDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks failed: %v", err)
	}
	cmd := exec.Command("git", "init", mainDir) //nolint:gosec // G204 test uses controlled paths
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Configure git identity for CI environments where user.name/email may not be set
	for _, kv := range [][2]string{{"user.name", "test"}, {"user.email", "test@test.com"}} {
		c := exec.Command("git", "-C", mainDir, "config", kv[0], kv[1]) //nolint:gosec // G204 test uses controlled paths
		if err := c.Run(); err != nil {
			t.Fatalf("git config %s failed: %v", kv[0], err)
		}
	}

	// Create an initial commit so we can create a worktree
	cmd = exec.Command("git", "-C", mainDir, "commit", "--allow-empty", "-m", "init") //nolint:gosec // G204 test uses controlled paths
	if err := cmd.Run(); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}

	// Create a worktree
	wtParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks failed: %v", err)
	}
	wtDir := filepath.Join(wtParent, "wt")
	cmd = exec.Command("git", "-C", mainDir, "worktree", "add", "-b", "test-wt", wtDir) //nolint:gosec // G204 test uses controlled paths
	if err := cmd.Run(); err != nil {
		t.Fatalf("git worktree add failed: %v", err)
	}

	// SyncWorktreePath from the worktree should resolve to the main repo's .git
	got, err := SyncWorktreePath(wtDir)
	if err != nil {
		t.Fatalf("SyncWorktreePath failed: %v", err)
	}

	// The git-common-dir for a worktree points to the main repo's .git/
	expected := filepath.Join(mainDir, ".git", "thrum-sync", "a-sync")
	if got != expected {
		t.Errorf("SyncWorktreePath(%q) = %q, want %q", wtDir, got, expected)
	}
}

func TestVarDir(t *testing.T) {
	tests := []struct {
		name     string
		thrumDir string
		expected string
	}{
		{
			name:     "basic path",
			thrumDir: "/home/user/repo/.thrum",
			expected: "/home/user/repo/.thrum/var",
		},
		{
			name:     "trailing slash",
			thrumDir: "/home/user/repo/.thrum/",
			expected: "/home/user/repo/.thrum/var",
		},
		{
			name:     "relative path",
			thrumDir: ".thrum",
			expected: ".thrum/var",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VarDir(tt.thrumDir)
			if got != tt.expected {
				t.Errorf("VarDir(%q) = %q, want %q", tt.thrumDir, got, tt.expected)
			}
		})
	}
}

func TestIdentitiesDir(t *testing.T) {
	tests := []struct {
		name     string
		repoPath string
		expected string
	}{
		{
			name:     "basic path",
			repoPath: "/home/user/repo",
			expected: "/home/user/repo/.thrum/identities",
		},
		{
			name:     "trailing slash",
			repoPath: "/home/user/repo/",
			expected: "/home/user/repo/.thrum/identities",
		},
		{
			name:     "relative path",
			repoPath: ".",
			expected: ".thrum/identities",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IdentitiesDir(tt.repoPath)
			if got != tt.expected {
				t.Errorf("IdentitiesDir(%q) = %q, want %q", tt.repoPath, got, tt.expected)
			}
		})
	}
}

func TestIsRedirected_WithRedirect(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.Mkdir(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	// Create redirect file
	redirectPath := filepath.Join(thrumDir, "redirect")
	if err := os.WriteFile(redirectPath, []byte("/some/path"), 0600); err != nil {
		t.Fatalf("failed to write redirect file: %v", err)
	}

	if !IsRedirected(tmpDir) {
		t.Error("expected IsRedirected to return true when redirect file exists")
	}
}

func TestIsRedirected_WithoutRedirect(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.Mkdir(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	if IsRedirected(tmpDir) {
		t.Error("expected IsRedirected to return false when redirect file does not exist")
	}
}

func TestResolveThrumDir_RedirectChain(t *testing.T) {
	// Create three temp dirs: A -> B -> C
	dirA := t.TempDir()
	dirB := t.TempDir()
	dirC := t.TempDir()

	thrumA := filepath.Join(dirA, ".thrum")
	thrumB := filepath.Join(dirB, ".thrum")
	thrumC := filepath.Join(dirC, ".thrum")
	if err := os.MkdirAll(thrumA, 0750); err != nil {
		t.Fatalf("failed to create thrumA: %v", err)
	}
	if err := os.MkdirAll(thrumB, 0750); err != nil {
		t.Fatalf("failed to create thrumB: %v", err)
	}
	if err := os.MkdirAll(thrumC, 0750); err != nil {
		t.Fatalf("failed to create thrumC: %v", err)
	}

	// A redirects to B, B redirects to C
	if err := os.WriteFile(filepath.Join(thrumA, "redirect"), []byte(thrumB), 0600); err != nil {
		t.Fatalf("failed to write redirect A->B: %v", err)
	}
	if err := os.WriteFile(filepath.Join(thrumB, "redirect"), []byte(thrumC), 0600); err != nil {
		t.Fatalf("failed to write redirect B->C: %v", err)
	}

	_, err := ResolveThrumDir(dirA)
	if err == nil {
		t.Fatal("expected error for redirect chain")
	}
	if !strings.Contains(err.Error(), "redirect chain") {
		t.Errorf("expected 'redirect chain' error, got: %v", err)
	}
}

func TestResolveThrumDir_SelfRedirect(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create thrum dir: %v", err)
	}

	// Redirect to self
	if err := os.WriteFile(filepath.Join(thrumDir, "redirect"), []byte(thrumDir), 0600); err != nil {
		t.Fatalf("failed to write self-redirect: %v", err)
	}

	_, err := ResolveThrumDir(dir)
	if err == nil {
		t.Fatal("expected error for self-redirect")
	}
	if !strings.Contains(err.Error(), "redirect chain") {
		t.Errorf("expected 'redirect chain' error, got: %v", err)
	}
}

// --- FindThrumRoot tests ---

func TestFindThrumRoot_InRootDir(t *testing.T) {
	// .thrum/ is in the starting directory itself
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, ".thrum"), 0750); err != nil {
		t.Fatal(err)
	}

	got, err := FindThrumRoot(tmpDir)
	if err != nil {
		t.Fatalf("FindThrumRoot failed: %v", err)
	}
	if got != tmpDir {
		t.Errorf("expected %s, got %s", tmpDir, got)
	}
}

func TestFindThrumRoot_InParentDir(t *testing.T) {
	// .thrum/ is in the parent, start from a subdirectory
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, ".thrum"), 0750); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(tmpDir, "src", "internal")
	if err := os.MkdirAll(subDir, 0750); err != nil {
		t.Fatal(err)
	}

	got, err := FindThrumRoot(subDir)
	if err != nil {
		t.Fatalf("FindThrumRoot failed: %v", err)
	}
	if got != tmpDir {
		t.Errorf("expected %s, got %s", tmpDir, got)
	}
}

func TestFindThrumRoot_DeeplyNested(t *testing.T) {
	// .thrum/ at root, start from deeply nested subdir
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, ".thrum"), 0750); err != nil {
		t.Fatal(err)
	}
	deepDir := filepath.Join(tmpDir, "a", "b", "c", "d", "e")
	if err := os.MkdirAll(deepDir, 0750); err != nil {
		t.Fatal(err)
	}

	got, err := FindThrumRoot(deepDir)
	if err != nil {
		t.Fatalf("FindThrumRoot failed: %v", err)
	}
	if got != tmpDir {
		t.Errorf("expected %s, got %s", tmpDir, got)
	}
}

func TestFindThrumRoot_NotFound(t *testing.T) {
	// No .thrum/ anywhere in the hierarchy
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "some", "path")
	if err := os.MkdirAll(subDir, 0750); err != nil {
		t.Fatal(err)
	}

	_, err := FindThrumRoot(subDir)
	if err == nil {
		t.Fatal("expected error when .thrum/ not found")
	}
	if !strings.Contains(err.Error(), "no .thrum/ directory found") {
		t.Errorf("expected 'no .thrum/ directory found' error, got: %v", err)
	}
}

func TestFindThrumRoot_ThrumFileNotDir(t *testing.T) {
	// .thrum exists but is a file, not a directory — should not match
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, ".thrum"), []byte("not a dir"), 0600); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(tmpDir, "child")
	if err := os.Mkdir(subDir, 0750); err != nil {
		t.Fatal(err)
	}

	_, err := FindThrumRoot(subDir)
	if err == nil {
		t.Fatal("expected error when .thrum is a file, not a directory")
	}
}

func TestFindThrumRoot_WithRedirect(t *testing.T) {
	// .thrum/ exists with a redirect file — should still be found
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.Mkdir(thrumDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "redirect"), []byte("/some/path"), 0600); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(tmpDir, "nested")
	if err := os.Mkdir(subDir, 0750); err != nil {
		t.Fatal(err)
	}

	got, err := FindThrumRoot(subDir)
	if err != nil {
		t.Fatalf("FindThrumRoot failed: %v", err)
	}
	if got != tmpDir {
		t.Errorf("expected %s, got %s", tmpDir, got)
	}
}
