package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderTemplate(t *testing.T) {
	data := TemplateData{
		AgentName:   "test_agent",
		AgentRole:   "implementer",
		AgentModule: "auth",
		MCPCommand:  "thrum",
	}

	tests := []struct {
		runtime  string
		template string
		contains []string
	}{
		{
			runtime:  "claude",
			template: "settings.json.tmpl",
			contains: []string{
				`"thrum"`,
				"test_agent",
				"mcp",
			},
		},
		{
			runtime:  "codex",
			template: "session-start.sh.tmpl",
			contains: []string{
				"THRUM_NAME=test_agent",
				"THRUM_ROLE=implementer",
				"THRUM_MODULE=auth",
			},
		},
		{
			runtime:  "codex",
			template: "AGENTS.md.tmpl",
			contains: []string{
				"test_agent",
				"implementer",
				"auth",
			},
		},
		{
			runtime:  "cursor",
			template: "cursorrules.tmpl",
			contains: []string{
				"test_agent",
				"implementer",
				"auth",
			},
		},
		{
			runtime:  "gemini",
			template: "instructions.md.tmpl",
			contains: []string{
				"test_agent",
				"implementer",
			},
		},
		{
			runtime:  "gemini",
			template: "settings.json.tmpl",
			contains: []string{
				`"thrum"`,
				"mcp",
			},
		},
		{
			runtime:  "shared",
			template: "startup.sh.tmpl",
			contains: []string{
				"test_agent",
				"implementer",
				"auth",
				"thrum daemon",
				"thrum quickstart",
			},
		},
		{
			runtime:  "auggie",
			template: "settings.json.tmpl",
			contains: []string{
				`"thrum"`,
				"test_agent",
				"mcp",
				"SessionStart",
			},
		},
		{
			runtime:  "auggie",
			template: "rules.md.tmpl",
			contains: []string{
				"test_agent",
				"implementer",
				"auth",
				"type: always",
			},
		},
		{
			runtime:  "cli-only",
			template: "polling-loop.sh.tmpl",
			contains: []string{
				"test_agent",
				"thrum inbox",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.runtime+"/"+tt.template, func(t *testing.T) {
			result, err := RenderTemplate(tt.runtime, tt.template, data)
			if err != nil {
				t.Fatal(err)
			}

			for _, substr := range tt.contains {
				if !strings.Contains(result, substr) {
					t.Errorf("expected output to contain %q, got:\n%s", substr, result)
				}
			}
		})
	}
}

func TestRenderTemplate_InvalidTemplate(t *testing.T) {
	data := TemplateData{AgentName: "test"}
	_, err := RenderTemplate("nonexistent", "fake.tmpl", data)
	if err == nil {
		t.Fatal("expected error for invalid template")
	}
}

func TestRuntimeInit_DryRun(t *testing.T) {
	tmpDir := t.TempDir()

	opts := RuntimeInitOptions{
		RepoPath:  tmpDir,
		Runtime:   "claude",
		DryRun:    true,
		AgentName: "test_agent",
		AgentRole: "implementer",
		AgentMod:  "test",
	}

	result, err := RuntimeInit(opts)
	if err != nil {
		t.Fatalf("RuntimeInit dry-run failed: %v", err)
	}

	if !result.DryRun {
		t.Error("expected DryRun=true in result")
	}

	if len(result.Files) == 0 {
		t.Error("expected file actions in result")
	}

	// Verify no files were created
	for _, f := range result.Files {
		outPath := filepath.Join(tmpDir, f.Path)
		if _, err := os.Stat(outPath); err == nil {
			t.Errorf("dry-run should not create files, but %s exists", f.Path)
		}
	}
}

func TestRuntimeInit_CreateFiles(t *testing.T) {
	tmpDir := t.TempDir()

	opts := RuntimeInitOptions{
		RepoPath:  tmpDir,
		Runtime:   "claude",
		Force:     false,
		AgentName: "test_agent",
		AgentRole: "implementer",
		AgentMod:  "test",
	}

	result, err := RuntimeInit(opts)
	if err != nil {
		t.Fatalf("RuntimeInit failed: %v", err)
	}

	// Verify files were created
	for _, f := range result.Files {
		outPath := filepath.Join(tmpDir, f.Path)
		if _, err := os.Stat(outPath); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", f.Path)
		}
	}

	// Verify .claude/settings.json contains expected content
	settingsPath := filepath.Join(tmpDir, ".claude", "settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	if !strings.Contains(string(content), "test_agent") {
		t.Error("settings.json should contain agent name")
	}
	if !strings.Contains(string(content), "thrum") {
		t.Error("settings.json should contain MCP command")
	}
}

