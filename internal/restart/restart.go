package restart

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// jsonlEntry represents a single line in a Claude Code JSONL transcript.
type jsonlEntry struct {
	Type        string   `json:"type"`
	IsSidechain bool     `json:"isSidechain"`
	Message     jsonlMsg `json:"message"`
}

// jsonlMsg represents the message field in a JSONL entry.
type jsonlMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// contentBlock represents a block within a message's content array.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ExtractConversation reads a Claude Code JSONL transcript and extracts
// user+assistant text only (no tool use, thinking, sidechains, or non-message types).
// Returns formatted conversation text truncated to maxLines.
func ExtractConversation(jsonlPath string, maxLines int) (string, error) {
	f, err := os.Open(jsonlPath) // #nosec G304 -- path from internal session lookup
	if err != nil {
		return "", fmt.Errorf("open JSONL: %w", err)
	}
	defer func() { _ = f.Close() }()

	var exchanges []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		var entry jsonlEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}

		// Skip sidechains and non-conversation types
		if entry.IsSidechain {
			continue
		}
		if entry.Type != "user" && entry.Type != "assistant" {
			continue
		}

		text := extractText(entry.Message)
		if text == "" {
			continue
		}

		role := "USER"
		if entry.Type == "assistant" {
			role = "ASSISTANT"
		}
		exchanges = append(exchanges, fmt.Sprintf("=== %s ===\n%s", role, text))
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan JSONL: %w", err)
	}

	return truncateExchanges(exchanges, maxLines), nil
}

// extractText pulls text-only content from a message, skipping tool_use,
// tool_result, and thinking blocks.
func extractText(msg jsonlMsg) string {
	// Try as plain string first
	var plainText string
	if err := json.Unmarshal(msg.Content, &plainText); err == nil {
		return strings.TrimSpace(plainText)
	}

	// Try as array of content blocks
	var blocks []contentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return ""
	}

	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// truncateExchanges formats exchanges and truncates to maxLines.
// Ensures output starts with a USER marker and ends with ASSISTANT text.
func truncateExchanges(exchanges []string, maxLines int) string {
	if len(exchanges) == 0 {
		return ""
	}

	full := strings.Join(exchanges, "\n\n")
	lines := strings.Split(full, "\n")

	if len(lines) <= maxLines {
		return full
	}

	// Truncate from the top, keeping the tail
	truncated := lines[len(lines)-maxLines:]

	// Find the first === USER === marker to align boundary
	startIdx := 0
	for i, line := range truncated {
		if line == "=== USER ===" {
			startIdx = i
			break
		}
	}
	truncated = truncated[startIdx:]

	// Find the last === ASSISTANT === marker and keep everything through its text
	lastAssistantEnd := len(truncated)
	for i := len(truncated) - 1; i >= 0; i-- {
		if truncated[i] == "=== USER ===" {
			// Ends on a user message — trim it (we want to end on assistant)
			lastAssistantEnd = i
			break
		}
		if truncated[i] == "=== ASSISTANT ===" {
			break // Already ends on assistant, keep all
		}
	}
	truncated = truncated[:lastAssistantEnd]

	header := fmt.Sprintf("[Conversation continued from earlier — truncated to last %d lines]\n\n", len(truncated))
	return header + strings.Join(truncated, "\n")
}

// FormatRestartSnapshot builds the complete snapshot file content.
func FormatRestartSnapshot(agentName, sessionID, reason, conversation string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Restart Snapshot — %s\n\n", agentName)
	fmt.Fprintf(&out, "**Session:** %s\n", sessionID)
	fmt.Fprintf(&out, "**Saved:** %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&out, "**Reason:** %s\n\n", reason)
	out.WriteString(conversation)
	return out.String()
}
