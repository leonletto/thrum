package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInferBasePath_HomeDerived(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no $HOME; cannot exercise default path")
	}
	repoPath := "/some/path/my-repo"
	got := InferBasePath(repoPath)
	want := filepath.Join(home, ".thrum", "worktrees", "my-repo")
	if got != want {
		t.Errorf("InferBasePath(%q): got %q, want %q", repoPath, got, want)
	}
}

func TestInferBasePath_TrailingSlash(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no $HOME")
	}
	repoPath := "/some/path/my-repo/"
	got := InferBasePath(repoPath)
	// filepath.Base strips trailing slash; project name is "my-repo".
	want := filepath.Join(home, ".thrum", "worktrees", "my-repo")
	if got != want {
		t.Errorf("InferBasePath(%q): got %q, want %q", repoPath, got, want)
	}
}
