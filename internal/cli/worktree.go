package cli

import "github.com/leonletto/thrum/internal/worktree"

// EnsureWorktreeRedirects delegates to worktree.EnsureRedirects.
// Kept for backward compatibility with callers in cmd/thrum/main.go.
func EnsureWorktreeRedirects(worktreePath, mainRepo string) error {
	return worktree.EnsureRedirects(worktreePath, mainRepo)
}

// EnforceOneIdentity delegates to worktree.EnforceOneIdentity.
func EnforceOneIdentity(worktreePath, newAgentName string) []string {
	return worktree.EnforceOneIdentity(worktreePath, newAgentName)
}

// BuildQuickstartCmd delegates to worktree.BuildQuickstartCmd.
func BuildQuickstartCmd(name, role, module, intent, runtime string) string {
	return worktree.BuildQuickstartCmd(name, role, module, intent, runtime)
}
