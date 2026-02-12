package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/groups"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/subscriptions"
	"github.com/leonletto/thrum/internal/types"
)

// SendRequest represents the request for message.send RPC.
type SendRequest struct {
	Content       string         `json:"content"`
	Format        string         `json:"format,omitempty"`     // default: "markdown"
	Structured    map[string]any `json:"structured,omitempty"` // optional typed payload
	ThreadID      string         `json:"thread_id,omitempty"`
	Scopes        []types.Scope  `json:"scopes,omitempty"`
	Refs          []types.Ref    `json:"refs,omitempty"`
	Mentions      []string       `json:"mentions,omitempty"` // e.g., ["@reviewer"]
	Tags          []string       `json:"tags,omitempty"`
	Priority      string         `json:"priority,omitempty"`  // "low", "normal", "high"
	ActingAs      string         `json:"acting_as,omitempty"` // Impersonate this agent (users only)
	Disclose      bool           `json:"disclose,omitempty"`  // Show [via user:X] in message
	CallerAgentID string         `json:"caller_agent_id,omitempty"`
}

// SendResponse represents the response from message.send RPC.
type SendResponse struct {
	MessageID string `json:"message_id"`
	ThreadID  string `json:"thread_id,omitempty"`
	CreatedAt string `json:"created_at"`
}

// GetMessageRequest represents the request for message.get RPC.
type GetMessageRequest struct {
	MessageID string `json:"message_id"`
}

// GetMessageResponse represents the response from message.get RPC.
type GetMessageResponse struct {
	Message MessageDetail `json:"message"`
}

// MessageDetail represents detailed information about a message.
type MessageDetail struct {
	MessageID string            `json:"message_id"`
	ThreadID  string            `json:"thread_id,omitempty"`
	Author    AuthorInfo        `json:"author"`
	Body      types.MessageBody `json:"body"`
	Scopes    []types.Scope     `json:"scopes"`
	Refs      []types.Ref       `json:"refs"`
	Metadata  MessageMetadata   `json:"metadata"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at,omitempty"`
	Deleted   bool              `json:"deleted"`
}

// AuthorInfo represents information about the message author.
type AuthorInfo struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
}

// MessageMetadata represents metadata about a message.
type MessageMetadata struct {
	DeletedAt    string `json:"deleted_at,omitempty"`
	DeleteReason string `json:"delete_reason,omitempty"`
}

// ListMessagesRequest represents the request for message.list RPC.
type ListMessagesRequest struct {
	// Filters
	Scope    *types.Scope `json:"scope,omitempty"`     // Filter by scope
	Ref      *types.Ref   `json:"ref,omitempty"`       // Filter by ref
	ThreadID string       `json:"thread_id,omitempty"` // Filter by thread
	AuthorID string       `json:"author_id,omitempty"` // Filter by author
	Mentions bool         `json:"mentions,omitempty"`  // Only mentioning current agent (resolved from config)
	Unread   bool         `json:"unread,omitempty"`    // Only unread messages (resolved from config)

	// Explicit filters (for remote callers like MCP server that can't use config resolution)
	MentionRole    string `json:"mention_role,omitempty"`     // Filter to messages with mention ref matching this role
	UnreadForAgent string `json:"unread_for_agent,omitempty"` // Filter to messages unread by this agent_id

	// Inbox behavior
	ExcludeSelf       bool   `json:"exclude_self,omitempty"`        // Exclude messages authored by the current agent (inbox mode)
	CallerAgentID     string `json:"caller_agent_id,omitempty"`     // For worktree callers to pass their agent ID
	CallerMentionRole string `json:"caller_mention_role,omitempty"` // For worktree callers to pass their role for mentions filter

	// Auto-filter: show only messages addressed to this agent (mentions) + broadcasts (no mentions)
	ForAgent     string `json:"for_agent,omitempty"`      // Agent name to filter for (messages mentioning this name + broadcasts)
	ForAgentRole string `json:"for_agent_role,omitempty"` // Agent role to filter for (messages mentioning this role + broadcasts)

	// Pagination
	PageSize int `json:"page_size,omitempty"` // Default: 10
	Page     int `json:"page,omitempty"`      // Default: 1

	// Sorting
	SortBy    string `json:"sort_by,omitempty"`    // "created_at", "updated_at"
	SortOrder string `json:"sort_order,omitempty"` // "asc", "desc"
}

// ListMessagesResponse represents the response from message.list RPC.
type ListMessagesResponse struct {
	Messages   []MessageSummary `json:"messages"`
	Total      int              `json:"total"`
	Unread     int              `json:"unread"`
	Page       int              `json:"page"`
	PageSize   int              `json:"page_size"`
	TotalPages int              `json:"total_pages"`
}

