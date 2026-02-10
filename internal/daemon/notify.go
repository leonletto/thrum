package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"

	"github.com/leonletto/thrum/internal/types"
)

// ClientRegistry tracks connected clients by session ID.
type ClientRegistry struct {
	mu      sync.RWMutex
	clients map[string]*ConnectedClient
}

// ConnectedClient represents a connected client with their session.
type ConnectedClient struct {
	sessionID string
	conn      net.Conn
}

// NewClientRegistry creates a new client registry.
func NewClientRegistry() *ClientRegistry {
	return &ClientRegistry{
		clients: make(map[string]*ConnectedClient),
	}
}

// Register adds a client to the registry.
func (r *ClientRegistry) Register(sessionID string, conn net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.clients[sessionID] = &ConnectedClient{
		sessionID: sessionID,
		conn:      conn,
	}
}

// Unregister removes a client from the registry.
func (r *ClientRegistry) Unregister(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.clients, sessionID)
}

// Notify sends a notification to a specific session's client.
func (r *ClientRegistry) Notify(sessionID string, notification *Notification) error {
	r.mu.RLock()
	client, exists := r.clients[sessionID]
	r.mu.RUnlock()

	if !exists {
		// Client not connected - this is fine, they'll see it in their inbox
		return nil
	}

	// Send JSON-RPC notification (no id field, no response expected)
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  notification.Method,
		"params":  notification.Params,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	// Append newline for framing
	data = append(data, '\n')

	_, err = client.conn.Write(data)
	if err != nil {
		// Client disconnected - unregister them
		r.Unregister(sessionID)
		return fmt.Errorf("write notification: %w", err)
	}

	return nil
}

// Notification is the push payload sent to clients.
type Notification struct {
	Method string       `json:"method"` // "notification.message"
	Params NotifyParams `json:"params"`
}

// NotifyParams contains the notification parameters.
type NotifyParams struct {
	MessageID           string        `json:"message_id"`
	ThreadID            string        `json:"thread_id,omitempty"`
	Author              AuthorInfo    `json:"author"`
	Preview             string        `json:"preview"` // First 100 chars of content
	Scopes              []types.Scope `json:"scopes"`
	MatchedSubscription MatchInfo     `json:"matched_subscription"`
	Timestamp           string        `json:"timestamp"`
}

// AuthorInfo contains information about the message author.
type AuthorInfo struct {
	AgentID string `json:"agent_id"`
	Role    string `json:"role"`
	Module  string `json:"module"`
}

// MatchInfo contains information about which subscription matched.
type MatchInfo struct {
	SubscriptionID int    `json:"subscription_id"`
	MatchType      string `json:"match_type"` // "scope", "mention", "all"
}
