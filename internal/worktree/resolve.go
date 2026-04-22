package worktree

import (
	"fmt"
	"os"
	"path/filepath"
)

// NormalizeWorktreePath validates and canonicalizes an absolute worktree
// path. Every caller writing identity.worktree must route through this
// helper so the stored value is consistently absolute + cleaned — three
// downstream consumers break silently on a bare-name form: snapshot-save
// JSONL mtime fallback (cmd/thrum/main.go:8005), guard.CWDMatches
// (internal/identity/guard/guard.go:102), and context.WorktreePath
// (internal/context/context.go:509).
//
// Errors on: empty input, relative paths (caller must resolve), and
// non-existent paths (caller must create the worktree before registering).
// The resolver deliberately does NOT consult `git worktree list` — every
// known caller already has the path (identity files live at
// <worktree>/.thrum/identities/<name>.json), so name-to-path lookup is
// not a real use case.
func NormalizeWorktreePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("worktree path is empty")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("worktree path must be absolute, got %q", path)
	}
	cleaned := filepath.Clean(path)
	if _, err := os.Stat(cleaned); err != nil {
		return "", fmt.Errorf("worktree path does not exist: %w", err)
	}
	return cleaned, nil
}
