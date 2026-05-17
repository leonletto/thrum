package reminders

import (
	"fmt"
	"time"
)

// FormatAgentBody produces the terse payload delivered to in-thrum agent
// inboxes. Per brainstorm Q3.1 the wording differs by trigger kind:
//
//   - condition_pane_quiet (daemon-source idle detection):
//     "Idle Agent Detected with idle-id: {id} — run `thrum agent reminder {id}`"
//   - time (agent/user-set reminders, daemon-source staleness pings):
//     "Reminder fired: {id} — run `thrum agent reminder {id}`"
//
// Body content is identical across daemon-source and agent-source rows
// for the same trigger kind — the source is recorded in the row, not
// the body. Email delivery uses FormatEmail; this function is the
// agent-message body only.
func FormatAgentBody(r *Reminder) string {
	switch r.TriggerKind {
	case TriggerConditionPaneQuiet:
		return fmt.Sprintf(
			"Idle Agent Detected with idle-id: %s — run `thrum agent reminder %s`",
			r.ID, r.ID,
		)
	default:
		// Time-triggered (and any future trigger kinds) get the generic
		// "Reminder fired" prefix.
		return fmt.Sprintf(
			"Reminder fired: %s — run `thrum agent reminder %s`",
			r.ID, r.ID,
		)
	}
}

// FormatEmail produces the (subject, body) payload for email delivery of
// daemon-source condition-triggered (idle) reminders. Per brainstorm
// Q3.1 the body is full-sentence prose with the activity-since-raised
// duration and the lookup command embedded.
//
// User-set time-triggered reminders don't currently route to email
// (Q3.1 future use case). Callers should pre-filter to
// daemon/condition_pane_quiet rows before calling FormatEmail; the
// function does not gate the trigger kind itself so test fixtures can
// drive the formatter without spinning up the full mint pipeline.
//
// `now` is the formatter's clock seam — passed in so tests assert
// against deterministic elapsed-time output. Production passes
// time.Now().UTC().
func FormatEmail(r *Reminder, now time.Time) (subject string, body string) {
	subject = fmt.Sprintf("Thrum reminder — agent %s idle", r.TargetAgent)
	elapsed := formatElapsed(now.Sub(r.RaisedAt))
	body = fmt.Sprintf(`Agent %s has been idle for %s (since %s). To see
what's happening and decide whether to defer or act, run this command in a
thrum-aware terminal:

    thrum agent reminder %s

From there: `+"`--defer 1h`"+` to snooze, `+"`--clear`"+` to dismiss, `+"`--cancel`"+` to withdraw.
`, r.TargetAgent, elapsed, r.RaisedAt.UTC().Format(time.RFC3339), r.ID)
	return subject, body
}

// formatElapsed renders a Duration as a short human-readable string
// matching the project's existing "Xm ago / Xh ago / Xd ago" idiom
// (internal/cli/inbox.go). Returned without the "ago" suffix since the
// caller embeds it in "idle for X" phrasing.
//
// Negative durations (raised_at in the future — clock skew, test
// fixtures with weird `now`) format as their absolute value rather
// than producing a "-2h" output that would confuse a reader.
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1m"
		}
		return fmt.Sprintf("%dm", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1h"
		}
		return fmt.Sprintf("%dh", hours)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d"
		}
		return fmt.Sprintf("%dd", days)
	}
}
