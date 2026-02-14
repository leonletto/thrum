package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/gitctx"
	"github.com/leonletto/thrum/internal/identity"
)

// RegisterRequest represents the request for agent.register RPC.
type RegisterRequest struct {
	Name       string `json:"name,omitempty"`
	Role       string `json:"role"`
	Module     string `json:"module"`
	Display    string `json:"display,omitempty"`
	Force      bool   `json:"force,omitempty"`
	ReRegister bool   `json:"re_register,omitempty"`
}

// RegisterResponse represents the response from agent.register RPC.
type RegisterResponse struct {
	AgentID  string        `json:"agent_id"`
	Status   string        `json:"status"` // "registered", "conflict", "updated"
	Conflict *ConflictInfo `json:"conflict,omitempty"`
}

// ConflictInfo represents information about a registration conflict.
type ConflictInfo struct {
	ExistingAgentID string `json:"existing_agent_id"`
	RegisteredAt    string `json:"registered_at"`
	LastSeenAt      string `json:"last_seen_at"`
}

// AgentInfo represents information about a registered agent.
type AgentInfo struct {
	AgentID      string `json:"agent_id"`
	Kind         string `json:"kind"`
	Role         string `json:"role"`
	Module       string `json:"module"`
	Display      string `json:"display"`
	RegisteredAt string `json:"registered_at"`
	LastSeenAt   string `json:"last_seen_at,omitempty"`
}

// ListAgentsRequest represents the request for agent.list RPC.
type ListAgentsRequest struct {
	Role   string `json:"role,omitempty"`
	Module string `json:"module,omitempty"`
}

// ListAgentsResponse represents the response from agent.list RPC.
type ListAgentsResponse struct {
	Agents []AgentInfo `json:"agents"`
}

// AgentRegisterOptions contains options for agent registration.
type AgentRegisterOptions struct {
	Name       string
	Role       string
	Module     string
	Display    string
	Force      bool
	ReRegister bool
}

// AgentListOptions contains options for listing agents.
type AgentListOptions struct {
	Role   string
	Module string
}

// AgentDeleteOptions contains options for deleting an agent.
type AgentDeleteOptions struct {
	Name string
}

// DeleteAgentRequest represents the request for agent.delete RPC.
type DeleteAgentRequest struct {
	Name string `json:"name"`
}

// DeleteAgentResponse represents the response from agent.delete RPC.
type DeleteAgentResponse struct {
	AgentID string `json:"agent_id"`
	Deleted bool   `json:"deleted"`
	Message string `json:"message,omitempty"`
}

// AgentCleanupOptions contains options for cleaning up orphaned agents.
type AgentCleanupOptions struct {
	DryRun    bool
	Force     bool
	Threshold int // Days since last seen
}

// CleanupAgentRequest represents the request for agent.cleanup RPC.
type CleanupAgentRequest struct {
	DryRun    bool `json:"dry_run"`
	Force     bool `json:"force"`
	Threshold int  `json:"threshold"`
}

// OrphanedAgent represents an orphaned agent.
type OrphanedAgent struct {
	AgentID           string `json:"agent_id"`
	Role              string `json:"role"`
	Module            string `json:"module"`
	Worktree          string `json:"worktree"`
	Branch            string `json:"branch"`
	LastSeenAt        string `json:"last_seen_at"`
	WorktreeMissing   bool   `json:"worktree_missing"`
	BranchMissing     bool   `json:"branch_missing"`
	DaysSinceLastSeen int    `json:"days_since_last_seen"`
	MessageCount      int    `json:"message_count"`
}

// CleanupAgentResponse represents the response from agent.cleanup RPC.
type CleanupAgentResponse struct {
	Orphans []OrphanedAgent `json:"orphans"`
	Deleted []string        `json:"deleted"` // List of deleted agent IDs
	DryRun  bool            `json:"dry_run"`
	Message string          `json:"message,omitempty"`
}

