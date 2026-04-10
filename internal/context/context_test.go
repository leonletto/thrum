package context

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSave(t *testing.T) {
	thrumDir := t.TempDir()
	content := []byte("# Test Context\n\nSome session state here.\n")

	if err := Save(thrumDir, "test_agent", content); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists
	path := filepath.Join(thrumDir, "context", "test_agent.md")
	data, err := os.ReadFile(path) //nolint:gosec // G304 - test helper reading temp file
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", data, content)
	}

	// Verify directory permissions
	info, err := os.Stat(filepath.Join(thrumDir, "context"))
	if err != nil {
		t.Fatalf("Stat context dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0750 {
		t.Errorf("directory permission: got %o, want 0750", perm)
	}
}

func TestSaveOverwrite(t *testing.T) {
	thrumDir := t.TempDir()

	if err := Save(thrumDir, "agent", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := Save(thrumDir, "agent", []byte("second")); err != nil {
		t.Fatal(err)
	}

	data, err := Load(thrumDir, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" {
		t.Errorf("got %q, want %q", data, "second")
	}
}

func TestLoad(t *testing.T) {
	thrumDir := t.TempDir()
	content := []byte("# Agent Context\n")

	if err := Save(thrumDir, "myagent", content); err != nil {
		t.Fatal(err)
	}

	data, err := Load(thrumDir, "myagent")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", data, content)
	}
}

func TestLoadNonExistent(t *testing.T) {
	thrumDir := t.TempDir()

	data, err := Load(thrumDir, "nonexistent")
	if err != nil {
		t.Fatalf("Load should not error for missing file: %v", err)
	}
	if data != nil {
		t.Errorf("expected nil for missing context, got %q", data)
	}
}

