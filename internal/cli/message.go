package cli

import (
	"fmt"
	"strings"

	"github.com/leonletto/thrum/internal/types"
)

// --- Message Get ---

// MessageGetResponse represents the response from message.get RPC.
type MessageGetResponse struct {
	Message MessageDetail `json:"message"`
}

// MessageDetail represents detailed information about a message.
type MessageDetail struct {
	MessageID string            `json:"message_id"`
	ThreadID  string            `json:"thread_id,omitempty"`
	Author    AuthorInfo        `json:"author"`
	Body      types.MessageBody `json:"body"`
	Scopes    []types.Scope     `json:"scopes"`
	Refs      []types.Ref       `json:"refs"`
	Metadata  MessageMetadata   `json:"metadata"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at,omitempty"`
	Deleted   bool              `json:"deleted"`
}

// AuthorInfo represents the message author.
type AuthorInfo struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
}

// MessageMetadata represents message metadata.
type MessageMetadata struct {
	DeletedAt    string `json:"deleted_at,omitempty"`
	DeleteReason string `json:"delete_reason,omitempty"`
}

// MessageGet retrieves a single message by ID.
func MessageGet(client *Client, messageID string) (*MessageGetResponse, error) {
	req := map[string]string{"message_id": messageID}
	var resp MessageGetResponse
	if err := client.Call("message.get", req, &resp); err != nil {
		return nil, fmt.Errorf("message.get RPC failed: %w", err)
	}
	return &resp, nil
}

// FormatMessageGet formats a message detail for display.
func FormatMessageGet(resp *MessageGetResponse) string {
	msg := resp.Message
	var out strings.Builder

	agentName := extractAgentName(msg.Author.AgentID)
	relTime := formatRelativeTime(msg.CreatedAt)

	out.WriteString(fmt.Sprintf("Message: %s\n", msg.MessageID))
	out.WriteString(fmt.Sprintf("  From:    %s\n", agentName))
	out.WriteString(fmt.Sprintf("  Time:    %s\n", relTime))

	if msg.ThreadID != "" {
		out.WriteString(fmt.Sprintf("  Thread:  %s\n", msg.ThreadID))
	}

	if len(msg.Scopes) > 0 {
		scopeStrs := make([]string, len(msg.Scopes))
		for i, s := range msg.Scopes {
			scopeStrs[i] = s.Type + ":" + s.Value
		}
		out.WriteString(fmt.Sprintf("  Scopes:  %s\n", strings.Join(scopeStrs, ", ")))
	}

	if len(msg.Refs) > 0 {
		refStrs := make([]string, len(msg.Refs))
		for i, r := range msg.Refs {
			refStrs[i] = r.Type + ":" + r.Value
		}
		out.WriteString(fmt.Sprintf("  Refs:    %s\n", strings.Join(refStrs, ", ")))
	}

	if msg.UpdatedAt != "" {
		out.WriteString(fmt.Sprintf("  Edited:  %s\n", formatRelativeTime(msg.UpdatedAt)))
	}

	if msg.Deleted {
		out.WriteString("  Status:  DELETED\n")
	}

	out.WriteString("\n")
	out.WriteString(msg.Body.Content)
	out.WriteString("\n")

	return out.String()
}

// --- Message Edit ---

// MessageEditResponse represents the response from message.edit RPC.
type MessageEditResponse struct {
	MessageID string `json:"message_id"`
	UpdatedAt string `json:"updated_at"`
	Version   int    `json:"version"`
}

// MessageEdit edits a message's content.
func MessageEdit(client *Client, messageID, content string) (*MessageEditResponse, error) {
	req := map[string]string{
		"message_id": messageID,
		"content":    content,
	}
	var resp MessageEditResponse
	if err := client.Call("message.edit", req, &resp); err != nil {
		return nil, fmt.Errorf("message.edit RPC failed: %w", err)
	}
	return &resp, nil
}

