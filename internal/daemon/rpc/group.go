package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/groups"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/types"
)

// GroupHandler handles group-related RPC methods.
type GroupHandler struct {
	state    *state.State
	resolver *groups.Resolver
}

// NewGroupHandler creates a new group handler.
func NewGroupHandler(st *state.State) *GroupHandler {
	return &GroupHandler{
		state:    st,
		resolver: groups.NewResolver(st.RawDB()),
	}
}

// -- Request/Response types --

// GroupCreateRequest is the request for group.create RPC.
type GroupCreateRequest struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	CallerAgentID string `json:"caller_agent_id,omitempty"`
}

// GroupCreateResponse is the response from group.create RPC.
type GroupCreateResponse struct {
	GroupID   string `json:"group_id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// GroupDeleteRequest is the request for group.delete RPC.
type GroupDeleteRequest struct {
	Name          string `json:"name"`
	CallerAgentID string `json:"caller_agent_id,omitempty"`
}

// GroupDeleteResponse is the response from group.delete RPC.
type GroupDeleteResponse struct {
	Name      string `json:"name"`
	DeletedAt string `json:"deleted_at"`
}

// GroupMemberAddRequest is the request for group.member.add RPC.
type GroupMemberAddRequest struct {
	Group         string `json:"group"`
	MemberType    string `json:"member_type"` // "agent", "role"
	MemberValue   string `json:"member_value"`
	CallerAgentID string `json:"caller_agent_id,omitempty"`
}

// GroupMemberAddResponse is the response from group.member.add RPC.
type GroupMemberAddResponse struct {
	Group       string `json:"group"`
	MemberType  string `json:"member_type"`
	MemberValue string `json:"member_value"`
}

// GroupMemberRemoveRequest is the request for group.member.remove RPC.
type GroupMemberRemoveRequest struct {
	Group         string `json:"group"`
	MemberType    string `json:"member_type"`
	MemberValue   string `json:"member_value"`
	CallerAgentID string `json:"caller_agent_id,omitempty"`
}

// GroupMemberRemoveResponse is the response from group.member.remove RPC.
type GroupMemberRemoveResponse struct {
	Group       string `json:"group"`
	MemberType  string `json:"member_type"`
	MemberValue string `json:"member_value"`
}

// GroupListRequest is the request for group.list RPC.
type GroupListRequest struct{}

// GroupSummary represents a group in a list response.
type GroupSummary struct {
	GroupID     string `json:"group_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MemberCount int    `json:"member_count"`
	CreatedAt   string `json:"created_at"`
}

// GroupListResponse is the response from group.list RPC.
type GroupListResponse struct {
	Groups []GroupSummary `json:"groups"`
}

// GroupInfoRequest is the request for group.info RPC.
type GroupInfoRequest struct {
	Name string `json:"name"`
}

