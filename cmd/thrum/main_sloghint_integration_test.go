package main

import (
	"log/slog"
	"os"
	"testing"

	"github.com/leonletto/thrum/internal/cli"
)

func TestInstallSlogBridge_RoutesWarnToPushedHints(t *testing.T) {
	cli.ResetPushedHintsForTest()
	orig := slog.Default()
	defer slog.SetDefault(orig)

	installSlogBridge(true /* jsonMode */, os.Stderr)
	slog.Warn("worktree.PaneTargetForIdentity refused: test path")

	hints := cli.DrainPushedHints()
	if len(hints) != 1 {
		t.Fatalf("got %d hints, want 1", len(hints))
	}
	if hints[0].Code != "worktree.panetargetforidentity" {
		t.Errorf("code=%q", hints[0].Code)
	}
}

func TestInstallSlogBridge_HumanModeStillWritesStderr(t *testing.T) {
	// In non-JSON mode we want slog to continue behaving normally — users
	// running `thrum ... 2> log.txt` still see the warnings.
	// Smoke test: installing in human mode should not route to the pushed
	// buffer (so the CLI falls back to the Go default text handler).
	cli.ResetPushedHintsForTest()
	orig := slog.Default()
	defer slog.SetDefault(orig)

	installSlogBridge(false /* jsonMode */, os.Stderr)
	slog.Warn("test message")

	if got := cli.DrainPushedHints(); len(got) != 0 {
		t.Errorf("human mode should not push hints, got %d", len(got))
	}
}
