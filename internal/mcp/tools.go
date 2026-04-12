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

	// Parse the recipient into a mention
	mentionRole := parseMention(input.To)

	// Build the daemon RPC request
	sendReq := rpc.SendRequest{
		Content:       input.Content,
		Format:        "markdown",
		Mentions:      []string{mentionRole},
		ReplyTo:       input.ReplyTo,
		CallerAgentID: s.agentID,
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
		Status:     "delivered",
		MessageID:  sendResp.MessageID,
		ResolvedTo: sendResp.ResolvedTo,
		Warnings:   sendResp.Warnings,
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

	// Step 1: List unread messages for this agent (by ID and role)
	listReq := rpc.ListMessagesRequest{
		ForAgent:       s.agentID,
		ForAgentRole:   s.agentRole,
		CallerAgentID:  s.agentID,
		UnreadForAgent: s.agentID,
		ExcludeSelf:    true,
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
	for _, msg := range listResp.Messages {
		messages = append(messages, MessageInfo{
			MessageID: msg.MessageID,
			From:      msg.AgentID,
			Content:   msg.Body.Content,
			Timestamp: msg.CreatedAt,
		})
	}

	remaining := listResp.Total - len(messages)
	if remaining < 0 {
		remaining = 0
	}

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

	result, err := s.waiter.WaitForMessage(ctx, input.Timeout)
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

		name := a.Display
		if name == "" {
			name = a.AgentID
		}

		agents = append(agents, AgentInfo{
			Name:       name,
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

// handleBroadcast sends a message to all agents via the @everyone group.
// Deprecated: use send_message with to="@everyone" instead.
func (s *Server) handleBroadcast(
	ctx context.Context,
	req *gomcp.CallToolRequest,
	input BroadcastInput,
) (*gomcp.CallToolResult, BroadcastOutput, error) {
	if input.Content == "" {
		return nil, BroadcastOutput{}, fmt.Errorf("'content' is required")
	}

	// Map broadcast to send_message(to="@everyone")
	client, err := s.newDaemonClient()
	if err != nil {
		return nil, BroadcastOutput{}, fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	sendReq := rpc.SendRequest{
		Content:       input.Content,
		Format:        "markdown",
		Mentions:      []string{"everyone"},
		CallerAgentID: s.agentID,
	}

	var sendResp rpc.SendResponse
	if err := client.Call("message.send", sendReq, &sendResp); err != nil {
		return nil, BroadcastOutput{}, fmt.Errorf("send broadcast: %w", err)
	}

	return nil, BroadcastOutput{
		Status:     "sent",
		SentTo:     []string{"@everyone"},
		TotalSent:  1,
		MessageIDs: []string{sendResp.MessageID},
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

// parseMention extracts the recipient identifier from various addressing formats.
// - "@impl_api" → "impl_api" (agent name)
// - "@reviewer" → "reviewer" (role or name)
// - "agent:ops:abc123" → "ops" (legacy format extracts role)
// - "ops" → "ops" (bare string passthrough).
func parseMention(to string) string {
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


// Group tool handlers removed — groups are no longer user-facing MCP tools.