// MessageSummary represents a summary of a message for listing.
type MessageSummary struct {
	MessageID string            `json:"message_id"`
	ThreadID  string            `json:"thread_id,omitempty"`
	AgentID   string            `json:"agent_id"`
	Body      types.MessageBody `json:"body"`
	CreatedAt string            `json:"created_at"`
	Deleted   bool              `json:"deleted"`
	IsRead    bool              `json:"is_read"` // Computed from message_reads (session_id OR agent_id match)
}

// DeleteMessageRequest represents the request for message.delete RPC.
type DeleteMessageRequest struct {
	MessageID string `json:"message_id"`
	Reason    string `json:"reason,omitempty"`
}

// DeleteMessageResponse represents the response from message.delete RPC.
type DeleteMessageResponse struct {
	MessageID string `json:"message_id"`
	DeletedAt string `json:"deleted_at"`
}

// EditRequest represents the request for message.edit RPC.
type EditRequest struct {
	MessageID  string         `json:"message_id"`
	Content    string         `json:"content,omitempty"`    // New content
	Structured map[string]any `json:"structured,omitempty"` // New structured data
}

// EditResponse represents the response from message.edit RPC.
type EditResponse struct {
	MessageID string `json:"message_id"`
	UpdatedAt string `json:"updated_at"`
	Version   int    `json:"version"` // Edit count
}

// MarkReadRequest represents the request for message.markRead RPC.
type MarkReadRequest struct {
	MessageIDs    []string `json:"message_ids"` // Batch support
	CallerAgentID string   `json:"caller_agent_id,omitempty"`
}

// MarkReadResponse represents the response from message.markRead RPC.
type MarkReadResponse struct {
	MarkedCount int                 `json:"marked_count"`
	AlsoReadBy  map[string][]string `json:"also_read_by,omitempty"` // Key: message_id, Value: list of other agent_ids
}

// MessageHandler handles message-related RPC methods.
type MessageHandler struct {
	state         *state.State
	dispatcher    *subscriptions.Dispatcher
	groupResolver *groups.Resolver
}

// NewMessageHandler creates a new message handler.
func NewMessageHandler(state *state.State) *MessageHandler {
	return &MessageHandler{
		state:         state,
		dispatcher:    subscriptions.NewDispatcher(state.DB()),
		groupResolver: groups.NewResolver(state.DB()),
	}
}

// NewMessageHandlerWithDispatcher creates a new message handler with a custom dispatcher.
// The dispatcher should have the client notifier configured for push notifications.
func NewMessageHandlerWithDispatcher(state *state.State, dispatcher *subscriptions.Dispatcher) *MessageHandler {
	return &MessageHandler{
		state:         state,
		dispatcher:    dispatcher,
		groupResolver: groups.NewResolver(state.DB()),
	}
}

// HandleSend handles the message.send RPC method.
func (h *MessageHandler) HandleSend(ctx context.Context, params json.RawMessage) (any, error) {
	var req SendRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.Content == "" {
		return nil, fmt.Errorf("content is required")
	}

	// Default format to markdown
	format := req.Format
	if format == "" {
		format = "markdown"
	}

	// Validate format
	if format != "markdown" && format != "plain" && format != "json" {
		return nil, fmt.Errorf("invalid format: %s (must be 'markdown', 'plain', or 'json')", format)
	}

	// Generate message ID
	messageID := identity.GenerateMessageID()

	// Resolve current agent and session
	callerID, sessionID, err := h.resolveAgentAndSession(req.CallerAgentID)
	if err != nil {
		return nil, fmt.Errorf("resolve agent and session: %w", err)
	}

	// Handle impersonation (users can impersonate agents)
	agentID := callerID
	var authoredBy string
	disclosed := false

	if req.ActingAs != "" {
		// Validate impersonation request
		if err := h.validateImpersonation(callerID, req.ActingAs); err != nil {
			return nil, err
		}

		// Set up impersonation metadata
		agentID = req.ActingAs
		authoredBy = callerID
		disclosed = req.Disclose
	}

	// Prepare timestamp
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Marshal structured data if present
	var structuredJSON string
	if req.Structured != nil {
		data, err := json.Marshal(req.Structured)
		if err != nil {
			return nil, fmt.Errorf("marshal structured data: %w", err)
		}
		structuredJSON = string(data)
	}

	// Convert mentions to refs (with group detection)
	refs := req.Refs
	scopes := req.Scopes
	for _, mention := range req.Mentions {
		// Remove @ prefix if present
		role := mention
		if len(role) > 0 && role[0] == '@' {
			role = role[1:]
		}

		// Check if this mention is a group
		isGroup, err := h.groupResolver.IsGroup(role)
		if err != nil {
			return nil, fmt.Errorf("check group %q: %w", role, err)
		}

		if isGroup {
			// Group mention — store as group scope (pull model, no expansion)
			scopes = append(scopes, types.Scope{Type: "group", Value: role})
			// Also store audit ref for queryability
			refs = append(refs, types.Ref{Type: "group", Value: role})
		} else {
			// Not a group — treat as regular mention (push model)
			refs = append(refs, types.Ref{Type: "mention", Value: role})
		}
	}

	// Build message.create event
	event := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: now,
		MessageID: messageID,
		ThreadID:  req.ThreadID,
		AgentID:   agentID,
		SessionID: sessionID,
		Body: types.MessageBody{
			Format:     format,
			Content:    req.Content,
			Structured: structuredJSON,
		},
		Scopes:     scopes,
		Refs:       refs,
		AuthoredBy: authoredBy,
		Disclosed:  disclosed,
	}

	// Write event to JSONL and SQLite
	h.state.Lock()
	defer h.state.Unlock()

	if err := h.state.WriteEvent(event); err != nil {
		return nil, fmt.Errorf("write message.create event: %w", err)
	}

	// Trigger subscription notifications
	// Use the pre-configured dispatcher (has client notifier set for push notifications)
	preview := req.Content
	if len(preview) > 100 {
		preview = preview[:100]
	}

	msgInfo := &subscriptions.MessageInfo{
		MessageID: messageID,
		ThreadID:  req.ThreadID,
		AgentID:   agentID,
		SessionID: sessionID,
		Scopes:    event.Scopes,
		Refs:      event.Refs,
		Timestamp: now,
		Preview:   preview,
	}

	// Find matching subscriptions and push notifications to connected clients
	_, err = h.dispatcher.DispatchForMessage(msgInfo)
	if err != nil {
		// Log error but don't fail the message send
		_ = err
	}

	// Store threadID for emit (need to unlock state before emitting)
	threadID := req.ThreadID

	// Unlock state before emitting (to avoid deadlock)
	h.state.Unlock()

	// Emit thread.updated event for real-time updates
	if threadID != "" {
		_ = h.emitThreadUpdated(ctx, threadID)
	}

	// Re-lock state before returning (to satisfy defer unlock)
	h.state.Lock()

	return &SendResponse{
		MessageID: messageID,
		ThreadID:  req.ThreadID,
		CreatedAt: now,
	}, nil
}

