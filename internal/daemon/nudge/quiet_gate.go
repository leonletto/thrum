package nudge

// quiet_gate.go — chrome-quiet gate for inbound-message nudges (thrum-nlel /
// thrum-3i2s). A nudge types text + Enter into the recipient's tmux pane. If it
// fires while the human is typing in the bottom input chrome, it fragments their
// in-progress message. This gate defers the nudge until the input chrome is
// quiet, WITHOUT delaying nudges while the agent is merely busy generating.
//
// WHY THIS PREDICATE (vs the two alternatives we rejected):
//   - The 2026-05-15 brainstorm proposed a GLOBAL pane-silence gate over
//     `tmux #{window_activity}`. It fails because a rendering spinner bumps
//     window_activity every ~1s, so a busy-but-not-typed pane never looks
//     "silent" and the nudge waits the full deadline — i.e. it blocks nudges
//     during agent activity, the exact failure thrum-nlel calls out. SUPERSEDED.
//   - Per-runtime chrome-REGION hashing (isolate the input box, hash it) is
//     fragile: every runtime's input/footer layout differs and footer/tips lines
//     rotate, producing false "still changing" reads.
//
// The predicate here gates window_activity behind "no spinner rendered", which
// makes window_activity meaningful again: while a spinner is up the agent owns
// the turn (fire immediately, spinner-permissive); with no spinner, the only
// thing bumping activity on an idle-awaiting-input pane is the human typing.
// A generous bottom-region content-stability check backstops the idle case so a
// pane that periodically self-redraws (cursor/footer) still fires promptly
// instead of waiting for the deadline. CRUCIALLY, neither fire-path can trip
// mid-typing: typing bumps window_activity AND mutates the bottom region, so
// both "quiet" signals stay false while a key is being pressed. The worst case
// for an idle pane that redraws its bottom region is a *delayed* nudge bounded
// by the dispatch deadline — never a mid-keystroke interrupt.

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/permission"
	trt "github.com/leonletto/thrum/internal/runtime"
	ttmux "github.com/leonletto/thrum/internal/tmux"
)

// nudgeDecision is the outcome of the chrome-quiet gate.
type nudgeDecision int

const (
	nudgeFire  nudgeDecision = iota // safe to type now — send the nudge
	nudgeDefer                      // pane shows an interactive dialog — hand to the 7phu deferred queue
	nudgeDrop                       // ctx cancelled (daemon shutdown) — drop the pane-poke (spool still carries the message)
)

const (
	// nudgeQuietPollInterval is how often the gate re-samples the pane while
	// waiting for the chrome to quiet.
	nudgeQuietPollInterval = 400 * time.Millisecond
	// nudgeChromeTailLines is how many bottom lines form the "input chrome"
	// region for the stability backstop. Generous on purpose: it must always
	// include the input box so a typed character changes the region (preventing
	// a mid-typing false-fire). Footer/tips rotation inside this window only
	// causes a bounded *delay*, never an interrupt.
	nudgeChromeTailLines = 10
)

// Test seams (mirror internal/tmux/nudge.go's pattern): production points at the
// real tmux + clock; tests substitute fakes to drive the poll loop deterministically.
var (
	lastActivityFn = ttmux.LastActivity
	timeNowFn      = time.Now
	sleepFn        = realSleepCtx
	spinnerReFn    = presetSpinnerRe
)

