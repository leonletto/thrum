package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/leonletto/thrum/internal/daemon/rpc"
)

// handleSendMessage sends a message to another agent via the daemon RPC.
func (s *Server) handleSendMessage(
	ctx context.Context,
	req *gomcp.CallToolRequest,
	input SendMessageInput,
) (*gomcp.CallToolResult, SendMessageOutput, error) {
	if input.To == "" {
		return nil, SendMessageOutput{}, fmt.Errorf("'to' is required")
	}
	if input.Content == "" {
		return nil, SendMessageOutput{}, fmt.Errorf("'content' is required")
	}

	// Parse the recipient into a mention role
	mentionRole := parseMentionRole(input.To)

	// Build the daemon RPC request
	sendReq := rpc.SendRequest{
		Content:  input.Content,
		Format:   "markdown",
		Mentions: []string{mentionRole},
		Priority: input.Priority,
		ThreadID: input.ThreadID,
	}
	if sendReq.Priority == "" {
		sendReq.Priority = "normal"
	}
	if !isValidPriority(sendReq.Priority) {
		return nil, SendMessageOutput{}, fmt.Errorf("invalid priority %q: must be critical, high, normal, or low", sendReq.Priority)
	}

	// Add metadata as structured data if provided
	if len(input.Metadata) > 0 {
		sendReq.Structured = make(map[string]any, len(input.Metadata))
		for k, v := range input.Metadata {
			sendReq.Structured[k] = v
		}
	}

	// Per-call client (cli.Client is not concurrent-safe)
	client, err := s.newDaemonClient()
	if err != nil {
		return nil, SendMessageOutput{}, fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	var sendResp rpc.SendResponse
	if err := client.Call("message.send", sendReq, &sendResp); err != nil {
		return nil, SendMessageOutput{}, fmt.Errorf("send message: %w", err)
	}

	return nil, SendMessageOutput{
		Status:          "delivered",
		MessageID:       sendResp.MessageID,
		RecipientStatus: "unknown", // would need agent.list to determine
	}, nil
}

// handleCheckMessages checks for new messages addressed to this agent.
func (s *Server) handleCheckMessages(
	ctx context.Context,
	req *gomcp.CallToolRequest,
	input CheckMessagesInput,
) (*gomcp.CallToolResult, CheckMessagesOutput, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}

	// Step 1: List unread messages mentioning this agent's role
	listReq := rpc.ListMessagesRequest{
		MentionRole:    s.agentRole,
		UnreadForAgent: s.agentID,
		PageSize:       limit,
		SortBy:         "created_at",
		SortOrder:      "asc",
	}

	client, err := s.newDaemonClient()
	if err != nil {
		return nil, CheckMessagesOutput{}, fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	var listResp rpc.ListMessagesResponse
	if err := client.Call("message.list", listReq, &listResp); err != nil {
		return nil, CheckMessagesOutput{}, fmt.Errorf("list messages: %w", err)
	}

	if len(listResp.Messages) == 0 {
		return nil, CheckMessagesOutput{
			Status:   "empty",
			Messages: []MessageInfo{},
		}, nil
	}

	// Convert to MCP message format
	messages := make([]MessageInfo, 0, len(listResp.Messages))
	messageIDs := make([]string, 0, len(listResp.Messages))
	for _, msg := range listResp.Messages {
		messages = append(messages, MessageInfo{
			MessageID: msg.MessageID,
			From:      msg.AgentID,
			Content:   msg.Body.Content,
			ThreadID:  msg.ThreadID,
			Timestamp: msg.CreatedAt,
		})
		messageIDs = append(messageIDs, msg.MessageID)
	}

	// Step 2: Mark messages as read (use a new client per-call)
	remaining := listResp.Total - len(messages)
	if remaining < 0 {
		remaining = 0
	}

	markClient, err := s.newDaemonClient()
	if err != nil {
		// Return messages even if we can't mark them read
		return nil, CheckMessagesOutput{
			Status:    "messages",
			Messages:  messages,
			Remaining: remaining,
		}, nil
	}
	defer func() { _ = markClient.Close() }()

	markReq := rpc.MarkReadRequest{MessageIDs: messageIDs}
	var markResp rpc.MarkReadResponse
	_ = markClient.Call("message.markRead", markReq, &markResp) // best-effort

	return nil, CheckMessagesOutput{
		Status:    "messages",
		Messages:  messages,
		Remaining: remaining,
	}, nil
}

// handleWaitForMessage blocks until a message arrives or timeout expires.
func (s *Server) handleWaitForMessage(
	ctx context.Context,
	req *gomcp.CallToolRequest,
	input WaitForMessageInput,
) (*gomcp.CallToolResult, WaitForMessageOutput, error) {
	if s.waiter == nil {
		return nil, WaitForMessageOutput{}, fmt.Errorf("wait_for_message requires WebSocket connection to daemon; waiter not initialized")
	}

	result, err := s.waiter.WaitForMessage(ctx, input.Timeout, input.PriorityFilter)
	if err != nil {
		return nil, WaitForMessageOutput{}, err
	}
	return nil, *result, nil
}

// handleListAgents lists all registered agents and their status.
func (s *Server) handleListAgents(
	ctx context.Context,
	req *gomcp.CallToolRequest,
	input ListAgentsInput,
) (*gomcp.CallToolResult, ListAgentsOutput, error) {
	client, err := s.newDaemonClient()
	if err != nil {
		return nil, ListAgentsOutput{}, fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	var listResp rpc.ListAgentsResponse
	if err := client.Call("agent.list", rpc.ListAgentsRequest{}, &listResp); err != nil {
		return nil, ListAgentsOutput{}, fmt.Errorf("list agents: %w", err)
	}

	// Default IncludeOffline to true when not specified (nil)
	includeOffline := true
	if input.IncludeOffline != nil {
		includeOffline = *input.IncludeOffline
	}

	agents := make([]AgentInfo, 0, len(listResp.Agents))
	now := time.Now()
	for _, a := range listResp.Agents {
		status := deriveAgentStatus(a.LastSeenAt, now)

		// Filter offline agents if requested
		if !includeOffline && status == "offline" {
			continue
		}

		agents = append(agents, AgentInfo{
			Name:       a.Display,
			Role:       a.Role,
			Module:     a.Module,
			Status:     status,
			LastSeenAt: a.LastSeenAt,
		})
	}

	return nil, ListAgentsOutput{
		Agents: agents,
		Count:  len(agents),
	}, nil
}

// handleBroadcast sends a message to all active agents.
func (s *Server) handleBroadcast(
	ctx context.Context,
	req *gomcp.CallToolRequest,
	input BroadcastInput,
) (*gomcp.CallToolResult, BroadcastOutput, error) {
	if input.Content == "" {
		return nil, BroadcastOutput{}, fmt.Errorf("'content' is required")
	}

	// Get agent list
	client, err := s.newDaemonClient()
	if err != nil {
		return nil, BroadcastOutput{}, fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	var listResp rpc.ListAgentsResponse
	if err := client.Call("agent.list", rpc.ListAgentsRequest{}, &listResp); err != nil {
		return nil, BroadcastOutput{}, fmt.Errorf("list agents: %w", err)
	}

	// Build exclude set from user-provided names/roles
	excludeSet := make(map[string]bool)
	if input.Filter != nil {
		for _, name := range input.Filter.Exclude {
			excludeSet[name] = true
		}
	}

	now := time.Now()
	priority := input.Priority
	if priority == "" {
		priority = "normal"
	}
	if !isValidPriority(priority) {
		return nil, BroadcastOutput{}, fmt.Errorf("invalid priority %q: must be critical, high, normal, or low", priority)
	}

	var sentTo, failedTo []string
	var messageIDs []string

	for _, a := range listResp.Agents {
		// Skip self by agent ID (precise, won't exclude other agents with same role)
		if a.AgentID == s.agentID {
			continue
		}
		// Check user-provided exclude list against role and display name
		if excludeSet[a.Role] || excludeSet[a.Display] {
			continue
		}

		// Filter by status if requested
		if input.Filter != nil && input.Filter.Status != "" && input.Filter.Status != "all" {
			status := deriveAgentStatus(a.LastSeenAt, now)
			if input.Filter.Status == "active" && status != "active" {
				continue
			}
		}

		// Send message to this agent
		sendClient, err := s.newDaemonClient()
		if err != nil {
			failedTo = append(failedTo, a.Role)
			continue
		}

		sendReq := rpc.SendRequest{
			Content:  input.Content,
			Format:   "markdown",
			Mentions: []string{a.Role},
			Priority: priority,
		}
		var sendResp rpc.SendResponse
		if err := sendClient.Call("message.send", sendReq, &sendResp); err != nil {
			failedTo = append(failedTo, a.Role)
			_ = sendClient.Close()
			continue
		}
		_ = sendClient.Close()

		sentTo = append(sentTo, a.Role)
		messageIDs = append(messageIDs, sendResp.MessageID)
	}

	status := "sent"
	if len(sentTo) == 0 {
		status = "no_recipients"
	} else if len(failedTo) > 0 {
		status = "partial"
	}

	return nil, BroadcastOutput{
		Status:     status,
		SentTo:     sentTo,
		FailedTo:   failedTo,
		TotalSent:  len(sentTo),
		MessageIDs: messageIDs,
	}, nil
}

// deriveAgentStatus derives the agent status from last_seen_at timestamp.
func deriveAgentStatus(lastSeenAt string, now time.Time) string {
	if lastSeenAt == "" {
		return "offline"
	}
	lastSeen, err := time.Parse(time.RFC3339Nano, lastSeenAt)
	if err != nil {
		lastSeen, err = time.Parse(time.RFC3339, lastSeenAt)
		if err != nil {
			return "offline"
		}
	}
	elapsed := now.Sub(lastSeen)
	if elapsed < 2*time.Minute {
		return "active"
	}
	return "offline"
}

// isValidPriority checks if a priority string is one of the allowed values.
func isValidPriority(p string) bool {
	switch p {
	case "critical", "high", "normal", "low":
		return true
	}
	return false
}

// parseMentionRole extracts the role from various addressing formats.
// - "@ops" → "ops"
// - "agent:ops:abc123" → "ops"
// - "ops" → "ops".
func parseMentionRole(to string) string {
	// Strip @ prefix
	if strings.HasPrefix(to, "@") {
		return to[1:]
	}
	// Extract role from composite agent ID format "agent:role:hash"
	if strings.HasPrefix(to, "agent:") {
		parts := strings.SplitN(to, ":", 3)
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	return to
}
