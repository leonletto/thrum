package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/runtime"
)

// fakeHomeDir overrides userHomeDirFunc to return a temp directory with no
// installed plugins. Call this at the start of tests that don't test plugin
// detection to avoid interference from the real ~/.claude/plugins/.
func fakeHomeDir(t *testing.T) {
	t.Helper()
	fakeHome := t.TempDir()
	original := userHomeDirFunc
	userHomeDirFunc = func() (string, error) { return fakeHome, nil }
	t.Cleanup(func() { userHomeDirFunc = original })
}

func TestInstallSkills_AgentWithConfigDir(t *testing.T) {
	fakeHomeDir(t)
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude"), 0750)

	opts := SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "claude",
		Force:    false,
	}
	result, err := InstallSkills(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.InstallPath != ".claude/skills/thrum" {
		t.Errorf("expected .claude/skills/thrum, got %q", result.InstallPath)
	}
	skillPath := filepath.Join(tmpDir, ".claude/skills/thrum/SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Errorf("SKILL.md not written: %v", err)
	}
}

func TestInstallSkills_AgentWithoutConfigDir(t *testing.T) {
	fakeHomeDir(t)
	tmpDir := t.TempDir()

	opts := SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "claude",
	}
	result, err := InstallSkills(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.InstallPath != ".agents/skills/thrum" {
		t.Errorf("expected .agents/skills/thrum, got %q", result.InstallPath)
	}
}

func TestInstallSkills_NoOverwriteWithoutForce(t *testing.T) {
	fakeHomeDir(t)
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, ".agents/skills/thrum")
	_ = os.MkdirAll(skillDir, 0750)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("existing"), 0600)

	opts := SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "claude",
		Force:    false,
	}
	_, err := InstallSkills(opts)
	if err == nil {
		t.Error("expected error when skill exists and force=false")
	}
}

func TestInstallSkills_OverwriteWithForce(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, ".agents/skills/thrum")
	_ = os.MkdirAll(skillDir, 0750)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("old"), 0600)

	opts := SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "claude",
		Force:    true,
	}
	result, err := InstallSkills(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(tmpDir, result.InstallPath, "SKILL.md"))
	if string(data) == "old" {
		t.Error("expected SKILL.md to be overwritten")
	}
}

func TestInstallSkills_WritesReferences(t *testing.T) {
	fakeHomeDir(t)
	tmpDir := t.TempDir()
	opts := SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "claude",
	}
	result, err := InstallSkills(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, ref := range []string{"CLI_REFERENCE.md", "MESSAGING.md", "LISTENER_PATTERN.md"} {
		path := filepath.Join(tmpDir, result.InstallPath, "references", ref)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("reference %s not written: %v", ref, err)
		}
	}
}

func TestInstallSkills_DryRun(t *testing.T) {
	fakeHomeDir(t)
	tmpDir := t.TempDir()
	opts := SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "claude",
		DryRun:   true,
	}
	result, err := InstallSkills(opts)
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if len(result.Files) == 0 {
		t.Error("expected file list in dry-run result")
	}
	skillPath := filepath.Join(tmpDir, ".agents/skills/thrum/SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		t.Error("dry-run should not write files")
	}
}

func TestInstallSkills_UnknownAgent(t *testing.T) {
	tmpDir := t.TempDir()
	opts := SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "nonexistent",
	}
	_, err := InstallSkills(opts)
	if err == nil {
		t.Error("expected error for unknown agent")
	}
}

func TestInstallSkills_CursorAgent(t *testing.T) {
	fakeHomeDir(t)
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".cursor"), 0750)

	opts := SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "cursor",
	}
	result, err := InstallSkills(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.InstallPath != ".cursor/skills/thrum" {
		t.Errorf("expected .cursor/skills/thrum, got %q", result.InstallPath)
	}
}

func TestInstallSkills_BlockedByLocalPlugin(t *testing.T) {
	fakeHomeDir(t)
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude"), 0750)
	// Create a local .claude-plugin/plugin.json with thrum name
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude-plugin"), 0750)
	_ = os.WriteFile(filepath.Join(tmpDir, ".claude-plugin/plugin.json"),
		[]byte(`{"name": "thrum", "version": "0.5.5"}`), 0600)

	opts := SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "claude",
	}
	_, err := InstallSkills(opts)
	if err == nil {
		t.Error("expected error when thrum plugin is installed locally")
	}
	if err != nil && !strings.Contains(err.Error(), "thrum plugin already installed") {
		t.Errorf("expected plugin-already-installed error, got: %v", err)
	}
}

