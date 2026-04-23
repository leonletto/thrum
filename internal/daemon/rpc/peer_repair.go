package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

// xir.27 sub-4: dedicated peer.repair RPC.
//
// Repair reconciles an existing peer entry using the stored Token as its
// trust anchor. Unlike pair.request, repair does NOT mint a new token and
// does NOT require an active pairing session — it verifies a stored token
// and refreshes metadata (daemon_id, address, hostname, etc.) that may have
// rotated on the dialer's side. The two protocols are separate on purpose:
// see thrum-xir.27 "Research notes: sub-4 graft surface analysis" for the
// line-by-line comparison that decided against extending pair.request.

// PeerRepairFunc handles an incoming peer.repair request.
//
// Parameters: token (the dialer's stored authenticator), dialer's current
// daemon_id, name, address, repo_name, hostname, repo_path, git_origin_url.
//
// Returns the local daemon's current identity metadata so the dialer can
// refresh its own stored entry in turn, plus an error. Token validation
// and peer-entry refresh are the implementation's responsibility.
type PeerRepairFunc func(
	token, dialerDaemonID, dialerName, dialerAddress string,
	dialerRepoName, dialerHostname, dialerRepoPath, dialerGitOriginURL string,
) (localDaemonID, localName, localRepoName, localHostname, localRepoPath, localGitOriginURL string, err error)

// peerRepairResponse is the wire format returned by peer.repair on success.
// Mirrors pairRequestResponse but omits the Token field (repair does not
// mint; it reuses the token the dialer presented).
type peerRepairResponse struct {
	Status       string `json:"status"` // "repaired"
	DaemonID     string `json:"daemon_id"`
	Name         string `json:"name"`
	RepoName     string `json:"repo_name,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	RepoPath     string `json:"repo_path,omitempty"`
	GitOriginURL string `json:"git_origin_url,omitempty"`
}

// PeerRepairHandler handles the peer.repair RPC method.
type PeerRepairHandler struct {
	handleRepair PeerRepairFunc
}

// NewPeerRepairHandler creates a new peer.repair handler.
func NewPeerRepairHandler(fn PeerRepairFunc) *PeerRepairHandler {
	return &PeerRepairHandler{handleRepair: fn}
}

// Handle processes a peer.repair RPC call.
// The remote peer (the dialer) sends: token (required), daemon_id, name,
// address, and optional identity fields. On success, returns the local
// daemon's current identity metadata so the dialer can refresh its own
// entry.
func (h *PeerRepairHandler) Handle(_ context.Context, params json.RawMessage) (any, error) {
	var req struct {
		Token        string `json:"token"`
		DaemonID     string `json:"daemon_id"`
		Name         string `json:"name"`
		Address      string `json:"address,omitempty"`
		RepoName     string `json:"repo_name,omitempty"`
		Hostname     string `json:"hostname,omitempty"`
		RepoPath     string `json:"repo_path,omitempty"`
		GitOriginURL string `json:"git_origin_url,omitempty"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if req.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if req.DaemonID == "" {
		return nil, fmt.Errorf("daemon_id is required")
	}

	localDaemonID, localName, localRepoName, localHostname, localRepoPath, localGitOriginURL, err := h.handleRepair(
		req.Token, req.DaemonID, req.Name, req.Address,
		req.RepoName, req.Hostname, req.RepoPath, req.GitOriginURL,
	)
	if err != nil {
		return nil, err
	}

	return peerRepairResponse{
		Status:       "repaired",
		DaemonID:     localDaemonID,
		Name:         localName,
		RepoName:     localRepoName,
		Hostname:     localHostname,
		RepoPath:     localRepoPath,
		GitOriginURL: localGitOriginURL,
	}, nil
}
