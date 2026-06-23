package rpc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordedCall captures a single sendKeysFn / sendSpecialKeyFn invocation
// so tests can assert call shape and ordering.
type recordedCall struct {
	kind string // "send" (SendKeys text) or "enter" (SendSpecialKey)
	text string
}

// recordSendKeys swaps in fakes for sendKeysFn / sendSpecialKeyFn that
// append every invocation to a shared slice. Returns a cleanup the
// caller MUST register with t.Cleanup, plus a getter that reads the
// accumulated sequence under the same mutex the closure uses.
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
	// between text and Enter. Mock so the test doesn't burn 200ms+ per
	// banner emit and doesn't depend on real time.
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

// installReadyPane installs fakes that make waitForPaneReady's silence
// loop fire on the first probe and capturePaneFn return a safe-to-type
// pane (plain shell prompt). Returns the cleanup so callers register it
// with t.Cleanup.
func installReadyPane(t *testing.T) func() {
	t.Helper()
	prevActivity := tmuxLastActivityFn
	prevCapture := capturePaneFn

	past := time.Now().Add(-(silenceThreshold + time.Second))
	tmuxLastActivityFn = func(_ string) (time.Time, error) { return past, nil }
	capturePaneFn = func(_ string, _ int) (string, error) { return "$ \n", nil }

	return func() {
		tmuxLastActivityFn = prevActivity
		capturePaneFn = prevCapture
	}
}

