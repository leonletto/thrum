package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/transport"
	"github.com/leonletto/thrum/internal/types"
)

func TestUserRegister_Success(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	handler := NewUserHandler(st)

	// Test successful registration via WebSocket
	req := RegisterUserRequest{
		Username: "leon",
		Display:  "Leon Letto",
	}

	params, _ := json.Marshal(req)
	ctx := transport.WithTransport(context.Background(), transport.TransportWebSocket)

	result, err := handler.HandleRegister(ctx, params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	resp, ok := result.(*RegisterUserResponse)
	if !ok {
		t.Fatalf("expected *RegisterUserResponse, got %T", result)
	}

	if resp.UserID != "user:leon" {
		t.Errorf("expected user ID 'user:leon', got %s", resp.UserID)
	}

	if resp.Username != "leon" {
		t.Errorf("expected username 'leon', got %s", resp.Username)
	}

	if resp.DisplayName != "Leon Letto" {
		t.Errorf("expected display name 'Leon Letto', got %s", resp.DisplayName)
	}

	if resp.Status != "registered" {
		t.Errorf("expected status 'registered', got %s", resp.Status)
	}

	if resp.Token == "" {
		t.Error("expected session token, got empty string")
	}
}

func TestUserRegister_UnixSocketRejection(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	handler := NewUserHandler(st)

	req := RegisterUserRequest{
		Username: "leon",
		Display:  "Leon Letto",
	}

	params, _ := json.Marshal(req)
	ctx := transport.WithTransport(context.Background(), transport.TransportUnixSocket)

	_, err = handler.HandleRegister(ctx, params)
	if err == nil {
		t.Fatal("expected error for Unix socket registration, got nil")
	}

	rpcErr, ok := err.(*RPCError)
	if !ok {
		t.Fatalf("expected *RPCError, got %T", err)
	}

	if rpcErr.Code != -32001 {
		t.Errorf("expected error code -32001, got %d", rpcErr.Code)
	}

	if rpcErr.Message != "User registration only available via WebSocket" {
		t.Errorf("unexpected error message: %s", rpcErr.Message)
	}
}

func TestUserRegister_IdempotentReRegistration(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Register user first time
	event := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-01-01T00:00:00Z",
		AgentID:   "user:leon",
		Kind:      "user",
		Role:      "leon",
		Module:    "ui",
		Display:   "Leon Letto",
	}

	st.Lock()
	if err := st.WriteEvent(context.Background(), event); err != nil {
		t.Fatalf("failed to write initial event: %v", err)
	}
	st.Unlock()

	// Re-register same user — should succeed (idempotent)
	handler := NewUserHandler(st)

	req := RegisterUserRequest{
		Username: "leon",
		Display:  "Leon Letto Updated",
	}

	params, _ := json.Marshal(req)
	ctx := transport.WithTransport(context.Background(), transport.TransportWebSocket)

	result, err := handler.HandleRegister(ctx, params)
	if err != nil {
		t.Fatalf("expected idempotent re-registration to succeed, got %v", err)
	}

	resp, ok := result.(*RegisterUserResponse)
	if !ok {
		t.Fatalf("expected *RegisterUserResponse, got %T", result)
	}

	if resp.UserID != "user:leon" {
		t.Errorf("expected user ID 'user:leon', got %s", resp.UserID)
	}

	if resp.Status != "existing" {
		t.Errorf("expected status 'existing', got %s", resp.Status)
	}

	if resp.Username != "leon" {
		t.Errorf("expected username 'leon', got %s", resp.Username)
	}

	if resp.Token == "" {
		t.Error("expected session token, got empty string")
	}
}

func TestSanitizeUsername(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Leon Letto", "leon-letto"},
		{"alice", "alice"},
		{"Bob O'Brien", "bob-obrien"},
		{"user@name.com", "usernamecom"}, // @ and . stripped
		{"  spaces  ", "spaces"},
		{"UPPERCASE", "uppercase"},
		{"a-b_c", "a-b-c"}, // _ maps to -
		{"", "user"},
		{"!!!!", "user"},
		{"León Ñoño", "león-ñoño"}, // unicode letters preserved
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeUsername(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeUsername(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestUserRegister_InvalidFormats(t *testing.T) {
	tests := []struct {
		name     string
		username string
		wantErr  string
	}{
		{
			name:     "empty",
			username: "",
			wantErr:  "username is required",
		},
		{
			name:     "too_long",
			username: "this_username_is_way_too_long_more_than_32_chars",
			wantErr:  "invalid username format",
		},
		{
			name:     "special_chars",
			username: "user@name",
			wantErr:  "invalid username format",
		},
		{
			name:     "spaces",
			username: "user name",
			wantErr:  "invalid username format",
		},
		{
			name:     "dots",
			username: "user.name",
			wantErr:  "invalid username format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			thrumDir := filepath.Join(tmpDir, ".thrum")
			if err := os.MkdirAll(thrumDir, 0750); err != nil {
				t.Fatalf("failed to create .thrum dir: %v", err)
			}

			st, err := state.NewState(thrumDir, thrumDir, "r_TEST123456")
			if err != nil {
				t.Fatalf("failed to create state: %v", err)
			}
			defer func() { _ = st.Close() }()

			handler := NewUserHandler(st)

			req := RegisterUserRequest{
				Username: tt.username,
			}

			params, _ := json.Marshal(req)
			ctx := transport.WithTransport(context.Background(), transport.TransportWebSocket)

			_, err = handler.HandleRegister(ctx, params)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			if err.Error() != tt.wantErr && !contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestUserRegister_NamespacePrefixEnforcement(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "r_TEST123456")
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	handler := NewUserHandler(st)

	req := RegisterUserRequest{
		Username: "agent:something",
	}

	params, _ := json.Marshal(req)
	ctx := transport.WithTransport(context.Background(), transport.TransportWebSocket)

	_, err = handler.HandleRegister(ctx, params)
	if err == nil {
		t.Fatal("expected error for agent: prefix, got nil")
	}

	if err.Error() != "username cannot start with 'agent:' prefix" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestUserRegister_ValidFormats(t *testing.T) {
	validUsernames := []string{
		"leon",
		"user123",
		"test_user",
		"my-user",
		"a",
		"12345",
		"_underscore",
		"-hyphen",
	}

	for _, username := range validUsernames {
		t.Run(username, func(t *testing.T) {
			tmpDir := t.TempDir()
			thrumDir := filepath.Join(tmpDir, ".thrum")
			if err := os.MkdirAll(thrumDir, 0750); err != nil {
				t.Fatalf("failed to create .thrum dir: %v", err)
			}

			st, err := state.NewState(thrumDir, thrumDir, "r_TEST123456")
			if err != nil {
				t.Fatalf("failed to create state: %v", err)
			}
			defer func() { _ = st.Close() }()

			handler := NewUserHandler(st)

			req := RegisterUserRequest{
				Username: username,
			}

			params, _ := json.Marshal(req)
			ctx := transport.WithTransport(context.Background(), transport.TransportWebSocket)

			result, err := handler.HandleRegister(ctx, params)
			if err != nil {
				t.Fatalf("expected no error for valid username %q, got %v", username, err)
			}

			resp, ok := result.(*RegisterUserResponse)
			if !ok {
				t.Fatalf("expected *RegisterUserResponse, got %T", result)
			}

			expectedID := "user:" + username
			if resp.UserID != expectedID {
				t.Errorf("expected user ID %q, got %q", expectedID, resp.UserID)
			}
		})
	}
}

// contains is a helper to check if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
