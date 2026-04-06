package bridge

import (
	"context"
	"encoding/json"
)

// Notification is a server-push JSON-RPC frame with no id (fire-and-forget).
type Notification struct {
	Method string
	Params json.RawMessage
}

// TransportBridge is the common interface for all peer connections.
// Implementations handle connection lifecycle, JSON-RPC 2.0 framing,
// and auth. The routing layer consumes Call() and Notifications().
type TransportBridge interface {
	// PeerName returns the human-readable name of the remote peer.
	PeerName() string

	// Call sends a JSON-RPC 2.0 request and waits for the response.
	Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error)

	// Notifications returns a channel of server-push notifications.
	Notifications() <-chan Notification

	// Connect establishes the connection to the remote peer.
	Connect(ctx context.Context) error

	// Close terminates the connection.
	Close() error

	// Connected reports whether the transport is currently connected.
	Connected() bool
}
