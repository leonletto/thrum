package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/transport"
)

// Handler is a function that handles a JSON-RPC request.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Server represents the Unix socket RPC server.
type Server struct {
	socketPath string
	listener   net.Listener
	handlers   map[string]Handler
	mu         sync.RWMutex
	shutdown   bool
	wg         sync.WaitGroup
	startTime  time.Time
}

// NewServer creates a new RPC server.
func NewServer(socketPath string) *Server {
	return &Server{
		socketPath: socketPath,
		handlers:   make(map[string]Handler),
		startTime:  time.Now(),
	}
}

// RegisterHandler registers a handler for a JSON-RPC method.
func (s *Server) RegisterHandler(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
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

	// Wait for all connections to finish (with timeout)
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
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		// Read request line
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

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

		// Call handler with transport context
		ctxWithTransport := transport.WithTransport(ctx, transport.TransportUnixSocket)
		result, err := handler(ctxWithTransport, req.Params)
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
