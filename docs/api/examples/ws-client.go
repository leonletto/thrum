// Thrum WebSocket Client Example (Go)
//
// This example demonstrates:
// - User registration
// - Sending messages
// - Event subscriptions
// - Event handling
//
// Usage:
//   go run ws-client.go

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// JSON-RPC types

type JSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
	ID      int    `json:"id"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	ID      int             `json:"id"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type JSONRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// Event types

type MessageCreateEvent struct {
	Type      string `json:"type"`
	MessageID string `json:"message_id"`
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	Timestamp string `json:"timestamp"`
	Body      struct {
		Format  string `json:"format"`
		Content string `json:"content"`
	} `json:"body"`
	Scopes []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"scopes"`
	Refs []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"refs"`
}

// Client

type ThrumClient struct {
	conn      *websocket.Conn
	nextID    int
	pending   map[int]chan json.RawMessage
	mu        sync.Mutex
	userID    string
	sessionID string
}

func NewThrumClient(url string) (*ThrumClient, error) {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	client := &ThrumClient{
		conn:    conn,
		nextID:  1,
		pending: make(map[int]chan json.RawMessage),
	}

	// Start message handler
	go client.handleMessages()

	log.Println("✓ Connected to Thrum daemon")
	return client, nil
}

func (c *ThrumClient) handleMessages() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			log.Printf("Read error: %v", err)
			return
		}

		// Try to parse as response (has id field)
		var resp JSONRPCResponse
		if err := json.Unmarshal(data, &resp); err == nil && resp.ID != 0 {
			c.mu.Lock()
			ch, ok := c.pending[resp.ID]
			if ok {
				delete(c.pending, resp.ID)
				c.mu.Unlock()

				if resp.Error != nil {
					log.Printf("RPC error: %s (code: %d)", resp.Error.Message, resp.Error.Code)
					close(ch)
				} else {
					ch <- resp.Result
					close(ch)
				}
			} else {
				c.mu.Unlock()
			}
			continue
		}

		// Try to parse as notification (no id field)
		var notif JSONRPCNotification
		if err := json.Unmarshal(data, &notif); err == nil && notif.Method != "" {
			c.handleEvent(notif.Method, notif.Params)
			continue
		}

		log.Printf("Unknown message: %s", string(data))
	}
}

func (c *ThrumClient) handleEvent(method string, params json.RawMessage) {
	switch method {
	case "message.created":
		var event MessageCreateEvent
		if err := json.Unmarshal(params, &event); err != nil {
			log.Printf("Failed to unmarshal message.created: %v", err)
			return
		}
		c.onMessageCreated(&event)

	case "message.edited":
		log.Printf("Message edited")

	case "message.deleted":
		log.Printf("Message deleted")

	default:
		log.Printf("Unknown event: %s", method)
	}
}

func (c *ThrumClient) onMessageCreated(event *MessageCreateEvent) {
	fmt.Printf("\n📨 New message from %s:\n", event.AgentID)
	fmt.Printf("   %s\n", event.Body.Content)
	fmt.Printf("   ID: %s\n", event.MessageID)
}

func (c *ThrumClient) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	ch := make(chan json.RawMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      id,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	// Wait for response with timeout
	select {
	case result := <-ch:
		return result, nil
	case <-time.After(30 * time.Second):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("request timeout")
	}
}

func (c *ThrumClient) RegisterUser(username, displayName string) error {
	params := map[string]string{
		"username": username,
	}
	if displayName != "" {
		params["display_name"] = displayName
	}

	result, err := c.call("user.register", params)
	if err != nil {
		return err
	}

	var resp struct {
		UserID    string `json:"user_id"`
		SessionID string `json:"session_id"`
		Username  string `json:"username"`
	}

	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}

	c.userID = resp.UserID
	c.sessionID = resp.SessionID

	log.Printf("✓ Registered as: %s", resp.UserID)
	log.Printf("  Session: %s", resp.SessionID)

	return nil
}

func (c *ThrumClient) SendMessage(content string, options map[string]any) (string, error) {
	params := map[string]any{
		"content": content,
		"scopes":  []any{},
		"refs":    []any{},
	}

	for k, v := range options {
		params[k] = v
	}

	result, err := c.call("message.send", params)
	if err != nil {
		return "", err
	}

	var resp struct {
		MessageID string `json:"message_id"`
	}

	if err := json.Unmarshal(result, &resp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	log.Printf("✓ Message sent: %s", resp.MessageID)
	return resp.MessageID, nil
}

func (c *ThrumClient) Subscribe(filterType string, options map[string]any) (string, error) {
	params := map[string]any{
		"filter_type": filterType,
	}

	for k, v := range options {
		params[k] = v
	}

	result, err := c.call("subscribe.create", params)
	if err != nil {
		return "", err
	}

	var resp struct {
		SubscriptionID string `json:"subscription_id"`
	}

	if err := json.Unmarshal(result, &resp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	log.Printf("✓ Subscribed: %s", resp.SubscriptionID)
	return resp.SubscriptionID, nil
}

func (c *ThrumClient) ListMessages(pageSize, page int) ([]map[string]any, error) {
	params := map[string]any{
		"page_size": pageSize,
		"page":      page,
	}

	result, err := c.call("message.list", params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Messages   []map[string]any `json:"messages"`
		TotalCount int              `json:"total_count"`
	}

	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	log.Printf("\n📋 Recent messages (%d total)", resp.TotalCount)
	return resp.Messages, nil
}

func (c *ThrumClient) Close() error {
	return c.conn.Close()
}

func main() {
	// Create client
	client, err := NewThrumClient("ws://localhost:9999")
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer client.Close()

	// Wait for connection to stabilize
	time.Sleep(100 * time.Millisecond)

	// 1. Register as user
	if err := client.RegisterUser("alice", "Alice Smith"); err != nil {
		log.Fatalf("Registration failed: %v", err)
	}

	// 2. Subscribe to all events
	if _, err := client.Subscribe("all", nil); err != nil {
		log.Fatalf("Subscribe failed: %v", err)
	}

	// 3. Send a message
	_, err = client.SendMessage("Hello from the Go WebSocket client!", map[string]any{
		"scopes": []map[string]string{
			{"type": "task", "value": "demo"},
		},
	})
	if err != nil {
		log.Fatalf("Send message failed: %v", err)
	}

	// 4. List recent messages
	messages, err := client.ListMessages(5, 1)
	if err != nil {
		log.Fatalf("List messages failed: %v", err)
	}

	for _, msg := range messages {
		msgID := msg["message_id"].(string)
		body := msg["body"].(map[string]any)
		content := body["content"].(string)

		if len(content) > 50 {
			content = content[:50] + "..."
		}

		log.Printf("   %s: %s", msgID, content)
	}

	// Keep connection alive to receive events
	log.Println("\n👂 Listening for events... (press Ctrl+C to exit)")

	// Wait for interrupt signal
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt

	log.Println("\nShutting down...")
}
