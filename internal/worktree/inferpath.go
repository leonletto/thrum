package worktree

import (
	"os"
	"path/filepath"
)

// InferBasePath returns the conventional worktree base path for a
// repo: $HOME/.thrum/worktrees/<project>. Returned whether or not
// the path exists yet. Returns the empty string when $HOME cannot
// be resolved. Spec §3.3 — migrated from
// cmd/thrum/main.go:inferWorktreeBasePath (deleted as part of
// non7.4).
func InferBasePath(repoPath string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	projectName := filepath.Base(repoPath)
	return filepath.Join(home, ".thrum", "worktrees", projectName)
}