func TestClear(t *testing.T) {
	thrumDir := t.TempDir()

	// Save then clear
	if err := Save(thrumDir, "agent", []byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := Clear(thrumDir, "agent"); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// Verify file is gone
	data, err := Load(thrumDir, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Errorf("expected nil after clear, got %q", data)
	}
}

func TestClearNonExistent(t *testing.T) {
	thrumDir := t.TempDir()

	// Clear on non-existent should not error (idempotent)
	if err := Clear(thrumDir, "nonexistent"); err != nil {
		t.Fatalf("Clear should be idempotent: %v", err)
	}
}

func TestContextPath(t *testing.T) {
	path := ContextPath("/repo/.thrum", "coordinator")
	want := filepath.Join("/repo/.thrum", "context", "coordinator.md")
	if path != want {
		t.Errorf("ContextPath: got %q, want %q", path, want)
	}
}

func TestMultipleAgents(t *testing.T) {
	thrumDir := t.TempDir()

	agents := map[string]string{
		"coordinator": "# Coordinator\nPlanning session",
		"implementer": "# Implementer\nWriting code",
		"reviewer":    "# Reviewer\nReviewing PR",
	}

	// Save all
	for name, content := range agents {
		if err := Save(thrumDir, name, []byte(content)); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
	}

	// Verify all
	for name, want := range agents {
		data, err := Load(thrumDir, name)
		if err != nil {
			t.Fatalf("Load %s: %v", name, err)
		}
		if string(data) != want {
			t.Errorf("Load %s: got %q, want %q", name, data, want)
		}
	}

	// Clear one, verify others remain
	if err := Clear(thrumDir, "reviewer"); err != nil {
		t.Fatal(err)
	}

	if data, _ := Load(thrumDir, "reviewer"); data != nil {
		t.Error("reviewer should be cleared")
	}
	if data, _ := Load(thrumDir, "coordinator"); data == nil {
		t.Error("coordinator should still exist")
	}
	if data, _ := Load(thrumDir, "implementer"); data == nil {
		t.Error("implementer should still exist")
	}
}

func TestSaveBadDir(t *testing.T) {
	// Use a file as the "thrumDir" so MkdirAll fails
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "notadir")
	if err := os.WriteFile(filePath, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	err := Save(filePath, "agent", []byte("data"))
	if err == nil {
		t.Fatal("expected error when thrumDir is a file")
	}
}

func TestLoadReadError(t *testing.T) {
	// Create context dir but make the file a directory to cause a read error
	thrumDir := t.TempDir()
	contextDir := filepath.Join(thrumDir, "context")
	if err := os.MkdirAll(filepath.Join(contextDir, "agent.md"), 0750); err != nil {
		t.Fatal(err)
	}

	_, err := Load(thrumDir, "agent")
	if err == nil {
		t.Fatal("expected error reading a directory as file")
	}
}

func TestClearRemoveError(t *testing.T) {
	// Create context dir but make the .md path a non-empty directory
	thrumDir := t.TempDir()
	mdDir := filepath.Join(thrumDir, "context", "agent.md")
	if err := os.MkdirAll(mdDir, 0750); err != nil {
		t.Fatal(err)
	}
	// Put a file inside so rmdir fails
	if err := os.WriteFile(filepath.Join(mdDir, "child"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	err := Clear(thrumDir, "agent")
	if err == nil {
		t.Fatal("expected error removing non-empty directory")
	}
}

func TestPreamblePath(t *testing.T) {
	path := PreamblePath("/repo/.thrum", "coordinator")
	want := filepath.Join("/repo/.thrum", "context", "coordinator_preamble.md")
	if path != want {
		t.Errorf("PreamblePath: got %q, want %q", path, want)
	}
}

func TestLoadPreambleExisting(t *testing.T) {
	thrumDir := t.TempDir()
	content := []byte("# Custom Preamble\n")

	if err := SavePreamble(thrumDir, "test_agent", content); err != nil {
		t.Fatal(err)
	}

	data, err := LoadPreamble(thrumDir, "test_agent")
	if err != nil {
		t.Fatalf("LoadPreamble failed: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", data, content)
	}
}

func TestLoadPreambleMissing(t *testing.T) {
	thrumDir := t.TempDir()

	data, err := LoadPreamble(thrumDir, "nonexistent")
	if err != nil {
		t.Fatalf("LoadPreamble should not error for missing file: %v", err)
	}
	if data != nil {
		t.Errorf("expected nil for missing preamble, got %q", data)
	}
}

func TestSavePreamble(t *testing.T) {
	thrumDir := t.TempDir()
	content := []byte("# My Preamble\n\nCustom instructions.\n")

	if err := SavePreamble(thrumDir, "agent", content); err != nil {
		t.Fatalf("SavePreamble failed: %v", err)
	}

	path := filepath.Join(thrumDir, "context", "agent_preamble.md")
	data, err := os.ReadFile(path) //nolint:gosec // G304 - test helper reading temp file
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", data, content)
	}
}

func TestEnsurePreambleCreatesDefault(t *testing.T) {
	thrumDir := t.TempDir()

	if err := EnsurePreamble(thrumDir, "agent"); err != nil {
		t.Fatalf("EnsurePreamble failed: %v", err)
	}

	data, err := LoadPreamble(thrumDir, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if data == nil {
		t.Fatal("expected preamble to be created")
	}
	if string(data) != string(DefaultPreamble()) {
		t.Errorf("expected default preamble, got %q", data)
	}
}

func TestEnsurePreambleNoOverwrite(t *testing.T) {
	thrumDir := t.TempDir()
	custom := []byte("# Custom\n")

	if err := SavePreamble(thrumDir, "agent", custom); err != nil {
		t.Fatal(err)
	}

	if err := EnsurePreamble(thrumDir, "agent"); err != nil {
		t.Fatalf("EnsurePreamble failed: %v", err)
	}

	data, err := LoadPreamble(thrumDir, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(custom) {
		t.Errorf("EnsurePreamble overwrote existing: got %q, want %q", data, custom)
	}
}

func TestDefaultPreambleContent(t *testing.T) {
	content := DefaultPreamble()
	if len(content) == 0 {
		t.Fatal("DefaultPreamble should not be empty")
	}
	s := string(content)

	// Mode-independent content MUST be present
	for _, keyword := range []string{
		"thrum context",
		"thrum prime",
		".thrum/strategies/sub-agent-strategy.md",
		".thrum/strategies/thrum-registration.md",
		".thrum/strategies/resume-after-context-loss.md",
		"Operating Principles",
		"Anti-Patterns",
		"Context Hog",
	} {
		if !strings.Contains(s, keyword) {
			t.Errorf("DefaultPreamble missing mode-independent keyword %q", keyword)
		}
	}

	// Listener-specific content MUST be absent (moved to prime section 5)
	for _, keyword := range []string{
		"LISTENER RULE",
		"CronCreate",
		"Deaf Agent",
		"Background Message Listener",
		"message-listener",
		"Startup Protocol",
	} {
		if strings.Contains(s, keyword) {
			t.Errorf("DefaultPreamble should not contain listener-specific keyword %q", keyword)
		}
	}

	// Tmux commands and dispatch pattern MUST be present
	for _, keyword := range []string{
		"thrum tmux start",
		"thrum tmux status",
		"Sub-Agent Dispatcher",
		"thrum send",
	} {
		if !strings.Contains(s, keyword) {
			t.Errorf("DefaultPreamble missing tmux/dispatch keyword %q", keyword)
		}
	}
}

func TestSaveEmptyContent(t *testing.T) {
	thrumDir := t.TempDir()

	if err := Save(thrumDir, "agent", []byte{}); err != nil {
		t.Fatal(err)
	}

	data, err := Load(thrumDir, "agent")
	if err != nil {
		t.Fatal(err)
	}
	// Empty file should return empty bytes (not nil, since file exists)
	if len(data) != 0 {
		t.Errorf("expected empty content, got %q", data)
	}
}

// Tests for quickstart context bootstrapping behavior (thrum-epce)

func TestBootstrapCreatesEmptyContextAndDefaultPreamble(t *testing.T) {
	// Simulates quickstart without --preamble-file
	thrumDir := t.TempDir()
	agentName := "test-impl"

	// Create empty context (only if not exists)
	ctxPath := ContextPath(thrumDir, agentName)
	if _, err := os.Stat(ctxPath); os.IsNotExist(err) {
		if err := Save(thrumDir, agentName, []byte("")); err != nil {
			t.Fatalf("Save empty context: %v", err)
		}
	}

	// Create default preamble
	if err := EnsurePreamble(thrumDir, agentName); err != nil {
		t.Fatalf("EnsurePreamble: %v", err)
	}

	// Verify context file is empty
	ctxData, err := Load(thrumDir, agentName)
	if err != nil {
		t.Fatalf("Load context: %v", err)
	}
	if len(ctxData) != 0 {
		t.Errorf("context should be empty, got %q", ctxData)
	}

	// Verify preamble has default content
	preambleData, err := LoadPreamble(thrumDir, agentName)
	if err != nil {
		t.Fatal(err)
	}
	if string(preambleData) != string(DefaultPreamble()) {
		t.Errorf("preamble should be default, got %q", preambleData)
	}
}

func TestBootstrapComposedPreamble(t *testing.T) {
	// Simulates quickstart with --preamble-file
	thrumDir := t.TempDir()
	agentName := "test-impl"

	// First create default (as quickstart does)
	if err := EnsurePreamble(thrumDir, agentName); err != nil {
		t.Fatal(err)
	}

	// Compose default + custom (as quickstart does with --preamble-file)
	customContent := []byte("## Project Context\n\nThis is custom content.\n")
	composed := append(DefaultPreamble(), []byte("\n---\n\n")...)
	composed = append(composed, customContent...)

	if err := SavePreamble(thrumDir, agentName, composed); err != nil {
		t.Fatalf("SavePreamble composed: %v", err)
	}

	// Verify composed content
	data, err := LoadPreamble(thrumDir, agentName)
	if err != nil {
		t.Fatal(err)
	}

	s := string(data)
	if !strings.Contains(s, "Thrum Quick Reference") {
		t.Error("composed preamble missing default content")
	}
	if !strings.Contains(s, "\n---\n\n") {
		t.Error("composed preamble missing separator")
	}
	if !strings.Contains(s, "Project Context") {
		t.Error("composed preamble missing custom content")
	}
}

func TestBootstrapEnsurePreambleNoOverwriteExisting(t *testing.T) {
	// Re-running quickstart without --preamble-file should not overwrite
	thrumDir := t.TempDir()
	agentName := "test-impl"

	custom := []byte("## Custom Preamble\n\nAlready set up.\n")
	if err := SavePreamble(thrumDir, agentName, custom); err != nil {
		t.Fatal(err)
	}

	// Quickstart calls EnsurePreamble — should be a no-op
	if err := EnsurePreamble(thrumDir, agentName); err != nil {
		t.Fatal(err)
	}

	data, err := LoadPreamble(thrumDir, agentName)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(custom) {
		t.Errorf("EnsurePreamble overwrote existing preamble: got %q, want %q", data, custom)
	}
}

func TestBootstrapPreambleFileReplaces(t *testing.T) {
	// Re-running with --preamble-file replaces the preamble
	thrumDir := t.TempDir()
	agentName := "test-impl"

	// Initial preamble
	initial := []byte("## Old Preamble\n")
	if err := SavePreamble(thrumDir, agentName, initial); err != nil {
		t.Fatal(err)
	}

	// Re-run with new custom content (SavePreamble overwrites)
	newCustom := []byte("## New Project Context\n\nUpdated content.\n")
	composed := append(DefaultPreamble(), []byte("\n---\n\n")...)
	composed = append(composed, newCustom...)

	if err := SavePreamble(thrumDir, agentName, composed); err != nil {
		t.Fatal(err)
	}

	data, err := LoadPreamble(thrumDir, agentName)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "New Project Context") {
		t.Error("preamble should contain new custom content")
	}
	if strings.Contains(string(data), "Old Preamble") {
		t.Error("preamble should not contain old content")
	}
}

func TestBootstrapContextFileNotOverwritten(t *testing.T) {
	// Re-running quickstart should not overwrite existing context
	thrumDir := t.TempDir()
	agentName := "test-impl"

	existing := []byte("# Existing context data\n")
	if err := Save(thrumDir, agentName, existing); err != nil {
		t.Fatal(err)
	}

	// Bootstrap checks: only create if not exists
	ctxPath := ContextPath(thrumDir, agentName)
	if _, err := os.Stat(ctxPath); os.IsNotExist(err) {
		if err := Save(thrumDir, agentName, []byte("")); err != nil {
			t.Fatal(err)
		}
	}

	// Verify existing content preserved
	data, err := Load(thrumDir, agentName)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(existing) {
		t.Errorf("context file overwritten: got %q, want %q", data, existing)
	}
}

func TestBootstrapPreambleFileNotFound(t *testing.T) {
	// Simulates --preamble-file pointing to non-existent file
	_, err := os.ReadFile("/nonexistent/path/preamble.md")
	if err == nil {
		t.Fatal("expected error reading nonexistent preamble file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected IsNotExist error, got: %v", err)
	}
}

func TestRoleAwarePreamble(t *testing.T) {
	knownRoles := []struct {
		role       string
		headerText string
	}{
		{"coordinator", "## Your Role: Coordinator"},
		{"implementer", "## Your Role: Implementer"},
		{"planner", "## Your Role: Planner"},
		{"researcher", "## Your Role: Researcher"},
		{"reviewer", "## Your Role: Reviewer"},
		{"tester", "## Your Role: Tester"},
		{"deployer", "## Your Role: Deployer"},
		{"documenter", "## Your Role: Documenter"},
		{"monitor", "## Your Role: Monitor"},
		{"orchestrator", "## Your Role: Orchestrator"},
	}

	for _, tc := range knownRoles {
		t.Run(tc.role, func(t *testing.T) {
			content := RoleAwarePreamble(tc.role)
			s := string(content)

			// Role header must appear at the start
			if !strings.HasPrefix(s, tc.headerText) {
				t.Errorf("RoleAwarePreamble(%q): expected prefix %q, got start: %q", tc.role, tc.headerText, s[:min(len(s), 80)])
			}

			// Separator between header and base preamble
			if !strings.Contains(s, "\n---\n\n") {
				t.Errorf("RoleAwarePreamble(%q): missing separator", tc.role)
			}

			// Base preamble content must still be present
			if !strings.Contains(s, "Operating Principles") {
				t.Errorf("RoleAwarePreamble(%q): missing base preamble content", tc.role)
			}
			if !strings.Contains(s, "thrum prime") {
				t.Errorf("RoleAwarePreamble(%q): missing context command reference", tc.role)
			}
		})
	}

	// Case-insensitive matching
	t.Run("case_insensitive", func(t *testing.T) {
		upper := RoleAwarePreamble("COORDINATOR")
		lower := RoleAwarePreamble("coordinator")
		if string(upper) != string(lower) {
			t.Error("RoleAwarePreamble should be case-insensitive")
		}
	})
}

func TestRoleAwarePreambleUnknownRole(t *testing.T) {
	unknown := RoleAwarePreamble("unknown-role")
	def := DefaultPreamble()
	if string(unknown) != string(def) {
		t.Errorf("RoleAwarePreamble(unknown): expected DefaultPreamble(), got different content")
	}

	empty := RoleAwarePreamble("")
	if string(empty) != string(def) {
		t.Errorf("RoleAwarePreamble(\"\"): expected DefaultPreamble(), got different content")
	}
}

func TestGenerateProjectState(t *testing.T) {
	opts := &ProjectStateOpts{
		RepoName: "test-project",
		Language: "Go",
		Version:  "v1.2.3",
		Branch:   "main",
	}
	content := GenerateProjectState(opts)
	s := string(content)
	if !strings.Contains(s, "# Project State — test-project") {
		t.Error("missing header")
	}
	if !strings.Contains(s, "**Codebase:** Go") {
		t.Error("missing language")
	}
	if !strings.Contains(s, "**Version:** v1.2.3") {
		t.Error("missing version")
	}
	if !strings.Contains(s, "## Recent Sessions") {
		t.Error("missing Recent Sessions section")
	}
	if !strings.Contains(s, "## Key Architecture Files") {
		t.Error("missing Key Architecture Files section")
	}
}

func TestGenerateProjectStateMinimal(t *testing.T) {
	opts := &ProjectStateOpts{
		RepoName: "bare-repo",
		Branch:   "develop",
	}
	content := GenerateProjectState(opts)
	s := string(content)
	if !strings.Contains(s, "# Project State — bare-repo") {
		t.Error("missing header")
	}
	if !strings.Contains(s, "**Branch:** develop") {
		t.Error("missing branch")
	}
	// Blank codebase/version lines should still be present
	if !strings.Contains(s, "**Codebase:**") {
		t.Error("missing codebase line")
	}
	if !strings.Contains(s, "**Version:**") {
		t.Error("missing version line")
	}
}

func TestGenerateProjectStateWithBeads(t *testing.T) {
	opts := &ProjectStateOpts{
		RepoName: "thrum",
		Language: "Go + Node.js",
		Version:  "v0.6.3",
		Branch:   "main",
		Beads:    "32 open, 245 closed",
	}
	content := GenerateProjectState(opts)
	s := string(content)
	if !strings.Contains(s, "**Beads:** 32 open, 245 closed") {
		t.Error("missing beads stats")
	}
}

func TestDetectLanguage(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0o600)
	lang := DetectLanguage(dir)
	if lang != "Go" {
		t.Errorf("expected Go, got %q", lang)
	}
}

func TestDetectLanguageMultiple(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0o600)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o600)
	lang := DetectLanguage(dir)
	if lang != "Go + Node.js" {
		t.Errorf("expected 'Go + Node.js', got %q", lang)
	}
}

