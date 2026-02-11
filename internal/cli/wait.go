package cli

import (
	"fmt"
	"strings"
	"time"
)

// WaitOptions contains options for waiting for messages.
type WaitOptions struct {
	Timeout       time.Duration
	Scope         string    // Format: "type:value"
	Mention       string    // Format: "@role"
	All           bool      // Subscribe to all messages (broadcasts + directed)
	After         time.Time // Only return messages created after this time (zero = no filter)
	CallerAgentID string    // Caller's resolved agent ID (for worktree identity)
}

// Wait blocks until a matching message arrives or timeout occurs.
func Wait(client *Client, opts WaitOptions) (*Message, error) {
	// Parse scope if provided
	var scope map[string]string
	if opts.Scope != "" {
		parts := strings.SplitN(opts.Scope, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("scope must be in 'type:value' format, got: %s", opts.Scope)
		}
		scope = map[string]string{
			"type":  parts[0],
			"value": parts[1],
		}
	}

	// Build subscribe params
	subscribeParams := map[string]any{}
	if scope != nil {
		subscribeParams["scope"] = scope
	}
	if opts.Mention != "" {
		subscribeParams["mention_role"] = strings.TrimPrefix(opts.Mention, "@")
	}
	if opts.CallerAgentID != "" {
		subscribeParams["caller_agent_id"] = opts.CallerAgentID
	}
	if opts.All {
		subscribeParams["all"] = true
	}

	// Subscribe to notifications
	var subscribeResult struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if err := client.Call("subscribe", subscribeParams, &subscribeResult); err != nil {
		return nil, fmt.Errorf("failed to subscribe: %w", err)
	}

	// Unsubscribe when done
	defer func() {
		unsubParams := map[string]any{
			"subscription_id": subscribeResult.SubscriptionID,
		}
		if opts.CallerAgentID != "" {
			unsubParams["caller_agent_id"] = opts.CallerAgentID
		}
		_ = client.Call("unsubscribe", unsubParams, nil)
	}()

	// Start timeout timer
	timeout := time.After(opts.Timeout)
	pollTicker := time.NewTicker(500 * time.Millisecond)
	defer pollTicker.Stop()

	// Poll for new messages
	for {
		select {
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for message")

		case <-pollTicker.C:
			// Check for new messages
			var inbox InboxResult
			listParams := map[string]any{
				"page_size": 1,
				"unread":    true,
			}
			if scope != nil {
				listParams["scope"] = scope
			}
			if opts.CallerAgentID != "" {
				listParams["caller_agent_id"] = opts.CallerAgentID
			}

			if err := client.Call("message.list", listParams, &inbox); err != nil {
				continue // Ignore errors and keep waiting
			}

			if len(inbox.Messages) > 0 {
				msg := &inbox.Messages[0]
				// Apply --after filter: skip messages created before threshold
				if !opts.After.IsZero() && msg.CreatedAt != "" {
					createdAt, parseErr := time.Parse(time.RFC3339Nano, msg.CreatedAt)
					if parseErr == nil && !createdAt.After(opts.After) {
						continue // Message is too old, keep waiting
					}
				}
				return msg, nil
			}
		}
	}
}
