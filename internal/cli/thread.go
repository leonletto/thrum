package cli

import (
	"fmt"
	"strings"

	"github.com/leonletto/thrum/internal/types"
)

// --- Thread Create ---

// CreateThreadRequest represents the request for thread.create RPC.
type CreateThreadRequest struct {
	Title         string          `json:"title"`
	Scopes        []types.Scope   `json:"scopes,omitempty"`
	Recipient     *string         `json:"recipient,omitempty"`
	Message       *MessageContent `json:"message,omitempty"`
	CallerAgentID string          `json:"caller_agent_id,omitempty"`
}

// MessageContent represents message content for thread.create.
type MessageContent struct {
	Content string `json:"content"`
	Format  string `json:"format,omitempty"`
}

// CreateThreadResponse represents the response from thread.create RPC.
type CreateThreadResponse struct {
	ThreadID  string  `json:"thread_id"`
	Title     string  `json:"title"`
	CreatedBy string  `json:"created_by"`
	CreatedAt string  `json:"created_at"`
	MessageID *string `json:"message_id,omitempty"`
}

// ThreadCreateOptions contains options for creating a thread.
type ThreadCreateOptions struct {
	Title         string
	Scopes        []types.Scope
	To            string // Recipient agent (e.g., "@reviewer")
	Message       string // Initial message content
	CallerAgentID string // Caller's resolved agent ID (for worktree identity)
}

// ThreadCreate creates a new thread.
func ThreadCreate(client *Client, opts ThreadCreateOptions) (*CreateThreadResponse, error) {
	req := CreateThreadRequest{
		Title:         opts.Title,
		Scopes:        opts.Scopes,
		CallerAgentID: opts.CallerAgentID,
	}

	// Add recipient and message if --to and --message are provided
	if opts.To != "" {
		recipient := strings.TrimPrefix(opts.To, "@")
		req.Recipient = &recipient
	}
	if opts.Message != "" {
		req.Message = &MessageContent{
			Content: opts.Message,
			Format:  "markdown",
		}
	}

	var result CreateThreadResponse
	if err := client.Call("thread.create", req, &result); err != nil {
		return nil, fmt.Errorf("thread.create RPC failed: %w", err)
	}

	return &result, nil
}

// FormatThreadCreate formats the thread create response for display.
func FormatThreadCreate(result *CreateThreadResponse) string {
	output := fmt.Sprintf("✓ Thread created: %s\n", result.ThreadID)
	output += fmt.Sprintf("  Title:      %s\n", result.Title)
	if result.CreatedBy != "" {
		output += fmt.Sprintf("  Created by: %s\n", result.CreatedBy)
	}
	if result.MessageID != nil {
		output += fmt.Sprintf("  Message:    %s\n", *result.MessageID)
	}

	return output
}

// --- Thread List ---

// ThreadListOptions contains options for listing threads.
type ThreadListOptions struct {
	Scope         string
	Page          int
	PageSize      int
	CallerAgentID string // Caller's resolved agent ID (for worktree identity)
}

// ThreadListResponse represents the response from thread.list RPC.
type ThreadListResponse struct {
	Threads    []ThreadSummary `json:"threads"`
	Total      int             `json:"total"`
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	TotalPages int             `json:"total_pages"`
}

// ThreadSummary represents a thread in the list.
type ThreadSummary struct {
	ThreadID     string  `json:"thread_id"`
	Title        string  `json:"title"`
	MessageCount int     `json:"message_count"`
	UnreadCount  int     `json:"unread_count"`
	LastActivity string  `json:"last_activity"`
	LastSender   string  `json:"last_sender"`
	Preview      *string `json:"preview,omitempty"`
	CreatedBy    string  `json:"created_by"`
	CreatedAt    string  `json:"created_at"`
}

// ThreadList lists threads with optional filtering and pagination.
func ThreadList(client *Client, opts ThreadListOptions) (*ThreadListResponse, error) {
	params := map[string]any{}

	if opts.Scope != "" {
		parts := strings.SplitN(opts.Scope, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("scope must be in 'type:value' format, got: %s", opts.Scope)
		}
		params["scope"] = map[string]string{"type": parts[0], "value": parts[1]}
	}

	if opts.PageSize > 0 {
		params["page_size"] = opts.PageSize
	}
	if opts.Page > 0 {
		params["page"] = opts.Page
	}

	if opts.CallerAgentID != "" {
		params["caller_agent_id"] = opts.CallerAgentID
	}

	var resp ThreadListResponse
	if err := client.Call("thread.list", params, &resp); err != nil {
		return nil, fmt.Errorf("thread.list RPC failed: %w", err)
	}
	return &resp, nil
}

