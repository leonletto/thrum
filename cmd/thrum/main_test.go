package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/worktree"
)

func TestPrintAgentSummaryField(t *testing.T) {
	s := &cli.AgentSummary{
		AgentID:     "bob",
		Role:        "impl",
		TmuxAlive:   true,
		PID:         9001,
		TmuxSession: "bob:0.0",
		Host:        "laptop.local",
	}
	cases := []struct {
		field, want string
	}{
		{"agent_id", "bob\n"},
		{"role", "impl\n"},
		{"tmux_alive", "true\n"},
		{"pid", "9001\n"},
		{"tmux_session", "bob:0.0\n"},
		{"host", "laptop.local\n"},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		if err := printAgentSummaryField(&buf, s, tc.field); err != nil {
			t.Fatalf("field %q: %v", tc.field, err)
		}
		if buf.String() != tc.want {
			t.Fatalf("field %q: got %q, want %q", tc.field, buf.String(), tc.want)
		}
	}

	var buf bytes.Buffer
	err := printAgentSummaryField(&buf, s, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("error message should mention 'unknown field': got %q", err.Error())
	}
}

func TestCronInstallInboxPoll_EmitsInstruction(t *testing.T) {
	var buf bytes.Buffer
	cmd := cronInstallInboxPollCmd()
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	out := buf.String()
	for _, needle := range []string{"CronCreate", "7,22,37,52", "thrum inbox --unread", "durable: false"} {
		if !strings.Contains(out, needle) {
			t.Errorf("output missing %q:\n%s", needle, out)
		}
	}
	// Sanity: no trailing daemon call paths — the output should be pure text.
	for _, forbidden := range []string{"panic", "error:", "daemon"} {
		if strings.Contains(strings.ToLower(out), forbidden) {
			t.Errorf("unexpected %q in output:\n%s", forbidden, out)
		}
	}
}

func TestInferWorktreeBasePath_DefaultsToThrumWorktrees(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := worktree.InferBasePath("/some/path/falcon-backend")
	want := filepath.Join(home, ".thrum", "worktrees", "falcon-backend")
	if got != want {
		t.Errorf("worktree.InferBasePath = %q, want %q", got, want)
	}
}
