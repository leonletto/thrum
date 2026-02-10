package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/types"
)

// SessionStartRequest represents the request for session.start RPC.
type SessionStartRequest struct {
	AgentID string        `json:"agent_id"`
	Scopes  []types.Scope `json:"scopes,omitempty"`
	Refs    []types.Ref   `json:"refs,omitempty"`
}

// SessionStartResponse represents the response from session.start RPC.
type SessionStartResponse struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	StartedAt string `json:"started_at"`
}

// SessionEndRequest represents the request for session.end RPC.
type SessionEndRequest struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason,omitempty"`
}

// SessionEndResponse represents the response from session.end RPC.
type SessionEndResponse struct {
	SessionID string `json:"session_id"`
	EndedAt   string `json:"ended_at"`
	Duration  int64  `json:"duration_ms"`
}

// SessionStartOptions contains options for starting a session.
type SessionStartOptions struct {
	AgentID string
	Scopes  []types.Scope
	Refs    []types.Ref
}

// SessionEndOptions contains options for ending a session.
type SessionEndOptions struct {
	SessionID string
	Reason    string
}

// SessionStart starts a new session.
func SessionStart(client *Client, opts SessionStartOptions) (*SessionStartResponse, error) {
	req := SessionStartRequest(opts)

	var result SessionStartResponse
	if err := client.Call("session.start", req, &result); err != nil {
		return nil, fmt.Errorf("session.start RPC failed: %w", err)
	}

	return &result, nil
}

// SessionEnd ends a session.
func SessionEnd(client *Client, opts SessionEndOptions) (*SessionEndResponse, error) {
	req := SessionEndRequest(opts)

	var result SessionEndResponse
	if err := client.Call("session.end", req, &result); err != nil {
		return nil, fmt.Errorf("session.end RPC failed: %w", err)
	}

	return &result, nil
}

// FormatSessionStart formats the session start response for display.
func FormatSessionStart(result *SessionStartResponse) string {
	output := fmt.Sprintf("✓ Session started: %s\n", result.SessionID)
	output += fmt.Sprintf("  Agent:      %s\n", result.AgentID)

	// Format start time
	if result.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, result.StartedAt); err == nil {
			output += fmt.Sprintf("  Started:    %s\n", t.Format("2006-01-02 15:04:05"))
		} else {
			output += fmt.Sprintf("  Started:    %s\n", result.StartedAt)
		}
	}

	return output
}

// FormatSessionEnd formats the session end response for display.
func FormatSessionEnd(result *SessionEndResponse) string {
	output := fmt.Sprintf("✓ Session ended: %s\n", result.SessionID)

	// Format end time
	if result.EndedAt != "" {
		if t, err := time.Parse(time.RFC3339, result.EndedAt); err == nil {
			output += fmt.Sprintf("  Ended:      %s\n", t.Format("2006-01-02 15:04:05"))
		} else {
			output += fmt.Sprintf("  Ended:      %s\n", result.EndedAt)
		}
	}

	// Format duration
	duration := time.Duration(result.Duration) * time.Millisecond
	output += fmt.Sprintf("  Duration:   %s\n", formatDuration(duration))

	return output
}

// SetIntentRequest represents the request for session.setIntent RPC.
type SetIntentRequest struct {
	SessionID string `json:"session_id"`
	Intent    string `json:"intent"`
}

// SetIntentResponse represents the response from session.setIntent RPC.
type SetIntentResponse struct {
	SessionID       string `json:"session_id"`
	Intent          string `json:"intent"`
	IntentUpdatedAt string `json:"intent_updated_at"`
}

// SetTaskRequest represents the request for session.setTask RPC.
type SetTaskRequest struct {
	SessionID   string `json:"session_id"`
	CurrentTask string `json:"current_task"`
}

// SetTaskResponse represents the response from session.setTask RPC.
type SetTaskResponse struct {
	SessionID     string `json:"session_id"`
	CurrentTask   string `json:"current_task"`
	TaskUpdatedAt string `json:"task_updated_at"`
}

// SessionSetIntent sets the intent for the current session.
func SessionSetIntent(client *Client, sessionID, intent string) (*SetIntentResponse, error) {
	req := SetIntentRequest{
		SessionID: sessionID,
		Intent:    intent,
	}

	var result SetIntentResponse
	if err := client.Call("session.setIntent", req, &result); err != nil {
		return nil, fmt.Errorf("session.setIntent RPC failed: %w", err)
	}

	return &result, nil
}

// SessionSetTask sets the current task for the session.
func SessionSetTask(client *Client, sessionID, task string) (*SetTaskResponse, error) {
	req := SetTaskRequest{
		SessionID:   sessionID,
		CurrentTask: task,
	}

	var result SetTaskResponse
	if err := client.Call("session.setTask", req, &result); err != nil {
		return nil, fmt.Errorf("session.setTask RPC failed: %w", err)
	}

	return &result, nil
}

// FormatSetIntent formats the set intent response for display.
func FormatSetIntent(result *SetIntentResponse) string {
	if result.Intent == "" {
		return "✓ Intent cleared\n"
	}
	return fmt.Sprintf("✓ Intent set: %s\n", result.Intent)
}

// FormatSetTask formats the set task response for display.
func FormatSetTask(result *SetTaskResponse) string {
	if result.CurrentTask == "" {
		return "✓ Task cleared\n"
	}
	return fmt.Sprintf("✓ Task set: %s\n", result.CurrentTask)
}

