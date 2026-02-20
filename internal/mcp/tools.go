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
		Priority:      input.Priority,
		ReplyTo:       input.ReplyTo,
		CallerAgentID: s.agentID,
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
	messageIDs := make([]string, 0, len(listResp.Messages))
	for _, msg := range listResp.Messages {
		messages = append(messages, MessageInfo{
			MessageID: msg.MessageID,
			From:      msg.AgentID,
			Content:   msg.Body.Content,
			Priority:  msg.Priority,
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

	markReq := rpc.MarkReadRequest{
		MessageIDs:    messageIDs,
		CallerAgentID: s.agentID,
	}
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

	priority := input.Priority
	if priority == "" {
		priority = "normal"
	}
	if !isValidPriority(priority) {
		return nil, BroadcastOutput{}, fmt.Errorf("invalid priority %q: must be critical, high, normal, or low", priority)
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
		Priority:      priority,
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

// isValidPriority checks if a priority string is one of the allowed values.
func isValidPriority(p string) bool {
	switch p {
	case "critical", "high", "normal", "low":
		return true
	}
	return false
}

// parseMention extracts the recipient identifier from various addressing formats.
// - "@impl_api" → "impl_api" (agent name)
// - "@reviewer" → "reviewer" (role or name)
// - "agent:ops:abc123" → "ops" (legacy format extracts role)
// - "ops" → "ops" (bare string passthrough)
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

// -- Group tool handlers --

// handleCreateGroup creates a new messaging group.
func (s *Server) handleCreateGroup(
	ctx context.Context,
	req *gomcp.CallToolRequest,
	input CreateGroupInput,
) (*gomcp.CallToolResult, CreateGroupOutput, error) {
	if input.Name == "" {
		return nil, CreateGroupOutput{}, fmt.Errorf("'name' is required")
	}

	client, err := s.newDaemonClient()
	if err != nil {
		return nil, CreateGroupOutput{}, fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{"name": input.Name}
	if input.Description != "" {
		params["description"] = input.Description
	}

	var resp rpc.GroupCreateResponse
	if err := client.Call("group.create", params, &resp); err != nil {
		return nil, CreateGroupOutput{}, fmt.Errorf("create group: %w", err)
	}

	return nil, CreateGroupOutput{
		GroupID: resp.GroupID,
		Name:    resp.Name,
	}, nil
}

// handleDeleteGroup deletes a messaging group.
func (s *Server) handleDeleteGroup(
	ctx context.Context,
	req *gomcp.CallToolRequest,
	input DeleteGroupInput,
) (*gomcp.CallToolResult, DeleteGroupOutput, error) {
	if input.Name == "" {
		return nil, DeleteGroupOutput{}, fmt.Errorf("'name' is required")
	}

	client, err := s.newDaemonClient()
	if err != nil {
		return nil, DeleteGroupOutput{}, fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	var resp rpc.GroupDeleteResponse
	if err := client.Call("group.delete", map[string]any{"name": input.Name}, &resp); err != nil {
		return nil, DeleteGroupOutput{}, fmt.Errorf("delete group: %w", err)
	}

	return nil, DeleteGroupOutput{
		Name:   resp.Name,
		Status: "deleted",
	}, nil
}

// handleAddGroupMember adds a member to a group.
func (s *Server) handleAddGroupMember(
	ctx context.Context,
	req *gomcp.CallToolRequest,
	input AddGroupMemberInput,
) (*gomcp.CallToolResult, AddGroupMemberOutput, error) {
	if input.Group == "" {
		return nil, AddGroupMemberOutput{}, fmt.Errorf("'group' is required")
	}
	if input.MemberType == "" || input.MemberValue == "" {
		return nil, AddGroupMemberOutput{}, fmt.Errorf("'member_type' and 'member_value' are required")
	}

	client, err := s.newDaemonClient()
	if err != nil {
		return nil, AddGroupMemberOutput{}, fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	var resp rpc.GroupMemberAddResponse
	if err := client.Call("group.member.add", map[string]any{
		"group":        input.Group,
		"member_type":  input.MemberType,
		"member_value": input.MemberValue,
	}, &resp); err != nil {
		return nil, AddGroupMemberOutput{}, fmt.Errorf("add group member: %w", err)
	}

	return nil, AddGroupMemberOutput{
		Group:       resp.Group,
		MemberType:  resp.MemberType,
		MemberValue: resp.MemberValue,
		Status:      "added",
	}, nil
}

// handleRemoveGroupMember removes a member from a group.
func (s *Server) handleRemoveGroupMember(
	ctx context.Context,
	req *gomcp.CallToolRequest,
	input RemoveGroupMemberInput,
) (*gomcp.CallToolResult, RemoveGroupMemberOutput, error) {
	if input.Group == "" {
		return nil, RemoveGroupMemberOutput{}, fmt.Errorf("'group' is required")
	}
	if input.MemberType == "" || input.MemberValue == "" {
		return nil, RemoveGroupMemberOutput{}, fmt.Errorf("'member_type' and 'member_value' are required")
	}

	client, err := s.newDaemonClient()
	if err != nil {
		return nil, RemoveGroupMemberOutput{}, fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	var resp rpc.GroupMemberRemoveResponse
	if err := client.Call("group.member.remove", map[string]any{
		"group":        input.Group,
		"member_type":  input.MemberType,
		"member_value": input.MemberValue,
	}, &resp); err != nil {
		return nil, RemoveGroupMemberOutput{}, fmt.Errorf("remove group member: %w", err)
	}

	return nil, RemoveGroupMemberOutput{
		Group:       resp.Group,
		MemberType:  resp.MemberType,
		MemberValue: resp.MemberValue,
		Status:      "removed",
	}, nil
}

// handleListGroups lists all groups.
func (s *Server) handleListGroups(
	ctx context.Context,
	req *gomcp.CallToolRequest,
	input ListGroupsInput,
) (*gomcp.CallToolResult, ListGroupsOutput, error) {
	client, err := s.newDaemonClient()
	if err != nil {
		return nil, ListGroupsOutput{}, fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	var resp rpc.GroupListResponse
	if err := client.Call("group.list", struct{}{}, &resp); err != nil {
		return nil, ListGroupsOutput{}, fmt.Errorf("list groups: %w", err)
	}

	groups := make([]GroupSummaryMCP, 0, len(resp.Groups))
	for _, g := range resp.Groups {
		groups = append(groups, GroupSummaryMCP{
			Name:        g.Name,
			Description: g.Description,
			MemberCount: g.MemberCount,
		})
	}

	return nil, ListGroupsOutput{
		Groups: groups,
		Count:  len(groups),
	}, nil
}

// handleGetGroup gets detailed info about a group.
func (s *Server) handleGetGroup(
	ctx context.Context,
	req *gomcp.CallToolRequest,
	input GetGroupInput,
) (*gomcp.CallToolResult, GetGroupOutput, error) {
	if input.Name == "" {
		return nil, GetGroupOutput{}, fmt.Errorf("'name' is required")
	}

	client, err := s.newDaemonClient()
	if err != nil {
		return nil, GetGroupOutput{}, fmt.Errorf("connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Use group.members RPC which supports expand
	var resp rpc.GroupMembersResponse
	if err := client.Call("group.members", map[string]any{
		"name":   input.Name,
		"expand": input.Expand,
	}, &resp); err != nil {
		return nil, GetGroupOutput{}, fmt.Errorf("get group: %w", err)
	}

	// Also get group info for description
	var infoResp rpc.GroupInfoResponse
	if err := client.Call("group.info", map[string]any{"name": input.Name}, &infoResp); err != nil {
		return nil, GetGroupOutput{}, fmt.Errorf("get group info: %w", err)
	}

	members := make([]GroupMemberMCP, 0, len(resp.Members))
	for _, m := range resp.Members {
		members = append(members, GroupMemberMCP{
			MemberType:  m.MemberType,
			MemberValue: m.MemberValue,
		})
	}

	return nil, GetGroupOutput{
		Name:        infoResp.Name,
		Description: infoResp.Description,
		Members:     members,
		Expanded:    resp.Expanded,
	}, nil
}
