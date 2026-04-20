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

// TestEnforceOneIdentity_QuarantinesOthers — thrum-ajmd. Sibling
// identities are MOVED to .thrum/identities/.quarantine/, not deleted.
// This preserves recourse when EnforceOneIdentity fires incorrectly.
func TestEnforceOneIdentity_QuarantinesOthers(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	os.MkdirAll(idDir, 0750)

	os.WriteFile(filepath.Join(idDir, "old_agent.json"), []byte(`{"agent":{"name":"old_agent"}}`), 0600)
	os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{"agent":{"name":"new_agent"}}`), 0600)

	quarantined := EnforceOneIdentity(dir, "new_agent")

	if _, err := os.Stat(filepath.Join(idDir, "old_agent.json")); !os.IsNotExist(err) {
		t.Error("old_agent.json should no longer be at top level")
	}
	if _, err := os.Stat(filepath.Join(idDir, "new_agent.json")); err != nil {
		t.Error("new_agent.json should survive")
	}
	if len(quarantined) != 1 || quarantined[0] != "old_agent" {
		t.Errorf("quarantined = %v, want [old_agent]", quarantined)
	}

	// Quarantine directory exists and holds a timestamped copy of the
	// original file — not a delete, so recovery is possible.
	qDir := filepath.Join(idDir, ".quarantine")
	entries, err := os.ReadDir(qDir)
	if err != nil {
		t.Fatalf("quarantine dir should exist: %v", err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "old_agent.json.") {
			found = true
			data, _ := os.ReadFile(filepath.Join(qDir, e.Name()))
			if !strings.Contains(string(data), "old_agent") {
				t.Errorf("quarantined file contents missing: %s", data)
			}
		}
	}
	if !found {
		t.Errorf("quarantined file not found in %s (entries: %v)", qDir, entries)
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
		t.Error("old identity should no longer be at top level (it's in .quarantine/)")
	}
	if _, err := os.Stat(filepath.Join(ctxDir, "old_agent.md")); err != nil {
		t.Error("context file should be preserved")
	}
}

func TestEnforceOneIdentity_NoIdentitiesDir(t *testing.T) {
	dir := t.TempDir()
	quarantined := EnforceOneIdentity(dir, "agent")
	if len(quarantined) != 0 {
		t.Error("expected no quarantines")
	}
}

// TestEnforceOneIdentity_MultipleKeepers — thrum-dw06. Variadic keep
// list must preserve every named identity, not just the first. The
// daemon-side enforceWorktreeIdentity hook passes both the newly
// registered agent's name AND the peercred-resolved caller's name so
// neither gets quarantined. Without this, registering a differently
// named agent from an existing agent's cwd (e.g. the E2E harness
// registering short-lived test agents from the coordinator dir)
// quarantines the caller's own identity file.
func TestEnforceOneIdentity_MultipleKeepers(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		t.Fatal(err)
	}

	os.WriteFile(filepath.Join(idDir, "caller.json"), []byte(`{"agent":{"name":"caller"}}`), 0600)
	os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{"agent":{"name":"new_agent"}}`), 0600)
	os.WriteFile(filepath.Join(idDir, "stale.json"), []byte(`{"agent":{"name":"stale"}}`), 0600)

	quarantined := EnforceOneIdentity(dir, "new_agent", "caller")

	// Both named keepers survive.
	if _, err := os.Stat(filepath.Join(idDir, "new_agent.json")); err != nil {
		t.Errorf("new_agent.json must survive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(idDir, "caller.json")); err != nil {
		t.Errorf("caller.json must survive (regression: caller's own identity was quarantined): %v", err)
	}
	// Unkept sibling is quarantined.
	if _, err := os.Stat(filepath.Join(idDir, "stale.json")); !os.IsNotExist(err) {
		t.Errorf("stale.json should be quarantined, still at top level")
	}
	if len(quarantined) != 1 || quarantined[0] != "stale" {
		t.Errorf("quarantined = %v, want [stale]", quarantined)
	}
}

// TestEnforceOneIdentity_EmptyKeeperIgnored — a zero-length keep arg
// (e.g. peercred resolved.AgentID is "" for anonymous callers) must be
// skipped, not matched against unnamed sibling files.
func TestEnforceOneIdentity_EmptyKeeperIgnored(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{}`), 0600)
	os.WriteFile(filepath.Join(idDir, "stale.json"), []byte(`{}`), 0600)

	quarantined := EnforceOneIdentity(dir, "new_agent", "")

	if _, err := os.Stat(filepath.Join(idDir, "stale.json")); !os.IsNotExist(err) {
		t.Errorf("stale.json should be quarantined even with empty second keeper")
	}
	if len(quarantined) != 1 {
		t.Errorf("want 1 quarantined, got %v", quarantined)
	}
}

// TestEnforceOneIdentity_QuarantineSkipped — the .quarantine/ dir
// itself must never be scanned as an identity file. Files already
// inside .quarantine/ stay there.
func TestEnforceOneIdentity_QuarantineSkipped(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	qDir := filepath.Join(idDir, ".quarantine")
	os.MkdirAll(qDir, 0o750)

	os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{}`), 0600)
	os.WriteFile(filepath.Join(qDir, "ancient.json.20260101T000000Z"), []byte(`{}`), 0600)

	quarantined := EnforceOneIdentity(dir, "new_agent")
	if len(quarantined) != 0 {
		t.Errorf("quarantine dir contents must not be re-quarantined, got %v", quarantined)
	}
	if _, err := os.Stat(filepath.Join(qDir, "ancient.json.20260101T000000Z")); err != nil {
		t.Errorf("existing quarantine file must remain: %v", err)
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
