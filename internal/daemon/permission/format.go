package permission

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// maxPaneTailLines caps how many lines of pane content we include in
	// the nudge body. thrum-7khf trimmed this from 15 → 5: the previous
	// cap captured approval-irrelevant UI chrome (separators, shortcut
	// hints, "❯ 1. Yes / 2. No" selector lines) that buried the actual
	// command. Five lines is enough for "<command>\n<reason>" plus a
	// little context without being mobile-hostile on Telegram.
	maxPaneTailLines = 5

	// maxPaneTailBytes is a hard byte cap. Still 2KB — a single
	// multi-arg bash line can exceed the line cap in bytes.
	maxPaneTailBytes = 2_000

	// maxReminderCount is the total number of nudges (first-detect + 5
	// reminders) before the scheduler gives up and marks the agent stuck.
	// Surfaced here so the rendered "reminder N/6" footer stays in sync
	// with the scheduler cadence.
	maxReminderCount = 6
)

// FormatNudge renders a compact nudge body (thrum-7khf). Structure:
//
//	⚠ @<agent> · <session> (<runtime>)
//
//	  <pane tail, max 5 lines, indented>
//
//	Reply: y (approve) · n (deny) · or thrum tmux send <session> "<a>"|"<d>"
//	(reminder N/6 · <repo> · <pattern> · <N> ago)
//
// Design goals:
//   - Glanceable on Telegram mobile (≤10 lines for typical prompts)
//   - Decision-first: command + reply hint above the fold
//   - Operator-debugging fields (repo / pattern / first-seen / reminder
//     count) collapsed into a single trailing footer line
//   - Backwards-compatible: the y/n reply path, tmux keystroke command,
//     and msgMap keys are unchanged.
//
// Pure function — no I/O, safe to test with golden fixtures.
//
// Parameters:
//   - row         snapshot of the permission_nudges row being announced.
//   - paneTail    raw captured pane content; this function truncates.
//   - runtime     runtime name (e.g. "cursor") for the header parens.
//   - projectName repo name for the footer metadata.
//   - now         injected current time so tests can pin "N ago" output.
func FormatNudge(row *NudgeRow, paneTail, runtime, projectName string, now time.Time) string {
	var b strings.Builder

	// Header: agent · session (runtime)
	fmt.Fprintf(&b, "⚠ @%s · %s (%s)\n\n", row.AgentName, row.Session, runtime)

	// Pane tail (indented for visual grouping, max 5 lines)
	trimmed := truncatePaneTail(paneTail)
	if trimmed != "" {
		b.WriteString(indentLines(trimmed, "  "))
		b.WriteString("\n\n")
	}

	// Reply line: one-line action hint covering reply-text, tmux-send,
	// and (when no deny key) Ctrl+C interrupt.
	if row.DenyKey != "" {
		fmt.Fprintf(&b,
			"Reply: y (approve) · n (deny) · or thrum tmux send %s %q|%q\n",
			row.Session, row.ApproveKey, row.DenyKey)
	} else {
		fmt.Fprintf(&b,
			"Reply: y (approve) · or thrum tmux send %s %q (Ctrl+C in pane to interrupt)\n",
			row.Session, row.ApproveKey)
	}

	// Footer: metadata the approver rarely needs to read, kept inline
	// for debugging / audit.
	reminder := row.NudgeCount
	if reminder < 1 {
		reminder = 1
	}
	fmt.Fprintf(&b, "(reminder %d/%d · %s · %s · %s ago)\n",
		reminder, maxReminderCount,
		projectName, row.PatternKey,
		friendlyDuration(now.Sub(row.FirstDetected)))

	return b.String()
}

// truncatePaneTail caps the pane content at maxPaneTailLines lines AND
// maxPaneTailBytes bytes, preferring the HEAD of the capture.
//
// thrum-7khf: the daemon hands us the last ~15 lines of the tmux pane,
// which for a typical permission prompt contains (top→bottom):
// command, reason, permission-rule noise, "Do you want to proceed?",
// the selector ("❯ 1. Yes / 2. No"), and shortcut hints. The approval-
// relevant content (command + reason) sits at the top; the selector +
// chrome sits at the bottom. Keeping the HEAD N lines therefore surfaces
// the decision info and drops the chrome. For shorter prompts (cursor,
// opencode) the capture fits within the cap and no truncation occurs.
//
// The byte-cap branch keeps the HEAD bytes and walks back to the
// previous rune boundary if a mid-rune split lands there, so a single
// >2KB line containing multi-byte runes (e.g. a long URL with arrows,
// a base64 blob with Unicode punctuation) cannot emit invalid UTF-8
// into the nudge body.
func truncatePaneTail(pane string) string {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	if len(lines) > maxPaneTailLines {
		lines = lines[:maxPaneTailLines]
	}
	out := strings.Join(lines, "\n")
	if len(out) > maxPaneTailBytes {
		out = out[:maxPaneTailBytes]
		// Drop any trailing bytes that form an incomplete UTF-8 rune
		// (mid-rune split at the tail of the retained prefix). A valid
		// rune at the end decodes cleanly; an incomplete one surfaces
		// as RuneError with size=1, so we can pop bytes until the
		// trailing rune decodes.
		for len(out) > 0 {
			r, sz := utf8.DecodeLastRuneInString(out)
			if r != utf8.RuneError || sz != 1 {
				break
			}
			out = out[:len(out)-1]
		}
		// Trim back to the previous newline so we don't end mid-line.
		if nl := strings.LastIndexByte(out, '\n'); nl > -1 {
			out = out[:nl]
		}
	}
	return out
}

// indentLines prefixes every line in s with prefix. Empty input returns
// an empty string (no leading prefix).
func indentLines(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}

// friendlyDuration renders a duration in the most concise human form:
// "42s", "7m", "2h15m". Negative durations are clamped to "0s".
func friendlyDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
