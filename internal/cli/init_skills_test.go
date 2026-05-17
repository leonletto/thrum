package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

// freshThrumDir returns a path that satisfies applySkillsBootstrap
// pre-requisites: <repo>/.thrum/ with a valid config.json. Tests for
// the helper-level behavior of applySkillsBootstrap should call this
// instead of going through full cli.Init.
func freshThrumDir(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	thrumDir := filepath.Join(repo, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir .thrum: %v", err)
	}
	cfg := &config.ThrumConfig{Daemon: config.DaemonConfig{LocalOnly: true}}
	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return repo
}

func TestApplySkillsBootstrap_CreatesGitkeep(t *testing.T) {
	t.Parallel()

	repo := freshThrumDir(t)
	if err := applySkillsBootstrap(InitOptions{RepoPath: repo, Yes: true}); err != nil {
		t.Fatalf("applySkillsBootstrap: %v", err)
	}
	gk := filepath.Join(repo, ".thrum", "skills", ".gitkeep")
	if _, err := os.Stat(gk); err != nil {
		t.Fatalf(".gitkeep not created at %s: %v", gk, err)
	}
}

func TestApplySkillsBootstrap_GitignoreFreshRepo(t *testing.T) {
	t.Parallel()

	repo := freshThrumDir(t)
	if err := applySkillsBootstrap(InitOptions{RepoPath: repo, Yes: true}); err != nil {
		t.Fatalf("applySkillsBootstrap: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	got := string(data)
	for _, want := range []string{"!.thrum/skills/", ".thrum/agents/*/proposed-skills/"} {
		if !strings.Contains(got, want) {
			t.Errorf("gitignore missing %q\n--- contents ---\n%s", want, got)
		}
	}
}

func TestApplySkillsBootstrap_GitignoreYesFlag(t *testing.T) {
	t.Parallel()

	repo := freshThrumDir(t)
	// Pre-write a .gitignore with the v0.10.x blanket but no negation —
	// the upgrade case. Yes=true should auto-apply without a prompt.
	gi := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(gi, []byte(".thrum/\n.thrum.*.json\n"), 0o600); err != nil {
		t.Fatalf("seed gitignore: %v", err)
	}

	if err := applySkillsBootstrap(InitOptions{RepoPath: repo, Yes: true}); err != nil {
		t.Fatalf("applySkillsBootstrap: %v", err)
	}

	data, _ := os.ReadFile(gi)
	got := string(data)
	if !strings.Contains(got, "!.thrum/skills/") {
		t.Errorf("Yes flag should auto-apply negation\n--- contents ---\n%s", got)
	}
}

func TestApplySkillsBootstrap_GitignoreInteractive(t *testing.T) {
	t.Parallel()

	repo := freshThrumDir(t)
	gi := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(gi, []byte(".thrum/\n"), 0o600); err != nil {
		t.Fatalf("seed gitignore: %v", err)
	}

	// User says yes via the fake prompter.
	prompter := &fakeConfirmPrompter{response: true}
	if err := applySkillsBootstrap(InitOptions{RepoPath: repo, Prompter: prompter}); err != nil {
		t.Fatalf("applySkillsBootstrap: %v", err)
	}
	if prompter.calls != 1 {
		t.Errorf("expected 1 prompt, got %d", prompter.calls)
	}
	data, _ := os.ReadFile(gi)
	if !strings.Contains(string(data), "!.thrum/skills/") {
		t.Errorf("user-said-yes path should add negation; got:\n%s", string(data))
	}

	// User says no — negation should NOT be added.
	repo2 := freshThrumDir(t)
	gi2 := filepath.Join(repo2, ".gitignore")
	if err := os.WriteFile(gi2, []byte(".thrum/\n"), 0o600); err != nil {
		t.Fatalf("seed gitignore: %v", err)
	}
	prompter2 := &fakeConfirmPrompter{response: false}
	if err := applySkillsBootstrap(InitOptions{RepoPath: repo2, Prompter: prompter2}); err != nil {
		t.Fatalf("applySkillsBootstrap: %v", err)
	}
	data2, _ := os.ReadFile(gi2)
	if strings.Contains(string(data2), "!.thrum/skills/") {
		t.Errorf("user-said-no path should NOT add negation; got:\n%s", string(data2))
	}
}

func TestApplySkillsBootstrap_GitignoreIdempotent(t *testing.T) {
	t.Parallel()

	repo := freshThrumDir(t)
	opts := InitOptions{RepoPath: repo, Yes: true}
	if err := applySkillsBootstrap(opts); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	gi := filepath.Join(repo, ".gitignore")
	first, _ := os.ReadFile(gi)

	if err := applySkillsBootstrap(opts); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	second, _ := os.ReadFile(gi)

	if string(first) != string(second) {
		t.Errorf("gitignore changed on repeated apply\n--- first ---\n%s--- second ---\n%s", first, second)
	}
}

func TestApplySkillsBootstrap_ConfigSkillsKeyInserted(t *testing.T) {
	t.Parallel()

	repo := freshThrumDir(t)
	if err := applySkillsBootstrap(InitOptions{RepoPath: repo, Yes: true}); err != nil {
		t.Fatalf("applySkillsBootstrap: %v", err)
	}

	cfg, err := config.LoadThrumConfig(filepath.Join(repo, ".thrum"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Skills.PendingReminderAfter != config.DefaultSkillsPendingReminderAfter {
		t.Errorf("PendingReminderAfter = %q, want %q",
			cfg.Skills.PendingReminderAfter, config.DefaultSkillsPendingReminderAfter)
	}
}

func TestApplySkillsBootstrap_ConfigSkillsKeyPreserved(t *testing.T) {
	t.Parallel()

	repo := freshThrumDir(t)
	thrumDir := filepath.Join(repo, ".thrum")
	cfg := &config.ThrumConfig{
		Daemon: config.DaemonConfig{LocalOnly: true},
		Skills: config.SkillsConfig{PendingReminderAfter: "72h"},
	}
	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if err := applySkillsBootstrap(InitOptions{RepoPath: repo, Yes: true}); err != nil {
		t.Fatalf("applySkillsBootstrap: %v", err)
	}

	loaded, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.Skills.PendingReminderAfter != "72h" {
		t.Errorf("PendingReminderAfter overwritten: got %q, want %q",
			loaded.Skills.PendingReminderAfter, "72h")
	}
}

func TestApplySkillsBootstrap_FineGrainedIgnorePreserved(t *testing.T) {
	t.Parallel()

	repo := freshThrumDir(t)
	gi := filepath.Join(repo, ".gitignore")
	original := strings.Join([]string{
		"# user content",
		".env",
		"node_modules/",
		"",
		"# Thrum runtime files",
		".thrum/db.sqlite",
		".thrum/sockets/",
		".thrum/identities/",
		".thrum/state/",
		"",
	}, "\n")
	if err := os.WriteFile(gi, []byte(original), 0o600); err != nil {
		t.Fatalf("seed gitignore: %v", err)
	}

	if err := applySkillsBootstrap(InitOptions{RepoPath: repo, Yes: true}); err != nil {
		t.Fatalf("applySkillsBootstrap: %v", err)
	}

	data, _ := os.ReadFile(gi)
	got := string(data)
	// Every original ignore line must still be present.
	for _, want := range []string{
		".env",
		"node_modules/",
		".thrum/db.sqlite",
		".thrum/sockets/",
		".thrum/identities/",
		".thrum/state/",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fine-grained entry lost: %q", want)
		}
	}
	if !strings.Contains(got, "!.thrum/skills/") {
		t.Errorf("negation not added; got:\n%s", got)
	}
}

// fakeConfirmPrompter records calls to Confirm and returns the
// configured response. String/Choice are not used by this test and
// panic if invoked so accidental misuse surfaces loudly.
type fakeConfirmPrompter struct {
	response bool
	calls    int
}

func (p *fakeConfirmPrompter) Confirm(_ PromptID, _ string, _ bool) (bool, error) {
	p.calls++
	return p.response, nil
}

func (p *fakeConfirmPrompter) String(_ PromptID, _, _ string) (string, error) {
	panic("fakeConfirmPrompter.String should not be called by skills-bootstrap tests")
}

func (p *fakeConfirmPrompter) Choice(_ PromptID, _ string, _ []string, _ int) (int, error) {
	panic("fakeConfirmPrompter.Choice should not be called by skills-bootstrap tests")
}

// Sanity check that the SkillsConfig field serializes as a top-level
// "skills" key with the expected sub-keys. Catches accidental rename
// drift since downstream consumers (cmd/thrum/skill.go, daemon config
// reload) rely on the on-disk shape.
func TestSkillsConfig_JSONShape(t *testing.T) {
	t.Parallel()

	cfg := &config.ThrumConfig{
		Skills: config.SkillsConfig{PendingReminderAfter: "48h"},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe struct {
		Skills struct {
			PendingReminderAfter string `json:"pending_reminder_after"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if probe.Skills.PendingReminderAfter != "48h" {
		t.Errorf("PendingReminderAfter shape drift: got %q, want %q",
			probe.Skills.PendingReminderAfter, "48h")
	}
}