func TestRuntimeInit_SkipExisting(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a pre-existing file
	claudeDir := filepath.Join(tmpDir, ".claude")
	os.MkdirAll(claudeDir, 0755)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("existing"), 0644)

	opts := RuntimeInitOptions{
		RepoPath:  tmpDir,
		Runtime:   "claude",
		Force:     false,
		AgentName: "test_agent",
	}

	result, err := RuntimeInit(opts)
	if err != nil {
		t.Fatalf("RuntimeInit failed: %v", err)
	}

	// First file (settings.json) should be skipped
	skipped := false
	for _, f := range result.Files {
		if f.Path == ".claude/settings.json" && f.Skipped {
			skipped = true
		}
	}
	if !skipped {
		t.Error("expected settings.json to be skipped when it already exists")
	}

	// Verify existing file was not modified
	content, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if string(content) != "existing" {
		t.Error("existing file should not be modified without --force")
	}
}

func TestRuntimeInit_ForceOverwrite(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a pre-existing file
	claudeDir := filepath.Join(tmpDir, ".claude")
	os.MkdirAll(claudeDir, 0755)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("existing"), 0644)

	opts := RuntimeInitOptions{
		RepoPath:  tmpDir,
		Runtime:   "claude",
		Force:     true,
		AgentName: "test_agent",
	}

	result, err := RuntimeInit(opts)
	if err != nil {
		t.Fatalf("RuntimeInit failed: %v", err)
	}

	// No files should be skipped
	for _, f := range result.Files {
		if f.Skipped {
			t.Errorf("no files should be skipped with --force, but %s was", f.Path)
		}
	}

	// Verify file was overwritten
	content, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if string(content) == "existing" {
		t.Error("file should be overwritten with --force")
	}
}

func TestRuntimeInit_AllRuntimes(t *testing.T) {
	tmpDir := t.TempDir()

	opts := RuntimeInitOptions{
		RepoPath:  tmpDir,
		Runtime:   "all",
		Force:     true,
		AgentName: "test_agent",
		AgentRole: "implementer",
		AgentMod:  "test",
	}

	result, err := RuntimeInit(opts)
	if err != nil {
		t.Fatalf("RuntimeInit for all failed: %v", err)
	}

	if len(result.Files) == 0 {
		t.Error("expected files to be created for all runtimes")
	}

	// Check key files exist
	expectedFiles := []string{
		".claude/settings.json",
		".codex/hooks/session-start",
		"AGENTS.md",
		".cursorrules",
		".gemini/instructions.md",
		".augment/settings.json",
		".augment/rules/thrum.md",
		"scripts/thrum-startup.sh",
	}
	for _, expected := range expectedFiles {
		outPath := filepath.Join(tmpDir, expected)
		if _, err := os.Stat(outPath); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist for --runtime all", expected)
		}
	}
}

