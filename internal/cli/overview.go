package cli

import (
	"fmt"
	"strings"
	"time"
)

// OverviewResult contains the combined overview data.
type OverviewResult struct {
	Health      HealthResult       `json:"health"`
	Agent       *WhoamiResult      `json:"agent,omitempty"`
	WorkContext *AgentWorkContext  `json:"work_context,omitempty"`
	Team        []AgentWorkContext `json:"team,omitempty"`
	Inbox       *struct {
		Total  int `json:"total"`
		Unread int `json:"unread"`
	} `json:"inbox,omitempty"`
	WebSocketPort int `json:"websocket_port,omitempty"`
}

// Overview fetches combined overview data from the daemon.
func Overview(client *Client, callerAgentID ...string) (*OverviewResult, error) {
	result := &OverviewResult{}

	// Step 1: Health check
	if err := client.Call("health", map[string]any{}, &result.Health); err != nil {
		return nil, fmt.Errorf("failed to get health: %w", err)
	}

	// Step 2: Agent identity
	var whoami WhoamiResult
	whoamiParams := map[string]any{}
	if len(callerAgentID) > 0 && callerAgentID[0] != "" {
		whoamiParams["caller_agent_id"] = callerAgentID[0]
	}
	if err := client.Call("agent.whoami", whoamiParams, &whoami); err == nil {
		result.Agent = &whoami

		// Step 3: My work context (if session active)
		if whoami.SessionID != "" {
			var ctxResp ListContextResponse
			ctxReq := ListContextRequest{AgentID: whoami.AgentID}
			if err := client.Call("agent.listContext", ctxReq, &ctxResp); err == nil {
				if len(ctxResp.Contexts) > 0 {
					result.WorkContext = &ctxResp.Contexts[0]
				}
			}
		}

		// Step 4: Team contexts (all, will filter self in format)
		var allCtx ListContextResponse
		if err := client.Call("agent.listContext", ListContextRequest{}, &allCtx); err == nil {
			for i := range allCtx.Contexts {
				if allCtx.Contexts[i].AgentID != whoami.AgentID {
					result.Team = append(result.Team, allCtx.Contexts[i])
				}
			}
		}

		// Step 5: Inbox counts
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
	}

	return result, nil
}

// FormatOverview formats the overview result for display.
func FormatOverview(result *OverviewResult) string {
	var output strings.Builder

	// Identity section
	if result.Agent != nil {
		output.WriteString(fmt.Sprintf("You: @%s (%s)\n", result.Agent.Role, result.Agent.AgentID))

		// Session info
		if result.Agent.SessionID != "" {
			sessionAge := ""
			if result.Agent.SessionStart != "" {
				if t, err := time.Parse(time.RFC3339, result.Agent.SessionStart); err == nil {
					sessionAge = fmt.Sprintf(" for %s", formatDuration(time.Since(t)))
				}
			}
			output.WriteString(fmt.Sprintf("Session: active%s\n", sessionAge))
		} else {
			output.WriteString("Session: none\n")
		}

		// Work context
		if result.WorkContext != nil {
			ctx := result.WorkContext
			if ctx.Intent != "" {
				output.WriteString(fmt.Sprintf("Intent: %s\n", ctx.Intent))
			}
			if ctx.CurrentTask != "" {
				output.WriteString(fmt.Sprintf("Task: %s\n", ctx.CurrentTask))
			}
			if ctx.Branch != "" {
				branchInfo := ctx.Branch
				parts := []string{}
				if len(ctx.UnmergedCommits) > 0 {
					parts = append(parts, fmt.Sprintf("%d commits", len(ctx.UnmergedCommits)))
				}
				totalFiles := len(ctx.ChangedFiles) + len(ctx.UncommittedFiles)
				if totalFiles > 0 {
					parts = append(parts, fmt.Sprintf("%d files", totalFiles))
				}
				if len(parts) > 0 {
					branchInfo += " (" + strings.Join(parts, ", ") + ")"
				}
				output.WriteString(fmt.Sprintf("Branch: %s\n", branchInfo))
			}
		}
	} else {
		output.WriteString("Not registered (use 'thrum agent register')\n")
	}

	// Team section
	if len(result.Team) > 0 {
		output.WriteString("\nTeam:\n")

		// Calculate column widths
		termWidth := GetTerminalWidth()
		roleW := 12
		branchW := 20
		intentW := termWidth - roleW - branchW - 16 // 16 for padding/updated
		if intentW < 15 {
			intentW = 15
		}

		for _, ctx := range result.Team {
			role := extractRole(ctx.AgentID)
			if len(role) > roleW {
				role = role[:roleW-3] + "..."
			}

			branch := ctx.Branch
			if branch == "" {
				branch = "-"
			}
			if len(branch) > branchW {
				branch = branch[:branchW-3] + "..."
			}

			intent := ctx.Intent
			if intent == "" {
				intent = "-"
			}
			if len(intent) > intentW {
				intent = intent[:intentW-3] + "..."
			}

			updated := ""
			if ctx.GitUpdatedAt != "" {
				if t, err := time.Parse(time.RFC3339, ctx.GitUpdatedAt); err == nil {
					updated = formatTimeAgo(t)
				}
			}

			output.WriteString(fmt.Sprintf("  %-*s %-*s %-*s %s\n",
				roleW, role, branchW, branch, intentW, intent, updated))
		}
	}

	// Inbox
	if result.Inbox != nil {
		output.WriteString("\n")
		if result.Inbox.Unread > 0 {
			output.WriteString(fmt.Sprintf("Inbox: %d unread (%d total)\n", result.Inbox.Unread, result.Inbox.Total))
		} else if result.Inbox.Total > 0 {
			output.WriteString(fmt.Sprintf("Inbox: %d messages (all read)\n", result.Inbox.Total))
		} else {
			output.WriteString("Inbox: empty\n")
		}
	}

	// Sync status
	syncStatus := result.Health.SyncState
	if syncStatus == "synced" {
		output.WriteString("Sync: ✓ synced\n")
	} else if syncStatus != "" {
		output.WriteString(fmt.Sprintf("Sync: %s\n", syncStatus))
	}

	// WebSocket and UI
	if result.WebSocketPort > 0 {
		output.WriteString(fmt.Sprintf("WebSocket: ws://localhost:%d/ws\n", result.WebSocketPort))
		output.WriteString(fmt.Sprintf("UI: http://localhost:%d\n", result.WebSocketPort))
	}

	return output.String()
}
