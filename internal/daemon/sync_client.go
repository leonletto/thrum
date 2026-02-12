package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"

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

// SyncClient connects to a peer daemon and pulls events.
type SyncClient struct {
	timeout time.Duration
}

// NewSyncClient creates a new sync client.
func NewSyncClient() *SyncClient {
	return &SyncClient{timeout: SyncClientTimeout}
}

// PullEvents connects to a peer and pulls events after the given sequence number.
func (c *SyncClient) PullEvents(peerAddr string, afterSeq int64) (*PullResponse, error) {
	conn, err := net.DialTimeout("tcp", peerAddr, c.timeout)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", peerAddr, err)
	}
	defer conn.Close()

	// Set deadline for the entire exchange
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	return c.pullBatch(conn, afterSeq, 1000)
}

// PullAllEvents pulls all events from a peer in batches, continuing until no more are available.
// Updates the checkpoint via the provided callback after each batch.
func (c *SyncClient) PullAllEvents(peerAddr string, afterSeq int64, onBatch func(events []eventlog.Event, nextSeq int64) error) error {
	conn, err := net.DialTimeout("tcp", peerAddr, c.timeout)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", peerAddr, err)
	}
	defer conn.Close()

	currentSeq := afterSeq

	for {
		// Set deadline per batch
		if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
			return fmt.Errorf("set deadline: %w", err)
		}

		resp, err := c.pullBatch(conn, currentSeq, 1000)
		if err != nil {
			return fmt.Errorf("pull batch after seq %d: %w", currentSeq, err)
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
// This is fire-and-forget — errors are returned but callers typically ignore them.
func (c *SyncClient) SendNotify(peerAddr string, daemonID string, latestSeq int64, eventCount int) error {
	conn, err := net.DialTimeout("tcp", peerAddr, c.timeout)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", peerAddr, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}

	params := map[string]any{
		"daemon_id":   daemonID,
		"latest_seq":  latestSeq,
		"event_count": eventCount,
	}

	_, err = c.callRPC(conn, "sync.notify", params)
	return err
}

// QueryPeerInfo calls sync.peer_info on a peer and returns daemon identity.
func (c *SyncClient) QueryPeerInfo(peerAddr string) (*PeerInfoResult, error) {
	conn, err := net.DialTimeout("tcp", peerAddr, c.timeout)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", peerAddr, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	resp, err := c.callRPC(conn, "sync.peer_info", nil)
	if err != nil {
		return nil, err
	}

	var info PeerInfoResult
	if err := json.Unmarshal(resp, &info); err != nil {
		return nil, fmt.Errorf("unmarshal peer info: %w", err)
	}
	return &info, nil
}

// PeerInfoResult represents peer identity information.
type PeerInfoResult struct {
	DaemonID  string `json:"daemon_id"`
	Hostname  string `json:"hostname"`
	PublicKey string `json:"public_key"`
}

// pullBatch sends a sync.pull request on an existing connection and reads the response.
func (c *SyncClient) pullBatch(conn net.Conn, afterSeq int64, maxBatch int) (*PullResponse, error) {
	params := map[string]any{
		"after_sequence": afterSeq,
		"max_batch":      maxBatch,
	}

	respData, err := c.callRPC(conn, "sync.pull", params)
	if err != nil {
		return nil, err
	}

	var resp PullResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal pull response: %w", err)
	}
	return &resp, nil
}

// rpcRequest is the JSON-RPC 2.0 request format used by the sync client.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      int    `json:"id"`
}

// rpcResponse is the JSON-RPC 2.0 response format.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      int             `json:"id"`
}

// rpcError represents a JSON-RPC error in the client response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var rpcIDCounter int

// callRPC sends a JSON-RPC request and reads the response on an existing connection.
func (c *SyncClient) callRPC(conn net.Conn, method string, params any) (json.RawMessage, error) {
	rpcIDCounter++
	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      rpcIDCounter,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	return resp.Result, nil
}
