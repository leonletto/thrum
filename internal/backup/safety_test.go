package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateSafetyBackup(t *testing.T) {
	base := t.TempDir()
	repoName := "test-repo"
	currentDir := filepath.Join(base, repoName, "current")

	if err := os.MkdirAll(currentDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(currentDir, "events.jsonl"), []byte("data\n"), 0600); err != nil {
		t.Fatal(err)
	}

	zipPath, err := CreateSafetyBackup(base, repoName)
	if err != nil {
		t.Fatalf("CreateSafetyBackup() error: %v", err)
	}

	if zipPath == "" {
		t.Fatal("expected non-empty zip path")
	}

	// Verify it's a pre-restore file
	if !strings.HasPrefix(filepath.Base(zipPath), "pre-restore-") {
		t.Errorf("expected pre-restore- prefix, got %q", filepath.Base(zipPath))
	}

	// Verify zip exists
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("safety backup not found: %v", err)
	}

	// Current dir should still exist (not modified)
	if _, err := os.Stat(filepath.Join(currentDir, "events.jsonl")); err != nil {
		t.Error("current dir should not be modified by safety backup")
	}
}

func TestCreateSafetyBackup_NothingToProtect(t *testing.T) {
	base := t.TempDir()
	zipPath, err := CreateSafetyBackup(base, "test-repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if zipPath != "" {
		t.Errorf("expected empty path for nothing to protect, got %q", zipPath)
	}
}

func TestCreateSafetyBackup_EmptyCurrentDir(t *testing.T) {
	base := t.TempDir()
	repoName := "test-repo"
	currentDir := filepath.Join(base, repoName, "current")
	if err := os.MkdirAll(currentDir, 0750); err != nil {
		t.Fatal(err)
	}

	zipPath, err := CreateSafetyBackup(base, repoName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if zipPath != "" {
		t.Errorf("expected empty path for empty current dir, got %q", zipPath)
	}
}
