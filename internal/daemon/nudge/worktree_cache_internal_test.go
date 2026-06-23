package nudge

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// resetWorktreeCache swaps in a fresh cache + a counting worktreePathsFn seam
// and restores the package globals (worktreePathsFn, timeNowFn, wtCache) on
// cleanup. Internal-test access (package nudge) is why this file isn't in
// package nudge_test. Returns a pointer to the invocation counter so tests can
// assert how many real `git worktree list` execs the burst collapsed to.
func resetWorktreeCache(t *testing.T, paths []string) *int64 {
	t.Helper()
	var calls int64
	origFn := worktreePathsFn
	origNow := timeNowFn
	origCache := wtCache
	worktreePathsFn = func(_ context.Context, _ string) []string {
		atomic.AddInt64(&calls, 1)
		return paths
	}
	wtCache = newWorktreePathsCache()
	t.Cleanup(func() {
		worktreePathsFn = origFn
		timeNowFn = origNow
		wtCache = origCache
	})
	return &calls
}

// TestCachedWorktreePaths_BurstCollapsesToOneGitCall is the load-bearing
// assertion for thrum-yz0a: a burst of concurrent callers must collapse to a
// SINGLE underlying `git worktree list` exec via the single-flight + TTL cache,
// not one exec per caller (the storm amplifier). Mirrors pull_gate.go's
// single-flight discipline.
func TestCachedWorktreePaths_BurstCollapsesToOneGitCall(t *testing.T) {
	calls := resetWorktreeCache(t, []string{"/repo", "/repo/.wt/a"})

	const burst = 200
	var wg sync.WaitGroup
	wg.Add(burst)
	start := make(chan struct{})
	for i := 0; i < burst; i++ {
		go func() {
			defer wg.Done()
			<-start // release all goroutines together to maximise concurrency
			got := cachedWorktreePaths("/repo")
			require.Equal(t, []string{"/repo", "/repo/.wt/a"}, got)
		}()
	}
	close(start)
	wg.Wait()

	require.Equal(t, int64(1), atomic.LoadInt64(calls),
		"a concurrent burst of %d callers must single-flight to exactly 1 git-list exec", burst)
}

// TestCachedWorktreePaths_SequentialReusesWithinTTL pins that sequential calls
// inside the TTL window reuse the cached value (no refetch), and that crossing
// the TTL boundary triggers exactly one refetch.
func TestCachedWorktreePaths_SequentialReusesWithinTTL(t *testing.T) {
	calls := resetWorktreeCache(t, []string{"/repo"})

	now := time.Unix(1000, 0)
	timeNowFn = func() time.Time { return now }

	for i := 0; i < 10; i++ {
		cachedWorktreePaths("/repo")
	}
	require.Equal(t, int64(1), atomic.LoadInt64(calls), "10 calls within the TTL window must hit cache after the first")

	// Advance past the TTL: exactly one refetch.
	now = now.Add(worktreePathsTTL + time.Second)
	for i := 0; i < 10; i++ {
		cachedWorktreePaths("/repo")
	}
	require.Equal(t, int64(2), atomic.LoadInt64(calls), "crossing the TTL boundary must trigger exactly one refetch")
}

// TestCachedWorktreePaths_DistinctReposDoNotShare ensures the cache keys on
// repoDir — two different repos each get their own fetch (no cross-key bleed).
func TestCachedWorktreePaths_DistinctReposDoNotShare(t *testing.T) {
	calls := resetWorktreeCache(t, []string{"/x"})

	now := time.Unix(2000, 0)
	timeNowFn = func() time.Time { return now }

	cachedWorktreePaths("/repoA")
	cachedWorktreePaths("/repoB")
	require.Equal(t, int64(2), atomic.LoadInt64(calls), "distinct repoDir keys must each fetch once")
}

// TestHasLocalIdentity_BurstBoundedGitCalls exercises the cache through the
// real public hot-path entry point. Recipients absent from the main identities
// dir force the worktree walk (the git-subprocess site at nudge.go:187). A
// burst of HasLocalIdentity calls across many recipients must collapse the
// git-list execs to a small constant, not one-per-recipient.
func TestHasLocalIdentity_BurstBoundedGitCalls(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	require.NoError(t, os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750))
	repoDir := filepath.Dir(thrumDir)

	// Cache returns a worktree list that contains only the repoDir itself, so
	// the walk finds no extra identity dirs — every recipient is "not local",
	// which is fine; we are counting git-list execs, not membership.
	calls := resetWorktreeCache(t, []string{repoDir})

	now := time.Unix(3000, 0)
	timeNowFn = func() time.Time { return now }

	for i := 0; i < 100; i++ {
		nameI := "ghost" + string(rune('a'+i%26))
		require.False(t, HasLocalIdentity(thrumDir, nameI))
	}
	require.Equal(t, int64(1), atomic.LoadInt64(calls),
		"100 HasLocalIdentity calls within one TTL window must share a single git-list exec")
}