// AgentRegister registers an agent with the daemon.
func AgentRegister(client *Client, opts AgentRegisterOptions) (*RegisterResponse, error) {
	req := RegisterRequest(opts)

	var result RegisterResponse
	if err := client.Call("agent.register", req, &result); err != nil {
		return nil, fmt.Errorf("agent.register RPC failed: %w", err)
	}

	return &result, nil
}

// AgentList retrieves the list of registered agents.
func AgentList(client *Client, opts AgentListOptions) (*ListAgentsResponse, error) {
	req := ListAgentsRequest(opts)

	var result ListAgentsResponse
	if err := client.Call("agent.list", req, &result); err != nil {
		return nil, fmt.Errorf("agent.list RPC failed: %w", err)
	}

	return &result, nil
}

// AgentDelete deletes an agent.
func AgentDelete(client *Client, opts AgentDeleteOptions) (*DeleteAgentResponse, error) {
	req := DeleteAgentRequest(opts)

	var result DeleteAgentResponse
	if err := client.Call("agent.delete", req, &result); err != nil {
		return nil, fmt.Errorf("agent.delete RPC failed: %w", err)
	}

	return &result, nil
}

// AgentCleanup performs cleanup of orphaned agents.
func AgentCleanup(client *Client, opts AgentCleanupOptions) (*CleanupAgentResponse, error) {
	req := CleanupAgentRequest(opts)

	var result CleanupAgentResponse
	if err := client.Call("agent.cleanup", req, &result); err != nil {
		return nil, fmt.Errorf("agent.cleanup RPC failed: %w", err)
	}

	return &result, nil
}

// AgentWhoami retrieves current agent identity.
func AgentWhoami(client *Client, callerAgentID ...string) (*WhoamiResult, error) {
	params := map[string]any{}
	if len(callerAgentID) > 0 && callerAgentID[0] != "" {
		params["caller_agent_id"] = callerAgentID[0]
	}
	var result WhoamiResult
	if err := client.Call("agent.whoami", params, &result); err != nil {
		return nil, fmt.Errorf("agent.whoami RPC failed: %w", err)
	}

	return &result, nil
}

// FormatRegisterResponse formats the agent registration response for display.
func FormatRegisterResponse(result *RegisterResponse) string {
	var output strings.Builder

	switch result.Status {
	case "registered":
		output.WriteString(fmt.Sprintf("✓ Agent registered: %s\n", result.AgentID))

	case "updated":
		output.WriteString(fmt.Sprintf("✓ Agent re-registered: %s\n", result.AgentID))

	case "conflict":
		if result.Conflict != nil {
			output.WriteString("✗ Registration conflict\n")
			output.WriteString("  Another agent already registered with the same role/module:\n")
			output.WriteString(fmt.Sprintf("  Agent ID:  %s\n", result.Conflict.ExistingAgentID))

			// Format timestamps
			if result.Conflict.RegisteredAt != "" {
				if t, err := time.Parse(time.RFC3339, result.Conflict.RegisteredAt); err == nil {
					output.WriteString(fmt.Sprintf("  Registered: %s\n", t.Format("2006-01-02 15:04:05")))
				}
			}
			if result.Conflict.LastSeenAt != "" {
				if t, err := time.Parse(time.RFC3339, result.Conflict.LastSeenAt); err == nil {
					output.WriteString(fmt.Sprintf("  Last seen:  %s\n", t.Format("2006-01-02 15:04:05")))
				}
			}

			output.WriteString("\n")
			output.WriteString("Use --force to override or --re-register if this is the same agent returning.\n")
		} else {
			output.WriteString("✗ Registration conflict (no details available)\n")
		}

	default:
		output.WriteString(fmt.Sprintf("Agent registration status: %s\n", result.Status))
	}

	return output.String()
}

