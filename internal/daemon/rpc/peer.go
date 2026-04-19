package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// --- Function types for peer handlers ---

// StartPairingFunc starts a pairing session.
//
// Inputs:
//   - timeout — how long the daemon should hold the pairing session open.
//   - peerType — explicit transport selection (xir.27): tailscale|local|network|a-sync.
//     Empty preserves legacy implicit-tailscale behavior at the RPC layer.
//   - addressHint — for peerType=="network", the user-supplied LAN IP that
//     anchors the peercode. Ignored for other types.
//   - remote — for peerType=="a-sync", the user-supplied git URL. Ignored
//     for other types.
//
// Returns the pair-code, the peercode address (ip:port — empty for a-sync),
// and the resolved transport label echoed back so the CLI can surface
// "Pairing code (transport=X): ..." consistently.
type StartPairingFunc func(timeout time.Duration, peerType, addressHint, remote string) (code, address, transport string, err error)

// WaitForPairingFunc blocks until the active pairing session completes or times out.
// Returns the paired peer's name, address, and daemon ID.
type WaitForPairingFunc func(ctx context.Context) (peerName, peerAddress, peerDaemonID string, err error)

// JoinPeerFunc connects to a remote peer.
//
// Inputs:
//   - peerAddr, code — peercode address + pair-code (used for tailscale/local/network).
//   - repoPath — legacy local-peer hint (used pre-xir.27; --type local is the
//     preferred entry point).
//   - peerType — explicit transport selection: tailscale|local|network|a-sync|repair.
//   - peerName — required when peerType=="repair"; identifies the existing
//     peer entry to reconcile.
//   - remote — required when peerType=="a-sync"; the git URL.
type JoinPeerFunc func(peerAddr, code, repoPath, peerType, peerName, remote string) (peerName_, peerDaemonID string, err error)

// RemovePeerFunc removes a peer by daemon ID.
type RemovePeerFunc func(daemonID string) error

// FindPeerByNameFunc resolves a peer name to a daemon ID.
type FindPeerByNameFunc func(name string) (daemonID string, found bool)

// --- Request/Response types ---

// PeerStartPairingRequest is the params for peer.start_pairing.
type PeerStartPairingRequest struct {
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	AuthKey        string `json:"auth_key,omitempty"` // Tailscale auth key (passed from CLI prompt)
	// Type is the user-selected transport (tailscale|local|network|a-sync).
	// Required from xir.27 onwards. Empty Type is accepted at the RPC layer
	// for legacy callers and routes to the implicit-tailscale flow; the CLI
	// surfaces the missing-flag error before this RPC is called. "repair"
	// is not valid here (use peer.join for repair).
	Type string `json:"type,omitempty"`
	// Address is the user-supplied LAN IP when Type=="network". The daemon
	// validates the IP via internal/netdetect and uses it as the peercode
	// address.
	Address string `json:"address,omitempty"`
	// Remote is the user-supplied git URL when Type=="a-sync". Used to
	// configure the git remote and stamp the peer entry; no peercode is
	// emitted for a-sync.
	Remote string `json:"remote,omitempty"`
}

// PeerStartPairingResponse is the result of peer.start_pairing.
type PeerStartPairingResponse struct {
	Code    string `json:"code"`
	Address string `json:"address,omitempty"` // local peer address (ip:port) — class depends on Type
	// Transport echoes the daemon's chosen transport label (tailscale|local|network|a-sync).
	// Useful for the CLI to print "Pairing code (transport=X): ..." consistently.
	Transport string `json:"transport,omitempty"`
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
	Address  string `json:"address"`
	Code     string `json:"code"`
	RepoPath string `json:"repo_path,omitempty"`
	// Type is the user-selected transport (tailscale|local|network|a-sync|repair).
	// Required from xir.27 onwards. Daemon dispatches dial transport based on this.
	Type string `json:"type,omitempty"`
	// PeerName identifies the existing peer when Type=="repair". For repair the
	// daemon looks up Token+DaemonID+Address from peers.json and re-handshakes
	// without minting a new token. Address/Code are unused for repair.
	PeerName string `json:"peer_name,omitempty"`
	// Remote is the user-supplied git URL when Type=="a-sync".
	Remote string `json:"remote,omitempty"`
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

	// If the CLI provided an auth key (from user prompt), set it in the process env
	// so the lazy tsnet start can pick it up
	if req.AuthKey != "" {
		_ = os.Setenv("THRUM_TS_AUTHKEY", req.AuthKey)
	}

	timeout := 5 * time.Minute
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}

	code, address, transport, err := h.startPairing(timeout, req.Type, req.Address, req.Remote)
	if err != nil {
		return nil, fmt.Errorf("start pairing: %w", err)
	}

	return PeerStartPairingResponse{Code: code, Address: address, Transport: transport}, nil
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

	// xir.27: address+code are required for the peercode-based transports
	// (tailscale/local/network) but unused for a-sync (no live handshake)
	// and repair (resolves via stored peers.json). Per-type validation
	// happens in the joinFn closure.
	switch req.Type {
	case "a-sync", "repair":
		// peercode not required
	default:
		if req.Address == "" {
			return nil, fmt.Errorf("address is required")
		}
		if req.Code == "" {
			return nil, fmt.Errorf("code is required")
		}
	}

	peerName, peerDaemonID, err := h.joinPeer(req.Address, req.Code, req.RepoPath, req.Type, req.PeerName, req.Remote)
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
