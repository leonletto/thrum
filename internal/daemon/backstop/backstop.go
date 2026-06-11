// Package backstop provides a daemon-side 15-minute backstop nudger that
// re-fires the existing tmux/spool nudge for agents detected as alive with
// unread messages older than the configured age cutoff. It catches messages
// that the original push delivery missed (e.g., tmux wedged, hook didn't
// fire, agent in a long bash invocation that didn't yield) without
// requiring per-agent SessionStart cron jobs.
//
// Dedup is implicit: a message whose delivery row already has read_at set,
// or whose delivered_at is younger than AgeCutoff, never enters the
// candidate set.
//
// Forward-binding note: once A-B1 lands RegisterInternal on the scheduler
// substrate, Run will be swapped for a single RegisterInternal call. The
// goroutine/ticker shape here mirrors PeriodicSyncScheduler + BackupScheduler
// so the swap is a one-line change.
package backstop

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
)

// AliveWindow is the "alive" threshold for an agent: agents whose
// last_seen_at falls within this window are considered candidates for
// backstop nudges. Chosen conservatively (the daemon heartbeat / touch
// path fires far more often) and matches the staleness pattern used by
// internal/cli/hint_sources_send for send.recipient-stale hints.
const AliveWindow = 1 * time.Hour

// Dispatcher is the abstraction over the existing tmux/spool nudge.
// In production this is implemented by a thin shim around the daemon's
// nudge dispatcher that takes an agent_id and an unread count and
// routes to tmux if the agent's pane is alive, otherwise drops a spool
// file for the next SessionStart hook to consume.
//
// IMPORTANT: implementations MUST NOT route to OutboundRelay/Telegram
// for backstop nudges — backstops are a forgotten-mail reminder, not a
// paging signal.
type Dispatcher interface {
	Dispatch(ctx context.Context, agentID string, unreadCount int) error
}

// Backstop polls for stale-unread per agent and dispatches reminder nudges.
//
// DB is the project-standard safe wrapper (internal/daemon/safedb) — raw
// *sql.DB is prohibited in daemon code per the safedb compliance rule.
// QueryContext on *safedb.DB has the same signature as on *sql.DB, so
// query call sites here are unchanged.
type Backstop struct {
	DB        *safedb.DB
	Dispatch  Dispatcher
	AgeCutoff time.Duration    // typical: 15 * time.Minute
	Interval  time.Duration    // typical: 15 * time.Minute
	Now       func() time.Time // injected for tests; Run defaults to time.Now
	// IsResident is the recipient-residency predicate (thrum-wo2z): the agents
	// table includes SYNCED REMOTE registrations (sync replicates them and
	// refreshes last_seen_at), so the scan's alive-window does NOT imply the
	// recipient lives on this daemon. Without this filter, a delivery for a
	// remote-resident recipient synced into the local DB wakes a local session
	// every tick forever (the leonair 15-minute phantom-wake metronome). The
	// daemon wires it to nudge.HasLocalIdentity — identity-file-based, the
	// same residency notion the inbox side resolves from. nil = legacy
	// allow-all (existing constructors/tests unchanged).
	IsResident func(agentID string) bool
	// VisibleUnread is the inbox-visibility predicate (thrum-saj4): the raw
	// message_deliveries scan counts unread rows the recipient cannot SEE —
	// a delivery row can exist for a message scoped/addressed such that the
	// inbox's for-agent visibility filter (buildForAgentClause) hides it
	// (storm-era supervisor-relay feeders). Nudging for invisible mail is the
	// phantom-nudge residual wo2z's residency skip did not reach. The daemon
	// wires this to rpc.CountInboxVisibleUnread — the SAME live count the
	// inbox listing produces (share-live, not frozen: the backstop must agree
	// with the inbox at all times). Returns the count the recipient would see
	// in `thrum inbox --unread`; the backstop nudges only when it is > 0. nil
	// = legacy allow-all (existing constructors/tests unchanged).
	VisibleUnread func(ctx context.Context, agentID string) (int, error)
}

// Run blocks on ctx and ticks at Backstop.Interval. Returns when ctx is done.
func (b *Backstop) Run(ctx context.Context) {
	if b.Now == nil {
		b.Now = time.Now
	}
	ticker := time.NewTicker(b.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.Tick(ctx); err != nil {
				slog.Warn("[backstop] tick error", "err", err)
			}
		}
	}
}

// Tick runs one polling cycle: find alive agents with unread older than
// AgeCutoff, dispatch a single nudge per agent carrying the count.
func (b *Backstop) Tick(ctx context.Context) error {
	if b.Now == nil {
		b.Now = time.Now
	}
	now := b.Now().UTC()
	cutoff := now.Add(-b.AgeCutoff).Format(time.RFC3339Nano)
	aliveSince := now.Add(-AliveWindow).Format(time.RFC3339Nano)

	rows, err := b.DB.QueryContext(ctx, `
		SELECT md.recipient_agent_id, COUNT(*) AS unread_count
		FROM message_deliveries md
		JOIN agents a ON a.agent_id = md.recipient_agent_id
		WHERE md.read_at IS NULL
		  AND md.delivered_at < ?
		  AND a.last_seen_at > ?
		GROUP BY md.recipient_agent_id
	`, cutoff, aliveSince)
	if err != nil {
		return fmt.Errorf("query stale unread: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type backlog struct {
		agentID string
		count   int
	}
	var backlogs []backlog
	for rows.Next() {
		var bl backlog
		if err := rows.Scan(&bl.agentID, &bl.count); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		backlogs = append(backlogs, bl)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	for _, bl := range backlogs {
		// thrum-wo2z: skip recipients not resident on this daemon — their
		// unread state is genuinely theirs, on their own box; nudging a local
		// session for it is the phantom-wake defect. Residency first: it is the
		// cheapest check (a filesystem stat) and prunes the synced-remote rows
		// before the visibility query runs.
		if b.IsResident != nil && !b.IsResident(bl.agentID) {
			slog.Debug("[backstop] skip non-resident recipient", "agent", bl.agentID, "unread", bl.count)
			continue
		}
		// thrum-saj4: the raw scan counted unread DELIVERY rows; nudge only for
		// the subset the recipient can actually SEE in their inbox (the same
		// for-agent visibility filter HandleList applies). A delivery row for a
		// message hidden by that filter (e.g. scoped to a group the agent isn't
		// in) must not wake them. dispatchCount = the visible count, so the
		// nudge text reflects what they'll find.
		dispatchCount := bl.count
		if b.VisibleUnread != nil {
			visible, err := b.VisibleUnread(ctx, bl.agentID)
			if err != nil {
				slog.Warn("[backstop] visible-unread check failed", "agent", bl.agentID, "err", err)
				continue // fail closed: a count error must not resurrect the phantom nudge
			}
			if visible == 0 {
				slog.Debug("[backstop] skip filter-hidden unread", "agent", bl.agentID, "raw_unread", bl.count)
				continue
			}
			dispatchCount = visible
		}
		if err := b.Dispatch.Dispatch(ctx, bl.agentID, dispatchCount); err != nil {
			slog.Warn("[backstop] dispatch failed", "agent", bl.agentID, "err", err)
			// continue — one bad nudge shouldn't stop the rest
		}
	}
	return nil
}