func TestDetectLanguageNone(t *testing.T) {
	dir := t.TempDir()
	lang := DetectLanguage(dir)
	if lang != "" {
		t.Errorf("expected empty string, got %q", lang)
	}
}

func TestWriteStrategies(t *testing.T) {
	thrumDir := t.TempDir()

	err := WriteStrategies(thrumDir)
	if err != nil {
		t.Fatalf("WriteStrategies failed: %v", err)
	}

	// Verify all strategy files were written
	for _, name := range []string{
		"sub-agent-strategy.md",
		"thrum-registration.md",
		"resume-after-context-loss.md",
	} {
		path := filepath.Join(thrumDir, "strategies", name)
		data, err := os.ReadFile(path) //nolint:gosec // G304 - test helper reading temp file
		if err != nil {
			t.Errorf("strategy file %s not found: %v", name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("strategy file %s is empty", name)
		}
	}
}

func TestWriteStrategiesIdempotent(t *testing.T) {
	thrumDir := t.TempDir()

	// Write twice — should not error
	if err := WriteStrategies(thrumDir); err != nil {
		t.Fatalf("first WriteStrategies failed: %v", err)
	}
	if err := WriteStrategies(thrumDir); err != nil {
		t.Fatalf("second WriteStrategies failed: %v", err)
	}
}

// TestWriteStrategiesWritesLlmsTxt verifies that WriteStrategies creates
// .thrum/llms.txt with expected content. This covers the init path since
// thrum init calls WriteStrategies directly.
func TestWriteStrategiesWritesLlmsTxt(t *testing.T) {
	thrumDir := t.TempDir()

	if err := WriteStrategies(thrumDir); err != nil {
		t.Fatalf("WriteStrategies failed: %v", err)
	}

	llmsPath := filepath.Join(thrumDir, "llms.txt")

	data, err := os.ReadFile(llmsPath) //nolint:gosec // G304 - test helper reading temp file
	if err != nil {
		t.Fatalf("llms.txt not created: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("llms.txt is empty")
	}

	if !strings.HasPrefix(string(data), "# Thrum v") {
		prefix := string(data)
		if len(prefix) > 20 {
			prefix = prefix[:20]
		}
		t.Errorf("llms.txt does not start with expected header; got prefix: %q", prefix)
	}

	info, err := os.Stat(llmsPath)
	if err != nil {
		t.Fatalf("Stat llms.txt: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0644 {
		t.Errorf("llms.txt permission: got %o, want 0644", perm)
	}

	// Verify content matches the embedded reference file on disk.
	embeddedData, err := os.ReadFile(filepath.Join("reference", "llms.txt")) //nolint:gosec // G304 - test helper reading reference file
	if err != nil {
		t.Fatalf("read reference/llms.txt: %v", err)
	}
	if !bytes.Equal(data, embeddedData) {
		t.Errorf("llms.txt content (%d bytes) does not match embedded reference (%d bytes)", len(data), len(embeddedData))
	}
}

// TestWriteStrategiesSelfHealsLlmsTxt verifies that WriteStrategies recreates
// .thrum/llms.txt and strategy files when they have been deleted — matching
// the daemon self-heal behavior on restart (thrum-w71.3: strategies + llms.txt).
func TestWriteStrategiesSelfHealsLlmsTxt(t *testing.T) {
	thrumDir := t.TempDir()

	// Initial write.
	if err := WriteStrategies(thrumDir); err != nil {
		t.Fatalf("initial WriteStrategies failed: %v", err)
	}

	llmsPath := filepath.Join(thrumDir, "llms.txt")
	original, err := os.ReadFile(llmsPath) //nolint:gosec // G304 - test helper reading temp file
	if err != nil {
		t.Fatalf("read initial llms.txt: %v", err)
	}

	// Simulate accidental deletion of llms.txt.
	if err := os.Remove(llmsPath); err != nil {
		t.Fatalf("remove llms.txt: %v", err)
	}
	if _, err := os.Stat(llmsPath); !os.IsNotExist(err) {
		t.Fatal("llms.txt should not exist after removal")
	}

	// Self-heal: call WriteStrategies again (what the daemon does on restart).
	if err := WriteStrategies(thrumDir); err != nil {
		t.Fatalf("second WriteStrategies failed: %v", err)
	}

	restored, err := os.ReadFile(llmsPath) //nolint:gosec // G304 - test helper reading temp file
	if err != nil {
		t.Fatalf("llms.txt not recreated after self-heal: %v", err)
	}
	if !bytes.Equal(original, restored) {
		t.Errorf("restored llms.txt (%d bytes) differs from original (%d bytes)", len(restored), len(original))
	}

	// Also verify strategy file self-heal (thrum-w71.3 spec covers both).
	strategyPath := filepath.Join(thrumDir, "strategies", "sub-agent-strategy.md")

	// Delete the strategy file.
	if err := os.Remove(strategyPath); err != nil {
		t.Fatalf("remove strategy file: %v", err)
	}
	if _, err := os.Stat(strategyPath); !os.IsNotExist(err) {
		t.Fatal("strategy file should not exist after removal")
	}

	// Self-heal: WriteStrategies should recreate it.
	if err := WriteStrategies(thrumDir); err != nil {
		t.Fatalf("third WriteStrategies failed: %v", err)
	}

	restoredStrategy, err := os.ReadFile(strategyPath) //nolint:gosec // G304 - test helper reading temp file
	if err != nil {
		t.Fatalf("strategy file not recreated after self-heal at %s: %v", strategyPath, err)
	}
	if len(restoredStrategy) == 0 {
		t.Errorf("restored strategy file at %s is empty", strategyPath)
	}
}

// TestEmbeddedLlmsMatchesRoot fails if the root llms.txt has drifted from the
// embedded copy at internal/context/reference/llms.txt. Run
// 'make sync-embed-reference' to fix.
func TestEmbeddedLlmsMatchesRoot(t *testing.T) {
	rootPath := filepath.Join("..", "..", "llms.txt")
	rootData, err := os.ReadFile(rootPath) //nolint:gosec // G304 - test helper reading root llms.txt
	if err != nil {
		t.Fatalf("read root llms.txt at %s: %v", rootPath, err)
	}

	embeddedPath := filepath.Join("reference", "llms.txt")
	embeddedData, err := os.ReadFile(embeddedPath) //nolint:gosec // G304 - test helper reading reference file
	if err != nil {
		t.Fatalf("read embedded llms.txt at %s: %v", embeddedPath, err)
	}

	if !bytes.Equal(rootData, embeddedData) {
		t.Fatalf("root llms.txt (%d bytes) differs from internal/context/reference/llms.txt (%d bytes); run 'make sync-embed-reference'",
			len(rootData), len(embeddedData))
	}
}
