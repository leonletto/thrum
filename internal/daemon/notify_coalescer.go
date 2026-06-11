package daemon

import (
	"sync"
	"time"
)

// notifyCoalescer bounds the sync.notify fan-out under event bursts
// (thrum-oc74). The onEventWrite hook fires BroadcastNotify once per applied
// event with no debounce, so a burst fans out O(events × peers) — the common
// amplifier under all three 2026-06-10 storms (ycqj/1846/lv9x). The receiver
// consumes latest_seq/event_count for logging only (sync_notify.go Handle —
// triggerSync keys on daemonID alone), so coalescing payloads is semantically
// free.
//
// Shape: LEADING-EDGE + TRAILING. When idle, the first Offer flushes
// immediately — a quiet single event pays zero added latency. Offers landing
// within the window are absorbed into one trailing flush at window end,
// carrying the high-water seq and the summed event count. The trailing flush
// SKIPS when the seq has not advanced past the last flush (e.g. the Path-A
// re-ingest residual, which re-offers seq=0 for an already-announced event) —
// no information is lost because consumers key on daemonID only.
//
// Shutdown: there is deliberately NO lifecycle/drain. A pending trailing
// flush dropping with the process loses only a hint — checkpoint-driven pulls
// recover on the next notify or poll, the identical guarantee to the
// pre-coalescer per-call fire-and-forget goroutines.
type notifyCoalescer struct {
	window time.Duration
	flush  func(latestSeq int64, eventCount int)

	mu          sync.Mutex
	lastSentSeq int64
	armed       bool // a trailing window is open (timer scheduled)
	pendingSeq  int64
	pendingN    int
}

// notifyCoalesceWindow is the default coalescing window. 100ms: a notify only
// triggers a peer pull whose own RTT+apply exceeds this, so a tighter window
// buys nothing perceptible; at 100ms a 1000-event burst over T seconds is
// bounded to ≤ 1 + ceil(T/0.1) notifies per peer instead of 1000.
var notifyCoalesceWindow = 100 * time.Millisecond

func newNotifyCoalescer(window time.Duration, flush func(latestSeq int64, eventCount int)) *notifyCoalescer {
	return &notifyCoalescer{window: window, flush: flush}
}

// Offer submits one event's notify. Either flushes immediately (idle,
// leading edge) or absorbs into the open trailing window.
func (c *notifyCoalescer) Offer(seq int64, count int) {
	c.mu.Lock()
	if c.armed {
		// Window open: absorb. High-water seq; counts accumulate.
		if seq > c.pendingSeq {
			c.pendingSeq = seq
		}
		c.pendingN += count
		c.mu.Unlock()
		return
	}
	// Idle: leading-edge flush now, then open the trailing window so the
	// burst that may follow gets absorbed.
	c.armed = true
	c.pendingSeq = 0
	c.pendingN = 0
	if seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	flush := c.flush
	time.AfterFunc(c.window, c.trailingFlush)
	c.mu.Unlock()

	flush(seq, count)
}

// trailingFlush closes the window: emits one coalesced notify for everything
// absorbed — unless the seq never advanced past the last flush (replay
// residual), in which case it skips silently.
func (c *notifyCoalescer) trailingFlush() {
	c.mu.Lock()
	seq, n := c.pendingSeq, c.pendingN
	c.armed = false
	c.pendingSeq = 0
	c.pendingN = 0
	skip := n == 0 || seq <= c.lastSentSeq
	if !skip && seq > c.lastSentSeq {
		c.lastSentSeq = seq
	}
	flush := c.flush
	c.mu.Unlock()

	if !skip {
		flush(seq, n)
	}
}
