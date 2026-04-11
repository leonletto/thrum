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

	d.OnMatch("ERROR: first")       // leading edge
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

