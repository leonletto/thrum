package worktree

import (
	"fmt"
	"log/slog"
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
	// Resolve symlinks so downstream equality checks (e.g.
	// guard.cwdMatches' filepath.EvalSymlinks pair) don't get tripped up
	// by /tmp → /private/tmp on macOS or similar per-mount aliasing.
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve worktree symlinks: %w", err)
	}
	return resolved, nil
}

// CanonicalizeWorktreePath resolves a worktree path to its post-EvalSymlinks
// canonical form so that the value stored in session_refs matches what the
// peercred resolver sees at match time.
//
// On macOS, /tmp and /var are symlinks to /private/tmp and /private/var.
// The CLI sends `git rev-parse --show-toplevel` output (a shell-logical path)
// while gopsutil.Cwd() returns the vnode path after kernel-level symlink
// expansion. Both-sides EvalSymlinks in the resolver bridge the gap — but
// only when the stored path still exists. If the directory was deleted (or
// the daemon runs on a different host from the writer), EvalSymlinks on the
// stored side fails and the raw-string fallback mismatches the resolved
// candidate path, breaking peercred matching.
//
// Canonicalizing at write time ensures every new session_refs worktree row
// is already in vnode form, so the resolver's EvalSymlinks no-ops cleanly
// instead of diverging on failure.
//
// Fail-open: if EvalSymlinks fails (e.g. path does not exist yet, network
// mount, cross-host artifact), the original path is returned unchanged and
// a Debug log is emitted — no error is surfaced to the caller. This matches
// the behaviour callers had before this helper existed.
func CanonicalizeWorktreePath(path string) string {
	if path == "" {
		return path
	}
	cleaned := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		slog.Debug("worktree.CanonicalizeWorktreePath: EvalSymlinks failed, storing raw path",
			slog.String("path", path),
			slog.String("error", err.Error()))
		return cleaned
	}
	return resolved
}
