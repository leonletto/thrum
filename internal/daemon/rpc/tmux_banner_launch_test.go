package rpc

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// recordedCall captures a single sendKeysFn / sendSpecialKeyFn invocation
// so the order test can assert banner-before-launch ordering. The two
// kinds use distinct prefixes so an assertion on the sequence is
// straightforward without an enum.
type recordedCall struct {
	kind string // "send" (SendKeys text) or "enter" (SendSpecialKey Enter)
	text string // SendKeys payload, or "Enter" for enter calls
}

// recordSendKeys swaps in fakes for sendKeysFn / sendSpecialKeyFn that
// append every invocation to a shared slice. Returns a cleanup that the
// caller MUST register with t.Cleanup, plus a getter for the accumulated
// sequence so the test reads it under the same mutex the closure uses.
func recordSendKeys(t *testing.T) (restore func(), calls func() []recordedCall) {
	t.Helper()
	prevSend := sendKeysFn
	prevEnter := sendSpecialKeyFn
	prevSleep := sleepFn

	var mu sync.Mutex
	var seq []recordedCall

	sendKeysFn = func(_, text string) error {
		mu.Lock()
		defer mu.Unlock()
		seq = append(seq, recordedCall{kind: "send", text: text})
		return nil
	}
	sendSpecialKeyFn = func(_, key string) error {
		mu.Lock()
		defer mu.Unlock()
		seq = append(seq, recordedCall{kind: "enter", text: key})
		return nil
	}
	// emitIdentityBanner's sendKeysAndSubmit inserts paneInputSubmitGap
	// between text and Enter. Without a mocked sleepFn this test would
	// take 200ms+ per banner emission and add real-clock dependency.
	sleepFn = func(time.Duration) {}

	restore = func() {
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevEnter
		sleepFn = prevSleep
	}
	calls = func() []recordedCall {
		mu.Lock()
		defer mu.Unlock()
		out := make([]recordedCall, len(seq))
		copy(out, seq)
		return out
	}
	return restore, calls
}

// installSafePaneCapture installs a capturePaneFn that returns a content
// emitIdentityBanner's safety check classifies as safe-to-type (plain
// shell prompt — no trust gate, no permission prompt). Returns the
// cleanup so callers register it with t.Cleanup.
func installSafePaneCapture(t *testing.T) func() {
	t.Helper()
	prev := capturePaneFn
	capturePaneFn = func(_ string, _ int) (string, error) {
		return "$ \n", nil
	}
	return func() { capturePaneFn = prev }
}

// TestLaunchRuntimeWithBanner_HookRuntime_BannerBeforeLaunch pins thrum-8dl3
// Fix #1: for hook runtimes (claude / codex / cursor) the pane-side identity
// banner MUST land at the shell prompt BEFORE the runtime launch keystrokes
// so the PrimeTruncationSentinel ends up in scrollback rather than inside
// the runtime's `❯` input box. Pre-fix the banner emitted post-launch from
// the watchdog goroutine, which silently corrupted the banner and stripped
// the watchdog's top anchor → false-engaged → no nudge → idle agent.
func TestLaunchRuntimeWithBanner_HookRuntime_BannerBeforeLaunch(t *testing.T) {
	cwd := t.TempDir()
	writeTestIdentityFile(t, cwd, "impl_test", 0, "")

	restoreSend, calls := recordSendKeys(t)
	t.Cleanup(restoreSend)
	t.Cleanup(installSafePaneCapture(t))

	h := NewTmuxHandler(t.TempDir(), nil)
	h.sessionMu.Lock()
	h.sessionCwds = map[string]string{"sess": cwd}
	h.sessionMu.Unlock()

	if err := h.launchRuntimeWithBanner("sess", "sess:0.0", "claude", "claude"); err != nil {
		t.Fatalf("launchRuntimeWithBanner: %v", err)
	}

	seq := calls()
	if len(seq) < 3 {
		t.Fatalf("expected banner SendKeys + banner Enter + launch SendKeys; got %d calls: %+v", len(seq), seq)
	}

	// First call must be the banner printf — not the runtime launch.
	if seq[0].kind != "send" || !strings.HasPrefix(seq[0].text, "printf ") {
		t.Fatalf("expected first call to be banner printf send-keys, got %+v", seq[0])
	}
	if !strings.Contains(seq[0].text, "impl_test") {
		t.Errorf("banner SendKeys did not include agent name; got %q", seq[0].text)
	}

	// The launch send-keys ("claude") must come AFTER the banner Enter
	// — otherwise the runtime takes over the pane before the banner
	// printf is submitted and the banner lands in the runtime's input box.
	var launchAt = -1
	for i, c := range seq {
		if c.kind == "send" && c.text == "claude" {
			launchAt = i
			break
		}
	}
	if launchAt < 0 {
		t.Fatalf("launch SendKeys (text=%q) not found in sequence: %+v", "claude", seq)
	}
	if launchAt == 0 {
		t.Fatalf("launch SendKeys appeared FIRST — banner must precede it. Sequence: %+v", seq)
	}
	// Defensive: the banner's Enter must have been submitted before the
	// launch SendKeys so the printf actually runs at the shell prompt.
	sawBannerEnterBeforeLaunch := false
	for i := 0; i < launchAt; i++ {
		if seq[i].kind == "enter" {
			sawBannerEnterBeforeLaunch = true
			break
		}
	}
	if !sawBannerEnterBeforeLaunch {
		t.Errorf("banner Enter was not submitted before the launch SendKeys; the printf would land inside the runtime instead of the shell. Sequence: %+v", seq)
	}
}

