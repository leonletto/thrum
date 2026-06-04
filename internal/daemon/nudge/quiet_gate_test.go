package nudge

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// bottomLines must normalize CRLF (matching permission/detect.go) so a remote /
// SSH / Windows CRLF pane doesn't carry a trailing \r that defeats the
// chrome-stability comparison and delays every remote nudge to the deadline.
func TestBottomLines_NormalizesCRLF(t *testing.T) {
	crlf := "transcript\r\n────\r\n> input\r\n"
	lf := "transcript\n────\n> input\n"
	if got, want := bottomLines(crlf, 5), bottomLines(lf, 5); got != want {
		t.Fatalf("CRLF not normalized: %q != %q", got, want)
	}
	if strings.Contains(bottomLines(crlf, 5), "\r") {
		t.Fatalf("bottomLines left a stray \\r: %q", bottomLines(crlf, 5))
	}
}

// quietHarness installs controllable chrome-quiet seams: a fake clock advanced
// by sleepFn, a scripted sequence of pane captures, a controllable last-activity
// time, and a spinner matcher. Restores the real seams on cleanup.
type quietHarness struct {
	mu        sync.Mutex
	clock     time.Time
	captures  []string // consumed in order; the last entry repeats once exhausted
	capIdx    int
	captureFn func(target string, lines int) (string, error)
	activity  func(now time.Time) time.Time // returns the window_activity time given the current clock
	spinnerRe *regexp.Regexp
}

func installQuietHarness(t *testing.T, h *quietHarness) {
	t.Helper()
	origCapture := capturePaneFn
	origActivity := lastActivityFn
	origNow := timeNowFn
	origSleep := sleepFn
	origSpinner := spinnerReFn

	capturePaneFn = func(target string, lines int) (string, error) {
		if h.captureFn != nil {
			return h.captureFn(target, lines)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.captures) == 0 {
			return "", nil
		}
		i := h.capIdx
		if i >= len(h.captures) {
			i = len(h.captures) - 1
		} else {
			h.capIdx++
		}
		return h.captures[i], nil
	}
	lastActivityFn = func(string) (time.Time, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.activity == nil {
			return h.clock, nil // default: "active right now"
		}
		return h.activity(h.clock), nil
	}
	timeNowFn = func() time.Time {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.clock
	}
	sleepFn = func(_ context.Context, d time.Duration) bool {
		h.mu.Lock()
		h.clock = h.clock.Add(d)
		h.mu.Unlock()
		return true
	}
	spinnerReFn = func(string) *regexp.Regexp { return h.spinnerRe }

	t.Cleanup(func() {
		capturePaneFn = origCapture
		lastActivityFn = origActivity
		timeNowFn = origNow
		sleepFn = origSleep
		spinnerReFn = origSpinner
	})
}

func (h *quietHarness) elapsed(start time.Time) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clock.Sub(start)
}

// emptyThrumDir gives a config-less thrum dir → SilenceGate defaults (10s/60s, enabled).
func emptyThrumDir(t *testing.T) string { return t.TempDir() }

func TestPaneQuietForNudge_SpinnerFiresImmediately(t *testing.T) {
	start := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	h := &quietHarness{clock: start, captures: []string{"line\nSPIN running\n> "}, spinnerRe: regexp.MustCompile(`SPIN`)}
	installQuietHarness(t, h)
	if got := paneQuietForNudge(context.Background(), emptyThrumDir(t), "s:0.0", "claude"); got != nudgeFire {
		t.Fatalf("spinner present: got %v, want nudgeFire", got)
	}
	if e := h.elapsed(start); e != 0 {
		t.Fatalf("spinner-permissive should fire immediately, waited %v", e)
	}
}

