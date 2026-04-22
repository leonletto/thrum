package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/leonletto/thrum/internal/worktree"
)

// EnsureWorktreeRedirects delegates to worktree.EnsureRedirects.
// Kept for backward compatibility with callers in cmd/thrum/main.go.
func EnsureWorktreeRedirects(worktreePath, mainRepo string) error {
	return worktree.EnsureRedirects(worktreePath, mainRepo)
}

// BuildQuickstartCmd delegates to worktree.BuildQuickstartCmd.
func BuildQuickstartCmd(name, role, module, intent, runtime string, noAgentPID bool) string {
	return worktree.BuildQuickstartCmd(name, role, module, intent, runtime, noAgentPID)
}

// PrintRedirectConfirmations writes one checkmark line per redirect
// file EnsureRedirects created for worktreePath. The thrum redirect
// is always expected; the beads redirect is conditional on the main
// repo having .beads/ and is reported only when the file is actually
// present on disk. Testable helper for thrum-ufv5.13 — callers in
// cmd/thrum/main.go delegate here so output stays aligned with what
// EnsureRedirects actually wrote.
func PrintRedirectConfirmations(w io.Writer, worktreePath string) {
	_, _ = fmt.Fprintln(w, "✓ Thrum redirect configured")
	if _, err := os.Stat(filepath.Join(worktreePath, ".beads", "redirect")); err == nil {
		_, _ = fmt.Fprintln(w, "✓ Beads redirect configured")
	}
}
