package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/daemon/escalation"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// StateCorruptionRouter is the minimal escalation surface the
// agent.mark_state_corruption RPC reaches when it needs to page the
// operator. Mirrors agentdispatch.EscalationRouter (same shape; same
// escalation.Alert type) so daemon-boot wiring can share a single
// implementation across both consumers.
//
// Defined here rather than re-using the agentdispatch package's
// interface to keep the rpc package's compile-time dependency
// minimal — only escalation.Alert crosses the package boundary,
// and that struct already lives in internal/daemon/escalation.
type StateCorruptionRouter interface {
	Route(ctx context.Context, alert escalation.Alert, subject, body string) error
}

// AgentStateCorruptionHandler implements the agent.mark_state_corruption
// RPC per spec §6.5 — the 4th of 5 B-B1 escalation sites (idle-nudge
// exhaust, stage-failure 3-consecutive, auto-respawn loop guard,
// state.md parse failure, nudge target offline).
//
// Invoked by `/thrum:recover-agent-state` (via the
// `thrum agent state recover` CLI) when the recovery skill detects a
// malformed state.md. Three side effects:
//
//  1. Set agents.state_md_parse_failed_at = now (gates auto-respawn
//     per spec §3.x).
//  2. Append a state_md_parse_failed row to agent_lifecycle_events
//     with details.broken_path = "<.thrum/agents/<name>/state.md.broken>".
//  3. Call EscalationRouter.Route with Alert{Source:
//     "b-b1.state_md_parse_failed"} so the operator is paged.
//
// Auth: any identified caller. The skill itself runs as the agent
// (in the agent's tmux pane); the daemon trusts the caller to
// pass the correct agent_name (peercred identity validation lives
// outside this handler).
type AgentStateCorruptionHandler struct {
	state  *state.State
	router StateCorruptionRouter
}

// NewAgentStateCorruptionHandler wires the handler. router may be
// nil during early daemon boot or in test fixtures that don't care
// about the escalation path — when nil, the handler still performs
// the DB writes (the gate-flag + event-log half of the operation)
// and returns success. Per the agentdispatch.routeEscalation
// pattern: nil-Router means "no operator paging available", not "fail".
func NewAgentStateCorruptionHandler(s *state.State, router StateCorruptionRouter) *AgentStateCorruptionHandler {
	return &AgentStateCorruptionHandler{state: s, router: router}
}

// MarkStateCorruptionRequest is the wire shape. agent_name is the
// canonical key. broken_path tells the operator where the offending
// content lives for review.
type MarkStateCorruptionRequest struct {
	AgentName  string `json:"agent_name"`
	BrokenPath string `json:"broken_path"`
}

// MarkStateCorruptionResponse echoes back a small confirmation
// shape — failed_at is the timestamp the daemon stored, useful for
// the recovery skill's status log.
type MarkStateCorruptionResponse struct {
	AgentName string `json:"agent_name"`
	FailedAt  string `json:"failed_at"`
	Escalated bool   `json:"escalated"`
}

// HandleMarkStateCorruption is the JSON-RPC entry point. Spec §6.5
// + §8 contract:
//
//   - Validates agent_name is non-empty + exists in the agents table.
//   - Atomic block under state.Lock(): UPDATE agents SET
//     state_md_parse_failed_at + INSERT agent_lifecycle_events row.
//   - Outside the lock: call EscalationRouter.Route (so the
//     downstream router I/O doesn't block the DB lock).
//
// Returns the failed_at timestamp + a bool indicating whether
// escalation routing was actually attempted (false when router
// is nil).
func (h *AgentStateCorruptionHandler) HandleMarkStateCorruption(ctx context.Context, params json.RawMessage) (any, error) {
	var req MarkStateCorruptionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.AgentName == "" {
		return nil, fmt.Errorf("agent_name is required")
	}

	now := time.Now().UTC()

	h.state.Lock()
	defer h.state.Unlock()

	// Verify the agent exists. Per the existing handler convention
	// (session.go line 542-554) this is a separate query rather
	// than relying on the UPDATE row-count, because the update
	// silently succeeds when no row matches.
	var exists bool
	if err := h.state.DB().QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM agents WHERE agent_id = ?)`,
		req.AgentName,
	).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check agent existence: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("agent %s not registered", req.AgentName)
	}

	// (1) Set the gate flag.
	if _, err := h.state.DB().ExecContext(ctx,
		`UPDATE agents SET state_md_parse_failed_at = ? WHERE agent_id = ?`,
		now.Unix(), req.AgentName,
	); err != nil {
		return nil, fmt.Errorf("update state_md_parse_failed_at: %w", err)
	}

	// (2) Append the lifecycle event. details JSON carries the
	// broken-path location for operator review per spec §6.5.
	detailsJSON, err := json.Marshal(map[string]string{"broken_path": req.BrokenPath})
	if err != nil {
		return nil, fmt.Errorf("marshal event details: %w", err)
	}
	if _, err := h.state.DB().ExecContext(ctx,
		`INSERT INTO agent_lifecycle_events (agent_name, event_kind, event_time, details) VALUES (?, ?, ?, ?)`,
		req.AgentName, "state_md_parse_failed", now.Unix(), string(detailsJSON),
	); err != nil {
		return nil, fmt.Errorf("insert lifecycle event: %w", err)
	}

	resp := &MarkStateCorruptionResponse{
		AgentName: req.AgentName,
		FailedAt:  now.Format(time.RFC3339),
		Escalated: false,
	}

	// (3) Route the escalation OUTSIDE the state lock. Router I/O
	// (email send, message dispatch) shouldn't block the SQL
	// connection. The router may be nil when escalation isn't
	// wired yet — degraded but functional.
	h.state.Unlock()
	defer h.state.Lock()

	if h.router != nil {
		alert := escalation.Alert{
			Source:    "b-b1.state_md_parse_failed",
			AgentName: req.AgentName,
		}
		subject := fmt.Sprintf("state.md unparseable for agent %s — auto-respawn blocked",
			req.AgentName)
		body := fmt.Sprintf(`The scheduled-agent state.md file for %q failed
structural validation. Auto-respawn is BLOCKED until the operator
clears the corruption flag via `+"`thrum agent ack-state-corruption %s`"+`.

The offending content has been preserved at:
  %s

Review the file, repair if recoverable, then clear the flag.
Per B-B1 spec §6.5.
`, req.AgentName, req.AgentName, req.BrokenPath)
		if err := h.router.Route(ctx, alert, subject, body); err != nil {
			// Per escalation.RouteEscalation conventions, route
			// failures are absorbed at the email layer (D-B1 queue
			// retries) but propagate from the supervisor-fallback
			// path. Either way, the DB writes already succeeded —
			// the corruption flag is set + the event is logged —
			// so we surface the route error but mark Escalated=false.
			return nil, fmt.Errorf("escalation route: %w", err)
		}
		resp.Escalated = true
	}

	return resp, nil
}
