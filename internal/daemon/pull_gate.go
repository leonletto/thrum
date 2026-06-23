package daemon

import "sync"

// pullGate serializes peer pulls per peer with trailing-rerun collapse
// (thrum-w78a). The sync.notify receive path had no per-peer in-flight
// dedup: the handler pool (100) caps TOTAL goroutines, not per-peer, so a
// burst of notifies from one peer spawned up to 100 concurrent full pulls
// against that peer — the receive-side amplifier in the 2026-06-10/11
// storms (the send side is bounded by the oc74 notify coalescer; this is
// its receive-side complement, and together: burst → ≤2 notifies → ≤2
// sequential pulls).
//
// Semantics: a request for an idle peer takes the flight and runs the pull,
// then LOOPS while rerun was set — the single trailing re-pull that
// represents every request absorbed during the flight. A request landing
// while the peer's flight is held sets rerun=true and returns immediately.
// No timers: the trailing pull starts the instant the in-flight one
// finishes, so worst-case freshness lag is one pull duration (vs unbounded
// concurrency before). The trailing pull deliberately runs on the original
// holder's goroutine: on the notify path that keeps the pool slot held
// while re-pulling — natural backpressure.
//
// Entries are never deleted: the peer set is small and stable for a daemon's
// lifetime, and a stale entry costs two booleans.
type pullGate struct {
	mu     sync.Mutex
	states map[string]*pullFlight
}

type pullFlight struct {
	inFlight bool
	rerun    bool
}

func newPullGate() *pullGate {
	return &pullGate{states: make(map[string]*pullFlight)}
}

// Do runs pull under the key's single-flight discipline. Returns true if
// this call ran the pull (including any trailing reruns), false if it was
// absorbed into an already-running flight's trailing rerun.
func (g *pullGate) Do(key string, pull func()) bool {
	g.mu.Lock()
	st, ok := g.states[key]
	if !ok {
		st = &pullFlight{}
		g.states[key] = st
	}
	if st.inFlight {
		st.rerun = true
		g.mu.Unlock()
		return false
	}
	st.inFlight = true
	g.mu.Unlock()

	// thrum-w78a panic safety: pull() does SQLite/network/file I/O, so a
	// panic must NOT leave inFlight latched — that would silently wedge the
	// peer (every subsequent SyncFromPeer absorbed forever) the moment a
	// recover() lands on the notify-pool worker. Mirrors x/sync/singleflight's
	// defer: on panic, re-acquire the lock, clear the flight, then re-panic so
	// the crash/stack still surfaces. The sentinel distinguishes the panic
	// unwind from normal completion (which clears inFlight itself, under lock).
	cleanedUp := false
	defer func() {
		if cleanedUp {
			return
		}
		g.mu.Lock()
		st.inFlight = false
		st.rerun = false
		g.mu.Unlock()
		// Not a normal return → an in-flight panic is unwinding; let it.
	}()

	for {
		pull()
		g.mu.Lock()
		if st.rerun {
			st.rerun = false
			g.mu.Unlock()
			continue
		}
		st.inFlight = false
		cleanedUp = true
		g.mu.Unlock()
		return true
	}
}
