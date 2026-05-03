package cli

import (
	"bytes"
	"io"
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
