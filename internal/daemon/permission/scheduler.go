package permission

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/identity/guard"
	"github.com/leonletto/thrum/internal/process"
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
	// MaxNudgeCount is the total number of nudges (first-detect + 5
	// reminders) before the scheduler gives up. Kept in sync with
	// FormatNudge's "Reminder #N of 6" header via maxReminderCount.
	maxNudgeCount = 6

	// NudgeTTL is how long a pending row lives before SweepExpired
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
	// Hash over volatile-line-stripped content so cadence tracking is
	// stable across cosmetic pane updates (codex "Working (Ns)" timer,
	// Claude spinner animations, cursor blinks). Without this, every
	// poll on the same semantic prompt would look like a new prompt,
	// reset the reminder counter, and re-fire firstDetect. stripVolatileLines
	// is a no-op for runtimes with no registered patterns (unknown,
	// empty string), so downstream behavior is preserved for pre-poller
	// call sites.
	//
	// Scope the hash to the same bottom-window the DetectPaneState gate
	// uses (thrum-k4wf). Single source of truth for "what counts as the
	// active prompt": upper-scrollback drift never reaches the detector
	// (so OnDetection won't fire for scroll-out prompts), so hashing the
	// full paneTail would sometimes differ between polls on content the
	// detector already declared irrelevant and cause spurious
	// new-prompt resets.
	paneHash := sha256.Sum256([]byte(stripVolatileLines(runtime, bottomLines(paneTail, paneBottomMatchLines))))

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
	// Read the configured entries once — both ResolveSupervisors and the
	// orphan-path slog.Warn reference them. Caching also closes a
	// theoretical consistency gap where a mid-resolution config reload
	// could make the logged entries differ from the resolved ones.
	configuredEntries := p.loadSupervisorEntries()
	supers, err := p.ResolveSupervisors(ctx, configuredEntries)
	if err != nil {
		slog.Error("[permission] resolve supervisors failed", "err", err)
	}
	slog.Info("[permission] firstDetect",
		"session", session,
		"runtime", runtime,
		"pattern", matched.Name,
		"supers_count", len(supers),
		"agent_name", agentName)

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
		// No live supervisors — the give-up cadence path will never fire
		// for this agent (reminders require a recipient), so this is a
		// terminal silent-failure unless surfaced. Warn loudly with the
		// configured entries so operators can see what was tried, then
		// mark the affected agent stuck immediately to mirror the state
		// visible in thrum team / UI. The orphan row is still persisted
		// so OnRecovery can clean it up and clearAgentStuck runs when the
		// pane resumes. markAgentStuck errors are non-fatal: dropping the
		// stuck flag when the identity file is missing is preferable to
		// losing the orphan row.
		slog.Warn("[permission] orphan insert — no live supervisors resolved; nudge dropped",
			"session", session,
			"runtime", runtime,
			"pattern", matched.Name,
			"configured_entries", configuredEntries)
		if stuckErr := p.markAgentStuck(ctx, agentName); stuckErr != nil {
			slog.Warn("[permission] markAgentStuck failed in orphan path",
				"agent", agentName, "err", stuckErr)
		}
		row.MessageID = "perm_orphan_" + session + "_" + now.Format("20060102T150405")
		return p.store.InsertPendingNudge(ctx, row)
	}

	body := FormatNudge(row, paneTail, runtime, p.projectName, now)
	var firstMsgID string
	for i, to := range supers {
		// First-detect messages have no thread yet; threadID="" lets
		// the projector store NULL so the message_id itself becomes
		// the thread root that fireReminder will reference.
		msgID, err := p.SendSupervisorMessage(ctx, to, body, "")
		if err != nil {
			slog.Error("[permission] send nudge failed", "to", to, "err", err)
			continue
		}
		slog.Info("[permission] nudge sent", "to", to, "msg_id", msgID, "session", session)
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

	// Match firstDetect's error handling exactly: log the error but
	// still advance the reminder with whatever recipients we got back
	// (typically none in the error case). A transient state DB hiccup
	// should not silently stall the cadence.
	supers, err := p.ResolveSupervisors(ctx, p.loadSupervisorEntries())
	if err != nil {
		slog.Error("[permission] resolve supervisors failed", "err", err)
	}
	body := FormatNudge(row, paneTail, runtime, p.projectName, now)
	for _, to := range supers {
		// Reminder messages carry the firstDetect message_id as their
		// thread_id so TryResolve can walk messages.thread_id back to
		// the nudge row when a supervisor replies to a reminder rather
		// than to the original firstDetect message.
		if _, err := p.SendSupervisorMessage(ctx, to, body, row.MessageID); err != nil {
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
// from .thrum/config.json. Called fresh per nudge so operators can
// change the supervisor list without a daemon restart.
//
// Missing file, parse failure, or unset field all return nil — the
// caller's fallback in ResolveSupervisors then broadcasts to the
// default "coordinator" role.
func (p *Permission) loadSupervisorEntries() []string {
	cfg, err := config.LoadThrumConfig(p.thrumDir)
	if err != nil {
		slog.Warn("[permission] load thrum config failed", "err", err)
		return nil
	}
	return cfg.PermissionSupervisors
}

// markAgentStuck writes agent_status="stuck" into the agent's identity
// file so the UI, `thrum team`, and other consumers can reflect that
// the agent is blocked on a permission prompt. Unconditional — always
// writes, even if the agent was already marked stuck (touching
// AgentStatusUpdatedAt is still useful).
func (p *Permission) markAgentStuck(ctx context.Context, agentName string) error {
	return p.setAgentStatus(ctx, agentName, "stuck", "")
}

// clearAgentStuck resets the stuck marker when the agent resumes. It
// is a no-op unless the current status is "stuck" — this matters so
// we don't clobber other statuses (e.g. "working", "offline") that
// might have been set by another code path while the nudge was
// pending.
func (p *Permission) clearAgentStuck(ctx context.Context, agentName string) error {
	return p.setAgentStatus(ctx, agentName, "", "stuck")
}

// setAgentStatus loads the agent's identity file from disk, optionally
// checks the current status against onlyIf, writes newStatus, and
// saves. OnlyIf == "" means unconditional. Uses os.ReadFile +
// json.Unmarshal directly rather than config.LoadIdentityWithPath so
// the agent session's ambient THRUM_HOME does not redirect us to the
// wrong identities directory.
//
// Ctx is threaded from the public mark/clear callers and is honored
// by a single preflight Err() check before the synchronous file I/O.
// Today that makes the check almost cosmetic — the read + write are
// microsecond-scale — but when the helper grows an async or
// network path (e.g. pushing an agent.update event), the ctx already
// flows through. Removing the parameter now would just have to be
// re-added later.
//
// Empty agentName is a silent no-op. This happens on the recovery
// edge case where an agent's identity file has been deleted between
// firstDetect and recovery (e.g. `thrum agent delete` ran while a
// nudge was pending): HandleCheckPane's findIdentityForSession then
// returns "" and OnRecovery forwards that down to the stuck-clear
// call, which would otherwise try to read the malformed path
// `.thrum/identities/.json` and pollute operator logs with a
// spurious ENOENT. The stuck flag is best-effort; dropping it
// silently when there's no identity to update is the right call.
func (p *Permission) setAgentStatus(ctx context.Context, agentName, newStatus, onlyIf string) error {
	if agentName == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("setAgentStatus: %w", err)
	}
	// #nosec G304 — agentName comes from event-driven code paths; the
	// path is constrained to .thrum/identities and the identity file
	// is an internal artifact.
	idPath := filepath.Join(p.thrumDir, "identities", agentName+".json")
	data, err := os.ReadFile(idPath) // #nosec G304
	if err != nil {
		return fmt.Errorf("read identity %s: %w", agentName, err)
	}
	var idFile config.IdentityFile
	if err := json.Unmarshal(data, &idFile); err != nil {
		return fmt.Errorf("parse identity %s: %w", agentName, err)
	}
	if onlyIf != "" && idFile.AgentStatus != onlyIf {
		return nil // no-op — we don't own the current status
	}
	// G4: refuse to mutate agent_status on a dead agent's identity file.
	// The scheduler's status writes race against agent lifecycle: if an
	// agent crashes between OnDetection and the reminder that flips it
	// to "stuck", we must not silently label a dead owner. AgentPID=0
	// means the agent never primed a PID; G4 applies to dead-after-alive
	// transitions only, so skip the gate in that case.
	if idFile.AgentPID != 0 {
		mode := guard.ConfigForIdentityDir(filepath.Join(p.thrumDir, "identities")).DaemonWriterLiveness
		if mode == "" {
			mode = guard.ModeStrict
		}
		if gErr := guard.G4(&guard.WriterContext{
			Mode:       mode,
			SubjectPID: idFile.AgentPID,
			IsPIDAlive: func(pid int) bool { return process.IsRunning(pid) },
		}); gErr != nil {
			return fmt.Errorf("setAgentStatus refused for %s: %w", agentName, gErr)
		}
	}
	idFile.AgentStatus = newStatus
	idFile.AgentStatusUpdatedAt = time.Now().UTC()
	if err := config.SaveIdentityFile(p.thrumDir, &idFile); err != nil {
		return fmt.Errorf("save identity %s: %w", agentName, err)
	}
	return nil
}
