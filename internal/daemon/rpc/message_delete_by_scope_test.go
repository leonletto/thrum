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

func TestMessageDeleteByScope(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum directory: %v", err)
	}

	// Create state
	repoID := "r_TEST12345678"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Set up test identity via environment variables
	t.Setenv("THRUM_ROLE", "tester")
	t.Setenv("THRUM_MODULE", "test-module")
	t.Setenv("THRUM_DISPLAY", "Test Agent")

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

	// Start session
	sessionHandler := NewSessionHandler(st)
	sessionReq := SessionStartRequest{
		AgentID: agentID,
	}
	sessionParams, _ := json.Marshal(sessionReq)
	sessionResp, err := sessionHandler.HandleStart(context.Background(), sessionParams)
	if err != nil {
		t.Fatalf("failed to start session: %v", err)
	}
	_, ok := sessionResp.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", sessionResp)
	}

	// Create message handler
	handler := NewMessageHandler(st)

	t.Run("delete messages with matching scope", func(t *testing.T) {
		// Send two messages with group scope "backend"
		for i := 0; i < 2; i++ {
			req := SendRequest{
				Content: "Backend message",
				Scopes: []types.Scope{
					{Type: "group", Value: "backend"},
				},
			}
			params, _ := json.Marshal(req)
			_, err := handler.HandleSend(context.Background(), params)
			if err != nil {
				t.Fatalf("HandleSend failed: %v", err)
			}
		}

		// Delete by scope
		delReq := DeleteByScopeRequest{
			ScopeType:  "group",
			ScopeValue: "backend",
		}
		delParams, _ := json.Marshal(delReq)
		resp, err := handler.HandleDeleteByScope(context.Background(), delParams)
		if err != nil {
			t.Fatalf("HandleDeleteByScope failed: %v", err)
		}

		delResp, ok := resp.(*DeleteByScopeResponse)
		if !ok {
			t.Fatalf("expected *DeleteByScopeResponse, got %T", resp)
		}
		if delResp.DeletedCount != 2 {
			t.Errorf("expected DeletedCount=2, got %d", delResp.DeletedCount)
		}

		// Verify the messages are gone from the database
		var count int
		err = st.RawDB().QueryRow(
			"SELECT COUNT(*) FROM message_scopes WHERE scope_type = ? AND scope_value = ?",
			"group", "backend",
		).Scan(&count)
		if err != nil {
			t.Fatalf("failed to query message_scopes: %v", err)
		}
		if count != 0 {
			t.Errorf("expected 0 remaining scopes, got %d", count)
		}
	})

	t.Run("returns 0 count when no messages match", func(t *testing.T) {
		delReq := DeleteByScopeRequest{
			ScopeType:  "group",
			ScopeValue: "nonexistent-group",
		}
		delParams, _ := json.Marshal(delReq)
		resp, err := handler.HandleDeleteByScope(context.Background(), delParams)
		if err != nil {
			t.Fatalf("HandleDeleteByScope failed: %v", err)
		}

		delResp, ok := resp.(*DeleteByScopeResponse)
		if !ok {
			t.Fatalf("expected *DeleteByScopeResponse, got %T", resp)
		}
		if delResp.DeletedCount != 0 {
			t.Errorf("expected DeletedCount=0, got %d", delResp.DeletedCount)
		}
	})

	t.Run("error on empty scope_type", func(t *testing.T) {
		delReq := DeleteByScopeRequest{
			ScopeType:  "",
			ScopeValue: "backend",
		}
		delParams, _ := json.Marshal(delReq)
		_, err := handler.HandleDeleteByScope(context.Background(), delParams)
		if err == nil {
			t.Fatal("expected error for empty scope_type, got nil")
		}
	})

	t.Run("error on empty scope_value", func(t *testing.T) {
		delReq := DeleteByScopeRequest{
			ScopeType:  "group",
			ScopeValue: "",
		}
		delParams, _ := json.Marshal(delReq)
		_, err := handler.HandleDeleteByScope(context.Background(), delParams)
		if err == nil {
			t.Fatal("expected error for empty scope_value, got nil")
		}
	})

	t.Run("cleans up related records for deleted messages", func(t *testing.T) {
		// Send a message with scope, refs, and then mark it read
		sendReq := SendRequest{
			Content: "Message with refs and scope",
			Scopes: []types.Scope{
				{Type: "group", Value: "cleanup-test"},
			},
			Refs: []types.Ref{
				{Type: "issue", Value: "beads-999"},
			},
		}
		sendParams, _ := json.Marshal(sendReq)
		sendResp, err := handler.HandleSend(context.Background(), sendParams)
		if err != nil {
			t.Fatalf("HandleSend failed: %v", err)
		}
		sendResponse, ok := sendResp.(*SendResponse)
		if !ok {
			t.Fatalf("expected *SendResponse, got %T", sendResp)
		}
		msgID := sendResponse.MessageID

		// Mark it as read to populate message_reads
		markReadReq := MarkReadRequest{
			MessageIDs: []string{msgID},
		}
		markReadParams, _ := json.Marshal(markReadReq)
		_, err = handler.HandleMarkRead(context.Background(), markReadParams)
		if err != nil {
			t.Fatalf("HandleMarkRead failed: %v", err)
		}

		// Verify records exist before deletion
		var refCount int
		if err := st.RawDB().QueryRow(
			"SELECT COUNT(*) FROM message_refs WHERE message_id = ?", msgID,
		).Scan(&refCount); err != nil {
			t.Fatalf("query refs: %v", err)
		}
		if refCount == 0 {
			t.Error("expected refs to exist before deletion")
		}

		var readCount int
		if err := st.RawDB().QueryRow(
			"SELECT COUNT(*) FROM message_reads WHERE message_id = ?", msgID,
		).Scan(&readCount); err != nil {
			t.Fatalf("query reads: %v", err)
		}
		if readCount == 0 {
			t.Error("expected reads to exist before deletion")
		}

		// Delete by scope
		delReq := DeleteByScopeRequest{
			ScopeType:  "group",
			ScopeValue: "cleanup-test",
		}
		delParams, _ := json.Marshal(delReq)
		resp, err := handler.HandleDeleteByScope(context.Background(), delParams)
		if err != nil {
			t.Fatalf("HandleDeleteByScope failed: %v", err)
		}
		delResp, ok := resp.(*DeleteByScopeResponse)
		if !ok {
			t.Fatalf("expected *DeleteByScopeResponse, got %T", resp)
		}
		if delResp.DeletedCount != 1 {
			t.Errorf("expected DeletedCount=1, got %d", delResp.DeletedCount)
		}

		// Verify all related records are gone
		for _, table := range []string{"message_refs", "message_reads", "message_scopes", "messages"} {
			var cnt int
			if err := st.RawDB().QueryRow(
				"SELECT COUNT(*) FROM "+table+" WHERE message_id = ?", msgID,
			).Scan(&cnt); err != nil {
				t.Fatalf("query %s after delete: %v", table, err)
			}
			if cnt != 0 {
				t.Errorf("expected 0 rows in %s for deleted message, got %d", table, cnt)
			}
		}
	})

	t.Run("messages without matching scope are NOT deleted", func(t *testing.T) {
		// Send one message with the target scope
		targetReq := SendRequest{
			Content: "Target message",
			Scopes: []types.Scope{
				{Type: "group", Value: "to-delete"},
			},
		}
		targetParams, _ := json.Marshal(targetReq)
		_, err := handler.HandleSend(context.Background(), targetParams)
		if err != nil {
			t.Fatalf("HandleSend for target failed: %v", err)
		}

		// Send another message with a different scope
		otherReq := SendRequest{
			Content: "Other message",
			Scopes: []types.Scope{
				{Type: "group", Value: "keep-me"},
			},
		}
		otherParams, _ := json.Marshal(otherReq)
		otherSendResp, err := handler.HandleSend(context.Background(), otherParams)
		if err != nil {
			t.Fatalf("HandleSend for other failed: %v", err)
		}
		otherSendResponse, ok := otherSendResp.(*SendResponse)
		if !ok {
			t.Fatalf("expected *SendResponse, got %T", otherSendResp)
		}
		otherMsgID := otherSendResponse.MessageID

		// Delete only the target scope
		delReq := DeleteByScopeRequest{
			ScopeType:  "group",
			ScopeValue: "to-delete",
		}
		delParams, _ := json.Marshal(delReq)
		resp, err := handler.HandleDeleteByScope(context.Background(), delParams)
		if err != nil {
			t.Fatalf("HandleDeleteByScope failed: %v", err)
		}
		delResp, ok := resp.(*DeleteByScopeResponse)
		if !ok {
			t.Fatalf("expected *DeleteByScopeResponse, got %T", resp)
		}
		if delResp.DeletedCount != 1 {
			t.Errorf("expected DeletedCount=1, got %d", delResp.DeletedCount)
		}

		// Verify the other message still exists
		var cnt int
		if err := st.RawDB().QueryRow(
			"SELECT COUNT(*) FROM messages WHERE message_id = ?", otherMsgID,
		).Scan(&cnt); err != nil {
			t.Fatalf("query other message: %v", err)
		}
		if cnt != 1 {
			t.Errorf("expected other message to still exist (count=1), got %d", cnt)
		}

		// Verify the other message's scope still exists
		var scopeCnt int
		if err := st.RawDB().QueryRow(
			"SELECT COUNT(*) FROM message_scopes WHERE scope_type = ? AND scope_value = ?",
			"group", "keep-me",
		).Scan(&scopeCnt); err != nil {
			t.Fatalf("query other scope: %v", err)
		}
		if scopeCnt != 1 {
			t.Errorf("expected other scope to still exist (count=1), got %d", scopeCnt)
		}
	})
}
