package rpc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// newAgentHandlerAt constructs an AgentHandler whose state.RepoPath()
// points at repoPath (a real git repo created by the caller). Cleanup
// is registered so the DB file is closed at test end.
func newAgentHandlerAt(t *testing.T, repoPath string) *AgentHandler {
	t.Helper()
	thrumDir := filepath.Join(repoPath, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_thrum_8bza_test", "")
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewAgentHandler(st)
}

// TestResolveWorktreePath covers thrum-8bza: resolveWorktreePath must
// accept both legacy bare-name inputs and post-thrum-x6e8.2 absolute-
// path inputs. Without this, `thrum tmux restart` fails on any identity
// file written post-nu16 because its Worktree field is a path but the
// git-worktree-list matcher compares against basename.
func TestResolveWorktreePath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Create a real git repo so the legacy-name fallback (git worktree
	// list) has something to match.
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}

	ctx := context.Background()

	t.Run("absolute_path_exists_returns_path", func(t *testing.T) {
		got := resolveWorktreePath(ctx, repo, repo)
		if got == "" {
			t.Fatalf("expected non-empty result for existing abs path")
		}
		// macOS may return /private/var/... for /var/...; resolve both
		// and compare.
		gotResolved, _ := filepath.EvalSymlinks(got)
		wantResolved, _ := filepath.EvalSymlinks(repo)
		if filepath.Clean(gotResolved) != filepath.Clean(wantResolved) &&
			filepath.Clean(got) != filepath.Clean(repo) {
			t.Errorf("expected %q or %q, got %q", repo, wantResolved, got)
		}
	})

	t.Run("absolute_path_missing_returns_empty", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		if got := resolveWorktreePath(ctx, repo, missing); got != "" {
			t.Errorf("expected empty for missing abs path, got %q", got)
		}
	})

	t.Run("legacy_basename_match_returns_path", func(t *testing.T) {
		// Legacy-name path: basename(repo) == stored → return repo path.
		name := filepath.Base(repo)
		got := resolveWorktreePath(ctx, repo, name)
		// git worktree list on a bare repo without any added worktrees
		// lists the main repo itself. The resolver should find it.
		if got == "" {
			t.Fatalf("expected legacy basename match to resolve, got empty (name=%q, repo=%q)", name, repo)
		}
	})

	t.Run("legacy_basename_nomatch_returns_empty", func(t *testing.T) {
		if got := resolveWorktreePath(ctx, repo, "completely-unknown-worktree-xxx"); got != "" {
			t.Errorf("expected empty for unmatched legacy name, got %q", got)
		}
	})

	t.Run("empty_input_returns_empty", func(t *testing.T) {
		if got := resolveWorktreePath(ctx, repo, ""); got != "" {
			t.Errorf("expected empty for empty input, got %q", got)
		}
	})
}

// TestWorktreeExists_PathAware covers thrum-8bza for
// AgentHandler.worktreeExists: must accept both bare name and absolute
// path. Without this, orphan-agent detection (agent.cleanup) silently
// mis-flags live worktrees whose identity files store absolute paths.
func TestWorktreeExists_PathAware(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}

	// worktreeExists needs a state with RepoPath; it's daemon-side, so
	// we wire through the daemon's state.State. For focused unit
	// testing we use a nil-state handler only at the shape-level.
	// The method only calls h.state.RepoPath() — construct via the
	// same newAgentHandlerForTest pattern other tests in this package
	// use. Since there isn't one, skip nil-state branches and test
	// the logic via a focused helper.
	//
	// Simpler: test the behavior by direct filesystem assertions via
	// a small handler wrapper — but that forks the logic. Instead:
	// rely on git-repo-in-tempdir + real state setup from existing
	// testhelpers. Use the same scaffolding as TestTmuxHandler_HandleCreate.
	t.Run("absolute_path_exists_returns_true", func(t *testing.T) {
		h := newAgentHandlerAt(t, repo)
		if !h.worktreeExists(context.Background(), repo) {
			t.Errorf("expected true for existing absolute path %q", repo)
		}
	})

	t.Run("absolute_path_missing_returns_false", func(t *testing.T) {
		h := newAgentHandlerAt(t, repo)
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		if h.worktreeExists(context.Background(), missing) {
			t.Errorf("expected false for missing absolute path %q", missing)
		}
	})

	t.Run("legacy_basename_match_returns_true", func(t *testing.T) {
		h := newAgentHandlerAt(t, repo)
		if !h.worktreeExists(context.Background(), filepath.Base(repo)) {
			t.Errorf("expected true for legacy basename %q", filepath.Base(repo))
		}
	})

	t.Run("legacy_basename_nomatch_returns_false", func(t *testing.T) {
		h := newAgentHandlerAt(t, repo)
		if h.worktreeExists(context.Background(), "completely-unknown-xxx") {
			t.Errorf("expected false for unmatched legacy name")
		}
	})

	t.Run("empty_input_returns_false", func(t *testing.T) {
		h := newAgentHandlerAt(t, repo)
		if h.worktreeExists(context.Background(), "") {
			t.Errorf("expected false for empty input")
		}
	})
}

