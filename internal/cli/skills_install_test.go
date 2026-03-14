package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallSkills_AgentWithConfigDir(t *testing.T) {
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
