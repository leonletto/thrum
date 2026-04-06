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
func (c *SyncClient) wsCall(ctx context.Context, wsURL string, method string, params map[string]any) (json.RawMessage, error) {
	client := bridge.NewWSClient(wsURL)

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

// syncWSURL builds the WebSocket URL for a peer's sync endpoint using a token.
// PeerAddr is the "host:port" address stored in PeerInfo.Address.
func syncWSURL(peerAddr, token string) string {
	if token != "" {
		return fmt.Sprintf("ws://%s/ws?token=%s", peerAddr, token)
	}
	return fmt.Sprintf("ws://%s/ws", peerAddr)
}

// pairingWSURL builds the WebSocket URL for a peer's pairing endpoint.
func pairingWSURL(peerAddr, code string) string {
	return fmt.Sprintf("ws://%s/ws?pairing_code=%s", peerAddr, code)
}

// PullEvents connects to a peer and pulls events after the given sequence number.
// Token is included in the URL query string for authentication.
func (c *SyncClient) PullEvents(peerAddr string, afterSeq int64, token string) (*PullResponse, error) {
	ctx := context.Background()
	wsURL := syncWSURL(peerAddr, token)

	params := map[string]any{
		"after_sequence": afterSeq,
		"max_batch":      1000,
	}

	raw, err := c.wsCall(ctx, wsURL, "sync.pull", params)
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
// Token is included in the URL query string for authentication.
func (c *SyncClient) PullAllEvents(peerAddr string, afterSeq int64, token string, onBatch func(events []eventlog.Event, nextSeq int64) error) error {
	ctx := context.Background()
	wsURL := syncWSURL(peerAddr, token)

	// Open a single WebSocket connection and reuse it for all batches.
	client := bridge.NewWSClient(wsURL)

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
// Token is included in the URL query string for authentication. This is fire-and-forget.
func (c *SyncClient) SendNotify(peerAddr string, daemonID string, latestSeq int64, eventCount int, token string) error {
	ctx := context.Background()
	wsURL := syncWSURL(peerAddr, token)

	params := map[string]any{
		"daemon_id":   daemonID,
		"latest_seq":  latestSeq,
		"event_count": eventCount,
	}

	_, err := c.wsCall(ctx, wsURL, "sync.notify", params)
	return err
}

// QueryPeerInfo calls sync.peer_info on a peer and returns daemon identity.
// Token is included in the URL query string for authentication.
func (c *SyncClient) QueryPeerInfo(peerAddr string, token string) (*PeerInfoResult, error) {
	ctx := context.Background()
	wsURL := syncWSURL(peerAddr, token)

	raw, err := c.wsCall(ctx, wsURL, "sync.peer_info", nil)
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
	Status   string `json:"status"`
	Token    string `json:"token"`
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
}

// RequestPairing sends a pair.request to a remote peer using the pairing code.
// The connection uses ?pairing_code= (no token) since the goal is to obtain a token.
func (c *SyncClient) RequestPairing(peerAddr, code, localDaemonID, localName, localAddress string) (*PairResult, error) {
	ctx := context.Background()
	wsURL := pairingWSURL(peerAddr, code)

	params := map[string]any{
		"code":      code,
		"daemon_id": localDaemonID,
		"name":      localName,
		"address":   localAddress,
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
