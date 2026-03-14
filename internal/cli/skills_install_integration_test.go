package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/runtime"
)

func TestSkillsInstall_EndToEnd_Claude(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude"), 0750)

	// Verify detection finds Claude
	agents := runtime.DetectAgents(tmpDir)
	if len(agents) == 0 || agents[0].Name != "claude" {
		t.Fatalf("expected claude detection, got %v", agents)
	}

	// Install skills
	result, err := InstallSkills(SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "claude",
	})
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Verify path
	if result.InstallPath != ".claude/skills/thrum" {
		t.Errorf("wrong path: %q", result.InstallPath)
	}

	// Verify SKILL.md content
	data, err := os.ReadFile(filepath.Join(tmpDir, result.InstallPath, "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md not found: %v", err)
	}
	if !strings.Contains(string(data), "name: thrum") {
		t.Error("SKILL.md missing frontmatter")
	}

	// Verify no agent-specific content
	content := strings.ToLower(string(data))
	for _, forbidden := range []string{"claude-plugin", "plugin.json", "sessionstart", "precompact"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("SKILL.md contains agent-specific reference: %q", forbidden)
		}
	}

	// Verify references exist
	refs := filepath.Join(tmpDir, result.InstallPath, "references")
	entries, _ := os.ReadDir(refs)
	if len(entries) < 3 {
		t.Errorf("expected at least 3 reference files, got %d", len(entries))
	}
}

func TestSkillsInstall_EndToEnd_Generic(t *testing.T) {
	tmpDir := t.TempDir()

	// Use amp agent (which maps to .agents/skills)
	result, err := InstallSkills(SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "amp",
	})
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	if result.InstallPath != ".agents/skills/thrum" {
		t.Errorf("expected .agents/skills/thrum, got %q", result.InstallPath)
	}

	// Verify files are actually written
	skillPath := filepath.Join(tmpDir, result.InstallPath, "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Errorf("SKILL.md not written: %v", err)
	}
}

func TestSkillsInstall_EndToEnd_DryRun(t *testing.T) {
	tmpDir := t.TempDir()

	result, err := InstallSkills(SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "claude",
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}

	// Should list files but not write anything
	if len(result.Files) == 0 {
		t.Error("expected file list in dry-run result")
	}
	skillPath := filepath.Join(tmpDir, ".agents/skills/thrum/SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		t.Error("dry-run should not write files")
	}
}

func TestSkillsInstall_EndToEnd_ForceOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".cursor"), 0750)

	// First install
	_, err := InstallSkills(SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "cursor",
	})
	if err != nil {
		t.Fatalf("first install failed: %v", err)
	}

	// Second install without force should fail
	_, err = InstallSkills(SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "cursor",
	})
	if err == nil {
		t.Error("expected error on second install without --force")
	}

	// Second install with force should succeed
	result, err := InstallSkills(SkillsInstallOptions{
		RepoPath: tmpDir,
		Agent:    "cursor",
		Force:    true,
	})
	if err != nil {
		t.Fatalf("force install failed: %v", err)
	}
	if result.InstallPath != ".cursor/skills/thrum" {
		t.Errorf("expected .cursor/skills/thrum, got %q", result.InstallPath)
	}
}

func TestSkillsInstall_EndToEnd_MultipleAgents(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, ".claude"), 0750)
	_ = os.MkdirAll(filepath.Join(tmpDir, ".cursor"), 0750)

	// Detection finds both
	agents := runtime.DetectAgents(tmpDir)
	names := make(map[string]bool)
	for _, a := range agents {
		names[a.Name] = true
	}
	if !names["claude"] || !names["cursor"] {
		t.Fatalf("expected both claude and cursor detected, got %v", names)
	}

	// Install for each agent separately
	for _, agentName := range []string{"claude", "cursor"} {
		result, err := InstallSkills(SkillsInstallOptions{
			RepoPath: tmpDir,
			Agent:    agentName,
		})
		if err != nil {
			t.Fatalf("install for %s failed: %v", agentName, err)
		}
		skillPath := filepath.Join(tmpDir, result.InstallPath, "SKILL.md")
		if _, err := os.Stat(skillPath); err != nil {
			t.Errorf("SKILL.md not written for %s: %v", agentName, err)
		}
	}
}
