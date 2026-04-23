//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/identity/guard"
	ttmux "github.com/leonletto/thrum/internal/tmux"
)

// TestTmuxLaunch_IdentityInvariants_DeadPIDClearedAndSessionWritten is
// the L2 integration test for thrum-nu16 (x6e8.6 + x6e8.2). It drives
// real tmux + a real TmuxHandler through the launch path and asserts
// that:
//
//  1. Pre-launch, an identity file with a dead stored agent_pid
//     simulates the x6e8.6 state (tmux-create inline-quickstart
//     subshell PID that exited).
//  2. The preamble (clearStalePIDForLaunch) nulls the dead PID.
//  3. writeTmuxToIdentity's Pass 0 succeeds (G4 skips subjectPID=0).
//  4. Post-launch, tmux_session is populated with target "<sess>:0.0"
//     and agent_pid is 0 (awaiting first /thrum:prime).
//  5. Worktree is an absolute path (no x6e8.2 regression).
func TestTmuxLaunch_IdentityInvariants_DeadPIDClearedAndSessionWritten(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	// Clear ambient host env that would redirect identity loaders.
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	t.Setenv("THRUM_AGENT_ID", "")

	cwd := t.TempDir() // scratch worktree (abs path by construction)
	sessionName := "thrum-nu16-launch-test"

	// Cleanup any stale session from a prior run + on exit.
	_ = ttmux.KillSession(sessionName)
	t.Cleanup(func() { _ = ttmux.KillSession(sessionName) })

	// Pre-populate an identity file in the scratch worktree with a
	// dead stored PID — simulates the state the tmux-create inline
	// quickstart leaves behind when run in a pane subshell that
	// subsequently exits.
	deadPID := 2147483646 // guaranteed-dead
	idDir := filepath.Join(cwd, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}
	idPath := filepath.Join(idDir, "impl_test.json")
	preIdentity := config.IdentityFile{
		Version: 4,
		RepoID:  "test-repo",
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    "impl_test",
			Role:    "implementer",
			Module:  "testing",
			Display: "impl_test",
		},
		Worktree: cwd,     // absolute — x6e8.2 shape
		AgentPID: deadPID, // x6e8.6 shape
	}
	writeJSON(t, idPath, preIdentity)

	// Create the tmux session the handler will operate on.
	if err := ttmux.CreateSession(sessionName, cwd); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Build a TmuxHandler and register the session's cwd (HandleCreate
	// normally populates this map; we bypass HandleCreate because it
	// does much more than the launch invariant needs and would require
	// a full daemon bootstrap).
	handler := rpc.NewTmuxHandler(t.TempDir(), nil)
	rpc.SetSessionCwdForTest(handler, sessionName, cwd)

	// Invoke launch. Runtime "claude" matches the default; SendKeys
	// will attempt to type "claude" into the pane, which is fine —
	// the pane runs bash so it'll fail the command but the identity
	// write completes before any launch-cmd failure would matter.
	launchReq, _ := json.Marshal(map[string]any{"name": sessionName, "runtime": "claude"})
	if _, err := handler.HandleLaunch(context.Background(), launchReq); err != nil {
		t.Fatalf("HandleLaunch: %v", err)
	}

	// HandleLaunch writes synchronously (preamble → writeTmuxToIdentity
	// → regression guard, all on the calling goroutine); assert
	// immediately after return.

	// Assert post-launch state.
	postIdentity := readJSON[config.IdentityFile](t, idPath)

	if postIdentity.AgentPID != 0 {
		t.Errorf("expected AgentPID cleared to 0 post-launch, got %d", postIdentity.AgentPID)
	}
	if postIdentity.TmuxSession == "" {
		t.Errorf("expected TmuxSession populated post-launch, got empty (x6e8.6 regression)")
	}
	if !strings.HasPrefix(postIdentity.TmuxSession, sessionName) {
		t.Errorf("expected TmuxSession to start with %q, got %q", sessionName, postIdentity.TmuxSession)
	}
	if !filepath.IsAbs(postIdentity.Worktree) {
		t.Errorf("expected Worktree to stay absolute, got %q (x6e8.2 regression)", postIdentity.Worktree)
	}
	if postIdentity.Worktree != cwd {
		t.Errorf("expected Worktree=%q, got %q", cwd, postIdentity.Worktree)
	}
}

// TestTmuxLaunchPrime_PIDReclaim mirrors the prime-reclaim invariant
// from spec Part 1D: after launch, a call to guard.WritePID with the
// live runtime PID (as prime does at cmd/thrum/main.go:4060-4064)
// writes the PID into the identity file, completing the handoff.
func TestTmuxLaunchPrime_PIDReclaim(t *testing.T) {
	cwd := t.TempDir()
	idDir := filepath.Join(cwd, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	idPath := filepath.Join(idDir, "impl_test.json")
	writeJSON(t, idPath, config.IdentityFile{
		Version: 4,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "impl_test", Role: "implementer", Module: "testing",
		},
		Worktree: cwd,
		AgentPID: 0, // post-launch state
	})

	// Simulate prime's WritePID call.
	livePID := os.Getpid()
	if err := guard.WritePID(idPath, livePID); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	got := readJSON[config.IdentityFile](t, idPath)
	if got.AgentPID != livePID {
		t.Errorf("expected AgentPID=%d after prime reclaim, got %d", livePID, got.AgentPID)
	}
	if !filepath.IsAbs(got.Worktree) {
		t.Errorf("expected Worktree absolute, got %q", got.Worktree)
	}
}

// --- helpers ---

func writeJSON[T any](t *testing.T, path string, v T) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return v
}