// FormatAgentList formats the agent list response for display (basic view)
// Use FormatAgentListWithContext for enhanced view with session info.
func FormatAgentList(result *ListAgentsResponse) string {
	if len(result.Agents) == 0 {
		return "No agents registered.\n" + Hint("agent.list.empty", false, false)
	}

	var output strings.Builder

	output.WriteString(fmt.Sprintf("Registered agents (%d):\n\n", len(result.Agents)))

	for _, agent := range result.Agents {
		// Format agent ID with role highlighted
		output.WriteString(fmt.Sprintf("┌─ @%s (%s)\n", agent.Role, agent.AgentID))

		// Module
		if agent.Module != "" {
			output.WriteString(fmt.Sprintf("│  Module:     %s\n", agent.Module))
		}

		// Display name
		if agent.Display != "" {
			output.WriteString(fmt.Sprintf("│  Display:    %s\n", agent.Display))
		}

		// Kind
		if agent.Kind != "" {
			output.WriteString(fmt.Sprintf("│  Kind:       %s\n", agent.Kind))
		}

		// Registration time
		if agent.RegisteredAt != "" {
			if t, err := time.Parse(time.RFC3339, agent.RegisteredAt); err == nil {
				duration := time.Since(t)
				output.WriteString(fmt.Sprintf("│  Registered: %s (%s ago)\n",
					t.Format("2006-01-02 15:04:05"), formatDuration(duration)))
			} else {
				output.WriteString(fmt.Sprintf("│  Registered: %s\n", agent.RegisteredAt))
			}
		}

		// Last seen
		if agent.LastSeenAt != "" {
			if t, err := time.Parse(time.RFC3339, agent.LastSeenAt); err == nil {
				duration := time.Since(t)
				output.WriteString(fmt.Sprintf("│  Last seen:  %s (%s ago)\n",
					t.Format("2006-01-02 15:04:05"), formatDuration(duration)))
			} else {
				output.WriteString(fmt.Sprintf("│  Last seen:  %s\n", agent.LastSeenAt))
			}
		}

		output.WriteString("└─\n\n")
	}

	return output.String()
}

// FormatAgentDelete formats the agent delete response for display.
func FormatAgentDelete(result *DeleteAgentResponse) string {
	if result.Deleted {
		return fmt.Sprintf("✓ %s\n", result.Message)
	}
	return fmt.Sprintf("✗ Failed to delete agent: %s\n", result.Message)
}

// FormatAgentCleanup formats the agent cleanup response for display.
func FormatAgentCleanup(result *CleanupAgentResponse) string {
	var output strings.Builder

	if result.DryRun {
		output.WriteString("Orphaned agents (dry-run mode):\n\n")
	} else {
		output.WriteString("Agent cleanup results:\n\n")
	}

	if len(result.Orphans) == 0 {
		output.WriteString("No orphaned agents found.\n")
		return output.String()
	}

	for _, orphan := range result.Orphans {
		output.WriteString(fmt.Sprintf("Agent: %s (%s, module: %s)\n", orphan.AgentID, orphan.Role, orphan.Module))
		if orphan.Worktree != "" {
			status := "exists"
			if orphan.WorktreeMissing {
				status = "DELETED"
			}
			output.WriteString(fmt.Sprintf("  Worktree: %s [%s]\n", orphan.Worktree, status))
		}
		if orphan.Branch != "" {
			status := "exists"
			if orphan.BranchMissing {
				status = "DELETED"
			}
			output.WriteString(fmt.Sprintf("  Branch:   %s [%s]\n", orphan.Branch, status))
		}
		if orphan.LastSeenAt != "" {
			output.WriteString(fmt.Sprintf("  Last seen: %s (%d days ago)\n", orphan.LastSeenAt, orphan.DaysSinceLastSeen))
		}
		output.WriteString(fmt.Sprintf("  Messages: %d\n\n", orphan.MessageCount))
	}

	if !result.DryRun && len(result.Deleted) > 0 {
		output.WriteString(fmt.Sprintf("✓ Deleted %d orphaned agent(s): %s\n", len(result.Deleted), strings.Join(result.Deleted, ", ")))
	}

	if result.Message != "" {
		output.WriteString(fmt.Sprintf("\n%s\n", result.Message))
	}

	return output.String()
}

