package context

import (
	"os"
	"path/filepath"
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
