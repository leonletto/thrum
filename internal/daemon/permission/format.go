package permission

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxPaneTailLines caps how many lines of pane content we include in a
	// nudge. Balances context-richness against Telegram's ~4KB DM limit.
	maxPaneTailLines = 15

	// MaxPaneTailBytes is a hard byte cap to keep the final body under
	// ~3KB of text with room for headers.
	maxPaneTailBytes = 2_000

	// MaxReminderCount is the total number of nudges (first-detect + 5
	// reminders) before the scheduler gives up and marks the agent stuck.
	// Surfaced here so the rendered "Reminder #N of 6" header stays in
	// sync with the scheduler cadence.
	maxReminderCount = 6
)

// FormatNudge renders the rich nudge body described in spec §5. It is a
// pure function — no I/O, safe to test with golden fixtures.
//
// Parameters:
//   - row         snapshot of the permission_nudges row being announced.
//   - paneTail    raw captured pane content; this function truncates.
//   - runtime     displayed on the "Runtime:" line (e.g. "cursor"). Kept
//     separate because row.PatternKey is "runtime.name" and
//     the runtime alone reads better in the header.
//   - projectName displayed on the "Repo:" line.
//   - now         injected current time so tests can pin "N ago" output.
func FormatNudge(row *NudgeRow, paneTail, runtime, projectName string, now time.Time) string {
	var b strings.Builder

	// Subject
	fmt.Fprintf(&b, "⚠ Permission prompt — @%s (%s)\n\n",
		row.AgentName, row.Session)

	// Metadata block
	fmt.Fprintf(&b, "Repo:    %s\n", projectName)
	fmt.Fprintf(&b, "Runtime: %s\n", runtime)
	fmt.Fprintf(&b, "Pattern: %s\n", row.PatternKey)
	fmt.Fprintf(&b, "First detected: %s (%s ago)\n",
		row.FirstDetected.Format("2006-01-02 15:04:05"),
		friendlyDuration(now.Sub(row.FirstDetected)))

	reminder := row.NudgeCount
	if reminder < 1 {
		reminder = 1
	}
	fmt.Fprintf(&b, "Reminder #%d of %d\n\n", reminder, maxReminderCount)

	// Pane tail
	b.WriteString("Pane tail (last 15 lines):\n")
	b.WriteString(indentLines(truncatePaneTail(paneTail), "  "))
	b.WriteString("\n\n")

	// Separator + copy-paste actions
	b.WriteString("─────────────────────────\n")
	fmt.Fprintf(&b, "To approve:  thrum tmux send %s %q\n", row.Session, row.ApproveKey)
	if row.DenyKey != "" {
		fmt.Fprintf(&b, "To deny:     thrum tmux send %s %q\n", row.Session, row.DenyKey)
	} else {
		b.WriteString("To interrupt: press Ctrl+C in the pane\n")
	}
	b.WriteString("\nOr reply to this message with `y` / `n` — works from CLI, web UI, and Telegram.\n")

	return b.String()
}

// truncatePaneTail caps the pane content at maxPaneTailLines lines AND
// maxPaneTailBytes bytes, preferring the tail (most recent output).
//
// The byte-cap branch walks past any UTF-8 continuation bytes left
// over from a mid-rune split before applying the newline rescue, so
// a single >2KB line containing multi-byte runes (e.g. a long URL
// with arrows, a base64 blob with Unicode punctuation) cannot emit
// invalid UTF-8 into the nudge body.
func truncatePaneTail(pane string) string {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	if len(lines) > maxPaneTailLines {
		lines = lines[len(lines)-maxPaneTailLines:]
	}
	out := strings.Join(lines, "\n")
	if len(out) > maxPaneTailBytes {
		out = out[len(out)-maxPaneTailBytes:]
		// Walk past any UTF-8 continuation bytes from a mid-rune split.
		for len(out) > 0 && !utf8.RuneStart(out[0]) {
			out = out[1:]
		}
		// Trim to the next newline so we don't start mid-line.
		if nl := strings.IndexByte(out, '\n'); nl > -1 {
			out = out[nl+1:]
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
