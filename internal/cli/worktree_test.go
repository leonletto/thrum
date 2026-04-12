package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ensureWorktreeRedirects tests ---

func TestEnsureWorktreeRedirects_CreatesThrum(t *testing.T) {
	mainRepo := t.TempDir()
	mainThrumDir := filepath.Join(mainRepo, ".thrum")
	if err := os.MkdirAll(mainThrumDir, 0750); err != nil {
		t.Fatal(err)
	}

	worktree := t.TempDir()
	// Write .git file pointing to main repo (simulates git worktree)
	if err := os.WriteFile(filepath.Join(worktree, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := EnsureWorktreeRedirects(worktree, mainRepo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify .thrum/redirect
	redirect, err := os.ReadFile(filepath.Join(worktree, ".thrum", "redirect"))
	if err != nil {
		t.Fatal("redirect file not created")
	}
	if string(redirect) != mainThrumDir+"\n" {
		t.Errorf("redirect = %q, want %q", string(redirect), mainThrumDir+"\n")
	}

	// Verify .thrum/identities/ exists
	if _, err := os.Stat(filepath.Join(worktree, ".thrum", "identities")); err != nil {
		t.Error("identities dir not created")
	}

	// Verify .thrum/context/ exists
	if _, err := os.Stat(filepath.Join(worktree, ".thrum", "context")); err != nil {
		t.Error("context dir not created")
	}
}

func TestEnsureWorktreeRedirects_CreatesBeads(t *testing.T) {
	mainRepo := t.TempDir()
	os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0750)
	os.MkdirAll(filepath.Join(mainRepo, ".beads"), 0750)

	worktree := t.TempDir()
	os.WriteFile(filepath.Join(worktree, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0644)

	err := EnsureWorktreeRedirects(worktree, mainRepo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	redirect, err := os.ReadFile(filepath.Join(worktree, ".beads", "redirect"))
	if err != nil {
		t.Fatal("beads redirect not created")
	}
	expected := filepath.Join(mainRepo, ".beads") + "\n"
	if string(redirect) != expected {
		t.Errorf("beads redirect = %q, want %q", string(redirect), expected)
	}
}

func TestEnsureWorktreeRedirects_SkipsBeadsWhenNotPresent(t *testing.T) {
	mainRepo := t.TempDir()
	os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0750)
	// No .beads/ in main repo

	worktree := t.TempDir()
	os.WriteFile(filepath.Join(worktree, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0644)

	err := EnsureWorktreeRedirects(worktree, mainRepo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(worktree, ".beads")); !os.IsNotExist(err) {
		t.Error(".beads should not exist when main repo has no .beads/")
	}
}

func TestEnsureWorktreeRedirects_FixesBrokenRedirect(t *testing.T) {
	mainRepo := t.TempDir()
	os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0750)

	worktree := t.TempDir()
	os.WriteFile(filepath.Join(worktree, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0644)

	// Write a broken redirect
	os.MkdirAll(filepath.Join(worktree, ".thrum"), 0750)
	os.WriteFile(filepath.Join(worktree, ".thrum", "redirect"),
		[]byte("/nonexistent/path/.thrum\n"), 0644)

	err := EnsureWorktreeRedirects(worktree, mainRepo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be fixed
	redirect, _ := os.ReadFile(filepath.Join(worktree, ".thrum", "redirect"))
	expected := filepath.Join(mainRepo, ".thrum") + "\n"
	if string(redirect) != expected {
		t.Errorf("redirect not fixed: got %q, want %q", string(redirect), expected)
	}
}

func TestEnsureWorktreeRedirects_ErrorNoMainThrum(t *testing.T) {
	mainRepo := t.TempDir()
	// No .thrum/ in main repo

	worktree := t.TempDir()
	os.WriteFile(filepath.Join(worktree, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0644)

	err := EnsureWorktreeRedirects(worktree, mainRepo)
	if err == nil {
		t.Fatal("expected error when main repo has no .thrum/")
	}
}

func TestEnsureWorktreeRedirects_ErrorWorktreeNotFound(t *testing.T) {
	err := EnsureWorktreeRedirects("/nonexistent/path", t.TempDir())
	if err == nil {
		t.Fatal("expected error for nonexistent worktree")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureWorktreeRedirects_Idempotent(t *testing.T) {
	mainRepo := t.TempDir()
	os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0750)

	worktree := t.TempDir()
	os.WriteFile(filepath.Join(worktree, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0644)

	// Run twice
	if err := EnsureWorktreeRedirects(worktree, mainRepo); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := EnsureWorktreeRedirects(worktree, mainRepo); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

// --- enforceOneIdentity tests ---

func TestEnforceOneIdentity_DeletesOthers(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	os.MkdirAll(idDir, 0750)

	// Create two identity files
	os.WriteFile(filepath.Join(idDir, "old_agent.json"), []byte(`{"agent":{"name":"old_agent"}}`), 0644)
	os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{"agent":{"name":"new_agent"}}`), 0644)

	deleted := EnforceOneIdentity(dir, "new_agent")

	// old_agent.json should be deleted
	if _, err := os.Stat(filepath.Join(idDir, "old_agent.json")); !os.IsNotExist(err) {
		t.Error("old_agent.json should be deleted")
	}

	// new_agent.json should survive
	if _, err := os.Stat(filepath.Join(idDir, "new_agent.json")); err != nil {
		t.Error("new_agent.json should survive")
	}

	if len(deleted) != 1 || deleted[0] != "old_agent" {
		t.Errorf("deleted = %v, want [old_agent]", deleted)
	}
}

func TestEnforceOneIdentity_PreservesContext(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	ctxDir := filepath.Join(dir, ".thrum", "context")
	os.MkdirAll(idDir, 0750)
	os.MkdirAll(ctxDir, 0750)

	os.WriteFile(filepath.Join(idDir, "old_agent.json"), []byte(`{}`), 0644)
	os.WriteFile(filepath.Join(ctxDir, "old_agent.md"), []byte("# Notes"), 0644)

	EnforceOneIdentity(dir, "new_agent")

	// Identity deleted
	if _, err := os.Stat(filepath.Join(idDir, "old_agent.json")); !os.IsNotExist(err) {
		t.Error("old identity should be deleted")
	}

	// Context preserved
	if _, err := os.Stat(filepath.Join(ctxDir, "old_agent.md")); err != nil {
		t.Error("context file should be preserved")
	}
}

func TestEnforceOneIdentity_NoIdentitiesDir(t *testing.T) {
	dir := t.TempDir()
	// No .thrum/identities/ — should not panic
	deleted := EnforceOneIdentity(dir, "agent")
	if len(deleted) != 0 {
		t.Error("expected no deletions")
	}
}

// --- buildQuickstartCmd tests ---

func TestBuildQuickstartCmd_Basic(t *testing.T) {
	cmd := BuildQuickstartCmd("impl_api", "implementer", "api", "", "")
	expected := "thrum quickstart --name 'impl_api' --role 'implementer' --module 'api' --force"
	if cmd != expected {
		t.Errorf("got %q, want %q", cmd, expected)
	}
}

func TestBuildQuickstartCmd_WithIntent(t *testing.T) {
	cmd := BuildQuickstartCmd("impl_api", "implementer", "api", "Build the API endpoints", "claude")
	if !strings.Contains(cmd, "--intent 'Build the API endpoints'") {
		t.Errorf("missing intent: %s", cmd)
	}
	if !strings.Contains(cmd, "--runtime 'claude'") {
		t.Errorf("missing runtime: %s", cmd)
	}
}

func TestBuildQuickstartCmd_QuotesSpecialChars(t *testing.T) {
	cmd := BuildQuickstartCmd("impl_api", "implementer", "api", "Build API; handle auth", "")
	// Semicolons in intent must be safely quoted
	if !strings.Contains(cmd, "'Build API; handle auth'") {
		t.Errorf("intent not safely quoted: %s", cmd)
	}
}

func TestBuildQuickstartCmd_EscapesSingleQuoteInIntent(t *testing.T) {
	cmd := BuildQuickstartCmd("impl_api", "implementer", "api", "Build API's auth", "")
	// Single quotes escaped via '\'' idiom
	if strings.Contains(cmd, "'Build API's auth'") {
		t.Errorf("unescaped single quote would break shell: %s", cmd)
	}
	// Should contain the escaped form
	if !strings.Contains(cmd, `'Build API'\''s auth'`) {
		t.Errorf("expected escaped single quote: %s", cmd)
	}
}
