package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	agentcontext "github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// ContextSaveRequest is the request for context.save.
type ContextSaveRequest struct {
	AgentName string `json:"agent_name"`
	Content   []byte `json:"content"`
}

// ContextSaveResponse is the response for context.save.
type ContextSaveResponse struct {
	AgentName string `json:"agent_name"`
	Message   string `json:"message"`
}

// ContextShowRequest is the request for context.show.
type ContextShowRequest struct {
	AgentName string `json:"agent_name"`
}

// ContextShowResponse is the response for context.show.
type ContextShowResponse struct {
	AgentName  string `json:"agent_name"`
	Content    []byte `json:"content"`
	HasContext bool   `json:"has_context"`
	Size       int64  `json:"size,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// ContextClearRequest is the request for context.clear.
type ContextClearRequest struct {
	AgentName string `json:"agent_name"`
}

// ContextClearResponse is the response for context.clear.
type ContextClearResponse struct {
	AgentName string `json:"agent_name"`
	Message   string `json:"message"`
}

// ContextHandler handles context-related RPC methods.
type ContextHandler struct {
	state *state.State
}

// NewContextHandler creates a new context handler.
func NewContextHandler(state *state.State) *ContextHandler {
	return &ContextHandler{state: state}
}

// HandleSave handles the context.save RPC method.
func (h *ContextHandler) HandleSave(ctx context.Context, params json.RawMessage) (any, error) {
	var req ContextSaveRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.AgentName == "" {
		return nil, errors.New("agent_name is required")
	}

	h.state.Lock()
	defer h.state.Unlock()

	thrumDir := filepath.Join(h.state.RepoPath(), ".thrum")

	if err := agentcontext.Save(thrumDir, req.AgentName, req.Content); err != nil {
		return nil, fmt.Errorf("save context: %w", err)
	}

	return &ContextSaveResponse{
		AgentName: req.AgentName,
		Message:   fmt.Sprintf("Context saved for %s (%d bytes)", req.AgentName, len(req.Content)),
	}, nil
}

// HandleShow handles the context.show RPC method.
func (h *ContextHandler) HandleShow(ctx context.Context, params json.RawMessage) (any, error) {
	var req ContextShowRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.AgentName == "" {
		return nil, errors.New("agent_name is required")
	}

	h.state.RLock()
	defer h.state.RUnlock()

	thrumDir := filepath.Join(h.state.RepoPath(), ".thrum")

	content, err := agentcontext.Load(thrumDir, req.AgentName)
	if err != nil {
		return nil, fmt.Errorf("load context: %w", err)
	}

	if len(content) == 0 {
		return &ContextShowResponse{
			AgentName:  req.AgentName,
			HasContext: false,
		}, nil
	}

	contextPath := agentcontext.ContextPath(thrumDir, req.AgentName)
	stat, _ := os.Stat(contextPath)

	resp := &ContextShowResponse{
		AgentName:  req.AgentName,
		Content:    content,
		HasContext: true,
	}
	if stat != nil {
		resp.Size = stat.Size()
		resp.UpdatedAt = stat.ModTime().Format(time.RFC3339)
	}

	return resp, nil
}

// HandleClear handles the context.clear RPC method.
func (h *ContextHandler) HandleClear(ctx context.Context, params json.RawMessage) (any, error) {
	var req ContextClearRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.AgentName == "" {
		return nil, errors.New("agent_name is required")
	}

	h.state.Lock()
	defer h.state.Unlock()

	thrumDir := filepath.Join(h.state.RepoPath(), ".thrum")

	if err := agentcontext.Clear(thrumDir, req.AgentName); err != nil {
		return nil, fmt.Errorf("clear context: %w", err)
	}

	return &ContextClearResponse{
		AgentName: req.AgentName,
		Message:   fmt.Sprintf("Context cleared for %s", req.AgentName),
	}, nil
}
