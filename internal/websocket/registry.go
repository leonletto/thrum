package websocket

import (
	"encoding/json"
	"fmt"
	"sync"
)

// ClientRegistry tracks connected WebSocket clients by session ID.
// It also maintains an unkeyed set of all active connections so that
// BroadcastAll can reach passive observers (e.g. the browser UI) that
// connect but never call session.start / subscribe.
type ClientRegistry struct {
	mu          sync.RWMutex
	clients     map[string]*Connection // sessionID → connection (registered clients)
	connections map[*Connection]struct{} // all active connections, keyed by pointer
}

// NewClientRegistry creates a new client registry.
func NewClientRegistry() *ClientRegistry {
	return &ClientRegistry{
		clients:     make(map[string]*Connection),
		connections: make(map[*Connection]struct{}),
	}
}

// addConn adds a raw connection to the all-connections set.
// Called by the WebSocket server as soon as a new connection is accepted.
func (r *ClientRegistry) addConn(conn *Connection) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connections[conn] = struct{}{}
}

// removeConn removes a raw connection from the all-connections set.
// Called when the connection is closed.
func (r *ClientRegistry) removeConn(conn *Connection) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.connections, conn)
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
	r.clients = make(map[string]*Connection)

	// Also clear the all-connections set so stale pointers don't leak
	// across daemon restart cycles.
	for conn := range r.connections {
		_ = conn.Close()
	}
	r.connections = make(map[*Connection]struct{})
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

	return r.sendTo(conn, sessionID, notification)
}

// BroadcastAll sends a notification to every connected WebSocket client,
// regardless of whether they have registered a sessionID. This is used to
// push events (e.g. notification.message) to passive observers like the
// browser UI that connect but never call thrum subscribe.
func (r *ClientRegistry) BroadcastAll(notification any) {
	r.mu.RLock()
	// Snapshot connections so we don't hold the lock during Send.
	conns := make([]*Connection, 0, len(r.connections))
	for conn := range r.connections {
		conns = append(conns, conn)
	}
	r.mu.RUnlock()

	for _, conn := range conns {
		// Ignore errors — client may have disconnected between snapshot and send.
		_ = r.sendTo(conn, conn.sessionID, notification)
	}
}

// sendTo marshals the notification and writes it to conn.
// sessionID is only used for unregistering on send failure; it may be empty.
func (r *ClientRegistry) sendTo(conn *Connection, sessionID string, notification any) error {
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
		// Client disconnected or buffer full — unregister them if they had a session.
		if sessionID != "" {
			r.Unregister(sessionID)
		}
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
