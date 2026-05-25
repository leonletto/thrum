// Package profile provides lightweight per-phase wall-clock instrumentation
// gated by the THRUM_PROFILE environment variable. When disabled (the
// production default), Time() and NewTimer() return no-op closures with
// sub-nanosecond overhead — instrumentation can live in hot-path code
// without measurable cost.
//
// When enabled (THRUM_PROFILE=1 at daemon start), each call emits a
// slog.Info entry under the "profile.<label>" message with elapsed_ms
// plus any extra key/value pairs supplied by the caller.
//
// Originally landed for thrum-bpq5 (walker+compactor wall-clock
// investigation). Designed to be permanent — flip THRUM_PROFILE on when
// a future investigation needs phase timing, off the rest of the time.
package profile

import (
	"log/slog"
	"os"
	"sync/atomic"
	"time"
)

// enabled is checked on every Time/Timer call. Atomic so a future RPC
// could flip it at runtime without restart. Default false (no overhead).
var enabled atomic.Bool

// noop is the disabled-mode closure. Returning a non-nil func keeps
// `defer profile.Time(...)()` syntactically valid at the call site without
// nil-check noise.
var noop = func() {}

// Init reads THRUM_PROFILE from the process environment and flips the gate
// on if set to a truthy value ("1", "true", "yes"). Call once at daemon
// bootstrap. Safe to call multiple times; later calls overwrite earlier
// ones.
func Init() {
	v := os.Getenv("THRUM_PROFILE")
	enabled.Store(v == "1" || v == "true" || v == "yes")
}

// SetEnabled forces the gate on or off at runtime (for tests or for a
// future runtime-control RPC). Init() is the normal startup path.
func SetEnabled(on bool) {
	enabled.Store(on)
}

// Enabled reports whether profile instrumentation is currently emitting.
func Enabled() bool {
	return enabled.Load()
}

// Time returns a closure that, when invoked, logs the elapsed wall-clock
// since Time() was called as a slog.Info entry under "profile.<label>".
// Optional extras pass key/value pairs (slog convention) appended after
// elapsed_ms. When profiling is disabled, returns a no-op closure with no
// time.Now() call and no slog emit.
//
// Usage:
//
//	defer profile.Time("walker.total")()
//	defer profile.Time("walker.total", "row_count", n)()
func Time(label string, extras ...any) func() {
	if !enabled.Load() {
		return noop
	}
	start := time.Now()
	return func() {
		args := make([]any, 0, 2+len(extras))
		args = append(args, "elapsed_ms", time.Since(start).Milliseconds())
		args = append(args, extras...)
		slog.Info("profile."+label, args...)
	}
}

// Timer is the fluent variant: capture additional key/value pairs that
// only become known at Done() time.
type Timer struct {
	label   string
	start   time.Time
	enabled bool // snapshot of gate at creation so Done() doesn't re-check
}

// NewTimer starts a timer for the given label. Disabled-state timers
// short-circuit Done() to a no-op.
func NewTimer(label string) *Timer {
	t := &Timer{label: label, enabled: enabled.Load()}
	if t.enabled {
		t.start = time.Now()
	}
	return t
}

// Done emits the slog entry. Optional kv pairs append after elapsed_ms.
// No-op when the gate was off at NewTimer().
func (t *Timer) Done(extras ...any) {
	if !t.enabled {
		return
	}
	args := make([]any, 0, 2+len(extras))
	args = append(args, "elapsed_ms", time.Since(t.start).Milliseconds())
	args = append(args, extras...)
	slog.Info("profile."+t.label, args...)
}

// ElapsedMs returns the wall-clock since NewTimer (no slog emit). For
// callers that aggregate multiple sub-phases into a single parent entry.
// Returns 0 when the gate was off at NewTimer().
func (t *Timer) ElapsedMs() int64 {
	if !t.enabled {
		return 0
	}
	return time.Since(t.start).Milliseconds()
}

// Phase records a sub-phase elapsed_ms under the timer's parent label.
// Useful for fine-grained timing without emitting separate top-level
// slog entries.
func (t *Timer) Phase(name string, elapsedMs int64) {
	if !t.enabled {
		return
	}
	slog.Info("profile."+t.label+"."+name, "elapsed_ms", elapsedMs)
}
