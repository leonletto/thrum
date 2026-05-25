//go:build unix

package peercred

import (
	"sync"
	"time"
)

// thrum-xir.45: peer-credential CWD resolution is the dominant cost in
// every unix-socket RPC's pre-handler latency on macOS, because
// processCWD shells out to /usr/sbin/lsof (~30-300ms wall time depending
// on system load and fork contention). Under sustained gate-style load
// (the release-test framework hammers the daemon with ~108 scenarios
// back-to-back), per-RPC pre-handler latency creeps from ~55ms baseline
// into the 1-2s range and CLI socket-read deadlines start firing,
// surfacing as "tmux.create: i/o timeout" in scenarios 69-75 and 80.
//
// This file caches the (pid → cwd) lookup with a short TTL. A given
// process's CWD changes infrequently in practice; caching it for a few
// seconds eliminates N-1 lsof shell-outs per RPC sequence on the same
// connection (typically refresh + 1-2 actual RPCs per CLI invocation).
// The TTL bounds staleness against the rare cd-mid-session case and
// against PID-recycling after a process exits.
//
// Path B (libproc proc_pidinfo native syscall replacement for the
// underlying processCWDFn) is filed as xir.48 for a follow-up cycle —
// once that lands, this cache layer composes with it: cache hits become
// even cheaper, and cache misses no longer fork a subprocess.

// cwdCacheTTL is the per-entry lifetime. 5s is long enough to coalesce
// the refresh+action RPC pair the CLI typically issues, and short enough
// that operator CD-mid-session converges quickly. Exposed as a var so
// tests can shorten it without sleeping.
var cwdCacheTTL = 5 * time.Second

// cwdCacheEntry holds a single cached CWD-resolution result (cwd, err
// pair from processCWDFn) plus the wall-clock at which the entry becomes
// stale. Negative results (err != nil) are cached too — repeatedly
// shelling out for a PID we already know is unresolvable would defeat
// the cache's purpose.
type cwdCacheEntry struct {
	cwd       string
	err       error
	expiresAt time.Time
}

// cwdCache maps PID → most-recent resolution result. Lifetime is bounded
// by cwdCacheTTL; entries are evicted lazily on the next lookup that
// finds them expired. The daemon's RPC volume is low enough that lazy
// eviction is sufficient; no background sweeper is wired here.
var (
	cwdCacheMu sync.Mutex
	cwdCache   = make(map[int]cwdCacheEntry)
)

// cachedProcessCWD is the resolver's fast-path entry point for
// PID → CWD lookup. On a fresh cache or expired entry it falls through
// to processCWDFn (the platform-specific real resolver); subsequent
// calls within cwdCacheTTL return the cached result without forking.
func cachedProcessCWD(pid int) (string, error) {
	now := time.Now()

	cwdCacheMu.Lock()
	if e, ok := cwdCache[pid]; ok && now.Before(e.expiresAt) {
		cwdCacheMu.Unlock()
		return e.cwd, e.err
	}
	cwdCacheMu.Unlock()

	// Cache miss or expired: call the real resolver. Do NOT hold the
	// cache lock across the subprocess call (lsof can take hundreds of
	// ms; blocking concurrent lookups for unrelated PIDs would defeat
	// the cache's purpose).
	cwd, err := processCWDFn(pid)

	cwdCacheMu.Lock()
	cwdCache[pid] = cwdCacheEntry{
		cwd:       cwd,
		err:       err,
		expiresAt: now.Add(cwdCacheTTL),
	}
	cwdCacheMu.Unlock()

	return cwd, err
}

// clearCWDCacheForTest empties the cache. Test-only entry point —
// production code should rely on TTL-based eviction.
func clearCWDCacheForTest() {
	cwdCacheMu.Lock()
	cwdCache = make(map[int]cwdCacheEntry)
	cwdCacheMu.Unlock()
}
