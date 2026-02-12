package mcp

// SendMessageInput is the input for the send_message MCP tool.
type SendMessageInput struct {
	To       string            `json:"to" jsonschema:"Recipient: @role name or agent name"`
	Content  string            `json:"content" jsonschema:"Message text"`
	Priority string            `json:"priority,omitempty" jsonschema:"Message priority: critical, high, normal, or low. Default: normal"`
	ThreadID string            `json:"thread_id,omitempty" jsonschema:"Thread ID to reply in"`
	Metadata map[string]string `json:"metadata,omitempty" jsonschema:"Optional key-value metadata"`
}

// SendMessageOutput is the output for the send_message MCP tool.
type SendMessageOutput struct {
	Status          string `json:"status" jsonschema:"Delivery status: delivered or queued"`
	MessageID       string `json:"message_id" jsonschema:"ID of the sent message"`
	RecipientStatus string `json:"recipient_status" jsonschema:"Recipient status: listening, queued, or unknown"`
}

// CheckMessagesInput is the input for the check_messages MCP tool.
// AgentID is intentionally omitted — the MCP server resolves identity at startup
// via config.LoadWithPath, so the client doesn't need to pass it.
type CheckMessagesInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Max messages to return. Default 50"`
}

// MessageInfo represents a single message returned by check_messages.
type MessageInfo struct {
	MessageID string `json:"message_id"`
	From      string `json:"from"`
	Content   string `json:"content"`
	Priority  string `json:"priority,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	Timestamp string `json:"timestamp"`
}

// CheckMessagesOutput is the output for the check_messages MCP tool.
type CheckMessagesOutput struct {
	Status    string        `json:"status" jsonschema:"Result status: messages or empty"`
	Messages  []MessageInfo `json:"messages" jsonschema:"List of messages"`
	Remaining int           `json:"remaining" jsonschema:"Number of remaining unread messages"`
}

// WaitForMessageInput is the input for the wait_for_message MCP tool.
// AgentID is intentionally omitted — the MCP server resolves identity at startup
// via config.LoadWithPath, so the client doesn't need to pass it.
type WaitForMessageInput struct {
	Timeout        int    `json:"timeout,omitempty" jsonschema:"Max seconds to wait. Default 300, max 600"`
	PriorityFilter string `json:"priority_filter,omitempty" jsonschema:"Filter: all, critical, high_and_above, or normal_and_above. Default: all"`
}

// WaitForMessageOutput is the output for the wait_for_message MCP tool.
type WaitForMessageOutput struct {
	Status        string       `json:"status" jsonschema:"Result: message_received or timeout"`
	Message       *MessageInfo `json:"message,omitempty" jsonschema:"The received message if any"`
	WaitedSeconds int          `json:"waited_seconds" jsonschema:"How long the wait lasted in seconds"`
}

// ListAgentsInput is the input for the list_agents MCP tool.
type ListAgentsInput struct {
	IncludeOffline *bool `json:"include_offline,omitempty" jsonschema:"Include inactive agents. Default true"`
}

// AgentInfo represents a single agent returned by list_agents.
type AgentInfo struct {
	Name       string `json:"name"`
	Role       string `json:"role"`
	Module     string `json:"module,omitempty"`
	Status     string `json:"status" jsonschema:"Agent status: active or offline"`
	LastSeenAt string `json:"last_seen_at,omitempty"`
}

// ListAgentsOutput is the output for the list_agents MCP tool.
type ListAgentsOutput struct {
	Agents []AgentInfo `json:"agents"`
	Count  int         `json:"count"`
}

// BroadcastFilter defines optional filters for broadcast_message.
type BroadcastFilter struct {
	Status  string   `json:"status,omitempty" jsonschema:"Filter by agent status: all or active"`
	Exclude []string `json:"exclude,omitempty" jsonschema:"Agent names to exclude from broadcast"`
}

// BroadcastInput is the input for the broadcast_message MCP tool.
// AgentID/From is intentionally omitted — the MCP server resolves identity at startup
// via config.LoadWithPath, so the client doesn't need to pass it.
type BroadcastInput struct {
	Content  string           `json:"content" jsonschema:"Message text to broadcast"`
	Priority string           `json:"priority,omitempty" jsonschema:"Message priority: critical, high, normal, or low. Default: normal"`
	Filter   *BroadcastFilter `json:"filter,omitempty" jsonschema:"Optional filters for recipient selection"`
}

// BroadcastOutput is the output for the broadcast_message MCP tool.
type BroadcastOutput struct {
	Status     string   `json:"status" jsonschema:"Result: sent, partial, or no_recipients"`
	SentTo     []string `json:"sent_to" jsonschema:"Names of agents that received the broadcast"`
	FailedTo   []string `json:"failed_to,omitempty" jsonschema:"Names of agents where send failed"`
	TotalSent  int      `json:"total_sent"`
	MessageIDs []string `json:"message_ids" jsonschema:"IDs of sent messages"`
}

// -- Group tool types --

// CreateGroupInput is the input for the create_group MCP tool.
type CreateGroupInput struct {
	Name        string `json:"name" jsonschema:"Group name (e.g. reviewers)"`
	Description string `json:"description,omitempty" jsonschema:"Optional group description"`
}

// CreateGroupOutput is the output for the create_group MCP tool.
type CreateGroupOutput struct {
	GroupID string `json:"group_id" jsonschema:"ID of the created group"`
	Name    string `json:"name" jsonschema:"Name of the created group"`
}

// DeleteGroupInput is the input for the delete_group MCP tool.
type DeleteGroupInput struct {
	Name string `json:"name" jsonschema:"Name of the group to delete"`
}

// DeleteGroupOutput is the output for the delete_group MCP tool.
type DeleteGroupOutput struct {
	Name   string `json:"name" jsonschema:"Name of the deleted group"`
	Status string `json:"status" jsonschema:"Result: deleted"`
}

// AddGroupMemberInput is the input for the add_group_member MCP tool.
type AddGroupMemberInput struct {
	Group       string `json:"group" jsonschema:"Group name to add the member to"`
	MemberType  string `json:"member_type" jsonschema:"Member type: agent, role, or group"`
	MemberValue string `json:"member_value" jsonschema:"Member value: agent name, role name, or group name"`
}

// AddGroupMemberOutput is the output for the add_group_member MCP tool.
type AddGroupMemberOutput struct {
	Group       string `json:"group"`
	MemberType  string `json:"member_type"`
	MemberValue string `json:"member_value"`
	Status      string `json:"status" jsonschema:"Result: added"`
}

// RemoveGroupMemberInput is the input for the remove_group_member MCP tool.
type RemoveGroupMemberInput struct {
	Group       string `json:"group" jsonschema:"Group name to remove the member from"`
	MemberType  string `json:"member_type" jsonschema:"Member type: agent, role, or group"`
	MemberValue string `json:"member_value" jsonschema:"Member value: agent name, role name, or group name"`
}

// RemoveGroupMemberOutput is the output for the remove_group_member MCP tool.
type RemoveGroupMemberOutput struct {
	Group       string `json:"group"`
	MemberType  string `json:"member_type"`
	MemberValue string `json:"member_value"`
	Status      string `json:"status" jsonschema:"Result: removed"`
}

// ListGroupsInput is the input for the list_groups MCP tool.
type ListGroupsInput struct{}

// GroupSummaryMCP represents a group summary in MCP output.
type GroupSummaryMCP struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MemberCount int    `json:"member_count"`
}

// ListGroupsOutput is the output for the list_groups MCP tool.
type ListGroupsOutput struct {
	Groups []GroupSummaryMCP `json:"groups"`
	Count  int               `json:"count"`
}

// GetGroupInput is the input for the get_group MCP tool.
type GetGroupInput struct {
	Name   string `json:"name" jsonschema:"Group name to look up"`
	Expand bool   `json:"expand,omitempty" jsonschema:"Resolve nested groups and roles to agent IDs"`
}

// GroupMemberMCP represents a group member in MCP output.
type GroupMemberMCP struct {
	MemberType  string `json:"member_type"`
	MemberValue string `json:"member_value"`
}

// GetGroupOutput is the output for the get_group MCP tool.
type GetGroupOutput struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Members     []GroupMemberMCP `json:"members"`
	Expanded    []string         `json:"expanded,omitempty" jsonschema:"Resolved agent IDs (when expand=true)"`
}
