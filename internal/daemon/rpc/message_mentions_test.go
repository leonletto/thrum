package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
)

func TestMessageSend_WithMentions(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum directory: %v", err)
	}

	// Create state
	repoID := "r_TEST12345678"
	st, err := state.NewState(thrumDir, thrumDir, repoID, "")
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Set up test identity
	t.Setenv("THRUM_ROLE", "tester")
	t.Setenv("THRUM_MODULE", "test-module")

	// Register agent
	agentID := identity.GenerateAgentID(repoID, "tester", "test-module", "")
	agentHandler := NewAgentHandler(st)
	registerReq := RegisterRequest{
		Role:   "tester",
		Module: "test-module",
	}
	registerParams, _ := json.Marshal(registerReq)
	_, err = agentHandler.HandleRegister(context.Background(), registerParams)
	if err != nil {
		t.Fatalf("failed to register agent: %v", err)
	}

	// Register reviewer and implementer agents so recipient validation passes
	reviewerID := identity.GenerateAgentID(repoID, "reviewer", "test-module", "")
	reviewerParams, _ := json.Marshal(RegisterRequest{Role: "reviewer", Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), reviewerParams); err != nil {
		t.Fatalf("failed to register reviewer: %v", err)
	}
	implementerID := identity.GenerateAgentID(repoID, "implementer", "test-module", "")
	implementerParams, _ := json.Marshal(RegisterRequest{Role: "implementer", Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), implementerParams); err != nil {
		t.Fatalf("failed to register implementer: %v", err)
	}

	// Start session
	sessionHandler := NewSessionHandler(st)
	sessionReq := SessionStartRequest{
		AgentID: agentID,
	}
	sessionParams, _ := json.Marshal(sessionReq)
	_, err = sessionHandler.HandleStart(context.Background(), sessionParams)
	if err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	// Start sessions for reviewer and implementer
	reviewerSessionParams, _ := json.Marshal(SessionStartRequest{AgentID: reviewerID})
	if _, err := sessionHandler.HandleStart(context.Background(), reviewerSessionParams); err != nil {
		t.Fatalf("failed to start reviewer session: %v", err)
	}
	implementerSessionParams, _ := json.Marshal(SessionStartRequest{AgentID: implementerID})
	if _, err := sessionHandler.HandleStart(context.Background(), implementerSessionParams); err != nil {
		t.Fatalf("failed to start implementer session: %v", err)
	}

	// Create message handler
	handler := NewMessageHandler(st)

	// Send message with mentions
	req := SendRequest{
		Content:       "Hey @reviewer, please check this code",
		Format:        "markdown",
		Mentions:      []string{"@reviewer", "implementer"}, // Mix of @-prefixed and non-prefixed
		CallerAgentID: agentID,
	}
	params, _ := json.Marshal(req)

	resp, err := handler.HandleSend(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleSend failed: %v", err)
	}

	sendResp, ok := resp.(*SendResponse)
	if !ok {
		t.Fatalf("expected *SendResponse, got %T", resp)
	}

	// Without auto role groups, @reviewer and implementer resolve as role-based mentions.
	if sendResp.ResolvedTo < 2 {
		t.Errorf("expected at least 2 resolved mentions, got %d", sendResp.ResolvedTo)
	}

	// Verify mention refs were created (not group scopes — groups are no longer auto-created)
	query := `SELECT ref_type, ref_value FROM message_refs WHERE message_id = ? AND ref_type = 'mention' ORDER BY ref_value`
	rows, err := st.RawDB().Query(query, sendResp.MessageID)
	if err != nil {
		t.Fatalf("failed to query refs: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var mentionRefs []struct {
		Type  string
		Value string
	}
	for rows.Next() {
		var ref struct {
			Type  string
			Value string
		}
		if err := rows.Scan(&ref.Type, &ref.Value); err != nil {
			t.Fatalf("failed to scan ref: %v", err)
		}
		mentionRefs = append(mentionRefs, ref)
	}

	// Should have 2 mention refs (reviewer and implementer resolved as roles)
	if len(mentionRefs) != 2 {
		t.Errorf("expected 2 mention refs, got %d", len(mentionRefs))
	}

	expectedRefs := []struct {
		Type  string
		Value string
	}{
		{"mention", "implementer"},
		{"mention", "reviewer"},
	}

	for i, expected := range expectedRefs {
		if i >= len(mentionRefs) {
			break
		}
		if mentionRefs[i].Type != expected.Type {
			t.Errorf("ref[%d]: expected type=%s, got %s", i, expected.Type, mentionRefs[i].Type)
		}
		if mentionRefs[i].Value != expected.Value {
			t.Errorf("ref[%d]: expected value=%s, got %s", i, expected.Value, mentionRefs[i].Value)
		}
	}
}