func TestPaneQuietForNudge_IdleFastPathFires(t *testing.T) {
	start := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	h := &quietHarness{
		clock:    start,
		captures: []string{safePane},
		activity: func(now time.Time) time.Time { return now.Add(-time.Hour) }, // long idle
	}
	installQuietHarness(t, h)
	if got := paneQuietForNudge(context.Background(), emptyThrumDir(t), "s:0.0", "claude"); got != nudgeFire {
		t.Fatalf("idle pane: got %v, want nudgeFire", got)
	}
	if e := h.elapsed(start); e != 0 {
		t.Fatalf("idle fast-path should fire immediately, waited %v", e)
	}
}

func TestPaneQuietForNudge_DialogDefers(t *testing.T) {
	start := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	h := &quietHarness{clock: start, captures: []string{dialogPane}}
	installQuietHarness(t, h)
	if got := paneQuietForNudge(context.Background(), emptyThrumDir(t), "s:0.0", "claude"); got != nudgeDefer {
		t.Fatalf("dialog up: got %v, want nudgeDefer (7phu compose)", got)
	}
}

// Activity keeps bumping (idle redraw / continuous output) but the input chrome
// is stable → must fire via the chrome-stability backstop after the quiet
// window, NOT wait for the 60s deadline. This is the empirical-degradation guard.
func TestPaneQuietForNudge_ChromeStableFiresBeforeDeadline(t *testing.T) {
	start := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	h := &quietHarness{
		clock:    start,
		captures: []string{"transcript\n────────\n> idle input\n"}, // stable, repeats
		activity: func(now time.Time) time.Time { return now },     // always "active right now"
	}
	installQuietHarness(t, h)
	got := paneQuietForNudge(context.Background(), emptyThrumDir(t), "s:0.0", "claude")
	if got != nudgeFire {
		t.Fatalf("stable chrome: got %v, want nudgeFire", got)
	}
	e := h.elapsed(start)
	if e < 10*time.Second {
		t.Fatalf("fired too early (%v); chrome-stability window is 10s", e)
	}
	if e >= 60*time.Second {
		t.Fatalf("fired at the deadline (%v); chrome-stability backstop should fire ~10s, not 60s", e)
	}
}

// The human is typing: the input chrome changes every poll and activity bumps,
// so neither quiet signal trips → the nudge waits out to the deadline (it never
// fires mid-keystroke). Bounded by the 60s deadline.
func TestPaneQuietForNudge_ContinuousTypingHitsDeadline(t *testing.T) {
	start := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	var n int
	h := &quietHarness{
		clock:    start,
		activity: func(now time.Time) time.Time { return now }, // always active
		captureFn: func(string, int) (string, error) {
			n++
			return "transcript\n────────\n> typing " + string(rune('a'+n%26)) + "\n", nil // changes every poll
		},
	}
	installQuietHarness(t, h)
	got := paneQuietForNudge(context.Background(), emptyThrumDir(t), "s:0.0", "claude")
	if got != nudgeFire {
		t.Fatalf("continuous typing: got %v, want nudgeFire (deadline)", got)
	}
	e := h.elapsed(start)
	if e < 60*time.Second {
		t.Fatalf("fired before deadline (%v) while chrome was still changing — would interrupt typing", e)
	}
}

// Typing for a while, then the human pauses: fires the quiet window after the
// LAST change, not before.
func TestPaneQuietForNudge_TypingThenQuietFiresAfterPause(t *testing.T) {
	start := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	var calls int
	h := &quietHarness{
		clock:    start,
		activity: func(now time.Time) time.Time { return now }, // always active (no fast-idle)
		captureFn: func(string, int) (string, error) {
			calls++
			if calls <= 5 {
				return "t\n────\n> typing " + string(rune('a'+calls)) + "\n", nil // changing
			}
			return "t\n────\n> done\n", nil // stable thereafter
		},
	}
	installQuietHarness(t, h)
	got := paneQuietForNudge(context.Background(), emptyThrumDir(t), "s:0.0", "claude")
	if got != nudgeFire {
		t.Fatalf("typing-then-quiet: got %v, want nudgeFire", got)
	}
	// 5 changing polls (~2s) + 10s of stability ≈ 12s; must be > 10s and < deadline.
	e := h.elapsed(start)
	if e < 10*time.Second || e >= 60*time.Second {
		t.Fatalf("fire timing %v outside [10s,60s) — should fire ~quiet-window after last keystroke", e)
	}
}

