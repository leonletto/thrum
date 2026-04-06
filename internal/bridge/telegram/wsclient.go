package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"

	"github.com/leonletto/thrum/internal/bridge"
)

// Notification represents a JSON-RPC 2.0 server notification (no id).
type Notification = bridge.Notification

// WSClient wraps the shared bridge.WSClient with loopback-only validation.
type WSClient struct {
	*bridge.WSClient
}

// Dial connects to the given WebSocket URL and starts the read loop.
// The URL MUST point to a loopback address (127.0.0.1, [::1], or localhost).
func Dial(ctx context.Context, rawURL string) (*WSClient, error) {
	client := bridge.NewWSClient(rawURL, bridge.WithAddressValidator(validateLoopback))
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}
	return &WSClient{WSClient: client}, nil
}

// Close shuts down the client. Safe to call multiple times.
func (c *WSClient) Close() {
	_ = c.WSClient.Close()
}

// Call sends a JSON-RPC 2.0 request with the given method and params,
// then waits for the matching response.
func (c *WSClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	// The shared WSClient.Call takes map[string]any. Convert if needed.
	switch p := params.(type) {
	case map[string]any:
		return c.WSClient.Call(ctx, method, p)
	case nil:
		return c.WSClient.Call(ctx, method, nil)
	default:
		// Marshal and unmarshal to convert arbitrary types to map[string]any.
		data, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("unmarshal params: %w", err)
		}
		return c.WSClient.Call(ctx, method, m)
	}
}

// validateLoopback checks that the given URL resolves to a loopback address.
func validateLoopback(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid WebSocket URL: %w", err)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("WebSocket URL has no host")
	}

	if host == "localhost" {
		return nil
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("WebSocket URL host is not a loopback address")
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("WebSocket URL host is not a loopback address")
	}

	return nil
}
