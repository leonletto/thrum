package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
	"github.com/leonletto/thrum/internal/profile"
	"github.com/leonletto/thrum/internal/transport"
)

// Handler is a function that handles a JSON-RPC request.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Server represents the Unix socket RPC server.
type Server struct {
	socketPath       string
	listener         net.Listener
	handlers         map[string]Handler
	longPoll         map[string]bool
	mu               sync.RWMutex
	shutdown         bool
	wg               sync.WaitGroup
	startTime        time.Time
	connsMu          sync.Mutex            // protects conns
	conns            map[net.Conn]struct{} // active client connections
	identityResolver peercred.Resolver     // optional; nil disables per-connection identity resolution (tests, early boot)
}

// NewServer creates a new RPC server.
func NewServer(socketPath string) *Server {
	return &Server{
		socketPath: socketPath,
		handlers:   make(map[string]Handler),
		longPoll:   make(map[string]bool),
		startTime:  time.Now(),
		conns:      make(map[net.Conn]struct{}),
	}
}

// RegisterHandler registers a handler for a JSON-RPC method.
func (s *Server) RegisterHandler(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// RegisterLongPollHandler registers a handler that may block for an extended
// period (up to 6 minutes). These handlers receive a longer context timeout
// than the default 10-second limit used for normal RPC methods.
func (s *Server) RegisterLongPollHandler(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
	s.longPoll[method] = true
}

// SetIdentityResolver wires a peer-credential resolver into the server so that
// every RPC request gets its kernel-verified identity injected into the
// per-request context. Passing nil disables peercred-based identity injection
// entirely (used by tests and early-boot phases before the state DB is ready).
//
// The resolver is consulted once per RPC request (not once per connection) so
// that agents registered after a connection is accepted (e.g. quickstart) are
// picked up immediately without requiring a reconnect. For the CLI path this is
// net-zero cost (each command opens a fresh connection for a single RPC). For
// long-lived connections (MCP server) the overhead is one gopsutil Cwd + one
// session_refs query per call, which is ~1 ms and acceptable.
func (s *Server) SetIdentityResolver(r peercred.Resolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identityResolver = r
}

// anonymousAllowedMethods is the allowlist of JSON-RPC methods that callers
// without a resolved peercred identity may still invoke. The list is the
// union of:
//
//   - the baseline the bead acceptance criteria named (team.list, message.list,
//     group.list, session.list, agent.get/whoami, daemon.status),
//   - every read-only RPC in the daemon that does not require caller identity
//     for its correctness (health, session.get, tmux.status/capture/check-pane,
//     monitor.list/show/logs, sync/peer status queries, user.identify which
//     only reads git config).
//
// The coordinator's directive for v0.9.0: err on the side of ALLOWING more
// read-only RPCs, not fewer. A stuck `cd ~ && thrum team` is worse than a
// theoretically tighter boundary on read access. Mutating RPCs fall off this
// list and are rejected for anonymous callers.
//
// SECURITY: This list is consulted in handleConnection BEFORE the handler
// runs. Any method not in this list, when invoked by an anonymous caller,
// is rejected with a clear error — it never reaches the handler.
var anonymousAllowedMethods = map[string]bool{
	// Observability / liveness
	"health":           true,
	"daemon.status":    true,
	"sync.status":      true,
	"tsync.peers.list": true,
	"peer.list":        true,
	"peer.status":      true,
	"telegram.status":  true,
	// Read-only agent/team/session queries
	"agent.list":        true,
	"agent.whoami":      true,
	"agent.listContext": true,
	"team.list":         true,
	"session.list":      true,
	// Read-only context queries
	"context.show":          true,
	"context.preamble.show": true,
	// Read-only message/group queries
	"message.get":    true,
	"message.list":   true,
	"message.outbox": true,
	"group.list":     true,
	"group.info":     true,
	"group.members":  true,
	// Read-only monitor queries
	"monitor.list": true,
	"monitor.show": true,
	"monitor.logs": true,
	// Read-only tmux queries
	"tmux.status":       true,
	"tmux.capture":      true,
	"tmux.check-pane":   true,
	"tmux.queue-status": true,
	"tmux.queue-wait":   true,
	// Git-config identity read (no auth)
	"user.identify": true,
	// Bootstrap: the quickstart flow calls register → session.start →
	// session.setIntent on a single connection. Peercred identity is resolved
	// once at connection accept time, so even after agent.register populates
	// agent_work_contexts, the current connection stays tagged as anonymous.
	// All three bootstrap RPCs must be anonymous-allowed or daemon restart
	// creates a chicken-and-egg. Socket is 0600 so only the owning user can
	// reach these endpoints.
	"agent.register":    true,
	"session.start":     true,
	"session.setIntent": true,
	// Bootstrap (thrum-5oui — DO NOT REMOVE without re-reading the safety
	// invariant below): `thrum tmux start` is the agent-restart entry point.
	// On ONE connection it calls tmux.create (no_agent=true) THEN tmux.launch:
	//   - tmux.create with no_agent=true creates a BARE tmux session — no agent
	//     row, no identity binding, no THRUM_* env (see HandleCreate's
	//     `if !req.NoAgent` guards, internal/daemon/rpc/tmux.go).
	//   - tmux.launch sends a FIXED, name-validated runtime-launch command
	//     (runtimeToLaunchCmd + isValidRuntimeName, no arbitrary keystrokes —
	//     unlike tmux.send, which stays gated) into that session.
	// The launched runtime then self-registers live via quickstart →
	// agent.register (whose cross-worktree guard is the actual identity
	// authority). BOTH must be anonymous-allowed: a no_agent create writes no
	// session_ref, so the caller is STILL anonymous on the very next RPC
	// (identity is re-resolved per-RPC) — if tmux.launch were gated, the
	// bootstrap would create a bare session but never launch the runtime.
	// Without this pair, an UNBOUND caller (a fresh worktree, or an agent whose
	// session ended — sweeper-culled, idle, or killed) can never restart,
	// because binding requires an active session that only this bootstrap can
	// create. That chicken-and-egg is the whole agent-restart failure class
	// (thrum-5oui; routes qxr3/mnhp were prior members).
	//
	// SAFETY INVARIANT: tmux.create and tmux.launch are registered ONLY on the
	// local unix socket (cmd/thrum/main.go server.RegisterHandler), NEVER on
	// wsRegistry / the peer/tsnet transport. Allowing them anonymously
	// therefore widens only the 0600 owner-only socket — same boundary as
	// agent.register/session.start above. TestRestartClass_TmuxCreateNotOnWebSocket
	// and TestRestartClass_TmuxLaunchNotOnWebSocket (internal/daemon/rpc/
	// sec8_trust_boundary_test.go) pin this; if either gains a wsRegistry/peer
	// registration, that test MUST fail (anonymous create/launch over the
	// network would be a real hole).
	"tmux.create": true,
	"tmux.launch": true,
}

// Start starts the server and begins accepting connections.
func (s *Server) Start(ctx context.Context) error {
	// Ensure socket directory exists
	socketDir := filepath.Dir(s.socketPath)
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	// Remove old socket if it exists
	if err := s.removeOldSocket(); err != nil {
		return fmt.Errorf("failed to remove old socket: %w", err)
	}

	// Create Unix socket listener
	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}
	s.listener = listener

	// Set socket permissions to owner-only
	if err := os.Chmod(s.socketPath, 0600); err != nil {
		_ = listener.Close()
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	// Accept connections in a goroutine
	s.wg.Add(1)
	go s.acceptLoop(ctx)

	return nil
}

// Stop stops the server and waits for all connections to finish.
func (s *Server) Stop() error {
	s.mu.Lock()
	s.shutdown = true
	s.mu.Unlock()

	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			return fmt.Errorf("failed to close listener: %w", err)
		}
	}

	// Force-close all active client connections so long-polling clients
	// (like `thrum wait`) immediately detect the disconnect and can
	// reconnect to the new daemon after a restart.
	s.connsMu.Lock()
	for conn := range s.conns {
		_ = conn.Close()
	}
	s.connsMu.Unlock()

	// Wait for all connection goroutines to finish (with timeout)
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All connections finished
	case <-time.After(5 * time.Second):
		// Timeout waiting for connections
	}

	// Remove socket file
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove socket: %w", err)
	}

	return nil
}

