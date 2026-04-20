package permission

import "strings"

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

// bottomLines returns the last n lines of content. If content has
// n or fewer lines it is returned unchanged. A trailing newline is
// preserved if present in the original so multi-line regex anchors
// (`(?m)^...`) behave identically to the full-content path.
func bottomLines(content string, n int) string {
	if n <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= n {
		return content
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
