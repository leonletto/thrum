package rpc

import (
	"context"
	"encoding/json"
)

// PeerInfoResponse represents the result of a sync.peer_info RPC call.
type PeerInfoResponse struct {
	DaemonID  string `json:"daemon_id"`
	Hostname  string `json:"hostname"`
	PublicKey string `json:"public_key"` // Placeholder — Epic 4 adds real Ed25519 keys
}

// PeerInfoHandler handles the sync.peer_info RPC method.
type PeerInfoHandler struct {
	daemonID string
	hostname string
}

// NewPeerInfoHandler creates a new sync.peer_info handler.
func NewPeerInfoHandler(daemonID, hostname string) *PeerInfoHandler {
	return &PeerInfoHandler{
		daemonID: daemonID,
		hostname: hostname,
	}
}

// Handle handles a sync.peer_info request.
func (h *PeerInfoHandler) Handle(_ context.Context, _ json.RawMessage) (any, error) {
	return PeerInfoResponse{
		DaemonID:  h.daemonID,
		Hostname:  h.hostname,
		PublicKey: "", // Placeholder — Epic 4 will add Ed25519 key exchange
	}, nil
}
