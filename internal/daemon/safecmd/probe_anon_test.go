package safecmd

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// mustGit runs a git command in dir and fails the test on error. Shared by the
// anonymous-probe tests; the same definition is reused in
// internal/sync/branch_test.go (Task 9).
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func TestGitProbeAnonymous_LocalRepoReadsRefs(t *testing.T) {
	// A local file:// repo is anonymously readable → succeeds with refs.
	dir := t.TempDir()
	mustGit(t, dir, "init", "--bare")
	// seed one ref so ls-remote returns output
	wd := t.TempDir()
	mustGit(t, wd, "init")
	mustGit(t, wd, "commit", "--allow-empty", "-m", "x")
	mustGit(t, wd, "remote", "add", "origin", dir)
	mustGit(t, wd, "push", "origin", "HEAD:refs/heads/main")

	out, err := GitProbeAnonymous(context.Background(), "file://"+dir)
	if err != nil {
		t.Fatalf("expected success, got err: %v", err)
	}
	if !strings.Contains(string(out), "refs/heads/main") {
		t.Fatalf("expected ref in output, got: %s", out)
	}
}

// TestGitProbeAnonymous_IgnoresLocalInsteadOf proves the probe is isolated from
// the LOCAL .git/config of whatever repo the daemon's cwd sits in. A per-repo
// url.insteadOf that would rewrite the target URL to a dead host must NOT be
// applied — the probe runs from a neutral dir (cmd.Dir) with
// GIT_CEILING_DIRECTORIES, so it never discovers the surrounding repo.
func TestGitProbeAnonymous_IgnoresLocalInsteadOf(t *testing.T) {
	// Real, anonymously-readable target.
	target := t.TempDir()
	mustGit(t, target, "init", "--bare")
	seed := t.TempDir()
	mustGit(t, seed, "init")
	mustGit(t, seed, "commit", "--allow-empty", "-m", "x")
	mustGit(t, seed, "remote", "add", "origin", target)
	mustGit(t, seed, "push", "origin", "HEAD:refs/heads/main")
	targetURL := "file://" + target

	// A repo with a LOCAL insteadOf that would rewrite targetURL to a dead host.
	evil := t.TempDir()
	mustGit(t, evil, "init")
	mustGit(t, evil, "config", `url.https://nonexistent.invalid/.insteadOf`, targetURL)

	// Run the test process from inside the malicious repo so that, absent
	// isolation, git would pick up its local insteadOf.
	restore := chdir(t, evil)
	defer restore()

	out, err := GitProbeAnonymous(context.Background(), targetURL)
	if err != nil {
		t.Fatalf("local insteadOf leaked into the probe (cwd not isolated): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "refs/heads/main") {
		t.Fatalf("expected target refs, got: %s", out)
	}
}

// chdir changes the working directory to dir and returns a restore func. Used
// to simulate the daemon running inside a repo with a hostile local config.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	return func() { _ = os.Chdir(prev) }
}