func TestInstallSkills_LocalPluginBypassedWithForce(t *testing.T) {
	fakeHomeDir(t)
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude"), 0750)
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude-plugin"), 0750)
	_ = os.WriteFile(filepath.Join(tmpDir, ".claude-plugin/plugin.json"),
		[]byte(`{"name": "thrum"}`), 0600)

	opts := SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "claude",
		Force:    true,
	}
	result, err := InstallSkills(opts)
	if err != nil {
		t.Fatalf("expected --force to bypass plugin check, got: %v", err)
	}
	if result.InstallPath != ".claude/skills/thrum" {
		t.Errorf("expected .claude/skills/thrum, got %q", result.InstallPath)
	}
}

func TestInstallSkills_NonClaudeAgentSkipsPluginCheck(t *testing.T) {
	fakeHomeDir(t)
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".cursor"), 0750)
	// Even with a .claude-plugin, cursor agent shouldn't be affected
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude-plugin"), 0750)
	_ = os.WriteFile(filepath.Join(tmpDir, ".claude-plugin/plugin.json"),
		[]byte(`{"name": "thrum"}`), 0600)

	opts := SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "cursor",
	}
	result, err := InstallSkills(opts)
	if err != nil {
		t.Fatalf("cursor agent should not be blocked by Claude plugin: %v", err)
	}
	if result.InstallPath != ".cursor/skills/thrum" {
		t.Errorf("expected .cursor/skills/thrum, got %q", result.InstallPath)
	}
}

func TestCheckThrumPlugin_CloudePluginDir(t *testing.T) {
	fakeHomeDir(t)
	tmpDir := t.TempDir()
	// Test with claude-plugin/ (no dot prefix)
	_ = os.MkdirAll(filepath.Join(tmpDir, "claude-plugin"), 0750)
	_ = os.WriteFile(filepath.Join(tmpDir, "claude-plugin/plugin.json"),
		[]byte(`{"name": "Thrum Agent Coordination"}`), 0600)

	agent, _ := runtime.GetAgent("claude")
	loc := checkThrumPlugin(tmpDir, agent)
	if loc == "" {
		t.Error("expected to detect thrum plugin in claude-plugin/")
	}
}

func TestCheckThrumPlugin_NonThrumPlugin(t *testing.T) {
	fakeHomeDir(t)
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude-plugin"), 0750)
	_ = os.WriteFile(filepath.Join(tmpDir, ".claude-plugin/plugin.json"),
		[]byte(`{"name": "some-other-plugin"}`), 0600)

	agent, _ := runtime.GetAgent("claude")
	loc := checkThrumPlugin(tmpDir, agent)
	if loc != "" {
		t.Errorf("expected no detection for non-thrum plugin, got %q", loc)
	}
}

func TestCheckThrumPlugin_UserLevelInstalled(t *testing.T) {
	fakeHome := t.TempDir()
	original := userHomeDirFunc
	userHomeDirFunc = func() (string, error) { return fakeHome, nil }
	t.Cleanup(func() { userHomeDirFunc = original })

	// Create a fake installed_plugins.json with thrum
	pluginsDir := filepath.Join(fakeHome, ".claude", "plugins")
	_ = os.MkdirAll(pluginsDir, 0750)
	_ = os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"),
		[]byte(`{"version":2,"plugins":{"thrum@thrum":[{"scope":"user"}]}}`), 0600)

	tmpDir := t.TempDir()
	agent, _ := runtime.GetAgent("claude")
	loc := checkThrumPlugin(tmpDir, agent)
	if loc == "" {
		t.Error("expected to detect thrum plugin from installed_plugins.json")
	}
	if !strings.Contains(loc, "thrum@thrum") {
		t.Errorf("expected location to mention thrum@thrum, got %q", loc)
	}
}

func TestCheckThrumPlugin_NoPluginsFile(t *testing.T) {
	fakeHomeDir(t)
	tmpDir := t.TempDir()

	agent, _ := runtime.GetAgent("claude")
	loc := checkThrumPlugin(tmpDir, agent)
	if loc != "" {
		t.Errorf("expected no detection with no plugins, got %q", loc)
	}
}

func TestFormatSkillsInstall(t *testing.T) {
	result := &SkillsInstallResult{
		Agent:       "claude",
		InstallPath: ".claude/skills/thrum",
		Files:       []string{"SKILL.md", "references/CLI_REFERENCE.md"},
	}
	output := FormatSkillsInstall(result)
	if output == "" {
		t.Error("expected non-empty output")
	}
}
