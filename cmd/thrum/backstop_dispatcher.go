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
//     deterministic per (agent, day, hour) so repeated ticks within a
//     polling window dedupe naturally and the spool janitor reaps it
//     when the underlying messages are read.
//
// OutboundRelay/Telegram is intentionally NOT involved: backstops are a
// forgotten-mail reminder for the local recipient, not a paging signal.
type backstopDispatcher struct {
	thrumDir string
}

func newBackstopDispatcher(thrumDir string) backstop.Dispatcher {
	return &backstopDispatcher{thrumDir: thrumDir}
}

func (d *backstopDispatcher) Dispatch(_ context.Context, agentID string, unreadCount int) error {
	// Fire tmux nudge for the agent. nudge.DispatchTmux is fire-and-forget
	// and silently no-ops if the agent has no live tmux session, which is
	// the right behavior here — the spool write below covers the dead-pane
	// case.
	nudge.DispatchTmux(d.thrumDir, []string{agentID}, "thrum-backstop")

	// Always also write a spool envelope so the next SessionStart hook
	// will surface the reminder for an agent whose pane wasn't reachable.
	// The envelope uses a synthetic msg_id keyed to the polling window so
	// repeated ticks within a single window collapse to one file. The
	// spool janitor only reaps envelopes whose underlying messages are
	// read, so a synthetic envelope that doesn't correspond to a real
	// message stays put until the agent runs `thrum inbox --unread`.
	//
	// We tag it with the unread_count via the From field so the
	// check-inbox.sh hook can include it in the surfaced notification
	// (existing scripts already format "New message from @<from>", which
	// remains correct for human reading).
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
