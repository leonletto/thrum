package rpc

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/identitybanner"
)

// buildClaudeFreshPane returns a simulated pane snapshot that mimics a freshly
// launched Claude Code agent: banner sentinel, blank lines, animated spinner,
// more blank lines, and the horizontal rule separating transcript from chrome.
// No real agent output is present — the watchdog should fire a nudge.
func buildClaudeFreshPane() string {
	return "Agent: @test-agent\nRole:  implementer\n" +
		identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"\n" +
		"✻ Churned for 3s\n" +
		"\n" +
		"\n" +
		"────────────────────────────────────────\n" +
		"│ >                                     │\n" +
		"claude-sonnet-4-6 · 0 tokens · ~/dev\n" +
		"? for shortcuts\n"
}

// buildClaudeRespondedPane returns a pane where the agent has written a line
// after the sentinel — the watchdog must NOT fire a nudge.
func buildClaudeRespondedPane() string {
	return "Agent: @test-agent\n" +
		identitybanner.PrimeTruncationSentinel + "\n" +
		"\n" +
		"I'll read the prime now.\n" +
		"\n" +
		"✻ Scanning for 5s\n" +
		"────────────────────────────────────────\n" +
		"│ >                                     │\n"
}

var (
	testBottomAnchorRe = regexp.MustCompile(`^─{20,}$`)
	// testSpinnerRe uses \S+ (not \w+) to handle unicode verb suffixes like "Sautéed".
	testSpinnerRe = regexp.MustCompile(`^✻ \S+ for \d+s$`)
)

// ── paneAgentEngaged unit tests ──────────────────────────────────────────────

// TestPaneAgentEngaged_FreshPane: banner + blanks + spinner + rule → not engaged → nudge.
func TestPaneAgentEngaged_FreshPane(t *testing.T) {
	got := paneAgentEngaged(buildClaudeFreshPane(), testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) for fresh pane with only spinner between anchors")
	}
}

// TestPaneAgentEngaged_AgentResponded: agent wrote text → engaged → no nudge.
func TestPaneAgentEngaged_AgentResponded(t *testing.T) {
	got := paneAgentEngaged(buildClaudeRespondedPane(), testBottomAnchorRe, testSpinnerRe)
	if !got {
		t.Error("expected true (engaged) when agent output is present between anchors")
	}
}

// TestPaneAgentEngaged_NoTopAnchor: sentinel scrolled out → conservative true.
func TestPaneAgentEngaged_NoTopAnchor(t *testing.T) {
	pane := "✻ Scanning for 2s\n────────────────────────────────────────\n│ > │\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if !got {
		t.Error("expected true (conservative) when top anchor is missing")
	}
}

// TestPaneAgentEngaged_NoBottomAnchor: no horizontal rule (nil re) → conservative true.
func TestPaneAgentEngaged_NoBottomAnchor(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\n\n✻ Churned for 1s\n\n"
	got := paneAgentEngaged(pane, nil, testSpinnerRe)
	if !got {
		t.Error("expected true (conservative) when bottom anchor regex is nil")
	}
}

// TestPaneAgentEngaged_BottomAboveTop: rule above sentinel → conservative true.
func TestPaneAgentEngaged_BottomAboveTop(t *testing.T) {
	pane := "────────────────────────────────────────\n" + identitybanner.PrimeTruncationSentinel + "\n\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if !got {
		t.Error("expected true (conservative) when bottom anchor is above top anchor")
	}
}

// TestPaneAgentEngaged_NilSpinnerRe_BlanksOnly: no spinner re, only blanks → not engaged.
func TestPaneAgentEngaged_NilSpinnerRe_BlanksOnly(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\n\n\n────────────────────────────────────────\n│ > │\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, nil)
	if got {
		t.Error("expected false (not engaged) with nil spinnerRe and only blanks between anchors")
	}
}

// TestPaneAgentEngaged_NilSpinnerRe_AnyText: no spinner re, non-blank text → engaged.
func TestPaneAgentEngaged_NilSpinnerRe_AnyText(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel + "\nSome output\n────────────────────────────────────────\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, nil)
	if !got {
		t.Error("expected true (engaged) with nil spinnerRe and text between anchors")
	}
}

