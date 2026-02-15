package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// --- Function types for peer handlers ---

// StartPairingFunc starts a pairing session and returns the code.
type StartPairingFunc func(timeout time.Duration) (code string, err error)

// WaitForPairingFunc blocks until the active pairing session completes or times out.
// Returns the paired peer's name, address, and daemon ID.
type WaitForPairingFunc func(ctx context.Context) (peerName, peerAddress, peerDaemonID string, err error)

// JoinPeerFunc connects to a remote peer, sends a pairing code, and stores the result.
type JoinPeerFunc func(peerAddr, code string) (peerName, peerDaemonID string, err error)

// RemovePeerFunc removes a peer by daemon ID.
type RemovePeerFunc func(daemonID string) error

// FindPeerByNameFunc resolves a peer name to a daemon ID.
type FindPeerByNameFunc func(name string) (daemonID string, found bool)

// --- Request/Response types ---

// PeerStartPairingRequest is the params for peer.start_pairing.
type PeerStartPairingRequest struct {
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// PeerStartPairingResponse is the result of peer.start_pairing.
type PeerStartPairingResponse struct {
	Code string `json:"code"`
}

// PeerWaitPairingResponse is the result of peer.wait_pairing.
type PeerWaitPairingResponse struct {
	Status       string `json:"status"` // "paired" or "timeout" or "error"
	PeerName     string `json:"peer_name,omitempty"`
	PeerAddress  string `json:"peer_address,omitempty"`
	PeerDaemonID string `json:"peer_daemon_id,omitempty"`
	Message      string `json:"message,omitempty"`
}

// PeerJoinRequest is the params for peer.join.
type PeerJoinRequest struct {
	Address string `json:"address"`
	Code    string `json:"code"`
}

// PeerJoinResponse is the result of peer.join.
type PeerJoinResponse struct {
	Status       string `json:"status"` // "paired" or "error"
	PeerName     string `json:"peer_name,omitempty"`
	PeerDaemonID string `json:"peer_daemon_id,omitempty"`
	Message      string `json:"message,omitempty"`
}

// PeerRemoveRequest is the params for peer.remove.
type PeerRemoveRequest struct {
	Name     string `json:"name,omitempty"`
	DaemonID string `json:"daemon_id,omitempty"`
}

// PeerDetailedStatus is the detailed status of a single peer.
type PeerDetailedStatus struct {
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Token    bool   `json:"has_token"`
	PairedAt string `json:"paired_at"`
	LastSync string `json:"last_sync"`
	LastSeq  int64  `json:"last_synced_seq"`
}

// PeerListEntry is a single peer in the compact list.
type PeerListEntry struct {
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	LastSync string `json:"last_sync"`
	LastSeq  int64  `json:"last_synced_seq"`
}

// --- Handlers ---

// PeerStartPairingHandler handles the peer.start_pairing RPC.
type PeerStartPairingHandler struct {
	startPairing StartPairingFunc
}

// NewPeerStartPairingHandler creates a new handler.
func NewPeerStartPairingHandler(fn StartPairingFunc) *PeerStartPairingHandler {
	return &PeerStartPairingHandler{startPairing: fn}
}

// Handle starts a pairing session and returns the code.
func (h *PeerStartPairingHandler) Handle(_ context.Context, params json.RawMessage) (any, error) {
	var req PeerStartPairingRequest
	if params != nil {
		_ = json.Unmarshal(params, &req)
	}

	timeout := 5 * time.Minute
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}

	code, err := h.startPairing(timeout)
	if err != nil {
		return nil, fmt.Errorf("start pairing: %w", err)
	}

	return PeerStartPairingResponse{Code: code}, nil
}

// PeerWaitPairingHandler handles the peer.wait_pairing RPC.
type PeerWaitPairingHandler struct {
	waitForPairing WaitForPairingFunc
}

// NewPeerWaitPairingHandler creates a new handler.
func NewPeerWaitPairingHandler(fn WaitForPairingFunc) *PeerWaitPairingHandler {
	return &PeerWaitPairingHandler{waitForPairing: fn}
}

