package permission

import (
	"context"
	"errors"
	"testing"
	"time"
)

// thrum-g23nb: the release-line-native reminder-ladder cancellation. Two
// conditions short-circuit fireReminder — a fresh pane recheck proving the
// modal is gone (cancel + recover), and an all-read supervisor audience (skip
// the send but keep the row). These tests pin both, plus the fail-open and
// fires-when-unread cases.

// modalMatches is pane content DetectPaneState classifies as the cursor
// not_in_allowlist permission prompt; modalGone is content it classifies as no
// prompt.
const (
	modalMatchesContent = "Not in allowlist: test\n"
	modalGoneContent    = "all done\n$ "
)

// countThreadMessages counts supervisor nudge messages in the thread rooted at
// rootID (the firstDetect message itself plus any reminders threaded under it).
// Used to assert whether fireReminder actually SENT a reminder.
func countThreadMessages(t *testing.T, p *Permission, rootID string) int {
	t.Helper()
	var n int
	if err := p.state.RawDB().QueryRow(
		`SELECT COUNT(*) FROM messages WHERE message_id = ? OR thread_id = ?`,
		rootID, rootID,
	).Scan(&n); err != nil {
		t.Fatalf("count thread messages: %v", err)
	}
	return n
}

// markDeliveryRead stamps read_at on the (msgID, recipient) delivery and asserts
// exactly one row was updated (catches a recipient-name typo silently passing).
func markDeliveryRead(t *testing.T, p *Permission, msgID, recipient string) {
	t.Helper()
	res, err := p.state.RawDB().Exec(
		`UPDATE message_deliveries SET read_at = ? WHERE message_id = ? AND recipient_agent_id = ?`,
		"2026-04-14T10:00:30Z", msgID, recipient)
	if err != nil {
		t.Fatalf("mark delivery read: %v", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		t.Fatalf("mark delivery read: updated %d rows, want 1 (recipient %q of %s)", n, recipient, msgID)
	}
}

// firstDetectAt drives a fresh first-detect and returns the nudge row.
func firstDetectAt(t *testing.T, p *Permission, ctx context.Context) *NudgeRow {
	t.Helper()
	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("first detect: %v", err)
	}
	row, err := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if err != nil || row == nil {
		t.Fatalf("expected nudge row after first detect (err=%v)", err)
	}
	return row
}

// advanceToFirstReminder moves the clock to the first reminder slot
// (FirstDetected + reminderSchedule[0]) so the next OnDetection fires a
// reminder.
func advanceToFirstReminder(p *Permission, clock *time.Time, row *NudgeRow) {
	*clock = row.FirstDetected.Add(reminderSchedule[0])
	p.SetClock(func() time.Time { return *clock })
}

func TestScheduler_G23nb_ReminderCancelsWhenModalCleared(t *testing.T) {
	p, clock := newSchedulerFixture(t)
	ctx := context.Background()

	p.SetPaneCaptureForTest(func(_ string, _ int) (string, error) { return modalMatchesContent, nil })
	row := firstDetectAt(t, p, ctx)

	// Modal is gone by the time the reminder slot arrives.
	p.SetPaneCaptureForTest(func(_ string, _ int) (string, error) { return modalGoneContent, nil })
	advanceToFirstReminder(p, clock, row)

	before := countThreadMessages(t, p, row.MessageID)
	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("reminder detect: %v", err)
	}

	// Row is claimed (deleted) — the ladder stopped.
	got, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if got != nil {
		t.Errorf("expected the nudge row to be removed when the modal cleared, got %+v", got)
	}
	// No reminder was sent.
	if after := countThreadMessages(t, p, row.MessageID); after != before {
		t.Errorf("expected no reminder send on cancel, thread messages went %d -> %d", before, after)
	}
}

