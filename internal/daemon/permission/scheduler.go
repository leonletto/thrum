package permission

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"time"
)

// reminderSchedule encodes the exponential backoff for reminders
// #2..#6, measured as offsets from FirstDetected. Reminder #1 fires
// at t=0 during firstDetect and is NOT in this slice; index i maps to
// reminder #(i+2).
//
// A sixth nudge (reminder #6 at +4h) is the final one — after sending
// it the scheduler flips into the give-up path on the next check-pane
// fire and marks the agent stuck.
var reminderSchedule = []time.Duration{
	5 * time.Minute,  // reminder #2
	15 * time.Minute, // reminder #3
	45 * time.Minute, // reminder #4
	2 * time.Hour,    // reminder #5
	4 * time.Hour,    // reminder #6
}

const (
	// maxNudgeCount is the total number of nudges (first-detect + 5
	// reminders) before the scheduler gives up. Kept in sync with
	// FormatNudge's "Reminder #N of 6" header via maxReminderCount.
	maxNudgeCount = 6

	// nudgeTTL is how long a pending row lives before SweepExpired
	// removes it. Generous to cover an overnight human response.
	nudgeTTL = 8 * time.Hour
)

// OnDetection is the scheduler entry point called from HandleCheckPane
// whenever state=permission is reported for a session. It routes the
// event to one of four paths:
//
//  1. first-detect    — no existing row for this session
//  2. pane-hash reset — existing row but a different prompt is showing
//  3. reminder advance — same prompt, time for the next nudge in the cadence
//  4. give-up         — same prompt, already sent maxNudgeCount nudges
//
// runtime identifies which Pattern matched (e.g. "cursor"); matched is
// the Pattern struct returned by permission.Match so the scheduler
// does not re-read the identity file and re-run detection.
func (p *Permission) OnDetection(ctx context.Context, session, runtime, tmuxTarget, agentName string, matched *Pattern, paneTail string) error {
	now := p.now()
	paneHash := sha256.Sum256([]byte(paneTail))

	row, err := p.store.LookupPendingNudgeBySession(ctx, session)
	if err != nil {
		return err
	}

	if row == nil {
		return p.firstDetect(ctx, session, runtime, tmuxTarget, agentName, matched, paneTail, paneHash, now)
	}

	if row.LastPaneHash != paneHash {
		// New prompt — reset the counter with a fresh first-nudge.
		_ = p.store.DeletePendingNudge(ctx, row.MessageID)
		return p.firstDetect(ctx, session, runtime, tmuxTarget, agentName, matched, paneTail, paneHash, now)
	}

	if row.NudgeCount >= maxNudgeCount {
		return p.giveUp(ctx, row, agentName)
	}

	// Reminder scheduling: reminder #N fires at FirstDetected +
	// reminderSchedule[N-2]. row.NudgeCount holds the count of nudges
	// ALREADY sent, so the next slot is at index (NudgeCount - 1).
	nextIdx := row.NudgeCount - 1
	if nextIdx < 0 || nextIdx >= len(reminderSchedule) {
		// Defensive — a NudgeCount of 0 would point before the slice
		// and > maxNudgeCount is caught above. If somehow reached, do
		// nothing rather than crash.
		return nil
	}
	nextAt := row.FirstDetected.Add(reminderSchedule[nextIdx])
	if now.Before(nextAt) {
		return nil
	}
	return p.fireReminder(ctx, row, runtime, paneTail, paneHash, now)
}

