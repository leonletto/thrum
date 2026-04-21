package permission

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/leonletto/thrum/internal/tmux"
	"github.com/leonletto/thrum/internal/types"
)

// paneRecheckLines controls how much pane tail we capture for the
// pre-send recheck. Matches paneBottomMatchLines (detect.go) so the
// recheck sees the same scoped window as the scheduler's original
// detection — no new spurious matches or misses from a wider capture.
const paneRecheckLines = paneBottomMatchLines

// approveReplyRe and denyReplyRe match a full supervisor reply body
// (post ToLower + TrimSpace). Anchoring matters: a body like "why
// not?" must NOT dispatch approve just because it contains "y".
var (
	approveReplyRe = regexp.MustCompile(`^(y|yes|approve|a)$`)
	denyReplyRe    = regexp.MustCompile(`^(n|no|deny|d)$`)
)

// AfterMessageCreate is the event-write hook entry point. It fires on
// every message.create event write — local RPC, sync ingest, or
// cross-repo bridge inbound — which is what gives us cross-repo
// reply delivery for free via the same dispatch path.
//
// Events without a reply_to ref are no-ops (the message still lands
// in the normal delivery path; we just don't care about it here).
func (p *Permission) AfterMessageCreate(ctx context.Context, evt types.MessageCreateEvent) {
	replyTo := ""
	for _, ref := range evt.Refs {
		if ref.Type == "reply_to" {
			replyTo = ref.Value
			break
		}
	}
	if replyTo == "" {
		return
	}
	p.TryResolve(ctx, evt, replyTo)
}

// TryResolve looks up the pending nudge matching replyTo and — for
// approve/deny bodies — atomically claims and dispatches it. Unknown
// nudge (wrong daemon, already answered, or won the race with a
// concurrent reply) is a silent no-op. Unknown reply body (neither
// approve nor deny) leaves the row in place so reminders continue
// firing, treating the message as a normal thread comment.
//
// Race safety: since the hook now runs off the writer goroutine, two
// concurrent replies (e.g. a local reply and a cross-repo synced
// reply arriving in the same sync batch) can both enter TryResolve
// for the same row. The race-safe path is an atomic
// DELETE ... RETURNING via Store.DeleteAndReturnPendingNudge: the
// first caller gets the row and fires the keystroke; the second
// caller sees (nil, nil) and exits silently. A naive
// lookup-then-delete would let both pass the nil check and
// double-fire the keystroke, which corrupts numeric-selection
// prompts (claude "1/2/3") where the second keystroke lands on the
// next prompt.
//
// Thread-id fallback: reminder messages (#2-6) are sent with
// thread_id = firstDetect message_id (the nudge row PK). When a
// supervisor replies to reminder #N, replyTo is the reminder's
// message_id — not the firstDetect message_id — so the direct
// DeleteAndReturnPendingNudge returns (nil, nil). The fallback
// queries messages.thread_id for replyTo; if the result equals the
// nudge row PK, the delete succeeds on the second attempt. This is
// a two-step read-then-delete, not a single atomic operation, but
// the window between the thread_id read and the delete is safe: if
// a concurrent approve or recover races in, DeleteAndReturnPendingNudge
// returns (nil, nil) and the fallback no-ops silently.
//
// Trade-off: if the keystroke subprocess fails AFTER the delete
// succeeded, the row is gone — there is no retry. Accepted per the
// Epic C review: losing one retry is strictly safer than
// double-firing. Reminders won't resurrect the row (SweepExpired
// only deletes).
//
// Body classification happens BEFORE the atomic delete so unknown
// bodies (and deny-without-key) do not remove the row. Only an
// actionable approve/deny attempts the claim.
func (p *Permission) TryResolve(ctx context.Context, evt types.MessageCreateEvent, replyTo string) {
	body := strings.ToLower(strings.TrimSpace(evt.Body.Content))

	switch {
	case approveReplyRe.MatchString(body):
		// Atomically claim the row. If someone else got there first,
		// exit silently.
		row, err := p.store.DeleteAndReturnPendingNudge(ctx, replyTo)
		if err != nil {
			slog.Error("[permission] atomic claim for approve failed",
				"reply_to", replyTo, "err", err)
			return
		}
		if row == nil {
			// Direct lookup missed — fall back to the thread_id path.
			// Reminder messages carry thread_id = firstDetect message_id
			// (the nudge row PK), so walking messages.thread_id lets us
			// resolve replies aimed at a reminder rather than the root.
			row, err = p.claimNudgeViaThreadID(ctx, replyTo)
			if err != nil {
				slog.Error("[permission] thread-id fallback for approve failed",
					"reply_to", replyTo, "err", err)
				return
			}
			if row == nil {
				return // not ours
			}
		}
		if !p.paneStillMatches("approve", row) {
			return
		}
		if err := p.sendKeystroke(row.TmuxTarget, row.ApproveKey); err != nil {
			// Row is already gone. No retry possible by design; log
			// loudly so the operator can intervene by hand.
			slog.Error("[permission] approve keystroke failed AFTER atomic claim",
				"target", row.TmuxTarget, "key", row.ApproveKey,
				"message_id", row.MessageID, "err", err)
		}

	case denyReplyRe.MatchString(body):
		// Peek the row to check whether this pattern even has a deny
		// key before we claim it. Without this pre-check we'd claim
		// and delete rows for patterns that can't actually be denied
		// from the reply path (e.g. auggie Tool Approval Required),
		// leaving the reminder schedule unable to recover.
		nudgeID, err := p.resolveNudgeID(ctx, replyTo)
		if err != nil {
			slog.Error("[permission] lookup nudge for deny peek failed",
				"reply_to", replyTo, "err", err)
			return
		}
		if nudgeID == "" {
			return // not ours
		}
		peek, err := p.store.LookupPendingNudgeByMessageID(ctx, nudgeID)
		if err != nil {
			slog.Error("[permission] lookup nudge for deny peek failed",
				"reply_to", replyTo, "nudge_id", nudgeID, "err", err)
			return
		}
		if peek == nil {
			return // not ours
		}
		if peek.DenyKey == "" {
			slog.Info("[permission] deny requested but no deny key for pattern",
				"pattern", peek.PatternKey)
			return
		}
		// Now claim atomically. The small window between the peek
		// and this delete is tolerable: if another concurrent reply
		// claimed it first (approve path, or a second deny from a
		// different repo), our delete returns (nil, nil) and we
		// silently no-op.
		row, err := p.store.DeleteAndReturnPendingNudge(ctx, nudgeID)
		if err != nil {
			slog.Error("[permission] atomic claim for deny failed",
				"reply_to", replyTo, "err", err)
			return
		}
		if row == nil {
			return // lost the race — peer already dispatched
		}
		if !p.paneStillMatches("deny", row) {
			return
		}
		if err := p.sendKeystroke(row.TmuxTarget, row.DenyKey); err != nil {
			slog.Error("[permission] deny keystroke failed AFTER atomic claim",
				"target", row.TmuxTarget, "key", row.DenyKey,
				"message_id", row.MessageID, "err", err)
		}

	default:
		// Not an approve/deny body — leave the row in place. The
		// message flows to normal delivery as a thread comment; the
		// supervisor can send a real "y" or "n" afterwards to
		// actually dispatch.
	}
}

