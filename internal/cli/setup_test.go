package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetup_CreatesRedirect(t *testing.T) {
	// Create main repo with .thrum/
	mainRepo := t.TempDir()
	mainThrumDir := filepath.Join(mainRepo, ".thrum")
	if err := os.MkdirAll(mainThrumDir, 0750); err != nil {
		t.Fatalf("Failed to create main .thrum/: %v", err)
	}

	// Create worktree directory
	worktree := t.TempDir()

	err := Setup(SetupOptions{
		RepoPath: worktree,
		MainRepo: mainRepo,
	})
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Verify redirect file exists and has correct content
	redirectPath := filepath.Join(worktree, ".thrum", "redirect")
	content, err := os.ReadFile(redirectPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Failed to read redirect file: %v", err)
	}
	expected := mainThrumDir + "\n"
	if string(content) != expected {
		t.Errorf("Redirect content = %q, want %q", string(content), expected)
	}

	// Verify identities directory was created
	identitiesDir := filepath.Join(worktree, ".thrum", "identities")
	info, err := os.Stat(identitiesDir)
	if err != nil {
		t.Fatalf("Identities dir not found: %v", err)
	}
	if !info.IsDir() {
		t.Error("Identities path is not a directory")
	}
}

func TestSetup_MainRepoNotInitialized(t *testing.T) {
	// Main repo WITHOUT .thrum/
	mainRepo := t.TempDir()
	worktree := t.TempDir()

	err := Setup(SetupOptions{
		RepoPath: worktree,
		MainRepo: mainRepo,
	})
	if err == nil {
		t.Fatal("Expected error when main repo is not initialized")
	}
	if !strings.Contains(err.Error(), "main repo not initialized") {
		t.Errorf("Expected 'main repo not initialized' error, got: %v", err)
	}
}

func TestSetup_SelfRedirect(t *testing.T) {
	// Create a repo that is both main and worktree
	repo := t.TempDir()
	thrumDir := filepath.Join(repo, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("Failed to create .thrum/: %v", err)
	}

	err := Setup(SetupOptions{
		RepoPath: repo,
		MainRepo: repo,
	})
	if err == nil {
		t.Fatal("Expected error when setting up redirect to self")
	}
	if !strings.Contains(err.Error(), "cannot setup redirect to self") {
		t.Errorf("Expected 'cannot setup redirect to self' error, got: %v", err)
	}
}

func TestSetup_Idempotent(t *testing.T) {
	// Create main repo with .thrum/
	mainRepo := t.TempDir()
	mainThrumDir := filepath.Join(mainRepo, ".thrum")
	if err := os.MkdirAll(mainThrumDir, 0750); err != nil {
		t.Fatalf("Failed to create main .thrum/: %v", err)
	}

	worktree := t.TempDir()

	opts := SetupOptions{
		RepoPath: worktree,
		MainRepo: mainRepo,
	}

	// First setup
	if err := Setup(opts); err != nil {
		t.Fatalf("First Setup failed: %v", err)
	}

	// Read redirect content after first setup
	redirectPath := filepath.Join(worktree, ".thrum", "redirect")
	firstContent, err := os.ReadFile(redirectPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Failed to read redirect after first setup: %v", err)
	}

	// Second setup should not fail
	if err := Setup(opts); err != nil {
		t.Fatalf("Second Setup failed: %v", err)
	}

	// Read redirect content after second setup
	secondContent, err := os.ReadFile(redirectPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Failed to read redirect after second setup: %v", err)
	}

	// Content should be identical
	if string(firstContent) != string(secondContent) {
		t.Errorf("Setup is not idempotent: first=%q, second=%q", firstContent, secondContent)
	}

	// Identities dir should still exist
	identitiesDir := filepath.Join(worktree, ".thrum", "identities")
	if _, err := os.Stat(identitiesDir); os.IsNotExist(err) {
		t.Error("Identities dir missing after second setup")
	}
}