// FormatAgentListWithContext formats agent list with session and work context info.
func FormatAgentListWithContext(agents *ListAgentsResponse, contexts *ListContextResponse) string {
	if len(agents.Agents) == 0 {
		return "No agents registered.\n" + Hint("agent.list.empty", false, false)
	}

	var output strings.Builder

	// Build a map of agent_id -> work context for quick lookup
	contextMap := make(map[string]*AgentWorkContext)
	if contexts != nil {
		for i := range contexts.Contexts {
			ctx := &contexts.Contexts[i]
			contextMap[ctx.AgentID] = ctx
		}
	}

	output.WriteString(fmt.Sprintf("Registered agents (%d):\n\n", len(agents.Agents)))

	for _, agent := range agents.Agents {
		// Get work context for this agent (if any)
		ctx := contextMap[agent.AgentID]

		// Determine active/inactive state
		status := "●" // Active indicator
		statusText := "active"
		if ctx == nil || ctx.SessionID == "" {
			status = "○" // Inactive indicator
			statusText = "offline"
		}

		// Format agent ID with role and status
		output.WriteString(fmt.Sprintf("┌─ %s @%s (%s)\n", status, agent.Role, statusText))

		// Module
		if agent.Module != "" {
			output.WriteString(fmt.Sprintf("│  Module:  %s\n", agent.Module))
		}

		// Session info for active agents
		if ctx != nil && ctx.SessionID != "" {
			// Intent (truncated to 50 chars)
			if ctx.Intent != "" {
				intent := ctx.Intent
				if len(intent) > 50 {
					intent = intent[:47] + "..."
				}
				output.WriteString(fmt.Sprintf("│  Intent:  %s\n", intent))
			}

			// Task
			if ctx.CurrentTask != "" {
				output.WriteString(fmt.Sprintf("│  Task:    %s\n", ctx.CurrentTask))
			}

			// Branch info
			if ctx.Branch != "" {
				branchInfo := ctx.Branch
				if len(ctx.UnmergedCommits) > 0 {
					branchInfo += fmt.Sprintf(" (%d commits)", len(ctx.UnmergedCommits))
				}
				output.WriteString(fmt.Sprintf("│  Branch:  %s\n", branchInfo))
			}

			// Session duration
			if ctx.GitUpdatedAt != "" {
				if t, err := time.Parse(time.RFC3339, ctx.GitUpdatedAt); err == nil {
					output.WriteString(fmt.Sprintf("│  Active:  %s\n", formatTimeAgo(t)))
				}
			}
		} else {
			// Last seen for inactive agents
			if agent.LastSeenAt != "" {
				if t, err := time.Parse(time.RFC3339, agent.LastSeenAt); err == nil {
					output.WriteString(fmt.Sprintf("│  Last seen: %s\n", formatTimeAgo(t)))
				}
			}
		}

		output.WriteString("└─\n\n")
	}

	return output.String()
}

// FormatWhoami formats the whoami response for display.
func FormatWhoami(result *WhoamiResult) string {
	var output strings.Builder

	output.WriteString(fmt.Sprintf("Agent ID:  %s\n", result.AgentID))
	output.WriteString(fmt.Sprintf("Role:      @%s\n", result.Role))

	if result.Module != "" {
		output.WriteString(fmt.Sprintf("Module:    %s\n", result.Module))
	}

	if result.Display != "" {
		output.WriteString(fmt.Sprintf("Display:   %s\n", result.Display))
	}

	output.WriteString(fmt.Sprintf("Source:    %s\n", result.Source))

	if result.SessionID != "" {
		sessionAge := ""
		if result.SessionStart != "" {
			if t, err := time.Parse(time.RFC3339, result.SessionStart); err == nil {
				duration := time.Since(t)
				sessionAge = fmt.Sprintf(" (%s ago)", formatDuration(duration))
			}
		}
		output.WriteString(fmt.Sprintf("Session:   %s%s\n", result.SessionID, sessionAge))
	} else {
		output.WriteString("Session:   none (use 'thrum session start' to begin)\n")
	}

	return output.String()
}

