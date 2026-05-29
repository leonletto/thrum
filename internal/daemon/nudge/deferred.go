package nudge

import (
	"log/slog"
	"sync"

	"github.com/leonletto/thrum/internal/daemon/permission"
)

// thrum-7phu: deferred-nudge queue.
//
// When a message-arrival nudge would be typed into a pane that is showing an
// interactive selection dialog (permission modal, trust gate, or an
// AskUserQuestion-style menu — anything where Enter selects an option), typing
// the notification text + Enter silently auto-answers a human decision. The
// daemon instead DEFERS the nudge: it records that the session has a pending
// notification and re-delivers it once the pane is safe to type again (the
// permission SessionPoller drives HandleCheckPane, which calls RedeliverIfSafe
// every poll cycle).
//
// The message itself is never lost regardless of this queue — a spool envelope
// is written at message-create time and surfaces via the agent's check-inbox
// hook. This queue only governs the live tmux PANE poke. It is therefore
// in-memory: a daemon restart drops pending pane-pokes, but the spool + DB
// still carry the message. One entry per session (collapsed): while a dialog is
// up, multiple arrivals fold into a single "you have new mail" poke naming the
// most recent sender; the agent's inbox holds the full set.

// deferredNudge is one pending pane-poke awaiting a safe-to-type window.
type deferredNudge struct {
	target string // full tmux target "session:window.pane"
	sender string // most-recent sender name, shown in the nudge text
}

var (
	deferredMu  sync.Mutex
	deferredByS = map[string]deferredNudge{} // session → pending poke

	// nudgeFn is the keystroke-injection seam. Production points at the real
	// tmux Nudge; tests substitute a recorder. Mirrors the seam pattern in
	// internal/tmux/nudge.go (nudgeSendKeys).
	nudgeFn = realNudge
)

// DeferNudge records (or refreshes) a pending pane-poke for session, to be
// re-delivered when the pane next becomes safe to type. Collapses to one entry
// per session — the latest sender wins.
func DeferNudge(session, target, sender string) {
	deferredMu.Lock()
	deferredByS[session] = deferredNudge{target: target, sender: sender}
	deferredMu.Unlock()
	slog.Info("[nudge] deferred (pane shows interactive dialog)",
		"session", session, "target", target, "sender", sender)
}

// HasDeferred reports whether a pending pane-poke exists for session.
func HasDeferred(session string) bool {
	deferredMu.Lock()
	defer deferredMu.Unlock()
	_, ok := deferredByS[session]
	return ok
}

// takeDeferred atomically removes and returns the pending poke for session.
func takeDeferred(session string) (deferredNudge, bool) {
	deferredMu.Lock()
	defer deferredMu.Unlock()
	d, ok := deferredByS[session]
	if ok {
		delete(deferredByS, session)
	}
	return d, ok
}

// RedeliverIfSafe re-delivers a deferred pane-poke for session iff (a) the
// captured pane is now safe to type into (no permission prompt, trust gate, or
// selection menu) and (b) a poke is pending. Returns true when a nudge was
// delivered. Self-contained: it re-checks pane safety itself so callers can
// invoke it unconditionally on every poll. On a transient send failure the poke
// is re-deferred so the next safe poll retries it (defer, never drop).
//
// runtime + paneContent come from the daemon's identity lookup + pane capture
// (HandleCheckPane). Empty paneContent is treated as "unknown → not safe" so a
// blind capture failure never re-delivers into a possibly-active dialog.
func RedeliverIfSafe(session, runtime, paneContent string) bool {
	if paneContent == "" {
		return false
	}
	if !permission.IsPaneSafeToType(runtime, paneContent) {
		return false
	}
	d, ok := takeDeferred(session)
	if !ok {
		return false
	}
	if err := nudgeFn(d.target, d.sender); err != nil {
		// Re-defer on transient failure so the next safe poll retries.
		DeferNudge(session, d.target, d.sender)
		slog.Warn("[nudge] deferred re-delivery failed; will retry next poll",
			"session", session, "target", d.target, "err", err)
		return false
	}
	slog.Info("[nudge] deferred nudge re-delivered (pane now safe)",
		"session", session, "target", d.target, "sender", d.sender)
	return true
}
