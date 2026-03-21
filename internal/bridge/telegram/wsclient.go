package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// readDeadline is the read deadline for the WebSocket connection.
	// The server sends pings every 54s; we reset the deadline on pong.
	// 90s allows for jitter beyond the 60s server timeout.
	readDeadline = 90 * time.Second

	// notifyChanSize is the buffer size for the notifications channel.
	notifyChanSize = 256
)

// Notification represents a JSON-RPC 2.0 server notification (no id).
type Notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// rpcError is the JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("RPC error %d: %s", e.Code, e.Message)
}

// rpcResponse is the shape of a JSON-RPC 2.0 response frame.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// WSClient is a JSON-RPC 2.0 client over WebSocket that connects to the
// Thrum daemon's WS server. It sends RPC calls and receives BroadcastAll
// notifications (e.g. notification.message).
type WSClient struct {
	conn      *websocket.Conn
	url       string
	nextID    atomic.Int64
	pending   map[int64]chan rpcResponse
	notifyCh  chan Notification
	mu        sync.Mutex // guards pending map
	writeMu   sync.Mutex // guards conn.WriteMessage (gorilla allows one concurrent writer)
	done      chan struct{}
	closeOnce sync.Once
}

// Dial connects to the given WebSocket URL and starts the read loop.
// The URL MUST point to a loopback address (127.0.0.1, [::1], or localhost).
func Dial(ctx context.Context, rawURL string) (*WSClient, error) {
	if err := validateLoopback(rawURL); err != nil {
		return nil, err
	}

	dialer := &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.DialContext(ctx, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("websocket dial: %w", err)
	}

	c := &WSClient{
		conn:     conn,
		url:      rawURL,
		pending:  make(map[int64]chan rpcResponse),
		notifyCh: make(chan Notification, notifyChanSize),
		done:     make(chan struct{}),
	}

	// Reset read deadline when we receive pings from the server (server pings
	// every 54s). Without this, the client's 90s read deadline expires during
	// idle periods and kills the connection.
	conn.SetPingHandler(func(appData string) error {
		// Reset read deadline on each server ping
		_ = conn.SetReadDeadline(time.Now().Add(readDeadline))
		// Send pong back (gorilla's default handler does this, but we replaced it)
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})
	// Also handle pongs (in case we send pings in the future).
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readDeadline))
	})
	// Set initial read deadline.
	if err := conn.SetReadDeadline(time.Now().Add(readDeadline)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set read deadline: %w", err)
	}

	go c.readLoop()

	return c, nil
}

// validateLoopback checks that the given URL resolves to a loopback address.
// It returns an error if the host is not 127.0.0.1, [::1], or localhost.
func validateLoopback(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid WebSocket URL: %w", err)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("WebSocket URL has no host")
	}

	// Resolve the host to an IP address and check if it is a loopback.
	// "localhost" typically resolves to 127.0.0.1 or ::1, but we also
	// accept it by name to avoid DNS-related failures in tests.
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

// Call sends a JSON-RPC 2.0 request with the given method and params,
// then waits for the matching response. It respects ctx cancellation.
func (c *WSClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Register pending channel before writing so we don't miss the response.
	respCh := make(chan rpcResponse, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, fmt.Errorf("client closed")
	default:
	}
	c.pending[id] = respCh
	c.mu.Unlock()

	// Clean up pending entry on return.
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	c.writeMu.Lock()
	writeErr := c.conn.WriteMessage(websocket.TextMessage, data)
	c.writeMu.Unlock()
	if writeErr != nil {
		return nil, fmt.Errorf("write RPC request: %w", writeErr)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("client closed")
	case resp := <-respCh:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// Notifications returns the channel on which server-push notifications
// (JSON-RPC frames without an id) are delivered.
func (c *WSClient) Notifications() <-chan Notification {
	return c.notifyCh
}

// Close shuts down the client. Safe to call multiple times.
func (c *WSClient) Close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()

		// Unblock any pending calls.
		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
	})
}

// readLoop reads frames from the WebSocket, routing responses to pending
// callers and notifications to notifyCh.
func (c *WSClient) readLoop() {
	defer func() {
		// Signal done and drain pending callers if not already closed.
		c.closeOnce.Do(func() {
			close(c.done)
			_ = c.conn.Close()

			c.mu.Lock()
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
		})
	}()

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		var frame rpcResponse
		if err := json.Unmarshal(msg, &frame); err != nil {
			continue // skip unparseable frames
		}

		if frame.ID != nil {
			// This is a response to a pending call.
			c.mu.Lock()
			ch, ok := c.pending[*frame.ID]
			c.mu.Unlock()
			if ok {
				select {
				case ch <- frame:
				default:
				}
			}
			continue
		}

		// No id: treat as a notification if it has a method.
		if frame.Method != "" {
			notif := Notification{
				Method: frame.Method,
				Params: frame.Params,
			}
			select {
			case c.notifyCh <- notif:
			default:
				// Drop if buffer full; client is not consuming fast enough.
			}
		}
	}
}
