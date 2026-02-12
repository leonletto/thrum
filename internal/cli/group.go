package cli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// -- Options structs --

// GroupCreateOptions contains options for creating a group.
type GroupCreateOptions struct {
	Name          string
	Description   string
	CallerAgentID string
}

// GroupDeleteOptions contains options for deleting a group.
type GroupDeleteOptions struct {
	Name          string
	CallerAgentID string
}

// GroupAddOptions contains options for adding a member to a group.
type GroupAddOptions struct {
	Group         string
	MemberType    string // "agent", "role", "group"
	MemberValue   string
	CallerAgentID string
}

// GroupRemoveOptions contains options for removing a member from a group.
type GroupRemoveOptions struct {
	Group         string
	MemberType    string
	MemberValue   string
	CallerAgentID string
}

// GroupListOptions contains options for listing groups.
type GroupListOptions struct{}

// GroupInfoOptions contains options for getting group info.
type GroupInfoOptions struct {
	Name string
}

// GroupMembersOptions contains options for listing group members.
type GroupMembersOptions struct {
	Name   string
	Expand bool
}

// -- Result structs --

// GroupCreateResult is the result of creating a group.
type GroupCreateResult struct {
	GroupID   string `json:"group_id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// GroupDeleteResult is the result of deleting a group.
type GroupDeleteResult struct {
	Name      string `json:"name"`
	DeletedAt string `json:"deleted_at"`
}

// GroupAddResult is the result of adding a member to a group.
type GroupAddResult struct {
	Group       string `json:"group"`
	MemberType  string `json:"member_type"`
	MemberValue string `json:"member_value"`
}

// GroupRemoveResult is the result of removing a member from a group.
type GroupRemoveResult struct {
	Group       string `json:"group"`
	MemberType  string `json:"member_type"`
	MemberValue string `json:"member_value"`
}

// GroupListResult is the result of listing groups.
type GroupListResult struct {
	Groups []GroupSummaryItem `json:"groups"`
}

// GroupSummaryItem represents a group in a list.
type GroupSummaryItem struct {
	GroupID     string `json:"group_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MemberCount int    `json:"member_count"`
	CreatedAt   string `json:"created_at"`
}

