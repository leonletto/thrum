package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/types"
)

// SubscribeRequest represents the request for subscriptions.subscribe RPC.
type SubscribeRequest struct {
	Scope         *types.Scope `json:"scope,omitempty"`
	MentionRole   *string      `json:"mention_role,omitempty"`
	All           bool         `json:"all,omitempty"`
	CallerAgentID string       `json:"caller_agent_id,omitempty"`
}

// SubscribeResponse represents the response from subscriptions.subscribe RPC.
type SubscribeResponse struct {
	SubscriptionID int    `json:"subscription_id"`
	SessionID      string `json:"session_id"`
	CreatedAt      string `json:"created_at"`
}

// UnsubscribeRequest represents the request for subscriptions.unsubscribe RPC.
type UnsubscribeRequest struct {
	SubscriptionID int    `json:"subscription_id"`
	CallerAgentID  string `json:"caller_agent_id,omitempty"`
}

// UnsubscribeResponse represents the response from subscriptions.unsubscribe RPC.
type UnsubscribeResponse struct {
	Removed bool `json:"removed"`
}

// ListSubscriptionsRequest represents the request for subscriptions.list RPC.
type ListSubscriptionsRequest struct {
	CallerAgentID string `json:"caller_agent_id,omitempty"`
}

// ListSubscriptionsResponse represents the response from subscriptions.list RPC.
type ListSubscriptionsResponse struct {
	Subscriptions []SubscriptionInfo `json:"subscriptions"`
}

// SubscriptionInfo represents information about a subscription.
type SubscriptionInfo struct {
	ID          int    `json:"id"`
	ScopeType   string `json:"scope_type,omitempty"`
	ScopeValue  string `json:"scope_value,omitempty"`
	MentionRole string `json:"mention_role,omitempty"`
	All         bool   `json:"all,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// SubscribeOptions contains options for subscribing.
type SubscribeOptions struct {
	Scope         *types.Scope
	MentionRole   *string
	All           bool
	CallerAgentID string
}

// Subscribe creates a new subscription.
func Subscribe(client *Client, opts SubscribeOptions) (*SubscribeResponse, error) {
	req := SubscribeRequest(opts)

	var result SubscribeResponse
	if err := client.Call("subscribe", req, &result); err != nil {
		return nil, fmt.Errorf("subscribe RPC failed: %w", err)
	}

	return &result, nil
}

// Unsubscribe removes a subscription.
func Unsubscribe(client *Client, subscriptionID int) (*UnsubscribeResponse, error) {
	req := UnsubscribeRequest{
		SubscriptionID: subscriptionID,
	}

	var result UnsubscribeResponse
	if err := client.Call("unsubscribe", req, &result); err != nil {
		return nil, fmt.Errorf("unsubscribe RPC failed: %w", err)
	}

	return &result, nil
}

// ListSubscriptions retrieves all subscriptions for the current session.
func ListSubscriptions(client *Client, callerAgentID string) (*ListSubscriptionsResponse, error) {
	req := ListSubscriptionsRequest{
		CallerAgentID: callerAgentID,
	}

	var result ListSubscriptionsResponse
	if err := client.Call("subscriptions.list", req, &result); err != nil {
		return nil, fmt.Errorf("subscriptions.list RPC failed: %w", err)
	}

	return &result, nil
}

// FormatSubscribe formats the subscribe response for display.
func FormatSubscribe(result *SubscribeResponse) string {
	output := fmt.Sprintf("✓ Subscription created: #%d\n", result.SubscriptionID)
	output += fmt.Sprintf("  Session:    %s\n", result.SessionID)

	// Format created time
	if result.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, result.CreatedAt); err == nil {
			output += fmt.Sprintf("  Created:    %s\n", t.Format("2006-01-02 15:04:05"))
		}
	}

	return output
}

// FormatUnsubscribe formats the unsubscribe response for display.
func FormatUnsubscribe(subscriptionID int, result *UnsubscribeResponse) string {
	if result.Removed {
		return fmt.Sprintf("✓ Subscription #%d removed\n", subscriptionID)
	}
	return fmt.Sprintf("✗ Failed to remove subscription #%d\n", subscriptionID)
}

// FormatSubscriptionsList formats the subscriptions list response for display.
func FormatSubscriptionsList(result *ListSubscriptionsResponse) string {
	if len(result.Subscriptions) == 0 {
		return "No active subscriptions.\n" + Hint("subscriptions.empty", false, false)
	}

	var output strings.Builder

	fmt.Fprintf(&output, "Active subscriptions (%d):\n\n", len(result.Subscriptions))

	for _, sub := range result.Subscriptions {
		fmt.Fprintf(&output, "┌─ Subscription #%d\n", sub.ID)

		// Determine subscription type
		if sub.All {
			output.WriteString("│  Type:       All messages (firehose)\n")
		} else if sub.MentionRole != "" {
			fmt.Fprintf(&output, "│  Type:       Mention (@%s)\n", sub.MentionRole)
		} else if sub.ScopeType != "" {
			fmt.Fprintf(&output, "│  Type:       Scope (%s:%s)\n", sub.ScopeType, sub.ScopeValue)
		}

		// Created time
		if sub.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339, sub.CreatedAt); err == nil {
				duration := time.Since(t)
				fmt.Fprintf(&output, "│  Created:    %s (%s ago)\n",
					t.Format("2006-01-02 15:04:05"), formatDuration(duration))
			}
		}

		output.WriteString("└─\n\n")
	}

	return output.String()
}
