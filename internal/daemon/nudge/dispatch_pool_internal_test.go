package nudge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/stretchr/testify/require"
)

// TestDispatchTmux_BoundsConcurrentRecipientWorkers is the second load-bearing
// assertion for thrum-yz0a: DispatchTmux must NOT spawn one unbounded goroutine
// per recipient. Under a broadcast with many recipients the per-recipient
// fan-out must be capped by a bounded worker pool (dispatchPoolSize), so the
// concurrent tmux-subprocess load it generates stays bounded regardless of
// recipient count or burst depth.
//
// Mechanism: every recipient has a resolvable tmux identity in the MAIN
// identities dir (so resolveTargetAndRuntime returns fast, no git walk — this
// isolates the goroutine-bound test from the worktree cache). The injected
// hasSessionFn is the per-worker observation point: it gauges live concurrency,
// blocks until the test releases it, then returns false (worker stops without
// touching the real quiet gate / tmux).
func TestDispatchTmux_BoundsConcurrentRecipientWorkers(t *testing.T) {
	const recipients = 50
	require.Greater(t, recipients, dispatchPoolSize*2, "test must oversubscribe the pool to prove the bound")

	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	require.NoError(t, os.MkdirAll(identitiesDir, 0o750))

	names := make([]string, recipients)
	for i := 0; i < recipients; i++ {
		name := fmt.Sprintf("agent%02d", i)
		names[i] = name
		id := config.IdentityFile{TmuxSession: fmt.Sprintf("sess%02d:0.0", i)}
		idJSON, err := json.Marshal(id)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(identitiesDir, name+".json"), idJSON, 0o600))
	}

	var cur, maxConc, processed int64
	release := make(chan struct{})

	origHasSession := hasSessionFn
	t.Cleanup(func() { hasSessionFn = origHasSession })
	hasSessionFn = func(string) bool {
		n := atomic.AddInt64(&cur, 1)
		for {
			m := atomic.LoadInt64(&maxConc)
			if n <= m || atomic.CompareAndSwapInt64(&maxConc, m, n) {
				break
			}
		}
		<-release // hold the worker so concurrency is observable
		atomic.AddInt64(&cur, -1)
		atomic.AddInt64(&processed, 1)
		return false // stop the worker here — no real tmux / quiet gate
	}

	// Fire-and-forget: must return immediately even though workers will block.
	DispatchTmux(context.Background(), thrumDir, names, "sender")

	// The pool fills to exactly dispatchPoolSize and the rest queue on the
	// semaphore (they cannot enter hasSessionFn until a slot frees).
	require.Eventually(t, func() bool {
		return atomic.LoadInt64(&cur) >= int64(dispatchPoolSize)
	}, 2*time.Second, 5*time.Millisecond, "pool never saturated to dispatchPoolSize")

	// And it must NEVER exceed the bound while workers are held.
	require.Never(t, func() bool {
		return atomic.LoadInt64(&maxConc) > int64(dispatchPoolSize)
	}, 300*time.Millisecond, 10*time.Millisecond,
		"concurrent recipient workers exceeded dispatchPoolSize (%d) — fan-out is unbounded", dispatchPoolSize)

	// Release the workers and confirm every recipient was still processed.
	close(release)
	require.Eventually(t, func() bool {
		return atomic.LoadInt64(&processed) == int64(recipients)
	}, 2*time.Second, 5*time.Millisecond, "not all recipients were processed after release")

	require.Equal(t, int64(dispatchPoolSize), atomic.LoadInt64(&maxConc),
		"pool should be fully utilised (reach exactly dispatchPoolSize), got %d", atomic.LoadInt64(&maxConc))
}
