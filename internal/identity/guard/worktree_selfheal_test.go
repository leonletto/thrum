package guard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

// writeIdentityFileWithWorktree is a focused helper for the worktree
// self-heal tests. It writes an identity file with the given Worktree
// field (the value under test) into the given identities dir.
func writeIdentityFileWithWorktree(t *testing.T, idDir, name, worktree string) string {
	t.Helper()
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	id := config.IdentityFile{
		Version: 4,
		RepoID:  "test-repo",
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    name,
			Role:    "implementer",
			Module:  "test",
			Display: name,
		},
		Worktree: worktree,
	}
	b, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(idDir, name+".json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestReconcileDrift_RewritesBareNameWorktree(t *testing.T) {
	repoRoot := t.TempDir()
	idDir := filepath.Join(repoRoot, ".thrum", "identities")
	path := writeIdentityFileWithWorktree(t, idDir, "impl_foo", "thrum") // bare name

	// Load the file as reconcileDrift would see it.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var idFile config.IdentityFile
	if err := json.Unmarshal(b, &idFile); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	cc := &CheckContext{Ctx: context.Background()}
	if err := reconcileDrift(context.Background(), repoRoot, &idFile, cc); err != nil {
		t.Fatalf("reconcileDrift: %v", err)
	}

	// Read back to confirm the self-heal persisted to disk.
	b2, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	var after config.IdentityFile
	if err := json.Unmarshal(b2, &after); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if !filepath.IsAbs(after.Worktree) {
		t.Errorf("expected absolute Worktree, got %q", after.Worktree)
	}
	if after.Worktree != filepath.Clean(repoRoot) {
		t.Errorf("expected Worktree=%q, got %q", filepath.Clean(repoRoot), after.Worktree)
	}
}

func TestReconcileDrift_PreservesAbsoluteWorktree(t *testing.T) {
	repoRoot := t.TempDir()
	idDir := filepath.Join(repoRoot, ".thrum", "identities")
	absPath := filepath.Clean(repoRoot)
	_ = writeIdentityFileWithWorktree(t, idDir, "impl_foo", absPath)

	idFile := config.IdentityFile{
		Version:  4,
		Worktree: absPath, // already absolute
	}
	cc := &CheckContext{Ctx: context.Background()}
	// Nothing to do with an already-absolute Worktree — reconcileDrift
	// may be a no-op or may persist other drift (runtime/branch). The
	// important assertion is that Worktree isn't clobbered.
	_ = reconcileDrift(context.Background(), repoRoot, &idFile, cc)

	if idFile.Worktree != absPath {
		t.Errorf("expected Worktree preserved as %q, got %q", absPath, idFile.Worktree)
	}
}

func TestReconcileDrift_UnstatablePath_WarnsAndPreserves(t *testing.T) {
	// Give reconcileDrift a repoPath that doesn't exist. Self-heal must
	// NOT clobber the stored value; the helper logs and continues.
	missing := filepath.Join(t.TempDir(), "does", "not", "exist")
	idFile := config.IdentityFile{
		Version:  4,
		Worktree: "thrum", // bare name — invites rewrite
	}
	cc := &CheckContext{Ctx: context.Background()}

	_ = reconcileDrift(context.Background(), missing, &idFile, cc)

	// Even though the value is non-absolute, we must NOT rewrite to a
	// non-existent path. Either it's still "thrum" or unchanged
	// depending on whether other drift fired.
	if filepath.IsAbs(idFile.Worktree) {
		t.Errorf("expected Worktree preserved as-is, got absolute %q", idFile.Worktree)
	}
}

func TestReconcileDrift_EmptyWorktree_NotChanged(t *testing.T) {
	// Empty Worktree field must not trigger the self-heal (pre-quickstart
	// state — caller hasn't told us the path yet).
	repoRoot := t.TempDir()
	idFile := config.IdentityFile{Version: 4, Worktree: ""}
	cc := &CheckContext{Ctx: context.Background()}

	_ = reconcileDrift(context.Background(), repoRoot, &idFile, cc)

	if idFile.Worktree != "" {
		t.Errorf("expected empty Worktree preserved, got %q", idFile.Worktree)
	}
}
