package cli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SendOptions contains options for sending a message.
type SendOptions struct {
	Content       string
	Scopes        []string // Format: "type:value"
	Refs          []string // Format: "type:value"
	Mentions      []string // Format: "@role"
	Thread        string
	Structured    string // JSON string
	Priority      string
	Format        string
	To            string // Direct recipient (e.g., "@reviewer")
	Broadcast     bool   // Send as broadcast (no specific recipient)
	CallerAgentID string // Caller's resolved agent ID (for worktree identity)
}

// SendResult contains the result of sending a message.
type SendResult struct {
	MessageID string `json:"message_id"`
	ThreadID  string `json:"thread_id,omitempty"`
	CreatedAt string `json:"created_at"`
}

// Send sends a message via the daemon.
func Send(client *Client, opts SendOptions) (*SendResult, error) {
	// Validate mutual exclusivity of --broadcast and --to
	if opts.Broadcast && opts.To != "" {
		return nil, fmt.Errorf("--broadcast and --to are mutually exclusive")
	}

	// Parse scopes
	scopes, err := parseScopes(opts.Scopes)
	if err != nil {
		return nil, fmt.Errorf("invalid scope: %w", err)
	}

	// Parse refs
	refs, err := parseRefs(opts.Refs)
	if err != nil {
		return nil, fmt.Errorf("invalid ref: %w", err)
	}

	// Parse structured data if provided
	var structured map[string]any
	if opts.Structured != "" {
		if err := json.Unmarshal([]byte(opts.Structured), &structured); err != nil {
			return nil, fmt.Errorf("invalid structured data: %w", err)
		}
	}

	// Add --to recipient as a mention
	if opts.To != "" {
		to := opts.To
		if !strings.HasPrefix(to, "@") {
			to = "@" + to
		}
		opts.Mentions = append(opts.Mentions, to)
	}

	// Build params
	params := map[string]any{
		"content": opts.Content,
	}

	if opts.Format != "" {
		params["format"] = opts.Format
	}

	if structured != nil {
		params["structured"] = structured
	}

	if opts.Thread != "" {
		params["thread_id"] = opts.Thread
	}

	if len(scopes) > 0 {
		params["scopes"] = scopes
	}

	if len(refs) > 0 {
		params["refs"] = refs
	}

	if len(opts.Mentions) > 0 {
		params["mentions"] = opts.Mentions
	}

	if opts.Priority != "" {
		params["priority"] = opts.Priority
	}

	if opts.CallerAgentID != "" {
		params["caller_agent_id"] = opts.CallerAgentID
	}

	// Call RPC
	var result SendResult
	if err := client.Call("message.send", params, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// parseScopes parses scope strings in "type:value" format.
func parseScopes(scopes []string) ([]map[string]string, error) {
	if len(scopes) == 0 {
		return nil, nil
	}

	result := make([]map[string]string, len(scopes))
	for i, scope := range scopes {
		parts := strings.SplitN(scope, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("scope must be in 'type:value' format, got: %s", scope)
		}

		result[i] = map[string]string{
			"type":  parts[0],
			"value": parts[1],
		}
	}

	return result, nil
}

// parseRefs parses ref strings in "type:value" format.
func parseRefs(refs []string) ([]map[string]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}

	result := make([]map[string]string, len(refs))
	for i, ref := range refs {
		parts := strings.SplitN(ref, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("ref must be in 'type:value' format, got: %s", ref)
		}

		result[i] = map[string]string{
			"type":  parts[0],
			"value": parts[1],
		}
	}

	return result, nil
}
