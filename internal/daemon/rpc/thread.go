package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/types"
)

// CreateThreadRequest represents the request for thread.create RPC.
type CreateThreadRequest struct {
	Title         string          `json:"title"`
	Scopes        []types.Scope   `json:"scopes,omitempty"`
	Recipient     *string         `json:"recipient,omitempty"` // NEW: Agent ID to send initial message to
	Message       *MessageContent `json:"message,omitempty"`   // NEW: Initial message content
	ActingAs      *string         `json:"acting_as,omitempty"` // NEW: Impersonation (passed to message.send)
	Disclose      bool            `json:"disclose,omitempty"`  // NEW: Show via tag (passed to message.send)
	CallerAgentID string          `json:"caller_agent_id,omitempty"`
}

// MessageContent represents the content of a message for thread.create.
type MessageContent struct {
	Content    string         `json:"content"`
	Format     string         `json:"format,omitempty"` // default: "markdown"
	Structured map[string]any `json:"structured,omitempty"`
}

// CreateThreadResponse represents the response from thread.create RPC.
type CreateThreadResponse struct {
	ThreadID  string  `json:"thread_id"`
	CreatedAt string  `json:"created_at"`
	MessageID *string `json:"message_id,omitempty"` // NEW: ID of initial message if created
}

// ListThreadsRequest represents the request for thread.list RPC.
type ListThreadsRequest struct {
	Scope         *types.Scope `json:"scope,omitempty"`
	PageSize      int          `json:"page_size,omitempty"`
	Page          int          `json:"page,omitempty"`
	CallerAgentID string       `json:"caller_agent_id,omitempty"`
}

// ListThreadsResponse represents the response from thread.list RPC.
type ListThreadsResponse struct {
	Threads    []ThreadSummary `json:"threads"`
	Total      int             `json:"total"`
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	TotalPages int             `json:"total_pages"`
}

// ThreadSummary represents a summary of a thread for listing.
type ThreadSummary struct {
	ThreadID     string  `json:"thread_id"`
	Title        string  `json:"title"`
	MessageCount int     `json:"message_count"`
	UnreadCount  int     `json:"unread_count"` // NEW: Count of messages not read by session or agent
	LastActivity string  `json:"last_activity"`
	LastSender   string  `json:"last_sender"`       // NEW: Agent ID of last message author
	Preview      *string `json:"preview,omitempty"` // NEW: First 100 chars of last message
	CreatedBy    string  `json:"created_by"`
	CreatedAt    string  `json:"created_at"`
}

// GetThreadRequest represents the request for thread.get RPC.
type GetThreadRequest struct {
	ThreadID string `json:"thread_id"`
	PageSize int    `json:"page_size,omitempty"`
	Page     int    `json:"page,omitempty"`
}

// GetThreadResponse represents the response from thread.get RPC.
type GetThreadResponse struct {
	Thread     ThreadDetail     `json:"thread"`
	Messages   []MessageSummary `json:"messages"`
	Total      int              `json:"total"`
	Page       int              `json:"page"`
	PageSize   int              `json:"page_size"`
	TotalPages int              `json:"total_pages"`
}

