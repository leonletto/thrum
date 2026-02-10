package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/daemon/rpc"
)

const (
	maxQueueSize       = 1000
	defaultWaitTimeout = 300 // seconds
	maxWaitTimeout     = 600 // seconds
)

// MessageNotification represents a notification received via WebSocket.
type MessageNotification struct {
	MessageID string `json:"message_id"`
	Preview   string `json:"preview"`
	AgentID   string `json:"agent_id"`
	Timestamp string `json:"timestamp"`
}

// Waiter manages the WebSocket connection to the daemon for real-time
// message notifications. It powers the wait_for_message MCP tool.
type Waiter struct {
	wsConn     *websocket.Conn
	socketPath string // for per-call RPC clients
	agentRole  string
	nextID     atomic.Int64 // incrementing JSON-RPC request ID

	queue    []MessageNotification
	mu       sync.Mutex
	waiterCh chan struct{} // closed when notification arrives
	active   bool          // whether a wait is currently active

	ctx    context.Context
	cancel context.CancelFunc
}

// NewWaiter creates a Waiter that connects to the daemon WebSocket.
func NewWaiter(ctx context.Context, socketPath, agentRole, wsURL string) (*Waiter, error) {
	wCtx, cancel := context.WithCancel(ctx)

	u, err := url.Parse(wsURL)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("parse WebSocket URL: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.DialContext(wCtx, u.String(), nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("connect to daemon WebSocket at %s: %w", wsURL, err)
	}

	w := &Waiter{
		wsConn:     conn,
		socketPath: socketPath,
		agentRole:  agentRole,
		queue:      make([]MessageNotification, 0),
		ctx:        wCtx,
		cancel:     cancel,
	}

	// Register, identify, and subscribe via WebSocket RPC
	if err := w.setup(); err != nil {
		_ = conn.Close()
		cancel()
		return nil, fmt.Errorf("WebSocket setup: %w", err)
	}

	// Start read loop
	go w.readLoop()

	return w, nil
}

// setup sends user.register, user.identify, and subscribe RPCs over WebSocket.
func (w *Waiter) setup() error {
	// 1. user.identify (gets git user info)
	identResp, err := w.wsRPC("user.identify", nil)
	if err != nil {
		return fmt.Errorf("user.identify: %w", err)
	}

	var identResult struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(identResp, &identResult); err != nil {
		return fmt.Errorf("parse identify response: %w", err)
	}

	// 2. user.register with the username
	_, err = w.wsRPC("user.register", map[string]string{
		"username": identResult.Username,
	})
	if err != nil {
		return fmt.Errorf("user.register: %w", err)
	}

	// 3. subscribe to mentions for this agent's role
	mentionRole := w.agentRole
	_, err = w.wsRPC("subscribe", map[string]any{
		"mention_role": mentionRole,
	})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	return nil
}

// wsRPC sends a JSON-RPC request over WebSocket and reads the response.
func (w *Waiter) wsRPC(method string, params any) (json.RawMessage, error) {
	id := w.nextID.Add(1)
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}

	if err := w.wsConn.WriteJSON(req); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	// Read response (may need to skip notifications)
	for {
		_, msg, err := w.wsConn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}

		var resp struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      any             `json:"id,omitempty"`
			Method  string          `json:"method,omitempty"`
			Result  json.RawMessage `json:"result,omitempty"`
			Error   *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue // skip unparseable messages
		}

		// If it's a notification (no id), skip it during setup
		if resp.ID == nil && resp.Method != "" {
			continue
		}

		if resp.Error != nil {
			return nil, fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
		}

		return resp.Result, nil
	}
}

