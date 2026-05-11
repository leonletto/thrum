package rpc

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestNudgeSilentPaneAfter_NudgesWhenSilent pins thrum-puhr.10: when the
// pane capture is identical at baseline and post-wait, the watchdog
// fires send-keys + Enter with the configured nudge text.
func TestNudgeSilentPaneAfter_NudgesWhenSilent(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep := capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
	})

	capturePaneFn = func(_ string, _ int) (string, error) { return "welcome screen", nil }
	sleepFn = func(time.Duration) {}

	var sent atomic.Value
	sendKeysFn = func(target, text string) error {
		sent.Store(target + "|" + text)
		return nil
	}
	var enter atomic.Bool
	sendSpecialKeyFn = func(_, key string) error {
		if key == "Enter" {
			enter.Store(true)
		}
		return nil
	}

	thrumDir := t.TempDir()
	// Default config (silence_watchdog_seconds == 0) → enabled, 30s.
	nudgeSilentPaneAfter("sess:0.0", "claude", thrumDir, "thrum inbox --unread")

	got := sent.Load()
	if got == nil || got.(string) != "sess:0.0|thrum inbox --unread" {
		t.Errorf("expected send-keys nudge fired with target+text, got %v", got)
	}
	if !enter.Load() {
		t.Error("expected Enter sent after nudge")
	}
}

// TestNudgeSilentPaneAfter_SkipsWhenActive verifies no nudge fires when
// the pane produced output between baseline and post-wait captures.
func TestNudgeSilentPaneAfter_SkipsWhenActive(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep := capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
	})

	var call atomic.Int32
	capturePaneFn = func(_ string, _ int) (string, error) {
		n := call.Add(1)
		if n == 1 {
			return "before", nil
		}
		return "after — agent engaged", nil
	}
	sleepFn = func(time.Duration) {}

	var sendCount atomic.Int32
	sendKeysFn = func(_, _ string) error {
		sendCount.Add(1)
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	nudgeSilentPaneAfter("sess:0.0", "claude", t.TempDir(), "thrum inbox --unread")

	if got := sendCount.Load(); got != 0 {
		t.Errorf("expected no send-keys when pane changed; got %d", got)
	}
}

// TestWaitForPaneReady_ReturnsWhenStable verifies the readiness helper
// returns once the pane has been byte-identical for the configured
// number of consecutive captures.
func TestWaitForPaneReady_ReturnsWhenStable(t *testing.T) {
	prevCapture, prevSleep := capturePaneFn, sleepFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sleepFn = prevSleep
	})

	// Sequence: rendering... rendering... ready, ready, ready
	captures := []string{"loading", "loading.", "ready", "ready", "ready"}
	var idx atomic.Int32
	capturePaneFn = func(_ string, _ int) (string, error) {
		n := int(idx.Add(1)) - 1
		if n >= len(captures) {
			n = len(captures) - 1
		}
		return captures[n], nil
	}
	var slept atomic.Int32
	sleepFn = func(d time.Duration) { slept.Add(int32(d / time.Second)) }

	waitForPaneReady("sess:0.0", "claude", 2, 30)

	// Sequence: baseline("loading") + 4 polls. The third poll matches
	// the second (both "ready") → streak=1; the fourth matches again
	// → streak=2 → return. Total: 5 captures.
	if got := idx.Load(); got != 5 {
		t.Errorf("expected 5 captures (baseline + 4 polls), got %d", got)
	}
	if got := slept.Load(); got > 5 {
		t.Errorf("expected to return in <5 sleep-seconds when stable; got %d", got)
	}
}

