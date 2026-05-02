package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

func TestDefaultIntent(t *testing.T) {
	tests := []struct {
		role     string
		repo     string
		expected string
	}{
		{"coordinator", "thrum", "Coordinate agents and tasks in thrum"},
		{"implementer", "thrum", "Implement features and fixes in thrum"},
		{"reviewer", "myapp", "Review code and PRs in myapp"},
		{"planner", "thrum", "Plan architecture and design in thrum"},
		{"tester", "thrum", "Test and validate changes in thrum"},
		{"unknown_role", "thrum", "Working in thrum"},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := DefaultIntent(tt.role, tt.repo)
			if got != tt.expected {
				t.Errorf("DefaultIntent(%q, %q) = %q, want %q", tt.role, tt.repo, got, tt.expected)
			}
		})
	}
}

func TestAutoDisplay(t *testing.T) {
	tests := []struct {
		role, module, expected string
	}{
		{"coordinator", "main", "Coordinator (main)"},
		{"implementer", "auth", "Implementer (auth)"},
		{"coordinator", "", "Coordinator"},
		{"", "main", ""},
	}
	for _, tt := range tests {
		t.Run(tt.role+"_"+tt.module, func(t *testing.T) {
			got := AutoDisplay(tt.role, tt.module)
			if got != tt.expected {
				t.Errorf("AutoDisplay(%q, %q) = %q, want %q", tt.role, tt.module, got, tt.expected)
			}
		})
	}
}

func TestGetRepoName(t *testing.T) {
	got := GetRepoName("/nonexistent/path")
	if got != "unknown" {
		t.Logf("GetRepoName for non-repo: %q", got)
	}
}

func TestGetCurrentBranch(t *testing.T) {
	got := GetCurrentBranch("/nonexistent/path")
	if got != "main" {
		t.Errorf("GetCurrentBranch fallback = %q, want %q", got, "main")
	}
}

func TestGetRepoID(t *testing.T) {
	got := GetRepoID("/nonexistent/path")
	if got != "" {
		t.Logf("GetRepoID for non-repo: %q", got)
	}
}

func TestDefaultModule_PrefersRemoteHEAD(t *testing.T) {
	repo := setupGitRepoWithRemoteHEAD(t, "origin", "master")
	got := DefaultModule(repo)
	if got != "master" {
		t.Errorf("DefaultModule = %q, want %q", got, "master")
	}
}

func TestDefaultModule_FallsBackToLocalHEAD(t *testing.T) {
	repo := setupGitRepoNoRemote(t, "develop")
	got := DefaultModule(repo)
	if got != "develop" {
		t.Errorf("DefaultModule = %q, want %q", got, "develop")
	}
}

func TestDefaultModule_FallsBackToMain(t *testing.T) {
	repo := setupBareRepoNoCommits(t)
	got := DefaultModule(repo)
	if got != "main" {
		t.Errorf("DefaultModule = %q, want %q", got, "main")
	}
}

// setupGitRepoWithRemoteHEAD creates a repo whose origin remote is a bare repo
// with an initial commit on `branch`, and refs/remotes/origin/HEAD points to
// origin/<branch>.
func setupGitRepoWithRemoteHEAD(t *testing.T, remote, branch string) string {
	t.Helper()
	ctx := context.Background()

	bare := filepath.Join(t.TempDir(), "remote.git")
	if _, err := safecmd.Git(ctx, "", "init", "--bare", "-b", branch, bare); err != nil {
		t.Fatalf("init bare: %v", err)
	}

	repo := t.TempDir()
	if _, err := safecmd.Git(ctx, "", "init", "-b", branch, repo); err != nil {
		t.Fatalf("init repo: %v", err)
	}
	writeFile(t, filepath.Join(repo, "README.md"), "# x\n")
	if _, err := safecmd.Git(ctx, repo, "add", "README.md"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := safecmd.Git(ctx, repo, "commit", "-m", "init"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := safecmd.Git(ctx, repo, "remote", "add", remote, bare); err != nil {
		t.Fatalf("remote add: %v", err)
	}
	if _, err := safecmd.Git(ctx, repo, "push", "-u", remote, branch); err != nil {
		t.Fatalf("push: %v", err)
	}
	// Set origin/HEAD to track the remote default branch.
	if _, err := safecmd.Git(ctx, repo, "remote", "set-head", remote, branch); err != nil {
		t.Fatalf("set-head: %v", err)
	}
	return repo
}

// setupGitRepoNoRemote creates a repo on `branch` with one commit and no
// remote configured.
func setupGitRepoNoRemote(t *testing.T, branch string) string {
	t.Helper()
	ctx := context.Background()
	repo := t.TempDir()
	if _, err := safecmd.Git(ctx, "", "init", "-b", branch, repo); err != nil {
		t.Fatalf("init: %v", err)
	}
	writeFile(t, filepath.Join(repo, "README.md"), "# x\n")
	if _, err := safecmd.Git(ctx, repo, "add", "README.md"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := safecmd.Git(ctx, repo, "commit", "-m", "init"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return repo
}

// setupBareRepoNoCommits creates a fresh repo with no commits — neither
// origin/HEAD nor local HEAD resolves to a branch via symbolic-ref --short.
func setupBareRepoNoCommits(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	cmd := exec.Command("git", "init", repo)
	if err := cmd.Run(); err != nil {
		t.Fatalf("init: %v", err)
	}
	return repo
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
