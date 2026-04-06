package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

// AddressChangedFunc is called when a peer notifies us of an address change.
// PeerToken identifies the peer; newIP and newPort are the new network location.
type AddressChangedFunc func(peerToken, newIP, newPort string) error

// PeerAddressChangedHandler handles the peer.address_changed RPC.
type PeerAddressChangedHandler struct {
	updateFn AddressChangedFunc
}

// NewPeerAddressChangedHandler creates a new handler with the given update function.
func NewPeerAddressChangedHandler(fn AddressChangedFunc) *PeerAddressChangedHandler {
	return &PeerAddressChangedHandler{updateFn: fn}
}

// Handle parses the peer.address_changed params and invokes the update function.
func (h *PeerAddressChangedHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	var req struct {
		PeerToken string `json:"peer_token"`
		NewIP     string `json:"new_ip"`
		NewPort   string `json:"new_port"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if req.PeerToken == "" || req.NewIP == "" || req.NewPort == "" {
		return nil, fmt.Errorf("peer_token, new_ip, and new_port are required")
	}
	if err := h.updateFn(req.PeerToken, req.NewIP, req.NewPort); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