func TestScheduler_G23nb_ReminderFiresOnCaptureError_FailOpen(t *testing.T) {
	p, clock := newSchedulerFixture(t)
	ctx := context.Background()

	p.SetPaneCaptureForTest(func(_ string, _ int) (string, error) { return modalMatchesContent, nil })
	row := firstDetectAt(t, p, ctx)

	// Capture fails at reminder time — fail OPEN: keep the ladder, fire.
	p.SetPaneCaptureForTest(func(_ string, _ int) (string, error) {
		return "", errors.New("tmux capture boom")
	})
	advanceToFirstReminder(p, clock, row)

	before := countThreadMessages(t, p, row.MessageID)
	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("reminder detect: %v", err)
	}

	got, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if got == nil {
		t.Fatal("fail-open: row must survive a capture error")
	}
	if got.NudgeCount != 2 {
		t.Errorf("fail-open: NudgeCount = %d, want 2 (reminder fired)", got.NudgeCount)
	}
	if after := countThreadMessages(t, p, row.MessageID); after != before+1 {
		t.Errorf("fail-open: expected one reminder send, thread messages went %d -> %d", before, after)
	}
}

func TestScheduler_G23nb_ReminderSkipsSendWhenAllRead(t *testing.T) {
	p, clock := newSchedulerFixture(t)
	ctx := context.Background()

	p.SetPaneCaptureForTest(func(_ string, _ int) (string, error) { return modalMatchesContent, nil })
	row := firstDetectAt(t, p, ctx)

	// The sole supervisor recipient reads the original nudge.
	markDeliveryRead(t, p, row.MessageID, "coordinator_main")
	advanceToFirstReminder(p, clock, row)

	before := countThreadMessages(t, p, row.MessageID)
	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("reminder detect: %v", err)
	}

	got, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if got == nil {
		t.Fatal("read-state skip must KEEP the row (reply path)")
	}
	// Slot is consumed (cadence marches toward give-up) ...
	if got.NudgeCount != 2 {
		t.Errorf("read-state skip: NudgeCount = %d, want 2 (slot consumed)", got.NudgeCount)
	}
	// ... but NO reminder message was sent.
	if after := countThreadMessages(t, p, row.MessageID); after != before {
		t.Errorf("read-state skip: expected no send, thread messages went %d -> %d", before, after)
	}
}

func TestScheduler_G23nb_ReminderFiresWhenUnread(t *testing.T) {
	p, clock := newSchedulerFixture(t)
	ctx := context.Background()

	p.SetPaneCaptureForTest(func(_ string, _ int) (string, error) { return modalMatchesContent, nil })
	row := firstDetectAt(t, p, ctx)

	// Recipient has NOT read — the reminder must fire normally.
	advanceToFirstReminder(p, clock, row)

	before := countThreadMessages(t, p, row.MessageID)
	if err := p.OnDetection(ctx, "cursor-test", "cursor", "cursor-test:0.0",
		"researcher_cursor", testPattern(), "pane A"); err != nil {
		t.Fatalf("reminder detect: %v", err)
	}

	got, _ := p.store.LookupPendingNudgeBySession(ctx, "cursor-test")
	if got == nil || got.NudgeCount != 2 {
		t.Fatalf("unread: expected row with NudgeCount=2, got %+v", got)
	}
	if after := countThreadMessages(t, p, row.MessageID); after != before+1 {
		t.Errorf("unread: expected one reminder send, thread messages went %d -> %d", before, after)
	}
}

// TestScheduler_G23nb_CountUnreadThreadDeliveries_ExcludesSender pins the
// sender-exclusion: the supervisor pseudo-agent's auto-read self-delivery must
// not count as audience read-state, else a single unread recipient could be
// masked.
func TestScheduler_G23nb_CountUnreadThreadDeliveries_ExcludesSender(t *testing.T) {
	p, _ := newSchedulerFixture(t)
	ctx := context.Background()

	p.SetPaneCaptureForTest(func(_ string, _ int) (string, error) { return modalMatchesContent, nil })
	row := firstDetectAt(t, p, ctx)

	// Sender self-delivery (supervisor_thrum) is auto-read; coordinator unread.
	unread, total, err := p.store.CountUnreadThreadDeliveries(ctx, row.MessageID, p.supervisorID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 1 || unread != 1 {
		t.Errorf("sender excluded: want total=1 unread=1 (coordinator only), got total=%d unread=%d", total, unread)
	}

	markDeliveryRead(t, p, row.MessageID, "coordinator_main")
	unread, total, err = p.store.CountUnreadThreadDeliveries(ctx, row.MessageID, p.supervisorID)
	if err != nil {
		t.Fatalf("count after read: %v", err)
	}
	if total != 1 || unread != 0 {
		t.Errorf("after recipient read: want total=1 unread=0, got total=%d unread=%d", total, unread)
	}
}
