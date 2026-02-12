package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

// PeerStatus represents a peer's status for RPC responses.
type PeerStatus struct {
	DaemonID string `json:"daemon_id"`
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
	LastSeen string `json:"last_seen"`
	Status   string `json:"status"`
	LastSeq  int64  `json:"last_synced_seq"`
}

// SyncFromPeerFunc pulls events from a specific peer and applies them.
type SyncFromPeerFunc func(peerAddr, peerDaemonID string) (applied, skipped int, err error)

// ListPeersFunc returns known peer statuses.
type ListPeersFunc func() []PeerStatus

// AddPeerFunc manually adds a peer.
type AddPeerFunc func(hostname string, port int) error

// TsyncForceHandler handles the tsync.force RPC to trigger Tailscale sync.
type TsyncForceHandler struct {
	syncFromPeer SyncFromPeerFunc
	listPeers    ListPeersFunc
}

// NewTsyncForceHandler creates a new tsync.force handler.
func NewTsyncForceHandler(syncFn SyncFromPeerFunc, listFn ListPeersFunc) *TsyncForceHandler {
	return &TsyncForceHandler{syncFromPeer: syncFn, listPeers: listFn}
}

// Handle triggers sync from all known peers (or a specific one if --from is set).
func (h *TsyncForceHandler) Handle(_ context.Context, params json.RawMessage) (any, error) {
	var req struct {
		From string `json:"from"`
	}
	if params != nil {
		_ = json.Unmarshal(params, &req)
	}

	peers := h.listPeers()
	if len(peers) == 0 {
		return map[string]any{"status": "no_peers", "message": "no peers configured"}, nil
	}

	var results []map[string]any
	for _, peer := range peers {
		if req.From != "" && peer.Hostname != req.From && peer.DaemonID != req.From {
			continue
		}

		addr := peer.Hostname
		if peer.Port > 0 {
			addr = fmt.Sprintf("%s:%d", peer.Hostname, peer.Port)
		}
		applied, skipped, err := h.syncFromPeer(addr, peer.DaemonID)
		result := map[string]any{
			"peer":    peer.DaemonID,
			"applied": applied,
			"skipped": skipped,
		}
		if err != nil {
			result["error"] = err.Error()
		}
		results = append(results, result)
	}

	return map[string]any{"status": "ok", "results": results}, nil
}

// TsyncPeersListHandler handles the tsync.peers.list RPC.
type TsyncPeersListHandler struct {
	listPeers ListPeersFunc
}

// NewTsyncPeersListHandler creates a new tsync.peers.list handler.
func NewTsyncPeersListHandler(listFn ListPeersFunc) *TsyncPeersListHandler {
	return &TsyncPeersListHandler{listPeers: listFn}
}

// Handle returns the list of known peers.
func (h *TsyncPeersListHandler) Handle(_ context.Context, _ json.RawMessage) (any, error) {
	return h.listPeers(), nil
}

// TsyncPeersAddHandler handles the tsync.peers.add RPC.
type TsyncPeersAddHandler struct {
	addPeer AddPeerFunc
}

// NewTsyncPeersAddHandler creates a new tsync.peers.add handler.
func NewTsyncPeersAddHandler(addFn AddPeerFunc) *TsyncPeersAddHandler {
	return &TsyncPeersAddHandler{addPeer: addFn}
}

// Handle adds a peer manually.
func (h *TsyncPeersAddHandler) Handle(_ context.Context, params json.RawMessage) (any, error) {
	var req struct {
		Hostname string `json:"hostname"`
		Port     int    `json:"port"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if req.Hostname == "" {
		return nil, fmt.Errorf("hostname is required")
	}
	if req.Port <= 0 {
		req.Port = 9100
	}

	if err := h.addPeer(req.Hostname, req.Port); err != nil {
		return nil, err
	}

	return map[string]string{"status": "ok", "hostname": req.Hostname}, nil
}
