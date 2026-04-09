package safecmd_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

// newGitRepoForTest initializes a git repo in t.TempDir with a known user.name
// and user.email set via `git config` (not `-c`), so GitConfig tests observe
// the real config values.
func newGitRepoForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec // test uses controlled args
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.name", "Alice Example")
	run("config", "user.email", "alice@example.com")
	return filepath.Clean(dir)
}

func TestGit_SuccessfulCommand(t *testing.T) {
	ctx := context.Background()
	out, err := safecmd.Git(ctx, ".", "--version")
	if err != nil {
		t.Fatalf("git --version failed: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected output from git --version")
	}
}

func TestGit_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := safecmd.Git(ctx, ".", "--version")
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}

func TestGit_TimeoutContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(1 * time.Millisecond)

	_, err := safecmd.Git(ctx, ".", "status")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestGit_InvalidDir(t *testing.T) {
	ctx := context.Background()
	_, err := safecmd.Git(ctx, "/nonexistent-path-that-does-not-exist", "status")
	if err == nil {
		t.Fatal("expected error for invalid directory")
	}
}

func TestGitLong_SuccessfulCommand(t *testing.T) {
	ctx := context.Background()
	out, err := safecmd.GitLong(ctx, ".", "--version")
	if err != nil {
		t.Fatalf("git --version failed: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected output from git --version")
	}
}

func TestGitLong_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := safecmd.GitLong(ctx, ".", "--version")
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}

func TestGitConfig_ReadsUserName(t *testing.T) {
	dir := newGitRepoForTest(t)
	ctx := context.Background()

	got, err := safecmd.GitConfig(ctx, dir, "user.name")
	if err != nil {
		t.Fatalf("GitConfig(user.name) failed: %v", err)
	}
	if got != "Alice Example" {
		t.Fatalf("GitConfig(user.name) = %q, want %q", got, "Alice Example")
	}
}

func TestGitConfig_ReadsUserEmail(t *testing.T) {
	dir := newGitRepoForTest(t)
	ctx := context.Background()

	got, err := safecmd.GitConfig(ctx, dir, "user.email")
	if err != nil {
		t.Fatalf("GitConfig(user.email) failed: %v", err)
	}
	if got != "alice@example.com" {
		t.Fatalf("GitConfig(user.email) = %q, want %q", got, "alice@example.com")
	}
}

func TestGitConfig_DoesNotReflectInjectedOverrides(t *testing.T) {
	// The critical property of GitConfig: it must return the REAL value
	// from the repo's config, not the thrum-injected override that Git/GitLong
	// would apply. We prove this by writing "Alice Example" and verifying
	// GitConfig sees that, not "Thrum".
	dir := newGitRepoForTest(t)
	ctx := context.Background()

	got, err := safecmd.GitConfig(ctx, dir, "user.name")
	if err != nil {
		t.Fatalf("GitConfig(user.name) failed: %v", err)
	}
	if got == "Thrum" {
		t.Fatalf("GitConfig returned injected thrum override %q; expected real value %q", got, "Alice Example")
	}
}

func TestGitConfig_MissingKeyReturnsEmpty(t *testing.T) {
	dir := newGitRepoForTest(t)
	ctx := context.Background()

	got, err := safecmd.GitConfig(ctx, dir, "nonexistent.key")
	if err != nil {
		t.Fatalf("GitConfig(nonexistent.key) returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("GitConfig(nonexistent.key) = %q, want %q", got, "")
	}
}

func TestGitConfig_InvalidDirReturnsError(t *testing.T) {
	ctx := context.Background()
	_, err := safecmd.GitConfig(ctx, "/nonexistent-path-that-does-not-exist", "user.name")
	if err == nil {
		t.Fatal("expected error for invalid directory")
	}
}

func TestGitConfig_CancelledContext(t *testing.T) {
	dir := newGitRepoForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := safecmd.GitConfig(ctx, dir, "user.name")
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}
