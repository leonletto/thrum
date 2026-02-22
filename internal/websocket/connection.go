package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leonletto/thrum/internal/transport"
)

// Connection represents a WebSocket connection with JSON-RPC handling.
type Connection struct {
	conn      *websocket.Conn
	server    *Server
	sessionID string
	sendCh    chan []byte
	mu        sync.Mutex
	closed    bool
}

// NewConnection creates a new WebSocket connection wrapper.
func NewConnection(conn *websocket.Conn, server *Server) *Connection {
	return &Connection{
		conn:   conn,
		server: server,
		sendCh: make(chan []byte, 256), // Buffered channel for outgoing messages
	}
}

// ReadLoop reads messages from the WebSocket connection.
func (c *Connection) ReadLoop(ctx context.Context) error {
	defer func() {
		_ = c.Close()
	}()

	// Set read deadline
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Read message
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				return fmt.Errorf("read error: %w", err)
			}
			return nil
		}

		// Handle JSON-RPC request
		if err := c.handleRequest(ctx, message); err != nil {
			// Log error but continue
			fmt.Printf("Error handling request: %v\n", err)
		}
	}
}

// WriteLoop writes messages to the WebSocket connection.
func (c *Connection) WriteLoop(ctx context.Context) error {
	ticker := time.NewTicker(54 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case message := <-c.sendCh:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return fmt.Errorf("write error: %w", err)
			}

		case <-ticker.C:
			// Send ping to keep connection alive
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return fmt.Errorf("ping error: %w", err)
			}
		}
	}
}

// Send queues a message to be sent to the client.
func (c *Connection) Send(msg []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return fmt.Errorf("connection closed")
	}

	select {
	case c.sendCh <- msg:
		return nil
	default:
		return fmt.Errorf("send buffer full")
	}
}

// Close closes the WebSocket connection.
func (c *Connection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	close(c.sendCh)

	// Unregister from client registry and clean up subscriptions
	if c.sessionID != "" {
		c.server.clients.Unregister(c.sessionID)
		if c.server.onDisconnect != nil {
			c.server.onDisconnect(c.sessionID)
		}
	}

	return c.conn.Close()
}

// handleRequest processes a JSON-RPC request (single or batch).
func (c *Connection) handleRequest(ctx context.Context, data []byte) error {
	// Try to detect if this is a batch request (array)
	trimmed := json.RawMessage(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return c.handleBatchRequest(ctx, data)
	}

	return c.handleSingleRequest(ctx, data)
}

// handleSingleRequest processes a single JSON-RPC request.
func (c *Connection) handleSingleRequest(ctx context.Context, data []byte) error {
	// Parse JSON-RPC request
	var req jsonRPCRequest
	if err := json.Unmarshal(data, &req); err != nil {
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error: &jsonRPCError{
				Code:    -32700, // Parse error
				Message: "Parse error",
				Data:    err.Error(),
			},
		}
		return c.sendResponse(resp)
	}

	// Process the request and send the response
	resp := c.processSingleRequest(ctx, req)
	return c.sendResponse(resp)
}

// handleBatchRequest processes a batch of JSON-RPC requests.
func (c *Connection) handleBatchRequest(ctx context.Context, data []byte) error {
	// Parse batch request
	var requests []jsonRPCRequest
	if err := json.Unmarshal(data, &requests); err != nil {
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error: &jsonRPCError{
				Code:    -32700, // Parse error
				Message: "Parse error",
				Data:    err.Error(),
			},
		}
		return c.sendResponse(resp)
	}

	// Empty batch is invalid
	if len(requests) == 0 {
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error: &jsonRPCError{
				Code:    -32600, // Invalid request
				Message: "Invalid request",
				Data:    "batch request cannot be empty",
			},
		}
		return c.sendResponse(resp)
	}

	// Process each request and collect responses
	responses := make([]jsonRPCResponse, len(requests))
	for i, req := range requests {
		responses[i] = c.processSingleRequest(ctx, req)
	}

	// Send batch response
	return c.sendBatchResponse(responses)
}

// processSingleRequest processes a single request and returns the response (doesn't send it).
func (c *Connection) processSingleRequest(ctx context.Context, req jsonRPCRequest) jsonRPCResponse {
	// Validate JSON-RPC version
	if req.JSONRPC != "2.0" {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonRPCError{
				Code:    -32600, // Invalid request
				Message: "Invalid request",
				Data:    "jsonrpc field must be '2.0'",
			},
		}
	}

	// Get handler
	handler, ok := c.server.getHandler(req.Method)
	if !ok {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonRPCError{
				Code:    -32601, // Method not found
				Message: "Method not found",
				Data:    fmt.Sprintf("method '%s' is not registered", req.Method),
			},
		}
	}

	// Default nil params to empty JSON object so handlers can always unmarshal.
	// This happens when the client omits the "params" field (e.g. JSON.stringify
	// drops undefined values), which leaves req.Params as nil after parsing.
	params := req.Params
	if params == nil {
		params = json.RawMessage("{}")
	}

	// Call handler with WebSocket transport context
	ctxWithTransport := transport.WithTransport(ctx, transport.TransportWebSocket)
	result, err := handler(ctxWithTransport, params)
	if err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonRPCError{
				Code:    -32000, // Server error
				Message: err.Error(),
			},
		}
	}

	// Marshal result
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonRPCError{
				Code:    -32603, // Internal error
				Message: "Internal error",
				Data:    err.Error(),
			},
		}
	}

	// Success response
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  resultJSON,
	}
}

// sendResponse sends a JSON-RPC response to the client.
func (c *Connection) sendResponse(resp jsonRPCResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	return c.Send(data)
}

// sendBatchResponse sends a batch of JSON-RPC responses to the client.
func (c *Connection) sendBatchResponse(responses []jsonRPCResponse) error {
	data, err := json.Marshal(responses)
	if err != nil {
		return fmt.Errorf("marshal batch response: %w", err)
	}

	return c.Send(data)
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
