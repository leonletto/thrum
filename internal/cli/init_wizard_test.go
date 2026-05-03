package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestWizard_StepRoleTemplates_EnhancedWritesAllThree(t *testing.T) {
	repo := setupTestRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".thrum"), 0o750); err != nil {
		t.Fatal(err)
	}
	fp := &FakePrompter{Choices: map[PromptID]int{PromptRoleTemplates: 0}}
	cfg := &WizardConfig{RepoPath: repo, Prompter: fp}
	if err := stepRoleTemplates(cfg); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"coordinator", "implementer", "orchestrator"} {
		path := filepath.Join(repo, ".thrum", "role_templates", role+".md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("role template missing: %s", path)
		}
	}
	impl, _ := os.ReadFile(filepath.Join(repo, ".thrum", "role_templates", "implementer.md"))
	if !strings.Contains(string(impl), "Filesystem Boundary") {
		t.Error("enhanced implementer template missing Filesystem Boundary section")
	}
}

func TestWizard_StepRoleTemplates_SkipWritesNothing(t *testing.T) {
	repo := setupTestRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".thrum"), 0o750); err != nil {
		t.Fatal(err)
	}
	fp := &FakePrompter{Choices: map[PromptID]int{PromptRoleTemplates: 2}}
	cfg := &WizardConfig{RepoPath: repo, Prompter: fp}
	if err := stepRoleTemplates(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".thrum", "role_templates")); !os.IsNotExist(err) {
		t.Error("role_templates dir should not exist for skip choice")
	}
}

func TestWizard_StepRoleTemplates_NonForcePromptsBeforeOverwrite(t *testing.T) {
	repo := setupTestRepo(t)
	destDir := filepath.Join(repo, ".thrum", "role_templates")
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(destDir, "coordinator.md")
	if err := os.WriteFile(existing, []byte("PRE-EXISTING"), 0o600); err != nil {
		t.Fatal(err)
	}
	fp := &FakePrompter{
		Choices:  map[PromptID]int{PromptRoleTemplates: 0},
		Confirms: map[PromptID]bool{PromptOverwriteRoleTemplate: false},
	}
	cfg := &WizardConfig{RepoPath: repo, Prompter: fp, Force: false}
	if err := stepRoleTemplates(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(existing)
	if string(data) != "PRE-EXISTING" {
		t.Errorf("file overwritten despite refused confirm; got %q", data)
	}
	for _, r := range []string{"implementer", "orchestrator"} {
		if _, err := os.Stat(filepath.Join(destDir, r+".md")); err != nil {
			t.Errorf("expected %s.md to be written: %v", r, err)
		}
	}
}

func TestWizard_StepDaemon_DecisionMatrix(t *testing.T) {
	cases := []struct {
		running, force bool
		want           daemonAction
	}{
		{false, false, daemonActionStart},
		{false, true, daemonActionStart},
		{true, false, daemonActionSkip},
		{true, true, daemonActionRestart},
	}
	for _, c := range cases {
		got := decideDaemonAction(c.running, c.force)
		if got != c.want {
			t.Errorf("running=%v force=%v: got %v, want %v", c.running, c.force, got, c.want)
		}
	}
}

func TestWizard_Rollback_RemovesThrumDirAndRestoresFiles(t *testing.T) {
	repo := setupTestRepo(t)
	gi := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(gi, []byte("orig\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &WizardConfig{RepoPath: repo}
	snapshotGitFiles(cfg)
	if !cfg.gitignoreExisted || string(cfg.gitignoreSnapshot) != "orig\n" {
		t.Fatalf("snapshot not captured: existed=%v data=%q", cfg.gitignoreExisted, cfg.gitignoreSnapshot)
	}
	// Simulate Init: scaffold .thrum/ + append to .gitignore.
	if err := os.MkdirAll(filepath.Join(repo, ".thrum"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gi, []byte("orig\nthrum/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := rollback(cfg, fmt.Errorf("simulated"))
	if err == nil {
		t.Error("rollback should return wrapped error")
	}
	if _, err := os.Stat(filepath.Join(repo, ".thrum")); !os.IsNotExist(err) {
		t.Errorf(".thrum should be removed; stat err=%v", err)
	}
	got, _ := os.ReadFile(gi)
	if string(got) != "orig\n" {
		t.Errorf(".gitignore not restored: got %q, want %q", got, "orig\n")
	}
}

func TestWizard_Rollback_RemovesGitignoreWhenItDidntExist(t *testing.T) {
	repo := setupTestRepo(t)
	gi := filepath.Join(repo, ".gitignore")
	// Ensure no .gitignore — setupTestRepo doesn't create one.
	_ = os.Remove(gi)
	cfg := &WizardConfig{RepoPath: repo}
	snapshotGitFiles(cfg)
	if cfg.gitignoreExisted {
		t.Fatal("snapshot should record absent gitignore")
	}
	if err := os.WriteFile(gi, []byte("thrum/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".thrum"), 0o750); err != nil {
		t.Fatal(err)
	}
	_ = rollback(cfg, fmt.Errorf("x"))
	if _, err := os.Stat(gi); !os.IsNotExist(err) {
		t.Error(".gitignore should be removed because it didn't exist before Init")
	}
}

// blockingReader hangs forever on Read so a ScannerPrompter inside the
// subprocess never returns from a prompt — the wizard sits at Step 3 with
// .thrum/ already scaffolded, and SIGINT from the parent triggers cleanup.
type blockingReader struct{}

func (blockingReader) Read(_ []byte) (int, error) { select {} }

func TestWizard_SIGINT_RunsCleanup(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; SIGINT test relies on tmuxGate passing")
	}
	if os.Getenv("TEST_HELPER_RUN_WIZARD") == "1" {
		repo := os.Getenv("TEST_HELPER_REPO")
		cfg := &WizardConfig{
			RepoPath: repo,
			Prompter: NewScannerPrompter(blockingReader{}, io.Discard),
		}
		_ = RunWizard(cfg)
		return
	}
	repo := setupTestRepo(t)
	cmd := exec.Command(os.Args[0], "-test.run=TestWizard_SIGINT_RunsCleanup", "-test.timeout=30s")
	cmd.Env = append(os.Environ(),
		"TEST_HELPER_RUN_WIZARD=1",
		"TEST_HELPER_REPO="+repo,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Give Init() time to scaffold .thrum/ and reach the first prompt.
	time.Sleep(500 * time.Millisecond)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if _, err := os.Stat(filepath.Join(repo, ".thrum")); !os.IsNotExist(err) {
		t.Errorf(".thrum should be removed after SIGINT; stat err=%v", err)
	}
}