// A dialog that appears AFTER the gate has already polled safely must still
// defer (the 7phu check runs every poll, not just the first).
func TestPaneQuietForNudge_DialogMidWaitDefers(t *testing.T) {
	start := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	var calls int
	h := &quietHarness{
		clock:    start,
		activity: func(now time.Time) time.Time { return now }, // active (no fast-idle)
		captureFn: func(string, int) (string, error) {
			calls++
			if calls == 1 {
				return "t\n────\n> typing a\n", nil // safe, still "changing"
			}
			return dialogPane, nil // dialog appears on poll 2
		},
	}
	installQuietHarness(t, h)
	if got := paneQuietForNudge(context.Background(), emptyThrumDir(t), "s:0.0", "claude"); got != nudgeDefer {
		t.Fatalf("dialog appearing mid-wait: got %v, want nudgeDefer", got)
	}
}

// sleepFn returning false (ctx cancelled during the inter-poll sleep) drops.
func TestPaneQuietForNudge_SleepCancelDrops(t *testing.T) {
	start := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	var n int
	h := &quietHarness{
		clock:    start,
		activity: func(now time.Time) time.Time { return now },
		captureFn: func(string, int) (string, error) {
			n++
			return "t\n────\n> typing " + string(rune('a'+n)) + "\n", nil // never quiet
		},
	}
	installQuietHarness(t, h)
	sleepFn = func(context.Context, time.Duration) bool { return false } // simulate cancel during sleep
	if got := paneQuietForNudge(context.Background(), emptyThrumDir(t), "s:0.0", "claude"); got != nudgeDrop {
		t.Fatalf("sleep-cancel: got %v, want nudgeDrop", got)
	}
}

func TestRealSleepCtx_CancelledReturnsFalse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if realSleepCtx(ctx, time.Hour) {
		t.Fatal("realSleepCtx on a cancelled ctx: got true, want false")
	}
}

func TestPaneQuietForNudge_GateDisabledFiresImmediately(t *testing.T) {
	start := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	// chrome_quiet_seconds < 0 disables the gate.
	cfg := `{"nudge":{"chrome_quiet_seconds":-1}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &quietHarness{clock: start, captures: []string{safePane}, activity: func(now time.Time) time.Time { return now }}
	installQuietHarness(t, h)
	if got := paneQuietForNudge(context.Background(), dir, "s:0.0", "claude"); got != nudgeFire {
		t.Fatalf("disabled gate: got %v, want nudgeFire", got)
	}
	if e := h.elapsed(start); e != 0 {
		t.Fatalf("disabled gate should fire immediately, waited %v", e)
	}
}

func TestPaneQuietForNudge_CaptureErrorFiresAnyway(t *testing.T) {
	start := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	h := &quietHarness{clock: start, captureFn: func(string, int) (string, error) {
		return "", os.ErrInvalid
	}}
	installQuietHarness(t, h)
	if got := paneQuietForNudge(context.Background(), emptyThrumDir(t), "s:0.0", "claude"); got != nudgeFire {
		t.Fatalf("capture error: got %v, want nudgeFire (degrade to notify-anyway)", got)
	}
}

func TestPaneQuietForNudge_CtxCancelledDrops(t *testing.T) {
	start := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	h := &quietHarness{
		clock:    start,
		activity: func(now time.Time) time.Time { return now },
		captureFn: func(string, int) (string, error) {
			calls++
			if calls >= 2 {
				cancel() // cancel mid-wait
			}
			return "t\n────\n> typing " + string(rune('a'+calls)) + "\n", nil
		},
	}
	installQuietHarness(t, h)
	if got := paneQuietForNudge(ctx, emptyThrumDir(t), "s:0.0", "claude"); got != nudgeDrop {
		t.Fatalf("cancelled ctx: got %v, want nudgeDrop", got)
	}
}
