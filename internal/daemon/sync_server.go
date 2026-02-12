package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/leonletto/thrum/internal/transport"
)

// SyncRegistry is a handler registry that only allows sync.* RPC methods.
// This provides a security boundary — application RPCs are never exposed over Tailscale.
type SyncRegistry struct {
	handlers    map[string]Handler
	rateLimiter *SyncRateLimiter // optional per-peer rate limiting
}

// NewSyncRegistry creates a new sync-only handler registry.
func NewSyncRegistry() *SyncRegistry {
	return &SyncRegistry{
		handlers: make(map[string]Handler),
	}
}

// SetRateLimiter configures per-peer rate limiting for sync requests.
func (r *SyncRegistry) SetRateLimiter(rl *SyncRateLimiter) {
	r.rateLimiter = rl
}

// allowedSyncMethods is the whitelist of RPC methods allowed on the sync endpoint.
var allowedSyncMethods = map[string]bool{
	"sync.pull":      true,
	"sync.peer_info": true,
	"sync.notify":    true,
}

// Register registers a handler for a sync RPC method.
// Returns an error if the method is not in the sync.* whitelist.
func (r *SyncRegistry) Register(method string, handler Handler) error {
	if !allowedSyncMethods[method] {
		return fmt.Errorf("method %q is not allowed in sync registry; only sync.* methods are permitted", method)
	}
	r.handlers[method] = handler
	return nil
}

// ServeSyncRPC reads JSON-RPC requests from a connection and dispatches only to sync handlers.
// peerID identifies the remote peer for rate limiting (from WhoIs or connection info).
// Application RPCs return "method not found". Uses the same wire format as the Unix socket server.
func (r *SyncRegistry) ServeSyncRPC(ctx context.Context, conn net.Conn, peerID string) {
	ctx = transport.WithTransport(ctx, transport.TransportTailscale)

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				Error:   &jsonRPCError{Code: -32700, Message: "parse error"},
			}
			_ = writeSyncResponse(writer, resp)
			continue
		}

		// Security boundary: reject any non-sync method
		if !strings.HasPrefix(req.Method, "sync.") || !allowedSyncMethods[req.Method] {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
			}
			_ = writeSyncResponse(writer, resp)
			continue
		}

		// Per-peer rate limiting
		if r.rateLimiter != nil {
			if rlErr := r.rateLimiter.Allow(peerID); rlErr != nil {
				code := -32000
				if rle, ok := rlErr.(*RateLimitError); ok {
					code = -rle.Code // Use negative HTTP code as JSON-RPC error
				}
				resp := jsonRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &jsonRPCError{Code: code, Message: rlErr.Error()},
				}
				_ = writeSyncResponse(writer, resp)
				continue
			}
		}

		handler, ok := r.handlers[req.Method]
		if !ok {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
			}
			_ = writeSyncResponse(writer, resp)
			continue
		}

		result, err := handler(ctx, req.Params)
		if err != nil {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: -32000, Message: err.Error()},
			}
			_ = writeSyncResponse(writer, resp)
			continue
		}

		resultJSON, err := json.Marshal(result)
		if err != nil {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: -32603, Message: "internal error"},
			}
			_ = writeSyncResponse(writer, resp)
			continue
		}

		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  resultJSON,
		}
		_ = writeSyncResponse(writer, resp)
	}
}

// writeSyncResponse writes a JSON-RPC response as a newline-delimited JSON line.
func writeSyncResponse(w *bufio.Writer, resp jsonRPCResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.WriteByte('\n'); err != nil {
		return err
	}
	return w.Flush()
}