// FormatMessageEdit formats the edit response for display.
func FormatMessageEdit(resp *MessageEditResponse) string {
	return fmt.Sprintf("✓ Message edited: %s (version %d)\n", resp.MessageID, resp.Version)
}

// --- Message Delete ---

// MessageDeleteResponse represents the response from message.delete RPC.
type MessageDeleteResponse struct {
	MessageID string `json:"message_id"`
	DeletedAt string `json:"deleted_at"`
}

// MessageDelete deletes a message.
func MessageDelete(client *Client, messageID string) (*MessageDeleteResponse, error) {
	req := map[string]string{"message_id": messageID}
	var resp MessageDeleteResponse
	if err := client.Call("message.delete", req, &resp); err != nil {
		return nil, fmt.Errorf("message.delete RPC failed: %w", err)
	}
	return &resp, nil
}

// FormatMessageDelete formats the delete response for display.
func FormatMessageDelete(resp *MessageDeleteResponse) string {
	return fmt.Sprintf("✓ Message deleted: %s\n", resp.MessageID)
}

// --- Message Mark Read ---

// MarkReadResponse represents the response from message.markRead RPC.
type MarkReadResponse struct {
	MarkedCount int                 `json:"marked_count"`
	AlsoReadBy  map[string][]string `json:"also_read_by,omitempty"`
}

// MessageMarkRead marks messages as read.
func MessageMarkRead(client *Client, messageIDs []string, callerAgentID string) (*MarkReadResponse, error) {
	req := map[string]any{"message_ids": messageIDs}
	if callerAgentID != "" {
		req["caller_agent_id"] = callerAgentID
	}
	var resp MarkReadResponse
	if err := client.Call("message.markRead", req, &resp); err != nil {
		return nil, fmt.Errorf("message.markRead RPC failed: %w", err)
	}
	return &resp, nil
}

// FormatMarkRead formats the mark-read response for display.
func FormatMarkRead(resp *MarkReadResponse) string {
	if resp.MarkedCount == 1 {
		return "✓ Marked 1 message as read\n"
	}
	return fmt.Sprintf("✓ Marked %d messages as read\n", resp.MarkedCount)
}

// --- Reply ---

// ReplyOptions contains options for the reply command.
type ReplyOptions struct {
	MessageID     string
	Content       string
	Format        string
	CallerAgentID string // Caller's resolved agent ID (for worktree identity)
}

// Reply sends a reply to a message.
// It fetches the parent message to copy its audience (mentions/scopes) and sets reply_to ref.
func Reply(client *Client, opts ReplyOptions) (*SendResult, error) {
	// Get the parent message to extract its audience
	parentResp, err := MessageGet(client, opts.MessageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get parent message: %w", err)
	}
	parent := parentResp.Message

	// Build send options with reply_to ref
	sendOpts := SendOptions{
		Content:       opts.Content,
		ReplyTo:       opts.MessageID,
		CallerAgentID: opts.CallerAgentID,
	}

	if opts.Format != "" {
		sendOpts.Format = opts.Format
	}

	// Copy audience from parent message:
	// 1. Extract mentions from parent's refs (mention:agent_id refs)
	var mentions []string
	for _, ref := range parent.Refs {
		if ref.Type == "mention" {
			mentions = append(mentions, ref.Value)
		}
	}

	// 2. Add original sender so the reply routes back to them
	senderID := parent.Author.AgentID
	if senderID != "" && senderID != opts.CallerAgentID {
		alreadyMentioned := false
		for _, m := range mentions {
			if m == senderID {
				alreadyMentioned = true
				break
			}
		}
		if !alreadyMentioned {
			mentions = append(mentions, senderID)
		}
	}

	// 3. Add group scopes as mentions
	for _, scope := range parent.Scopes {
		if scope.Type == "group" {
			mentions = append(mentions, "@"+scope.Value)
		}
	}

	// Set mentions if we found any
	if len(mentions) > 0 {
		sendOpts.Mentions = mentions
	}

	return Send(client, sendOpts)
}