// TestPaneAgentEngaged_MultipleSpinnerLines: multiple consecutive spinner lines
// between anchors → not engaged (all are chrome).
func TestPaneAgentEngaged_MultipleSpinnerLines(t *testing.T) {
	// Use verbs that the regex matches (including unicode with \S+)
	pane := identitybanner.PrimeTruncationSentinel +
		"\n✻ Sautéed for 1s\n✻ Churned for 2s\n✻ Simmered for 3s\n" +
		"────────────────────────────────────────\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if got {
		t.Error("expected false (not engaged) when only multiple spinner lines between anchors")
	}
}

// TestPaneAgentEngaged_MultiLineResponse: multi-line agent prose → engaged.
func TestPaneAgentEngaged_MultiLineResponse(t *testing.T) {
	pane := identitybanner.PrimeTruncationSentinel +
		"\nI will now read the prime.\nLet me check the context.\n✻ Scanning for 2s\n" +
		"────────────────────────────────────────\n"
	got := paneAgentEngaged(pane, testBottomAnchorRe, testSpinnerRe)
	if !got {
		t.Error("expected true (engaged) for multi-line agent response")
	}
}

// ── nudgeSilentPaneAfter integration tests ───────────────────────────────────

// stableActivity returns a tmuxLastActivityFn stub that always reports
// pane-idle-for-silenceThreshold+1s (i.e. silence immediately detected).
func stableActivity() func(string) (time.Time, error) {
	past := time.Now().Add(-(silenceThreshold + time.Second))
	return func(_ string) (time.Time, error) { return past, nil }
}

