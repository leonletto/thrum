package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"sync/atomic"

	"github.com/leonletto/thrum/internal/paths"
)

// Client is a JSON-RPC client that connects to the Thrum daemon via Unix socket.
type Client struct {
	conn       net.Conn
	socketPath string
	nextID     atomic.Uint64
}

// NewClient creates a new RPC client connected to the daemon at the given socket path.
func NewClient(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon at %s: %w", socketPath, err)
	}

	client := &Client{
		conn:       conn,
		socketPath: socketPath,
	}
	client.nextID.Store(1)

	return client, nil
}

// Call makes a JSON-RPC call to the daemon
// method: the RPC method name (e.g., "health", "message.send")
// params: the parameters to send (will be JSON-encoded)
// result: pointer to store the result (will be JSON-decoded)
func (c *Client) Call(method string, params any, result any) error {
	// Create JSON-RPC request
	id := c.nextID.Add(1)
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	// Encode and send request
	encoder := json.NewEncoder(c.conn)
	if err := encoder.Encode(request); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	// Read and decode response
	decoder := json.NewDecoder(c.conn)
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      uint64          `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    any    `json:"data,omitempty"`
		} `json:"error,omitempty"`
	}

	if err := decoder.Decode(&response); err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for RPC error
	if response.Error != nil {
		return fmt.Errorf("RPC error %d: %s", response.Error.Code, response.Error.Message)
	}

	// Decode result if provided
	if result != nil && len(response.Result) > 0 {
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("failed to decode result: %w", err)
		}
	}

	return nil
}

// Close closes the connection to the daemon.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// DefaultSocketPath returns the default socket path for a given repository.
// It follows .thrum/redirect files so feature worktrees connect to the
// daemon running in the main worktree.
func DefaultSocketPath(repoPath string) string {
	thrumDir, err := paths.ResolveThrumDir(repoPath)
	if err != nil {
		// Fall back to local path if redirect fails
		return filepath.Join(repoPath, ".thrum", "var", "thrum.sock")
	}
	return filepath.Join(thrumDir, "var", "thrum.sock")
}