// readLoop reads WebSocket messages and routes notifications to the queue.
func (w *Waiter) readLoop() {
	defer func() {
		// Unblock any active waiter on connection loss
		w.mu.Lock()
		if w.waiterCh != nil {
			close(w.waiterCh)
			w.waiterCh = nil
		}
		w.mu.Unlock()
	}()

	for {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		_, msg, err := w.wsConn.ReadMessage()
		if err != nil {
			return // connection closed — defer will unblock waiters
		}

		// Parse as JSON-RPC notification
		var notif struct {
			Method string          `json:"method,omitempty"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal(msg, &notif); err != nil {
			continue
		}

		if notif.Method != "notification.message" {
			continue
		}

		// Parse notification params
		var n MessageNotification
		if err := json.Unmarshal(notif.Params, &n); err != nil {
			continue
		}

		w.mu.Lock()
		// Cap queue size
		if len(w.queue) >= maxQueueSize {
			w.queue = w.queue[1:] // drop oldest
		}
		w.queue = append(w.queue, n)

		// Wake waiting goroutine
		if w.waiterCh != nil {
			close(w.waiterCh)
			w.waiterCh = nil
		}
		w.mu.Unlock()
	}
}

// WaitForMessage blocks until a message arrives or the timeout expires.
func (w *Waiter) WaitForMessage(ctx context.Context, timeout int, priorityFilter string) (*WaitForMessageOutput, error) {
	// Validate timeout
	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}
	if timeout > maxWaitTimeout {
		timeout = maxWaitTimeout
	}

	// TODO: priorityFilter is accepted but not yet implemented.
	// All messages pass through regardless of priority.
	// Implement when priority-based notification filtering is added to the daemon.
	_ = priorityFilter

	// Enforce single-waiter
	w.mu.Lock()
	if w.active {
		w.mu.Unlock()
		return nil, fmt.Errorf("another wait_for_message is already active; only one waiter per agent")
	}
	w.active = true

	// Check queue first
	if len(w.queue) > 0 {
		n := w.queue[0]
		w.queue = w.queue[1:]
		w.active = false
		w.mu.Unlock()

		msg, err := w.fetchAndMark(n.MessageID)
		if err != nil {
			return &WaitForMessageOutput{
				Status:        "message_received",
				Message:       &MessageInfo{MessageID: n.MessageID, Content: n.Preview, Timestamp: n.Timestamp},
				WaitedSeconds: 0,
			}, nil
		}
		return &WaitForMessageOutput{
			Status:        "message_received",
			Message:       msg,
			WaitedSeconds: 0,
		}, nil
	}

	// Set up waiter channel
	ch := make(chan struct{})
	w.waiterCh = ch
	w.mu.Unlock()

	startTime := time.Now()

	// Block until message, timeout, or context cancellation
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()

	select {
	case <-ch:
		w.mu.Lock()
		w.active = false
		if len(w.queue) == 0 {
			w.mu.Unlock()
			return &WaitForMessageOutput{
				Status:        "timeout",
				WaitedSeconds: int(time.Since(startTime).Seconds()),
			}, nil
		}
		n := w.queue[0]
		w.queue = w.queue[1:]
		w.mu.Unlock()

		msg, err := w.fetchAndMark(n.MessageID)
		if err != nil {
			msg = &MessageInfo{MessageID: n.MessageID, Content: n.Preview, Timestamp: n.Timestamp}
		}
		return &WaitForMessageOutput{
			Status:        "message_received",
			Message:       msg,
			WaitedSeconds: int(time.Since(startTime).Seconds()),
		}, nil

	case <-timer.C:
		w.mu.Lock()
		w.waiterCh = nil
		w.active = false
		w.mu.Unlock()
		return &WaitForMessageOutput{
			Status:        "timeout",
			WaitedSeconds: timeout,
		}, nil

	case <-ctx.Done():
		w.mu.Lock()
		w.waiterCh = nil
		w.active = false
		w.mu.Unlock()
		return nil, ctx.Err()
	}
}

// fetchAndMark fetches the full message via RPC and marks it as read.
func (w *Waiter) fetchAndMark(messageID string) (*MessageInfo, error) {
	client, err := cli.NewClient(w.socketPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	var getResp rpc.GetMessageResponse
	if err := client.Call("message.get", rpc.GetMessageRequest{MessageID: messageID}, &getResp); err != nil {
		return nil, err
	}

	msg := &MessageInfo{
		MessageID: getResp.Message.MessageID,
		From:      getResp.Message.Author.AgentID,
		Content:   getResp.Message.Body.Content,
		ThreadID:  getResp.Message.ThreadID,
		Timestamp: getResp.Message.CreatedAt,
	}

	// Mark as read (best-effort, new client)
	markClient, err := cli.NewClient(w.socketPath)
	if err == nil {
		_ = markClient.Call("message.markRead", rpc.MarkReadRequest{MessageIDs: []string{messageID}}, nil)
		_ = markClient.Close()
	}

	return msg, nil
}

// Close shuts down the Waiter.
func (w *Waiter) Close() error {
	w.cancel()
	if w.wsConn != nil {
		return w.wsConn.Close()
	}
	return nil
}