// TestLaunchRuntimeWithBanner_NonHookRuntime_LaunchOnly verifies the helper
// does NOT emit the pane-side banner for runtimes whose preset has
// HasSessionStartHook=false. Those runtimes get the `/thrum:prime`
// keystroke from the post-launch goroutine instead — emitting a pre-launch
// banner here would be redundant and noisy.
func TestLaunchRuntimeWithBanner_NonHookRuntime_LaunchOnly(t *testing.T) {
	cwd := t.TempDir()
	writeTestIdentityFile(t, cwd, "impl_test", 0, "")

	restoreSend, calls := recordSendKeys(t)
	t.Cleanup(restoreSend)
	t.Cleanup(installSafePaneCapture(t))

	h := NewTmuxHandler(t.TempDir(), nil)
	h.sessionMu.Lock()
	h.sessionCwds = map[string]string{"sess": cwd}
	h.sessionMu.Unlock()

	if err := h.launchRuntimeWithBanner("sess", "sess:0.0", "opencode", "opencode"); err != nil {
		t.Fatalf("launchRuntimeWithBanner: %v", err)
	}

	seq := calls()
	if len(seq) != 2 {
		t.Fatalf("expected exactly launch SendKeys + Enter (no banner) for non-hook runtime; got %d calls: %+v", len(seq), seq)
	}
	if seq[0].kind != "send" || seq[0].text != "opencode" {
		t.Errorf("expected first call to be launch SendKeys 'opencode'; got %+v", seq[0])
	}
	if seq[1].kind != "enter" {
		t.Errorf("expected second call to be Enter; got %+v", seq[1])
	}
}

// TestLaunchRuntimeWithBanner_EmptyLaunchCmd_NoSendKeys covers the
// shell-runtime path: runtimeToLaunchCmd("shell") returns "" and the
// helper must short-circuit without sending any keystrokes. The shell
// preset has HasSessionStartHook=false so no banner emits either.
func TestLaunchRuntimeWithBanner_EmptyLaunchCmd_NoSendKeys(t *testing.T) {
	cwd := t.TempDir()
	writeTestIdentityFile(t, cwd, "impl_test", 0, "")

	restoreSend, calls := recordSendKeys(t)
	t.Cleanup(restoreSend)
	t.Cleanup(installSafePaneCapture(t))

	h := NewTmuxHandler(t.TempDir(), nil)
	h.sessionMu.Lock()
	h.sessionCwds = map[string]string{"sess": cwd}
	h.sessionMu.Unlock()

	if err := h.launchRuntimeWithBanner("sess", "sess:0.0", "shell", ""); err != nil {
		t.Fatalf("launchRuntimeWithBanner: %v", err)
	}

	if got := calls(); len(got) != 0 {
		t.Errorf("expected zero send-keys for empty launchCmd + non-hook shell runtime; got: %+v", got)
	}
}

// TestLaunchRuntimeWithBanner_HookRuntime_MissingIdentity_StillLaunches:
// the helper degrades gracefully when no identity file is present in the
// stored cwd — emitIdentityBanner silently no-ops, and the launch still
// proceeds. Important for the "session created with --no-agent" path.
func TestLaunchRuntimeWithBanner_HookRuntime_MissingIdentity_StillLaunches(t *testing.T) {
	cwd := t.TempDir() // no identity file written

	restoreSend, calls := recordSendKeys(t)
	t.Cleanup(restoreSend)
	t.Cleanup(installSafePaneCapture(t))

	h := NewTmuxHandler(t.TempDir(), nil)
	h.sessionMu.Lock()
	h.sessionCwds = map[string]string{"sess": cwd}
	h.sessionMu.Unlock()

	if err := h.launchRuntimeWithBanner("sess", "sess:0.0", "claude", "claude"); err != nil {
		t.Fatalf("launchRuntimeWithBanner: %v", err)
	}

	seq := calls()
	// No banner (no identity file) but the launch still fires.
	if len(seq) != 2 {
		t.Fatalf("expected launch SendKeys + Enter (banner skipped, no identity); got %d: %+v", len(seq), seq)
	}
	if seq[0].kind != "send" || seq[0].text != "claude" {
		t.Errorf("expected first call to be launch SendKeys 'claude' when banner is skipped; got %+v", seq[0])
	}
}
