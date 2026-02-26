package cli

import (
	"fmt"
	"os"
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
	Quiet         bool      // Suppress stderr status messages
}

// reconnectTimeout is how long to keep trying to reconnect to the daemon
// after a connection failure (e.g., daemon restart). The daemon can take
// 10+ seconds to restart, so we allow up to 60 seconds.
const reconnectTimeout = 60 * time.Second

// Wait blocks until a matching message arrives or timeout occurs.
// SocketPath is the Unix socket path to connect to the daemon.
// The connection is automatically re-established if the daemon restarts.
func Wait(socketPath string, opts WaitOptions) (*Message, error) {
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

	// Manage client connection with auto-reconnect
	var client *Client
	defer func() {
		if client != nil {
			_ = client.Close()
		}
	}()

	// connect attempts to create a new client, closing any existing one.
	connect := func() error {
		if client != nil {
			_ = client.Close()
			client = nil
		}
		c, err := NewClient(socketPath)
		if err != nil {
			return err
		}
		client = c
		return nil
	}

	// reconnect retries connection for up to reconnectTimeout, respecting
	// the overall wait timeout. Returns an error only if both deadlines expire.
	reconnect := func(overallTimeout <-chan time.Time) error {
		reconnectDeadline := time.After(reconnectTimeout)
		retryTicker := time.NewTicker(500 * time.Millisecond)
		defer retryTicker.Stop()

		for {
			select {
			case <-overallTimeout:
				return fmt.Errorf("timeout waiting for message")
			case <-reconnectDeadline:
				return fmt.Errorf("daemon did not restart within %s", reconnectTimeout)
			case <-retryTicker.C:
				if err := connect(); err == nil {
					if !opts.Quiet {
						fmt.Fprintln(os.Stderr, "Reconnected to daemon")
					}
					return nil
				}
			}
		}
	}

	// Initial connection
	if err := connect(); err != nil {
		// Daemon not running at start — try reconnecting
		if !opts.Quiet {
			fmt.Fprintln(os.Stderr, "Daemon not available, waiting for it to start...")
		}
		if err := reconnect(timeout); err != nil {
			return nil, err
		}
	}

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
				// Connection failed — daemon may have restarted.
				// Try to reconnect for up to reconnectTimeout.
				if !opts.Quiet {
					fmt.Fprintln(os.Stderr, "Lost connection to daemon, reconnecting...")
				}
				if err := reconnect(timeout); err != nil {
					return nil, err
				}
				continue
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
