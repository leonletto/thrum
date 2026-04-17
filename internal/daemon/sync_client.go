package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/bridge"
	"github.com/leonletto/thrum/internal/daemon/eventlog"
)

// SyncClientTimeout is the connection timeout for sync client dials.
const SyncClientTimeout = 5 * time.Second

// PullResponse represents the response from a sync.pull RPC call.
type PullResponse struct {
	Events        []eventlog.Event `json:"events"`
	NextSequence  int64            `json:"next_sequence"`
	MoreAvailable bool             `json:"more_available"`
}

// SyncClient connects to a peer daemon via WebSocket and pulls events.
type SyncClient struct {
	timeout time.Duration
}

// NewSyncClient creates a new sync client.
func NewSyncClient() *SyncClient {
	return &SyncClient{timeout: SyncClientTimeout}
}

// wsCall dials a WebSocket connection to wsURL, calls the given JSON-RPC method
// with params, and returns the raw result. The connection is closed when done.
// Extra DialOptions (e.g. WithBearerToken) are forwarded to the WSClient so
// credentials travel in handshake headers instead of the URL.
func (c *SyncClient) wsCall(ctx context.Context, wsURL string, method string, params map[string]any, opts ...bridge.DialOption) (json.RawMessage, error) {
	client := bridge.NewWSClient(wsURL, opts...)

	dialCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	if err := client.Connect(dialCtx); err != nil {
		return nil, fmt.Errorf("connect to %s: %w", wsURL, err)
	}
	defer func() { _ = client.Close() }()

	callCtx, callCancel := context.WithTimeout(ctx, 30*time.Second)
	defer callCancel()

	return client.Call(callCtx, method, params)
}

// syncWSURL builds the WebSocket URL for a peer's sync endpoint.
// The peer token is NOT included — callers pass it via bridge.WithBearerToken
// so it travels in the Authorization header instead of the URL.
func syncWSURL(peerAddr string) string {
	return fmt.Sprintf("ws://%s/ws", peerAddr)
}

// tokenDialOpts returns DialOptions that attach the peer token as a
// Bearer credential. Returns an empty slice when token is empty.
func tokenDialOpts(token string) []bridge.DialOption {
	if token == "" {
		return nil
	}
	return []bridge.DialOption{bridge.WithBearerToken(token)}
}

// pairingWSURL builds the WebSocket URL for a peer's pairing endpoint.
func pairingWSURL(peerAddr, code string) string {
	return fmt.Sprintf("ws://%s/ws?pairing_code=%s", peerAddr, code)
}

// PullEvents connects to a peer and pulls events after the given sequence number.
// Token is sent as an Authorization: Bearer header on the WebSocket handshake.
func (c *SyncClient) PullEvents(peerAddr string, afterSeq int64, token string) (*PullResponse, error) {
	ctx := context.Background()
	wsURL := syncWSURL(peerAddr)

	params := map[string]any{
		"after_sequence": afterSeq,
		"max_batch":      1000,
	}

	raw, err := c.wsCall(ctx, wsURL, "sync.pull", params, tokenDialOpts(token)...)
	if err != nil {
		return nil, fmt.Errorf("sync.pull from %s: %w", peerAddr, err)
	}

	var resp PullResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal pull response: %w", err)
	}
	return &resp, nil
}

// PullAllEvents pulls all events from a peer in batches, continuing until no more are available.
// Token is sent as an Authorization: Bearer header on the WebSocket handshake.
func (c *SyncClient) PullAllEvents(peerAddr string, afterSeq int64, token string, onBatch func(events []eventlog.Event, nextSeq int64) error) error {
	ctx := context.Background()
	wsURL := syncWSURL(peerAddr)

	// Open a single WebSocket connection and reuse it for all batches.
	client := bridge.NewWSClient(wsURL, tokenDialOpts(token)...)

	dialCtx, dialCancel := context.WithTimeout(ctx, c.timeout)
	if err := client.Connect(dialCtx); err != nil {
		dialCancel()
		return fmt.Errorf("connect to %s: %w", peerAddr, err)
	}
	dialCancel()
	defer func() { _ = client.Close() }()

	currentSeq := afterSeq

	for {
		params := map[string]any{
			"after_sequence": currentSeq,
			"max_batch":      1000,
		}

		callCtx, callCancel := context.WithTimeout(ctx, 30*time.Second)
		raw, err := client.Call(callCtx, "sync.pull", params)
		callCancel()
		if err != nil {
			return fmt.Errorf("sync.pull batch after seq %d: %w", currentSeq, err)
		}

		var resp PullResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return fmt.Errorf("unmarshal pull response: %w", err)
		}

		if len(resp.Events) == 0 {
			return nil
		}

		if err := onBatch(resp.Events, resp.NextSequence); err != nil {
			return fmt.Errorf("process batch: %w", err)
		}

		currentSeq = resp.NextSequence

		if !resp.MoreAvailable {
			return nil
		}
	}
}

// SendNotify sends a sync.notify RPC to a peer, signaling that new events are available.
// Token is sent as an Authorization: Bearer header. This is fire-and-forget.
func (c *SyncClient) SendNotify(peerAddr string, daemonID string, latestSeq int64, eventCount int, token string) error {
	ctx := context.Background()
	wsURL := syncWSURL(peerAddr)

	params := map[string]any{
		"daemon_id":   daemonID,
		"latest_seq":  latestSeq,
		"event_count": eventCount,
	}

	_, err := c.wsCall(ctx, wsURL, "sync.notify", params, tokenDialOpts(token)...)
	return err
}

// QueryPeerInfo calls sync.peer_info on a peer and returns daemon identity.
// Token is sent as an Authorization: Bearer header.
func (c *SyncClient) QueryPeerInfo(peerAddr string, token string) (*PeerInfoResult, error) {
	ctx := context.Background()
	wsURL := syncWSURL(peerAddr)

	raw, err := c.wsCall(ctx, wsURL, "sync.peer_info", nil, tokenDialOpts(token)...)
	if err != nil {
		return nil, err
	}

	var info PeerInfoResult
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("unmarshal peer info: %w", err)
	}
	return &info, nil
}

// PeerInfoResult represents peer identity information.
type PeerInfoResult struct {
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
}

// PairResult represents the response from a pair.request RPC call.
type PairResult struct {
	Status       string `json:"status"`
	Token        string `json:"token"`
	DaemonID     string `json:"daemon_id"`
	Name         string `json:"name"`
	RepoName     string `json:"repo_name,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	RepoPath     string `json:"repo_path,omitempty"`
	GitOriginURL string `json:"git_origin_url,omitempty"`
}

// RequestPairing sends a pair.request to a remote peer using the pairing code.
// local carries the full identity metadata of this daemon sent to the remote.
// The connection uses ?pairing_code= (no token) since the goal is to obtain a token.
func (c *SyncClient) RequestPairing(peerAddr, code string, local PairMetadata) (*PairResult, error) {
	ctx := context.Background()
	wsURL := pairingWSURL(peerAddr, code)

	params := map[string]any{
		"code":           code,
		"daemon_id":      local.DaemonID,
		"name":           local.Name,
		"address":        local.Address,
		"repo_name":      local.RepoName,
		"hostname":       local.Hostname,
		"repo_path":      local.RepoPath,
		"git_origin_url": local.GitOriginURL,
	}

	raw, err := c.wsCall(ctx, wsURL, "pair.request", params)
	if err != nil {
		return nil, fmt.Errorf("pair.request to %s: %w", peerAddr, err)
	}

	var result PairResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal pair result: %w", err)
	}
	return &result, nil
}
