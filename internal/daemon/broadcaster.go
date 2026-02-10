package daemon

import (
	"sync"
)

// ClientNotifier is the interface expected by the subscriptions dispatcher.
type ClientNotifier interface {
	Notify(sessionID string, notification any) error
}

// Broadcaster manages event broadcasting to all connected clients (Unix socket + WebSocket).
// It implements the ClientNotifier interface expected by subscriptions.Dispatcher.
type Broadcaster struct {
	unixClients *ClientRegistry  // Unix socket clients
	wsClients   WSClientNotifier // WebSocket clients
	mu          sync.RWMutex
}

// WSClientNotifier is an interface for WebSocket client registry.
type WSClientNotifier interface {
	Notify(sessionID string, notification any) error
}

// NewBroadcaster creates a new broadcaster with both Unix and WebSocket client registries.
func NewBroadcaster(unixClients *ClientRegistry, wsClients WSClientNotifier) *Broadcaster {
	return &Broadcaster{
		unixClients: unixClients,
		wsClients:   wsClients,
	}
}

// Notify sends a notification to a client on either Unix socket or WebSocket.
// It tries both transports since we don't know which one the client is using.
func (b *Broadcaster) Notify(sessionID string, notification any) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var lastErr error

	// Try Unix socket clients first
	if b.unixClients != nil {
		if notif, ok := notification.(map[string]any); ok {
			// Convert to the Notification type expected by Unix socket registry
			unixNotif := &Notification{
				Method: getNotificationMethod(notif),
				Params: convertToNotifyParams(notif),
			}
			if err := b.unixClients.Notify(sessionID, unixNotif); err != nil {
				lastErr = err
			} else {
				// Successfully sent via Unix socket
				return nil
			}
		}
	}

	// Try WebSocket clients
	if b.wsClients != nil {
		if err := b.wsClients.Notify(sessionID, notification); err != nil {
			lastErr = err
		} else {
			// Successfully sent via WebSocket
			return nil
		}
	}

	// If both failed or returned nil (client not connected), return last error
	// Returning nil here is fine - it means the client isn't connected, which is normal
	return lastErr
}

// getNotificationMethod extracts the method name from the notification payload.
func getNotificationMethod(notification map[string]any) string {
	if method, ok := notification["method"].(string); ok {
		return method
	}
	return "notification"
}

// convertToNotifyParams converts the subscription notification to Unix socket NotifyParams format.
func convertToNotifyParams(notification map[string]any) NotifyParams {
	params, ok := notification["params"].(map[string]any)
	if !ok {
		return NotifyParams{}
	}

	result := NotifyParams{}

	// Extract fields with type assertions
	if msgID, ok := params["message_id"].(string); ok {
		result.MessageID = msgID
	}
	if threadID, ok := params["thread_id"].(string); ok {
		result.ThreadID = threadID
	}
	if preview, ok := params["preview"].(string); ok {
		result.Preview = preview
	}
	if timestamp, ok := params["timestamp"].(string); ok {
		result.Timestamp = timestamp
	}

	// Extract author info
	if author, ok := params["author"].(map[string]any); ok {
		if agentID, ok := author["agent_id"].(string); ok {
			result.Author.AgentID = agentID
		}
		if role, ok := author["role"].(string); ok {
			result.Author.Role = role
		}
		if module, ok := author["module"].(string); ok {
			result.Author.Module = module
		}
	}

	// Extract matched subscription info
	if matched, ok := params["matched_subscription"].(map[string]any); ok {
		if subID, ok := matched["subscription_id"].(int); ok {
			result.MatchedSubscription.SubscriptionID = subID
		}
		if matchType, ok := matched["match_type"].(string); ok {
			result.MatchedSubscription.MatchType = matchType
		}
	}

	// TODO: Convert scopes array if needed
	// For now, we'll leave it empty as it requires more complex conversion

	return result
}
