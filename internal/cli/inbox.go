package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/identity"
)

// InboxOptions contains options for listing messages.
type InboxOptions struct {
	Scope             string // Format: "type:value"
	Mentions          bool
	Unread            bool
	PageSize          int
	Page              int
	CallerAgentID     string // Caller's resolved agent ID (for worktree identity)
	CallerMentionRole string // Caller's role (for mentions filter)
	ForAgent          string // Auto-filter: agent name (messages mentioning this name + broadcasts)
	ForAgentRole      string // Auto-filter: agent role (messages mentioning this role + broadcasts)
}

// Message represents a message from the inbox.
type Message struct {
	MessageID string `json:"message_id"`
	ThreadID  string `json:"thread_id,omitempty"`
	AgentID   string `json:"agent_id"`
	Body      struct {
		Format     string `json:"format"`
		Content    string `json:"content"`
		Structured string `json:"structured,omitempty"`
	} `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Deleted   bool   `json:"deleted"`
	IsRead    bool   `json:"is_read"`
}

// InboxResult contains the result of listing messages.
type InboxResult struct {
	Messages   []Message `json:"messages"`
	Total      int       `json:"total"`
	Unread     int       `json:"unread"`
	Page       int       `json:"page"`
	PageSize   int       `json:"page_size"`
	TotalPages int       `json:"total_pages"`
}

// Inbox retrieves messages from the inbox.
func Inbox(client *Client, opts InboxOptions) (*InboxResult, error) {
	params := map[string]any{}

	// Parse scope if provided
	if opts.Scope != "" {
		parts := strings.SplitN(opts.Scope, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("scope must be in 'type:value' format, got: %s", opts.Scope)
		}
		params["scope"] = map[string]string{
			"type":  parts[0],
			"value": parts[1],
		}
	}

	if opts.Mentions {
		params["mentions"] = true
	}

	if opts.Unread {
		params["unread"] = true
	}

	// Exclude messages sent by the current agent (no echo)
	params["exclude_self"] = true

	if opts.CallerAgentID != "" {
		params["caller_agent_id"] = opts.CallerAgentID
	}

	if opts.CallerMentionRole != "" {
		params["caller_mention_role"] = opts.CallerMentionRole
	}

	if opts.ForAgent != "" {
		params["for_agent"] = opts.ForAgent
	}

	if opts.ForAgentRole != "" {
		params["for_agent_role"] = opts.ForAgentRole
	}

	if opts.PageSize > 0 {
		params["page_size"] = opts.PageSize
	}

	if opts.Page > 0 {
		params["page"] = opts.Page
	}

	// Call RPC
	var result InboxResult
	if err := client.Call("message.list", params, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// FormatInbox formats the inbox result for display.
func FormatInbox(result *InboxResult) string {
	return FormatInboxWithOptions(result, InboxFormatOptions{})
}

// InboxFormatOptions contains options for formatting inbox output.
type InboxFormatOptions struct {
	ActiveScope string // The active filter scope (for empty state feedback)
	ForAgent    string // The agent name being filtered for (for empty state / footer)
	Quiet       bool
	JSON        bool
}

// FormatInboxWithOptions formats the inbox with filter context for better empty states.
func FormatInboxWithOptions(result *InboxResult, opts InboxFormatOptions) string {
	var output strings.Builder

	if len(result.Messages) == 0 {
		if opts.ActiveScope != "" {
			output.WriteString(fmt.Sprintf("No messages matching filter --scope %s\n", opts.ActiveScope))
			output.WriteString(fmt.Sprintf("  Showing 0 of %d total messages (filter: scope=%s)\n", result.Total, opts.ActiveScope))
			if !opts.Quiet && !opts.JSON {
				output.WriteString(Hint("inbox.empty", opts.Quiet, opts.JSON))
			}
		} else if opts.ForAgent != "" {
			output.WriteString(fmt.Sprintf("No messages for @%s.\n", opts.ForAgent))
			if !opts.Quiet && !opts.JSON {
				output.WriteString("  Tip: Use 'thrum inbox --all' to see all messages\n")
			}
		} else {
			output.WriteString("No messages in inbox.\n")
			if !opts.Quiet && !opts.JSON {
				output.WriteString(Hint("inbox.empty", opts.Quiet, opts.JSON))
			}
		}
		return output.String()
	}

	// Use terminal width for box sizing
	termWidth := GetTerminalWidth()
	boxWidth := termWidth - 2 // 2 for left/right borders
	if boxWidth < 40 {
		boxWidth = 40
	}
	if boxWidth > 120 {
		boxWidth = 120
	}
	contentWidth := boxWidth - 2 // 2 for padding inside box

	// Top border
	output.WriteString("┌" + strings.Repeat("─", boxWidth) + "┐\n")

	for i, msg := range result.Messages {
		// Message header line
		agentName := extractAgentName(msg.AgentID)
		relTime := formatRelativeTime(msg.CreatedAt)

		// Read indicator
		readIndicator := "●" // unread
		if msg.IsRead {
			readIndicator = "○" // read
		}

		header := fmt.Sprintf("│ %s %s  %s  %s", readIndicator, msg.MessageID, agentName, relTime)

		// Add thread reference
		if msg.ThreadID != "" {
			header += fmt.Sprintf("  thread:%s", truncateID(msg.ThreadID, 12))
		}

		// Add "(edited)" indicator if message was edited
		if msg.UpdatedAt != "" {
			header += " (edited)"
		}

		// Pad to box width
		header = padLine(header, boxWidth)
		output.WriteString(header + "│\n")

		// Message content (word wrap to fit in box)
		content := wordWrap(msg.Body.Content, contentWidth)
		for _, line := range strings.Split(content, "\n") {
			output.WriteString("│ " + padLine(line, contentWidth) + "│\n")
		}

		// Separator or bottom border
		if i < len(result.Messages)-1 {
			output.WriteString("├" + strings.Repeat("─", boxWidth) + "┤\n")
		} else {
			output.WriteString("└" + strings.Repeat("─", boxWidth) + "┘\n")
		}
	}

	// Footer with pagination info
	start := (result.Page-1)*result.PageSize + 1
	end := start + len(result.Messages) - 1

	footer := fmt.Sprintf("Showing %d-%d of %d messages", start, end, result.Total)
	if result.Unread > 0 {
		footer += fmt.Sprintf(" (%d unread)", result.Unread)
	}
	if opts.ForAgent != "" {
		footer += fmt.Sprintf(" (filtered for @%s)", opts.ForAgent)
	}

	output.WriteString(footer + "\n")

	return output.String()
}

// truncateID truncates an ID to a max length, adding ... If needed.
func truncateID(id string, maxLen int) string {
	if len(id) <= maxLen {
		return id
	}
	return id[:maxLen-3] + "..."
}

// extractAgentName extracts a short name from agent ID for display.
func extractAgentName(agentID string) string {
	return identity.ExtractDisplayName(agentID)
}

// formatRelativeTime formats a timestamp as relative time.
func formatRelativeTime(timestamp string) string {
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return timestamp
	}

	now := time.Now()
	diff := now.Sub(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", mins)
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", hours)
	case diff < 7*24*time.Hour:
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	default:
		return t.Format("Jan 2")
	}
}

// wordWrap wraps text to fit within a given width.
func wordWrap(text string, width int) string {
	if len(text) <= width {
		return text
	}

	var lines []string
	words := strings.Fields(text)

	if len(words) == 0 {
		return text
	}

	currentLine := words[0]

	for _, word := range words[1:] {
		if len(currentLine)+1+len(word) <= width {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}

	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return strings.Join(lines, "\n")
}

// padLine pads a line to a specific length.
func padLine(line string, length int) string {
	// Remove ANSI color codes for length calculation
	visibleLen := len(line)
	if visibleLen >= length {
		return line[:length]
	}
	return line + strings.Repeat(" ", length-visibleLen)
}
