//go:build integration

package integration

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
	ttmux "github.com/leonletto/thrum/internal/tmux"
)

// TestTwoSessionsKeepDistinctIdentityEnv pins thrum-jj0a.1: when two
// tmux sessions are created back-to-back on the same shared tmux server
// with distinct identity envs, each session's initial pane shell must
// see its own THRUM_NAME. Without per-session `-e KEY=VALUE` overrides,
// the second session would inherit the first session's identity from
// the server's captured environ.
func TestTwoSessionsKeepDistinctIdentityEnv(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	const (
		sessA  = "thrum-jj0a-sess-a"
		sessB  = "thrum-jj0a-sess-b"
		nameA  = "agent_alpha"
		nameB  = "agent_bravo"
	)

	// Cleanup, in case a prior run left them around.
	_ = ttmux.KillSession(sessA)
	_ = ttmux.KillSession(sessB)

	if err := ttmux.CreateSessionWithEnv(sessA, t.TempDir(), map[string]string{
		"THRUM_NAME": nameA,
	}); err != nil {
		t.Fatalf("CreateSessionWithEnv A: %v", err)
	}
	defer func() { _ = ttmux.KillSession(sessA) }()

	if err := ttmux.CreateSessionWithEnv(sessB, t.TempDir(), map[string]string{
		"THRUM_NAME": nameB,
	}); err != nil {
		t.Fatalf("CreateSessionWithEnv B: %v", err)
	}
	defer func() { _ = ttmux.KillSession(sessB) }()

	for _, tc := range []struct {
		session string
		want    string
	}{
		{sessA, nameA},
		{sessB, nameB},
	} {
		out, err := safecmd.Tmux(context.Background(), "show-environment", "-t", tc.session, "THRUM_NAME")
		if err != nil {
			t.Fatalf("show-environment -t %s: %v", tc.session, err)
		}
		got := strings.TrimSpace(string(out))
		// tmux returns "THRUM_NAME=value" or "-THRUM_NAME" (if unset).
		want := "THRUM_NAME=" + tc.want
		if got != want {
			t.Errorf("session %s: show-environment returned %q, want %q",
				tc.session, got, want)
		}
	}
}
