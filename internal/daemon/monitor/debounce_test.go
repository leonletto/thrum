package monitor

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedEmit struct {
	content string
	at      time.Time
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestDebounce_LeadingEdgeDeliversFirstMatch(t *testing.T) {
	var emits []recordedEmit
	clk := &fakeClock{now: time.Unix(0, 0)}
	d := NewDebouncer(60*time.Second, clk.Now, func(content string) {
		emits = append(emits, recordedEmit{content: content, at: clk.Now()})
	})

	d.OnMatch("ERROR: first")
	assert.Len(t, emits, 1)
	assert.Equal(t, "ERROR: first", emits[0].content)
}

func TestDebounce_SuppressesWithinWindow(t *testing.T) {
	var emits []recordedEmit
	clk := &fakeClock{now: time.Unix(0, 0)}
	d := NewDebouncer(60*time.Second, clk.Now, func(content string) {
		emits = append(emits, recordedEmit{content: content})
	})

	d.OnMatch("ERROR: first")
	clk.Advance(10 * time.Second)
	d.OnMatch("ERROR: second")
	clk.Advance(10 * time.Second)
	d.OnMatch("ERROR: third")

	assert.Len(t, emits, 1, "only the first leading-edge emit should fire")
}

func TestDebounce_TrailingSummaryAfterWindow(t *testing.T) {
	var emits []recordedEmit
	clk := &fakeClock{now: time.Unix(0, 0)}
	d := NewDebouncer(60*time.Second, clk.Now, func(content string) {
		emits = append(emits, recordedEmit{content: content})
	})

	d.OnMatch("ERROR: first") // leading edge
	clk.Advance(10 * time.Second)
	d.OnMatch("ERROR: suppressed1") // first suppressed → pendingFirst
	clk.Advance(5 * time.Second)
	d.OnMatch("ERROR: suppressed2") // second suppressed
	clk.Advance(50 * time.Second)   // now past the window (65s total)
	// Simulate the trailing timer firing.
	d.FlushExpired()

	require.Len(t, emits, 2)
	assert.Equal(t, "ERROR: first", emits[0].content)
	assert.Contains(t, emits[1].content, "ERROR: suppressed1")
	assert.Contains(t, emits[1].content, "+1 more matches suppressed")
}

func TestDebounce_NextMatchAfterQuietPeriodFiresLeadingEdgeAgain(t *testing.T) {
	var emits []recordedEmit
	clk := &fakeClock{now: time.Unix(0, 0)}
	d := NewDebouncer(60*time.Second, clk.Now, func(content string) {
		emits = append(emits, recordedEmit{content: content})
	})

	d.OnMatch("first")
	clk.Advance(120 * time.Second) // well past window
	d.OnMatch("second")

	assert.Len(t, emits, 2)
	assert.Equal(t, "first", emits[0].content)
	assert.Equal(t, "second", emits[1].content)
}

func TestDebounce_ExactlyAtBoundaryEmits(t *testing.T) {
	// edge case: match arrives exactly at lastEmitAt + window
	var emits []recordedEmit
	clk := &fakeClock{now: time.Unix(0, 0)}
	d := NewDebouncer(60*time.Second, clk.Now, func(content string) {
		emits = append(emits, recordedEmit{content: content})
	})

	d.OnMatch("first")
	clk.Advance(60 * time.Second)
	d.OnMatch("second")

	assert.Len(t, emits, 2)
}

// TestDebounce_QuietTail asserts that FlushExpired is a no-op when called
// with no pending matches. This covers the "quiet tail" case from the
// design doc Testing Strategy: if the runner's flush timer fires after a
// leading-edge emit with no subsequent matches in the window, no spurious
// trailing summary should be emitted. Review finding 7.
func TestDebounce_QuietTail(t *testing.T) {
	var emits []recordedEmit
	clk := &fakeClock{now: time.Unix(0, 0)}
	d := NewDebouncer(60*time.Second, clk.Now, func(content string) {
		emits = append(emits, recordedEmit{content: content})
	})

	// Leading edge, then no more matches at all for the whole window.
	d.OnMatch("solo")
	require.Len(t, emits, 1)

	// Advance past the window and call FlushExpired — pendingCount is 0,
	// so nothing should be emitted.
	clk.Advance(120 * time.Second)
	flushed := d.FlushExpired()
	assert.False(t, flushed, "FlushExpired must return false when pendingCount == 0")
	assert.Len(t, emits, 1, "no spurious trailing summary when there were no suppressed matches")
}

// TestDebounce_BackToBackWindows asserts that two full debounce cycles
// (leading → trailing → (quiet gap) → leading → trailing) interleave
// correctly. Per the "one emit per window" semantic, FlushExpired's
// trailing emit resets lastEmitAt to flush time, so the next OnMatch
// must wait a full window after the flush before it qualifies as a new
// leading edge — any match inside that reset window is still suppressed.
// This test verifies lastEmitAt is advanced (not cleared) after flush
// and that a subsequent window fully restarts the leading → trailing
// cycle when given enough quiet time. Review finding 7 — design doc
// Testing Strategy case "back-to-back windows".
func TestDebounce_BackToBackWindows(t *testing.T) {
	var emits []recordedEmit
	clk := &fakeClock{now: time.Unix(0, 0)}
	d := NewDebouncer(60*time.Second, clk.Now, func(content string) {
		emits = append(emits, recordedEmit{content: content, at: clk.Now()})
	})

	// --- Cycle 1 ---
	d.OnMatch("A-leading") // t=0: leading edge emit, lastEmitAt=0
	clk.Advance(5 * time.Second)
	d.OnMatch("A-suppressed1") // t=5: within window, pendingFirst
	clk.Advance(5 * time.Second)
	d.OnMatch("A-suppressed2")    // t=10: still within window
	clk.Advance(60 * time.Second) // t=70: past the t=0 window
	require.True(t, d.FlushExpired(),
		"cycle 1 trailing summary must fire; sets lastEmitAt=70s")

	// --- Inter-cycle quiet gap (must exceed window) ---
	// After cycle 1 flush, lastEmitAt=70s. A match at t=75s would be
	// suppressed (only 5s past flush). Advance a full window so the next
	// match qualifies as a fresh leading edge.
	clk.Advance(65 * time.Second) // t=135: lastEmitAt is 65s ago

	// --- Cycle 2 ---
	d.OnMatch("B-leading") // t=135: leading edge, lastEmitAt=135
	clk.Advance(10 * time.Second)
	d.OnMatch("B-suppressed1")    // t=145: within cycle 2 window
	clk.Advance(60 * time.Second) // t=205: past cycle 2 window
	require.True(t, d.FlushExpired(),
		"cycle 2 trailing summary must fire with its own pendingFirst")

	// 4 emits total: A-leading, A-trailing, B-leading, B-trailing
	require.Len(t, emits, 4)
	assert.Equal(t, "A-leading", emits[0].content)
	assert.Contains(t, emits[1].content, "A-suppressed1")
	assert.Contains(t, emits[1].content, "+1 more matches suppressed")
	assert.Equal(t, "B-leading", emits[2].content,
		"cycle 2 leading edge must contain B-leading, not bleed from cycle 1")
	assert.Contains(t, emits[3].content, "B-suppressed1",
		"cycle 2 trailing summary must reference its own pendingFirst")
}