// ListContextRequest represents the request for agent.listContext RPC.
type ListContextRequest struct {
	AgentID string `json:"agent_id,omitempty"`
	Branch  string `json:"branch,omitempty"`
	File    string `json:"file,omitempty"`
}

// ListContextResponse represents the response from agent.listContext RPC.
type ListContextResponse struct {
	Contexts []AgentWorkContext `json:"contexts"`
}

// AgentWorkContext represents an agent's work context.
type AgentWorkContext struct {
	SessionID        string              `json:"session_id"`
	AgentID          string              `json:"agent_id"`
	Branch           string              `json:"branch,omitempty"`
	WorktreePath     string              `json:"worktree_path,omitempty"`
	UnmergedCommits  []CommitSummary     `json:"unmerged_commits,omitempty"`
	UncommittedFiles []string            `json:"uncommitted_files,omitempty"`
	ChangedFiles     []string            `json:"changed_files,omitempty"` // Kept for backward compatibility
	FileChanges      []gitctx.FileChange `json:"file_changes,omitempty"`
	GitUpdatedAt     string              `json:"git_updated_at,omitempty"`
	CurrentTask      string              `json:"current_task,omitempty"`
	TaskUpdatedAt    string              `json:"task_updated_at,omitempty"`
	Intent           string              `json:"intent,omitempty"`
	IntentUpdatedAt  string              `json:"intent_updated_at,omitempty"`
}

// CommitSummary represents a single commit.
type CommitSummary struct {
	SHA     string   `json:"sha"`
	Message string   `json:"message"`
	Files   []string `json:"files,omitempty"`
}

// AgentListContext lists work contexts.
func AgentListContext(client *Client, agentID, branch, file string) (*ListContextResponse, error) {
	req := ListContextRequest{
		AgentID: agentID,
		Branch:  branch,
		File:    file,
	}

	var result ListContextResponse
	if err := client.Call("agent.listContext", req, &result); err != nil {
		return nil, fmt.Errorf("agent.listContext RPC failed: %w", err)
	}

	return &result, nil
}

// FormatContextList formats a list of work contexts for display.
func FormatContextList(result *ListContextResponse) string {
	if len(result.Contexts) == 0 {
		return "No active work contexts found.\n" + Hint("agent.context.empty", false, false)
	}

	var output strings.Builder

	// Get terminal width and calculate column widths proportionally
	termWidth := GetTerminalWidth()

	// Base widths for 120-char terminal (agent=15, session=12, branch=20, commits=8, files=6, intent=30, updated=10, spaces=7)
	// Scale proportionally if terminal is wider/narrower
	scale := float64(termWidth) / 120.0

	agentW := max(10, int(float64(15)*scale))
	sessionW := max(8, int(float64(12)*scale))
	branchW := max(10, int(float64(20)*scale))
	commitsW := 8 // Fixed width for numbers
	filesW := 6   // Fixed width for numbers
	intentW := max(15, int(float64(30)*scale))
	// updatedW is not needed - last column uses remaining space

	// Header
	headerFmt := fmt.Sprintf("%%-%ds %%-%ds %%-%ds %%%ds %%%ds %%-%ds %%s\n",
		agentW, sessionW, branchW, commitsW, filesW, intentW)
	output.WriteString(fmt.Sprintf(headerFmt,
		"AGENT", "SESSION", "BRANCH", "COMMITS", "FILES", "INTENT", "UPDATED"))
	output.WriteString(strings.Repeat("-", termWidth) + "\n")

	// Rows
	rowFmt := fmt.Sprintf("%%-%ds %%-%ds %%-%ds %%%dd %%%dd %%-%ds %%s\n",
		agentW, sessionW, branchW, commitsW, filesW, intentW)

	for _, ctx := range result.Contexts {
		// Extract role from agent_id (agent:role:module)
		role := extractRole(ctx.AgentID)
		if len(role) > agentW {
			role = role[:agentW-3] + "..."
		}

		// Truncate session ID
		sessionShort := ctx.SessionID
		if len(sessionShort) > sessionW {
			sessionShort = sessionShort[:sessionW]
		}

		// Branch name
		branch := ctx.Branch
		if branch == "" {
			branch = "-"
		}
		if len(branch) > branchW {
			branch = branch[:branchW-3] + "..."
		}

		// Commit and file counts
		commitCount := len(ctx.UnmergedCommits)
		fileCount := len(ctx.ChangedFiles) + len(ctx.UncommittedFiles)

		// Intent (truncated)
		intent := ctx.Intent
		if intent == "" {
			intent = "-"
		}
		if len(intent) > intentW {
			intent = intent[:intentW-3] + "..."
		}

		// Updated time
		updated := "-"
		if ctx.GitUpdatedAt != "" {
			if t, err := time.Parse(time.RFC3339, ctx.GitUpdatedAt); err == nil {
				updated = formatTimeAgo(t)
			}
		}

		output.WriteString(fmt.Sprintf(rowFmt,
			role, sessionShort, branch, commitCount, fileCount, intent, updated))
	}

	return output.String()
}

