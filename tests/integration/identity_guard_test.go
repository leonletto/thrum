//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/identity/guard"
)

// newGitTempDir returns a tmp dir with `git init` already run. Any test
// that exercises a guard bootstrap path (G2, init, quickstart) must use
// this instead of t.TempDir() — the unadorned tmpdir fails G2's
// non-git-bootstrap check.
func newGitTempDir(t *testing.T) string {
	t.Helper()
	// Clear ambient agent-session env so identity loaders find the
	// test fixture (not the host agent's identity file). These tests
	// typically run under an agent whose THRUM_NAME would otherwise
	// redirect config.LoadIdentityWithPath away from the tempdir.
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	t.Setenv("THRUM_AGENT_ID", "")

	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil { //nolint:gosec // test fixture
		t.Fatalf("git init: %v %s", err, out)
	}
	return dir
}

// seedIdentity writes a minimal identity file with the given fields
// into <repoDir>/.thrum/identities/<name>.json. Returns the absolute
// path to the written file.
func seedIdentity(t *testing.T, repoDir, name string, fields config.IdentityFile) string {
	t.Helper()
	identitiesDir := filepath.Join(repoDir, ".thrum", "identities")
	if err := os.MkdirAll(identitiesDir, 0o750); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}
	fields.Agent.Name = name
	data, _ := json.Marshal(fields)
	path := filepath.Join(identitiesDir, name+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	return path
}

// deadPID spawns a sleep subprocess, records its PID, kills it, and
// returns the now-dead PID. Test fixtures that need a definitely-dead
// PID should call this instead of hardcoding (e.g. 999999, which can
// coincidentally map to a live process on busy systems).
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "60") //nolint:gosec // test fixture
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	// Give the kernel a moment to reap; the caller will observe
	// process.IsRunning(pid) == false on all modern unixes.
	time.Sleep(20 * time.Millisecond)
	return pid
}

// ─── 6.1 ─── Cross-worktree PID mismatch ──────────────────────────────
// Agent A primes in worktree /A. Agent A cd's into worktree /B (which
// hosts a different agent's identity). Running a guard check from /B
// must hard-error in strict mode (spec §Rule #4‴ step 3.4), emit slog
// in warn mode, and be a no-op in off mode.

func TestIdentityGuard_CrossWorktreeMismatch(t *testing.T) {
	repoB := newGitTempDir(t)

	// Seed an identity in worktree B belonging to a different agent
	// whose AgentPID is the (live) current test process. When guard.Check
	// runs from this worktree, the caller's ancestor chain will not
	// contain AgentPID of the identity (the chain head is the test
	// binary which is NOT the "impl_b" agent per Rule #4‴) IF we
	// alias the chain properly — the cleanest way to force mismatch
	// is to seed an identity whose AgentPID is foreign.
	foreignPID := deadPID(t)
	seedIdentity(t, repoB, "impl_b", config.IdentityFile{
		Agent:    config.AgentConfig{Kind: "agent", Role: "impl", Module: "b"},
		Worktree: repoB,
		AgentPID: foreignPID, // different process — not us
	})

	// Matrix: strict → error; warn → nil + slog; off → nil + no slog.
	cases := []struct {
		mode        guard.Mode
		wantErr     bool
		wantSlog    bool
		description string
	}{
		{guard.ModeStrict, true, true, "strict denies + emits"},
		{guard.ModeWarn, false, true, "warn allows + emits"},
		{guard.ModeOff, false, false, "off silently passes"},
	}

	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			buf := &bytes.Buffer{}
			logger := slog.New(slog.NewJSONHandler(buf, nil))
			cfg := guard.DefaultConfig()
			cfg.CrossWorktree = tc.mode
			cfg.DeadPIDAutoReclaim = guard.ModeOff // avoid reclaim branch

			err := guard.Check(context.Background(), repoB, cfg, logger)
			if tc.wantErr && err == nil {
				t.Errorf("%s: want error, got nil", tc.description)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("%s: unexpected err: %v", tc.description, err)
			}
			if tc.wantErr {
				var gErr *guard.Error
				if !errors.As(err, &gErr) || gErr.Guard != "cross_worktree" {
					t.Errorf("%s: want *guard.Error(cross_worktree), got %v", tc.description, err)
				}
			}
			if tc.wantSlog && !strings.Contains(buf.String(), "identity_guard_fire") {
				t.Errorf("%s: want slog event, got %q", tc.description, buf.String())
			}
			if !tc.wantSlog && strings.Contains(buf.String(), "identity_guard_fire") {
				t.Errorf("%s: want no slog, got %q", tc.description, buf.String())
			}
		})
	}
}
