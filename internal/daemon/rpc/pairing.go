package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

// PairRequestFunc handles a pairing request with code verification.
// Returns (token, daemonID, name, error).
type PairRequestFunc func(code, peerDaemonID, peerName, peerAddress string) (token, daemonID, name string, err error)

// PairRequestHandler handles the pair.request RPC method on the Tailscale endpoint.
type PairRequestHandler struct {
	handlePair PairRequestFunc
}

// NewPairRequestHandler creates a new pair.request handler.
func NewPairRequestHandler(fn PairRequestFunc) *PairRequestHandler {
	return &PairRequestHandler{handlePair: fn}
}

// Handle handles a pair.request RPC call.
// The remote peer sends: code, daemon_id, name, address.
// On success, returns the local daemon's token, daemon_id, and name.
func (h *PairRequestHandler) Handle(_ context.Context, params json.RawMessage) (any, error) {
	var req struct {
		Code     string `json:"code"`
		DaemonID string `json:"daemon_id"`
		Name     string `json:"name"`
		Address  string `json:"address"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if req.Code == "" {
		return nil, fmt.Errorf("code is required")
	}
	if req.DaemonID == "" {
		return nil, fmt.Errorf("daemon_id is required")
	}

	token, daemonID, name, err := h.handlePair(req.Code, req.DaemonID, req.Name, req.Address)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"status":    "paired",
		"token":     token,
		"daemon_id": daemonID,
		"name":      name,
	}, nil
}
