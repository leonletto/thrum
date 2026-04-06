package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

// AddRemoteAgentFunc adds an agent to a peer's remote agent list.
type AddRemoteAgentFunc func(peerName, agentName string) error

// RemoveRemoteAgentFunc removes an agent from a peer's remote agent list.
type RemoveRemoteAgentFunc func(peerName, agentName string) error

// PeerConfigureHandler handles the peer.configure RPC.
type PeerConfigureHandler struct {
	addFn    AddRemoteAgentFunc
	removeFn RemoveRemoteAgentFunc
}

// NewPeerConfigureHandler creates a new handler.
func NewPeerConfigureHandler(addFn AddRemoteAgentFunc, removeFn RemoveRemoteAgentFunc) *PeerConfigureHandler {
	return &PeerConfigureHandler{addFn: addFn, removeFn: removeFn}
}

// Handle dispatches add-agent or remove-agent actions for a peer.
func (h *PeerConfigureHandler) Handle(_ context.Context, params json.RawMessage) (any, error) {
	var req struct {
		PeerName  string `json:"peer_name"`
		Action    string `json:"action"`
		AgentName string `json:"agent_name"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if req.PeerName == "" || req.AgentName == "" {
		return nil, fmt.Errorf("peer_name and agent_name are required")
	}

	switch req.Action {
	case "add-agent":
		if h.addFn == nil {
			return nil, fmt.Errorf("add-agent not supported")
		}
		if err := h.addFn(req.PeerName, req.AgentName); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "action": "added", "agent": req.AgentName}, nil
	case "remove-agent":
		if h.removeFn == nil {
			return nil, fmt.Errorf("remove-agent not supported")
		}
		if err := h.removeFn(req.PeerName, req.AgentName); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "action": "removed", "agent": req.AgentName}, nil
	default:
		return nil, fmt.Errorf("unknown action: %s", req.Action)
	}
}
