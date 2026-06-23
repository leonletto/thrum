package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// ReadDeadline is the read deadline for the WebSocket connection.
	// The server sends pings every 54s; we reset the deadline on pong.
	// 90s allows for jitter beyond the 60s server timeout.
	readDeadline = 90 * time.Second

	// NotifyChanSize is the buffer size for the notifications channel.
	notifyChanSize = 256
)

// ErrAddressRejected is returned when the address validator rejects a URL.
var ErrAddressRejected = fmt.Errorf("address rejected")

// DialOption configures a WSClient.
type DialOption func(*WSClient)

// WithAddressValidator sets a function that validates the target address
// before connecting. Return nil to allow, non-nil error to reject.
func WithAddressValidator(fn func(string) error) DialOption {
	return func(c *WSClient) { c.addrValidator = fn }
}

// WithPeerName sets the human-readable name for the remote peer.
func WithPeerName(name string) DialOption {
	return func(c *WSClient) { c.peerName = name }
}

// WithHeaders sets HTTP headers to send on the WebSocket handshake.
// Use this to pass credentials out-of-band (e.g. Authorization: Bearer)
// instead of encoding them in the URL, where they would leak into
// access logs, browser history, and intermediate proxies.
func WithHeaders(h http.Header) DialOption {
	return func(c *WSClient) { c.headers = h }
}

// WithBearerToken is a convenience wrapper that sets
// "Authorization: Bearer <token>" on the handshake.
func WithBearerToken(token string) DialOption {
	return func(c *WSClient) {
		if c.headers == nil {
			c.headers = http.Header{}
		}
		c.headers.Set("Authorization", "Bearer "+token)
	}
}

// LoopbackValidator rejects non-loopback addresses.
// Use as WithAddressValidator(LoopbackValidator).
func LoopbackValidator(addr string) error {
	u, err := url.Parse(addr)
	if err != nil {
		return err
	}
	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%w: %s is not loopback", ErrAddressRejected, host)
	}
	return nil
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

// WSClient is a JSON-RPC 2.0 client over WebSocket. It sends RPC calls and
// receives server-push notifications. Implements TransportBridge.
type WSClient struct {
	conn          *websocket.Conn
	url           string
	peerName      string
	headers       http.Header
	nextID        atomic.Int64
	pending       map[int64]chan rpcResponse
	notifyCh      chan Notification
	mu            sync.Mutex // guards pending map
	writeMu       sync.Mutex // guards conn.WriteMessage
	done          chan struct{}
	closeOnce     sync.Once
	connected     atomic.Bool
	addrValidator func(string) error
}

// NewWSClient creates a new WSClient targeting the given WebSocket URL.
// Call Connect() to establish the connection.
func NewWSClient(rawURL string, opts ...DialOption) *WSClient {
	c := &WSClient{
		url:      rawURL,
		pending:  make(map[int64]chan rpcResponse),
		notifyCh: make(chan Notification, notifyChanSize),
		done:     make(chan struct{}),
	}
	for _, o := range opts {
		o(c)
	}
	// Default peer name from URL host.
	if c.peerName == "" {
		if u, err := url.Parse(rawURL); err == nil {
			c.peerName = u.Host
		}
	}
	return c
}

// PeerName returns the human-readable name of the remote peer.
func (c *WSClient) PeerName() string { return c.peerName }

// Connected reports whether the transport is currently connected.
func (c *WSClient) Connected() bool { return c.connected.Load() }

// Connect establishes the WebSocket connection and starts the read loop.
func (c *WSClient) Connect(ctx context.Context) error {
	if c.addrValidator != nil {
		if err := c.addrValidator(c.url); err != nil {
			return err
		}
	}

	dialer := &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.DialContext(ctx, c.url, c.headers)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}

	c.conn = conn

	// Reset read deadline when we receive pings from the server.
	conn.SetPingHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(readDeadline))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readDeadline))
	})
	if err := conn.SetReadDeadline(time.Now().Add(readDeadline)); err != nil {
		_ = conn.Close()
		return fmt.Errorf("set read deadline: %w", err)
	}

	c.connected.Store(true)
	go c.readLoop()

	return nil
}

// Call sends a JSON-RPC 2.0 request and waits for the response.
func (c *WSClient) Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
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

// Notifications returns the channel of server-push notifications.
func (c *WSClient) Notifications() <-chan Notification {
	return c.notifyCh
}

// Close terminates the connection. Safe to call multiple times.
func (c *WSClient) Close() error {
	c.closeOnce.Do(func() {
		c.connected.Store(false)
		close(c.done)
		if c.conn != nil {
			_ = c.conn.Close()
		}

		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
	})
	return nil
}

// readLoop reads frames from the WebSocket, routing responses to pending
// callers and notifications to notifyCh.
func (c *WSClient) readLoop() {
	defer func() {
		c.closeOnce.Do(func() {
			c.connected.Store(false)
			close(c.done)
			_ = c.conn.Close()

			c.mu.Lock()
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
		})
		// Close the notify channel so consumers ranging Notifications()
		// (Bridge.runInbound) unblock and observe the conn death — without
		// this they block forever on a never-closed channel, so the bridge
		// never tears down or reconnects after a peer daemon restarts (the
		// zombie-bridge P0). Closed HERE, not inside Close()/closeOnce,
		// because readLoop is the SOLE sender on notifyCh and runs exactly
		// once: closing in this defer (after the read loop has exited) cannot
		// race a send, whereas closing from Close() on another goroutine could
		// (send-on-closed-channel panic). Close() instead tears down c.conn,
		// which makes ReadMessage error and ends this loop, reaching this defer.
		close(c.notifyCh)
	}()

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		var frame rpcResponse
		if err := json.Unmarshal(msg, &frame); err != nil {
			continue
		}

		if frame.ID != nil {
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

		if frame.Method != "" {
			notif := Notification{
				Method: frame.Method,
				Params: frame.Params,
			}
			select {
			case c.notifyCh <- notif:
			default:
			}
		}
	}
}
