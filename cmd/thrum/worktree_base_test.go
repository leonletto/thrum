package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newBaseTestRepo creates a temp git repo with an initial commit on
// main + one alt branch ("feature/alt"). Returns the repo path.
// Mirrors the pattern in internal/worktree/create_test.go:newTestRepo.
func newBaseTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runRepo := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	runRepo("git", "init")
	runRepo("git", "config", "user.email", "test@example.com")
	runRepo("git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("init\n"), 0600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runRepo("git", "add", "README.md")
	runRepo("git", "commit", "-m", "init")
	runRepo("git", "branch", "-M", "main")
	runRepo("git", "branch", "feature/alt")
	return repo
}

// TestResolveWorktreeBase_ExplicitFlagWins — thrum-pqcg: when --base
// is supplied, it must short-circuit cwd-HEAD detection entirely and
// be returned verbatim.
func TestResolveWorktreeBase_ExplicitFlagWins(t *testing.T) {
	repo := newBaseTestRepo(t)
	// Even though the repo is on main, --base override should win.
	got := resolveWorktreeBase(context.Background(), repo, "feature/alt")
	if got != "feature/alt" {
		t.Errorf("got %q, want \"feature/alt\" (explicit --base must win over cwd HEAD)", got)
	}
}

// TestResolveWorktreeBase_DefaultsToCwdHEAD — thrum-pqcg: when --base
// is empty AND the cwd is on a normal branch, the cwd HEAD's branch
// name is returned. This is the headline fix: pre-fix the CLI fell
// through to a silent "main" default regardless of cwd HEAD.
func TestResolveWorktreeBase_DefaultsToCwdHEAD(t *testing.T) {
	repo := newBaseTestRepo(t)
	// Switch the repo to feature/alt so cwd HEAD != main.
	if out, err := exec.Command("git", "-C", repo, "switch", "feature/alt").CombinedOutput(); err != nil {
		t.Fatalf("git switch feature/alt: %v\n%s", err, out)
	}
	got := resolveWorktreeBase(context.Background(), repo, "")
	if got != "feature/alt" {
		t.Errorf("got %q, want \"feature/alt\" (cwd HEAD must default to current branch per thrum-pqcg)", got)
	}
}

// TestResolveWorktreeBase_DetachedHEADFallsBackToMain — thrum-pqcg
// safety: on a detached HEAD, symbolic-ref fails; the helper warns
// and falls back to "main" so the command still works. Without an
// explicit --base, the operator may be surprised but the warn surfaces
// the fallback (vs pre-fix silent substitution).
func TestResolveWorktreeBase_DetachedHEADFallsBackToMain(t *testing.T) {
	repo := newBaseTestRepo(t)
	// Detach HEAD by checking out the commit directly.
	sha, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	shaStr := string(sha[:40])
	if out, err := exec.Command("git", "-C", repo, "checkout", "--detach", shaStr).CombinedOutput(); err != nil {
		t.Fatalf("git checkout --detach %s: %v\n%s", shaStr, err, out)
	}
	got := resolveWorktreeBase(context.Background(), repo, "")
	if got != "main" {
		t.Errorf("got %q, want \"main\" (detached HEAD must fall back to main per thrum-pqcg)", got)
	}
}

// TestResolveWorktreeBase_NonGitCwdFallsBackToMain — thrum-pqcg
// safety: when repoPath is not a git repo at all (e.g. a fresh
// tempdir), symbolic-ref errors with exit 128 and the helper falls
// back to "main".
func TestResolveWorktreeBase_NonGitCwdFallsBackToMain(t *testing.T) {
	nonGit := t.TempDir()
	got := resolveWorktreeBase(context.Background(), nonGit, "")
	if got != "main" {
		t.Errorf("got %q, want \"main\" (non-git cwd must fall back to main per thrum-pqcg)", got)
	}
}