// HandleGet handles the message.get RPC method.
func (h *MessageHandler) HandleGet(ctx context.Context, params json.RawMessage) (any, error) {
	var req GetMessageRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.MessageID == "" {
		return nil, fmt.Errorf("message_id is required")
	}

	h.state.RLock()
	defer h.state.RUnlock()

	// Query message
	query := `SELECT message_id, thread_id, agent_id, session_id, created_at, updated_at,
	                 body_format, body_content, body_structured, deleted, deleted_at, delete_reason
	          FROM messages
	          WHERE message_id = ?`

	var msg MessageDetail
	var threadID, updatedAt, bodyStructured, deletedAt, deleteReason sql.NullString
	var deleted int

	err := h.state.DB().QueryRow(query, req.MessageID).Scan(
		&msg.MessageID,
		&threadID,
		&msg.Author.AgentID,
		&msg.Author.SessionID,
		&msg.CreatedAt,
		&updatedAt,
		&msg.Body.Format,
		&msg.Body.Content,
		&bodyStructured,
		&deleted,
		&deletedAt,
		&deleteReason,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("message not found: %s", req.MessageID)
	}
	if err != nil {
		return nil, fmt.Errorf("query message: %w", err)
	}

	// Set optional fields
	if threadID.Valid {
		msg.ThreadID = threadID.String
	}
	if updatedAt.Valid {
		msg.UpdatedAt = updatedAt.String
	}
	if bodyStructured.Valid {
		msg.Body.Structured = bodyStructured.String
	}
	msg.Deleted = deleted == 1
	if deletedAt.Valid {
		msg.Metadata.DeletedAt = deletedAt.String
	}
	if deleteReason.Valid {
		msg.Metadata.DeleteReason = deleteReason.String
	}

	// Query scopes
	scopeQuery := `SELECT scope_type, scope_value FROM message_scopes WHERE message_id = ?`
	rows, err := h.state.DB().Query(scopeQuery, req.MessageID)
	if err != nil {
		return nil, fmt.Errorf("query scopes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	msg.Scopes = []types.Scope{}
	for rows.Next() {
		var scope types.Scope
		if err := rows.Scan(&scope.Type, &scope.Value); err != nil {
			return nil, fmt.Errorf("scan scope: %w", err)
		}
		msg.Scopes = append(msg.Scopes, scope)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scopes: %w", err)
	}

	// Query refs
	refQuery := `SELECT ref_type, ref_value FROM message_refs WHERE message_id = ?`
	rows, err = h.state.DB().Query(refQuery, req.MessageID)
	if err != nil {
		return nil, fmt.Errorf("query refs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	msg.Refs = []types.Ref{}
	for rows.Next() {
		var ref types.Ref
		if err := rows.Scan(&ref.Type, &ref.Value); err != nil {
			return nil, fmt.Errorf("scan ref: %w", err)
		}
		msg.Refs = append(msg.Refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate refs: %w", err)
	}

	return &GetMessageResponse{Message: msg}, nil
}

// HandleList handles the message.list RPC method.
func (h *MessageHandler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	var req ListMessagesRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Set defaults
	pageSize := req.PageSize
	if pageSize == 0 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100 // Max page size
	}

	page := req.Page
	if page == 0 {
		page = 1
	}

	sortBy := req.SortBy
	if sortBy == "" {
		sortBy = "created_at"
	}
	if sortBy != "created_at" && sortBy != "updated_at" {
		return nil, fmt.Errorf("invalid sort_by: %s (must be 'created_at' or 'updated_at')", sortBy)
	}

	sortOrder := req.SortOrder
	if sortOrder == "" {
		sortOrder = "desc"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		return nil, fmt.Errorf("invalid sort_order: %s (must be 'asc' or 'desc')", sortOrder)
	}

	h.state.RLock()
	defer h.state.RUnlock()

	// Resolve current agent ID once — used for exclude_self, is_read, and unread count
	var currentAgentID string
	if req.ExcludeSelf || req.Unread || req.UnreadForAgent != "" {
		if selfID, _, resolveErr := h.resolveAgentAndSession(req.CallerAgentID); resolveErr == nil {
			currentAgentID = selfID
		}
	}

	// Build query — include is_read status via correlated subquery when agent is known
	var selectCols string
	if currentAgentID != "" {
		selectCols = `SELECT m.message_id, m.thread_id, m.agent_id, m.created_at, m.updated_at,
		                     m.body_format, m.body_content, m.body_structured, m.deleted,
		                     CASE WHEN EXISTS(SELECT 1 FROM message_reads WHERE message_id = m.message_id AND agent_id = ?) THEN 1 ELSE 0 END as is_read`
	} else {
		selectCols = `SELECT m.message_id, m.thread_id, m.agent_id, m.created_at, m.updated_at,
		                     m.body_format, m.body_content, m.body_structured, m.deleted,
		                     0 as is_read`
	}
	query := selectCols + "\n\t          FROM messages m"

	// Add joins for filters
	joins := ""
	if req.Scope != nil {
		joins += " INNER JOIN message_scopes ms ON m.message_id = ms.message_id"
	}
	if req.Ref != nil {
		joins += " INNER JOIN message_refs mr ON m.message_id = mr.message_id"
	}

	query += joins + " WHERE 1=1"

	// Build WHERE clauses and args
	// correlated subquery for is_read needs agentID as the first arg
	args := []any{}
	if currentAgentID != "" {
		args = append(args, currentAgentID)
	}

	if req.ThreadID != "" {
		query += " AND m.thread_id = ?"
		args = append(args, req.ThreadID)
	}

	if req.AuthorID != "" {
		query += " AND m.agent_id = ?"
		args = append(args, req.AuthorID)
	}

	// Exclude messages authored by the current agent (inbox mode)
	var excludeAgentID string
	if req.ExcludeSelf && currentAgentID != "" {
		excludeAgentID = currentAgentID
		query += " AND m.agent_id != ?"
		args = append(args, excludeAgentID)
	}

	if req.Scope != nil {
		query += " AND ms.scope_type = ? AND ms.scope_value = ?"
		args = append(args, req.Scope.Type, req.Scope.Value)
	}

	if req.Ref != nil {
		query += " AND mr.ref_type = ? AND mr.ref_value = ?"
		args = append(args, req.Ref.Type, req.Ref.Value)
	}

	// Mentions filter: explicit MentionRole takes priority, then CallerMentionRole, falls back to config when Mentions=true
	mentionRole := req.MentionRole
	if mentionRole == "" && req.CallerMentionRole != "" && req.Mentions {
		mentionRole = req.CallerMentionRole
	}
	if mentionRole == "" && req.Mentions {
		cfg, cfgErr := config.LoadWithPath(h.state.RepoPath(), "", "")
		if cfgErr == nil && cfg.Agent.Role != "" {
			mentionRole = cfg.Agent.Role
		}
	}
	if mentionRole != "" {
		query += " AND m.message_id IN (SELECT mr_m.message_id FROM message_refs mr_m WHERE mr_m.ref_type = 'mention' AND mr_m.ref_value = ?)"
		args = append(args, mentionRole)
	}

	// Unread filter: explicit UnreadForAgent takes priority, falls back to config when Unread=true
	unreadAgentID := req.UnreadForAgent
	if unreadAgentID == "" && req.Unread {
		agentID, _, resolveErr := h.resolveAgentAndSession(req.CallerAgentID)
		if resolveErr == nil {
			unreadAgentID = agentID
		}
	}
	if unreadAgentID != "" {
		query += " AND m.message_id NOT IN (SELECT mrd.message_id FROM message_reads mrd WHERE mrd.agent_id = ?)"
		args = append(args, unreadAgentID)
	}

	// For-agent filter: show messages addressed to this agent (mentions) + broadcasts (no mention refs)
	forAgentValues := buildForAgentValues(req.ForAgent, req.ForAgentRole)
	if len(forAgentValues) > 0 {
		placeholders := make([]string, len(forAgentValues))
		for i := range forAgentValues {
			placeholders[i] = "?"
		}
		query += " AND (m.message_id NOT IN (SELECT mr_fa.message_id FROM message_refs mr_fa WHERE mr_fa.ref_type = 'mention')" +
			" OR m.message_id IN (SELECT mr_fa2.message_id FROM message_refs mr_fa2 WHERE mr_fa2.ref_type = 'mention' AND mr_fa2.ref_value IN (" + strings.Join(placeholders, ",") + ")))"
		for _, v := range forAgentValues {
			args = append(args, v)
		}
	}

	// Add sorting
	query += fmt.Sprintf(" ORDER BY m.%s %s", sortBy, sortOrder)

	// Count total matching messages (use same filters as main query)
	countQuery := "SELECT COUNT(DISTINCT m.message_id) FROM messages m" + joins + " WHERE 1=1"
	countArgs := []any{}
	if req.ThreadID != "" {
		countQuery += " AND m.thread_id = ?"
		countArgs = append(countArgs, req.ThreadID)
	}
	if req.AuthorID != "" {
		countQuery += " AND m.agent_id = ?"
		countArgs = append(countArgs, req.AuthorID)
	}
	if excludeAgentID != "" {
		countQuery += " AND m.agent_id != ?"
		countArgs = append(countArgs, excludeAgentID)
	}
	if req.Scope != nil {
		countQuery += " AND ms.scope_type = ? AND ms.scope_value = ?"
		countArgs = append(countArgs, req.Scope.Type, req.Scope.Value)
	}
	if req.Ref != nil {
		countQuery += " AND mr.ref_type = ? AND mr.ref_value = ?"
		countArgs = append(countArgs, req.Ref.Type, req.Ref.Value)
	}
	if mentionRole != "" {
		countQuery += " AND m.message_id IN (SELECT mr_m.message_id FROM message_refs mr_m WHERE mr_m.ref_type = 'mention' AND mr_m.ref_value = ?)"
		countArgs = append(countArgs, mentionRole)
	}
	if unreadAgentID != "" {
		countQuery += " AND m.message_id NOT IN (SELECT mrd.message_id FROM message_reads mrd WHERE mrd.agent_id = ?)"
		countArgs = append(countArgs, unreadAgentID)
	}
	if len(forAgentValues) > 0 {
		placeholders := make([]string, len(forAgentValues))
		for i := range forAgentValues {
			placeholders[i] = "?"
		}
		countQuery += " AND (m.message_id NOT IN (SELECT mr_fa.message_id FROM message_refs mr_fa WHERE mr_fa.ref_type = 'mention')" +
			" OR m.message_id IN (SELECT mr_fa2.message_id FROM message_refs mr_fa2 WHERE mr_fa2.ref_type = 'mention' AND mr_fa2.ref_value IN (" + strings.Join(placeholders, ",") + ")))"
		for _, v := range forAgentValues {
			countArgs = append(countArgs, v)
		}
	}

	var total int
	if err := h.state.DB().QueryRow(countQuery, countArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count messages: %w", err)
	}

	// Calculate pagination
	offset := (page - 1) * pageSize
	totalPages := (total + pageSize - 1) / pageSize // Ceiling division

	// Add pagination
	query += " LIMIT ? OFFSET ?"
	args = append(args, pageSize, offset)

	// Execute query
	rows, err := h.state.DB().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	messages := []MessageSummary{}
	for rows.Next() {
		var msg MessageSummary
		var threadID, updatedAt, bodyStructured sql.NullString
		var deleted, isRead int

		if err := rows.Scan(
			&msg.MessageID,
			&threadID,
			&msg.AgentID,
			&msg.CreatedAt,
			&updatedAt,
			&msg.Body.Format,
			&msg.Body.Content,
			&bodyStructured,
			&deleted,
			&isRead,
		); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}

		if threadID.Valid {
			msg.ThreadID = threadID.String
		}
		if bodyStructured.Valid {
			msg.Body.Structured = bodyStructured.String
		}
		msg.Deleted = deleted == 1
		msg.IsRead = isRead == 1

		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}

	// Calculate unread count
	unread := 0
	if currentAgentID != "" {
		unreadQuery := "SELECT COUNT(*) FROM messages m" + joins + " WHERE 1=1"
		unreadArgs := []any{}
		if req.ThreadID != "" {
			unreadQuery += " AND m.thread_id = ?"
			unreadArgs = append(unreadArgs, req.ThreadID)
		}
		if excludeAgentID != "" {
			unreadQuery += " AND m.agent_id != ?"
			unreadArgs = append(unreadArgs, excludeAgentID)
		}
		unreadQuery += " AND m.message_id NOT IN (SELECT mrd2.message_id FROM message_reads mrd2 WHERE mrd2.agent_id = ?)"
		unreadArgs = append(unreadArgs, currentAgentID)
		_ = h.state.DB().QueryRow(unreadQuery, unreadArgs...).Scan(&unread)
	}

	return &ListMessagesResponse{
		Messages:   messages,
		Total:      total,
		Unread:     unread,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}, nil
}

// HandleDelete handles the message.delete RPC method.
func (h *MessageHandler) HandleDelete(ctx context.Context, params json.RawMessage) (any, error) {
	var req DeleteMessageRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.MessageID == "" {
		return nil, fmt.Errorf("message_id is required")
	}

	// Verify message exists and is not already deleted
	h.state.RLock()
	var deleted int
	query := `SELECT deleted FROM messages WHERE message_id = ?`
	err := h.state.DB().QueryRow(query, req.MessageID).Scan(&deleted)
	h.state.RUnlock()

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("message not found: %s", req.MessageID)
	}
	if err != nil {
		return nil, fmt.Errorf("query message: %w", err)
	}

	if deleted == 1 {
		return nil, fmt.Errorf("message already deleted: %s", req.MessageID)
	}

	// Prepare timestamp
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Build message.delete event
	event := types.MessageDeleteEvent{
		Type:      "message.delete",
		Timestamp: now,
		MessageID: req.MessageID,
		Reason:    req.Reason,
	}

	// Write event to JSONL and SQLite
	h.state.Lock()
	defer h.state.Unlock()

	if err := h.state.WriteEvent(event); err != nil {
		return nil, fmt.Errorf("write message.delete event: %w", err)
	}

	return &DeleteMessageResponse{
		MessageID: req.MessageID,
		DeletedAt: now,
	}, nil
}

// HandleEdit handles the message.edit RPC method.
func (h *MessageHandler) HandleEdit(ctx context.Context, params json.RawMessage) (any, error) {
	var req EditRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.MessageID == "" {
		return nil, fmt.Errorf("message_id is required")
	}

	// At least one of content or structured must be provided
	if req.Content == "" && req.Structured == nil {
		return nil, fmt.Errorf("at least one of content or structured must be provided")
	}

	// Get current agent and session
	agentID, sessionID, err := h.resolveAgentAndSession("")
	if err != nil {
		return nil, fmt.Errorf("resolve agent and session: %w", err)
	}

	// Verify message exists, is not deleted, and author matches
	h.state.RLock()
	var authorAgentID string
	var deleted int
	var currentContent, currentStructured sql.NullString
	query := `SELECT agent_id, deleted, body_content, body_structured FROM messages WHERE message_id = ?`
	err = h.state.DB().QueryRow(query, req.MessageID).Scan(&authorAgentID, &deleted, &currentContent, &currentStructured)
	h.state.RUnlock()

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("message not found: %s", req.MessageID)
	}
	if err != nil {
		return nil, fmt.Errorf("query message: %w", err)
	}

	// Check if message is deleted
	if deleted == 1 {
		return nil, fmt.Errorf("cannot edit deleted message: %s", req.MessageID)
	}

	// Verify author (only the message author can edit)
	if authorAgentID != agentID {
		return nil, fmt.Errorf("only message author can edit (author: %s, current: %s)", authorAgentID, agentID)
	}

	// Prepare timestamp
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Determine new content (use existing if not provided)
	newContent := req.Content
	if newContent == "" && currentContent.Valid {
		newContent = currentContent.String
	}

	// Marshal structured data if present, otherwise use existing
	var structuredJSON string
	if req.Structured != nil {
		data, err := json.Marshal(req.Structured)
		if err != nil {
			return nil, fmt.Errorf("marshal structured data: %w", err)
		}
		structuredJSON = string(data)
	} else if currentStructured.Valid {
		structuredJSON = currentStructured.String
	}

	// Build message.edit event
	event := types.MessageEditEvent{
		Type:      "message.edit",
		Timestamp: now,
		MessageID: req.MessageID,
		Body: types.MessageBody{
			Format:     "markdown", // Use default format
			Content:    newContent,
			Structured: structuredJSON,
		},
	}

	// Write event to JSONL and SQLite
	h.state.Lock()
	defer h.state.Unlock()

	if err := h.state.WriteEvent(event); err != nil {
		return nil, fmt.Errorf("write message.edit event: %w", err)
	}

	// Trigger subscription notifications
	// Use the pre-configured dispatcher (has client notifier set for push notifications)
	preview := newContent
	if len(preview) > 100 {
		preview = preview[:100]
	}

	// Query message metadata for notification
	var threadID sql.NullString
	var scopes []types.Scope
	var refs []types.Ref

	query = `SELECT thread_id FROM messages WHERE message_id = ?`
	err = h.state.DB().QueryRow(query, req.MessageID).Scan(&threadID)
	if err != nil {
		return nil, fmt.Errorf("query message metadata: %w", err)
	}

	// Query scopes
	scopeQuery := `SELECT scope_type, scope_value FROM message_scopes WHERE message_id = ?`
	rows, err := h.state.DB().Query(scopeQuery, req.MessageID)
	if err != nil {
		return nil, fmt.Errorf("query scopes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var scope types.Scope
		if err := rows.Scan(&scope.Type, &scope.Value); err != nil {
			return nil, fmt.Errorf("scan scope: %w", err)
		}
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scopes: %w", err)
	}

	// Query refs
	refQuery := `SELECT ref_type, ref_value FROM message_refs WHERE message_id = ?`
	rows, err = h.state.DB().Query(refQuery, req.MessageID)
	if err != nil {
		return nil, fmt.Errorf("query refs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var ref types.Ref
		if err := rows.Scan(&ref.Type, &ref.Value); err != nil {
			return nil, fmt.Errorf("scan ref: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate refs: %w", err)
	}

	msgInfo := &subscriptions.MessageInfo{
		MessageID: req.MessageID,
		ThreadID:  threadID.String,
		AgentID:   agentID,
		SessionID: sessionID,
		Scopes:    scopes,
		Refs:      refs,
		Timestamp: now,
		Preview:   preview,
	}

	// Dispatch notifications (same as message.create)
	_, err = h.dispatcher.DispatchForMessage(msgInfo)
	if err != nil {
		// Log error but don't fail the edit
		_ = err
	}

	// Count edits for version number (count includes the edit we just applied)
	var editCount int
	countQuery := `SELECT COUNT(*) FROM message_edits WHERE message_id = ?`
	if err := h.state.DB().QueryRow(countQuery, req.MessageID).Scan(&editCount); err != nil {
		// If query fails, default to 1 (we just made an edit)
		editCount = 1
	}

	return &EditResponse{
		MessageID: req.MessageID,
		UpdatedAt: now,
		Version:   editCount,
	}, nil
}

// buildForAgentValues returns the unique set of values to match against mention refs
// for the for-agent inbox filter. Returns nil if no filtering should be applied.
func buildForAgentValues(forAgent, forAgentRole string) []string {
	if forAgent == "" && forAgentRole == "" {
		return nil
	}
	seen := map[string]bool{}
	var values []string
	for _, v := range []string{forAgent, forAgentRole} {
		if v != "" && !seen[v] {
			seen[v] = true
			values = append(values, v)
		}
	}
	return values
}

// resolveAgentAndSession returns the current agent ID and session ID.
func (h *MessageHandler) resolveAgentAndSession(callerAgentID string) (agentID string, sessionID string, err error) {
	if callerAgentID != "" {
		agentID = callerAgentID
	} else {
		// Fallback: load identity from daemon's config (single-worktree backward compat)
		log.Printf("WARNING: CallerAgentID not provided in message RPC, falling back to daemon repo path: %s (CLI should resolve identity)", h.state.RepoPath())
		cfg, loadErr := config.LoadWithPath(h.state.RepoPath(), "", "")
		if loadErr != nil {
			return "", "", fmt.Errorf("load config: %w", loadErr)
		}
		agentID = identity.GenerateAgentID(h.state.RepoID(), cfg.Agent.Role, cfg.Agent.Module, cfg.Agent.Name)
	}

	// Query for active session
	h.state.RLock()
	defer h.state.RUnlock()

	query := `SELECT session_id FROM sessions
	          WHERE agent_id = ? AND ended_at IS NULL
	          ORDER BY started_at DESC
	          LIMIT 1`

	err = h.state.DB().QueryRow(query, agentID).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return "", "", fmt.Errorf("no active session found for agent %s (you must start a session first)", agentID)
	}
	if err != nil {
		return "", "", fmt.Errorf("query active session: %w", err)
	}

	return agentID, sessionID, nil
}

// validateImpersonation validates that the caller is authorized to impersonate the target identity.
func (h *MessageHandler) validateImpersonation(callerID, targetID string) error {
	// Check if caller is a user
	if !strings.HasPrefix(callerID, "user:") {
		return fmt.Errorf("only users can impersonate agents (caller: %s)", callerID)
	}

	// Check if target is an agent (not a user)
	// Agent IDs are either named (e.g., "furiosa") or hash-based (e.g., "implementer_HASH")
	// User IDs start with "user:"
	if strings.HasPrefix(targetID, "user:") {
		return fmt.Errorf("users can only impersonate agents, not other users (target: %s)", targetID)
	}

	// Check if target agent exists
	h.state.RLock()
	defer h.state.RUnlock()

	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM agents WHERE agent_id = ?)`
	err := h.state.DB().QueryRow(query, targetID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check agent exists: %w", err)
	}

	if !exists {
		return fmt.Errorf("target agent does not exist: %s", targetID)
	}

	return nil
}

// HandleMarkRead handles the message.markRead RPC method.
func (h *MessageHandler) HandleMarkRead(ctx context.Context, params json.RawMessage) (any, error) {
	var req MarkReadRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if len(req.MessageIDs) == 0 {
		return nil, fmt.Errorf("message_ids is required and must not be empty")
	}

	// Get current agent and session
	agentID, sessionID, err := h.resolveAgentAndSession(req.CallerAgentID)
	if err != nil {
		return nil, fmt.Errorf("resolve agent and session: %w", err)
	}

	// Prepare timestamp
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Track marked count, collaboration info, and affected threads
	markedCount := 0
	alsoReadBy := make(map[string][]string)
	affectedThreads := make(map[string]bool)

	// Begin transaction for batch insert
	h.state.Lock()
	defer h.state.Unlock()

	tx, err := h.state.DB().Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// For each message_id
	for _, messageID := range req.MessageIDs {
		// Check if message exists and get thread_id (skip if not found, don't error)
		var msgThreadID sql.NullString
		err = tx.QueryRow("SELECT thread_id FROM messages WHERE message_id = ?", messageID).Scan(&msgThreadID)
		if err == sql.ErrNoRows {
			// Skip non-existent messages
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("check message exists: %w", err)
		}

		// Track affected thread
		if msgThreadID.Valid && msgThreadID.String != "" {
			affectedThreads[msgThreadID.String] = true
		}

		// Query existing reads for this message to detect collaboration
		rows, err := tx.Query("SELECT DISTINCT agent_id FROM message_reads WHERE message_id = ? AND agent_id != ?", messageID, agentID)
		if err != nil {
			return nil, fmt.Errorf("query existing reads: %w", err)
		}

		var otherAgents []string
		for rows.Next() {
			var otherAgentID string
			if err := rows.Scan(&otherAgentID); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan other agent: %w", err)
			}
			otherAgents = append(otherAgents, otherAgentID)
		}
		_ = rows.Close()

		if len(otherAgents) > 0 {
			alsoReadBy[messageID] = otherAgents
		}

		// Insert or update read record
		_, err = tx.Exec(`
			INSERT INTO message_reads (message_id, session_id, agent_id, read_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (message_id, session_id)
			DO UPDATE SET read_at = excluded.read_at, agent_id = excluded.agent_id
		`, messageID, sessionID, agentID, now)
		if err != nil {
			return nil, fmt.Errorf("insert message_read: %w", err)
		}

		markedCount++
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	// Unlock state before emitting events
	h.state.Unlock()

	// Emit thread.updated for each affected thread
	for threadID := range affectedThreads {
		_ = h.emitThreadUpdated(ctx, threadID)
	}

	// Re-lock state before returning (to satisfy defer unlock)
	h.state.Lock()

	// Build response
	resp := &MarkReadResponse{
		MarkedCount: markedCount,
	}
	if len(alsoReadBy) > 0 {
		resp.AlsoReadBy = alsoReadBy
	}

	return resp, nil
}

// emitThreadUpdated emits a thread.updated event for real-time WebSocket notifications.
func (h *MessageHandler) emitThreadUpdated(_ context.Context, threadID string) error {
	// Get current agent and session for unread count
	agentID, sessionID, err := h.resolveAgentAndSession("")
	if err != nil {
		// If we can't resolve agent/session, just skip emitting (best-effort)
		return nil
	}

	// Query thread stats
	h.state.RLock()
	defer h.state.RUnlock()

	query := `SELECT
	    COUNT(DISTINCT m.message_id) as message_count,
	    COUNT(DISTINCT CASE
	        WHEN mr.message_id IS NULL AND m.deleted = 0 THEN m.message_id
	        END) as unread_count,
	    COALESCE(MAX(m.created_at), '') as last_activity,
	    (SELECT agent_id FROM messages
	     WHERE thread_id = ? AND deleted = 0
	     ORDER BY created_at DESC LIMIT 1) as last_sender,
	    (SELECT SUBSTR(body_content, 1, 100)
	     FROM messages
	     WHERE thread_id = ? AND deleted = 0
	     ORDER BY created_at DESC LIMIT 1) as preview
	FROM messages m
	LEFT JOIN message_reads mr ON m.message_id = mr.message_id
	    AND (mr.session_id = ? OR mr.agent_id = ?)
	WHERE m.thread_id = ? AND m.deleted = 0`

	var messageCount, unreadCount int
	var lastActivity string
	var lastSender, preview sql.NullString

	err = h.state.DB().QueryRow(query, threadID, threadID, sessionID, agentID, threadID).Scan(
		&messageCount,
		&unreadCount,
		&lastActivity,
		&lastSender,
		&preview,
	)
	if err != nil {
		// If query fails, skip (best-effort)
		return nil
	}

	// Build thread update info
	info := &subscriptions.ThreadUpdateInfo{
		ThreadID:     threadID,
		MessageCount: messageCount,
		UnreadCount:  unreadCount,
		LastActivity: lastActivity,
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
	}

	if lastSender.Valid {
		info.LastSender = lastSender.String
	}
	if preview.Valid && preview.String != "" {
		previewStr := preview.String
		info.Preview = &previewStr
	}

	// Dispatch to subscribed sessions
	return h.dispatcher.DispatchThreadUpdated(info)
}
