package config

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDetectCurrentWorktree_ReturnsAbsolutePath covers thrum-8bza.
// Prior to the fix, detectCurrentWorktree returned the basename so the
// Pass 1 comparison at loadIdentityFromDir matched legacy bare-name
// identity files. Post thrum-x6e8.2/nu16 identity files store absolute
// paths; this helper now returns absolute paths to match.
func TestDetectCurrentWorktree_ReturnsAbsolutePath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}

	identitiesDir := filepath.Join(repo, ".thrum", "identities")

	got := detectCurrentWorktree(identitiesDir)
	if got == "" {
		t.Fatalf("expected non-empty result for valid git repo")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
	// macOS may alias /var → /private/var, so compare via EvalSymlinks.
	gotResolved, _ := filepath.EvalSymlinks(got)
	wantResolved, _ := filepath.EvalSymlinks(repo)
	if filepath.Clean(gotResolved) != filepath.Clean(wantResolved) &&
		filepath.Clean(got) != filepath.Clean(repo) {
		t.Errorf("expected %q or %q, got %q", repo, wantResolved, got)
	}
}

func TestDetectCurrentWorktree_NotGitRepo_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	identitiesDir := filepath.Join(dir, ".thrum", "identities")
	if got := detectCurrentWorktree(identitiesDir); got != "" {
		t.Errorf("expected empty for non-git dir, got %q", got)
	}
}

// TestLoad_WorktreeFiltering_AbsolutePathWorktree verifies that Pass 1
// matches identity files whose Worktree field is an absolute path (the
// post thrum-x6e8.2 / nu16 shape). The existing
// TestLoad_WorktreeFiltering_SingleMatch covers the legacy bare-name
// shape; this test covers the path shape so both branches of the
// thrum-8bza fix are exercised.
func TestLoad_WorktreeFiltering_AbsolutePathWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	t.Setenv("THRUM_AGENT_ID", "")

	tmpDir := t.TempDir()
	if out, err := exec.Command("git", "-C", tmpDir, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	// macOS aliases /var → /private/var; `git rev-parse --show-toplevel`
	// returns the resolved path, so the identity file's Worktree field
	// (which reconcileDrift also writes via EvalSymlinks) must match.
	resolvedTmp, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	thrumDir := filepath.Join(tmpDir, ".thrum")
	// Match: worktree is an absolute path == current repo
	matching := &IdentityFile{
		Version:  1,
		RepoID:   "r_TEST123",
		Worktree: resolvedTmp, // absolute path (post-nu16 shape)
		Agent: AgentConfig{
			Kind: "agent", Name: "matching_agent", Role: "implementer", Module: "test",
		},
	}
	// No match: different absolute path
	other := &IdentityFile{
		Version:  1,
		RepoID:   "r_TEST123",
		Worktree: "/nonexistent/other/path",
		Agent: AgentConfig{
			Kind: "agent", Name: "other_agent", Role: "tester", Module: "test",
		},
	}
	if err := SaveIdentityFile(thrumDir, matching); err != nil {
		t.Fatalf("save matching: %v", err)
	}
	if err := SaveIdentityFile(thrumDir, other); err != nil {
		t.Fatalf("save other: %v", err)
	}

	cfg, err := LoadWithPath(tmpDir, "", "")
	if err != nil {
		t.Fatalf("LoadWithPath: %v", err)
	}
	if cfg.Agent.Name != "matching_agent" {
		t.Errorf("expected 'matching_agent', got %q", cfg.Agent.Name)
	}
}
