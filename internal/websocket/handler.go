package websocket

import (
	"context"
	"encoding/json"
)

// Handler is a function that handles a JSON-RPC request.
// This matches the daemon.Handler signature for compatibility.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// HandlerRegistry provides access to registered RPC handlers.
type HandlerRegistry interface {
	// GetHandler retrieves a handler by method name.
	// Returns the handler and true if found, nil and false otherwise.
	GetHandler(method string) (Handler, bool)
}