// ThreadDetail represents detailed information about a thread.
type ThreadDetail struct {
	ThreadID  string `json:"thread_id"`
	Title     string `json:"title"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
}

// ThreadHandler handles thread-related RPC methods.
type ThreadHandler struct {
	state *state.State
}

// NewThreadHandler creates a new thread handler.
func NewThreadHandler(state *state.State) *ThreadHandler {
	return &ThreadHandler{state: state}
}

// resolveAgentAndSession returns the current agent ID and session ID.
func (h *ThreadHandler) resolveAgentAndSession(callerAgentID string) (agentID string, sessionID string, err error) {
	if callerAgentID != "" {
		agentID = callerAgentID
	} else {
		// Fallback: load identity from daemon's config (single-worktree backward compat)
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

// HandleCreate handles the thread.create RPC method.
func (h *ThreadHandler) HandleCreate(ctx context.Context, params json.RawMessage) (any, error) {
	var req CreateThreadRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	// Validate recipient/message consistency
	if (req.Recipient != nil && req.Message == nil) || (req.Recipient == nil && req.Message != nil) {
		return nil, fmt.Errorf("recipient and message must both be provided or both be nil")
	}

	// Generate thread ID
	threadID := identity.GenerateThreadID()

	// Resolve current agent
	agentID, _, err := h.resolveAgentAndSession(req.CallerAgentID)
	if err != nil {
		return nil, fmt.Errorf("resolve agent: %w", err)
	}

	// Prepare timestamp
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Build thread.create event
	event := types.ThreadCreateEvent{
		Type:      "thread.create",
		Timestamp: now,
		ThreadID:  threadID,
		Title:     req.Title,
		CreatedBy: agentID,
	}

	// Write event to JSONL and SQLite
	h.state.Lock()
	defer h.state.Unlock()

	if err := h.state.WriteEvent(event); err != nil {
		return nil, fmt.Errorf("write thread.create event: %w", err)
	}

	// TODO: Store thread scopes (requires thread_scopes table - not in current schema)

	resp := &CreateThreadResponse{
		ThreadID:  threadID,
		CreatedAt: now,
	}

	// Create initial message if provided
	if req.Recipient != nil && req.Message != nil {
		// Build SendRequest for initial message
		messageFormat := req.Message.Format
		if messageFormat == "" {
			messageFormat = "markdown"
		}

		sendReq := SendRequest{
			Content:    req.Message.Content,
			Format:     messageFormat,
			Structured: req.Message.Structured,
			ThreadID:   threadID,
		}

		// Handle impersonation if provided
		if req.ActingAs != nil && *req.ActingAs != "" {
			sendReq.ActingAs = *req.ActingAs
			sendReq.Disclose = req.Disclose
		}

		// Create message handler to send initial message
		messageHandler := NewMessageHandler(h.state)

		// Marshal send request
		sendParams, err := json.Marshal(sendReq)
		if err != nil {
			return nil, fmt.Errorf("marshal send request: %w", err)
		}

		// Send initial message (this will unlock/lock state internally, so we need to unlock first)
		h.state.Unlock()
		sendResp, err := messageHandler.HandleSend(ctx, sendParams)
		h.state.Lock()

		if err != nil {
			return nil, fmt.Errorf("send initial message: %w", err)
		}

		// Extract message ID from response
		if msgResp, ok := sendResp.(*SendResponse); ok {
			resp.MessageID = &msgResp.MessageID
		}
	}

	return resp, nil
}

// HandleList handles the thread.list RPC method.
func (h *ThreadHandler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	var req ListThreadsRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Set defaults
	pageSize := req.PageSize
	if pageSize == 0 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	page := req.Page
	if page == 0 {
		page = 1
	}

	// Resolve current agent and session for unread count
	agentID, sessionID, err := h.resolveAgentAndSession(req.CallerAgentID)
	if err != nil {
		return nil, fmt.Errorf("resolve agent and session: %w", err)
	}

	h.state.RLock()
	defer h.state.RUnlock()

	// Build enhanced query with unread_count, last_sender, preview
	query := `SELECT
	    t.thread_id,
	    t.title,
	    t.created_by,
	    t.created_at,
	    COUNT(DISTINCT m.message_id) as message_count,
	    COUNT(DISTINCT CASE
	        WHEN mr.message_id IS NULL AND m.deleted = 0 THEN m.message_id
	        END) as unread_count,
	    COALESCE(MAX(m.created_at), t.created_at) as last_activity,
	    (SELECT agent_id FROM messages
	     WHERE thread_id = t.thread_id AND deleted = 0
	     ORDER BY created_at DESC LIMIT 1) as last_sender,
	    (SELECT SUBSTR(body_content, 1, 100)
	     FROM messages
	     WHERE thread_id = t.thread_id AND deleted = 0
	     ORDER BY created_at DESC LIMIT 1) as preview
	FROM threads t
	LEFT JOIN messages m ON t.thread_id = m.thread_id AND m.deleted = 0
	LEFT JOIN message_reads mr ON m.message_id = mr.message_id
	    AND (mr.session_id = ? OR mr.agent_id = ?)
	WHERE 1=1`

	args := []any{sessionID, agentID}

	// TODO: Filter by scope (requires thread_scopes table)

	query += ` GROUP BY t.thread_id, t.title, t.created_by, t.created_at
	           ORDER BY last_activity DESC`

	// Count total threads
	countQuery := `SELECT COUNT(*) FROM threads WHERE 1=1`
	var total int
	if err := h.state.DB().QueryRow(countQuery).Scan(&total); err != nil {
		return nil, fmt.Errorf("count threads: %w", err)
	}

	// Calculate pagination
	offset := (page - 1) * pageSize
	totalPages := (total + pageSize - 1) / pageSize

	// Add pagination
	query += " LIMIT ? OFFSET ?"
	args = append(args, pageSize, offset)

	// Execute query
	rows, err := h.state.DB().Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query threads: %w", err)
	}
	defer func() { _ = rows.Close() }()

	threads := []ThreadSummary{}
	for rows.Next() {
		var thread ThreadSummary
		var lastSender, preview sql.NullString

		if err := rows.Scan(
			&thread.ThreadID,
			&thread.Title,
			&thread.CreatedBy,
			&thread.CreatedAt,
			&thread.MessageCount,
			&thread.UnreadCount,
			&thread.LastActivity,
			&lastSender,
			&preview,
		); err != nil {
			return nil, fmt.Errorf("scan thread: %w", err)
		}

		// Handle nullable fields
		if lastSender.Valid {
			thread.LastSender = lastSender.String
		}
		if preview.Valid && preview.String != "" {
			previewStr := preview.String
			thread.Preview = &previewStr
		}

		threads = append(threads, thread)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate threads: %w", err)
	}

	return &ListThreadsResponse{
		Threads:    threads,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}, nil
}

// HandleGet handles the thread.get RPC method.
func (h *ThreadHandler) HandleGet(ctx context.Context, params json.RawMessage) (any, error) {
	var req GetThreadRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate required fields
	if req.ThreadID == "" {
		return nil, fmt.Errorf("thread_id is required")
	}

	// Set defaults
	pageSize := req.PageSize
	if pageSize == 0 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	page := req.Page
	if page == 0 {
		page = 1
	}

	h.state.RLock()
	defer h.state.RUnlock()

	// Query thread details
	threadQuery := `SELECT thread_id, title, created_by, created_at
	                FROM threads
	                WHERE thread_id = ?`

	var thread ThreadDetail
	err := h.state.DB().QueryRow(threadQuery, req.ThreadID).Scan(
		&thread.ThreadID,
		&thread.Title,
		&thread.CreatedBy,
		&thread.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("thread not found: %s", req.ThreadID)
	}
	if err != nil {
		return nil, fmt.Errorf("query thread: %w", err)
	}

	// Count total messages in thread
	var total int
	countQuery := `SELECT COUNT(*) FROM messages WHERE thread_id = ?`
	if err := h.state.DB().QueryRow(countQuery, req.ThreadID).Scan(&total); err != nil {
		return nil, fmt.Errorf("count messages: %w", err)
	}

	// Calculate pagination
	offset := (page - 1) * pageSize
	totalPages := (total + pageSize - 1) / pageSize

	// Query messages in thread
	messagesQuery := `SELECT message_id, thread_id, agent_id, created_at,
	                         body_format, body_content, body_structured, deleted
	                  FROM messages
	                  WHERE thread_id = ?
	                  ORDER BY created_at ASC
	                  LIMIT ? OFFSET ?`

	rows, err := h.state.DB().Query(messagesQuery, req.ThreadID, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	messages := []MessageSummary{}
	for rows.Next() {
		var msg MessageSummary
		var threadID, bodyStructured sql.NullString
		var deleted int

		if err := rows.Scan(
			&msg.MessageID,
			&threadID,
			&msg.AgentID,
			&msg.CreatedAt,
			&msg.Body.Format,
			&msg.Body.Content,
			&bodyStructured,
			&deleted,
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

		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}

	return &GetThreadResponse{
		Thread:     thread,
		Messages:   messages,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}, nil
}
