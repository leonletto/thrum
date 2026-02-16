package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/subscriptions"
	"github.com/leonletto/thrum/internal/types"
)

// SubscribeRequest represents the request for subscriptions.subscribe RPC.
type SubscribeRequest struct {
	Scope         *types.Scope `json:"scope,omitempty"`        // e.g., {type: "module", value: "auth"}
	MentionRole   *string      `json:"mention_role,omitempty"` // e.g., "implementer"
	All           bool         `json:"all,omitempty"`          // Subscribe to everything (firehose)
	CallerAgentID string       `json:"caller_agent_id,omitempty"`
}

// SubscribeResponse represents the response from subscriptions.subscribe RPC.
type SubscribeResponse struct {
	SubscriptionID int    `json:"subscription_id"`
	SessionID      string `json:"session_id"`
	CreatedAt      string `json:"created_at"`
}

// UnsubscribeRequest represents the request for subscriptions.unsubscribe RPC.
type UnsubscribeRequest struct {
	SubscriptionID int    `json:"subscription_id"`
	CallerAgentID  string `json:"caller_agent_id,omitempty"`
}

// UnsubscribeResponse represents the response from subscriptions.unsubscribe RPC.
type UnsubscribeResponse struct {
	Removed bool `json:"removed"`
}

// ListSubscriptionsRequest represents the request for subscriptions.list RPC.
type ListSubscriptionsRequest struct {
	CallerAgentID string `json:"caller_agent_id,omitempty"`
}

// ListSubscriptionsResponse represents the response from subscriptions.list RPC.
type ListSubscriptionsResponse struct {
	Subscriptions []SubscriptionInfo `json:"subscriptions"`
}

// SubscriptionInfo represents information about a subscription.
type SubscriptionInfo struct {
	ID          int    `json:"id"`
	ScopeType   string `json:"scope_type,omitempty"`
	ScopeValue  string `json:"scope_value,omitempty"`
	MentionRole string `json:"mention_role,omitempty"`
	All         bool   `json:"all,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// SubscriptionHandler handles subscription-related RPC methods.
type SubscriptionHandler struct {
	state *state.State
	svc   *subscriptions.Service
}

// NewSubscriptionHandler creates a new subscription handler.
func NewSubscriptionHandler(state *state.State) *SubscriptionHandler {
	return &SubscriptionHandler{
		state: state,
		svc:   subscriptions.NewService(state.DB()),
	}
}

// HandleSubscribe handles the subscriptions.subscribe RPC method.
func (h *SubscriptionHandler) HandleSubscribe(ctx context.Context, params json.RawMessage) (any, error) {
	var req SubscribeRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validation: at least one of scope, mention_role, or all must be specified
	if req.Scope == nil && req.MentionRole == nil && !req.All {
		return nil, fmt.Errorf("at least one of scope, mention_role, or all must be specified")
	}

	// Get current session
	sessionID, err := h.getCurrentSession(ctx, req.CallerAgentID)
	if err != nil {
		return nil, fmt.Errorf("get current session: %w", err)
	}

	// Create subscription
	h.state.Lock()
	defer h.state.Unlock()

	sub, err := h.svc.Subscribe(ctx, sessionID, req.Scope, req.MentionRole, req.All)
	if err != nil {
		return nil, fmt.Errorf("subscribe: %w", err)
	}

	return &SubscribeResponse{
		SubscriptionID: sub.ID,
		SessionID:      sub.SessionID,
		CreatedAt:      sub.CreatedAt,
	}, nil
}

// HandleUnsubscribe handles the subscriptions.unsubscribe RPC method.
func (h *SubscriptionHandler) HandleUnsubscribe(ctx context.Context, params json.RawMessage) (any, error) {
	var req UnsubscribeRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validation
	if req.SubscriptionID == 0 {
		return nil, fmt.Errorf("subscription_id is required")
	}

	// Get current session
	sessionID, err := h.getCurrentSession(ctx, req.CallerAgentID)
	if err != nil {
		return nil, fmt.Errorf("get current session: %w", err)
	}

	// Remove subscription (only if it belongs to current session)
	h.state.Lock()
	defer h.state.Unlock()

	removed, err := h.svc.Unsubscribe(ctx, req.SubscriptionID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("unsubscribe: %w", err)
	}

	return &UnsubscribeResponse{
		Removed: removed,
	}, nil
}

// HandleList handles the subscriptions.list RPC method.
func (h *SubscriptionHandler) HandleList(ctx context.Context, params json.RawMessage) (any, error) {
	var req ListSubscriptionsRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Get current session
	sessionID, err := h.getCurrentSession(ctx, req.CallerAgentID)
	if err != nil {
		return nil, fmt.Errorf("get current session: %w", err)
	}

	// List subscriptions for current session
	h.state.RLock()
	defer h.state.RUnlock()

	subs, err := h.svc.List(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}

	// Convert to response format
	var infos []SubscriptionInfo
	for _, sub := range subs {
		info := SubscriptionInfo{
			ID:        sub.ID,
			CreatedAt: sub.CreatedAt,
		}

		// Check if this is an "all" subscription (all fields NULL)
		if sub.ScopeType == nil && sub.ScopeValue == nil && sub.MentionRole == nil {
			info.All = true
		} else {
			// Set scope fields if present
			if sub.ScopeType != nil {
				info.ScopeType = *sub.ScopeType
			}
			if sub.ScopeValue != nil {
				info.ScopeValue = *sub.ScopeValue
			}
			// Set mention_role if present
			if sub.MentionRole != nil {
				info.MentionRole = *sub.MentionRole
			}
		}

		infos = append(infos, info)
	}

	return &ListSubscriptionsResponse{
		Subscriptions: infos,
	}, nil
}

// getCurrentSession retrieves the current active session for the calling agent.
func (h *SubscriptionHandler) getCurrentSession(ctx context.Context, callerAgentID string) (string, error) {
	var agentID string
	if callerAgentID != "" {
		agentID = callerAgentID
	} else {
		// Fallback: load identity from daemon's config (single-worktree backward compat)
		cfg, err := config.LoadWithPath(h.state.RepoPath(), "", "")
		if err != nil {
			return "", fmt.Errorf("load config: %w", err)
		}
		agentID = identity.GenerateAgentID(h.state.RepoID(), cfg.Agent.Role, cfg.Agent.Module, cfg.Agent.Name)
	}

	// Query for active session
	query := `SELECT session_id FROM sessions
	          WHERE agent_id = ? AND ended_at IS NULL
	          ORDER BY started_at DESC
	          LIMIT 1`

	var sessionID string
	sessionErr := h.state.DB().QueryRowContext(ctx, query, agentID).Scan(&sessionID)
	if sessionErr == sql.ErrNoRows {
		return "", fmt.Errorf("no active session found for agent %s", agentID)
	}
	if sessionErr != nil {
		return "", fmt.Errorf("query active session: %w", sessionErr)
	}

	return sessionID, nil
}
