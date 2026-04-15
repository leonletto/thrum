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
			return // not ours, or already claimed by a concurrent reply
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
		peek, err := p.store.LookupPendingNudgeByMessageID(ctx, replyTo)
		if err != nil {
			slog.Error("[permission] lookup nudge for deny peek failed",
				"reply_to", replyTo, "err", err)
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
		row, err := p.store.DeleteAndReturnPendingNudge(ctx, replyTo)
		if err != nil {
			slog.Error("[permission] atomic claim for deny failed",
				"reply_to", replyTo, "err", err)
			return
		}
		if row == nil {
			return // lost the race — peer already dispatched
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