// resolveNudgeID returns the permission_nudges primary key that should
// be used to look up / claim the nudge for the given replyTo message_id.
//
// If replyTo directly matches a nudge row PK, it is returned as-is.
// Otherwise, the fallback queries messages.thread_id for replyTo — if a
// nudge row exists with that thread_id as its PK (i.e. the replyTo is a
// reminder in the firstDetect thread), the thread_id is returned.
// Returns "" when no nudge can be found, nil error on a clean miss.
func (p *Permission) resolveNudgeID(ctx context.Context, replyTo string) (string, error) {
	// Fast path: direct match.
	row, err := p.store.LookupPendingNudgeByMessageID(ctx, replyTo)
	if err != nil {
		return "", err
	}
	if row != nil {
		return replyTo, nil
	}
	// Fallback: walk the thread.
	threadID, err := p.store.LookupThreadIDForMessage(ctx, replyTo)
	if err != nil {
		return "", err
	}
	if threadID == "" {
		return "", nil
	}
	// Verify a nudge row actually exists for this thread_id before
	// returning it, so callers can treat "" == "not ours".
	threadRow, err := p.store.LookupPendingNudgeByMessageID(ctx, threadID)
	if err != nil {
		return "", err
	}
	if threadRow == nil {
		return "", nil
	}
	return threadID, nil
}

// claimNudgeViaThreadID is the thread_id fallback for the approve path.
// It resolves the nudge PK from the thread_id of replyTo and then
// atomically claims the row. Returns (nil, nil) when no nudge is found.
func (p *Permission) claimNudgeViaThreadID(ctx context.Context, replyTo string) (*NudgeRow, error) {
	threadID, err := p.store.LookupThreadIDForMessage(ctx, replyTo)
	if err != nil {
		return nil, err
	}
	if threadID == "" {
		return nil, nil
	}
	return p.store.DeleteAndReturnPendingNudge(ctx, threadID)
}

