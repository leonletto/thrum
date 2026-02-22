package rpc

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	ReplyTo       string         `json:"reply_to,omitempty"`
	Scopes        []types.Scope  `json:"scopes,omitempty"`
	Refs          []types.Ref    `json:"refs,omitempty"`
	Mentions      []string       `json:"mentions,omitempty"` // e.g., ["@reviewer"]
	Tags          []string       `json:"tags,omitempty"`
	ActingAs      string         `json:"acting_as,omitempty"` // Impersonate this agent (users only)
	Disclose      bool           `json:"disclose,omitempty"`  // Show [via user:X] in message
	CallerAgentID string         `json:"caller_agent_id,omitempty"`
}

// SendResponse represents the response from message.send RPC.
type SendResponse struct {
	MessageID  string   `json:"message_id"`
	ThreadID   string   `json:"thread_id,omitempty"`
	CreatedAt  string   `json:"created_at"`
	ResolvedTo int      `json:"resolved_to"`        // count of resolved mentions
	Warnings   []string `json:"warnings,omitempty"` // informational warnings
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
	ReplyTo   string            `json:"reply_to,omitempty"`
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

	// Time filter
	CreatedAfter string `json:"created_after,omitempty"` // Only return messages created after this RFC3339 timestamp

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
	ReplyTo   string            `json:"reply_to,omitempty"`
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

// ArchiveRequest represents the request for message.archive RPC.
type ArchiveRequest struct {
	ArchiveType string `json:"archive_type"` // "agent" or "group"
	Identifier  string `json:"identifier"`   // agent name or group name
}

// ArchiveResponse represents the response from message.archive RPC.
type ArchiveResponse struct {
	ArchivedCount int    `json:"archived_count"`
	ArchivePath   string `json:"archive_path"`
}

// archiveRecord is the structure written per line in the JSONL archive file.
type archiveRecord struct {
	MessageID string        `json:"message_id"`
	AgentID   string        `json:"agent_id"`
	CreatedAt string        `json:"created_at"`
	Body      archiveBody   `json:"body"`
	Scopes    []types.Scope `json:"scopes"`
	Refs      []types.Ref   `json:"refs"`
}

// archiveBody holds the body fields for an archived message.
type archiveBody struct {
	Format  string `json:"format"`
	Content string `json:"content"`
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

	// Convert mentions to refs (with group detection and recipient validation)
	refs := req.Refs
	scopes := req.Scopes
	resolvedTo := 0
	var warnings []string
	var unknownRecipients []string
	for _, mention := range req.Mentions {
		// Remove @ prefix if present
		role := mention
		if len(role) > 0 && role[0] == '@' {
			role = role[1:]
		}

		// Check if this mention is a group
		isGroup, err := h.groupResolver.IsGroup(ctx, role)
		if err != nil {
			return nil, fmt.Errorf("check group %q: %w", role, err)
		}

		if isGroup {
			// Group mention — store as group scope (pull model, no expansion)
			scopes = append(scopes, types.Scope{Type: "group", Value: role})
			// Also store audit ref for queryability
			refs = append(refs, types.Ref{Type: "group", Value: role})
			resolvedTo++
			// Warn sender that this resolved to a group, not an individual
			if role != "everyone" {
				warnings = append(warnings, fmt.Sprintf("@%s resolved to a group, not an individual agent", role))
			}
		} else {
			// Not a group — validate against agents table (by agent_id or role)
			var agentCount int
			err := h.state.DB().QueryRowContext(ctx,
				`SELECT COUNT(*) FROM agents WHERE agent_id = ? OR role = ?`,
				role, role,
			).Scan(&agentCount)
			if err != nil {
				return nil, fmt.Errorf("validate recipient %q: %w", role, err)
			}
			if agentCount > 0 {
				// Known agent or role — treat as regular mention (push model)
				refs = append(refs, types.Ref{Type: "mention", Value: role})
				resolvedTo++
			} else {
				// Unknown recipient
				unknownRecipients = append(unknownRecipients, "@"+role)
			}
		}
	}

	// Fail hard if any recipients could not be resolved
	if len(unknownRecipients) > 0 {
		return nil, fmt.Errorf("unknown recipients: %s — no matching agent, role, or group found",
			strings.Join(unknownRecipients, ", "))
	}

	// Handle reply_to: validate parent message exists and add reply_to ref
	if req.ReplyTo != "" {
		var exists int
		err := h.state.DB().QueryRowContext(ctx, `SELECT COUNT(1) FROM messages WHERE message_id = ?`, req.ReplyTo).Scan(&exists)
		if err != nil || exists == 0 {
			return nil, fmt.Errorf("reply_to message not found: %s", req.ReplyTo)
		}
		refs = append(refs, types.Ref{Type: "reply_to", Value: req.ReplyTo})
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
	// Lock only for WriteEvent
	h.state.Lock()
	if err := h.state.WriteEvent(ctx, event); err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("write message.create event: %w", err)
	}
	h.state.Unlock()

	// No lock for dispatch and emit (WebSocket I/O)
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
	_, _ = h.dispatcher.DispatchForMessage(ctx, msgInfo)

	// Emit thread.updated event for real-time updates
	if req.ThreadID != "" {
		_ = h.emitThreadUpdated(ctx, req.ThreadID)
	}

	return &SendResponse{
		MessageID:  messageID,
		ThreadID:   req.ThreadID,
		CreatedAt:  now,
		ResolvedTo: resolvedTo,
		Warnings:   warnings,
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

	err := h.state.DB().QueryRowContext(ctx, query, req.MessageID).Scan(
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
	rows, err := h.state.DB().QueryContext(ctx, scopeQuery, req.MessageID)
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
	rows, err = h.state.DB().QueryContext(ctx, refQuery, req.MessageID)
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
		if ref.Type == "reply_to" {
			msg.ReplyTo = ref.Value
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

	// Resolve current agent ID once — used for exclude_self, is_read, and unread count.
	// Use resolveAgentOnly (not resolveAgentAndSession) so the unread count works
	// even when the caller has no active session (e.g., thrum prime on startup).
	var currentAgentID string
	if req.ExcludeSelf || req.Unread || req.UnreadForAgent != "" {
		currentAgentID = h.resolveAgentOnly(req.CallerAgentID)
	}

	// Build query — include is_read status via correlated subquery when agent is known
	var selectCols string
	if currentAgentID != "" {
		selectCols = `SELECT m.message_id, m.thread_id, m.agent_id, m.created_at, m.updated_at,
		                     m.body_format, m.body_content, m.body_structured, m.deleted,
		                     CASE WHEN EXISTS(SELECT 1 FROM message_reads WHERE message_id = m.message_id AND agent_id = ?) THEN 1 ELSE 0 END as is_read,
		                     reply_ref.ref_value as reply_to`
	} else {
		selectCols = `SELECT m.message_id, m.thread_id, m.agent_id, m.created_at, m.updated_at,
		                     m.body_format, m.body_content, m.body_structured, m.deleted,
		                     0 as is_read,
		                     reply_ref.ref_value as reply_to`
	}
	query := selectCols + "\n\t          FROM messages m" +
		"\n\t          LEFT JOIN message_refs reply_ref ON reply_ref.message_id = m.message_id AND reply_ref.ref_type = 'reply_to'"

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

	// Time filter: only return messages created after a given timestamp
	if req.CreatedAfter != "" {
		query += " AND m.created_at > ?"
		args = append(args, req.CreatedAfter)
	}

	// For-agent filter: show messages mentioning me + messages scoped to my groups + old broadcasts (backward compat)
	forAgentValues := buildForAgentValues(req.ForAgent, req.ForAgentRole)
	forAgentClause, forAgentArgs := buildForAgentClause(forAgentValues, req.ForAgent, req.ForAgentRole)
	if forAgentClause != "" {
		query += forAgentClause
		args = append(args, forAgentArgs...)
	}

	// Add sorting — cluster replies with parents when using inbox (for_agent) mode,
	// but respect explicit sort_order when provided (e.g., wait uses desc for newest-first)
	if (req.ForAgent != "" || req.ForAgentRole != "") && req.SortOrder == "" {
		// Inbox mode: group replies under their parent, then chronological within each cluster
		query += " ORDER BY COALESCE(reply_ref.ref_value, m.message_id) ASC, m.created_at ASC"
	} else {
		query += fmt.Sprintf(" ORDER BY m.%s %s", sortBy, sortOrder)
	}

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
	if req.CreatedAfter != "" {
		countQuery += " AND m.created_at > ?"
		countArgs = append(countArgs, req.CreatedAfter)
	}
	if forAgentClause != "" {
		countQuery += forAgentClause
		countArgs = append(countArgs, forAgentArgs...)
	}

	var total int
	if err := h.state.DB().QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count messages: %w", err)
	}

	// Calculate pagination
	offset := (page - 1) * pageSize
	totalPages := (total + pageSize - 1) / pageSize // Ceiling division

	// Add pagination
	query += " LIMIT ? OFFSET ?"
	args = append(args, pageSize, offset)

	// Execute query
	rows, err := h.state.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	messages := []MessageSummary{}
	for rows.Next() {
		var msg MessageSummary
		var threadID, updatedAt, bodyStructured, replyTo sql.NullString
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
			&replyTo,
		); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}

		if threadID.Valid {
			msg.ThreadID = threadID.String
		}
		if replyTo.Valid {
			msg.ReplyTo = replyTo.String
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
		_ = h.state.DB().QueryRowContext(ctx, unreadQuery, unreadArgs...).Scan(&unread)
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
	err := h.state.DB().QueryRowContext(ctx, query, req.MessageID).Scan(&deleted)
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

	if err := h.state.WriteEvent(ctx, event); err != nil {
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
	err = h.state.DB().QueryRowContext(ctx, query, req.MessageID).Scan(&authorAgentID, &deleted, &currentContent, &currentStructured)
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
	// Lock only for WriteEvent
	h.state.Lock()
	if err := h.state.WriteEvent(ctx, event); err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("write message.edit event: %w", err)
	}
	h.state.Unlock()

	// Query metadata and dispatch without lock (DB queries + WebSocket I/O)
	preview := newContent
	if len(preview) > 100 {
		preview = preview[:100]
	}

	// Query message metadata for notification
	var threadID sql.NullString
	var scopes []types.Scope
	var refs []types.Ref

	query = `SELECT thread_id FROM messages WHERE message_id = ?`
	err = h.state.DB().QueryRowContext(ctx, query, req.MessageID).Scan(&threadID)
	if err != nil {
		return nil, fmt.Errorf("query message metadata: %w", err)
	}

	// Query scopes
	scopeQuery := `SELECT scope_type, scope_value FROM message_scopes WHERE message_id = ?`
	rows, err := h.state.DB().QueryContext(ctx, scopeQuery, req.MessageID)
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
	rows, err = h.state.DB().QueryContext(ctx, refQuery, req.MessageID)
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
	_, _ = h.dispatcher.DispatchForMessage(ctx, msgInfo)

	// Count edits for version number (count includes the edit we just applied)
	var editCount int
	countQuery := `SELECT COUNT(*) FROM message_edits WHERE message_id = ?`
	if err := h.state.DB().QueryRowContext(ctx, countQuery, req.MessageID).Scan(&editCount); err != nil {
		// If query fails, default to 1 (we just made an edit)
		editCount = 1
	}

	return &EditResponse{
		MessageID: req.MessageID,
		UpdatedAt: now,
		Version:   editCount,
	}, nil
}

// buildForAgentClause builds the SQL WHERE clause for the for-agent inbox filter.
// It combines three conditions with OR:
// 1. Messages with mention refs matching the agent (direct mentions)
// 2. Messages scoped to groups the agent belongs to (group membership)
// 3. Messages with no mention refs (old broadcast backward compat — remove in next release).
func buildForAgentClause(forAgentValues []string, forAgent, forAgentRole string) (string, []any) {
	if len(forAgentValues) == 0 {
		return "", nil
	}

	var args []any

	// Part 1: direct mention refs matching agent name/role
	mentionPlaceholders := make([]string, len(forAgentValues))
	for i := range forAgentValues {
		mentionPlaceholders[i] = "?"
	}
	mentionSubquery := "m.message_id IN (SELECT mr_fa2.message_id FROM message_refs mr_fa2 WHERE mr_fa2.ref_type = 'mention' AND mr_fa2.ref_value IN (" +
		strings.Join(mentionPlaceholders, ",") + "))"
	for _, v := range forAgentValues {
		args = append(args, v)
	}

	// Part 2: group membership subquery (flat groups only)
	// Messages scoped to groups the agent belongs to (via agent name, role, or wildcard)
	groupSubquery := `m.message_id IN (
		SELECT ms_g.message_id FROM message_scopes ms_g
		WHERE ms_g.scope_type = 'group'
		AND ms_g.scope_value IN (
			SELECT g.name FROM groups g
			JOIN group_members gm ON g.group_id = gm.group_id
			WHERE (gm.member_type = 'agent' AND gm.member_value = ?)
			   OR (gm.member_type = 'role' AND (gm.member_value = ? OR gm.member_value = '*'))
		)
	)`
	// Use forAgent for agent match, forAgentRole for role match
	agentVal := forAgent
	if agentVal == "" {
		agentVal = forAgentRole
	}
	roleVal := forAgentRole
	if roleVal == "" {
		roleVal = forAgent
	}
	args = append(args, agentVal, roleVal)

	// Part 3: backward compat — old broadcast messages (no mention refs AND no group scopes)
	// TODO: remove in next release after groups are established
	broadcastSubquery := "(m.message_id NOT IN (SELECT mr_fa.message_id FROM message_refs mr_fa WHERE mr_fa.ref_type = 'mention')" +
		" AND m.message_id NOT IN (SELECT ms_bc.message_id FROM message_scopes ms_bc WHERE ms_bc.scope_type = 'group'))"

	clause := " AND (" + mentionSubquery + " OR " + groupSubquery + " OR " + broadcastSubquery + ")"
	return clause, args
}

// buildForAgentValues returns the unique set of values to match against mention refs
// for the for-agent inbox filter. Returns nil if no filtering should be applied.
// Only the agent's own name/ID is used for direct mention matching; role-based
// fan-out is handled via the group membership subquery in buildForAgentClause.
func buildForAgentValues(forAgent, _ string) []string {
	if forAgent != "" {
		return []string{forAgent}
	}
	return nil
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

	// Use context.Background() since this is called from methods that already have ctx
	// but this function doesn't have ctx parameter. We'll need to add ctx parameter.
	err = h.state.DB().QueryRowContext(context.Background(), query, agentID).Scan(&sessionID)
	if err == sql.ErrNoRows {
		return "", "", fmt.Errorf("no active session found for agent %s (you must start a session first)", agentID)
	}
	if err != nil {
		return "", "", fmt.Errorf("query active session: %w", err)
	}

	return agentID, sessionID, nil
}

// resolveAgentOnly resolves the caller's agent ID without requiring an active session.
// Used by HandleList for unread count and is_read computation where only the agent
// identity matters, not the session.
func (h *MessageHandler) resolveAgentOnly(callerAgentID string) string {
	if callerAgentID != "" {
		return callerAgentID
	}
	// Fallback: load identity from daemon's config
	cfg, err := config.LoadWithPath(h.state.RepoPath(), "", "")
	if err != nil {
		return ""
	}
	return identity.GenerateAgentID(h.state.RepoID(), cfg.Agent.Role, cfg.Agent.Module, cfg.Agent.Name)
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
	err := h.state.DB().QueryRowContext(context.Background(), query, targetID).Scan(&exists)
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

	tx, err := h.state.DB().BeginTx(ctx, nil)
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

	err = h.state.DB().QueryRowContext(context.Background(), query, threadID, threadID, sessionID, agentID, threadID).Scan(
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
	return h.dispatcher.DispatchThreadUpdated(context.Background(), info)
}

// HandleArchive handles the message.archive RPC method.
// It exports matching messages to a JSONL archive file, then hard-deletes them.
func (h *MessageHandler) HandleArchive(ctx context.Context, params json.RawMessage) (any, error) {
	var req ArchiveRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate archive_type
	if req.ArchiveType != "agent" && req.ArchiveType != "group" {
		return nil, fmt.Errorf("invalid archive_type: %q (must be \"agent\" or \"group\")", req.ArchiveType)
	}

	// Validate identifier
	if req.Identifier == "" {
		return nil, fmt.Errorf("identifier is required")
	}

	// Collect matching message IDs under a read lock
	h.state.RLock()

	var messageIDs []string
	var queryErr error

	switch req.ArchiveType {
	case "agent":
		rows, err := h.state.DB().QueryContext(ctx,
			`SELECT message_id FROM messages WHERE agent_id = ?`,
			req.Identifier)
		if err != nil {
			h.state.RUnlock()
			return nil, fmt.Errorf("query agent messages: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				h.state.RUnlock()
				return nil, fmt.Errorf("scan message_id: %w", err)
			}
			messageIDs = append(messageIDs, id)
		}
		queryErr = rows.Err()

	case "group":
		rows, err := h.state.DB().QueryContext(ctx,
			`SELECT m.message_id
			 FROM messages m
			 JOIN message_scopes ms ON m.message_id = ms.message_id
			 WHERE ms.scope_type = 'group' AND ms.scope_value = ?`,
			req.Identifier)
		if err != nil {
			h.state.RUnlock()
			return nil, fmt.Errorf("query group messages: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				h.state.RUnlock()
				return nil, fmt.Errorf("scan message_id: %w", err)
			}
			messageIDs = append(messageIDs, id)
		}
		queryErr = rows.Err()
	}

	if queryErr != nil {
		h.state.RUnlock()
		return nil, fmt.Errorf("iterate messages: %w", queryErr)
	}

	// If no messages match, return early
	if len(messageIDs) == 0 {
		h.state.RUnlock()
		archivePath := filepath.Join(h.state.RepoPath(), ".thrum", "archive", req.Identifier+".jsonl")
		return &ArchiveResponse{ArchivedCount: 0, ArchivePath: archivePath}, nil
	}

	// Build full archive records (message body + scopes + refs) under the same read lock
	records := make([]archiveRecord, 0, len(messageIDs))
	for _, msgID := range messageIDs {
		var rec archiveRecord
		rec.MessageID = msgID

		err := h.state.DB().QueryRowContext(ctx,
			`SELECT agent_id, created_at, body_format, body_content FROM messages WHERE message_id = ?`,
			msgID,
		).Scan(&rec.AgentID, &rec.CreatedAt, &rec.Body.Format, &rec.Body.Content)
		if err != nil {
			h.state.RUnlock()
			return nil, fmt.Errorf("query message %s: %w", msgID, err)
		}

		// Scopes
		scopeRows, err := h.state.DB().QueryContext(ctx,
			`SELECT scope_type, scope_value FROM message_scopes WHERE message_id = ?`, msgID)
		if err != nil {
			h.state.RUnlock()
			return nil, fmt.Errorf("query scopes for %s: %w", msgID, err)
		}
		rec.Scopes = []types.Scope{}
		for scopeRows.Next() {
			var s types.Scope
			if err := scopeRows.Scan(&s.Type, &s.Value); err != nil {
				_ = scopeRows.Close()
				h.state.RUnlock()
				return nil, fmt.Errorf("scan scope: %w", err)
			}
			rec.Scopes = append(rec.Scopes, s)
		}
		if err := scopeRows.Err(); err != nil {
			_ = scopeRows.Close()
			h.state.RUnlock()
			return nil, fmt.Errorf("iterate scopes: %w", err)
		}
		_ = scopeRows.Close()

		// Refs
		refRows, err := h.state.DB().QueryContext(ctx,
			`SELECT ref_type, ref_value FROM message_refs WHERE message_id = ?`, msgID)
		if err != nil {
			h.state.RUnlock()
			return nil, fmt.Errorf("query refs for %s: %w", msgID, err)
		}
		rec.Refs = []types.Ref{}
		for refRows.Next() {
			var r types.Ref
			if err := refRows.Scan(&r.Type, &r.Value); err != nil {
				_ = refRows.Close()
				h.state.RUnlock()
				return nil, fmt.Errorf("scan ref: %w", err)
			}
			rec.Refs = append(rec.Refs, r)
		}
		if err := refRows.Err(); err != nil {
			_ = refRows.Close()
			h.state.RUnlock()
			return nil, fmt.Errorf("iterate refs: %w", err)
		}
		_ = refRows.Close()

		records = append(records, rec)
	}

	h.state.RUnlock()

	// Create archive directory
	archiveDir := filepath.Join(h.state.RepoPath(), ".thrum", "archive")
	if err := os.MkdirAll(archiveDir, 0o750); err != nil {
		return nil, fmt.Errorf("create archive directory: %w", err)
	}

	// Write JSONL archive file
	archivePath := filepath.Join(archiveDir, req.Identifier+".jsonl")
	f, err := os.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open archive file: %w", err)
	}

	w := bufio.NewWriter(f)
	for _, rec := range records {
		line, err := json.Marshal(rec)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("marshal archive record: %w", err)
		}
		if _, err := w.Write(line); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("write archive line: %w", err)
		}
		if err := w.WriteByte('\n'); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("write newline: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flush archive file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close archive file: %w", err)
	}

	// Hard-delete the messages (related tables first, then messages)
	h.state.Lock()
	defer h.state.Unlock()

	tx, err := h.state.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}

	for _, msgID := range messageIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM message_scopes WHERE message_id = ?`, msgID); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("delete scopes for %s: %w", msgID, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM message_refs WHERE message_id = ?`, msgID); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("delete refs for %s: %w", msgID, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM message_reads WHERE message_id = ?`, msgID); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("delete reads for %s: %w", msgID, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM message_edits WHERE message_id = ?`, msgID); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("delete edits for %s: %w", msgID, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE message_id = ?`, msgID); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("delete message %s: %w", msgID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delete transaction: %w", err)
	}

	return &ArchiveResponse{
		ArchivedCount: len(records),
		ArchivePath:   archivePath,
	}, nil
}

// DeleteByScopeRequest represents the request for message.deleteByScope RPC.
type DeleteByScopeRequest struct {
	ScopeType  string `json:"scope_type"`  // e.g., "group"
	ScopeValue string `json:"scope_value"` // e.g., "backend"
}

// DeleteByScopeResponse represents the response from message.deleteByScope RPC.
type DeleteByScopeResponse struct {
	DeletedCount int `json:"deleted_count"`
}

// HandleDeleteByScope handles the message.deleteByScope RPC method.
// Hard deletes all messages that have a matching scope (type+value).
func (h *MessageHandler) HandleDeleteByScope(ctx context.Context, params json.RawMessage) (any, error) {
	var req DeleteByScopeRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.ScopeType == "" || req.ScopeValue == "" {
		return nil, fmt.Errorf("scope_type and scope_value are required")
	}

	h.state.Lock()
	defer h.state.Unlock()

	// Find all message_ids that match this scope
	rows, err := h.state.DB().QueryContext(ctx,
		"SELECT message_id FROM message_scopes WHERE scope_type = ? AND scope_value = ?",
		req.ScopeType, req.ScopeValue)
	if err != nil {
		return nil, fmt.Errorf("query scoped messages: %w", err)
	}
	defer rows.Close()

	var messageIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan message id: %w", err)
		}
		messageIDs = append(messageIDs, id)
	}

	if len(messageIDs) == 0 {
		return &DeleteByScopeResponse{DeletedCount: 0}, nil
	}

	// Build placeholders for IN clause
	placeholders := make([]string, len(messageIDs))
	args := make([]any, len(messageIDs))
	for i, id := range messageIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	inClause := strings.Join(placeholders, ",")

	// Delete from related tables first
	for _, table := range []string{"message_edits", "message_reads", "message_refs", "message_scopes"} {
		_, err = h.state.DB().ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE message_id IN (%s)", table, inClause),
			args...)
		if err != nil {
			return nil, fmt.Errorf("delete from %s: %w", table, err)
		}
	}

	// Delete the messages
	_, err = h.state.DB().ExecContext(ctx,
		fmt.Sprintf("DELETE FROM messages WHERE message_id IN (%s)", inClause),
		args...)
	if err != nil {
		return nil, fmt.Errorf("delete messages: %w", err)
	}

	return &DeleteByScopeResponse{DeletedCount: len(messageIDs)}, nil
}

// DeleteByAgentRequest represents the request for message.deleteByAgent RPC.
type DeleteByAgentRequest struct {
	AgentID string `json:"agent_id"`
}

// DeleteByAgentResponse represents the response from message.deleteByAgent RPC.
type DeleteByAgentResponse struct {
	DeletedCount int `json:"deleted_count"`
}

// HandleDeleteByAgent handles the message.deleteByAgent RPC method.
// Hard deletes all messages where agent_id matches the given agent.
func (h *MessageHandler) HandleDeleteByAgent(ctx context.Context, params json.RawMessage) (any, error) {
	var req DeleteByAgentRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}

	h.state.Lock()
	defer h.state.Unlock()

	// Get count first
	var count int
	err := h.state.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM messages WHERE agent_id = ?", req.AgentID).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("count messages: %w", err)
	}

	// Hard delete all messages by this agent.
	// Delete from child tables first to avoid FK constraint issues.
	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_edits WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("delete message edits: %w", err)
	}

	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_reads WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("delete message reads: %w", err)
	}

	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_refs WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("delete message refs: %w", err)
	}

	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_scopes WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("delete message scopes: %w", err)
	}

	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM messages WHERE agent_id = ?", req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("delete messages: %w", err)
	}

	return &DeleteByAgentResponse{DeletedCount: count}, nil
}

// DeleteByAgentRequest represents the request for message.deleteByAgent RPC.
type DeleteByAgentRequest struct {
	AgentID string `json:"agent_id"`
}

// DeleteByAgentResponse represents the response from message.deleteByAgent RPC.
type DeleteByAgentResponse struct {
	DeletedCount int `json:"deleted_count"`
}

// HandleDeleteByAgent handles the message.deleteByAgent RPC method.
// Hard deletes all messages where agent_id matches the given agent.
func (h *MessageHandler) HandleDeleteByAgent(ctx context.Context, params json.RawMessage) (any, error) {
	var req DeleteByAgentRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}

	h.state.Lock()
	defer h.state.Unlock()

	// Get count first
	var count int
	err := h.state.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM messages WHERE agent_id = ?", req.AgentID).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("count messages: %w", err)
	}

	// Hard delete all messages by this agent.
	// Delete from child tables first to avoid FK constraint issues.
	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_edits WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("delete message edits: %w", err)
	}

	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_reads WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("delete message reads: %w", err)
	}

	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_refs WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("delete message refs: %w", err)
	}

	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM message_scopes WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)", req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("delete message scopes: %w", err)
	}

	_, err = h.state.DB().ExecContext(ctx,
		"DELETE FROM messages WHERE agent_id = ?", req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("delete messages: %w", err)
	}

	return &DeleteByAgentResponse{DeletedCount: count}, nil
}