func TestHandleSend_UnknownRecipient(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum directory: %v", err)
	}

	repoID := "r_TEST12345678"
	st, err := state.NewState(thrumDir, thrumDir, repoID, "")
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	t.Setenv("THRUM_ROLE", "tester")
	t.Setenv("THRUM_MODULE", "test-module")

	// Register a sender agent (so we have a valid session)
	agentID := identity.GenerateAgentID(repoID, "tester", "test-module", "")
	agentHandler := NewAgentHandler(st)
	registerParams, _ := json.Marshal(RegisterRequest{Role: "tester", Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), registerParams); err != nil {
		t.Fatalf("failed to register agent: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionParams, _ := json.Marshal(SessionStartRequest{AgentID: agentID})
	if _, err := sessionHandler.HandleStart(context.Background(), sessionParams); err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	handler := NewMessageHandler(st)

	t.Run("single unknown recipient", func(t *testing.T) {
		req := SendRequest{
			Content:       "hello",
			Format:        "markdown",
			Mentions:      []string{"@nonexistent"},
			CallerAgentID: agentID,
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleSend(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for unknown recipient, got nil")
		}
		if !strings.Contains(err.Error(), "unknown recipient") {
			t.Errorf("error should mention 'unknown recipient', got: %v", err)
		}
		if !strings.Contains(err.Error(), "@nonexistent") {
			t.Errorf("error should list '@nonexistent', got: %v", err)
		}
		if resp != nil {
			t.Error("response should be nil when error is returned")
		}

		// Verify no message was stored
		var count int
		_ = st.RawDB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
		if count != 0 {
			t.Errorf("expected 0 messages stored, got %d", count)
		}
	})

	t.Run("multiple unknown recipients", func(t *testing.T) {
		req := SendRequest{
			Content:       "hello",
			Format:        "markdown",
			Mentions:      []string{"@ghost1", "@ghost2"},
			CallerAgentID: agentID,
		}
		params, _ := json.Marshal(req)

		_, err := handler.HandleSend(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for unknown recipients, got nil")
		}
		if !strings.Contains(err.Error(), "@ghost1") {
			t.Errorf("error should list '@ghost1', got: %v", err)
		}
		if !strings.Contains(err.Error(), "@ghost2") {
			t.Errorf("error should list '@ghost2', got: %v", err)
		}
	})

	t.Run("mix of valid and unknown recipients", func(t *testing.T) {
		// "tester" is a valid role (registered above), "unknown" is not
		req := SendRequest{
			Content:       "hello",
			Format:        "markdown",
			Mentions:      []string{"@tester", "@unknown"},
			CallerAgentID: agentID,
		}
		params, _ := json.Marshal(req)

		_, err := handler.HandleSend(context.Background(), params)
		if err == nil {
			t.Fatal("expected error when any recipient is unknown, got nil")
		}
		if !strings.Contains(err.Error(), "@unknown") {
			t.Errorf("error should list '@unknown', got: %v", err)
		}

		// Verify no message stored (hard error = nothing saved)
		var count int
		_ = st.RawDB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
		if count != 0 {
			t.Errorf("expected 0 messages stored after mixed-recipient error, got %d", count)
		}
	})
}
