package restart

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	if len(truncated) == 0 {
		// Edge case: tail had no USER marker — return the raw tail instead of empty body
		truncated = lines[len(lines)-maxLines:]
	}

	header := fmt.Sprintf("[Conversation continued from earlier — truncated to last %d lines]\n\n", len(truncated))
	return header + strings.Join(truncated, "\n")
}

// claudeSessionInfo represents the ~/.claude/sessions/<pid>.json file.
type claudeSessionInfo struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
}

// FindSessionJSONL locates the JSONL transcript for a Claude Code session
// given its PID. ClaudeDir is typically ~/.claude.
func FindSessionJSONL(claudeDir string, pid int) (string, error) {
	sessFile := filepath.Join(claudeDir, "sessions", fmt.Sprintf("%d.json", pid))
	data, err := os.ReadFile(sessFile) // #nosec G304 -- pid is from internal identity resolution
	if err != nil {
		return "", fmt.Errorf("read session file for PID %d: %w", pid, err)
	}

	var info claudeSessionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return "", fmt.Errorf("parse session file: %w", err)
	}

	// Encode the cwd to match Claude's project directory naming
	encoded := encodeCwd(info.Cwd)
	jsonlPath := filepath.Join(claudeDir, "projects", encoded, info.SessionID+".jsonl")

	if _, err := os.Stat(jsonlPath); err != nil {
		return "", fmt.Errorf("JSONL not found at %s: %w", jsonlPath, err)
	}
	return jsonlPath, nil
}

// encodeCwd converts a cwd path to Claude's project directory name format.
// /Users/leon/dev/project → -Users-leon-dev-project
// /Users/leon/.workspaces/thrum → -Users-leon--workspaces-thrum
// Claude Code replaces both "/" and "." with "-".
func encodeCwd(cwd string) string {
	encoded := strings.TrimPrefix(cwd, "/")
	encoded = strings.ReplaceAll(encoded, "/", "-")
	encoded = strings.ReplaceAll(encoded, ".", "-")
	return "-" + encoded
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

// restartSnapshotPath returns the path to an agent's restart snapshot.
func restartSnapshotPath(thrumDir, agentName string) string {
	return filepath.Join(thrumDir, "restart", agentName+".md")
}

// SnapshotExists checks if a restart snapshot exists for the given agent.
func SnapshotExists(thrumDir, agentName string) bool {
	_, err := os.Stat(restartSnapshotPath(thrumDir, agentName))
	return err == nil
}

// DeleteSnapshot removes an existing restart snapshot (and any .consumed file).
func DeleteSnapshot(thrumDir, agentName string) {
	_ = os.Remove(restartSnapshotPath(thrumDir, agentName))
	_ = os.Remove(restartSnapshotPath(thrumDir, agentName) + ".consumed")
}

// SaveSnapshot writes a restart snapshot to disk.
// Creates the restart/ directory if needed.
func SaveSnapshot(thrumDir, agentName, content string) error {
	dir := filepath.Join(thrumDir, "restart")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create restart dir: %w", err)
	}
	path := restartSnapshotPath(thrumDir, agentName)
	return os.WriteFile(path, []byte(content), 0600)
}

// Restore reads and removes a restart snapshot. Returns the content.
// Uses rename-then-delete for crash safety.
func Restore(thrumDir, agentName string) (string, error) {
	path := restartSnapshotPath(thrumDir, agentName)
	data, err := os.ReadFile(path) // #nosec G304 -- path from internal thrumDir + agent name
	if err != nil {
		return "", fmt.Errorf("no restart snapshot for %s: %w", agentName, err)
	}
	consumed := path + ".consumed"
	if err := os.Rename(path, consumed); err != nil {
		// Fallback: direct delete if rename fails (e.g., cross-device)
		_ = os.Remove(path)
	} else {
		_ = os.Remove(consumed)
	}
	return string(data), nil
}

// ConsumeInPrime reads a restart snapshot for prime inclusion.
// Uses rename-then-delete for crash safety.
func ConsumeInPrime(thrumDir, agentName string) (string, error) {
	path := restartSnapshotPath(thrumDir, agentName)
	data, err := os.ReadFile(path) // #nosec G304 -- internal path
	if err != nil {
		return "", err
	}
	_ = os.Rename(path, path+".consumed")
	return string(data), nil
}

// CleanupConsumed deletes the .consumed file after prime output succeeds.
func CleanupConsumed(thrumDir, agentName string) {
	_ = os.Remove(restartSnapshotPath(thrumDir, agentName) + ".consumed")
}
