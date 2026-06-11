package rpc

import (
	"context"
	"fmt"
)

// CountInboxVisibleUnread returns the number of unread messages the inbox
// listing would show for agentID — the visibility-aware unread count
// (thrum-saj4). It is the SINGLE LIVE composition of the same pieces
// HandleList's inbox-unread count path uses: the for-agent visibility
// predicate (buildForAgentValues + buildForAgentClause — the 4-part
// mention/group/legacy-broadcast/broadcast-delivery filter), the exclude-self
// rule, the registered_at floor, and the not-yet-read clause.
//
// The backstop calls this (via an injected seam) instead of scanning raw
// message_deliveries: a delivery row can exist for a message the agent cannot
// SEE in any inbox view (e.g. scoped to a group the agent isn't in — the
// storm-era supervisor-relay feeder shape), so the raw scan over-counts and
// the backstop nudges every 15min for invisible mail (thrum-saj4, the
// visibility residual of the wo2z residency fix).
//
// SHARE-LIVE, not frozen (cf. the tcqw v40 backfill which DID freeze): the
// backstop's correctness requirement is "agree with the inbox NOW," so this
// must track the live predicate — a frozen copy would re-introduce the exact
// drift we are closing. TestCountInboxVisibleUnread_ParityWithHandleList pins
// it equal to HandleList's Total so the two can never diverge.
//
// This mirrors HandleList's countQuery assembly for the inbox-unread case
// ({ForAgent, ForAgentRole, Unread, ExcludeSelf} all keyed on agentID) —
// including the EXACT arg order: exclude-self, then the for-agent clause args,
// then the unread recipient, then the registered_at floor.
func (h *MessageHandler) CountInboxVisibleUnread(ctx context.Context, agentID string) (int, error) {
	if agentID == "" {
		return 0, nil
	}

	// Resolve the agent's role inline (HandleList resolves it from the caller's
	// config; here we read it from the agents table) so buildForAgentClause's
	// role-mention and group-role arms match the inbox view.
	var role string
	_ = h.state.DB().QueryRowContext(ctx,
		`SELECT COALESCE(role,'') FROM agents WHERE agent_id = ? LIMIT 1`, agentID).Scan(&role)

	query := "SELECT COUNT(DISTINCT m.message_id) FROM messages m WHERE 1=1"
	var args []any

	// exclude-self (inbox mode): the agent's own sends never count as unread.
	query += " AND m.agent_id != ?"
	args = append(args, agentID)

	// for-agent visibility predicate (the 4-part clause).
	values := buildForAgentValues(agentID, role)
	clause, clauseArgs := buildForAgentClause(values, agentID, role)
	query += clause
	args = append(args, clauseArgs...)

	// unread: exclude messages the agent has a read delivery row for.
	query += " AND m.message_id NOT IN (SELECT md.message_id FROM message_deliveries md WHERE md.recipient_agent_id = ? AND md.read_at IS NOT NULL)"
	args = append(args, agentID)

	// registered_at floor: historical group/broadcast messages sent before the
	// agent existed are excluded (same as HandleList's for-agent floor).
	var registeredAt string
	if err := h.state.DB().QueryRowContext(ctx,
		`SELECT registered_at FROM agents WHERE agent_id = ? LIMIT 1`, agentID).Scan(&registeredAt); err == nil && registeredAt != "" {
		query += " AND m.created_at > ?"
		args = append(args, registeredAt)
	}

	var n int
	if err := h.state.DB().QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count inbox-visible unread for %s: %w", agentID, err)
	}
	return n, nil
}