// FormatContextDetail formats a detailed work context for display.
func FormatContextDetail(ctx *AgentWorkContext) string {
	var output strings.Builder

	// Extract role from agent_id
	role := extractRole(ctx.AgentID)

	output.WriteString(fmt.Sprintf("Agent: %s (%s)\n", role, ctx.SessionID))

	if ctx.Branch != "" {
		output.WriteString(fmt.Sprintf("Branch: %s\n", ctx.Branch))
	}

	if ctx.WorktreePath != "" {
		output.WriteString(fmt.Sprintf("Worktree: %s\n", ctx.WorktreePath))
	}

	if ctx.Intent != "" {
		updatedAgo := ""
		if ctx.IntentUpdatedAt != "" {
			if t, err := time.Parse(time.RFC3339, ctx.IntentUpdatedAt); err == nil {
				updatedAgo = fmt.Sprintf(" (set %s)", formatTimeAgo(t))
			}
		}
		output.WriteString(fmt.Sprintf("Intent: %s%s\n", ctx.Intent, updatedAgo))
	}

	if ctx.CurrentTask != "" {
		updatedAgo := ""
		if ctx.TaskUpdatedAt != "" {
			if t, err := time.Parse(time.RFC3339, ctx.TaskUpdatedAt); err == nil {
				updatedAgo = fmt.Sprintf(" (set %s)", formatTimeAgo(t))
			}
		}
		output.WriteString(fmt.Sprintf("Task: %s%s\n", ctx.CurrentTask, updatedAgo))
	}

	// Unmerged commits
	if len(ctx.UnmergedCommits) > 0 {
		output.WriteString(fmt.Sprintf("\nUnmerged Commits (%d):\n", len(ctx.UnmergedCommits)))
		for _, commit := range ctx.UnmergedCommits {
			sha := commit.SHA
			if len(sha) > 7 {
				sha = sha[:7]
			}
			files := ""
			if len(commit.Files) > 0 {
				files = fmt.Sprintf(" [%s]", strings.Join(commit.Files, ", "))
			}
			output.WriteString(fmt.Sprintf("  %s %s%s\n", sha, commit.Message, files))
		}
	}

	// Changed files
	if len(ctx.ChangedFiles) > 0 {
		output.WriteString(fmt.Sprintf("\nChanged Files (vs main): %d\n", len(ctx.ChangedFiles)))
		for _, file := range ctx.ChangedFiles {
			output.WriteString(fmt.Sprintf("  %s\n", file))
		}
	}

	// Uncommitted files
	if len(ctx.UncommittedFiles) > 0 {
		output.WriteString(fmt.Sprintf("\nUncommitted: %d\n", len(ctx.UncommittedFiles)))
		for _, file := range ctx.UncommittedFiles {
			output.WriteString(fmt.Sprintf("  %s\n", file))
		}
	}

	return output.String()
}

