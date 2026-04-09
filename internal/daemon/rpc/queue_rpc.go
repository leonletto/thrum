package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/identity"
)

// QueueRequest is the tmux.queue RPC request.
type QueueRequest struct {
	Session   string `json:"session"`
	Text      string `json:"text"`
	TimeoutMs int64  `json:"timeout_ms"`
	Requester string `json:"requester"`
}

// QueueResponse is the tmux.queue RPC response.
type QueueResponse struct {
	CommandID string `json:"command_id"`
	Position  int    `json:"position"`
}

// generateCommandID generates a ULID-based command identifier with a "cmd_" prefix.
func generateCommandID() string {
	raw := identity.GenerateEventID() // returns "evt_<ULID>"
	return "cmd_" + strings.TrimPrefix(raw, "evt_")
}

// HandleQueue handles the tmux.queue RPC — submit a command to a session's queue.
func (h *TmuxHandler) HandleQueue(ctx context.Context, params json.RawMessage) (any, error) {
	var req QueueRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.Session == "" {
		return nil, fmt.Errorf("session is required")
	}
	if req.Text == "" {
		return nil, fmt.Errorf("text is required")
	}
	if req.Requester == "" {
		return nil, fmt.Errorf("requester is required")
	}
	if req.TimeoutMs == 0 {
		req.TimeoutMs = 120000 // default 2 minutes
	}

	cmd := &QueuedCommand{
		ID:             generateCommandID(),
		Text:           req.Text,
		RequesterAgent: req.Requester,
		Timeout:        time.Duration(req.TimeoutMs) * time.Millisecond,
		State:          StateQueued,
		SubmittedAt:    time.Now().UTC(),
	}

	queue := h.getOrCreateQueue(req.Session)
	position := queue.Len() + 1

	// Persist to DB first — if this fails, do not enqueue in-memory.
	h.state.Lock()
	err := persistCommand(ctx, h.state.DB(), req.Session, cmd, position)
	h.state.Unlock()
	if err != nil {
		return nil, fmt.Errorf("persist command: %w", err)
	}

	queue.Enqueue(cmd)

	return &QueueResponse{
		CommandID: cmd.ID,
		Position:  position,
	}, nil
}
