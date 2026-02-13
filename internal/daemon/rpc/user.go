package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/transport"
	"github.com/leonletto/thrum/internal/types"
)

// UserHandler handles user-related RPC methods.
type UserHandler struct {
	state *state.State
}

// NewUserHandler creates a new user handler.
func NewUserHandler(state *state.State) *UserHandler {
	return &UserHandler{
		state: state,
	}
}

// RegisterUserRequest represents the request for user.register RPC.
type RegisterUserRequest struct {
	Username string `json:"username"` // e.g., "leon" -> becomes "user:leon"
	Display  string `json:"display,omitempty"`
}

// RegisterUserResponse represents the response from user.register RPC.
type RegisterUserResponse struct {
	UserID      string `json:"user_id"`                // "user:leon"
	Username    string `json:"username"`               // "leon"
	DisplayName string `json:"display_name,omitempty"` // "Leon Letto"
	Token       string `json:"token"`                  // session token for reconnection
	Status      string `json:"status"`                 // "registered" or "existing"
}

// IdentifyResponse represents the response from user.identify RPC.
type IdentifyResponse struct {
	Username string `json:"username"` // sanitized git user.name
	Email    string `json:"email"`    // git user.email
	Display  string `json:"display"`  // raw git user.name
}

// ValidUsername validates a username.
// Must be alphanumeric, underscore, or hyphen. Length 1-32.
var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

// HandleIdentify handles the user.identify RPC method.
// Returns git user info from the repo's git config for auto-registration.
func (h *UserHandler) HandleIdentify(ctx context.Context, params json.RawMessage) (any, error) {
	repoPath := h.state.RepoPath()

	name, err := gitConfigValue(repoPath, "user.name")
	if err != nil || name == "" {
		return nil, fmt.Errorf("git config user.name not set: configure with 'git config user.name \"Your Name\"'")
	}

	email, _ := gitConfigValue(repoPath, "user.email")

	return &IdentifyResponse{
		Username: sanitizeUsername(name),
		Email:    email,
		Display:  name,
	}, nil
}

// HandleRegister handles the user.register RPC method.
// Idempotent: if the user already exists, returns existing info with a fresh token.
func (h *UserHandler) HandleRegister(ctx context.Context, params json.RawMessage) (any, error) {
	// Check transport - only WebSocket allowed
	if t := transport.GetTransport(ctx); t != transport.TransportWebSocket {
		return nil, &RPCError{
			Code:    -32001,
			Message: "User registration only available via WebSocket",
			Data:    fmt.Sprintf("current transport: %s", t.String()),
		}
	}

	var req RegisterUserRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Validate username
	if req.Username == "" {
		return nil, fmt.Errorf("username is required")
	}

	// Prevent agent: namespace confusion (check BEFORE regex validation)
	if len(req.Username) >= 6 && req.Username[:6] == "agent:" {
		return nil, fmt.Errorf("username cannot start with 'agent:' prefix")
	}

	if !usernameRegex.MatchString(req.Username) {
		return nil, fmt.Errorf("invalid username format: must be alphanumeric, underscore, or hyphen (1-32 chars)")
	}

	// Generate user ID
	userID := identity.GenerateUserID(req.Username)

	// Lock for conflict detection and registration
	h.state.Lock()
	defer h.state.Unlock()

	// Check for existing user
	existingUser, err := h.getUserByID(userID)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("check for existing user: %w", err)
	}

	// User already exists — return existing info (idempotent)
	if existingUser != nil {
		token := identity.GenerateSessionToken()
		display := existingUser.Display
		if req.Display != "" {
			display = req.Display
		}
		return &RegisterUserResponse{
			UserID:      userID,
			Username:    req.Username,
			DisplayName: display,
			Token:       token,
			Status:      "existing",
		}, nil
	}

	// Register new user
	return h.registerUser(userID, req.Username, req.Display)
}

// getUserByID retrieves a user by ID from the database.
func (h *UserHandler) getUserByID(userID string) (*AgentInfo, error) {
	query := `SELECT agent_id, kind, role, module, display, registered_at, last_seen_at
	          FROM agents WHERE agent_id = ?`

	var agent AgentInfo
	var display, lastSeenAt sql.NullString

	err := h.state.DB().QueryRow(query, userID).Scan(
		&agent.AgentID,
		&agent.Kind,
		&agent.Role,
		&agent.Module,
		&display,
		&agent.RegisteredAt,
		&lastSeenAt,
	)

	if err == sql.ErrNoRows {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("query user: %w", err)
	}

	if display.Valid {
		agent.Display = display.String
	}
	if lastSeenAt.Valid {
		agent.LastSeenAt = lastSeenAt.String
	}

	return &agent, nil
}

// registerUser writes a user.register event (stored as agent.register with kind="user").
func (h *UserHandler) registerUser(userID, username, display string) (*RegisterUserResponse, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Create agent.register event with kind="user"
	event := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: now,
		AgentID:   userID,
		Kind:      "user",
		Role:      username, // Store username in role field
		Module:    "ui",     // All users are from UI
		Display:   display,
	}

	// Write event to JSONL and SQLite
	if err := h.state.WriteEvent(event); err != nil {
		return nil, fmt.Errorf("write user.register event: %w", err)
	}

	// Generate session token for reconnection
	token := identity.GenerateSessionToken()

	return &RegisterUserResponse{
		UserID:      userID,
		Username:    username,
		DisplayName: display,
		Token:       token,
		Status:      "registered",
	}, nil
}

// sanitizeUsername converts a display name to a valid username.
// Replaces spaces with hyphens, strips non-alphanumeric chars, lowercases, truncates to 32.
func sanitizeUsername(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
		if b.Len() >= 32 {
			break
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "user"
	}
	return result
}

// gitConfigValue runs git config to get a value from the repo's git config.
func gitConfigValue(repoPath, key string) (string, error) {
	cmd := exec.Command("git", "config", "--get", key)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// RPCError represents a JSON-RPC error with custom code.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *RPCError) Error() string {
	return e.Message
}
