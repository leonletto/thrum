package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// HealthResult contains daemon health information.
type HealthResult struct {
	Status    string `json:"status"`
	UptimeMs  int64  `json:"uptime_ms"`
	Version   string `json:"version"`
	RepoID    string `json:"repo_id"`
	SyncState string `json:"sync_state"`
}

// WhoamiResult contains current agent information.
type WhoamiResult struct {
	AgentID      string `json:"agent_id"`
	Role         string `json:"role"`
	Module       string `json:"module"`
	Display      string `json:"display"`
	Source       string `json:"source"`
	SessionID    string `json:"session_id,omitempty"`
	SessionStart string `json:"session_start,omitempty"`
}

// StatusResult contains combined status information.
type StatusResult struct {
	Health      HealthResult      `json:"health"`
	Agent       *WhoamiResult     `json:"agent,omitempty"`
	WorkContext *AgentWorkContext `json:"work_context,omitempty"`
	Inbox       *struct {
		Total  int `json:"total"`
		Unread int `json:"unread"`
	} `json:"inbox,omitempty"`
	WebSocketPort int `json:"websocket_port,omitempty"`
}

// Status retrieves current status from the daemon.
func Status(client *Client) (*StatusResult, error) {
	result := &StatusResult{}

	// Get health status
	if err := client.Call("health", map[string]any{}, &result.Health); err != nil {
		return nil, fmt.Errorf("failed to get health: %w", err)
	}

	// Get agent info (may fail if no agent registered)
	var whoami WhoamiResult
	if err := client.Call("agent.whoami", map[string]any{}, &whoami); err == nil {
		result.Agent = &whoami

		// Get inbox counts if we have an agent
		var inbox InboxResult
		if err := client.Call("message.list", map[string]any{"page_size": 0}, &inbox); err == nil {
			result.Inbox = &struct {
				Total  int `json:"total"`
				Unread int `json:"unread"`
			}{
				Total:  inbox.Total,
				Unread: inbox.Unread,
			}
		}

		// Get work context if agent has an active session
		if whoami.SessionID != "" {
			// Filter by agent ID to get only this agent's context
			contextReq := ListContextRequest{
				AgentID: whoami.AgentID,
			}
			var contextResp ListContextResponse
			if err := client.Call("agent.listContext", contextReq, &contextResp); err == nil {
				if len(contextResp.Contexts) > 0 {
					result.WorkContext = &contextResp.Contexts[0]
				}
			}
		}
	}

	return result, nil
}

// FormatStatus formats the status result for display.
func FormatStatus(result *StatusResult) string {
	var output strings.Builder

	// Agent info
	if result.Agent != nil {
		output.WriteString(fmt.Sprintf("Agent:    %s (@%s)\n", result.Agent.AgentID, result.Agent.Role))
		if result.Agent.Module != "" {
			output.WriteString(fmt.Sprintf("Module:   %s\n", result.Agent.Module))
		}
		if result.Agent.Display != "" {
			output.WriteString(fmt.Sprintf("Display:  %s\n", result.Agent.Display))
		}

		// Session info
		if result.Agent.SessionID != "" {
			sessionAge := ""
			if result.Agent.SessionStart != "" {
				if t, err := time.Parse(time.RFC3339, result.Agent.SessionStart); err == nil {
					duration := time.Since(t)
					sessionAge = fmt.Sprintf(" (duration: %s)", formatDuration(duration))
				}
			}
			output.WriteString(fmt.Sprintf("Session:  %s%s\n", result.Agent.SessionID, sessionAge))
		} else {
			output.WriteString("Session:  none (use 'thrum session start' to begin)\n")
		}

		// Work context (only if session is active)
		if result.WorkContext != nil {
			ctx := result.WorkContext

			// Current intent
			if ctx.Intent != "" {
				output.WriteString(fmt.Sprintf("Intent:   %s\n", ctx.Intent))
			}

			// Current task
			if ctx.CurrentTask != "" {
				output.WriteString(fmt.Sprintf("Task:     %s\n", ctx.CurrentTask))
			}

			// Branch and commit info
			if ctx.Branch != "" {
				commitInfo := ""
				if len(ctx.UnmergedCommits) > 0 {
					commitInfo = fmt.Sprintf(" (%d commits ahead)", len(ctx.UnmergedCommits))
				}
				output.WriteString(fmt.Sprintf("Branch:   %s%s\n", ctx.Branch, commitInfo))
			}

			// Changed files count
			totalChanged := len(ctx.ChangedFiles) + len(ctx.UncommittedFiles)
			if totalChanged > 0 {
				uncommittedInfo := ""
				if len(ctx.UncommittedFiles) > 0 {
					uncommittedInfo = fmt.Sprintf(", %d uncommitted", len(ctx.UncommittedFiles))
				}
				output.WriteString(fmt.Sprintf("Files:    %d changed%s\n", totalChanged, uncommittedInfo))
			}
		}
	} else {
		output.WriteString("Agent:    not registered (use 'thrum agent register')\n")
	}

	// Inbox info
	if result.Inbox != nil {
		unreadInfo := ""
		if result.Inbox.Unread > 0 {
			unreadInfo = fmt.Sprintf(" (%d unread)", result.Inbox.Unread)
		}
		output.WriteString(fmt.Sprintf("Inbox:    %d messages%s\n", result.Inbox.Total, unreadInfo))
	}

	// Sync status
	syncStatus := result.Health.SyncState
	if syncStatus == "synced" {
		output.WriteString("Sync:     ✓ synced\n")
	} else {
		output.WriteString(fmt.Sprintf("Sync:     %s\n", syncStatus))
	}

	// Daemon info
	uptime := formatDuration(time.Duration(result.Health.UptimeMs) * time.Millisecond)
	output.WriteString(fmt.Sprintf("Daemon:   running (%s uptime, v%s)\n", uptime, result.Health.Version))

	// WebSocket and UI
	if result.WebSocketPort > 0 {
		output.WriteString(fmt.Sprintf("WebSocket: ws://localhost:%d/ws\n", result.WebSocketPort))
		output.WriteString(fmt.Sprintf("UI:        http://localhost:%d\n", result.WebSocketPort))
	}

	return output.String()
}

// ReadWebSocketPort reads the WebSocket port from the port file.
// Returns 0 if the file doesn't exist or contains invalid data.
func ReadWebSocketPort(repoPath string) int {
	portPath := filepath.Join(repoPath, ".thrum", "var", "ws.port")
	content, err := os.ReadFile(portPath) //nolint:gosec // G304 - path derived from repo root
	if err != nil {
		return 0
	}

	portStr := strings.TrimSpace(string(content))
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}

	return port
}

// formatDuration formats a duration in a human-readable way.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		minutes := int(d.Minutes()) % 60
		if minutes > 0 {
			return fmt.Sprintf("%dh%dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	}
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	if hours > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	return fmt.Sprintf("%dd", days)
}
