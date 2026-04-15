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

// TryResolve looks up the pending nudge matching replyTo; if found,
// parses the reply body and dispatches approve/deny. Unknown nudge
// (wrong daemon or already answered) is a silent no-op. Unknown
// reply body (neither approve nor deny) leaves the row in place so
// reminders continue firing — treating the message as a normal
// thread comment.
//
// Keystroke send failures do NOT delete the row: the next reminder
// fire will retry the dispatch.
func (p *Permission) TryResolve(ctx context.Context, evt types.MessageCreateEvent, replyTo string) {
	row, err := p.store.LookupPendingNudgeByMessageID(ctx, replyTo)
	if err != nil {
		slog.Error("[permission] lookup nudge by reply_to failed", "reply_to", replyTo, "err", err)
		return
	}
	if row == nil {
		return // not one of ours
	}

	body := strings.ToLower(strings.TrimSpace(evt.Body.Content))

	switch {
	case approveReplyRe.MatchString(body):
		if err := p.sendKeystroke(row.TmuxTarget, row.ApproveKey); err != nil {
			slog.Error("[permission] approve keystroke failed",
				"target", row.TmuxTarget, "key", row.ApproveKey, "err", err)
			return
		}
		if err := p.store.DeletePendingNudge(ctx, row.MessageID); err != nil {
			slog.Error("[permission] delete nudge after approve failed",
				"message_id", row.MessageID, "err", err)
		}

	case denyReplyRe.MatchString(body):
		if row.DenyKey == "" {
			// No in-prompt deny keystroke for this runtime (e.g.
			// auggie's Tool Approval Required prompt). Leave the row
			// so reminders continue; the operator must Ctrl+C in
			// the pane.
			slog.Info("[permission] deny requested but no deny key for pattern",
				"pattern", row.PatternKey)
			return
		}
		if err := p.sendKeystroke(row.TmuxTarget, row.DenyKey); err != nil {
			slog.Error("[permission] deny keystroke failed",
				"target", row.TmuxTarget, "key", row.DenyKey, "err", err)
			return
		}
		if err := p.store.DeletePendingNudge(ctx, row.MessageID); err != nil {
			slog.Error("[permission] delete nudge after deny failed",
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
// The list mirrors the set referenced by pattern ApproveKey/DenyKey
// values across the permission library (plus a few close neighbors
// that tmux accepts, for forward-compat when patterns evolve).
func isSpecialKeyName(key string) bool {
	switch key {
	case "Enter", "Escape", "Tab", "BTab",
		"Up", "Down", "Left", "Right",
		"Space", "BSpace", "Delete",
		"Home", "End", "PgUp", "PgDn":
		return true
	}
	return false
}