// GroupInfoResult is the result of getting group info.
type GroupInfoResult struct {
	GroupID     string            `json:"group_id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	CreatedAt   string            `json:"created_at"`
	CreatedBy   string            `json:"created_by"`
	Members     []GroupMemberItem `json:"members"`
}

// GroupMemberItem represents a member in a group.
type GroupMemberItem struct {
	MemberType  string `json:"member_type"`
	MemberValue string `json:"member_value"`
	AddedAt     string `json:"added_at"`
	AddedBy     string `json:"added_by,omitempty"`
}

// GroupMembersResult is the result of listing group members.
type GroupMembersResult struct {
	Members  []GroupMemberItem `json:"members"`
	Expanded []string          `json:"expanded,omitempty"`
}

// -- Handler functions --

// GroupCreate creates a new group via the daemon.
func GroupCreate(client *Client, opts GroupCreateOptions) (*GroupCreateResult, error) {
	params := map[string]any{
		"name": opts.Name,
	}
	if opts.Description != "" {
		params["description"] = opts.Description
	}
	if opts.CallerAgentID != "" {
		params["caller_agent_id"] = opts.CallerAgentID
	}

	var result GroupCreateResult
	if err := client.Call("group.create", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GroupDelete deletes a group via the daemon.
func GroupDelete(client *Client, opts GroupDeleteOptions) (*GroupDeleteResult, error) {
	params := map[string]any{
		"name": opts.Name,
	}
	if opts.CallerAgentID != "" {
		params["caller_agent_id"] = opts.CallerAgentID
	}

	var result GroupDeleteResult
	if err := client.Call("group.delete", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GroupAdd adds a member to a group via the daemon.
func GroupAdd(client *Client, opts GroupAddOptions) (*GroupAddResult, error) {
	params := map[string]any{
		"group":        opts.Group,
		"member_type":  opts.MemberType,
		"member_value": opts.MemberValue,
	}
	if opts.CallerAgentID != "" {
		params["caller_agent_id"] = opts.CallerAgentID
	}

	var result GroupAddResult
	if err := client.Call("group.member.add", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GroupRemove removes a member from a group via the daemon.
func GroupRemove(client *Client, opts GroupRemoveOptions) (*GroupRemoveResult, error) {
	params := map[string]any{
		"group":        opts.Group,
		"member_type":  opts.MemberType,
		"member_value": opts.MemberValue,
	}
	if opts.CallerAgentID != "" {
		params["caller_agent_id"] = opts.CallerAgentID
	}

	var result GroupRemoveResult
	if err := client.Call("group.member.remove", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GroupList lists all groups via the daemon.
func GroupList(client *Client, _ GroupListOptions) (*GroupListResult, error) {
	var result GroupListResult
	if err := client.Call("group.list", struct{}{}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GroupInfo gets detailed info about a group via the daemon.
func GroupInfo(client *Client, opts GroupInfoOptions) (*GroupInfoResult, error) {
	params := map[string]any{
		"name": opts.Name,
	}

	var result GroupInfoResult
	if err := client.Call("group.info", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GroupMembers lists members of a group via the daemon.
func GroupMembers(client *Client, opts GroupMembersOptions) (*GroupMembersResult, error) {
	params := map[string]any{
		"name":   opts.Name,
		"expand": opts.Expand,
	}

	var result GroupMembersResult
	if err := client.Call("group.members", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ResolveMemberType auto-detects member type from the value and flags.
// Returns (memberType, memberValue).
func ResolveMemberType(member string, roleFlag string) (string, string) {
	if roleFlag != "" {
		return "role", roleFlag
	}
	// Default: treat as agent, strip @ prefix
	value := strings.TrimPrefix(member, "@")
	return "agent", value
}

// FormatMemberDisplay formats a member for human-readable output.
func FormatMemberDisplay(memberType, memberValue string) string {
	switch memberType {
	case "agent":
		return fmt.Sprintf("@%s", memberValue)
	case "role":
		if memberValue == "*" {
			return "role:* (all agents)"
		}
		return fmt.Sprintf("role:%s", memberValue)
	case "group":
		return fmt.Sprintf("group:%s", memberValue)
	default:
		return fmt.Sprintf("%s:%s", memberType, memberValue)
	}
}

// FormatGroupList formats a group list as a human-readable table.
func FormatGroupList(groups []GroupSummaryItem) string {
	if len(groups) == 0 {
		return "No groups found."
	}

	var b strings.Builder
	for _, g := range groups {
		desc := g.Description
		if desc == "" {
			desc = "(no description)"
		}
		b.WriteString(fmt.Sprintf("  %-20s %-40s %d members\n", g.Name, desc, g.MemberCount))
	}
	return b.String()
}

// FormatGroupInfo formats group info as human-readable output.
func FormatGroupInfo(info *GroupInfoResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Group: %s\n", info.Name))
	if info.Description != "" {
		b.WriteString(fmt.Sprintf("Description: %s\n", info.Description))
	}
	b.WriteString(fmt.Sprintf("ID: %s\n", info.GroupID))
	b.WriteString(fmt.Sprintf("Created: %s by %s\n", info.CreatedAt, info.CreatedBy))
	b.WriteString(fmt.Sprintf("Members (%d):\n", len(info.Members)))
	for _, m := range info.Members {
		b.WriteString(fmt.Sprintf("  %s\n", FormatMemberDisplay(m.MemberType, m.MemberValue)))
	}
	return b.String()
}

// FormatGroupMembers formats group members as human-readable output.
func FormatGroupMembers(result *GroupMembersResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Members (%d):\n", len(result.Members)))
	for _, m := range result.Members {
		b.WriteString(fmt.Sprintf("  %s\n", FormatMemberDisplay(m.MemberType, m.MemberValue)))
	}
	if len(result.Expanded) > 0 {
		b.WriteString(fmt.Sprintf("\nExpanded (%d agents):\n", len(result.Expanded)))
		for _, a := range result.Expanded {
			b.WriteString(fmt.Sprintf("  @%s\n", a))
		}
	}
	return b.String()
}

// MarshalJSONIndent is a helper to marshal result structs as indented JSON.
func MarshalJSONIndent(v any) string {
	output, _ := json.MarshalIndent(v, "", "  ")
	return string(output)
}
