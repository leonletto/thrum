package cli

import "github.com/leonletto/thrum/internal/worktree"

// EnsureWorktreeRedirects delegates to worktree.EnsureRedirects.
// Kept for backward compatibility with callers in cmd/thrum/main.go.
func EnsureWorktreeRedirects(worktreePath, mainRepo string) error {
	return worktree.EnsureRedirects(worktreePath, mainRepo)
}

// BuildQuickstartCmd delegates to worktree.BuildQuickstartCmd.
func BuildQuickstartCmd(name, role, module, intent, runtime string) string {
	return worktree.BuildQuickstartCmd(name, role, module, intent, runtime)
}
