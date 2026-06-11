package daemon

import (
	"errors"
	"math/rand"
	"sync"
	"time"
)

// dialGate is a per-peer dial backoff + quarantine guard (thrum-aop6). It bounds
// how often this daemon dials a failing/unreachable peer so a single dead or
// flapping peer can't be hammered into a connection-reset/EOF storm (the
// leondev:9177 incident: 8770 resets + 4740 EOFs over ~6h driven by sync.notify
// fan-out + periodic pulls retrying with NO backoff).
//
// It guards the two storm dial paths — both bottom out in a per-peer dial keyed
// by daemonID:
//   - notify send: DaemonSyncManager.fanOutNotify -> SendNotify
//   - pull:        DaemonSyncManager.SyncFromPeer -> PullAllEvents
//
// Self-contained: the release line has no reachStore/peer_reachability infra.
// The 0.11 reachStore circuit-breaker is intended to SUPERSEDE this; the
// constants below are documented so that supersession can re-tune coherently.
//
// Composition with the sibling sync guards is orthogonal (no shared lock, no
// nesting): thrum-oc74's notify coalescer bounds the notify EVENT rate; this
// bounds per-peer DIAL attempts within each fan-out. thrum-w78a's pullGate
// bounds same-peer pull CONCURRENCY; this sits in FRONT of it (claim before
// pulls.Do), so a quarantined peer never even takes a flight slot.
//
// Recovery contract (thrum-aop6 HARD POINT 1 — a flapping peer must never die
// permanently): while quarantined, claim admits exactly one low-rate probe per
// dialProbeInterval; a probe success re-admits the peer immediately (state
// cleared), a probe failure keeps it quarantined with the next probe one
// interval out. So a peer that comes back is re-admitted on its first good dial.
const (
	// dialBackoffBase is the base unit of exponential backoff after a dial
	// failure: the computed backoff is dialBackoffBase * 2^(consecutiveFails-1),
	// capped at dialBackoffCap.
	dialBackoffBase = 1 * time.Second
	// dialBackoffCap caps the exponential backoff growth.
	dialBackoffCap = 5 * time.Minute
	// dialQuarantineThreshold is the number of CONSECUTIVE dial failures after
	// which a peer is quarantined (stop hammering; probe at a low rate instead).
	dialQuarantineThreshold = 5
	// dialProbeInterval is the low-rate probe spacing while a peer is
	// quarantined — one dial admitted per interval until it recovers.
	dialProbeInterval = 60 * time.Second
)

// dialState is the per-peer failure record. Healthy peers carry NO state (absent
// from the map) so the hot path — a reachable peer — is a single map miss.
type dialState struct {
	consecutiveFails int
	nextAttempt      time.Time // earliest time a dial is permitted
	quarantined      bool
}

// dialGate holds per-peer dial state. now and jitter are injectable so tests
// drive backoff/quarantine/recovery on a fake clock with zero real sleeps.
type dialGate struct {
	mu     sync.Mutex
	states map[string]*dialState
	now    func() time.Time
	// jitter returns a duration in [0, d), added to the fixed half of the
	// equal-jitter backoff. Default is rand-based; tests inject 0.
	jitter func(d time.Duration) time.Duration
}

func newDialGate() *dialGate {
	return &dialGate{
		states: make(map[string]*dialState),
		now:    time.Now,
		jitter: func(d time.Duration) time.Duration {
			if d <= 0 {
				return 0
			}
			return time.Duration(rand.Int63n(int64(d))) // #nosec G404 -- jitter only, not security-sensitive
		},
	}
}

// claim reports whether a dial to peerID is permitted right now, and atomically
// reserves the slot so concurrent callers within the same backoff/probe window
// don't stampede. A healthy peer (no failure state) is always permitted with no
// reservation — the common case stays cheap. While backed off or quarantined,
// claim admits at most one dial per window; the dial's outcome (OnSuccess /
// OnFailure) then sets the real next window, overwriting the provisional hold.
func (g *dialGate) claim(peerID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.states[peerID]
	if !ok {
		return true
	}
	now := g.now()
	if now.Before(st.nextAttempt) {
		return false
	}
	// Past the window — admit one dial and reserve to bound concurrent probes.
	if st.quarantined {
		st.nextAttempt = now.Add(dialProbeInterval)
	} else {
		st.nextAttempt = now.Add(dialBackoffCap)
	}
	return true
}

// OnSuccess clears all failure state for peerID — a reachable peer is re-admitted
// immediately (no residual backoff or quarantine).
func (g *dialGate) OnSuccess(peerID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.states, peerID)
}

// OnFailure records a dial failure for peerID, growing the per-peer backoff
// window and engaging quarantine once dialQuarantineThreshold consecutive
// failures accumulate.
func (g *dialGate) OnFailure(peerID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.states[peerID]
	if !ok {
		st = &dialState{}
		g.states[peerID] = st
	}
	st.consecutiveFails++
	now := g.now()

	if st.consecutiveFails >= dialQuarantineThreshold {
		st.quarantined = true
		st.nextAttempt = now.Add(dialProbeInterval)
		return
	}

	// Exponential backoff with equal jitter: actual delay is half the computed
	// backoff plus a random point in the other half (base/2 .. base). This
	// de-correlates retries across peers/daemons without ever dropping below
	// half the nominal backoff.
	backoff := dialBackoffBase << uint(st.consecutiveFails-1)
	if backoff > dialBackoffCap || backoff <= 0 {
		backoff = dialBackoffCap
	}
	st.nextAttempt = now.Add(backoff/2 + g.jitter(backoff/2))
}

// errDialFailed marks an error chain as a peer-DIAL (connect) failure, as
// opposed to a post-connect RPC/apply error. recordDialOutcome uses it to count
// only genuine reachability failures toward backoff/quarantine — a REACHABLE
// peer whose pull later hits an apply error must never be quarantined
// (thrum-aop6 HARD POINT, pin b). Wrapped at the dial sites in sync_client.go.
var errDialFailed = errors.New("dial failed")

// recordDialOutcome translates a dial/pull/notify result into a dialGate update:
// an errDialFailed-tagged error counts as a failure (backoff/quarantine);
// anything else — success, or a post-connect error on a reachable peer —
// resets the peer to healthy.
func recordDialOutcome(g *dialGate, peerID string, err error) {
	if err != nil && errors.Is(err, errDialFailed) {
		g.OnFailure(peerID)
		return
	}
	g.OnSuccess(peerID)
}
