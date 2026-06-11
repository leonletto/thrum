package daemon

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPullGate_SameKey_SingleFlightWithOneTrailing is the thrum-w78a core:
// N concurrent requests for the same peer must run pulls strictly
// sequentially (never two in flight) and collapse the queued N-1 into
// exactly ONE trailing re-pull — not N-1 re-pulls. Pre-fix, 100 notifies
// from one peer while a pull ran meant up to 100 concurrent full pulls
// against that peer (tonight's receive-side storm shape).
func TestPullGate_SameKey_SingleFlightWithOneTrailing(t *testing.T) {
	g := newPullGate()

	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	var pulls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})

	pull := func() {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		if pulls.Add(1) == 1 {
			close(started)
			<-release // hold the first pull so the others queue behind it
		}
		inFlight.Add(-1)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); g.Do("peerX", pull) }() // takes the flight
	<-started

	// 9 more requests land while the pull is in flight — all must be
	// absorbed into ONE trailing rerun and return immediately.
	for range 9 {
		wg.Add(1)
		go func() { defer wg.Done(); g.Do("peerX", pull) }()
	}
	// The absorbed callers return without running pull; give them a moment.
	time.Sleep(50 * time.Millisecond)
	if got := pulls.Load(); got != 1 {
		t.Fatalf("pulls while flight held = %d, want 1 (absorbed callers must not pull)", got)
	}
	close(release)
	wg.Wait()

	if got := pulls.Load(); got != 2 {
		t.Fatalf("total pulls = %d, want exactly 2 (the flight + ONE trailing rerun, not 9)", got)
	}
	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("max in-flight = %d, want 1 (single-flight)", got)
	}
}

// TestPullGate_DifferentKeys_Parallel pins that the gate is per-peer:
// pulls for different peers run concurrently (no global serialization).
func TestPullGate_DifferentKeys_Parallel(t *testing.T) {
	g := newPullGate()

	var concurrent atomic.Int64
	var sawParallel atomic.Bool
	barrier := make(chan struct{})

	pull := func() {
		if concurrent.Add(1) == 2 {
			sawParallel.Store(true)
			close(barrier)
		}
		<-barrier // both must be inside simultaneously to proceed
		concurrent.Add(-1)
	}

	var wg sync.WaitGroup
	for _, key := range []string{"peerA", "peerB"} {
		wg.Add(1)
		go func(k string) { defer wg.Done(); g.Do(k, pull) }(key)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock — different peers must pull in parallel, not serialize")
	}
	if !sawParallel.Load() {
		t.Fatal("pulls for different peers never overlapped")
	}
}

// TestPullGate_PanicUnlatchesGate is the review-CRITICAL regression: pull()
// touches SQLite/network/file I/O; a panic must NOT leave inFlight latched
// (which would wedge the peer — every later request absorbed forever — the
// moment a recover() lands on the notify-pool worker). The panic must still
// propagate (crash/stack surfaces); the gate must be re-armed after.
func TestPullGate_PanicUnlatchesGate(t *testing.T) {
	g := newPullGate()

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected the pull panic to propagate through Do, not be swallowed")
			}
		}()
		g.Do("peerX", func() { panic("boom in pull") })
	}()

	// The gate must NOT be latched: a second Do for the same peer runs.
	ran := false
	if got := g.Do("peerX", func() { ran = true }); !got {
		t.Fatal("second Do returned absorbed=false — gate stayed latched after the panic (the wedge)")
	}
	if !ran {
		t.Fatal("second pull never executed — peer silently wedged by the prior panic")
	}
}

// TestPullGate_ReArmsAcrossCycles pins the lifecycle: after a flight (and its
// trailing rerun) completes, the next request takes a fresh flight and runs
// immediately — the gate must not stay latched.
func TestPullGate_ReArmsAcrossCycles(t *testing.T) {
	g := newPullGate()
	var pulls atomic.Int64

	g.Do("peerX", func() { pulls.Add(1) })
	g.Do("peerX", func() { pulls.Add(1) })
	g.Do("peerX", func() { pulls.Add(1) })

	if got := pulls.Load(); got != 3 {
		t.Fatalf("sequential idle-state pulls = %d, want 3 (each runs immediately)", got)
	}
}
