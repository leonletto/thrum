package permission

import (
	"regexp"
	"strings"
)

// trustGateGenericRE matches the runtime-agnostic shape of a first-
// launch trust/safety gate: a "1. Yes" / "2. No" option pair PLUS the
// word "trust" (case-insensitive) appearing in the same captured window.
// Either order is acceptable: trust phrase before or after the options.
// (?is) = case-insensitive + dotall. The two ordering branches are
// distinct so we don't accidentally match a non-trust panel that
// merely contains "1. Yes / 2. No" alongside an unrelated mention of
// "trust" elsewhere — the proximity constraint matters.
var trustGateGenericRE = regexp.MustCompile(
	`(?is)1\.\s*Yes[^\n]*\n[^\n]*2\.\s*No.{0,400}trust|` +
		`trust.{0,400}1\.\s*Yes[^\n]*\n[^\n]*2\.\s*No`,
)

// codexTrustExactRE: per-runtime defensive precision for codex's
// observed first-launch trust dialog. "Do you trust the contents of
// this directory" is the exact prompt; matching this string is a
// strong positive signal even if option shape drifts.
var codexTrustExactRE = regexp.MustCompile(`(?i)Do you trust the contents of this directory`)

// claudeTrustExactRE: per-runtime defensive precision for claude's
// observed first-launch trust dialog. "Quick safety check" is the
// header; "trust this folder" appears in the body; both anchored
// together to suppress false positives where one phrase appears in
// unrelated content. (?is) for case-insensitive + dotall so the two
// phrases may live on separate lines in the captured window.
var claudeTrustExactRE = regexp.MustCompile(`(?is)Quick safety check.{0,500}trust this folder`)

// paneBottomMatchLines caps detection to the last N lines of the
// captured pane tail. An ACTIVE permission prompt sits at the bottom
// of the terminal — once the user answers, the prompt scrolls up as
// new content (tool output, agent turns) appears below it. Matching
// the full 30-line capture would keep detecting the scrolled-up
// prompt text for many seconds after resolution, driving OnDetection
// to repeatedly delete+recreate the pending_nudge row (its
// hash-change-as-new-prompt branch treats post-approval tail drift
// as a fresh prompt). 15 lines accommodates the longest runtime
// prompt (Claude's 3-option Variant A is ~6 lines) with headroom
// for leading tool-call context, while ensuring a handful of
// post-approval output lines is enough to push the resolved prompt
// out of the detection window so OnRecovery can clean up the row.
// See thrum-k4wf for the spam-loop incident this constant prevents.
const paneBottomMatchLines = 15

// DetectPaneState is the top-level entry point used by the CLI
// `thrum tmux check-pane` command. It consults the per-runtime
// pattern library.
//
// Return value encodes the detection result for the tmux.check-pane
// RPC:
//
//   - ""                               → no prompt detected (idle path)
//   - "permission:<runtime>.<name>"    → pattern matched; daemon can
//     look up the pattern via
//     Match() for nudge formatting.
//
// Unknown runtime (empty or not in the library) also returns empty,
// preserving the current "idle" behavior for agents that haven't had
// their runtime populated in the identity file yet.
//
// Matching is scoped to the bottom `paneBottomMatchLines` lines of
// paneContent (thrum-k4wf). Shorter panes are matched in full.
func DetectPaneState(runtime, paneContent string) string {
	if runtime == "" || paneContent == "" {
		return ""
	}
	m := Match(runtime, bottomLines(paneContent, paneBottomMatchLines))
	if m == nil {
		return ""
	}
	return "permission:" + runtime + "." + m.Name
}

// IsTrustGate reports whether the captured pane shows a first-launch
// trust / safety gate where automated keystroke injection would either
// (a) be typed into the trust dialog as garbage and approve/deny it
// without the user's intent, or (b) cause the runtime to interpret the
// keystroke as a quit signal and exit. Two recognition modes:
//
//  1. Generic — matches when the bottom of the pane contains a 1.Yes /
//     2.No option pair AND (case-insensitive) the word "trust". This
//     shape is shared across known runtimes (codex, claude) and is
//     durable against minor UI text drift.
//
//  2. Per-runtime exact — defensive precision against false positives
//     in unrelated panes that happen to contain "1. Yes / 2. No / trust"
//     as plain text. When runtime is "codex" or "claude", the exact
//     dialog phrase is checked as a second positive signal.
//
// Used by the four keystroke-injection sites in internal/daemon/rpc/tmux.go
// (waitForPaneReady, emitIdentityBanner, the post-readiness /thrum:prime
// send, and nudgeSilentPaneAfter) to gate SendKeys on a "safe to type"
// decision. Trust dialogs are NOT routed through the permission-prompt
// supervisor pipeline — they're a different class with no automated
// answer, so this detector is intentionally separate from the per-runtime
// Pattern library that drives OnDetection. See thrum-puhr.10 cluster 8.
//
// Empty paneContent returns false. Empty runtime is supported — generic
// detection still runs (covers the case where the daemon hasn't populated
// idFile.Runtime yet, e.g. pre-launch).
func IsTrustGate(runtime, paneContent string) bool {
	if paneContent == "" {
		return false
	}
	window := bottomLines(paneContent, paneBottomMatchLines)
	if trustGateGenericRE.MatchString(window) {
		return true
	}
	switch runtime {
	case "codex":
		return codexTrustExactRE.MatchString(window)
	case "claude":
		return claudeTrustExactRE.MatchString(window)
	}
	return false
}

// IsPaneSafeToType returns true when automated keystroke injection
// into the captured pane is safe — i.e. there is no detected
// permission prompt and no trust gate. Combines DetectPaneState (the
// per-runtime supervisor-routed prompt detector) with IsTrustGate (the
// runtime-agnostic / lightly-runtime-tagged trust dialog detector).
//
// This is the single chokepoint the four keystroke-injection sites
// should consult before SendKeys. Returning false MUST cause the caller
// to skip the inject AND log a structured info-level message that
// names the site, so operators can correlate "no banner / no prime"
// outcomes with the safety gate that suppressed them.
func IsPaneSafeToType(runtime, paneContent string) bool {
	if DetectPaneState(runtime, paneContent) != "" {
		return false
	}
	if IsTrustGate(runtime, paneContent) {
		return false
	}
	return true
}

// bottomLines returns the last n lines of content. If content has
// n or fewer lines it is returned unchanged. CRLF is normalized to LF
// before counting so a pane captured with Windows-style line endings
// (rare but possible via remote ssh transports) doesn't silently
// double the line count and under-slice.
//
// A single trailing newline on the input is stripped before counting
// so a tmux capture that happens to end with "\n" doesn't waste a
// slot on the phantom empty element produced by strings.Split —
// ensuring the window is exactly n lines of real content. If the
// input had a trailing newline we preserve it on output too, so
// multi-line regex anchors (`(?m)^...$`) behave identically to the
// full-content path.
//
//nolint:unparam // n is always paneBottomMatchLines today, but keeping it explicit makes tests readable and future tuning easy.
func bottomLines(content string, n int) string {
	if n <= 0 {
		return content
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	trailingNL := strings.HasSuffix(content, "\n")
	if trailingNL {
		content = content[:len(content)-1]
	}
	lines := strings.Split(content, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.Join(lines, "\n")
	if trailingNL {
		out += "\n"
	}
	return out
}
