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

// liveChildPID spawns a short-lived child process parented to the
// test binary and returns its PID. Used by the subagent test: the
// child inherits the test's ancestor chain, so walking up from the
// child will hit the test process and then any AI runtime that
// spawned the test. Caller must ensure the child is still alive when
// the PID is read — use a sleep long enough to cover the assertion.
func liveChildPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30") //nolint:gosec // test fixture
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn child: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

// liveSleeperPID spawns a long-running sleep subprocess and registers
// cleanup. Returns its PID. The process stays alive for the test's
// lifetime so guard.G1b's IsPIDAlive probe returns true.
func liveSleeperPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30") //nolint:gosec // test fixture
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
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

// ─── 6.2 ─── Same-dir multi-agent ─────────────────────────────────────
// Two agents in the same worktree with distinct names + PIDs coexist.
// A second prime of an already-held name (by a live foreign PID) hits
// G1b (quickstart_name_collision) in strict mode; warn allows; off
// silently passes.

func TestIdentityGuard_SameDirMultiAgent(t *testing.T) {
	repo := newGitTempDir(t)
	identitiesDir := filepath.Join(repo, ".thrum", "identities")

	foreignLivePID := liveSleeperPID(t)
	seedIdentity(t, repo, "impl_a", config.IdentityFile{
		Agent:    config.AgentConfig{Kind: "agent", Role: "impl"},
		Worktree: repo,
		AgentPID: foreignLivePID,
	})

	t.Run("distinct_name_no_collision", func(t *testing.T) {
		// impl_b is a new name — no existing file, no collision.
		cfg := guard.DefaultConfig()
		err := guard.G1b(&guard.QuickstartContext{
			Mode:          cfg.QuickstartNameCollision,
			IdentitiesDir: identitiesDir,
			Chain:         []int{os.Getpid()}, // caller is self, NOT foreignLivePID
			RequestedName: "impl_b",
			IsPIDAlive:    func(pid int) bool { return pid == foreignLivePID },
		})
		if err != nil {
			t.Errorf("distinct name should coexist, got %v", err)
		}
	})

	t.Run("same_name_live_foreign_pid_matrix", func(t *testing.T) {
		cases := []struct {
			mode    guard.Mode
			wantErr bool
		}{
			{guard.ModeStrict, true},
			{guard.ModeWarn, false},
			{guard.ModeOff, false},
		}
		for _, tc := range cases {
			t.Run(string(tc.mode), func(t *testing.T) {
				err := guard.G1b(&guard.QuickstartContext{
					Mode:          tc.mode,
					IdentitiesDir: identitiesDir,
					Chain:         []int{os.Getpid()}, // not the foreign PID
					RequestedName: "impl_a",           // collides with seeded file
					IsPIDAlive:    func(pid int) bool { return pid == foreignLivePID },
				})
				if tc.wantErr && err == nil {
					t.Errorf("%s: want G1b error, got nil", tc.mode)
				}
				if !tc.wantErr && err != nil {
					t.Errorf("%s: unexpected err: %v", tc.mode, err)
				}
				if tc.wantErr {
					var gErr *guard.Error
					if !errors.As(err, &gErr) || gErr.Guard != "quickstart_name_collision" {
						t.Errorf("%s: want *guard.Error(quickstart_name_collision), got %v", tc.mode, err)
					}
				}
			})
		}
	})

	t.Run("same_name_force_renames_to_deleted", func(t *testing.T) {
		// Reseed a fresh fixture so the .deleted rename doesn't leak
		// across subtests.
		repo := newGitTempDir(t)
		seedIdentity(t, repo, "impl_force", config.IdentityFile{
			Agent:    config.AgentConfig{Kind: "agent", Role: "impl"},
			Worktree: repo,
			AgentPID: foreignLivePID,
		})
		identitiesDir := filepath.Join(repo, ".thrum", "identities")

		err := guard.G1b(&guard.QuickstartContext{
			Mode:          guard.ModeStrict,
			IdentitiesDir: identitiesDir,
			Chain:         []int{os.Getpid()},
			RequestedName: "impl_force",
			Force:         true,
			IsPIDAlive:    func(pid int) bool { return pid == foreignLivePID },
		})
		if err != nil {
			t.Fatalf("force should bypass + rename, got %v", err)
		}
		// Original file renamed to .deleted sidekick.
		if _, statErr := os.Stat(filepath.Join(identitiesDir, "impl_force.json.deleted")); statErr != nil {
			t.Errorf(".deleted sidekick missing: %v", statErr)
		}
	})
}

// ─── 6.3 ─── Subagent ────────────────────────────────────────────────
// A child process forked from a registered agent's runtime inherits
// that agent's ancestor chain. DaemonResolve's chain-walk must
// authenticate the child as the parent agent (spec §Rule #4‴
// ancestor-chain clause). Verifies the Epic 6 runtime-ancestor
// precondition doesn't regress the happy path.

func TestIdentityGuard_Subagent_ChainAuthenticates(t *testing.T) {
	repo := newGitTempDir(t)
	identitiesDir := filepath.Join(repo, ".thrum", "identities")
	// NOTE: this test requires the go test process to itself run under
	// an AI-runtime ancestor (claude, codex, etc.) — resolveByChain's
	// step-2 precondition rejects callers without one. That holds when
	// the test is run inside a Claude session; skip otherwise.
	if rt, _, _ := guard.ClosestRuntimeAncestor(context.Background(), os.Getpid()); rt == 0 {
		t.Skip("test process has no AI-runtime ancestor; chain walk precondition would reject")
	}

	// Seed parent agent's identity: its AgentPID is the test process
	// itself. The child forked from exec.Command will have the test
	// process as a chain ancestor, so ChainContains(childChain, os.Getpid())
	// should be true.
	seedIdentity(t, repo, "impl_parent", config.IdentityFile{
		Agent:    config.AgentConfig{Kind: "agent", Role: "impl"},
		Worktree: repo,
		AgentPID: os.Getpid(),
	})

	childPID := liveChildPID(t)

	cfg := guard.DefaultConfig()
	got, err := guard.DaemonResolve(context.Background(), cfg, guard.DaemonResolveRequest{
		PeercredRan:   true, // anonymous branch: CWD-based match missed
		ConnectingPID: childPID,
		IdentitiesDir: identitiesDir,
	}, nil)
	if err != nil {
		t.Fatalf("subagent chain walk should authenticate, got %v", err)
	}
	if got.AgentID != "impl_parent" {
		t.Errorf("AgentID = %q, want impl_parent", got.AgentID)
	}
}
