package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- EnsureRedirects tests ---

func TestEnsureRedirects_CreatesThrum(t *testing.T) {
	mainRepo := t.TempDir()
	mainThrumDir := filepath.Join(mainRepo, ".thrum")
	if err := os.MkdirAll(mainThrumDir, 0750); err != nil {
		t.Fatal(err)
	}

	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	redirect, err := os.ReadFile(filepath.Join(wt, ".thrum", "redirect"))
	if err != nil {
		t.Fatal("redirect file not created")
	}
	if string(redirect) != mainThrumDir+"\n" {
		t.Errorf("redirect = %q, want %q", string(redirect), mainThrumDir+"\n")
	}

	if _, err := os.Stat(filepath.Join(wt, ".thrum", "identities")); err != nil {
		t.Error("identities dir not created")
	}
	if _, err := os.Stat(filepath.Join(wt, ".thrum", "context")); err != nil {
		t.Error("context dir not created")
	}
}

func TestEnsureRedirects_CreatesBeads(t *testing.T) {
	mainRepo := t.TempDir()
	os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0750)
	os.MkdirAll(filepath.Join(mainRepo, ".beads"), 0750)

	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0600)

	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	redirect, err := os.ReadFile(filepath.Join(wt, ".beads", "redirect"))
	if err != nil {
		t.Fatal("beads redirect not created")
	}
	expected := filepath.Join(mainRepo, ".beads") + "\n"
	if string(redirect) != expected {
		t.Errorf("beads redirect = %q, want %q", string(redirect), expected)
	}
}

func TestEnsureRedirects_SkipsBeadsWhenNotPresent(t *testing.T) {
	mainRepo := t.TempDir()
	os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0750)

	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0600)

	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wt, ".beads")); !os.IsNotExist(err) {
		t.Error(".beads should not exist when main repo has no .beads/")
	}
}

func TestEnsureRedirects_FixesBrokenRedirect(t *testing.T) {
	mainRepo := t.TempDir()
	os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0750)

	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0600)

	os.MkdirAll(filepath.Join(wt, ".thrum"), 0750)
	os.WriteFile(filepath.Join(wt, ".thrum", "redirect"), []byte("/nonexistent/path/.thrum\n"), 0600)

	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	redirect, _ := os.ReadFile(filepath.Join(wt, ".thrum", "redirect"))
	expected := filepath.Join(mainRepo, ".thrum") + "\n"
	if string(redirect) != expected {
		t.Errorf("redirect not fixed: got %q, want %q", string(redirect), expected)
	}
}

func TestEnsureRedirects_ErrorNoMainThrum(t *testing.T) {
	mainRepo := t.TempDir()
	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0600)

	err := EnsureRedirects(wt, mainRepo)
	if err == nil {
		t.Fatal("expected error when main repo has no .thrum/")
	}
}

func TestEnsureRedirects_ErrorWorktreeNotFound(t *testing.T) {
	err := EnsureRedirects("/nonexistent/path", t.TempDir())
	if err == nil {
		t.Fatal("expected error for nonexistent worktree")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureRedirects_Idempotent(t *testing.T) {
	mainRepo := t.TempDir()
	os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0750)

	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0600)

	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

// --- EnforceOneIdentity tests ---

func TestEnforceOneIdentity_DeletesOthers(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	os.MkdirAll(idDir, 0750)

	os.WriteFile(filepath.Join(idDir, "old_agent.json"), []byte(`{"agent":{"name":"old_agent"}}`), 0600)
	os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{"agent":{"name":"new_agent"}}`), 0600)

	deleted := EnforceOneIdentity(dir, "new_agent")

	if _, err := os.Stat(filepath.Join(idDir, "old_agent.json")); !os.IsNotExist(err) {
		t.Error("old_agent.json should be deleted")
	}
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

	os.WriteFile(filepath.Join(idDir, "old_agent.json"), []byte(`{}`), 0600)
	os.WriteFile(filepath.Join(ctxDir, "old_agent.md"), []byte("# Notes"), 0600)

	EnforceOneIdentity(dir, "new_agent")

	if _, err := os.Stat(filepath.Join(idDir, "old_agent.json")); !os.IsNotExist(err) {
		t.Error("old identity should be deleted")
	}
	if _, err := os.Stat(filepath.Join(ctxDir, "old_agent.md")); err != nil {
		t.Error("context file should be preserved")
	}
}

func TestEnforceOneIdentity_NoIdentitiesDir(t *testing.T) {
	dir := t.TempDir()
	deleted := EnforceOneIdentity(dir, "agent")
	if len(deleted) != 0 {
		t.Error("expected no deletions")
	}
}

// --- BuildQuickstartCmd tests ---

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
	if !strings.Contains(cmd, "'Build API; handle auth'") {
		t.Errorf("intent not safely quoted: %s", cmd)
	}
}

func TestBuildQuickstartCmd_EscapesSingleQuoteInIntent(t *testing.T) {
	cmd := BuildQuickstartCmd("impl_api", "implementer", "api", "Build API's auth", "")
	if strings.Contains(cmd, "'Build API's auth'") {
		t.Errorf("unescaped single quote would break shell: %s", cmd)
	}
	if !strings.Contains(cmd, `'Build API'\''s auth'`) {
		t.Errorf("expected escaped single quote: %s", cmd)
	}
}