// TestNudgeSilentPaneAfter_NudgesWhenSilent pins thrum-puhr.10 + thrum-84xc:
// when the pane shows only spinner/blanks between banner sentinel and horizontal
// rule, the watchdog fires send-keys + Enter with the configured nudge text.
func TestNudgeSilentPaneAfter_NudgesWhenSilent(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	tmuxLastActivityFn = stableActivity()
	capturePaneFn = func(_ string, _ int) (string, error) { return buildClaudeFreshPane(), nil }
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
// the agent has produced real output between the anchors.
func TestNudgeSilentPaneAfter_SkipsWhenActive(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	tmuxLastActivityFn = stableActivity()
	capturePaneFn = func(_ string, _ int) (string, error) { return buildClaudeRespondedPane(), nil }
	sleepFn = func(time.Duration) {}

	var sendCount atomic.Int32
	sendKeysFn = func(_, _ string) error {
		sendCount.Add(1)
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	nudgeSilentPaneAfter("sess:0.0", "claude", t.TempDir(), "thrum inbox --unread")

	if got := sendCount.Load(); got != 0 {
		t.Errorf("expected no send-keys when agent has responded; got %d", got)
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
// even if the pane has no agent output, no nudge keystroke fires when
// permission.IsPaneSafeToType reports the pane as unsafe.
func TestNudgeSilentPaneAfter_SkipsOnTrustGate(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	const trust = "Do you trust the contents of this directory?\n  1. Yes\n  2. No"
	tmuxLastActivityFn = stableActivity()
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
	prevCapture, prevSend, prevSleep, prevActivity := capturePaneFn, sendKeysFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	var captureCount atomic.Int32
	capturePaneFn = func(_ string, _ int) (string, error) {
		captureCount.Add(1)
		return "", nil
	}
	var activityCount atomic.Int32
	tmuxLastActivityFn = func(_ string) (time.Time, error) {
		activityCount.Add(1)
		return time.Now(), nil
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
	if got := activityCount.Load(); got != 0 {
		t.Errorf("expected no activity calls when watchdog disabled; got %d", got)
	}
}

// TestNudgeSilentPaneAfter_DeadlineExitsWhenBusy verifies that the watchdog
// exits silently (no nudge) when window_activity bumps continuously past the
// 30s deadline without ever achieving silenceThreshold of quiet.
func TestNudgeSilentPaneAfter_DeadlineExitsWhenBusy(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity, prevNow :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn, timeNowFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
		timeNowFn = prevNow
	})

	// Use a fake clock: both timeNowFn and tmuxLastActivityFn use it so
	// silenceFor = timeNowFn() - activity = 0 (activity is always "just now"
	// on the fake clock), and the deadline can be blown by advancing the clock.
	var fakeNow atomic.Value
	fakeNow.Store(time.Now())
	timeNowFn = func() time.Time { return fakeNow.Load().(time.Time) }

	// Activity is always at fakeNow — silence never exceeds silenceThreshold.
	tmuxLastActivityFn = func(_ string) (time.Time, error) { return fakeNow.Load().(time.Time), nil }

	// sleepFn advances the fake clock by 1s so the 30s deadline fires after ~31 ticks.
	sleepFn = func(d time.Duration) {
		fakeNow.Store(fakeNow.Load().(time.Time).Add(time.Second))
	}

	var captureCount atomic.Int32
	capturePaneFn = func(_ string, _ int) (string, error) {
		captureCount.Add(1)
		return "some pane", nil
	}
	var sendCount atomic.Int32
	sendKeysFn = func(_, _ string) error {
		sendCount.Add(1)
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	nudgeSilentPaneAfter("sess:0.0", "claude", t.TempDir(), "thrum inbox --unread")

	if got := sendCount.Load(); got != 0 {
		t.Errorf("expected no nudge when busy past deadline; got %d send-keys calls", got)
	}
	if got := captureCount.Load(); got != 0 {
		t.Errorf("expected no capture when busy past deadline; got %d", got)
	}
}

// TestNudgeSilentPaneAfter_TransientActivityErrors verifies the watchdog
// tolerates up to watchdogMaxConsecutiveErrors-1 errors then succeeds.
func TestNudgeSilentPaneAfter_TransientActivityErrors(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	// First watchdogMaxConsecutiveErrors-1 calls error, then always returns silent.
	var actCall atomic.Int32
	past := time.Now().Add(-(silenceThreshold + time.Second))
	tmuxLastActivityFn = func(_ string) (time.Time, error) {
		n := actCall.Add(1)
		if n <= int32(watchdogMaxConsecutiveErrors-1) {
			return time.Time{}, fmt.Errorf("transient error %d", n)
		}
		return past, nil
	}
	capturePaneFn = func(_ string, _ int) (string, error) { return buildClaudeFreshPane(), nil }
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

	nudgeSilentPaneAfter("sess:0.0", "claude", t.TempDir(), "thrum inbox --unread")

	got := sent.Load()
	if got == nil || got.(string) != "sess:0.0|thrum inbox --unread" {
		t.Errorf("expected nudge after transient errors recovered, got %v", got)
	}
}

// TestNudgeSilentPaneAfter_PersistentActivityErrors verifies the watchdog
// exits without nudging when activity errors persist past the threshold.
func TestNudgeSilentPaneAfter_PersistentActivityErrors(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	tmuxLastActivityFn = func(_ string) (time.Time, error) {
		return time.Time{}, fmt.Errorf("target gone")
	}
	capturePaneFn = func(_ string, _ int) (string, error) { return "", nil }
	sleepFn = func(time.Duration) {}

	var sendCount atomic.Int32
	sendKeysFn = func(_, _ string) error {
		sendCount.Add(1)
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	nudgeSilentPaneAfter("sess:0.0", "claude", t.TempDir(), "thrum inbox --unread")

	if got := sendCount.Load(); got != 0 {
		t.Errorf("expected no nudge when activity errors persist; got %d", got)
	}
}

// TestNudgeSilentPaneAfter_UnknownRuntime verifies watchdog degrades gracefully
// for a runtime with no preset (no anchor/spinner regexes). The conservative
// paneAgentEngaged default (no bottom anchor → true → no nudge) applies.
func TestNudgeSilentPaneAfter_UnknownRuntime(t *testing.T) {
	prevCapture, prevSend, prevSpecial, prevSleep, prevActivity :=
		capturePaneFn, sendKeysFn, sendSpecialKeyFn, sleepFn, tmuxLastActivityFn
	t.Cleanup(func() {
		capturePaneFn = prevCapture
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevSpecial
		sleepFn = prevSleep
		tmuxLastActivityFn = prevActivity
	})

	// Fresh pane but from an unknown runtime — no regexes → conservative, no nudge.
	tmuxLastActivityFn = stableActivity()
	capturePaneFn = func(_ string, _ int) (string, error) { return buildClaudeFreshPane(), nil }
	sleepFn = func(time.Duration) {}

	var sendCount atomic.Int32
	sendKeysFn = func(_, _ string) error {
		sendCount.Add(1)
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	nudgeSilentPaneAfter("sess:0.0", "unknown-runtime-xyz", t.TempDir(), "thrum inbox --unread")

	if got := sendCount.Load(); got != 0 {
		t.Errorf("expected no nudge for unknown runtime (conservative); got %d send-keys calls", got)
	}
}
