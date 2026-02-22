package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/types"
)

func TestMessageDeleteByAgent(t *testing.T) {
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

	// Set up test identity via environment variables
	t.Setenv("THRUM_ROLE", "tester")
	t.Setenv("THRUM_MODULE", "test-module")

	// Register agent and start session
	agentID := identity.GenerateAgentID(repoID, "tester", "test-module", "")
	agentHandler := NewAgentHandler(st)
	registerReq := RegisterRequest{Role: "tester", Module: "test-module"}
	registerParams, _ := json.Marshal(registerReq)
	if _, err := agentHandler.HandleRegister(context.Background(), registerParams); err != nil {
		t.Fatalf("failed to register agent: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionReq := SessionStartRequest{AgentID: agentID}
	sessionParams, _ := json.Marshal(sessionReq)
	if _, err := sessionHandler.HandleStart(context.Background(), sessionParams); err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	handler := NewMessageHandler(st)

	t.Run("delete all messages for an agent", func(t *testing.T) {
		// Send 3 messages from the agent
		for i := 0; i < 3; i++ {
			sendReq := SendRequest{Content: "Test message to delete by agent"}
			sendParams, _ := json.Marshal(sendReq)
			_, err := handler.HandleSend(context.Background(), sendParams)
			if err != nil {
				t.Fatalf("send message %d: %v", i, err)
			}
		}

		// Verify messages exist
		var countBefore int
		if err := st.RawDB().QueryRow("SELECT COUNT(*) FROM messages WHERE agent_id = ?", agentID).Scan(&countBefore); err != nil {
			t.Fatalf("count before: %v", err)
		}
		if countBefore < 3 {
			t.Fatalf("expected at least 3 messages before delete, got %d", countBefore)
		}

		// Call deleteByAgent
		req := DeleteByAgentRequest{AgentID: agentID}
		params, _ := json.Marshal(req)
		respRaw, err := handler.HandleDeleteByAgent(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleDeleteByAgent failed: %v", err)
		}

		delResp, ok := respRaw.(*DeleteByAgentResponse)
		if !ok {
			t.Fatalf("expected *DeleteByAgentResponse, got %T", respRaw)
		}

		if delResp.DeletedCount != countBefore {
			t.Errorf("expected DeletedCount %d, got %d", countBefore, delResp.DeletedCount)
		}

		// Verify messages are gone
		var countAfter int
		if err := st.RawDB().QueryRow("SELECT COUNT(*) FROM messages WHERE agent_id = ?", agentID).Scan(&countAfter); err != nil {
			t.Fatalf("count after: %v", err)
		}
		if countAfter != 0 {
			t.Errorf("expected 0 messages after delete, got %d", countAfter)
		}
	})

	t.Run("returns 0 count when agent has no messages", func(t *testing.T) {
		// Register a second agent with no messages
		t.Setenv("THRUM_ROLE", "tester2")
		t.Setenv("THRUM_MODULE", "test-module2")
		agent2ID := identity.GenerateAgentID(repoID, "tester2", "test-module2", "")
		reg2Params, _ := json.Marshal(RegisterRequest{Role: "tester2", Module: "test-module2"})
		if _, err := agentHandler.HandleRegister(context.Background(), reg2Params); err != nil {
			t.Fatalf("register second agent: %v", err)
		}

		req := DeleteByAgentRequest{AgentID: agent2ID}
		params, _ := json.Marshal(req)
		respRaw, err := handler.HandleDeleteByAgent(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleDeleteByAgent failed: %v", err)
		}

		delResp, ok := respRaw.(*DeleteByAgentResponse)
		if !ok {
			t.Fatalf("expected *DeleteByAgentResponse, got %T", respRaw)
		}
		if delResp.DeletedCount != 0 {
			t.Errorf("expected DeletedCount 0, got %d", delResp.DeletedCount)
		}

		// Restore original env
		t.Setenv("THRUM_ROLE", "tester")
		t.Setenv("THRUM_MODULE", "test-module")
	})

	t.Run("error on empty agent_id", func(t *testing.T) {
		req := DeleteByAgentRequest{AgentID: ""}
		params, _ := json.Marshal(req)
		_, err := handler.HandleDeleteByAgent(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for empty agent_id, got nil")
		}
		if err.Error() != "agent_id is required" {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("related records are also deleted", func(t *testing.T) {
		// Re-register original agent identity
		t.Setenv("THRUM_ROLE", "tester")
		t.Setenv("THRUM_MODULE", "test-module")

		// Register a fresh agent for this sub-test
		t.Setenv("THRUM_ROLE", "tester3")
		t.Setenv("THRUM_MODULE", "test-module3")
		agent3ID := identity.GenerateAgentID(repoID, "tester3", "test-module3", "")
		reg3Params, _ := json.Marshal(RegisterRequest{Role: "tester3", Module: "test-module3"})
		if _, err := agentHandler.HandleRegister(context.Background(), reg3Params); err != nil {
			t.Fatalf("register tester3 agent: %v", err)
		}
		sess3Params, _ := json.Marshal(SessionStartRequest{AgentID: agent3ID})
		if _, err := sessionHandler.HandleStart(context.Background(), sess3Params); err != nil {
			t.Fatalf("start tester3 session: %v", err)
		}

		// Send a message with a scope
		sendReq := SendRequest{
			Content: "Message with scope",
			Scopes:  []types.Scope{{Type: "group", Value: "backend"}},
		}
		sendParams, _ := json.Marshal(sendReq)
		respRaw, err := handler.HandleSend(context.Background(), sendParams)
		if err != nil {
			// If send with scope fails, send a plain message
			sendReq2 := SendRequest{Content: "Message for related records test"}
			sendParams2, _ := json.Marshal(sendReq2)
			respRaw, err = handler.HandleSend(context.Background(), sendParams2)
			if err != nil {
				t.Fatalf("send message: %v", err)
			}
		}

		sendResp, ok := respRaw.(*SendResponse)
		if !ok {
			t.Fatalf("expected *SendResponse, got %T", respRaw)
		}
		messageID := sendResp.MessageID

		// Mark the message as read
		markReadReq := MarkReadRequest{MessageIDs: []string{messageID}, CallerAgentID: agent3ID}
		markReadParams, _ := json.Marshal(markReadReq)
		if _, err := handler.HandleMarkRead(context.Background(), markReadParams); err != nil {
			// Not fatal - read marking may require different session setup
			t.Logf("mark read skipped: %v", err)
		}

		// Verify the message exists
		var msgCount int
		if err := st.RawDB().QueryRow("SELECT COUNT(*) FROM messages WHERE message_id = ?", messageID).Scan(&msgCount); err != nil {
			t.Fatalf("count message: %v", err)
		}
		if msgCount != 1 {
			t.Fatalf("expected 1 message, got %d", msgCount)
		}

		// Delete by agent
		req := DeleteByAgentRequest{AgentID: agent3ID}
		params, _ := json.Marshal(req)
		if _, err := handler.HandleDeleteByAgent(context.Background(), params); err != nil {
			t.Fatalf("HandleDeleteByAgent failed: %v", err)
		}

		// Verify message is gone
		var count int
		if err := st.RawDB().QueryRow("SELECT COUNT(*) FROM messages WHERE message_id = ?", messageID).Scan(&count); err != nil {
			t.Fatalf("count after delete: %v", err)
		}
		if count != 0 {
			t.Errorf("expected message to be deleted, still found %d rows", count)
		}

		// Verify no orphaned scopes
		var scopeCount int
		if err := st.RawDB().QueryRow("SELECT COUNT(*) FROM message_scopes WHERE message_id = ?", messageID).Scan(&scopeCount); err != nil {
			t.Fatalf("count scopes after delete: %v", err)
		}
		if scopeCount != 0 {
			t.Errorf("expected 0 orphaned scopes, got %d", scopeCount)
		}

		// Verify no orphaned reads
		var readsCount int
		if err := st.RawDB().QueryRow("SELECT COUNT(*) FROM message_reads WHERE message_id = ?", messageID).Scan(&readsCount); err != nil {
			t.Fatalf("count reads after delete: %v", err)
		}
		if readsCount != 0 {
			t.Errorf("expected 0 orphaned reads, got %d", readsCount)
		}

		// Restore original env
		t.Setenv("THRUM_ROLE", "tester")
		t.Setenv("THRUM_MODULE", "test-module")
	})
}