// GroupInfoResponse is the response from group.info RPC.
type GroupInfoResponse struct {
	GroupID     string        `json:"group_id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	CreatedAt   string        `json:"created_at"`
	CreatedBy   string        `json:"created_by"`
	Members     []GroupMember `json:"members"`
}

// GroupMember represents a member of a group.
type GroupMember struct {
	MemberType  string `json:"member_type"`
	MemberValue string `json:"member_value"`
	AddedAt     string `json:"added_at"`
	AddedBy     string `json:"added_by,omitempty"`
}

// GroupMembersRequest is the request for group.members RPC.
type GroupMembersRequest struct {
	Name   string `json:"name"`
	Expand bool   `json:"expand"`
}

// GroupMembersResponse is the response from group.members RPC.
type GroupMembersResponse struct {
	Members  []GroupMember `json:"members"`
	Expanded []string      `json:"expanded,omitempty"`
}

// -- Handlers --

// HandleCreate handles the group.create RPC method.
func (h *GroupHandler) HandleCreate(ctx context.Context, params json.RawMessage) (any, error) {
	var req GroupCreateRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	// Check if group already exists
	h.state.RLock()
	exists, err := h.resolver.IsGroup(req.Name)
	h.state.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("check group exists: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("group %q already exists", req.Name)
	}

	groupID := identity.GenerateGroupID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	createdBy := req.CallerAgentID
	if createdBy == "" {
		createdBy = "system"
	}

	event := types.GroupCreateEvent{
		Type:        "group.create",
		Timestamp:   now,
		GroupID:     groupID,
		Name:        req.Name,
		Description: req.Description,
		CreatedBy:   createdBy,
	}

	h.state.Lock()
	defer h.state.Unlock()

	if err := h.state.WriteEvent(ctx, event); err != nil {
		return nil, fmt.Errorf("write group.create event: %w", err)
	}

	return &GroupCreateResponse{
		GroupID:   groupID,
		Name:      req.Name,
		CreatedAt: now,
	}, nil
}

// HandleDelete handles the group.delete RPC method.
func (h *GroupHandler) HandleDelete(ctx context.Context, params json.RawMessage) (any, error) {
	var req GroupDeleteRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	// Prevent deleting @everyone
	if req.Name == "everyone" {
		return nil, fmt.Errorf("cannot delete built-in @everyone group")
	}

	// Look up group_id
	h.state.RLock()
	var groupID string
	err := h.state.DB().QueryRowContext(ctx, "SELECT group_id FROM groups WHERE name = ?", req.Name).Scan(&groupID)
	h.state.RUnlock()

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("group %q not found", req.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("query group: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	deletedBy := req.CallerAgentID
	if deletedBy == "" {
		deletedBy = "system"
	}

	event := types.GroupDeleteEvent{
		Type:      "group.delete",
		Timestamp: now,
		GroupID:   groupID,
		DeletedBy: deletedBy,
	}

	h.state.Lock()
	defer h.state.Unlock()

	if err := h.state.WriteEvent(ctx, event); err != nil {
		return nil, fmt.Errorf("write group.delete event: %w", err)
	}

	return &GroupDeleteResponse{
		Name:      req.Name,
		DeletedAt: now,
	}, nil
}

// HandleMemberAdd handles the group.member.add RPC method.
func (h *GroupHandler) HandleMemberAdd(ctx context.Context, params json.RawMessage) (any, error) {
	var req GroupMemberAddRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.Group == "" {
		return nil, fmt.Errorf("group is required")
	}
	if req.MemberType == "" || req.MemberValue == "" {
		return nil, fmt.Errorf("member_type and member_value are required")
	}

	// Validate member_type (only agent and role are allowed)
	if req.MemberType != "agent" && req.MemberType != "role" {
		return nil, fmt.Errorf("invalid member_type %q (must be 'agent' or 'role')", req.MemberType)
	}

	// Prevent adding members to @everyone (protected)
	if req.Group == "everyone" {
		return nil, fmt.Errorf("cannot modify members of built-in @everyone group")
	}

	// Look up group_id
	h.state.RLock()
	var groupID string
	err := h.state.DB().QueryRowContext(ctx, "SELECT group_id FROM groups WHERE name = ?", req.Group).Scan(&groupID)
	h.state.RUnlock()

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("group %q not found", req.Group)
	}
	if err != nil {
		return nil, fmt.Errorf("query group: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	addedBy := req.CallerAgentID
	if addedBy == "" {
		addedBy = "system"
	}

	event := types.GroupMemberAddEvent{
		Type:        "group.member.add",
		Timestamp:   now,
		GroupID:     groupID,
		MemberType:  req.MemberType,
		MemberValue: req.MemberValue,
		AddedBy:     addedBy,
	}

	h.state.Lock()
	defer h.state.Unlock()

	if err := h.state.WriteEvent(ctx, event); err != nil {
		return nil, fmt.Errorf("write group.member.add event: %w", err)
	}

	return &GroupMemberAddResponse{
		Group:       req.Group,
		MemberType:  req.MemberType,
		MemberValue: req.MemberValue,
	}, nil
}

// HandleMemberRemove handles the group.member.remove RPC method.
func (h *GroupHandler) HandleMemberRemove(ctx context.Context, params json.RawMessage) (any, error) {
	var req GroupMemberRemoveRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.Group == "" {
		return nil, fmt.Errorf("group is required")
	}
	if req.MemberType == "" || req.MemberValue == "" {
		return nil, fmt.Errorf("member_type and member_value are required")
	}

	// Prevent modifying @everyone
	if req.Group == "everyone" {
		return nil, fmt.Errorf("cannot modify members of built-in @everyone group")
	}

	// Look up group_id
	h.state.RLock()
	var groupID string
	err := h.state.DB().QueryRowContext(ctx, "SELECT group_id FROM groups WHERE name = ?", req.Group).Scan(&groupID)
	h.state.RUnlock()

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("group %q not found", req.Group)
	}
	if err != nil {
		return nil, fmt.Errorf("query group: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	removedBy := req.CallerAgentID
	if removedBy == "" {
		removedBy = "system"
	}

	event := types.GroupMemberRemoveEvent{
		Type:        "group.member.remove",
		Timestamp:   now,
		GroupID:     groupID,
		MemberType:  req.MemberType,
		MemberValue: req.MemberValue,
		RemovedBy:   removedBy,
	}

	h.state.Lock()
	defer h.state.Unlock()

	if err := h.state.WriteEvent(ctx, event); err != nil {
		return nil, fmt.Errorf("write group.member.remove event: %w", err)
	}

	return &GroupMemberRemoveResponse{
		Group:       req.Group,
		MemberType:  req.MemberType,
		MemberValue: req.MemberValue,
	}, nil
}

// HandleList handles the group.list RPC method.
func (h *GroupHandler) HandleList(ctx context.Context, _ json.RawMessage) (any, error) {
	h.state.RLock()
	defer h.state.RUnlock()

	rows, err := h.state.DB().QueryContext(ctx, `
		SELECT g.group_id, g.name, g.description, g.created_at,
		       (SELECT COUNT(*) FROM group_members gm WHERE gm.group_id = g.group_id) as member_count
		FROM groups g
		ORDER BY g.name
	`)
	if err != nil {
		return nil, fmt.Errorf("query groups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := []GroupSummary{}
	for rows.Next() {
		var g GroupSummary
		var desc sql.NullString
		if err := rows.Scan(&g.GroupID, &g.Name, &desc, &g.CreatedAt, &g.MemberCount); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		if desc.Valid {
			g.Description = desc.String
		}
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate groups: %w", err)
	}

	return &GroupListResponse{Groups: result}, nil
}

// HandleInfo handles the group.info RPC method.
func (h *GroupHandler) HandleInfo(ctx context.Context, params json.RawMessage) (any, error) {
	var req GroupInfoRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	h.state.RLock()
	defer h.state.RUnlock()

	var resp GroupInfoResponse
	var desc sql.NullString
	err := h.state.DB().QueryRowContext(ctx,
		"SELECT group_id, name, description, created_at, created_by FROM groups WHERE name = ?",
		req.Name,
	).Scan(&resp.GroupID, &resp.Name, &desc, &resp.CreatedAt, &resp.CreatedBy)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("group %q not found", req.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("query group: %w", err)
	}
	if desc.Valid {
		resp.Description = desc.String
	}

	// Query members
	rows, err := h.state.DB().QueryContext(ctx,
		"SELECT member_type, member_value, added_at, added_by FROM group_members WHERE group_id = ? ORDER BY member_type, member_value",
		resp.GroupID,
	)
	if err != nil {
		return nil, fmt.Errorf("query members: %w", err)
	}
	defer func() { _ = rows.Close() }()

	resp.Members = []GroupMember{}
	for rows.Next() {
		var m GroupMember
		var addedBy sql.NullString
		if err := rows.Scan(&m.MemberType, &m.MemberValue, &m.AddedAt, &addedBy); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		if addedBy.Valid {
			m.AddedBy = addedBy.String
		}
		resp.Members = append(resp.Members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate members: %w", err)
	}

	return &resp, nil
}

// HandleMembers handles the group.members RPC method.
func (h *GroupHandler) HandleMembers(ctx context.Context, params json.RawMessage) (any, error) {
	var req GroupMembersRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	h.state.RLock()
	defer h.state.RUnlock()

	// Check group exists
	var groupID string
	err := h.state.DB().QueryRowContext(ctx, "SELECT group_id FROM groups WHERE name = ?", req.Name).Scan(&groupID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("group %q not found", req.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("query group: %w", err)
	}

	// Query raw members
	rows, err := h.state.DB().QueryContext(ctx,
		"SELECT member_type, member_value, added_at, added_by FROM group_members WHERE group_id = ? ORDER BY member_type, member_value",
		groupID,
	)
	if err != nil {
		return nil, fmt.Errorf("query members: %w", err)
	}
	defer func() { _ = rows.Close() }()

	resp := GroupMembersResponse{Members: []GroupMember{}}
	for rows.Next() {
		var m GroupMember
		var addedBy sql.NullString
		if err := rows.Scan(&m.MemberType, &m.MemberValue, &m.AddedAt, &addedBy); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		if addedBy.Valid {
			m.AddedBy = addedBy.String
		}
		resp.Members = append(resp.Members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate members: %w", err)
	}

	// Expand if requested
	if req.Expand {
		expanded, err := h.resolver.ExpandMembers(req.Name)
		if err != nil {
			return nil, fmt.Errorf("expand members: %w", err)
		}
		resp.Expanded = expanded
	}

	return &resp, nil
}

// EnsureEveryoneGroup creates the built-in @everyone group if it doesn't exist.
// Called during daemon startup.
func EnsureEveryoneGroup(ctx context.Context, st *state.State) error {
	st.RLock()
	var exists bool
	err := st.DB().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM groups WHERE name = 'everyone')").Scan(&exists)
	st.RUnlock()
	if err != nil {
		return fmt.Errorf("check everyone group: %w", err)
	}
	if exists {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	createEvent := types.GroupCreateEvent{
		Type:        "group.create",
		Timestamp:   now,
		GroupID:     "grp_everyone",
		Name:        "everyone",
		Description: "All agents",
		CreatedBy:   "system",
	}

	st.Lock()
	defer st.Unlock()

	if err := st.WriteEvent(ctx, createEvent); err != nil {
		return fmt.Errorf("write everyone group.create: %w", err)
	}

	memberEvent := types.GroupMemberAddEvent{
		Type:        "group.member.add",
		Timestamp:   now,
		GroupID:     "grp_everyone",
		MemberType:  "role",
		MemberValue: "*",
		AddedBy:     "system",
	}

	if err := st.WriteEvent(ctx, memberEvent); err != nil {
		return fmt.Errorf("write everyone group.member.add: %w", err)
	}

	return nil
}
