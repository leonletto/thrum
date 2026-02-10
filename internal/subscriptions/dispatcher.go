package subscriptions

import (
	"database/sql"
	"fmt"

	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/types"
)

// ClientNotifier is an interface for sending notifications to clients.
type ClientNotifier interface {
	Notify(sessionID string, notification any) error
}

// Dispatcher manages notification dispatch for new messages.
type Dispatcher struct {
	db      *sql.DB
	clients ClientNotifier
}

// NewDispatcher creates a new notification dispatcher.
func NewDispatcher(db *sql.DB) *Dispatcher {
	return &Dispatcher{
		db:      db,
		clients: nil, // Can be set later with SetClientNotifier
	}
}

// SetClientNotifier sets the client notifier for pushing notifications.
func (d *Dispatcher) SetClientNotifier(notifier ClientNotifier) {
	d.clients = notifier
}

// MessageInfo represents the information needed to match against subscriptions.
type MessageInfo struct {
	MessageID string
	ThreadID  string
	AgentID   string
	SessionID string
	Scopes    []types.Scope
	Refs      []types.Ref
	Timestamp string
	Preview   string // First 100 chars of message content
}

// SubscriptionMatch represents a subscription that matched a message.
type SubscriptionMatch struct {
	SubscriptionID int
	SessionID      string
	MatchType      string // "scope", "mention", "all"
}

// DispatchForMessage finds all subscriptions that match a message and pushes notifications.
// Returns a list of sessions that were notified.
func (d *Dispatcher) DispatchForMessage(msg *MessageInfo) ([]SubscriptionMatch, error) {
	// Query all active subscriptions with agent info for mention matching
	query := `SELECT s.id, s.session_id, s.scope_type, s.scope_value, s.mention_role,
	                 a.agent_id, a.role
	          FROM subscriptions s
	          LEFT JOIN sessions sess ON s.session_id = sess.session_id
	          LEFT JOIN agents a ON sess.agent_id = a.agent_id`

	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query subscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var matches []SubscriptionMatch

	for rows.Next() {
		var id int
		var sessionID string
		var scopeType, scopeValue, mentionRole sql.NullString
		var agentID, agentRole sql.NullString

		err := rows.Scan(&id, &sessionID, &scopeType, &scopeValue, &mentionRole, &agentID, &agentRole)
		if err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}

		// Check if this subscription matches the message
		matchType := matchSubscription(msg, scopeType, scopeValue, mentionRole, agentID, agentRole)
		if matchType != "" {
			match := SubscriptionMatch{
				SubscriptionID: id,
				SessionID:      sessionID,
				MatchType:      matchType,
			}
			matches = append(matches, match)

			// Push notification if client notifier is available
			if d.clients != nil {
				notification := d.buildNotification(msg, match)
				// Ignore errors - client may not be connected
				_ = d.clients.Notify(sessionID, notification)
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscriptions: %w", err)
	}

	return matches, nil
}

// buildNotification creates a notification payload for a matched subscription.
func (d *Dispatcher) buildNotification(msg *MessageInfo, match SubscriptionMatch) any {
	// Extract preview (first 100 chars)
	preview := msg.Preview
	if len(preview) > 100 {
		preview = preview[:100] + "..."
	}

	// Parse author from AgentID
	// For named agents: AgentID is just the name (e.g., "furiosa")
	// For legacy/unnamed agents: AgentID has format "agent:role:hash" or "role_hash"
	// Note: Module is NOT in the agent ID - must be looked up from database if needed
	authorRole, _ := identity.ParseAgentID(msg.AgentID)
	authorName := msg.AgentID

	// Module extraction would require database lookup - not included in agent ID
	// For notification purposes, role and name are sufficient
	authorModule := ""

	author := struct {
		AgentID string `json:"agent_id"`
		Name    string `json:"name,omitempty"`
		Role    string `json:"role,omitempty"`
		Module  string `json:"module,omitempty"`
	}{
		AgentID: msg.AgentID,
		Name:    authorName,
		Role:    authorRole,
		Module:  authorModule,
	}

	return map[string]any{
		"method": "notification.message",
		"params": map[string]any{
			"message_id": msg.MessageID,
			"thread_id":  msg.ThreadID,
			"author":     author,
			"preview":    preview,
			"scopes":     msg.Scopes,
			"matched_subscription": map[string]any{
				"subscription_id": match.SubscriptionID,
				"match_type":      match.MatchType,
			},
			"timestamp": msg.Timestamp,
		},
	}
}

// matchSubscription checks if a message matches a subscription.
// Returns the match type ("scope", "mention", "all") or empty string if no match.
// Supports both role-based mentions (@reviewer) and name-based mentions (@furiosa).
func matchSubscription(msg *MessageInfo, scopeType, scopeValue, mentionRole, agentID, agentRole sql.NullString) string {
	// All subscription (all fields NULL) - always matches
	if !scopeType.Valid && !scopeValue.Valid && !mentionRole.Valid {
		return "all"
	}

	// Scope subscription - check if message has matching scope
	if scopeType.Valid && scopeValue.Valid {
		for _, scope := range msg.Scopes {
			if scope.Type == scopeType.String && scope.Value == scopeValue.String {
				return "scope"
			}
		}
	}

	// Mention subscription - check if message has mention ref that matches
	// either the agent's role OR the agent's ID/name
	if mentionRole.Valid {
		for _, ref := range msg.Refs {
			if ref.Type == "mention" {
				// Match if mention value equals the subscription's mention_role
				if ref.Value == mentionRole.String {
					return "mention"
				}
				// Also match if mention value equals the agent's role (for role-based mentions)
				if agentRole.Valid && ref.Value == agentRole.String {
					return "mention"
				}
				// Also match if mention value equals the agent's ID/name (for name-based mentions)
				if agentID.Valid && ref.Value == agentID.String {
					return "mention"
				}
			}
		}
	}

	return ""
}

// ThreadUpdateInfo represents the information for a thread update notification.
type ThreadUpdateInfo struct {
	ThreadID     string
	MessageCount int
	UnreadCount  int
	LastActivity string
	LastSender   string
	Preview      *string
	Timestamp    string
}

// DispatchThreadUpdated sends thread.updated notifications to subscribed sessions.
// This is a real-time notification (not persisted to JSONL).
func (d *Dispatcher) DispatchThreadUpdated(info *ThreadUpdateInfo) error {
	if d.clients == nil {
		// No client notifier configured, skip
		return nil
	}

	// Query all active subscriptions
	query := `SELECT DISTINCT session_id FROM subscriptions`
	rows, err := d.db.Query(query)
	if err != nil {
		return fmt.Errorf("query subscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	notification := map[string]any{
		"method": "notification.thread.updated",
		"params": map[string]any{
			"thread_id":     info.ThreadID,
			"message_count": info.MessageCount,
			"unread_count":  info.UnreadCount,
			"last_activity": info.LastActivity,
			"last_sender":   info.LastSender,
			"preview":       info.Preview,
			"timestamp":     info.Timestamp,
		},
	}

	// Send to all subscribed sessions
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			continue // Skip errors, best-effort delivery
		}

		// Ignore errors - client may not be connected
		_ = d.clients.Notify(sessionID, notification)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate subscriptions: %w", err)
	}

	return nil
}
