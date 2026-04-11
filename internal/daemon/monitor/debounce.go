package monitor

import (
	"fmt"
	"sync"
	"time"
)

// DebounceEmitFn is called when the debouncer decides a message should be
// delivered. Called while holding the debouncer lock, so the function should
// be short-running (or spawn a goroutine).
type DebounceEmitFn func(content string)

// Debouncer implements leading-edge debouncing with a trailing summary.
//
// Algorithm:
//   - First match in a window fires immediately (leading edge).
//   - Subsequent matches within the window are counted and their first
//     content is preserved as pendingFirst.
//   - When FlushExpired is called after the window has elapsed and there
//     are pending matches, a single trailing summary is emitted containing
//     pendingFirst plus "(+N more matches suppressed in the last X)".
//
// The supervisor is responsible for calling FlushExpired at the correct
// time — typically from a time.Timer scheduled for (lastEmitAt + window).
type Debouncer struct {
	mu     sync.Mutex
	window time.Duration
	now    func() time.Time
	emit   DebounceEmitFn

	lastEmitAt   time.Time
	pendingCount int
	pendingFirst string
}

// NewDebouncer constructs a debouncer with the given window.
func NewDebouncer(window time.Duration, now func() time.Time, emit DebounceEmitFn) *Debouncer {
	return &Debouncer{window: window, now: now, emit: emit}
}

// OnMatch is called by the runner for each matched line. Returns the
// duration until FlushExpired should next be called (i.e. when the current
// window will end). The caller schedules its timer for that time.
func (d *Debouncer) OnMatch(content string) time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.now()
	if d.lastEmitAt.IsZero() || now.Sub(d.lastEmitAt) >= d.window {
		d.emit(content)
		d.lastEmitAt = now
		d.pendingCount = 0
		d.pendingFirst = ""
		return d.window
	}

	if d.pendingCount == 0 {
		d.pendingFirst = content
	}
	d.pendingCount++
	return d.lastEmitAt.Add(d.window).Sub(now)
}

// FlushExpired emits a trailing summary if the window has elapsed and
// there are pending matches. Safe to call at any time; returns true if a
// summary was emitted.
func (d *Debouncer) FlushExpired() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.pendingCount == 0 {
		return false
	}
	if d.now().Sub(d.lastEmitAt) < d.window {
		return false
	}

	extra := d.pendingCount - 1
	var summary string
	if extra > 0 {
		summary = fmt.Sprintf("%s\n(+%d more matches suppressed in the last %s)",
			d.pendingFirst, extra, d.window)
	} else {
		// Only one suppressed: no "+N more" line.
		summary = d.pendingFirst + fmt.Sprintf("\n(suppressed during last %s)", d.window)
	}

	d.emit(summary)
	d.lastEmitAt = d.now()
	d.pendingCount = 0
	d.pendingFirst = ""
	return true
}
