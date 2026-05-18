package agentdispatch

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// InflightTracker counts in-flight agent-side RPCs by agent + method.
// Begin/End are paired wrappers around RPC handler entry/exit;
// Count is consulted by DrainListFilesRPCs at stage-8 teardown.
//
// The interface is narrow on purpose — adapter code in
// internal/daemon/rpc/ owns the Begin/End call pattern around the
// real agent.listFiles / agent.getFile handlers (lands with
// MB-1.S2). agentdispatch consumes only Count + the skip-drain
// short-circuit.
type InflightTracker interface {
	Begin(agentName, method string)
	End(agentName, method string)
	Count(agentName string, methods []string) int
}

// inflightTracker is the production InflightTracker. Begin/End writes
// are amortized O(1); Count is a small fixed-size sum (the caller
// passes a method list of length ≤ 2 in practice).
//
// skipDrain is the feature-detect short-circuit: when the daemon
// confirms agent.listFiles / agent.getFile aren't registered (the
// MB-1.S2 substrate hasn't shipped yet), Count always returns 0 so
// DrainListFilesRPCs settles instantly. Tracker callers don't need
// to know — Begin/End remain no-ops effectively because the agent
// substrate that calls them isn't wired in.
type inflightTracker struct {
	mu        sync.Mutex
	counts    map[string]map[string]int
	skipDrain bool
}

// NewInflightTracker returns a tracker ready for production wiring.
// Skip-drain mode defaults to false; daemon-boot sets it via
// SetSkipDrain after probing the agent.listFiles handler presence.
func NewInflightTracker() *inflightTracker {
	return &inflightTracker{
		counts: make(map[string]map[string]int),
	}
}

// SetSkipDrain toggles the feature-detect short-circuit. Daemon-boot
// calls SetSkipDrain(true) when no agent.listFiles handler is
// registered so stage-8 drain returns immediately rather than
// polling a tracker that will never see Begin/End calls.
//
// Safe to call before the daemon starts handling RPCs; deliberately
// NOT safe to flip mid-flight (would race the polling loop). Daemon
// boot is the only authorized caller.
func (t *inflightTracker) SetSkipDrain(skip bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.skipDrain = skip
}

// Begin records the start of an in-flight RPC for (agentName, method).
// Concurrent-safe; pair with End in a deferred call.
func (t *inflightTracker) Begin(agentName, method string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	per, ok := t.counts[agentName]
	if !ok {
		per = make(map[string]int)
		t.counts[agentName] = per
	}
	per[method]++
}

// End records the completion of an in-flight RPC. Idempotent over
// the zero count: an End without a matching Begin is silently
// clamped at 0 (it would only happen if Begin was skipped during a
// partial-init test fixture or via a future code path that forgets
// to pair calls).
func (t *inflightTracker) End(agentName, method string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	per, ok := t.counts[agentName]
	if !ok {
		return
	}
	if per[method] > 0 {
		per[method]--
	}
}

// Count returns the sum of in-flight RPCs for agentName across the
// supplied methods. When skip-drain mode is set, Count always
// returns 0 — the daemon has confirmed the underlying RPCs aren't
// wired, so the drain has nothing to wait for.
func (t *inflightTracker) Count(agentName string, methods []string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.skipDrain {
		return 0
	}
	per, ok := t.counts[agentName]
	if !ok {
		return 0
	}
	total := 0
	for _, m := range methods {
		total += per[m]
	}
	return total
}

// listFilesMethods enumerates the RPC methods stage-8 drain waits
// for. Kept as a package-level constant so the Drainer and free
// function agree on the watched set.
var listFilesMethods = []string{"agent.listFiles", "agent.getFile"}

// drainPollInterval is the cadence at which DrainListFilesRPCs
// re-checks the tracker. 50ms is fast enough that a quickly-settling
// RPC (the common case) clears the drain almost instantly, slow
// enough that a stuck drain doesn't hot-spin until the grace window
// expires.
const drainPollInterval = 50 * time.Millisecond

// DrainListFilesRPCs blocks until in-flight agent.listFiles +
// agent.getFile RPCs against agentName drop to zero OR the grace
// window expires. Returns true on clean drain, false on grace
// timeout (with a slog.Warn emitted so operators can investigate
// stuck RPCs).
//
// Polls every 50ms; stops as soon as the count reaches zero. When
// tracker is in skip-drain mode (feature-detect off-switch), the
// first Count returns 0 and the loop exits immediately.
//
// The free-function shape mirrors the canonical plan (E6.6 Task 62);
// callers needing the RPCDrainer interface use the Drainer wrapper
// below.
func DrainListFilesRPCs(tracker InflightTracker, agentName string, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if tracker.Count(agentName, listFilesMethods) == 0 {
			return true
		}
		time.Sleep(drainPollInterval)
	}
	// One last check after the deadline — a settle that happens
	// during the final sleep window shouldn't be reported as a
	// timeout. Race-detector-clean because Count itself is locked.
	// Capture the count into a local so the slog.Warn arg can't
	// race-decrement to 0 between the comparison and the log line
	// (which would emit a misleading "in_flight=0" for a false
	// return).
	remaining := tracker.Count(agentName, listFilesMethods)
	if remaining == 0 {
		return true
	}
	slog.Warn("teardown drain: listFiles RPCs still in flight after grace",
		"agent", agentName,
		"grace", grace,
		"in_flight", remaining,
	)
	return false
}

// Drainer satisfies the agentdispatch.RPCDrainer interface by
// adapting DrainListFilesRPCs to the (ctx, target, grace) → error
// shape that stage-8 teardown consumes. Stateless beyond the
// tracker reference — safe to share across concurrent dispatches.
type Drainer struct {
	tracker InflightTracker
}

// NewDrainer wires the tracker into a stage-8 RPCDrainer. The
// caller retains ownership of the tracker so the same instance can
// be wired into the agent-side RPC handlers (Begin/End) as well as
// the dispatcher (Count/drain).
func NewDrainer(tracker InflightTracker) *Drainer {
	return &Drainer{tracker: tracker}
}

// DrainListFiles satisfies RPCDrainer. Returns an error when the
// grace window is exceeded — stage-8 teardown discards the error
// (best-effort cleanup per AC 9.2.9) but operators reading daemon
// logs see the slog.Warn from DrainListFilesRPCs.
//
// ctx is part of the interface contract but intentionally unused:
// teardownGracefully passes context.Background() because cleanup
// must complete even on cancel paths. Honoring ctx here would defeat
// that invariant.
func (d *Drainer) DrainListFiles(_ context.Context, target string, grace time.Duration) error {
	if d == nil || d.tracker == nil {
		// Defensive: shouldn't happen under normal wiring (Deps.Drainer
		// would be nil instead, which scheduled_agent skips at the call
		// site). Belt-and-suspenders so the drain step can't panic.
		return nil
	}
	if DrainListFilesRPCs(d.tracker, target, grace) {
		return nil
	}
	return fmt.Errorf("drain timeout: agent %s has in-flight listFiles RPCs after %v", target, grace)
}

// Compile-time check that *Drainer satisfies the RPCDrainer
// interface — catches signature drift if RPCDrainer changes.
var _ RPCDrainer = (*Drainer)(nil)