// removeOldSocket removes a stale socket file.
func (s *Server) removeOldSocket() error {
	if _, err := os.Stat(s.socketPath); err == nil {
		// Try to connect to see if socket is active
		conn, err := net.DialTimeout("unix", s.socketPath, 500*time.Millisecond)
		if err == nil {
			// Socket is active
			_ = conn.Close()
			return fmt.Errorf("socket %s is in use by another daemon", s.socketPath)
		}

		// Socket is stale, remove it
		if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove stale socket: %w", err)
		}
	}
	return nil
}

// acceptLoop accepts connections in a loop.
func (s *Server) acceptLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.RLock()
			shutdown := s.shutdown
			s.mu.RUnlock()
			if shutdown {
				return
			}
			// Log error but continue
			fmt.Fprintf(os.Stderr, "accept error: %v\n", err)
			continue
		}

		s.wg.Add(1)
		go s.handleConnection(ctx, conn)
	}
}

// handleConnection handles a single connection.
func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer s.wg.Done()

	// Track this connection so Stop() can force-close it.
	s.connsMu.Lock()
	s.conns[conn] = struct{}{}
	s.connsMu.Unlock()
	defer func() {
		s.connsMu.Lock()
		delete(s.conns, conn)
		s.connsMu.Unlock()
		_ = conn.Close()
	}()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		// Set read deadline so idle connections don't accumulate forever.
		// Clients reconnect as needed; 5 minutes is generous for keep-alive.
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))

		// Read request line
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		// thrum-bpq5 substrate: arrival timestamp + per-phase RPC timing.
		// Gated by THRUM_PROFILE.
		rpcArrived := time.Now()

		// Parse JSON-RPC request
		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error: &jsonRPCError{
					Code:    -32700, // Parse error
					Message: "Parse error",
					Data:    err.Error(),
				},
			}
			if err := s.writeResponse(writer, resp); err != nil {
				return
			}
			continue
		}

		// Validate JSON-RPC version
		if req.JSONRPC != "2.0" {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &jsonRPCError{
					Code:    -32600, // Invalid request
					Message: "Invalid request",
					Data:    "jsonrpc field must be '2.0'",
				},
			}
			if err := s.writeResponse(writer, resp); err != nil {
				return
			}
			continue
		}

		// Get handler
		s.mu.RLock()
		handler, ok := s.handlers[req.Method]
		s.mu.RUnlock()

		if !ok {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &jsonRPCError{
					Code:    -32601, // Method not found
					Message: "Method not found",
					Data:    fmt.Sprintf("method '%s' is not registered", req.Method),
				},
			}
			if err := s.writeResponse(writer, resp); err != nil {
				return
			}
			continue
		}

		// Default nil params to empty JSON object so handlers can always unmarshal.
		reqParams := req.Params
		if reqParams == nil {
			reqParams = json.RawMessage("{}")
		}

		// Call handler with transport context and per-request timeout.
		// This prevents a single blocked handler from permanently hanging
		// the connection goroutine (which cascades into daemon deadlock).
		// Long-poll methods (e.g. peer.wait_pairing) need a longer timeout
		// since they block waiting for human interaction.
		s.mu.RLock()
		isLongPoll := s.longPoll[req.Method]
		s.mu.RUnlock()
		timeout := 10 * time.Second
		if isLongPoll {
			timeout = 6 * time.Minute
		}
		reqCtx, reqCancel := context.WithTimeout(ctx, timeout)
		ctxWithTransport := transport.WithTransport(reqCtx, transport.TransportUnixSocket)

		// Resolve peer-credential identity per-RPC. Doing this here (not once
		// at connection-accept time) prevents the anonymous-latch bug: if an
		// agent registers AFTER the connection is accepted but BEFORE the first
		// RPC body arrives, a per-connection resolve would latch ErrAnonymous
		// for the lifetime of that connection. Re-resolving on every RPC means
		// freshly-registered agents are visible immediately without a reconnect.
		//
		// Cost: one gopsutil Cwd + one session_refs query per RPC. For the CLI
		// path this is net-zero (each CLI command opens a fresh connection for
		// a single RPC). For long-lived connections (MCP server) the overhead
		// is ~1 ms per call, which is acceptable.
		//
		// When the resolver is absent (tests, browser/WS transport): skip the
		// block. The context stays un-injected — same as the old
		// connResolved=false path — so all existing tests keep passing.
		ctxWithIdentity := ctxWithTransport
		s.mu.RLock()
		resolver := s.identityResolver
		s.mu.RUnlock()
		if resolver != nil {
			// Extract the kernel-verified PID so handlers can walk the caller's
			// ancestor chain independent of identity resolution success.
			// Rule #4‴'s ancestor-chain clause wire-up is tracked in thrum-u5fk.4.
			if pid, err := peercred.PIDFromConn(conn); err == nil {
				ctxWithIdentity = peercred.WithConnectingPID(ctxWithIdentity, pid)
			}

			reqIdentity, resolveErr := resolver.Resolve(conn)
			if resolveErr == nil || errors.Is(resolveErr, peercred.ErrAnonymous) {
				// resolved (with or without a match) — inject result and enforce
				// the anonymous allowlist.
				ctxWithIdentity = peercred.WithIdentity(ctxWithIdentity, reqIdentity)

				// Read-only allowlist enforcement: an anonymous caller (peercred
				// ran but no registered worktree matched) may only invoke
				// methods on the allowlist. Anything else is rejected here,
				// before the handler runs.
				if reqIdentity == nil && !anonymousAllowedMethods[req.Method] {
					// thrum-wk7d (part 2): now that resolver_unix.go's
					// no-match logs at DEBUG, this is the canonical
					// daemon-side signal that an anonymous caller hit a
					// non-allowlisted method. The structured error
					// response below is the user-facing diagnostic; this
					// WARN gives operators reading the daemon log a
					// single line per actual rejection (rather than one
					// per every anonymous-allowed call).
					slog.Warn("anonymous caller rejected: method not in anonymous allowlist",
						"method", req.Method,
						"remote_addr", conn.RemoteAddr().String(),
						"code", -32002)
					reqCancel()
					resp := jsonRPCResponse{
						JSONRPC: "2.0",
						ID:      req.ID,
						Error: &jsonRPCError{
							Code: -32002, // anonymous caller not permitted
							// thrum-8nro.3: 'cd into a registered agent worktree'
							// is misleading when the caller IS already in one
							// but the daemon's binding cache hasn't been warmed
							// (post-restart, post-edit, etc.). 'thrum prime' is
							// the actual recovery in that case.
							Message: fmt.Sprintf("anonymous caller cannot invoke %q: daemon hasn't bound this caller to a registered agent. If you ARE a registered agent in this worktree, run 'thrum prime' to warm the binding cache, then retry. Otherwise cd into the agent's worktree first.", req.Method),
						},
					}
					if err := s.writeResponse(writer, resp); err != nil {
						return
					}
					continue
				}
			}
			// Any other error (kernel denied peercred, list lookup failed)
			// leaves ctxWithIdentity un-injected — the connection falls back
			// to legacy behavior to avoid wedging the daemon on transient DB errors.
		}

		handlerStart := time.Now()
		preHandlerMs := handlerStart.Sub(rpcArrived).Milliseconds()
		result, err := handler(ctxWithIdentity, reqParams)
		reqCancel()
		handlerMs := time.Since(handlerStart).Milliseconds()
		if profile.Enabled() {
			slog.Info("profile.rpc.dispatch",
				"method", req.Method,
				"pre_handler_ms", preHandlerMs,
				"handler_ms", handlerMs,
			)
		}
		if err != nil {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &jsonRPCError{
					Code:    -32000, // Server error
					Message: err.Error(),
				},
			}
			if err := s.writeResponse(writer, resp); err != nil {
				return
			}
			continue
		}

		// Marshal result
		resultJSON, err := json.Marshal(result)
		if err != nil {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &jsonRPCError{
					Code:    -32603, // Internal error
					Message: "Internal error",
					Data:    err.Error(),
				},
			}
			if err := s.writeResponse(writer, resp); err != nil {
				return
			}
			continue
		}

		// Success response
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  resultJSON,
		}
		if err := s.writeResponse(writer, resp); err != nil {
			return
		}
	}
}

// writeResponse writes a JSON-RPC response to the connection.
func (s *Server) writeResponse(writer *bufio.Writer, resp jsonRPCResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}

	if _, err := writer.Write(data); err != nil {
		return err
	}

	if err := writer.WriteByte('\n'); err != nil {
		return err
	}

	return writer.Flush()
}

// JSON-RPC 2.0 request structure.
type jsonRPCRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
	ID      *json.RawMessage `json:"id,omitempty"`
}

// JSON-RPC 2.0 response structure.
type jsonRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *jsonRPCError    `json:"error,omitempty"`
	ID      *json.RawMessage `json:"id,omitempty"`
}

// JSON-RPC 2.0 error structure.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