// TestWaitForPaneReady_BailsAtCeiling verifies the readiness helper
// gives up after ceiling seconds when the pane never stabilizes.
func TestWaitForPaneReady_BailsAtCeiling(t *testing.T) {
	prevCapture, prevSleep := capturePaneFn, sleepFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sleepFn = prevSleep
	})

	var i atomic.Int32
	capturePaneFn = func(_ string, _ int) (string, error) {
		return "changing-" + string(rune(int('a')+int(i.Add(1)))), nil
	}
	sleepFn = func(time.Duration) {}

	start := time.Now()
	waitForPaneReady("sess:0.0", "claude", 2, 5)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waitForPaneReady should have returned quickly with mocked sleep, took %v", elapsed)
	}
	if got := i.Load(); got < 6 {
		t.Errorf("expected ~ceiling+1 captures, got %d", got)
	}
}

// TestNudgeSilentPaneAfter_SkipsOnTrustGate pins the cluster-8 guard:
// even if the pane is silent for the watchdog threshold (true for any
// fresh runtime sitting at a first-launch trust prompt), no nudge
// keystroke fires when permission.IsPaneSafeToType reports the pane
// as unsafe.
func TestNudgeSilentPaneAfter_SkipsOnTrustGate(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep := capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
	})

	const trust = "Do you trust the contents of this directory?\n  1. Yes\n  2. No"
	capturePaneFn = func(_ string, _ int) (string, error) { return trust, nil }
	sleepFn = func(time.Duration) {}

	var sendCount atomic.Int32
	sendKeysFn = func(_, _ string) error {
		sendCount.Add(1)
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	nudgeSilentPaneAfter("sess:0.0", "codex", t.TempDir(), "thrum inbox --unread")

	if got := sendCount.Load(); got != 0 {
		t.Errorf("watchdog typed into a trust gate; expected 0 send-keys, got %d", got)
	}
}

// TestWaitForPaneReady_ReturnsUnsafeOnTrustGate pins the cluster-8
// readiness-side guard: when the stabilized pane is at a trust gate
// the function reports !safe so callers skip the inject.
func TestWaitForPaneReady_ReturnsUnsafeOnTrustGate(t *testing.T) {
	prevCapture, prevSleep := capturePaneFn, sleepFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sleepFn = prevSleep
	})

	const trust = "Do you trust the contents of this directory?\n  1. Yes\n  2. No"
	// Stabilizes immediately on the trust pane.
	capturePaneFn = func(_ string, _ int) (string, error) { return trust, nil }
	sleepFn = func(time.Duration) {}

	safe := waitForPaneReady("sess:0.0", "codex", 2, 30)
	if safe {
		t.Errorf("waitForPaneReady reported safe on a trust gate; expected unsafe")
	}
}

func TestWaitForPaneReady_ReturnsSafeOnIdlePane(t *testing.T) {
	prevCapture, prevSleep := capturePaneFn, sleepFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sleepFn = prevSleep
	})

	capturePaneFn = func(_ string, _ int) (string, error) { return "$ _", nil }
	sleepFn = func(time.Duration) {}

	if !waitForPaneReady("sess:0.0", "codex", 2, 30) {
		t.Errorf("waitForPaneReady reported unsafe on idle pane; expected safe")
	}
}

// TestNudgeSilentPaneAfter_DisabledByConfig verifies the watchdog
// short-circuits when restart.silence_watchdog_seconds is negative.
func TestNudgeSilentPaneAfter_DisabledByConfig(t *testing.T) {
	prevCapture, prevSend, prevSleep := capturePaneFn, sendKeysFn, sleepFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sleepFn = prevSleep
	})

	var captureCount atomic.Int32
	capturePaneFn = func(_ string, _ int) (string, error) {
		captureCount.Add(1)
		return "", nil
	}
	sleepFn = func(time.Duration) {}
	sendKeysFn = func(_, _ string) error { return nil }

	thrumDir := t.TempDir()
	configJSON := `{"restart":{"silence_watchdog_seconds":-1}}`
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	nudgeSilentPaneAfter("sess:0.0", "claude", thrumDir, "thrum inbox --unread")

	if got := captureCount.Load(); got != 0 {
		t.Errorf("expected no capture calls when watchdog disabled; got %d", got)
	}
}
