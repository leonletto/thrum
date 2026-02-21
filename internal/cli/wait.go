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
	After         time.Time // Only return messages created after this time (zero = no filter)
	CallerAgentID string    // Caller's resolved agent ID (for worktree identity)
	ForAgent      string    // Filter to messages for this agent
	ForAgentRole  string    // Filter to messages for this agent's role
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

	// Start timeout timer
	timeout := time.After(opts.Timeout)
	pollTicker := time.NewTicker(500 * time.Millisecond)
	defer pollTicker.Stop()

	// Track seen message IDs to avoid returning duplicates
	seen := make(map[string]bool)

	// Poll for new messages
	for {
		select {
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for message")

		case <-pollTicker.C:
			// Check for new messages
			var inbox InboxResult
			listParams := map[string]any{
				"page_size":  10,
				"sort_by":    "created_at",
				"sort_order": "desc",
			}
			// Use server-side created_after filter when available
			// Always format in UTC to match database timestamp format
			if !opts.After.IsZero() {
				listParams["created_after"] = opts.After.UTC().Format(time.RFC3339Nano)
			}
			if scope != nil {
				listParams["scope"] = scope
			}
			if opts.Mention != "" {
				listParams["mention_role"] = strings.TrimPrefix(opts.Mention, "@")
			}
			if opts.CallerAgentID != "" {
				listParams["caller_agent_id"] = opts.CallerAgentID
			}
			if opts.ForAgent != "" {
				listParams["for_agent"] = opts.ForAgent
			}
			if opts.ForAgentRole != "" {
				listParams["for_agent_role"] = opts.ForAgentRole
			}
			listParams["exclude_self"] = true

			if err := client.Call("message.list", listParams, &inbox); err != nil {
				continue // Ignore errors and keep waiting
			}

			// Return the first unseen message (newest first due to DESC sort)
			for i := range inbox.Messages {
				msg := &inbox.Messages[i]
				if seen[msg.MessageID] {
					continue
				}
				return msg, nil
			}

			// Mark all returned messages as seen so we don't re-process them
			for _, msg := range inbox.Messages {
				seen[msg.MessageID] = true
			}
		}
	}
}
