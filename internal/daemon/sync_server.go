package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/leonletto/thrum/internal/transport"
)

// SyncRegistry is a handler registry that only allows sync.* RPC methods.
// This provides a security boundary — application RPCs are never exposed over Tailscale.
// Token-based auth is enforced for sync.* methods; pair.request is exempt.
type SyncRegistry struct {
	handlers map[string]Handler
	peers    *PeerRegistry // for token auth; nil disables auth
}

// NewSyncRegistry creates a new sync-only handler registry.
func NewSyncRegistry() *SyncRegistry {
	return &SyncRegistry{
		handlers: make(map[string]Handler),
	}
}

// SetPeerRegistry enables token-based auth for sync.* RPCs.
// When set, all sync.* methods require a valid peer token in params.
// Pair.request is exempt (it's how peers obtain tokens).
func (r *SyncRegistry) SetPeerRegistry(peers *PeerRegistry) {
	r.peers = peers
}

// tokenExtract is used to extract just the token field from RPC params.
type tokenExtract struct {
	Token string `json:"token"`
}

// methodRequiresAuth returns true if the method requires token authentication.
func methodRequiresAuth(method string) bool {
	return method != "pair.request"
}

// allowedSyncMethods is the whitelist of RPC methods allowed on the Tailscale endpoint.
var allowedSyncMethods = map[string]bool{
	"sync.pull":      true,
	"sync.peer_info": true,
	"sync.notify":    true,
	"pair.request":   true,
}

// Register registers a handler for a sync RPC method.
// Returns an error if the method is not in the sync.* whitelist.
func (r *SyncRegistry) Register(method string, handler Handler) error {
	if !allowedSyncMethods[method] {
		return fmt.Errorf("method %q is not allowed in sync registry; only sync.*/pair.* methods are permitted", method)
	}
	r.handlers[method] = handler
	return nil
}

// ServeSyncRPC reads JSON-RPC requests from a connection and dispatches only to sync handlers.
// PeerID identifies the remote peer for rate limiting (from WhoIs or connection info).
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

		// Security boundary: only allow whitelisted methods
		if !allowedSyncMethods[req.Method] {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
			}
			_ = writeSyncResponse(writer, resp)
			continue
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

		// Token auth for sync.* methods (pair.request is exempt)
		var authedPeerID string
		if r.peers != nil && methodRequiresAuth(req.Method) {
			var te tokenExtract
			if req.Params != nil {
				_ = json.Unmarshal(req.Params, &te)
			}
			peer := r.peers.FindPeerByToken(te.Token)
			if peer == nil {
				resp := jsonRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &jsonRPCError{Code: -32000, Message: "unauthorized: unknown peer token"},
				}
				_ = writeSyncResponse(writer, resp)
				continue
			}
			authedPeerID = peer.DaemonID
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

		// Update last_sync for authenticated peers on successful requests
		if authedPeerID != "" && r.peers != nil {
			_ = r.peers.UpdateLastSync(authedPeerID)
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
