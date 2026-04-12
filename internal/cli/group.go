package cli

// Group CLI functions — only GroupList and GroupMembers remain.
// GroupCreate, GroupDelete, GroupAdd, GroupRemove, and formatting helpers
// removed with the group CLI commands. Telegram bridge and MCP waiter
// still use GroupList and GroupMembers via RPC.

// GroupListOptions contains options for listing groups.
type GroupListOptions struct{}

// GroupMembersOptions contains options for listing group members.
type GroupMembersOptions struct {
	Name   string
	Expand bool
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

// GroupList lists all groups via the daemon.
func GroupList(client *Client, _ GroupListOptions) (*GroupListResult, error) {
	var result GroupListResult
	if err := client.Call("group.list", struct{}{}, &result); err != nil {
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
