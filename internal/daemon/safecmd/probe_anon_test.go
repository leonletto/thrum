package safecmd

import (
	"context"
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
