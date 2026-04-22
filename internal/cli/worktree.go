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
// file EnsureRedirects actually created for worktreePath. Both the
// thrum line and the beads line are artifact-driven — we stat the
// concrete file before printing, so output stays truthful even if the
// conditions inside EnsureRedirects change later (thrum-ufv5.13,
// review #6: both redirects get the same treatment for consistency).
func PrintRedirectConfirmations(w io.Writer, worktreePath string) {
	if _, err := os.Stat(filepath.Join(worktreePath, ".thrum", "redirect")); err == nil {
		_, _ = fmt.Fprintln(w, "✓ Thrum redirect configured")
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".beads", "redirect")); err == nil {
		_, _ = fmt.Fprintln(w, "✓ Beads redirect configured")
	}
}
