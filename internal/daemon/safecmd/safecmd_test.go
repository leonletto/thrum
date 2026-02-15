package safecmd_test

import (
	"context"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

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
		t.Fatal("expected error from cancelled context")
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
		t.Fatal("expected error from cancelled context")
	}
}