// paneStillMatches captures the pane tail at dispatch time and checks
// that DetectPaneState still reports the same pattern the nudge row
// was opened against. Returns true when the pane still shows the
// prompt (safe to fire the keystroke) and false when the pane has
// moved on (skip the keystroke to avoid stray input — e.g. an extra
// "1" that lands in the post-approval turn).
//
// Capture failures are treated as "no longer matches" (fail-closed):
// better to skip a keystroke on a transient tmux hiccup than fire
// blind into a pane we cannot observe. thrum-rfy3 field repro: two
// supervisors approving near-simultaneously produced an extra
// keystroke on a couple of occasions; this recheck closes the
// remaining race windows the atomic claim cannot on its own.
//
// Recovery on skip: when paneStillMatches returns false the row is
// already atomically deleted by the caller. The prompt is NOT
// orphaned — if it is still present on the next tmux silence cycle,
// HandleCheckPane → OnDetection → firstDetect re-fires with a fresh
// row. Delay is bounded by the check-pane cadence (seconds), not
// forever, so a transient capture hiccup is self-healing.
//
// Logging is emitted here so the caller can stay log-free: Info for
// the expected pane-moved-on case (the operator already approved in
// the pane, or recovery fired concurrently — nothing to do), Warn
// for the capture-error and malformed-pattern cases so operators see
// the recovery-pending state.
//
// `op` is the caller's operation ("approve"/"deny") and is threaded
// into the log attrs so a grep over logs can distinguish which reply
// path was short-circuited.
func (p *Permission) paneStillMatches(op string, row *NudgeRow) bool {
	capture := p.paneCapture
	if capture == nil {
		capture = tmux.CapturePane
	}
	content, err := capture(row.TmuxTarget, paneRecheckLines)
	if err != nil {
		slog.Warn("[permission] "+op+" skipped — pane recheck capture failed (will re-fire on next check-pane cycle if prompt persists)",
			"target", row.TmuxTarget, "message_id", row.MessageID, "err", err)
		return false
	}
	runtime, _, ok := strings.Cut(row.PatternKey, ".")
	if !ok || runtime == "" {
		// Malformed PatternKey shouldn't reach here (set by
		// firstDetect from a validated Pattern), but fail-closed if
		// it somehow does: without a runtime we cannot re-detect.
		slog.Warn("[permission] "+op+" skipped — malformed pattern key",
			"target", row.TmuxTarget, "pattern_key", row.PatternKey,
			"message_id", row.MessageID)
		return false
	}
	detected := DetectPaneState(runtime, content)
	expected := "permission:" + row.PatternKey
	if detected == expected {
		return true
	}
	slog.Info("[permission] "+op+" skipped — pane no longer matches pattern",
		"target", row.TmuxTarget, "pattern_key", row.PatternKey,
		"detected", detected, "message_id", row.MessageID)
	return false
}

// sendKeystroke dispatches a cached keystroke sequence to the tmux
// target. The key argument may be a single token ("y", "Enter") or a
// comma-separated sequence ("End,Enter", "Down,Enter") for runtimes
// whose deny action requires navigation before confirmation.
//
// Injected p.keystrokeSender is called once per segment so tests can
// assert both WHAT was sent and the segmentation order. The default
// path routes each segment to tmux.SendSpecialKey (for named keys)
// or tmux.SendKeys (-l literal for single characters).
//
// IMPORTANT: no implicit trailing Enter is appended. Per the
// `send-keys -l 2 Enter` anti-pattern gotcha surfaced during Task
// 0.1, appending Enter after a single-character selection in
// runtimes like claude/cursor/codex breaks the prompt by sending an
// extra keypress. Every runtime in the pattern library is
// single-keystroke-to-submit; comma-separated deny sequences
// explicitly include their own Enter when one is needed.
func (p *Permission) sendKeystroke(target, key string) error {
	if key == "" {
		return fmt.Errorf("sendKeystroke: empty key")
	}
	segments := strings.Split(key, ",")
	sender := p.keystrokeSender
	if sender == nil {
		sender = defaultKeystroke
	}
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if err := sender(target, seg); err != nil {
			// Include both the failed segment AND the full original
			// key so post-mortem diagnosis can reconstruct which
			// part of a multi-step sequence broke (e.g. End
			// succeeded but Enter failed in an opencode
			// "End,Enter" deny).
			slog.Error("[permission] segment send failed",
				"target", target, "segment", seg, "full_key", key, "err", err)
			return err
		}
	}
	return nil
}

// defaultKeystroke is the production implementation of the
// keystroke sender: named keys via SendSpecialKey, single characters
// via SendKeys with the -l literal flag.
func defaultKeystroke(target, key string) error {
	if isSpecialKeyName(key) {
		return tmux.SendSpecialKey(target, key)
	}
	return tmux.SendKeys(target, key)
}

// isSpecialKeyName returns true if key is a tmux-named special key.
// The list covers all keys currently referenced by the permission
// pattern library (Enter, Escape, End, Down, Home, etc.) plus the
// remainder of tmux's named-key vocabulary so a future runtime
// pattern can use any of them without falling through to SendKeys
// with -l and being sent as literal text.
//
// Reference: tmux-send-keys(1) documents the full list; the set
// below reflects its current named-key namespace as of tmux 3.4.
func isSpecialKeyName(key string) bool {
	switch key {
	case "Enter", "Escape", "Tab", "BTab",
		"Up", "Down", "Left", "Right",
		"Space", "BSpace", "Delete",
		"Home", "End", "PgUp", "PgDn",
		"NPage", "PPage", "IC", "DC",
		"F1", "F2", "F3", "F4", "F5", "F6",
		"F7", "F8", "F9", "F10", "F11", "F12":
		return true
	}
	return false
}
