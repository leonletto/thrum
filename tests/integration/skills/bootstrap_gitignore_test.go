//go:build integration

// Package skills exercises the C-B1 thrum-init bootstrap steps end-to-end
// against the cli.Init entry point. Helper-level unit tests covering each
// AC bullet live in internal/cli/init_skills_test.go (fast, table-style);
// this file integrates the full Init path on a fresh repo to catch any
// wiring regression that wouldn't surface at the helper layer.
package skills

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
)

// newGitTempDir spins up a `git init`'d tempdir for cli.Init. cli.Init
// drives a sync-branch reconciliation that needs a real git repo on
// disk; the unadorned t.TempDir() would fail at git operations.
func newGitTempDir(t *testing.T) string {
	t.Helper()
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	t.Setenv("THRUM_AGENT_ID", "")
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil { //nolint:gosec // test fixture
		t.Fatalf("git init: %v %s", err, out)
	}
	// cli.Init's a-sync branch setup needs git user identity configured.
	for _, args := range [][]string{
		{"-C", dir, "config", "user.email", "test@example.com"},
		{"-C", dir, "config", "user.name", "Test"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil { //nolint:gosec // test fixture
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	// a-sync requires at least one commit on HEAD.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# fixture\n"), 0o600); err != nil {
		t.Fatalf("seed README: %v", err)
	}
	for _, args := range [][]string{
		{"-C", dir, "add", "README.md"},
		{"-C", dir, "commit", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil { //nolint:gosec // test fixture
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

// TestInitBootstrap_FreshRepo runs cli.Init() against a fresh git
// tempdir and asserts every C-B1 E8.3 AC item lands:
//   - .thrum/skills/.gitkeep exists
//   - .gitignore carries `.thrum/`, `!.thrum/skills/`, and the explicit
//     proposed-skills marker
//   - .thrum/config.json carries skills.pending_reminder_after = "48h"
func TestInitBootstrap_FreshRepo(t *testing.T) {
	repo := newGitTempDir(t)

	if err := cli.Init(cli.InitOptions{RepoPath: repo, Yes: true}); err != nil {
		t.Fatalf("cli.Init: %v", err)
	}

	// .gitkeep
	gk := filepath.Join(repo, ".thrum", "skills", ".gitkeep")
	if _, err := os.Stat(gk); err != nil {
		t.Errorf(".gitkeep missing: %v", err)
	}

	// .gitignore
	gi, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	body := string(gi)
	for _, want := range []string{".thrum/", "!.thrum/skills/", ".thrum/agents/*/proposed-skills/"} {
		if !strings.Contains(body, want) {
			t.Errorf(".gitignore missing %q\n--- contents ---\n%s", want, body)
		}
	}

	// Order: !.thrum/skills/ MUST come after .thrum/ blanket — otherwise
	// the negation has no effect.
	blanketIdx := strings.Index(body, "\n.thrum/")
	negationIdx := strings.Index(body, "\n!.thrum/skills/")
	if blanketIdx < 0 || negationIdx < 0 || negationIdx < blanketIdx {
		t.Errorf("negation must follow blanket in .gitignore; got blanket=%d negation=%d\n%s",
			blanketIdx, negationIdx, body)
	}

	// config.json
	cfg, err := config.LoadThrumConfig(filepath.Join(repo, ".thrum"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Skills.PendingReminderAfter != config.DefaultSkillsPendingReminderAfter {
		t.Errorf("skills.pending_reminder_after = %q, want %q",
			cfg.Skills.PendingReminderAfter, config.DefaultSkillsPendingReminderAfter)
	}
}

// TestInitBootstrap_Idempotent verifies that re-running cli.Init() on
// an already-bootstrapped repo (via --force) does not duplicate the
// skills .gitignore section or overwrite a user's pending_reminder_after.
func TestInitBootstrap_Idempotent(t *testing.T) {
	repo := newGitTempDir(t)

	if err := cli.Init(cli.InitOptions{RepoPath: repo, Yes: true}); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	firstGitignore, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))

	// Tweak skills config to a non-default value the second init must preserve.
	thrumDir := filepath.Join(repo, ".thrum")
	cfg, _ := config.LoadThrumConfig(thrumDir)
	cfg.Skills.PendingReminderAfter = "72h"
	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := cli.Init(cli.InitOptions{RepoPath: repo, Yes: true, Force: true}); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	secondGitignore, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))

	if string(firstGitignore) != string(secondGitignore) {
		t.Errorf(".gitignore changed across idempotent re-init\n--- first ---\n%s--- second ---\n%s",
			firstGitignore, secondGitignore)
	}

	cfg2, _ := config.LoadThrumConfig(thrumDir)
	if cfg2.Skills.PendingReminderAfter != "72h" {
		t.Errorf("user-set pending_reminder_after overwritten: got %q, want %q",
			cfg2.Skills.PendingReminderAfter, "72h")
	}
}
