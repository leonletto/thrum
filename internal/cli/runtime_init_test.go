package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/hookmerge"
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
				"thrum-startup.sh",
				"SessionStart",
			},
		},
		{
			runtime:  "codex",
			template: "session-start.sh.tmpl",
			contains: []string{
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
				`thrum --repo "$THRUM_HOME" daemon status`,
				`thrum --repo "$THRUM_HOME" quickstart`,
				`thrum --repo "$THRUM_HOME" whoami`,
				"THRUM_NAME",
				"THRUM_HOME",
				"THRUM_AGENT_ID",
				`thrum --repo "$THRUM_HOME"`,
				"CLAUDE_ENV_FILE",
			},
		},
		{
			runtime:  "auggie",
			template: "settings.json.tmpl",
			contains: []string{
				`"thrum"`,
				"mcp",
				"SessionStart",
			},
		},
		{
			runtime:  "auggie",
			template: "rules.md.tmpl",
			contains: []string{
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
	content, err := os.ReadFile(filepath.Clean(settingsPath))
	if err != nil {
		t.Fatalf("failed to read settings.json: %v", err)
	}
	if strings.Contains(string(content), "THRUM_NAME") {
		t.Error("settings.json should not hardcode THRUM_NAME (quickstart reuses existing identity)")
	}
	if strings.Contains(string(content), "mcpServers") {
		t.Error("settings.json should NOT contain mcpServers (removed to prevent Claude Code hang)")
	}
	if !strings.Contains(string(content), "SessionStart") {
		t.Error("settings.json should contain SessionStart hook")
	}
}

// TestRuntimeInit_MergeExistingClaudeSettings verifies thrum-nh88's
// JSON-merge behavior for .claude/settings.json: when the file already
// exists, third-party hook entries (bd, user) are preserved and thrum's
// own hooks are reconciled additively.
func TestRuntimeInit_MergeExistingClaudeSettings(t *testing.T) {
	tmpDir := t.TempDir()

	// Pre-existing file with a third-party (bd) hook + a user hook.
	claudeDir := filepath.Join(tmpDir, ".claude")
	_ = os.MkdirAll(claudeDir, 0750)
	pre := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "bd prime --hook-json"}]},
      {"hooks": [{"type": "command", "command": "user custom"}]}
    ]
  },
  "model": "claude-sonnet-4-5"
}`
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(pre), 0600)

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

	var settingsAction *FileAction
	for i := range result.Files {
		if result.Files[i].Path == ".claude/settings.json" {
			settingsAction = &result.Files[i]
			break
		}
	}
	if settingsAction == nil {
		t.Fatal("expected FileAction for .claude/settings.json in result")
	}
	if settingsAction.Skipped {
		t.Fatal("merge-mode template must not produce Skipped=true")
	}
	if settingsAction.Action != "merge" {
		t.Errorf("expected action=merge, got %q", settingsAction.Action)
	}

	// Verify on-disk content: bd hook + user hook + thrum SessionStart hook
	// must all be present.
	mergedRaw, _ := os.ReadFile(filepath.Clean(filepath.Join(claudeDir, "settings.json")))
	got := string(mergedRaw)
	for _, must := range []string{
		"bd prime --hook-json",
		"user custom",
		"thrum-startup.sh",
		"claude-sonnet-4-5",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("merged file missing expected substring %q\nfile content:\n%s", must, got)
		}
	}
}

func TestRuntimeInit_ForceOverwrite(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a pre-existing file
	claudeDir := filepath.Join(tmpDir, ".claude")
	_ = os.MkdirAll(claudeDir, 0750)
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("existing"), 0600)

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
	content, _ := os.ReadFile(filepath.Clean(filepath.Join(claudeDir, "settings.json")))
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
		"scripts/thrum-check-inbox.sh",
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
	content, err := os.ReadFile(filepath.Clean(settingsPath))
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
	if strings.Contains(settingsStr, "THRUM_NAME") {
		t.Error(".augment/settings.json should not hardcode THRUM_NAME (quickstart reuses existing identity)")
	}

	// Verify .augment/rules/thrum.md
	rulesPath := filepath.Join(tmpDir, ".augment", "rules", "thrum.md")
	content, err = os.ReadFile(filepath.Clean(rulesPath))
	if err != nil {
		t.Fatalf("failed to read .augment/rules/thrum.md: %v", err)
	}
	rulesStr := string(content)
	if !strings.Contains(rulesStr, "type: always") {
		t.Error("rules file should have 'type: always' frontmatter")
	}
	if !strings.Contains(rulesStr, "quickstart") {
		t.Error("rules file should contain quickstart command")
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
	content, err := os.ReadFile(filepath.Clean(startupPath))
	if err != nil {
		t.Fatalf("failed to read startup script: %v", err)
	}
	if strings.Contains(string(content), "default_agent") {
		t.Error("startup script should not hardcode default_agent (quickstart reuses existing identity)")
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

// TestRuntimeInit_ManagedTemplateOverwritesExisting verifies that daemon-owned
// scripts (managed: true) overwrite stale on-disk content on every quickstart,
// without requiring --force (thrum-akqv).
func TestRuntimeInit_ManagedTemplateOverwritesExisting(t *testing.T) {
	cases := []struct {
		runtime string
		path    string
	}{
		{"claude", "scripts/thrum-startup.sh"},
		{"claude", "scripts/thrum-check-inbox.sh"},
		{"codex", ".codex/hooks/session-start"},
		{"cli-only", "scripts/thrum-polling.sh"},
	}

	const stale = "# STALE TEMPLATE CONTENT — should be overwritten\n"

	for _, tc := range cases {
		t.Run(tc.runtime+"/"+tc.path, func(t *testing.T) {
			tmpDir := t.TempDir()
			outPath := filepath.Join(tmpDir, tc.path)
			if err := os.MkdirAll(filepath.Dir(outPath), 0750); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(outPath, []byte(stale), 0600); err != nil {
				t.Fatalf("seed stale file: %v", err)
			}

			result, err := RuntimeInit(RuntimeInitOptions{
				RepoPath:  tmpDir,
				Runtime:   tc.runtime,
				Force:     false,
				AgentName: "test_agent",
			})
			if err != nil {
				t.Fatalf("RuntimeInit: %v", err)
			}

			var found *FileAction
			for i := range result.Files {
				if result.Files[i].Path == tc.path {
					found = &result.Files[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("FileAction for %q missing from result", tc.path)
			}
			if found.Skipped {
				t.Errorf("managed template %q was skipped; expected overwrite", tc.path)
			}
			if found.Action != "overwrite" {
				t.Errorf("managed template %q action = %q; want %q", tc.path, found.Action, "overwrite")
			}

			content, err := os.ReadFile(filepath.Clean(outPath))
			if err != nil {
				t.Fatalf("read overwritten file: %v", err)
			}
			if string(content) == stale {
				t.Errorf("managed template %q content unchanged; expected overwrite", tc.path)
			}
		})
	}
}

// TestRuntimeInit_UserConfigTemplatePreservesEdits verifies that
// user-customizable configs (managed=false, merge=false, the default) keep
// skip-on-exists so re-quickstart never bulldozes local edits (thrum-akqv).
//
// The claude .claude/settings.json case is excluded — it uses the merge
// mode (thrum-nh88) and is covered by TestRuntimeInit_MergeExistingClaudeSettings.
func TestRuntimeInit_UserConfigTemplatePreservesEdits(t *testing.T) {
	cases := []struct {
		runtime string
		path    string
	}{
		{"codex", "AGENTS.md"},
		{"cursor", ".cursorrules"},
		{"gemini", ".gemini/settings.json"},
		{"opencode", "opencode.json"},
		{"auggie", ".augment/rules/thrum.md"},
	}

	const custom = "# CUSTOM USER CONTENT — must be preserved\n"

	for _, tc := range cases {
		t.Run(tc.runtime+"/"+tc.path, func(t *testing.T) {
			tmpDir := t.TempDir()
			outPath := filepath.Join(tmpDir, tc.path)
			if err := os.MkdirAll(filepath.Dir(outPath), 0750); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(outPath, []byte(custom), 0600); err != nil {
				t.Fatalf("seed custom file: %v", err)
			}

			result, err := RuntimeInit(RuntimeInitOptions{
				RepoPath:  tmpDir,
				Runtime:   tc.runtime,
				Force:     false,
				AgentName: "test_agent",
			})
			if err != nil {
				t.Fatalf("RuntimeInit: %v", err)
			}

			var found *FileAction
			for i := range result.Files {
				if result.Files[i].Path == tc.path {
					found = &result.Files[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("FileAction for %q missing from result", tc.path)
			}
			if !found.Skipped {
				t.Errorf("user-config %q was not skipped; would clobber local edits", tc.path)
			}
			if found.Action != "skip" {
				t.Errorf("user-config %q action = %q; want %q", tc.path, found.Action, "skip")
			}

			content, err := os.ReadFile(filepath.Clean(outPath))
			if err != nil {
				t.Fatalf("read preserved file: %v", err)
			}
			if string(content) != custom {
				t.Errorf("user-config %q content changed; expected preservation", tc.path)
			}
		})
	}
}

// TestClaudeTemplateMatchesCanonicalHooks asserts that the rendered claude
// settings template extracts to exactly the same (event, command) pairs
// listed in hookmerge.CanonicalThrumHooks. Worktree.EnsureRedirects relies
// on the constant; runtime-init in the main repo renders the template.
// Both must agree, otherwise a worktree's hooks would drift from the
// main repo's. thrum-nh88.
func TestClaudeTemplateMatchesCanonicalHooks(t *testing.T) {
	rendered, err := renderTemplatePath("templates/claude/settings.json.tmpl", TemplateData{})
	if err != nil {
		t.Fatalf("render claude template: %v", err)
	}
	var parsed hookmerge.Settings
	if err := json.Unmarshal([]byte(rendered), &parsed); err != nil {
		t.Fatalf("rendered template is invalid JSON: %v\n%s", err, rendered)
	}
	got := hookmerge.ExtractCommands(parsed)

	if len(got) != len(hookmerge.CanonicalThrumHooks) {
		t.Fatalf("hook count mismatch: template has %d, CanonicalThrumHooks has %d\ngot=%+v\nwant=%+v",
			len(got), len(hookmerge.CanonicalThrumHooks), got, hookmerge.CanonicalThrumHooks)
	}
	// Build sets keyed by event:command for order-independent compare.
	gotSet := make(map[string]struct{}, len(got))
	for _, c := range got {
		gotSet[c.Event+"::"+c.Command] = struct{}{}
	}
	for _, want := range hookmerge.CanonicalThrumHooks {
		if _, ok := gotSet[want.Event+"::"+want.Command]; !ok {
			t.Errorf("CanonicalThrumHooks entry not found in rendered template: %+v", want)
		}
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
