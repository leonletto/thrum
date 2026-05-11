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
	nudgeSilentPaneAfter("sess:0.0", thrumDir, "thrum inbox --unread")

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

	nudgeSilentPaneAfter("sess:0.0", t.TempDir(), "thrum inbox --unread")

	if got := sendCount.Load(); got != 0 {
		t.Errorf("expected no send-keys when pane changed; got %d", got)
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

	nudgeSilentPaneAfter("sess:0.0", thrumDir, "thrum inbox --unread")

	if got := captureCount.Load(); got != 0 {
		t.Errorf("expected no capture calls when watchdog disabled; got %d", got)
	}
}