// Handle blocks until pairing completes or times out.
func (h *PeerWaitPairingHandler) Handle(ctx context.Context, _ json.RawMessage) (any, error) {
	peerName, peerAddr, peerDaemonID, err := h.waitForPairing(ctx)
	if err != nil {
		return PeerWaitPairingResponse{
			Status:  "timeout",
			Message: err.Error(),
		}, err
	}

	return PeerWaitPairingResponse{
		Status:       "paired",
		PeerName:     peerName,
		PeerAddress:  peerAddr,
		PeerDaemonID: peerDaemonID,
	}, nil
}

// PeerJoinHandler handles the peer.join RPC.
type PeerJoinHandler struct {
	joinPeer JoinPeerFunc
}

// NewPeerJoinHandler creates a new handler.
func NewPeerJoinHandler(fn JoinPeerFunc) *PeerJoinHandler {
	return &PeerJoinHandler{joinPeer: fn}
}

// Handle connects to a remote peer and completes the pairing flow.
func (h *PeerJoinHandler) Handle(_ context.Context, params json.RawMessage) (any, error) {
	if params == nil {
		return nil, fmt.Errorf("missing params")
	}

	var req PeerJoinRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if req.Address == "" {
		return nil, fmt.Errorf("address is required")
	}
	if req.Code == "" {
		return nil, fmt.Errorf("code is required")
	}

	peerName, peerDaemonID, err := h.joinPeer(req.Address, req.Code)
	if err != nil {
		return PeerJoinResponse{
			Status:  "error",
			Message: err.Error(),
		}, err
	}

	return PeerJoinResponse{
		Status:       "paired",
		PeerName:     peerName,
		PeerDaemonID: peerDaemonID,
	}, nil
}

// PeerRemoveHandler handles the peer.remove RPC.
type PeerRemoveHandler struct {
	removePeer RemovePeerFunc
	findByName FindPeerByNameFunc
}

// NewPeerRemoveHandler creates a new handler.
func NewPeerRemoveHandler(removeFn RemovePeerFunc, findByNameFn FindPeerByNameFunc) *PeerRemoveHandler {
	return &PeerRemoveHandler{removePeer: removeFn, findByName: findByNameFn}
}

// Handle removes a peer by name or daemon ID.
func (h *PeerRemoveHandler) Handle(_ context.Context, params json.RawMessage) (any, error) {
	if params == nil {
		return nil, fmt.Errorf("missing params")
	}

	var req PeerRemoveRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	daemonID := req.DaemonID
	if daemonID == "" && req.Name != "" {
		id, found := h.findByName(req.Name)
		if !found {
			return nil, fmt.Errorf("peer %q not found", req.Name)
		}
		daemonID = id
	}
	if daemonID == "" {
		return nil, fmt.Errorf("name or daemon_id is required")
	}

	if err := h.removePeer(daemonID); err != nil {
		return nil, err
	}

	return map[string]string{"status": "ok"}, nil
}

// PeerStatusHandler handles the peer.status RPC.
type PeerStatusHandler struct {
	getStatus func() []PeerDetailedStatus
}

// NewPeerStatusHandler creates a new handler.
func NewPeerStatusHandler(fn func() []PeerDetailedStatus) *PeerStatusHandler {
	return &PeerStatusHandler{getStatus: fn}
}

// Handle returns detailed status for all peers.
func (h *PeerStatusHandler) Handle(_ context.Context, _ json.RawMessage) (any, error) {
	return h.getStatus(), nil
}

// PeerListHandler handles the peer.list RPC.
type PeerListHandler struct {
	listPeers func() []PeerListEntry
}

// NewPeerListHandler creates a new handler.
func NewPeerListHandler(fn func() []PeerListEntry) *PeerListHandler {
	return &PeerListHandler{listPeers: fn}
}

// Handle returns the compact peer list.
func (h *PeerListHandler) Handle(_ context.Context, _ json.RawMessage) (any, error) {
	return h.listPeers(), nil
}
