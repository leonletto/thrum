package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestRepoWithRemoteHEAD aliases the shared defaults_test helper so the
// wizard tests read naturally with the names used in the design plan.
func setupTestRepoWithRemoteHEAD(t *testing.T, remote, branch string) string {
	t.Helper()
	return setupGitRepoWithRemoteHEAD(t, remote, branch)
}

// setupTestRepo creates a minimal git repo on branch "main" with no remote.
// Wizard tests that don't need a remote-tracking default branch use this.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	return setupGitRepoNoRemote(t, "main")
}

func TestWizard_StepIdentity_UsesDefaults(t *testing.T) {
	repo := setupTestRepoWithRemoteHEAD(t, "origin", "main")
	fp := &FakePrompter{}
	cfg := &WizardConfig{
		RepoPath: repo,
		Prompter: fp,
	}
	id, err := stepIdentity(cfg)
	if err != nil {
		t.Fatal(err)
	}
	repoName := filepath.Base(repo)
	wantName := "coord_" + sanitize(repoName)
	if id.Name != wantName {
		t.Errorf("name = %q, want %q", id.Name, wantName)
	}
	if id.Role != "coordinator" {
		t.Errorf("role = %q, want coordinator", id.Role)
	}
	if id.Module != "main" {
		t.Errorf("module = %q, want main", id.Module)
	}
}

func TestWizard_StepIdentity_FlagsSkipPrompts(t *testing.T) {
	repo := setupTestRepoWithRemoteHEAD(t, "origin", "main")
	fp := &FakePrompter{Strings: map[PromptID]string{
		PromptAgentName: "WRONG", PromptRole: "WRONG", PromptModule: "WRONG",
	}}
	cfg := &WizardConfig{
		RepoPath: repo, Prompter: fp,
		NameFlag: "myname", RoleFlag: "implementer", ModuleFlag: "develop",
	}
	id, err := stepIdentity(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "myname" || id.Role != "implementer" || id.Module != "develop" {
		t.Errorf("got %+v, flags should have skipped prompts", id)
	}
}

func TestTmuxGate_PassesWhenTmuxFound(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed in test env")
	}
	if err := tmuxGate(io.Discard); err != nil {
		t.Errorf("tmuxGate() = %v, want nil", err)
	}
}

func TestTmuxGate_ReturnsInstallMessageWhenMissing(t *testing.T) {
	// Stub PATH so tmux is not findable.
	t.Setenv("PATH", "/nonexistent")
	var buf bytes.Buffer
	err := tmuxGate(&buf)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"tmux is required", "brew install tmux", "port install tmux", "apt install tmux"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q:\n%s", want, msg)
		}
	}
}

func TestWizard_StepWorktreesRoot_CreatesDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := setupTestRepo(t)
	cfg := &WizardConfig{RepoPath: repo, Prompter: &FakePrompter{}}
	got, err := stepWorktreesRoot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".thrum", "worktrees", filepath.Base(repo))
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("worktrees root not created: %v", err)
	}
}

func TestWizard_StepWorktreesRoot_RejectsRelative(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := setupTestRepo(t)
	fp := &FakePrompter{Strings: map[PromptID]string{PromptWorktreesRoot: "rel/path"}}
	cfg := &WizardConfig{RepoPath: repo, Prompter: fp}
	if _, err := stepWorktreesRoot(cfg); err == nil {
		t.Error("expected validation error for relative path")
	}
}

func TestWizard_StepWorktreesRoot_RejectsInsideRepo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := setupTestRepo(t)
	inside := filepath.Join(repo, "wt")
	fp := &FakePrompter{Strings: map[PromptID]string{PromptWorktreesRoot: inside}}
	cfg := &WizardConfig{RepoPath: repo, Prompter: fp}
	if _, err := stepWorktreesRoot(cfg); err == nil {
		t.Error("expected validation error for path inside repo")
	}
}

func TestWizard_StepWorktreesRoot_ExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := setupTestRepo(t)
	fp := &FakePrompter{Strings: map[PromptID]string{PromptWorktreesRoot: "~/custom/wt"}}
	cfg := &WizardConfig{RepoPath: repo, Prompter: fp}
	got, err := stepWorktreesRoot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "custom", "wt")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
