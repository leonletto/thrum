package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

// PairRequestFunc handles a pairing request with code verification.
// Parameters: code, peerDaemonID, peerName, peerAddress, peerRepoName, peerHostname, peerRepoPath, peerGitOriginURL.
// Returns: token, localDaemonID, localName, localRepoName, localHostname, localRepoPath, localGitOriginURL, error.
type PairRequestFunc func(
	code, peerDaemonID, peerName, peerAddress string,
	peerRepoName, peerHostname, peerRepoPath, peerGitOriginURL string,
) (token, localDaemonID, localName, localRepoName, localHostname, localRepoPath, localGitOriginURL string, err error)

// pairRequestResponse is the wire format returned by pair.request on success.
type pairRequestResponse struct {
	Status       string `json:"status"`
	Token        string `json:"token"`
	DaemonID     string `json:"daemon_id"`
	Name         string `json:"name"`
	RepoName     string `json:"repo_name,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	RepoPath     string `json:"repo_path,omitempty"`
	GitOriginURL string `json:"git_origin_url,omitempty"`
}

// PairRequestHandler handles the pair.request RPC method on the Tailscale endpoint.
type PairRequestHandler struct {
	handlePair PairRequestFunc
}

// NewPairRequestHandler creates a new pair.request handler.
func NewPairRequestHandler(fn PairRequestFunc) *PairRequestHandler {
	return &PairRequestHandler{handlePair: fn}
}

// Handle handles a pair.request RPC call.
// The remote peer sends: code, daemon_id, name, address, and optional identity fields.
// On success, returns the local daemon's token, daemon_id, name, and identity metadata.
func (h *PairRequestHandler) Handle(_ context.Context, params json.RawMessage) (any, error) {
	var req struct {
		Code         string `json:"code"`
		DaemonID     string `json:"daemon_id"`
		Name         string `json:"name"`
		Address      string `json:"address"`
		RepoName     string `json:"repo_name,omitempty"`
		Hostname     string `json:"hostname,omitempty"`
		RepoPath     string `json:"repo_path,omitempty"`
		GitOriginURL string `json:"git_origin_url,omitempty"`
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

	token, localDaemonID, localName, localRepoName, localHostname, localRepoPath, localGitOriginURL, err := h.handlePair(
		req.Code, req.DaemonID, req.Name, req.Address,
		req.RepoName, req.Hostname, req.RepoPath, req.GitOriginURL,
	)
	if err != nil {
		return nil, err
	}

	return pairRequestResponse{
		Status:       "paired",
		Token:        token,
		DaemonID:     localDaemonID,
		Name:         localName,
		RepoName:     localRepoName,
		Hostname:     localHostname,
		RepoPath:     localRepoPath,
		GitOriginURL: localGitOriginURL,
	}, nil
}
