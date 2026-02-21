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
	Status    string             `json:"status"`
	UptimeMs  int64              `json:"uptime_ms"`
	Version   string             `json:"version"`
	RepoID    string             `json:"repo_id"`
	SyncState string             `json:"sync_state"`
	Tailscale *TailscaleSyncInfo `json:"tailscale,omitempty"`
}

// TailscaleSyncInfo mirrors the RPC type for CLI deserialization.
type TailscaleSyncInfo struct {
	Enabled        bool            `json:"enabled"`
	Hostname       string          `json:"hostname"`
	ConnectedPeers int             `json:"connected_peers"`
	Peers          []TailscalePeer `json:"peers,omitempty"`
	SyncStatus     string          `json:"sync_status"`
}

// TailscalePeer represents a peer in the Tailscale sync status.
type TailscalePeer struct {
	DaemonID string `json:"daemon_id"`
	Hostname string `json:"hostname"`
	LastSync string `json:"last_sync"`
	Status   string `json:"status"`
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
	Branch       string `json:"branch,omitempty"`
	Intent       string `json:"intent,omitempty"`
}

// ContextInfo contains agent context file metadata for status display.
type ContextInfo struct {
	HasContext bool   `json:"has_context"`
	Size       int64  `json:"size,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// StatusResult contains combined status information.
type StatusResult struct {
	Health      HealthResult      `json:"health"`
	Agent       *WhoamiResult     `json:"agent,omitempty"`
	WorkContext *AgentWorkContext `json:"work_context,omitempty"`
	Context     *ContextInfo      `json:"context,omitempty"`
	Inbox       *struct {
		Total  int `json:"total"`
		Unread int `json:"unread"`
	} `json:"inbox,omitempty"`
	WebSocketPort int `json:"websocket_port,omitempty"`
}

// Status retrieves current status from the daemon.
func Status(client *Client, callerAgentID ...string) (*StatusResult, error) {
	result := &StatusResult{}

	// Get health status
	if err := client.Call("health", map[string]any{}, &result.Health); err != nil {
		return nil, fmt.Errorf("failed to get health: %w", err)
	}

	// Get agent info (may fail if no agent registered)
	var whoami WhoamiResult
	params := map[string]any{}
	if len(callerAgentID) > 0 && callerAgentID[0] != "" {
		params["caller_agent_id"] = callerAgentID[0]
	}
	if err := client.Call("agent.whoami", params, &whoami); err == nil {
		result.Agent = &whoami

		// Get inbox counts if we have an agent (scoped to this agent's actual inbox)
		var inbox InboxResult
		inboxParams := map[string]any{
			"page_size":    0,
			"exclude_self": true,
		}
		if whoami.AgentID != "" {
			inboxParams["for_agent"] = whoami.AgentID
		}
		if whoami.Role != "" {
			inboxParams["for_agent_role"] = whoami.Role
		}
		if err := client.Call("message.list", inboxParams, &inbox); err == nil {
			result.Inbox = &struct {
				Total  int `json:"total"`
				Unread int `json:"unread"`
			}{
				Total:  inbox.Total,
				Unread: inbox.Unread,
			}
		}

		// Get context file info
		var ctxShow struct {
			HasContext bool   `json:"has_context"`
			Size       int64  `json:"size,omitempty"`
			UpdatedAt  string `json:"updated_at,omitempty"`
		}
		if err := client.Call("context.show", map[string]any{"agent_name": whoami.AgentID}, &ctxShow); err == nil && ctxShow.HasContext {
			result.Context = &ContextInfo{
				HasContext: true,
				Size:       ctxShow.Size,
				UpdatedAt:  ctxShow.UpdatedAt,
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
		summary := &AgentSummary{
			AgentID:      result.Agent.AgentID,
			Role:         result.Agent.Role,
			Module:       result.Agent.Module,
			Display:      result.Agent.Display,
			SessionID:    result.Agent.SessionID,
			SessionStart: result.Agent.SessionStart,
		}
		if result.WorkContext != nil {
			summary.Branch = result.WorkContext.Branch
			summary.Intent = result.WorkContext.Intent
		}
		output.WriteString(FormatAgentSummary(summary))

		// Work context (only if session is active) — remaining fields not in AgentSummary
		if result.WorkContext != nil {
			ctx := result.WorkContext

			// Current task
			if ctx.CurrentTask != "" {
				fmt.Fprintf(&output, "Task:     %s\n", ctx.CurrentTask)
			}

			// Branch commit info (branch itself shown in AgentSummary)
			if ctx.Branch != "" && len(ctx.UnmergedCommits) > 0 {
				fmt.Fprintf(&output, "Commits:  %d ahead\n", len(ctx.UnmergedCommits))
			}

			// Changed files count
			totalChanged := len(ctx.ChangedFiles) + len(ctx.UncommittedFiles)
			if totalChanged > 0 {
				uncommittedInfo := ""
				if len(ctx.UncommittedFiles) > 0 {
					uncommittedInfo = fmt.Sprintf(", %d uncommitted", len(ctx.UncommittedFiles))
				}
				fmt.Fprintf(&output, "Files:    %d changed%s\n", totalChanged, uncommittedInfo)
			}
		}
	} else {
		output.WriteString("Agent:    not registered (use 'thrum agent register')\n")
	}

	// Context info
	if result.Context != nil && result.Context.HasContext {
		age := ""
		if result.Context.UpdatedAt != "" {
			if t, err := time.Parse(time.RFC3339, result.Context.UpdatedAt); err == nil {
				age = fmt.Sprintf(" (updated %s ago)", formatDuration(time.Since(t)))
			}
		}
		fmt.Fprintf(&output, "Context:  %d bytes%s\n", result.Context.Size, age)
	}

	// Inbox info
	if result.Inbox != nil {
		unreadInfo := ""
		if result.Inbox.Unread > 0 {
			unreadInfo = fmt.Sprintf(" (%d unread)", result.Inbox.Unread)
		}
		fmt.Fprintf(&output, "Inbox:    %d messages%s\n", result.Inbox.Total, unreadInfo)
	}

	// Sync status
	syncStatus := result.Health.SyncState
	if syncStatus == "synced" {
		output.WriteString("Sync:     ✓ synced\n")
	} else {
		fmt.Fprintf(&output, "Sync:     %s\n", syncStatus)
	}

	// Tailscale sync info
	if ts := result.Health.Tailscale; ts != nil && ts.Enabled {
		fmt.Fprintf(&output, "Tailscale: %s (%d peers)\n", ts.Hostname, ts.ConnectedPeers)
		for _, peer := range ts.Peers {
			fmt.Fprintf(&output, "  - %s (last sync: %s)\n", peer.Hostname, peer.LastSync)
		}
	}

	// Daemon info
	uptime := formatDuration(time.Duration(result.Health.UptimeMs) * time.Millisecond)
	fmt.Fprintf(&output, "Daemon:   running (%s uptime, v%s)\n", uptime, result.Health.Version)

	// WebSocket and UI
	if result.WebSocketPort > 0 {
		fmt.Fprintf(&output, "WebSocket: ws://localhost:%d/ws\n", result.WebSocketPort)
		fmt.Fprintf(&output, "UI:        http://localhost:%d\n", result.WebSocketPort)
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