// disableWatchdog writes a thrum config that turns the silence watchdog
// off (silence_watchdog_seconds=-1) so nudgeSilentPaneAfter is a no-op
// inside runPostLaunchInject's tail. Otherwise the watchdog would fire
// its own SendKeys and pollute the assertion sequence. Returns the
// thrumDir so callers can hand it to NewTmuxHandler.
func disableWatchdog(t *testing.T) string {
	t.Helper()
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"),
		[]byte(`{"restart":{"silence_watchdog_seconds":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return thrumDir
}

// TestRunPostLaunchInject_HookRuntime_EmitsBannerAfterReady pins the
// post-revert invariant: for hook runtimes the identity banner is
// emitted via emitIdentityBanner ONLY after waitForPaneReady reports
// the pane is input-ready. Banner SendKeys lands as a user-message
// into the running runtime's input prompt; the runtime responds with
// the banner text which is what reaches the captured pane.
//
// Parametrized over every built-in hook runtime so a preset
// misconfiguration that flips HasSessionStartHook on one of them is
// caught here.
func TestRunPostLaunchInject_HookRuntime_EmitsBannerAfterReady(t *testing.T) {
	for _, runtime := range []string{"claude", "codex", "cursor"} {
		t.Run(runtime, func(t *testing.T) {
			cwd := t.TempDir()
			writeTestIdentityFile(t, cwd, "impl_test", 0, "")

			restoreSend, calls := recordSendKeys(t)
			t.Cleanup(restoreSend)
			t.Cleanup(installReadyPane(t))
			thrumDir := disableWatchdog(t)

			h := NewTmuxHandler(thrumDir, nil)
			h.sessionMu.Lock()
			h.sessionCwds = map[string]string{"sess": cwd}
			h.sessionMu.Unlock()

			h.runPostLaunchInject("launch", "sess", "sess:0.0", runtime, "nudge")

			seq := calls()
			if len(seq) == 0 {
				t.Fatalf("expected banner SendKeys but no calls recorded — runPostLaunchInject early-bailed")
			}
			// Banner emission's first SendKeys carries the printf payload
			// — that's the proof the post-readiness banner emit fired.
			if seq[0].kind != "send" || !strings.HasPrefix(seq[0].text, "printf ") {
				t.Fatalf("expected first call to be banner printf SendKeys, got %+v", seq[0])
			}
			if !strings.Contains(seq[0].text, "impl_test") {
				t.Errorf("banner SendKeys missing agent name; got %q", seq[0].text)
			}
		})
	}
}

// TestRunPostLaunchInject_NonHookRuntime_SendsPrime: non-hook runtimes
// (opencode / auggie / amp / gemini / kiro-cli) don't have a
// SessionStart hook to auto-inject the briefing, so runPostLaunchInject
// fires `/thrum:prime` instead of the banner. Asserts the prime payload
// reaches sendKeysFn after readiness.
func TestRunPostLaunchInject_NonHookRuntime_SendsPrime(t *testing.T) {
	cwd := t.TempDir()
	writeTestIdentityFile(t, cwd, "impl_test", 0, "")

	restoreSend, calls := recordSendKeys(t)
	t.Cleanup(restoreSend)
	t.Cleanup(installReadyPane(t))
	thrumDir := disableWatchdog(t)

	h := NewTmuxHandler(thrumDir, nil)
	h.sessionMu.Lock()
	h.sessionCwds = map[string]string{"sess": cwd}
	h.sessionMu.Unlock()

	h.runPostLaunchInject("launch", "sess", "sess:0.0", "opencode", "nudge")

	seq := calls()
	if len(seq) == 0 {
		t.Fatal("expected /thrum:prime SendKeys but no calls recorded")
	}
	if !strings.Contains(seq[0].text, "thrum") && !strings.Contains(seq[0].text, "prime") {
		t.Errorf("expected first call to look like a /thrum:prime payload; got %+v", seq[0])
	}
}

// TestRunPostLaunchInject_NotReady_NoSendKeys: when waitForPaneReady
// returns false (e.g. pane stuck on a trust gate or never settled),
// runPostLaunchInject bails out with zero keystrokes. Critical safety
// invariant: never type into a dialog or a not-yet-rendered TUI.
func TestRunPostLaunchInject_NotReady_NoSendKeys(t *testing.T) {
	cwd := t.TempDir()
	writeTestIdentityFile(t, cwd, "impl_test", 0, "")

	restoreSend, calls := recordSendKeys(t)
	t.Cleanup(restoreSend)
	thrumDir := disableWatchdog(t)

	// No installReadyPane: simulate a never-silent pane that blows the
	// readiness ceiling, plus a trust-gate capture so the post-settle
	// safety check trips. shrunk via a synthetic clock — sleepFn is
	// already mocked by recordSendKeys.
	prevActivity, prevCapture, prevNow := tmuxLastActivityFn, capturePaneFn, timeNowFn
	t.Cleanup(func() {
		tmuxLastActivityFn = prevActivity
		capturePaneFn = prevCapture
		timeNowFn = prevNow
	})

	base := time.Now()
	tickSec := 0
	timeNowFn = func() time.Time { return base.Add(time.Duration(tickSec) * time.Second) }
	tmuxLastActivityFn = func(_ string) (time.Time, error) {
		tickSec++
		return timeNowFn(), nil // never silent
	}
	// A trust-gate-like pane string ensures the final-capture safety
	// check (which fires at the ceiling) classifies as unsafe-to-type
	// so waitForPaneReady returns false.
	capturePaneFn = func(_ string, _ int) (string, error) {
		return "Do you trust the contents of this directory?\n  1. Yes\n  2. No", nil
	}

	h := NewTmuxHandler(thrumDir, nil)
	h.sessionMu.Lock()
	h.sessionCwds = map[string]string{"sess": cwd}
	h.sessionMu.Unlock()

	h.runPostLaunchInject("launch", "sess", "sess:0.0", "claude", "nudge")

	if got := calls(); len(got) != 0 {
		t.Errorf("expected zero send-keys when pane is in trust-gate state; got %d: %+v", len(got), got)
	}
}

// --- populateSessionCwdFromIdentity tests ---
//
// Defensive sessionCwds populate for externally-created tmux sessions
// (raw `tmux new-session` + `thrum tmux launch`, where HandleCreate's
// canonical populate at tmux.go:352 never ran). Without this populate,
// runPostLaunchInject's emitIdentityBanner bails out with map_hit=false
// — no banner, no /thrum:prime, no SessionStart hook post-restart
// loud preamble.

// makePopulateHandler builds a TmuxHandler rooted at a fresh thrumDir =
// $worktree/.thrum, mirroring the production layout where AllIdentityDirs
// searches $thrumDir/identities relative to its parent. Returns the
// handler + the worktree dir (= the absolute path resolveWorktreePath
// will produce) + a cleanup. The optional `tmuxSession` in the identity
// file is what findIdentityForSession matches against.
func makePopulateHandler(t *testing.T, agentName, tmuxSession string) (*TmuxHandler, string) {
	t.Helper()
	worktree := t.TempDir()
	thrumDir := filepath.Join(worktree, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if agentName != "" {
		writeTestIdentityFile(t, worktree, agentName, 0, tmuxSession)
	}
	return NewTmuxHandler(thrumDir, nil), worktree
}

// TestPopulateSessionCwdFromIdentity_PopulatesWhenAbsent: the dominant
// case — sessionCwds[name] is empty and a matching identity file exists.
// The helper looks up the identity, resolves the worktree, and writes
// the binding. Pins the fix for v0.10.6 RC1 scen 03 root cause.
func TestPopulateSessionCwdFromIdentity_PopulatesWhenAbsent(t *testing.T) {
	h, worktree := makePopulateHandler(t, "coord", "sess:0.0")

	populated := h.populateSessionCwdFromIdentity(context.Background(), "sess")
	if !populated {
		t.Fatal("expected populated=true when sessionCwds is empty and identity is present")
	}
	h.sessionMu.RLock()
	got, ok := h.sessionCwds["sess"]
	h.sessionMu.RUnlock()
	if !ok || got != worktree {
		t.Fatalf("expected sessionCwds[sess]=%q after populate; got ok=%v val=%q", worktree, ok, got)
	}
	// cwdSessions side of the binding too — RestoreBinding writes both.
	h.sessionMu.RLock()
	gotSess, ok := h.cwdSessions[worktree]
	h.sessionMu.RUnlock()
	if !ok || gotSess != "sess" {
		t.Fatalf("expected cwdSessions[%q]=sess after populate; got ok=%v val=%q", worktree, ok, gotSess)
	}
}

// TestPopulateSessionCwdFromIdentity_NoOpWhenAlreadySet: HandleCreate
// already populated the binding for thrum-managed sessions. The helper
// MUST NOT overwrite — preserves the canonical populate byte-for-byte.
func TestPopulateSessionCwdFromIdentity_NoOpWhenAlreadySet(t *testing.T) {
	h, worktree := makePopulateHandler(t, "coord", "sess:0.0")
	const preExistingCwd = "/some/other/preexisting/cwd"
	h.sessionMu.Lock()
	h.sessionCwds["sess"] = preExistingCwd
	h.sessionMu.Unlock()

	populated := h.populateSessionCwdFromIdentity(context.Background(), "sess")
	if populated {
		t.Fatal("expected populated=false when sessionCwds[sess] already set")
	}
	h.sessionMu.RLock()
	got := h.sessionCwds["sess"]
	h.sessionMu.RUnlock()
	if got != preExistingCwd {
		t.Fatalf("expected sessionCwds[sess] preserved at %q; got %q (helper overwrote!)", preExistingCwd, got)
	}
	// Should also not have established the unwanted worktree binding.
	if _, exists := func() (string, bool) {
		h.sessionMu.RLock()
		defer h.sessionMu.RUnlock()
		v, ok := h.cwdSessions[worktree]
		return v, ok
	}(); exists {
		t.Errorf("helper should not have written cwdSessions[worktree] when it skipped the populate")
	}
}

// TestPopulateSessionCwdFromIdentity_NoIdentityFound: no identity file
// matches the session name. Helper returns false; map stays empty.
// Outer safety net (emitIdentityBanner's skip-on-empty) is still the
// final guard in this case.
func TestPopulateSessionCwdFromIdentity_NoIdentityFound(t *testing.T) {
	// No identity file at all.
	h, _ := makePopulateHandler(t, "", "")

	populated := h.populateSessionCwdFromIdentity(context.Background(), "sess")
	if populated {
		t.Fatal("expected populated=false when no identity file is present")
	}
	h.sessionMu.RLock()
	_, ok := h.sessionCwds["sess"]
	h.sessionMu.RUnlock()
	if ok {
		t.Error("expected sessionCwds[sess] to remain absent when no identity matched")
	}
}

// TestPopulateSessionCwdFromIdentity_TmuxSessionMismatch: an identity
// file exists but its TmuxSession field doesn't match the queried name.
// findIdentityForSession returns nothing; the helper returns false.
// Guards against accidental cross-pollution between adjacent agents.
func TestPopulateSessionCwdFromIdentity_TmuxSessionMismatch(t *testing.T) {
	h, _ := makePopulateHandler(t, "coord", "different-session:0.0")

	populated := h.populateSessionCwdFromIdentity(context.Background(), "sess")
	if populated {
		t.Fatal("expected populated=false when identity's TmuxSession field doesn't match the queried name")
	}
	h.sessionMu.RLock()
	_, ok := h.sessionCwds["sess"]
	h.sessionMu.RUnlock()
	if ok {
		t.Error("expected sessionCwds[sess] to remain absent on TmuxSession mismatch")
	}
}
