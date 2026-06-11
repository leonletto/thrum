package nudge

import (
	"context"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

// worktreePathsFn is the seam for the underlying `git worktree list` fetch.
// Production points at safecmd.WorktreePaths; tests inject a counting fake to
// assert the burst-collapse behaviour.
var worktreePathsFn = safecmd.WorktreePaths

// worktreePathsTTL bounds how long a cached worktree list is reused before a
// refetch. The worktree layout changes only when a worktree is added/removed
// (rare), so a few seconds of staleness is acceptable: a brand-new worktree's
// agent simply falls to the spool/DB backstop until the next refetch window.
const worktreePathsTTL = 3 * time.Second

// worktreePathsCache memoizes safecmd.WorktreePaths per repoDir with a short
// TTL and single-flight (thrum-yz0a). The nudge dispatch hot path resolved
// worktree paths once PER RECIPIENT across both the tmux-nudge goroutines
// (resolveTargetAndRuntime) and the spool loop (HasLocalIdentity) — an
// identical `git worktree list` subprocess every time. Under a message burst
// (1000 msgs x 30 recipients) that fan-out reached tens of thousands of git
// execs. This cache collapses a concurrent burst to ONE git exec per TTL
// window: the first caller takes the flight and runs the fetch; concurrent
// callers wait on the same flight and share its result (mirrors
// internal/daemon/pull_gate.go's single-flight discipline). safecmd.WorktreePaths
// itself stays PURE — only the three nudge.go hot sites route through here;
// cold callers (context.go, identity_scan.go, rpc/tmux.go) want freshness and
// call it directly.
//
// Entries are never deleted: a daemon serves a single repoDir, so the keyset is
// effectively size-1 and stable for the daemon's lifetime (matching pull_gate's
// never-delete rationale).
type worktreePathsCache struct {
	mu     sync.Mutex
	states map[string]*wtEntry
}

type wtEntry struct {
	paths   []string
	expiry  time.Time
	loading bool
	ready   chan struct{} // closed when the in-flight load completes
}

func newWorktreePathsCache() *worktreePathsCache {
	return &worktreePathsCache{states: make(map[string]*wtEntry)}
}

var wtCache = newWorktreePathsCache()

// cachedWorktreePaths returns the worktree paths for repoDir, served from the
// package cache when fresh and single-flighted on a miss.
func cachedWorktreePaths(repoDir string) []string {
	return wtCache.get(repoDir)
}

// get serves a fresh cached value, joins an in-flight load, or becomes the
// loader. Only the loader calls worktreePathsFn; everyone else shares its
// result.
func (c *worktreePathsCache) get(repoDir string) []string {
	c.mu.Lock()
	e, ok := c.states[repoDir]
	if !ok {
		e = &wtEntry{}
		c.states[repoDir] = e
	}

	// Fresh cached value (paths is non-nil only after a completed load —
	// WorktreePaths never returns nil/empty, so nil cleanly means "never
	// loaded").
	if !e.loading && e.paths != nil && timeNowFn().Before(e.expiry) {
		paths := e.paths
		c.mu.Unlock()
		return paths
	}

	// A load is already in flight: wait for it, then return its result.
	if e.loading {
		ready := e.ready
		c.mu.Unlock()
		<-ready
		c.mu.Lock()
		paths := e.paths
		c.mu.Unlock()
		return paths
	}

	// Become the loader.
	e.loading = true
	e.ready = make(chan struct{})
	ready := e.ready
	c.mu.Unlock()

	// Single-flight panic safety (mirrors pull_gate.go): worktreePathsFn shells
	// out to git, so a panic must NOT leave loading latched — that would wedge
	// every future caller for this repo (all absorbed into a flight that never
	// completes). On panic, clear the flight, wake waiters, then re-panic so the
	// crash still surfaces.
	completed := false
	defer func() {
		if completed {
			return
		}
		c.mu.Lock()
		e.loading = false
		close(ready)
		c.mu.Unlock()
	}()

	paths := worktreePathsFn(context.Background(), repoDir)

	c.mu.Lock()
	e.paths = paths
	e.expiry = timeNowFn().Add(worktreePathsTTL)
	e.loading = false
	completed = true
	close(ready)
	c.mu.Unlock()
	return paths
}
