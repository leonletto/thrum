package daemon

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// fakeClock is a deterministic clock for dialGate tests — no real sleeps.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newTestDialGate builds a dialGate with a fake clock and zero jitter so backoff
// windows are exactly deterministic.
func newTestDialGate() (*dialGate, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	g := newDialGate()
	g.now = clk.now
	g.jitter = func(time.Duration) time.Duration { return 0 }
	return g, clk
}

func TestDialGate_HealthyPeerAlwaysAllowed(t *testing.T) {
	g, _ := newTestDialGate()
	for i := range 3 {
		if !g.claim("peer") {
			t.Fatalf("healthy peer must always be claimable (attempt %d)", i)
		}
	}
}

func TestDialGate_BackoffGrowsExponentially(t *testing.T) {
	g, clk := newTestDialGate()

	// With zero jitter, equal-jitter delay = backoff/2 where
	// backoff = base * 2^(fails-1), capped at dialBackoffCap.
	wantDelays := []time.Duration{
		dialBackoffBase / 2,     // fail 1: backoff 1*base -> /2
		dialBackoffBase,         // fail 2: backoff 2*base -> /2 = base
		dialBackoffBase * 2,     // fail 3: backoff 4*base -> /2 = 2*base
		dialBackoffBase * 2 * 2, // fail 4: backoff 8*base -> /2 = 4*base
	}
	for i, want := range wantDelays {
		fails := i + 1
		if fails >= dialQuarantineThreshold {
			break // stay in the pre-quarantine backoff regime
		}
		g.OnFailure("peer")
		st := g.states["peer"]
		got := st.nextAttempt.Sub(clk.now())
		if got != want {
			t.Errorf("fail %d: nextAttempt delay = %s, want %s", fails, got, want)
		}
		// A claim before the window must be refused; after it, allowed.
		if g.claim("peer") {
			t.Errorf("fail %d: claim must be refused inside backoff window", fails)
		}
		clk.advance(want)
		// reset the clock reference for the next OnFailure by NOT advancing past
		// — re-stat from current now; next OnFailure recomputes from current now.
	}
}

func TestDialGate_QuarantineEngagesAtThreshold(t *testing.T) {
	g, clk := newTestDialGate()

	for range dialQuarantineThreshold {
		g.OnFailure("peer")
	}
	st := g.states["peer"]
	if !st.quarantined {
		t.Fatalf("peer must be quarantined after %d consecutive failures", dialQuarantineThreshold)
	}
	if got := st.nextAttempt.Sub(clk.now()); got != dialProbeInterval {
		t.Errorf("quarantined nextAttempt delay = %s, want probe interval %s", got, dialProbeInterval)
	}
	// No probe admitted until the probe interval elapses.
	clk.advance(dialProbeInterval - time.Second)
	if g.claim("peer") {
		t.Error("quarantined peer must not be claimable before the probe interval elapses")
	}
	// Exactly one probe admitted once the interval elapses.
	clk.advance(time.Second)
	if !g.claim("peer") {
		t.Error("quarantined peer must admit one probe after the probe interval")
	}
	// The claim reserved the next probe window — an immediate second claim refused.
	if g.claim("peer") {
		t.Error("a second concurrent probe within the same window must be refused")
	}
}

// TestDialGate_FlappingPeerRecovers is the leondev-doesn't-die regression lock
// (HARD POINT 1): a quarantined peer that keeps failing is probed forever at the
// low rate (never permanently dead), and re-admits on the FIRST successful dial.
func TestDialGate_FlappingPeerRecovers(t *testing.T) {
	g, clk := newTestDialGate()

	// Drive into quarantine.
	for range dialQuarantineThreshold {
		g.OnFailure("peer")
	}
	if !g.states["peer"].quarantined {
		t.Fatal("setup: expected quarantine")
	}

	// Probe #1: interval elapses -> one probe admitted -> it FAILS -> still
	// quarantined, next probe one interval out (bounded, not permanent death).
	clk.advance(dialProbeInterval)
	if !g.claim("peer") {
		t.Fatal("probe #1 must be admitted after the interval")
	}
	g.OnFailure("peer") // probe failed
	if !g.states["peer"].quarantined {
		t.Fatal("a failed probe must keep the peer quarantined")
	}
	if g.claim("peer") {
		t.Fatal("after a failed probe, no probe until the next interval (must not hammer)")
	}

	// Probe #2: interval elapses -> probe admitted -> it SUCCEEDS -> re-admit.
	clk.advance(dialProbeInterval)
	if !g.claim("peer") {
		t.Fatal("probe #2 must be admitted after the next interval")
	}
	g.OnSuccess("peer") // peer came back

	// Fully re-admitted: claimable again with no residual backoff/quarantine.
	if !g.claim("peer") {
		t.Error("a recovered peer must be immediately claimable (re-admitted)")
	}
	if _, ok := g.states["peer"]; ok {
		t.Error("a recovered peer must carry no failure state")
	}
}

func TestDialGate_SuccessResetsBackoff(t *testing.T) {
	g, _ := newTestDialGate()
	g.OnFailure("peer")
	g.OnFailure("peer")
	if g.states["peer"].consecutiveFails != 2 {
		t.Fatalf("expected 2 consecutive fails")
	}
	g.OnSuccess("peer")
	if _, ok := g.states["peer"]; ok {
		t.Error("OnSuccess must clear failure state")
	}
	if !g.claim("peer") {
		t.Error("peer must be claimable after success")
	}
}

// TestRecordDialOutcome_SentinelDiscrimination is pin (b): a successful dial that
// then hits an APPLY error must NOT count as a dial failure (the peer is
// reachable) — only errDialFailed-wrapped errors quarantine.
func TestRecordDialOutcome_SentinelDiscrimination(t *testing.T) {
	t.Run("dial failure -> OnFailure", func(t *testing.T) {
		g, _ := newTestDialGate()
		err := fmt.Errorf("connect to peer: %w: %w", errDialFailed, errors.New("connection reset by peer"))
		recordDialOutcome(g, "peer", err)
		st, ok := g.states["peer"]
		if !ok || st.consecutiveFails != 1 {
			t.Fatalf("dial failure must record OnFailure (state=%v)", st)
		}
	})

	t.Run("apply error (reachable) -> NOT OnFailure", func(t *testing.T) {
		g, _ := newTestDialGate()
		// Pre-load some failure state to prove a reachable-but-apply-error call
		// RESETS rather than increments.
		g.OnFailure("peer")
		applyErr := fmt.Errorf("apply batch: %w", errors.New("sqlite constraint"))
		recordDialOutcome(g, "peer", applyErr)
		if _, ok := g.states["peer"]; ok {
			t.Error("a successful dial with an apply error must NOT quarantine (must reset state)")
		}
	})

	t.Run("nil error -> OnSuccess", func(t *testing.T) {
		g, _ := newTestDialGate()
		g.OnFailure("peer")
		recordDialOutcome(g, "peer", nil)
		if _, ok := g.states["peer"]; ok {
			t.Error("nil error must reset state")
		}
	})
}
