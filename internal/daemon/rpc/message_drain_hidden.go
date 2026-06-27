package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DrainHiddenRequest is the request for the message.drainHidden RPC (thrum-f37v3
// Part B). It marks the caller's filter-hidden unread deliveries read.
type DrainHiddenRequest struct {
	CallerAgentID string `json:"caller_agent_id,omitempty"`
	// MarkedBefore is an RFC3339Nano watermark: only deliveries for messages
	// created at/before it are drained, preserving the read --all race guard
	// (mail that arrived after the listing stays unread). Empty drains all.
	MarkedBefore string `json:"marked_before,omitempty"`
}

// DrainHiddenResponse reports how many filter-hidden deliveries were drained.
type DrainHiddenResponse struct {
	DrainedCount int `json:"drained_count"`
}

// HandleDrainHidden marks read every unread delivery row whose message the
// caller's for-agent inbox filter HIDES — the "N additional unread outside your
// filter" residual that `read --all` can never clear (thrum-f37v3 Part B).
//
// Why a dedicated drain instead of feeding the hidden IDs through MarkRead:
// these messages FAIL recipientgate (the agent is not a legitimate recipient —
// e.g. a supervisor relay scoped to a group the agent isn't in), so MarkRead's
// receipt-legitimacy gate refuses to stamp them and `read --all` no-ops
// ("Marked 0"). The rows therefore accumulate unbounded as backstop/nudge fuel.
//
// The drain stamps message_deliveries.read_at DIRECTLY and deliberately emits
// NO message.receipt event. Emitting one would broadcast a phantom cross-mesh
// "read" receipt for a message the agent was never a legitimate recipient of —
// the exact thrum-1846 receipt-storm vector that gating receipt emission was
// meant to close. This is a LOCAL dismissal of undeliverable mail, not a real
// read, so it stays off the event log by design.
func (h *MessageHandler) HandleDrainHidden(ctx context.Context, params json.RawMessage) (any, error) {
	var req DrainHiddenRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	agentID, _, err := h.resolveAgentAndSession(ctx, req.CallerAgentID)
	if err != nil {
		return nil, fmt.Errorf("resolve agent and session: %w", err)
	}

	var role string
	_ = h.state.DB().QueryRowContext(ctx,
		`SELECT COALESCE(role,'') FROM agents WHERE agent_id = ? LIMIT 1`, agentID).Scan(&role)

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Build the for-agent VISIBILITY predicate; we drain its COMPLEMENT — the
	// delivery-backed messages this agent CANNOT see in its filtered inbox.
	values := buildForAgentValues(agentID, role)
	clause, clauseArgs := buildForAgentClause(values, agentID, role)
	visiblePred := strings.TrimPrefix(clause, " AND ") // "(mention OR group OR ...)"

	// Select the caller's hidden, unread, delivery-backed message IDs. The
	// EXISTS clause keeps the scan bounded to mail actually delivered to this
	// agent (mirrors the hidden_by_filter advisory count in HandleList).
	sel := `SELECT m.message_id FROM messages m
		WHERE EXISTS (SELECT 1 FROM message_deliveries md
		              WHERE md.message_id = m.message_id
		                AND md.recipient_agent_id = ?
		                AND md.read_at IS NULL)`
	selArgs := []any{agentID}
	if visiblePred != "" {
		sel += " AND NOT " + visiblePred
		selArgs = append(selArgs, clauseArgs...)
	}
	if req.MarkedBefore != "" {
		sel += " AND m.created_at <= ?"
		selArgs = append(selArgs, req.MarkedBefore)
	}

	h.state.Lock()
	defer h.state.Unlock()

	updateArgs := append([]any{now, agentID}, selArgs...)
	res, err := h.state.DB().ExecContext(ctx,
		`UPDATE message_deliveries SET read_at = ?
		 WHERE recipient_agent_id = ? AND read_at IS NULL
		   AND message_id IN (`+sel+`)`,
		updateArgs...)
	if err != nil {
		return nil, fmt.Errorf("drain hidden deliveries for %s: %w", agentID, err)
	}
	n, _ := res.RowsAffected()
	return &DrainHiddenResponse{DrainedCount: int(n)}, nil
}