// FormatThreadList formats the thread list for display.
func FormatThreadList(resp *ThreadListResponse) string {
	var out strings.Builder

	if len(resp.Threads) == 0 {
		return "No threads found.\n"
	}

	out.WriteString(fmt.Sprintf("%-20s %-30s %5s %6s %s\n", "THREAD", "TITLE", "MSGS", "UNREAD", "LAST ACTIVITY"))
	out.WriteString(strings.Repeat("─", 85) + "\n")

	for _, t := range resp.Threads {
		title := t.Title
		if len(title) > 28 {
			title = title[:25] + "..."
		}

		relTime := formatRelativeTime(t.LastActivity)
		sender := extractAgentName(t.LastSender)

		unread := fmt.Sprintf("%d", t.UnreadCount)
		if t.UnreadCount == 0 {
			unread = "·"
		}

		out.WriteString(fmt.Sprintf("%-20s %-30s %5d %6s %s (%s)\n",
			t.ThreadID, title, t.MessageCount, unread, relTime, sender))
	}

	start := (resp.Page-1)*resp.PageSize + 1
	end := start + len(resp.Threads) - 1
	out.WriteString(fmt.Sprintf("\nShowing %d-%d of %d threads\n", start, end, resp.Total))

	return out.String()
}

// --- Thread Show ---

// ThreadShowOptions contains options for showing a thread.
type ThreadShowOptions struct {
	ThreadID string
	Page     int
	PageSize int
}

// ThreadShowResponse represents the response from thread.get RPC.
type ThreadShowResponse struct {
	Thread     ThreadDetailInfo `json:"thread"`
	Messages   []Message        `json:"messages"`
	Total      int              `json:"total"`
	Page       int              `json:"page"`
	PageSize   int              `json:"page_size"`
	TotalPages int              `json:"total_pages"`
}

// ThreadDetailInfo represents thread metadata.
type ThreadDetailInfo struct {
	ThreadID  string `json:"thread_id"`
	Title     string `json:"title"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
}

// ThreadShow retrieves a thread with its messages.
func ThreadShow(client *Client, opts ThreadShowOptions) (*ThreadShowResponse, error) {
	params := map[string]any{
		"thread_id": opts.ThreadID,
	}
	if opts.PageSize > 0 {
		params["page_size"] = opts.PageSize
	}
	if opts.Page > 0 {
		params["page"] = opts.Page
	}

	var resp ThreadShowResponse
	if err := client.Call("thread.get", params, &resp); err != nil {
		return nil, fmt.Errorf("thread.get RPC failed: %w", err)
	}
	return &resp, nil
}

// FormatThreadShow formats a thread with messages for display.
func FormatThreadShow(resp *ThreadShowResponse) string {
	var out strings.Builder

	creator := extractAgentName(resp.Thread.CreatedBy)
	out.WriteString(fmt.Sprintf("Thread: %s\n", resp.Thread.ThreadID))
	out.WriteString(fmt.Sprintf("  Title:   %s\n", resp.Thread.Title))
	out.WriteString(fmt.Sprintf("  Created: %s by %s\n\n", formatRelativeTime(resp.Thread.CreatedAt), creator))

	if len(resp.Messages) == 0 {
		out.WriteString("No messages in this thread.\n")
		return out.String()
	}

	out.WriteString("┌" + strings.Repeat("─", 65) + "┐\n")

	for i, msg := range resp.Messages {
		agentName := extractAgentName(msg.AgentID)
		relTime := formatRelativeTime(msg.CreatedAt)

		header := fmt.Sprintf("│ %s  %s  %s", msg.MessageID, agentName, relTime)
		header = padLine(header, 65)
		out.WriteString(header + "│\n")

		content := wordWrap(msg.Body.Content, 63)
		for _, line := range strings.Split(content, "\n") {
			out.WriteString("│ " + padLine(line, 63) + "│\n")
		}

		if i < len(resp.Messages)-1 {
			out.WriteString("├" + strings.Repeat("─", 65) + "┤\n")
		} else {
			out.WriteString("└" + strings.Repeat("─", 65) + "┘\n")
		}
	}

	start := (resp.Page-1)*resp.PageSize + 1
	end := start + len(resp.Messages) - 1
	out.WriteString(fmt.Sprintf("Showing %d-%d of %d messages\n", start, end, resp.Total))

	return out.String()
}