// paneQuietForNudge polls target until its input chrome is quiet, then returns
// nudgeFire. Returns nudgeDefer if an interactive dialog is up (compose with the
// thrum-7phu deferred queue), nudgeDrop if ctx is cancelled. Degrades to
// nudgeFire (prior unconditional behavior) on a capture error or when the gate
// is disabled via config.
func paneQuietForNudge(ctx context.Context, thrumDir, target, runtime string) nudgeDecision {
	silenceSec, deadlineSec, enabled := loadNudgeGate(thrumDir)
	silence := time.Duration(silenceSec) * time.Second
	spinnerRe := spinnerReFn(runtime)
	deadlineAt := timeNowFn().Add(time.Duration(deadlineSec) * time.Second)

	var lastChrome string
	var chromeStableSince time.Time
	haveChrome := false

	for {
		if ctx.Err() != nil {
			return nudgeDrop
		}
		content, err := capturePaneFn(target, capturePaneLines)
		if err != nil {
			return nudgeFire // capture failure is rare; preserve prior notify-anyway behavior
		}
		// thrum-7phu compose (re-checked every poll): an interactive dialog
		// (permission/trust/AskUserQuestion) must defer, never type-through.
		if !permission.IsPaneSafeToType(runtime, content) {
			return nudgeDefer
		}
		if !enabled {
			return nudgeFire
		}
		now := timeNowFn()

		// Spinner-permissive: the agent owns the turn → fire immediately.
		if containsSpinner(content, spinnerRe) {
			return nudgeFire
		}
		// Fast idle path: whole pane silent long enough. Typing always bumps
		// window_activity, so a silent pane is definitively not being typed into.
		if act, aerr := lastActivityFn(target); aerr == nil && now.Sub(act) >= silence {
			return nudgeFire
		}
		// Chrome-stability backstop: the bottom input region has not changed for
		// the quiet window. Generous tail guarantees the input box is included,
		// so this cannot fire mid-keystroke (a typed char mutates the region).
		// This is NOT the thrum-wefd byte-equality anti-pattern: that compared
		// the WHOLE pane and was defeated by the ~1Hz spinner animation making
		// consecutive captures never byte-equal. Here the spinner-present check
		// above returns FIRST whenever a spinner is animating, so this comparison
		// only runs on a spinner-less pane — an animated spinner can never reach
		// (or defeat) it.
		chrome := bottomLines(content, nudgeChromeTailLines)
		if !haveChrome || chrome != lastChrome {
			lastChrome = chrome
			chromeStableSince = now
			haveChrome = true
		}
		if now.Sub(chromeStableSince) >= silence {
			return nudgeFire
		}
		// Deadline cap: a continuously-active pane still gets notified.
		if !now.Before(deadlineAt) {
			return nudgeFire
		}
		if !sleepFn(ctx, nudgeQuietPollInterval) {
			return nudgeDrop
		}
	}
}

// loadNudgeGate resolves the configured chrome-quiet + deadline windows.
func loadNudgeGate(thrumDir string) (silenceSec, deadlineSec int, enabled bool) {
	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil || cfg == nil {
		// Defaults on load failure (LoadThrumConfig already returns defaults when
		// no file exists; this guards a genuine read/parse error).
		return config.NudgeConfig{}.SilenceGate()
	}
	return cfg.Nudge.SilenceGate()
}

// containsSpinner reports whether any line of content matches the runtime's
// spinner regex (agent actively generating). nil regex (unknown runtime) → false,
// so a spinner-less runtime falls through to the activity/chrome paths.
func containsSpinner(content string, spinnerRe *regexp.Regexp) bool {
	if spinnerRe == nil {
		return false
	}
	for l := range strings.SplitSeq(content, "\n") {
		if spinnerRe.MatchString(strings.TrimSpace(l)) {
			return true
		}
	}
	return false
}

// bottomLines returns the last n non-blank-trailing lines of content, joined.
// Trailing blank lines are dropped first so the "bottom" tracks the real input
// region rather than terminal padding. CRLF is normalized to LF first (matching
// permission/detect.go's bottomLines): a CRLF pane (SSH / remote / Windows
// terminal — core thrum multi-host use cases) would otherwise carry a trailing
// \r that makes the stability comparison see a spurious diff and never go quiet,
// delaying every remote nudge to the full deadline.
func bottomLines(content string, n int) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := max(end-n, 0)
	return strings.Join(lines[start:end], "\n")
}

// presetSpinnerRe resolves the per-runtime spinner regex, or nil on an unknown
// runtime (degrades to activity/chrome gating).
func presetSpinnerRe(runtime string) *regexp.Regexp {
	if runtime == "" {
		return nil
	}
	preset, err := trt.GetPreset(runtime)
	if err != nil {
		return nil
	}
	return preset.SpinnerRegex
}

// realSleepCtx sleeps for d unless ctx is cancelled first; returns false if
// cancelled.
func realSleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