// firstDetect inserts a fresh row and fires the initial nudge. When
// there are no live supervisors (e.g. fresh daemon before any agent
// has registered), it still inserts a synthetic-ID row so the
// supervisor-resolution path stays idempotent and SweepExpired can
// clean it up later.
func (p *Permission) firstDetect(
	ctx context.Context,
	session, runtime, tmuxTarget, agentName string,
	matched *Pattern,
	paneTail string,
	paneHash [32]byte,
	now time.Time,
) error {
	supers, err := p.ResolveSupervisors(ctx, p.loadSupervisorEntries())
	if err != nil {
		slog.Error("[permission] resolve supervisors failed", "err", err)
	}

	row := &NudgeRow{
		Session:       session,
		TmuxTarget:    tmuxTarget,
		AgentName:     agentName,
		PatternKey:    runtime + "." + matched.Name,
		ApproveKey:    matched.ApproveKey,
		DenyKey:       matched.DenyKey,
		FirstDetected: now,
		LastNudgeAt:   now,
		NudgeCount:    1,
		LastPaneHash:  paneHash,
		ExpiresAt:     now.Add(nudgeTTL),
	}

	if len(supers) == 0 {
		// No live supervisors — still persist a row so recovery can
		// clean it up. MessageID is a stable synthetic so a second
		// orphan first-detect on the same session+time doesn't conflict.
		row.MessageID = "perm_orphan_" + session + "_" + now.Format("20060102T150405")
		return p.store.InsertPendingNudge(ctx, row)
	}

	body := FormatNudge(row, paneTail, runtime, p.projectName, now)
	var firstMsgID string
	for i, to := range supers {
		msgID, err := p.SendSupervisorMessage(ctx, to, body)
		if err != nil {
			slog.Error("[permission] send nudge failed", "to", to, "err", err)
			continue
		}
		if i == 0 {
			firstMsgID = msgID
		}
	}
	if firstMsgID == "" {
		// All sends failed — log and drop; do NOT leave an insert with
		// an empty PK. The next check-pane fire will retry.
		slog.Warn("[permission] no supervisor sends succeeded for first-detect",
			"session", session)
		return nil
	}
	row.MessageID = firstMsgID
	return p.store.InsertPendingNudge(ctx, row)
}

// fireReminder advances the row by one reminder slot, persists the
// update, and re-sends the nudge body (with the new reminder counter)
// to every live supervisor.
func (p *Permission) fireReminder(
	ctx context.Context,
	row *NudgeRow,
	runtime, paneTail string,
	paneHash [32]byte,
	now time.Time,
) error {
	row.NudgeCount++
	row.LastNudgeAt = now
	row.LastPaneHash = paneHash
	if err := p.store.UpdatePendingNudge(ctx, row); err != nil {
		return err
	}

	supers, _ := p.ResolveSupervisors(ctx, p.loadSupervisorEntries())
	body := FormatNudge(row, paneTail, runtime, p.projectName, now)
	for _, to := range supers {
		if _, err := p.SendSupervisorMessage(ctx, to, body); err != nil {
			slog.Error("[permission] reminder send failed", "to", to, "err", err)
		}
	}
	return nil
}

// giveUp is the terminal state: we've sent the full cadence, the
// prompt is still unresolved, and the agent is marked stuck. The row
// is intentionally left in place so a later OnRecovery can delete it
// and clear the stuck status.
func (p *Permission) giveUp(ctx context.Context, _ *NudgeRow, agentName string) error {
	return p.markAgentStuck(ctx, agentName)
}

// OnRecovery is called when HandleCheckPane reports state != permission
// for a session that had a pending nudge. It deletes the row and
// clears the agent's stuck status.
func (p *Permission) OnRecovery(ctx context.Context, session, agentName string) error {
	row, err := p.store.LookupPendingNudgeBySession(ctx, session)
	if err != nil || row == nil {
		return err
	}
	if err := p.store.DeletePendingNudge(ctx, row.MessageID); err != nil {
		return err
	}
	return p.clearAgentStuck(ctx, agentName)
}

// now returns the scheduler's notion of the current time. Tests
// inject a fake clock via SetClock; production leaves nowFunc nil.
func (p *Permission) now() time.Time {
	if p.nowFunc != nil {
		return p.nowFunc()
	}
	return time.Now().UTC()
}

// loadSupervisorEntries reads the current list of supervisor entries
// from disk. Called fresh per nudge so config edits take effect
// without a daemon restart.
//
// Stub for Task 5.4 — filled in by Task 5.5. Returning nil falls back
// to the default ["coordinator"] role broadcast inside ResolveSupervisors.
func (p *Permission) loadSupervisorEntries() []string {
	return nil
}

// markAgentStuck writes agent_status="stuck" into the identity file
// for the named agent so the UI and team listings can reflect that
// the agent is blocked on a permission prompt.
//
// Stub for Task 5.4 — filled in by Task 5.6.
func (p *Permission) markAgentStuck(ctx context.Context, agentName string) error {
	return nil
}

// clearAgentStuck clears the stuck marker when the agent resumes.
//
// Stub for Task 5.4 — filled in by Task 5.6.
func (p *Permission) clearAgentStuck(ctx context.Context, agentName string) error {
	return nil
}