// FormatWhoHas formats the who-has response showing agents touching a file.
func FormatWhoHas(file string, result *ListContextResponse) string {
	if len(result.Contexts) == 0 {
		return fmt.Sprintf("No agents are currently editing %s\n", file)
	}

	var output strings.Builder

	for _, ctx := range result.Contexts {
		role := extractRole(ctx.AgentID)
		branch := ctx.Branch
		if branch == "" {
			branch = "unknown"
		}

		// Try to find detailed file info from FileChanges
		var fileDetails string
		fileFound := false
		for _, fc := range ctx.FileChanges {
			if fc.Path == file {
				// Format: (+413 -187, modified 5m ago)
				timeAgo := formatTimeAgo(fc.LastModified)
				fileDetails = fmt.Sprintf(" (+%d -%d, %s %s)", fc.Additions, fc.Deletions, fc.Status, timeAgo)
				fileFound = true
				break
			}
		}

		// Fallback to legacy format if FileChanges not available
		if !fileFound {
			uncommitted := len(ctx.UncommittedFiles)
			fileDetails = fmt.Sprintf(" (%d uncommitted changes)", uncommitted)
		}

		output.WriteString(fmt.Sprintf("@%s is editing %s%s, branch: %s\n",
			role, file, fileDetails, branch))
	}

	return output.String()
}

// FormatPing formats the ping response showing agent presence.
func FormatPing(name string, agents *ListAgentsResponse, contexts *ListContextResponse) string {
	// Find the agent by name: check AgentID, Display, then fall back to Role
	var agent *AgentInfo
	for i := range agents.Agents {
		a := &agents.Agents[i]
		if a.AgentID == name || a.Display == name {
			agent = a
			break
		}
	}
	if agent == nil {
		for i := range agents.Agents {
			if agents.Agents[i].Role == name {
				agent = &agents.Agents[i]
				break
			}
		}
	}

	if agent == nil {
		return fmt.Sprintf("@%s: not found (no agent registered with this name or role)\n", name)
	}

	// Find context for this agent
	var ctx *AgentWorkContext
	if contexts != nil {
		for i := range contexts.Contexts {
			if contexts.Contexts[i].AgentID == agent.AgentID {
				ctx = &contexts.Contexts[i]
				break
			}
		}
	}

	var output strings.Builder

	if ctx != nil && ctx.SessionID != "" {
		// Active agent
		sessionDuration := ""
		if ctx.GitUpdatedAt != "" {
			if t, err := time.Parse(time.RFC3339, ctx.GitUpdatedAt); err == nil {
				sessionDuration = fmt.Sprintf(", last heartbeat %s", formatTimeAgo(t))
			}
		}
		output.WriteString(fmt.Sprintf("@%s: active%s\n", name, sessionDuration))

		if ctx.Intent != "" {
			output.WriteString(fmt.Sprintf("  Intent: %s\n", ctx.Intent))
		}
		if ctx.CurrentTask != "" {
			output.WriteString(fmt.Sprintf("  Task: %s\n", ctx.CurrentTask))
		}
		if ctx.Branch != "" {
			output.WriteString(fmt.Sprintf("  Branch: %s\n", ctx.Branch))
		}
	} else {
		// Offline agent
		lastSeen := ""
		if agent.LastSeenAt != "" {
			if t, err := time.Parse(time.RFC3339, agent.LastSeenAt); err == nil {
				lastSeen = fmt.Sprintf(" (last seen %s)", formatTimeAgo(t))
			}
		}
		output.WriteString(fmt.Sprintf("@%s: offline%s\n", name, lastSeen))
	}

	return output.String()
}

// extractRole extracts the role from an agent_id for display.
func extractRole(agentID string) string {
	return identity.ExtractDisplayName(agentID)
}

// formatTimeAgo formats a time as "Xm ago", "Xh ago", etc.
func formatTimeAgo(t time.Time) string {
	duration := time.Since(t)
	if duration < time.Minute {
		return "just now"
	} else if duration < time.Hour {
		return fmt.Sprintf("%dm ago", int(duration.Minutes()))
	} else if duration < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(duration.Hours()))
	} else {
		return fmt.Sprintf("%dd ago", int(duration.Hours()/24))
	}
}
