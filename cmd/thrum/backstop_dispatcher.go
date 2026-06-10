// Backstop dispatcher: adapts internal/daemon/backstop.Dispatcher to the
// existing nudge primitives (tmux + spool) without invoking OutboundRelay.
// See internal/daemon/backstop for the polling logic; this file is purely
// the production wiring for thrum-7b84.3 E3 (T4).
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/leonletto/thrum/internal/daemon/backstop"
	"github.com/leonletto/thrum/internal/daemon/inbox"
	"github.com/leonletto/thrum/internal/daemon/nudge"
)

// backstopDispatcher implements backstop.Dispatcher.
//
// On each Dispatch call:
//
//  1. If the agent has a live tmux pane, fire nudge.DispatchTmux. The
//     existing notification text ("New message from @<sender> — run
//     thrum inbox --unread to read") is appropriate — the recipient
//     opens their inbox the same way regardless of source.
//  2. Always write a synthetic spool envelope as a backstop reminder so
//     dead-pane agents pick it up on next SessionStart. The msg_id is
//     deterministic per (agent, minute) so rapid re-calls within the
//     same minute collapse to one file. The spool janitor skips
//     "backstop-"-prefixed entries (internal/daemon/inbox/janitor.go)
//     so they survive past the next sweep and are overwritten by the
//     next tick when the underlying messages are still unread.
//
// OutboundRelay/Telegram is intentionally NOT involved: backstops are a
// forgotten-mail reminder for the local recipient, not a paging signal.
type backstopDispatcher struct {
	thrumDir string
}

func newBackstopDispatcher(thrumDir string) backstop.Dispatcher {
	return &backstopDispatcher{thrumDir: thrumDir}
}

func (d *backstopDispatcher) Dispatch(ctx context.Context, agentID string, unreadCount int) error {
	// thrum-wo2z defense-in-depth (mirrors the 0.11 dispatch-layer guard): a
	// non-resident recipient gets NEITHER the tmux nudge NOR the spool
	// envelope. The primary residency filter lives in backstop.Tick; this
	// guard keeps the dispatcher safe standalone — without it, spool files
	// for remote agents accumulate forever (the janitor preserves backstop-
	// entries, and a remote agent never reads a local spool).
	if !nudge.HasLocalIdentity(d.thrumDir, agentID) {
		slog.Info("[backstop] skip non-local agent", "agent", agentID, "unread_count", unreadCount)
		return nil
	}

	// Fire tmux nudge for the agent. nudge.DispatchTmux is fire-and-forget
	// and silently no-ops if the agent has no live tmux session, which is
	// the right behavior here — the spool write below covers the dead-pane
	// case. ctx bounds the chrome-quiet poll goroutine against shutdown.
	nudge.DispatchTmux(ctx, d.thrumDir, []string{agentID}, "thrum-backstop")

	// Always also write a spool envelope so the next SessionStart hook
	// will surface the reminder for an agent whose pane wasn't reachable.
	// The envelope uses a synthetic msg_id keyed to the minute so
	// repeated calls within the same minute collapse to one file. The
	// spool janitor skips entries with the "backstop-" prefix
	// (internal/daemon/inbox/janitor.go) so these envelopes survive past
	// the hourly sweep — the dispatcher's per-tick re-write naturally
	// replaces them when the underlying messages remain unread.
	//
	// From is set to "thrum-backstop" so the check-inbox.sh hook
	// surfaces a recognizable sender name in its notification text.
	now := time.Now().UTC()
	env := inbox.Envelope{
		MsgID:      "backstop-" + now.Format("20060102T1504"),
		From:       "thrum-backstop",
		ReceivedAt: now,
	}
	if err := inbox.WriteSpool(d.thrumDir, agentID, env); err != nil {
		slog.Warn("[backstop] spool write failed",
			"agent", agentID,
			"unread_count", unreadCount,
			"err", err,
		)
		// Non-fatal: tmux nudge above may still have woken the agent.
	}
	return nil
}
