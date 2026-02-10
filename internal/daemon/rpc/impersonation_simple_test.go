package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
)

func TestImpersonation_ValidateUserCanImpersonateAgent(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum directory: %v", err)
	}

	repoID := "r_TEST12345678"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Register a test agent to impersonate
	agentHandler := NewAgentHandler(st)
	agentReq := RegisterRequest{
		Role:   "assistant",
		Module: "claude",
	}
	agentReqJSON, err := json.Marshal(agentReq)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	agentResp, err := agentHandler.HandleRegister(context.Background(), agentReqJSON)
	if err != nil {
		t.Fatalf("failed to register agent: %v", err)
	}
	regResp, ok := agentResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("failed to assert response type: expected *RegisterResponse, got %T", agentResp)
	}
	targetAgentID := regResp.AgentID

	// Create handler
	handler := NewMessageHandler(st)

	// Test: User can impersonate agent
	userID := "user:testuser"
	err = handler.validateImpersonation(userID, targetAgentID)
	if err != nil {
		t.Errorf("Expected no error for user impersonating agent, got: %v", err)
	}
}

func TestImpersonation_ValidateAgentCannotImpersonate(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum directory: %v", err)
	}

	repoID := "r_TEST12345678"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Register a test agent
	agentHandler := NewAgentHandler(st)
	agentReq := RegisterRequest{
		Role:   "assistant",
		Module: "claude",
	}
	agentReqJSON, err := json.Marshal(agentReq)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	agentResp, err := agentHandler.HandleRegister(context.Background(), agentReqJSON)
	if err != nil {
		t.Fatalf("failed to register agent: %v", err)
	}
	regResp, ok := agentResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("failed to assert response type: expected *RegisterResponse, got %T", agentResp)
	}
	targetAgentID := regResp.AgentID

	// Create handler
	handler := NewMessageHandler(st)

	// Test: Agent cannot impersonate another agent
	callerAgentID := identity.GenerateAgentID(repoID, "caller", "test", "")
	err = handler.validateImpersonation(callerAgentID, targetAgentID)
	if err == nil {
		t.Fatal("Expected error when agent tries to impersonate, got nil")
	}

	if !strings.Contains(err.Error(), "only users can impersonate") {
		t.Errorf("Expected error about only users can impersonate, got: %v", err)
	}
}

func TestImpersonation_ValidateUserCannotImpersonateUser(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum directory: %v", err)
	}

	repoID := "r_TEST12345678"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Create handler
	handler := NewMessageHandler(st)

	// Test: User cannot impersonate another user
	userID := "user:testuser"
	targetUserID := "user:otheruser"
	err = handler.validateImpersonation(userID, targetUserID)
	if err == nil {
		t.Fatal("Expected error when user tries to impersonate another user, got nil")
	}

	if !strings.Contains(err.Error(), "users can only impersonate agents") {
		t.Errorf("Expected error about users can only impersonate agents, got: %v", err)
	}
}

func TestImpersonation_ValidateNonexistentAgent(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum directory: %v", err)
	}

	repoID := "r_TEST12345678"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Create handler
	handler := NewMessageHandler(st)

	// Test: User cannot impersonate non-existent agent
	userID := "user:testuser"
	nonexistentAgentID := "agent:nonexistent:fake:FAKEHASH"
	err = handler.validateImpersonation(userID, nonexistentAgentID)
	if err == nil {
		t.Fatal("Expected error when impersonating non-existent agent, got nil")
	}

	if !strings.Contains(err.Error(), "target agent does not exist") {
		t.Errorf("Expected error about non-existent agent, got: %v", err)
	}
}

func TestImpersonation_MessageCreateEventFields(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum directory: %v", err)
	}

	repoID := "r_TEST12345678"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Set up test identity
	t.Setenv("THRUM_ROLE", "testagent")
	t.Setenv("THRUM_MODULE", "test")

	// Register agent
	agentHandler := NewAgentHandler(st)
	agentReq := RegisterRequest{
		Role:   "testagent",
		Module: "test",
	}
	agentReqJSON, err := json.Marshal(agentReq)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	agentResp, err := agentHandler.HandleRegister(context.Background(), agentReqJSON)
	if err != nil {
		t.Fatalf("failed to register agent: %v", err)
	}
	regResp, ok := agentResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("failed to assert response type: expected *RegisterResponse, got %T", agentResp)
	}
	agentID := regResp.AgentID

	// Start session
	sessionHandler := NewSessionHandler(st)
	sessionReq := SessionStartRequest{
		AgentID: agentID,
	}
	sessionReqJSON, err := json.Marshal(sessionReq)
	if err != nil {
		t.Fatalf("failed to marshal session request: %v", err)
	}
	_, err = sessionHandler.HandleStart(context.Background(), sessionReqJSON)
	if err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	// Create message handler
	handler := NewMessageHandler(st)

	// Send normal message (no impersonation)
	sendReq := SendRequest{
		Content: "Normal message without impersonation",
	}
	sendReqJSON, err := json.Marshal(sendReq)
	if err != nil {
		t.Fatalf("failed to marshal send request: %v", err)
	}

	resp, err := handler.HandleSend(context.Background(), sendReqJSON)
	if err != nil {
		t.Fatalf("HandleSend failed: %v", err)
	}

	sendResp, ok := resp.(*SendResponse)
	if !ok {
		t.Fatalf("failed to assert response type: expected *SendResponse, got %T", resp)
	}

	// Verify authored_by is NULL and disclosed is 0
	var authoredBy sql.NullString
	var disclosed int
	query := `SELECT authored_by, disclosed FROM messages WHERE message_id = ?`
	err = st.DB().QueryRow(query, sendResp.MessageID).Scan(&authoredBy, &disclosed)
	if err != nil {
		t.Fatalf("failed to query message: %v", err)
	}

	if authoredBy.Valid {
		t.Errorf("Expected authored_by to be NULL for normal message, got %s", authoredBy.String)
	}

	if disclosed != 0 {
		t.Errorf("Expected disclosed=0 for normal message, got %d", disclosed)
	}
}