// ListSessionsRequest represents the request for session.list RPC.
type ListSessionsRequest struct {
	AgentID    string `json:"agent_id,omitempty"`
	ActiveOnly bool   `json:"active_only,omitempty"`
}

// ListSessionsResponse represents the response from session.list RPC.
type ListSessionsResponse struct {
	Sessions []SessionSummary `json:"sessions"`
}

// SessionSummary represents a session in the list.
type SessionSummary struct {
	SessionID  string `json:"session_id"`
	AgentID    string `json:"agent_id"`
	StartedAt  string `json:"started_at"`
	EndedAt    string `json:"ended_at,omitempty"`
	EndReason  string `json:"end_reason,omitempty"`
	LastSeenAt string `json:"last_seen_at"`
	Intent     string `json:"intent,omitempty"`
	Status     string `json:"status"`
}

// SessionListOptions contains options for listing sessions.
type SessionListOptions struct {
	AgentID    string
	ActiveOnly bool
}

// SessionList lists sessions.
func SessionList(client *Client, opts SessionListOptions) (*ListSessionsResponse, error) {
	req := ListSessionsRequest(opts)

	var result ListSessionsResponse
	if err := client.Call("session.list", req, &result); err != nil {
		return nil, fmt.Errorf("session.list RPC failed: %w", err)
	}

	return &result, nil
}

// FormatSessionList formats the session list response for display.
func FormatSessionList(result *ListSessionsResponse) string {
	var output strings.Builder

	if len(result.Sessions) == 0 {
		output.WriteString("No sessions found.\n")
		return output.String()
	}

	fmt.Fprintf(&output, "Sessions (%d):\n", len(result.Sessions))

	for _, s := range result.Sessions {
		var statusStr string
		if s.Status == "active" {
			statusStr = "active"
		} else {
			statusStr = "ended"
		}

		fmt.Fprintf(&output, "\n  %s [%s]\n", s.SessionID, statusStr)
		fmt.Fprintf(&output, "    Agent:   %s\n", s.AgentID)

		if t, err := time.Parse(time.RFC3339Nano, s.StartedAt); err == nil {
			fmt.Fprintf(&output, "    Started: %s\n", t.Format("2006-01-02 15:04:05"))
		} else {
			fmt.Fprintf(&output, "    Started: %s\n", s.StartedAt)
		}

		if s.EndedAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, s.EndedAt); err == nil {
				fmt.Fprintf(&output, "    Ended:   %s\n", t.Format("2006-01-02 15:04:05"))
			} else {
				fmt.Fprintf(&output, "    Ended:   %s\n", s.EndedAt)
			}
		}

		if s.Intent != "" {
			fmt.Fprintf(&output, "    Intent:  %s\n", s.Intent)
		}

		if s.EndReason != "" && s.EndReason != "normal" {
			fmt.Fprintf(&output, "    Reason:  %s\n", s.EndReason)
		}
	}

	return output.String()
}

// HeartbeatRequest represents the request for session.heartbeat RPC.
type HeartbeatRequest struct {
	SessionID    string        `json:"session_id"`
	AddScopes    []types.Scope `json:"add_scopes,omitempty"`
	RemoveScopes []types.Scope `json:"remove_scopes,omitempty"`
	AddRefs      []types.Ref   `json:"add_refs,omitempty"`
	RemoveRefs   []types.Ref   `json:"remove_refs,omitempty"`
}

// HeartbeatResponse represents the response from session.heartbeat RPC.
type HeartbeatResponse struct {
	SessionID  string `json:"session_id"`
	LastSeenAt string `json:"last_seen_at"`
}

// HeartbeatOptions contains options for sending a heartbeat.
type HeartbeatOptions struct {
	SessionID    string
	AddScopes    []types.Scope
	RemoveScopes []types.Scope
	AddRefs      []types.Ref
	RemoveRefs   []types.Ref
}

// SessionHeartbeat sends a heartbeat for the session.
func SessionHeartbeat(client *Client, opts HeartbeatOptions) (*HeartbeatResponse, error) {
	req := HeartbeatRequest(opts)

	var result HeartbeatResponse
	if err := client.Call("session.heartbeat", req, &result); err != nil {
		return nil, fmt.Errorf("session.heartbeat RPC failed: %w", err)
	}

	return &result, nil
}

// FormatHeartbeat formats the heartbeat response for display.
func FormatHeartbeat(result *HeartbeatResponse, context *AgentWorkContext) string {
	var output strings.Builder

	fmt.Fprintf(&output, "✓ Heartbeat sent: %s\n", result.SessionID)

	if context != nil {
		// Show git context summary
		parts := []string{}
		if context.Branch != "" {
			parts = append(parts, fmt.Sprintf("branch: %s", context.Branch))
		}
		if len(context.UnmergedCommits) > 0 {
			parts = append(parts, fmt.Sprintf("%d commits", len(context.UnmergedCommits)))
		}
		totalFiles := len(context.ChangedFiles) + len(context.UncommittedFiles)
		if totalFiles > 0 {
			parts = append(parts, fmt.Sprintf("%d files", totalFiles))
		}
		if len(parts) > 0 {
			fmt.Fprintf(&output, "  Context: %s\n", strings.Join(parts, ", "))
		}
	}

	return output.String()
}
