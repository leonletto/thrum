package context

import (
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
	for _, keyword := range []string{"thrum inbox", "thrum send", "thrum reply", "thrum status", "thrum context save"} {
		if !strings.Contains(s, keyword) {
			t.Errorf("DefaultPreamble missing keyword %q", keyword)
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
