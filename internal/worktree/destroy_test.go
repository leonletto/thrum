package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDestroy_HappyPath(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx := context.Background()

	// Set up a worktree via Create so we have something real to
	// destroy.
	r, err := Create(ctx, CreateOpts{
		RepoPath: repoPath, BasePath: basePath,
		AgentName: "x", JobID: "j", WakeTimestamp: 1,
		Persistent: false, BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Destroy(ctx, DestroyOpts{
		RepoPath:     repoPath,
		WorktreePath: r.Path,
		Branch:       r.Branch,
		Force:        true,
	})
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Worktree directory gone.
	if _, err := os.Stat(r.Path); !os.IsNotExist(err) {
		t.Errorf("worktree path: got err=%v, want IsNotExist", err)
	}
	// Branch deleted.
	out, gerr := exec.Command("git", "-C", repoPath, "branch", "--list", r.Branch).CombinedOutput()
	if gerr != nil {
		t.Fatalf("git branch --list: %v\n%s", gerr, out)
	}
	if len(strings.TrimSpace(string(out))) != 0 {
		t.Errorf("branch still present: %s", out)
	}
}

func TestDestroy_Idempotent(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx := context.Background()

	// Destroy a path that doesn't exist → returns nil.
	res, err := Destroy(ctx, DestroyOpts{
		RepoPath:     repoPath,
		WorktreePath: filepath.Join(basePath, "never-existed"),
		Branch:       "agent/x/job-j-1",
		Force:        true,
	})
	if err != nil {
		t.Errorf("Destroy on missing path: got err=%v, want nil (idempotent)", err)
	}
	if res == nil {
		t.Error("Destroy on missing path: got nil result, want zero-value DestroyResult")
	} else if res.BranchDeleted {
		t.Error("BranchDeleted: got true, want false (path absent → branch untouched)")
	}
}

func TestDestroy_BranchStaysWhenBlank(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx := context.Background()

	r, err := Create(ctx, CreateOpts{
		RepoPath: repoPath, BasePath: basePath,
		AgentName: "x", JobID: "j", WakeTimestamp: 1,
		Persistent: false, BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Destroy(ctx, DestroyOpts{
		RepoPath:     repoPath,
		WorktreePath: r.Path,
		Branch:       "", // intentionally blank
		Force:        true,
	})
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Worktree gone, branch INTACT (cobra-default behavior).
	out, gerr := exec.Command("git", "-C", repoPath, "branch", "--list", r.Branch).CombinedOutput()
	if gerr != nil {
		t.Fatalf("git branch --list: %v\n%s", gerr, out)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		t.Errorf("branch absent: got empty list, want branch present (Branch field was blank)")
	}
}

func TestDestroy_BranchDeleteFailureNonFatal(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx := context.Background()

	r, err := Create(ctx, CreateOpts{
		RepoPath: repoPath, BasePath: basePath,
		AgentName: "x", JobID: "j", WakeTimestamp: 1,
		Persistent: false, BaseBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Pass a Branch field that names a branch that does NOT exist.
	// `git branch -D` will fail with "branch not found"; Destroy
	// must log the failure but still return nil (best-effort
	// branch deletion per spec §3.2).
	_, err = Destroy(ctx, DestroyOpts{
		RepoPath:     repoPath,
		WorktreePath: r.Path,
		Branch:       "agent/nonexistent/job-zzz-9", // bogus branch
		Force:        true,
	})
	// Worktree-remove succeeded; the bogus branch -D failed but
	// Destroy still returns nil.
	if err != nil {
		t.Errorf("Destroy with bogus Branch: got err=%v, want nil (best-effort)", err)
	}
	// Worktree path is gone (the part that DID work).
	if _, statErr := os.Stat(r.Path); !os.IsNotExist(statErr) {
		t.Errorf("worktree path: got err=%v, want IsNotExist", statErr)
	}
}

func TestDestroy_NotAWorktree(t *testing.T) {
	repoPath, basePath := newTestRepo(t)
	ctx := context.Background()

	// Create a plain directory at the would-be worktree path —
	// it exists but is NOT a git worktree.
	fakePath := filepath.Join(basePath, "not-a-worktree")
	if err := os.MkdirAll(fakePath, 0750); err != nil {
		t.Fatalf("mkdir fake: %v", err)
	}

	_, err := Destroy(ctx, DestroyOpts{
		RepoPath:     repoPath,
		WorktreePath: fakePath,
		Force:        true,
	})
	if err == nil {
		t.Fatal("Destroy on non-worktree path: got nil err, want git error")
	}
	// The wrapped error should mention git worktree remove.
	if !strings.Contains(err.Error(), "git worktree remove") {
		t.Errorf("err = %v, want wrap with 'git worktree remove'", err)
	}
}

func TestDestroy_Validation(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		opts DestroyOpts
	}{
		{"empty RepoPath", DestroyOpts{WorktreePath: "/w"}},
		{"empty WorktreePath", DestroyOpts{RepoPath: "/r"}},
		{"WorktreePath with .. segment", DestroyOpts{
			RepoPath: "/r", WorktreePath: "/base/../escape",
		}},
		{"WorktreePath relative ..", DestroyOpts{
			RepoPath: "/r", WorktreePath: "../escape",
		}},
		{"WorktreePath equals RepoPath (would delete repo)", DestroyOpts{
			RepoPath: "/r", WorktreePath: "/r",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Destroy(ctx, c.opts)
			if !errors.Is(err, ErrInvalidOpts) {
				t.Errorf("err = %v, want errors.Is(ErrInvalidOpts) true", err)
			}
		})
	}
}
