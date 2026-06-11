package daemon

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// flushRecorder counts coalescer flushes and records their payloads.
type flushRecorder struct {
	mu      sync.Mutex
	flushes []flushCall
	count   atomic.Int64
}

type flushCall struct {
	seq   int64
	count int
}

func (r *flushRecorder) flush(seq int64, count int) {
	r.count.Add(1)
	r.mu.Lock()
	r.flushes = append(r.flushes, flushCall{seq: seq, count: count})
	r.mu.Unlock()
}

func (r *flushRecorder) calls() []flushCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]flushCall, len(r.flushes))
	copy(out, r.flushes)
	return out
}

// TestNotifyCoalescer_QuietSingleEvent_Immediate pins the leading edge: when
// idle, the first Offer flushes IMMEDIATELY — a quiet single event pays zero
// added latency (the dispatch's max-delay concern, satisfied by construction).
func TestNotifyCoalescer_QuietSingleEvent_Immediate(t *testing.T) {
	rec := &flushRecorder{}
	c := newNotifyCoalescer(100*time.Millisecond, rec.flush)

	start := time.Now()
	c.Offer(42, 1)
	if got := rec.count.Load(); got != 1 {
		t.Fatalf("flushes = %d, want 1 immediately (leading edge)", got)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Errorf("leading flush took %v — must not wait for the window", elapsed)
	}
	calls := rec.calls()
	if calls[0].seq != 42 || calls[0].count != 1 {
		t.Errorf("flush payload = %+v, want seq=42 count=1", calls[0])
	}
}

// TestNotifyCoalescer_Burst_BoundedByWindowMath is tonight's storm shape: a
// sustained burst of 1000 Offers must produce a notify count bounded by
// window math (leading + ~one trailing per window), NOT by event count —
// the O(events × peers) amplifier is the defect.
func TestNotifyCoalescer_Burst_BoundedByWindowMath(t *testing.T) {
	rec := &flushRecorder{}
	window := 50 * time.Millisecond
	c := newNotifyCoalescer(window, rec.flush)

	start := time.Now()
	for i := int64(1); i <= 1000; i++ {
		c.Offer(i, 1)
	}
	burstDuration := time.Since(start)
	// Allow trailing flush to land.
	time.Sleep(window + 50*time.Millisecond)

	got := rec.count.Load()
	// Bound: 1 leading + one trailing per elapsed window (+1 slack for timer
	// scheduling). A tight loop of 1000 Offers spans well under one window on
	// any machine, so this is typically 2 — but bound by math, not by hope.
	maxFlushes := int64(1+(burstDuration/window)) + 2
	if got > maxFlushes {
		t.Fatalf("flushes = %d for 1000 events over %v — want <= %d (window math), the storm amplifier is back", got, burstDuration, maxFlushes)
	}
	// The trailing flush must carry the high-water seq and the absorbed count.
	calls := rec.calls()
	last := calls[len(calls)-1]
	if last.seq != 1000 {
		t.Errorf("last flush seq = %d, want 1000 (max-seq-wins)", last.seq)
	}
	var total int
	for _, fc := range calls {
		total += fc.count
	}
	if total != 1000 {
		t.Errorf("summed flush counts = %d, want 1000 (no events silently dropped from the count)", total)
	}
}

// TestNotifyCoalescer_PathADoubleIngest_OneFlush is the bead's scope-addition
// case: a pure Path-A replay re-fires the onEventWrite hook with seq=0 for an
// event already announced. Two Offers with no seq advance must collapse to
// exactly ONE flush — the trailing flush skips when the seq hasn't moved past
// the leading fire. Safe because the receiver keys on daemonID only
// (latest_seq/event_count are log-only).
func TestNotifyCoalescer_PathADoubleIngest_OneFlush(t *testing.T) {
	rec := &flushRecorder{}
	window := 50 * time.Millisecond
	c := newNotifyCoalescer(window, rec.flush)

	c.Offer(0, 1)
	c.Offer(0, 1)
	time.Sleep(window + 50*time.Millisecond)

	if got := rec.count.Load(); got != 1 {
		t.Fatalf("flushes = %d, want exactly 1 (stale-seq trailing flush must skip — Path-A double-ingest residual)", got)
	}
}

// TestNotifyCoalescer_SeqAdvancingPair_TwoFlushes pins that genuine progress
// within the window IS announced: leading flush for the first event, trailing
// flush carrying the newer seq for the second.
func TestNotifyCoalescer_SeqAdvancingPair_TwoFlushes(t *testing.T) {
	rec := &flushRecorder{}
	window := 50 * time.Millisecond
	c := newNotifyCoalescer(window, rec.flush)

	c.Offer(10, 1)
	c.Offer(11, 1)
	time.Sleep(window + 50*time.Millisecond)

	calls := rec.calls()
	if len(calls) != 2 {
		t.Fatalf("flushes = %d, want 2 (leading + trailing with the advanced seq); got %+v", len(calls), calls)
	}
	if calls[1].seq != 11 || calls[1].count != 1 {
		t.Errorf("trailing flush = %+v, want seq=11 count=1", calls[1])
	}
}

// TestNotifyCoalescer_ReArmsAfterIdle pins the cycle: burst → idle past the
// window → next event is a fresh leading-edge immediate flush again.
func TestNotifyCoalescer_ReArmsAfterIdle(t *testing.T) {
	rec := &flushRecorder{}
	window := 30 * time.Millisecond
	c := newNotifyCoalescer(window, rec.flush)

	c.Offer(1, 1) // leading
	c.Offer(2, 1) // absorbed → trailing
	time.Sleep(window + 30*time.Millisecond)
	base := rec.count.Load() // 2 (leading + trailing)

	start := time.Now()
	c.Offer(3, 1) // idle again → must be a fresh immediate leading flush
	if got := rec.count.Load(); got != base+1 {
		t.Fatalf("flushes after idle = %d, want %d (fresh leading edge)", got, base+1)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Errorf("post-idle flush took %v — must be immediate", elapsed)
	}
}
