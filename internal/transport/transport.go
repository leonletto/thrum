package transport

import "context"

// Transport represents the type of connection transport.
type Transport int

const (
	// TransportUnknown represents an unknown transport type.
	TransportUnknown Transport = iota
	// TransportUnixSocket represents a Unix socket connection.
	TransportUnixSocket
	// TransportWebSocket represents a WebSocket connection.
	TransportWebSocket
)

// String returns the string representation of a transport type.
func (t Transport) String() string {
	switch t {
	case TransportUnixSocket:
		return "unix_socket"
	case TransportWebSocket:
		return "websocket"
	default:
		return "unknown"
	}
}

// transportKey is the context key for transport type.
type transportKey struct{}

// WithTransport returns a new context with the transport type set.
func WithTransport(ctx context.Context, transport Transport) context.Context {
	return context.WithValue(ctx, transportKey{}, transport)
}

// GetTransport retrieves the transport type from the context.
// Returns TransportUnknown if not set.
func GetTransport(ctx context.Context) Transport {
	if t, ok := ctx.Value(transportKey{}).(Transport); ok {
		return t
	}
	return TransportUnknown
}