func TestRuntimeInit_Auggie(t *testing.T) {
	tmpDir := t.TempDir()

	opts := RuntimeInitOptions{
		RepoPath:  tmpDir,
		Runtime:   "auggie",
		Force:     true,
		AgentName: "test_agent",
		AgentRole: "implementer",
		AgentMod:  "backend",
	}

	result, err := RuntimeInit(opts)
	if err != nil {
		t.Fatalf("RuntimeInit for auggie failed: %v", err)
	}

	if len(result.Files) != 3 {
		t.Errorf("expected 3 files for auggie, got %d", len(result.Files))
	}

	// Verify .augment/settings.json
	settingsPath := filepath.Join(tmpDir, ".augment", "settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read .augment/settings.json: %v", err)
	}
	settingsStr := string(content)
	if !strings.Contains(settingsStr, "thrum") {
		t.Error(".augment/settings.json should contain MCP server config")
	}
	if !strings.Contains(settingsStr, "SessionStart") {
		t.Error(".augment/settings.json should contain SessionStart hook")
	}
	if !strings.Contains(settingsStr, "test_agent") {
		t.Error(".augment/settings.json should contain agent name")
	}

	// Verify .augment/rules/thrum.md
	rulesPath := filepath.Join(tmpDir, ".augment", "rules", "thrum.md")
	content, err = os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("failed to read .augment/rules/thrum.md: %v", err)
	}
	rulesStr := string(content)
	if !strings.Contains(rulesStr, "type: always") {
		t.Error("rules file should have 'type: always' frontmatter")
	}
	if !strings.Contains(rulesStr, "test_agent") {
		t.Error("rules file should contain agent name")
	}
	if !strings.Contains(rulesStr, "backend") {
		t.Error("rules file should contain agent module")
	}

	// Verify startup script
	startupPath := filepath.Join(tmpDir, "scripts", "thrum-startup.sh")
	if _, err := os.Stat(startupPath); os.IsNotExist(err) {
		t.Error("startup script should be created for auggie")
	}
}

func TestRuntimeInit_InvalidRuntime(t *testing.T) {
	tmpDir := t.TempDir()

	opts := RuntimeInitOptions{
		RepoPath: tmpDir,
		Runtime:  "nonexistent",
	}

	_, err := RuntimeInit(opts)
	if err == nil {
		t.Fatal("expected error for invalid runtime")
	}
}

func TestRuntimeInit_DefaultValues(t *testing.T) {
	tmpDir := t.TempDir()

	opts := RuntimeInitOptions{
		RepoPath: tmpDir,
		Runtime:  "cli-only",
		// No agent name, role, or module specified
	}

	result, err := RuntimeInit(opts)
	if err != nil {
		t.Fatalf("RuntimeInit failed: %v", err)
	}

	if len(result.Files) == 0 {
		t.Error("expected files even with default values")
	}

	// Verify startup script uses defaults
	startupPath := filepath.Join(tmpDir, "scripts", "thrum-startup.sh")
	content, err := os.ReadFile(startupPath)
	if err != nil {
		t.Fatalf("failed to read startup script: %v", err)
	}
	if !strings.Contains(string(content), "default_agent") {
		t.Error("startup script should use default agent name")
	}
}

func TestFormatRuntimeInit(t *testing.T) {
	result := &RuntimeInitResult{
		Runtime: "claude",
		DryRun:  false,
		Files: []FileAction{
			{Path: ".claude/settings.json", Action: "create"},
			{Path: "scripts/thrum-startup.sh", Action: "create"},
		},
	}

	output := FormatRuntimeInit(result)
	if !strings.Contains(output, ".claude/settings.json") {
		t.Error("output should contain file path")
	}
	if !strings.Contains(output, "✓") {
		t.Error("output should contain checkmark for created files")
	}
}

func TestFormatRuntimeInit_DryRun(t *testing.T) {
	result := &RuntimeInitResult{
		Runtime: "claude",
		DryRun:  true,
		Files: []FileAction{
			{Path: ".claude/settings.json", Action: "create"},
		},
	}

	output := FormatRuntimeInit(result)
	if !strings.Contains(output, "Dry run") {
		t.Error("output should indicate dry run")
	}
}

func TestEachRuntimeTemplateSet(t *testing.T) {
	runtimes := []string{"claude", "codex", "cursor", "gemini", "auggie", "cli-only"}

	for _, rt := range runtimes {
		t.Run(rt, func(t *testing.T) {
			tmpls := runtimeTemplates(rt)
			if len(tmpls) == 0 {
				t.Errorf("runtime %q has no templates", rt)
			}

			for _, tmpl := range tmpls {
				// Verify template exists in embedded FS
				_, err := templateFS.ReadFile(tmpl.tmplPath)
				if err != nil {
					t.Errorf("template %q not found in embedded FS: %v", tmpl.tmplPath, err)
				}
			}
		})
	}
}
