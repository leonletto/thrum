package rpc

import (
	"context"
	"encoding/json"
)

// PeerInfoResponse represents the result of a sync.peer_info RPC call.
type PeerInfoResponse struct {
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
}

// PeerInfoHandler handles the sync.peer_info RPC method.
type PeerInfoHandler struct {
	daemonID string
	name     string
}

// NewPeerInfoHandler creates a new sync.peer_info handler.
func NewPeerInfoHandler(daemonID, name string) *PeerInfoHandler {
	return &PeerInfoHandler{
		daemonID: daemonID,
		name:     name,
	}
}

// Handle handles a sync.peer_info request.
func (h *PeerInfoHandler) Handle(_ context.Context, _ json.RawMessage) (any, error) {
	return PeerInfoResponse{
		DaemonID: h.daemonID,
		Name:     h.name,
	}, nil
}
