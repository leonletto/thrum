package websocket

import (
	"encoding/json"
	"fmt"
	"sync"
)

// ClientRegistry tracks connected WebSocket clients by session ID.
type ClientRegistry struct {
	mu      sync.RWMutex
	clients map[string]*Connection
}

// NewClientRegistry creates a new client registry.
func NewClientRegistry() *ClientRegistry {
	return &ClientRegistry{
		clients: make(map[string]*Connection),
	}
}

// Register adds a client to the registry.
func (r *ClientRegistry) Register(sessionID string, conn *Connection) {
	r.mu.Lock()
	defer r.mu.Unlock()

	conn.sessionID = sessionID
	r.clients[sessionID] = conn
}

// Unregister removes a client from the registry.
func (r *ClientRegistry) Unregister(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.clients, sessionID)
}

// Get retrieves a client connection by session ID.
func (r *ClientRegistry) Get(sessionID string) (*Connection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	conn, ok := r.clients[sessionID]
	return conn, ok
}

// Count returns the number of connected clients.
func (r *ClientRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.clients)
}

// CloseAll closes all client connections.
func (r *ClientRegistry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, conn := range r.clients {
		_ = conn.Close()
	}

	// Clear the map
	r.clients = make(map[string]*Connection)
}

// Notify sends a notification to a specific session's client.
func (r *ClientRegistry) Notify(sessionID string, notification any) error {
	r.mu.RLock()
	conn, exists := r.clients[sessionID]
	r.mu.RUnlock()

	if !exists {
		// Client not connected - this is fine, they'll see it in their inbox
		return nil
	}

	// Send JSON-RPC notification (no id field, no response expected).
	// Extract inner params to avoid double-wrapping when notification
	// is already a {method, params} map from buildNotification().
	var params any = notification
	if m, ok := notification.(map[string]any); ok {
		if inner, ok := m["params"]; ok {
			params = inner
		}
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  getNotificationMethod(notification),
		"params":  params,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	if err := conn.Send(data); err != nil {
		// Client disconnected or buffer full - unregister them
		r.Unregister(sessionID)
		return fmt.Errorf("send notification: %w", err)
	}

	return nil
}

// getNotificationMethod extracts the notification method from the notification payload.
func getNotificationMethod(notification any) string {
	// Try to extract method from notification if it's a map
	if m, ok := notification.(map[string]any); ok {
		if method, ok := m["method"].(string); ok {
			return method
		}
	}

	// Default to generic notification method
	return "notification"
}
