package telegram

import (
	"context"

	"github.com/leonletto/thrum/internal/bridge"
)

// Notification represents a JSON-RPC 2.0 server notification (no id).
type Notification = bridge.Notification

// WSClient wraps the shared bridge.WSClient with loopback-only validation.
// The embedded bridge.WSClient satisfies bridge.TransportBridge directly.
type WSClient struct {
	*bridge.WSClient
}

// Dial connects to the given WebSocket URL and starts the read loop.
// The URL MUST point to a loopback address (127.0.0.1, [::1], or localhost).
func Dial(ctx context.Context, rawURL string) (*WSClient, error) {
	client := bridge.NewWSClient(rawURL, bridge.WithAddressValidator(bridge.LoopbackValidator))
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}
	return &WSClient{WSClient: client}, nil
}
