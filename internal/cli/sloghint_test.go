package cli

// NOTE: file MUST be `package cli` (not `package cli_test`) — tests poke the
// pushedHints buffer directly in subsequent steps. Keeping it in-package also
// lets us exercise the unexported deriveHintCode.

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestSlogHintHandler_WarnBecomesHint(t *testing.T) {
	ResetPushedHintsForTest()
	h := NewSlogHintHandler()
	rec := slog.NewRecord(time.Time{}, slog.LevelWarn,
		"worktree.PaneTargetForIdentity refused: caller pane belongs to a different worktree", 0)
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	hints := DrainPushedHints()
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Severity != SeverityWarn {
		t.Errorf("severity=%q want warn", hints[0].Severity)
	}
	if hints[0].Code != "worktree.panetargetforidentity" {
		t.Errorf("code=%q want worktree.panetargetforidentity", hints[0].Code)
	}
	if !strings.Contains(hints[0].Message, "refused") {
		t.Errorf("message=%q missing 'refused'", hints[0].Message)
	}
}

func TestDrainPushedHints_ClearsBuffer(t *testing.T) {
	ResetPushedHintsForTest()
	h := NewSlogHintHandler()
	_ = h.Handle(context.Background(),
		slog.NewRecord(time.Time{}, slog.LevelWarn, "pkg.x: a", 0))
	first := DrainPushedHints()
	second := DrainPushedHints()
	if len(first) != 1 {
		t.Fatalf("first=%d want 1", len(first))
	}
	if len(second) != 0 {
		t.Errorf("second=%d want 0 after drain", len(second))
	}
}

func TestSlogHintHandler_InfoDropped(t *testing.T) {
	ResetPushedHintsForTest()
	h := NewSlogHintHandler()
	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "pkg.x: ok", 0)
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Errorf("Enabled(Info)=true, want false")
	}
	_ = h.Handle(context.Background(), rec)
	if len(DrainPushedHints()) != 0 {
		t.Errorf("info record accumulated; want dropped")
	}
}

func TestDeriveHintCode_DottedPrefix(t *testing.T) {
	cases := map[string]string{
		"worktree.PaneTargetForIdentity refused: x": "worktree.panetargetforidentity",
		"identity_guard_fire":                       "runtime.warn",
		"bridge.telegram: send failed":              "bridge.telegram",
		// Daemon-style bracketed prefix should be unwrapped so the code is
		// useful even if such records leak into the CLI bridge.
		"[telegram.msgmap] persistence write failed": "telegram.msgmap",
		"[queue] foo failed":                         "runtime.warn",
	}
	for in, want := range cases {
		if got := deriveHintCode(in); got != want {
			t.Errorf("deriveHintCode(%q)=%q want %q", in, got, want)
		}
	}
}
