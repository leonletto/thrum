package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/types"
)

func TestMessageSend(t *testing.T) {
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
	sessionStartResp, ok := sessionResp.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", sessionResp)
	}
	sessionID := sessionStartResp.SessionID

	// Create message handler
	handler := NewMessageHandler(st)

	t.Run("send basic message", func(t *testing.T) {
		req := SendRequest{
			Content: "Hello, world!",
			Format:  "markdown",
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

		if sendResp.MessageID == "" {
			t.Error("expected non-empty message_id")
		}
		if sendResp.CreatedAt == "" {
			t.Error("expected non-empty created_at")
		}

		// Verify message was written to database
		var content string
		query := `SELECT body_content FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, sendResp.MessageID).Scan(&content)
		if err != nil {
			t.Fatalf("failed to query message: %v", err)
		}
		if content != "Hello, world!" {
			t.Errorf("expected content 'Hello, world!', got '%s'", content)
		}

		// Verify agent and session
		var msgAgentID, msgSessionID string
		query = `SELECT agent_id, session_id FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, sendResp.MessageID).Scan(&msgAgentID, &msgSessionID)
		if err != nil {
			t.Fatalf("failed to query message: %v", err)
		}
		if msgAgentID != agentID {
			t.Errorf("expected agent_id '%s', got '%s'", agentID, msgAgentID)
		}
		if msgSessionID != sessionID {
			t.Errorf("expected session_id '%s', got '%s'", sessionID, msgSessionID)
		}
	})

	t.Run("send message with thread", func(t *testing.T) {
		// Create thread first
		threadID := identity.GenerateThreadID()
		threadEvent := types.ThreadCreateEvent{
			Type:      "thread.create",
			Timestamp: "2025-01-01T00:00:00Z",
			ThreadID:  threadID,
			Title:     "Test Thread",
			CreatedBy: agentID,
		}
		if err := st.WriteEvent(context.Background(), threadEvent); err != nil {
			t.Fatalf("failed to create thread: %v", err)
		}

		req := SendRequest{
			Content:  "Message in thread",
			ThreadID: threadID,
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
		if sendResp.ThreadID != threadID {
			t.Errorf("expected thread_id '%s', got '%s'", threadID, sendResp.ThreadID)
		}

		// Verify thread_id in database
		var msgThreadID string
		query := `SELECT thread_id FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, sendResp.MessageID).Scan(&msgThreadID)
		if err != nil {
			t.Fatalf("failed to query message: %v", err)
		}
		if msgThreadID != threadID {
			t.Errorf("expected thread_id '%s', got '%s'", threadID, msgThreadID)
		}
	})

	t.Run("send message with scopes", func(t *testing.T) {
		req := SendRequest{
			Content: "Scoped message",
			Scopes: []types.Scope{
				{Type: "repo", Value: "github.com/user/repo"},
				{Type: "file", Value: "src/main.go"},
			},
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
		messageID := sendResp.MessageID

		// Verify scopes in database
		query := `SELECT scope_type, scope_value FROM message_scopes WHERE message_id = ? ORDER BY scope_type`
		rows, err := st.DB().Query(query, messageID)
		if err != nil {
			t.Fatalf("failed to query scopes: %v", err)
		}
		defer func() { _ = rows.Close() }()

		scopes := []types.Scope{}
		for rows.Next() {
			var scope types.Scope
			if err := rows.Scan(&scope.Type, &scope.Value); err != nil {
				t.Fatalf("failed to scan scope: %v", err)
			}
			scopes = append(scopes, scope)
		}

		if len(scopes) != 2 {
			t.Fatalf("expected 2 scopes, got %d", len(scopes))
		}
		if scopes[0].Type != "file" || scopes[0].Value != "src/main.go" {
			t.Errorf("unexpected scope: %+v", scopes[0])
		}
		if scopes[1].Type != "repo" || scopes[1].Value != "github.com/user/repo" {
			t.Errorf("unexpected scope: %+v", scopes[1])
		}
	})

	t.Run("send message with refs", func(t *testing.T) {
		req := SendRequest{
			Content: "Message with refs",
			Refs: []types.Ref{
				{Type: "issue", Value: "beads-123"},
				{Type: "commit", Value: "abc123def"},
			},
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
		messageID := sendResp.MessageID

		// Verify refs in database
		query := `SELECT ref_type, ref_value FROM message_refs WHERE message_id = ? ORDER BY ref_type`
		rows, err := st.DB().Query(query, messageID)
		if err != nil {
			t.Fatalf("failed to query refs: %v", err)
		}
		defer func() { _ = rows.Close() }()

		refs := []types.Ref{}
		for rows.Next() {
			var ref types.Ref
			if err := rows.Scan(&ref.Type, &ref.Value); err != nil {
				t.Fatalf("failed to scan ref: %v", err)
			}
			refs = append(refs, ref)
		}

		if len(refs) != 2 {
			t.Fatalf("expected 2 refs, got %d", len(refs))
		}
		if refs[0].Type != "commit" || refs[0].Value != "abc123def" {
			t.Errorf("unexpected ref: %+v", refs[0])
		}
		if refs[1].Type != "issue" || refs[1].Value != "beads-123" {
			t.Errorf("unexpected ref: %+v", refs[1])
		}
	})

	t.Run("send message with structured data", func(t *testing.T) {
		structuredData := map[string]any{
			"type":   "test",
			"value":  42,
			"nested": map[string]any{"key": "value"},
		}

		req := SendRequest{
			Content:    "Message with structured data",
			Structured: structuredData,
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
		messageID := sendResp.MessageID

		// Verify structured data in database
		var structuredJSON string
		query := `SELECT body_structured FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, messageID).Scan(&structuredJSON)
		if err != nil {
			t.Fatalf("failed to query message: %v", err)
		}

		var retrieved map[string]any
		if err := json.Unmarshal([]byte(structuredJSON), &retrieved); err != nil {
			t.Fatalf("failed to unmarshal structured data: %v", err)
		}

		if retrieved["type"] != "test" {
			t.Errorf("expected type 'test', got '%v'", retrieved["type"])
		}
		val, ok := retrieved["value"].(float64)
		if !ok {
			t.Fatalf("expected value to be float64, got %T", retrieved["value"])
		}
		if val != 42 {
			t.Errorf("expected value 42, got %v", val)
		}
	})

	t.Run("send message with default format", func(t *testing.T) {
		req := SendRequest{
			Content: "Message without format",
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
		messageID := sendResp.MessageID

		// Verify format defaults to markdown
		var format string
		query := `SELECT body_format FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, messageID).Scan(&format)
		if err != nil {
			t.Fatalf("failed to query message: %v", err)
		}
		if format != "markdown" {
			t.Errorf("expected format 'markdown', got '%s'", format)
		}
	})

	t.Run("validation - empty content", func(t *testing.T) {
		req := SendRequest{
			Content: "",
		}
		params, _ := json.Marshal(req)

		_, err := handler.HandleSend(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for empty content")
		}
		if err.Error() != "content is required" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("validation - invalid format", func(t *testing.T) {
		req := SendRequest{
			Content: "Test",
			Format:  "invalid",
		}
		params, _ := json.Marshal(req)

		_, err := handler.HandleSend(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for invalid format")
		}
	})

	t.Run("no active session", func(t *testing.T) {
		// End the session
		endReq := SessionEndRequest{
			SessionID: sessionID,
			Reason:    "test complete",
		}
		endParams, _ := json.Marshal(endReq)
		_, err := sessionHandler.HandleEnd(context.Background(), endParams)
		if err != nil {
			t.Fatalf("failed to end session: %v", err)
		}

		// Try to send message without active session
		req := SendRequest{
			Content: "Message without session",
		}
		params, _ := json.Marshal(req)

		_, err = handler.HandleSend(context.Background(), params)
		if err == nil {
			t.Fatal("expected error when no active session")
		}
	})
}

func TestMessageGet(t *testing.T) {
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

	// Set up test identity
	t.Setenv("THRUM_ROLE", "tester")
	t.Setenv("THRUM_MODULE", "test-module")

	// Register agent and start session
	agentID := identity.GenerateAgentID(repoID, "tester", "test-module", "")
	agentHandler := NewAgentHandler(st)
	registerReq := RegisterRequest{Role: "tester", Module: "test-module"}
	registerParams, _ := json.Marshal(registerReq)
	_, err = agentHandler.HandleRegister(context.Background(), registerParams)
	if err != nil {
		t.Fatalf("failed to register agent: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionReq := SessionStartRequest{AgentID: agentID}
	sessionParams, _ := json.Marshal(sessionReq)
	sessionResp, err := sessionHandler.HandleStart(context.Background(), sessionParams)
	if err != nil {
		t.Fatalf("failed to start session: %v", err)
	}
	sessionStartResp, ok := sessionResp.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", sessionResp)
	}
	sessionID := sessionStartResp.SessionID

	// Create message handler
	handler := NewMessageHandler(st)

	// Create a test message
	sendReq := SendRequest{
		Content: "Test message for get",
		Format:  "markdown",
		Scopes: []types.Scope{
			{Type: "repo", Value: "github.com/test/repo"},
		},
		Refs: []types.Ref{
			{Type: "issue", Value: "beads-456"},
		},
	}
	sendParams, _ := json.Marshal(sendReq)
	sendResp, err := handler.HandleSend(context.Background(), sendParams)
	if err != nil {
		t.Fatalf("failed to send message: %v", err)
	}
	sendResponse, ok := sendResp.(*SendResponse)
	if !ok {
		t.Fatalf("expected *SendResponse, got %T", sendResp)
	}
	messageID := sendResponse.MessageID

	t.Run("get existing message", func(t *testing.T) {
		req := GetMessageRequest{MessageID: messageID}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleGet(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleGet failed: %v", err)
		}

		getResp, ok := resp.(*GetMessageResponse)
		if !ok {
			t.Fatalf("expected *GetMessageResponse, got %T", resp)
		}

		msg := getResp.Message
		if msg.MessageID != messageID {
			t.Errorf("expected message_id '%s', got '%s'", messageID, msg.MessageID)
		}
		if msg.Body.Content != "Test message for get" {
			t.Errorf("expected content 'Test message for get', got '%s'", msg.Body.Content)
		}
		if msg.Body.Format != "markdown" {
			t.Errorf("expected format 'markdown', got '%s'", msg.Body.Format)
		}
		if msg.Author.AgentID != agentID {
			t.Errorf("expected agent_id '%s', got '%s'", agentID, msg.Author.AgentID)
		}
		if msg.Author.SessionID != sessionID {
			t.Errorf("expected session_id '%s', got '%s'", sessionID, msg.Author.SessionID)
		}
		if msg.Deleted {
			t.Error("expected deleted to be false")
		}
		if msg.CreatedAt == "" {
			t.Error("expected non-empty created_at")
		}

		// Verify scopes
		if len(msg.Scopes) != 1 {
			t.Fatalf("expected 1 scope, got %d", len(msg.Scopes))
		}
		if msg.Scopes[0].Type != "repo" || msg.Scopes[0].Value != "github.com/test/repo" {
			t.Errorf("unexpected scope: %+v", msg.Scopes[0])
		}

		// Verify refs
		if len(msg.Refs) != 1 {
			t.Fatalf("expected 1 ref, got %d", len(msg.Refs))
		}
		if msg.Refs[0].Type != "issue" || msg.Refs[0].Value != "beads-456" {
			t.Errorf("unexpected ref: %+v", msg.Refs[0])
		}
	})

	t.Run("get message with thread", func(t *testing.T) {
		// Create thread and message
		threadID := identity.GenerateThreadID()
		threadEvent := types.ThreadCreateEvent{
			Type:      "thread.create",
			Timestamp: "2025-01-01T00:00:00Z",
			ThreadID:  threadID,
			Title:     "Test Thread",
			CreatedBy: agentID,
		}
		if err := st.WriteEvent(context.Background(), threadEvent); err != nil {
			t.Fatalf("failed to create thread: %v", err)
		}

		sendReq := SendRequest{
			Content:  "Message in thread",
			ThreadID: threadID,
		}
		sendParams, _ := json.Marshal(sendReq)
		sendResp, err := handler.HandleSend(context.Background(), sendParams)
		if err != nil {
			t.Fatalf("failed to send message: %v", err)
		}
		sendResponse, ok := sendResp.(*SendResponse)
		if !ok {
			t.Fatalf("expected *SendResponse, got %T", sendResp)
		}
		msgID := sendResponse.MessageID

		// Get message
		req := GetMessageRequest{MessageID: msgID}
		params, _ := json.Marshal(req)
		resp, err := handler.HandleGet(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleGet failed: %v", err)
		}

		getResp, ok := resp.(*GetMessageResponse)
		if !ok {
			t.Fatalf("expected *GetMessageResponse, got %T", resp)
		}
		msg := getResp.Message
		if msg.ThreadID != threadID {
			t.Errorf("expected thread_id '%s', got '%s'", threadID, msg.ThreadID)
		}
	})

	t.Run("get deleted message", func(t *testing.T) {
		// Create and delete a message
		sendReq := SendRequest{Content: "Message to delete"}
		sendParams, _ := json.Marshal(sendReq)
		sendResp, err := handler.HandleSend(context.Background(), sendParams)
		if err != nil {
			t.Fatalf("failed to send message: %v", err)
		}
		sendResponse, ok := sendResp.(*SendResponse)
		if !ok {
			t.Fatalf("expected *SendResponse, got %T", sendResp)
		}
		msgID := sendResponse.MessageID

		// Delete the message
		deleteEvent := types.MessageDeleteEvent{
			Type:      "message.delete",
			Timestamp: "2025-01-01T12:00:00Z",
			MessageID: msgID,
			Reason:    "test delete",
		}
		if err := st.WriteEvent(context.Background(), deleteEvent); err != nil {
			t.Fatalf("failed to delete message: %v", err)
		}

		// Get the deleted message
		req := GetMessageRequest{MessageID: msgID}
		params, _ := json.Marshal(req)
		resp, err := handler.HandleGet(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleGet failed: %v", err)
		}

		getResp, ok := resp.(*GetMessageResponse)
		if !ok {
			t.Fatalf("expected *GetMessageResponse, got %T", resp)
		}
		msg := getResp.Message
		if !msg.Deleted {
			t.Error("expected deleted to be true")
		}
		if msg.Metadata.DeletedAt == "" {
			t.Error("expected non-empty deleted_at")
		}
		if msg.Metadata.DeleteReason != "test delete" {
			t.Errorf("expected delete_reason 'test delete', got '%s'", msg.Metadata.DeleteReason)
		}
	})

	t.Run("get non-existent message", func(t *testing.T) {
		req := GetMessageRequest{MessageID: "msg_NONEXISTENT"}
		params, _ := json.Marshal(req)

		_, err := handler.HandleGet(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for non-existent message")
		}
		if err.Error() != "message not found: msg_NONEXISTENT" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("validation - empty message_id", func(t *testing.T) {
		req := GetMessageRequest{MessageID: ""}
		params, _ := json.Marshal(req)

		_, err := handler.HandleGet(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for empty message_id")
		}
		if err.Error() != "message_id is required" {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestMessageList(t *testing.T) {
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

	// Set up test identity
	t.Setenv("THRUM_ROLE", "tester")
	t.Setenv("THRUM_MODULE", "test-module")

	// Register agent and start session
	agentID := identity.GenerateAgentID(repoID, "tester", "test-module", "")
	agentHandler := NewAgentHandler(st)
	registerReq := RegisterRequest{Role: "tester", Module: "test-module"}
	registerParams, _ := json.Marshal(registerReq)
	_, err = agentHandler.HandleRegister(context.Background(), registerParams)
	if err != nil {
		t.Fatalf("failed to register agent: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionReq := SessionStartRequest{AgentID: agentID}
	sessionParams, _ := json.Marshal(sessionReq)
	_, err = sessionHandler.HandleStart(context.Background(), sessionParams)
	if err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	// Create message handler
	handler := NewMessageHandler(st)

	// Create test thread
	threadID := identity.GenerateThreadID()
	threadEvent := types.ThreadCreateEvent{
		Type:      "thread.create",
		Timestamp: "2025-01-01T00:00:00Z",
		ThreadID:  threadID,
		Title:     "Test Thread",
		CreatedBy: agentID,
	}
	if err := st.WriteEvent(context.Background(), threadEvent); err != nil {
		t.Fatalf("failed to create thread: %v", err)
	}

	// Create multiple test messages
	messages := []struct {
		content  string
		threadID string
		scopes   []types.Scope
		refs     []types.Ref
	}{
		{
			content:  "Message 1",
			threadID: threadID,
			scopes:   []types.Scope{{Type: "repo", Value: "github.com/test/repo"}},
			refs:     []types.Ref{{Type: "issue", Value: "beads-123"}},
		},
		{
			content:  "Message 2",
			threadID: "",
			scopes:   []types.Scope{{Type: "file", Value: "src/main.go"}},
			refs:     []types.Ref{},
		},
		{
			content:  "Message 3",
			threadID: threadID,
			scopes:   []types.Scope{{Type: "repo", Value: "github.com/test/repo"}},
			refs:     []types.Ref{{Type: "commit", Value: "abc123"}},
		},
	}

	for _, msg := range messages {
		req := SendRequest{
			Content:  msg.content,
			ThreadID: msg.threadID,
			Scopes:   msg.scopes,
			Refs:     msg.refs,
		}
		params, _ := json.Marshal(req)
		_, err := handler.HandleSend(context.Background(), params)
		if err != nil {
			t.Fatalf("failed to send message: %v", err)
		}
	}

	t.Run("list all messages", func(t *testing.T) {
		req := ListMessagesRequest{}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 3 {
			t.Errorf("expected total 3, got %d", listResp.Total)
		}
		if len(listResp.Messages) != 3 {
			t.Errorf("expected 3 messages, got %d", len(listResp.Messages))
		}
		if listResp.Page != 1 {
			t.Errorf("expected page 1, got %d", listResp.Page)
		}
		if listResp.PageSize != 10 {
			t.Errorf("expected page_size 10, got %d", listResp.PageSize)
		}
		if listResp.TotalPages != 1 {
			t.Errorf("expected total_pages 1, got %d", listResp.TotalPages)
		}

		// Messages should be sorted by created_at desc (newest first)
		if listResp.Messages[0].Body.Content != "Message 3" {
			t.Errorf("expected first message 'Message 3', got '%s'", listResp.Messages[0].Body.Content)
		}
	})

	t.Run("filter by thread_id", func(t *testing.T) {
		req := ListMessagesRequest{ThreadID: threadID}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 2 {
			t.Errorf("expected total 2, got %d", listResp.Total)
		}
		for _, msg := range listResp.Messages {
			if msg.ThreadID != threadID {
				t.Errorf("expected thread_id '%s', got '%s'", threadID, msg.ThreadID)
			}
		}
	})

	t.Run("filter by author_id", func(t *testing.T) {
		req := ListMessagesRequest{AuthorID: agentID}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 3 {
			t.Errorf("expected total 3, got %d", listResp.Total)
		}
	})

	t.Run("filter by scope", func(t *testing.T) {
		req := ListMessagesRequest{
			Scope: &types.Scope{Type: "repo", Value: "github.com/test/repo"},
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 2 {
			t.Errorf("expected total 2, got %d", listResp.Total)
		}
	})

	t.Run("filter by ref", func(t *testing.T) {
		req := ListMessagesRequest{
			Ref: &types.Ref{Type: "issue", Value: "beads-123"},
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 1 {
			t.Errorf("expected total 1, got %d", listResp.Total)
		}
		if listResp.Messages[0].Body.Content != "Message 1" {
			t.Errorf("expected 'Message 1', got '%s'", listResp.Messages[0].Body.Content)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		// Page 1 with page_size 2
		req := ListMessagesRequest{
			PageSize: 2,
			Page:     1,
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 3 {
			t.Errorf("expected total 3, got %d", listResp.Total)
		}
		if len(listResp.Messages) != 2 {
			t.Errorf("expected 2 messages on page 1, got %d", len(listResp.Messages))
		}
		if listResp.TotalPages != 2 {
			t.Errorf("expected total_pages 2, got %d", listResp.TotalPages)
		}

		// Page 2
		req.Page = 2
		params, _ = json.Marshal(req)
		resp, err = handler.HandleList(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok = resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if len(listResp.Messages) != 1 {
			t.Errorf("expected 1 message on page 2, got %d", len(listResp.Messages))
		}
	})

	t.Run("sort ascending", func(t *testing.T) {
		req := ListMessagesRequest{
			SortBy:    "created_at",
			SortOrder: "asc",
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		// Oldest first
		if listResp.Messages[0].Body.Content != "Message 1" {
			t.Errorf("expected first message 'Message 1', got '%s'", listResp.Messages[0].Body.Content)
		}
		if listResp.Messages[2].Body.Content != "Message 3" {
			t.Errorf("expected last message 'Message 3', got '%s'", listResp.Messages[2].Body.Content)
		}
	})

	t.Run("validation - invalid sort_by", func(t *testing.T) {
		req := ListMessagesRequest{SortBy: "invalid"}
		params, _ := json.Marshal(req)

		_, err := handler.HandleList(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for invalid sort_by")
		}
	})

	t.Run("validation - invalid sort_order", func(t *testing.T) {
		req := ListMessagesRequest{SortOrder: "invalid"}
		params, _ := json.Marshal(req)

		_, err := handler.HandleList(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for invalid sort_order")
		}
	})

	t.Run("empty result", func(t *testing.T) {
		req := ListMessagesRequest{
			ThreadID: "thr_NONEXISTENT",
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 0 {
			t.Errorf("expected total 0, got %d", listResp.Total)
		}
		if len(listResp.Messages) != 0 {
			t.Errorf("expected 0 messages, got %d", len(listResp.Messages))
		}
	})
}

func TestMessageDelete(t *testing.T) {
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

	// Set up test identity
	t.Setenv("THRUM_ROLE", "tester")
	t.Setenv("THRUM_MODULE", "test-module")

	// Register agent and start session
	agentID := identity.GenerateAgentID(repoID, "tester", "test-module", "")
	agentHandler := NewAgentHandler(st)
	registerReq := RegisterRequest{Role: "tester", Module: "test-module"}
	registerParams, _ := json.Marshal(registerReq)
	_, err = agentHandler.HandleRegister(context.Background(), registerParams)
	if err != nil {
		t.Fatalf("failed to register agent: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionReq := SessionStartRequest{AgentID: agentID}
	sessionParams, _ := json.Marshal(sessionReq)
	_, err = sessionHandler.HandleStart(context.Background(), sessionParams)
	if err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	// Create message handler
	handler := NewMessageHandler(st)

	// Create a test message
	sendReq := SendRequest{Content: "Test message to delete"}
	sendParams, _ := json.Marshal(sendReq)
	sendResp, err := handler.HandleSend(context.Background(), sendParams)
	if err != nil {
		t.Fatalf("failed to send message: %v", err)
	}
	sendResponse, ok := sendResp.(*SendResponse)
	if !ok {
		t.Fatalf("expected *SendResponse, got %T", sendResp)
	}
	messageID := sendResponse.MessageID

	t.Run("delete existing message", func(t *testing.T) {
		req := DeleteMessageRequest{
			MessageID: messageID,
			Reason:    "test delete",
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleDelete(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleDelete failed: %v", err)
		}

		deleteResp, ok := resp.(*DeleteMessageResponse)
		if !ok {
			t.Fatalf("expected *DeleteMessageResponse, got %T", resp)
		}

		if deleteResp.MessageID != messageID {
			t.Errorf("expected message_id '%s', got '%s'", messageID, deleteResp.MessageID)
		}
		if deleteResp.DeletedAt == "" {
			t.Error("expected non-empty deleted_at")
		}

		// Verify message is marked as deleted in database
		var deleted int
		var deletedAt, deleteReason sql.NullString
		query := `SELECT deleted, deleted_at, delete_reason FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, messageID).Scan(&deleted, &deletedAt, &deleteReason)
		if err != nil {
			t.Fatalf("failed to query message: %v", err)
		}

		if deleted != 1 {
			t.Error("expected deleted to be 1")
		}
		if !deletedAt.Valid || deletedAt.String == "" {
			t.Error("expected non-empty deleted_at")
		}
		if !deleteReason.Valid || deleteReason.String != "test delete" {
			t.Errorf("expected delete_reason 'test delete', got '%s'", deleteReason.String)
		}
	})

	t.Run("delete already deleted message", func(t *testing.T) {
		req := DeleteMessageRequest{MessageID: messageID}
		params, _ := json.Marshal(req)

		_, err := handler.HandleDelete(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for already deleted message")
		}
		if err.Error() != fmt.Sprintf("message already deleted: %s", messageID) {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("delete non-existent message", func(t *testing.T) {
		req := DeleteMessageRequest{MessageID: "msg_NONEXISTENT"}
		params, _ := json.Marshal(req)

		_, err := handler.HandleDelete(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for non-existent message")
		}
		if err.Error() != "message not found: msg_NONEXISTENT" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("delete without reason", func(t *testing.T) {
		// Create another message
		sendReq := SendRequest{Content: "Another message"}
		sendParams, _ := json.Marshal(sendReq)
		sendResp, err := handler.HandleSend(context.Background(), sendParams)
		if err != nil {
			t.Fatalf("failed to send message: %v", err)
		}
		sendResponse, ok := sendResp.(*SendResponse)
		if !ok {
			t.Fatalf("expected *SendResponse, got %T", sendResp)
		}
		msgID := sendResponse.MessageID

		req := DeleteMessageRequest{MessageID: msgID}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleDelete(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleDelete failed: %v", err)
		}

		deleteResp, ok := resp.(*DeleteMessageResponse)
		if !ok {
			t.Fatalf("expected *DeleteMessageResponse, got %T", resp)
		}
		if deleteResp.MessageID != msgID {
			t.Errorf("expected message_id '%s', got '%s'", msgID, deleteResp.MessageID)
		}

		// Verify delete_reason is NULL in database
		var deleteReason sql.NullString
		query := `SELECT delete_reason FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, msgID).Scan(&deleteReason)
		if err != nil {
			t.Fatalf("failed to query message: %v", err)
		}
		if deleteReason.Valid && deleteReason.String != "" {
			t.Errorf("expected empty delete_reason, got '%s'", deleteReason.String)
		}
	})

	t.Run("validation - empty message_id", func(t *testing.T) {
		req := DeleteMessageRequest{MessageID: ""}
		params, _ := json.Marshal(req)

		_, err := handler.HandleDelete(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for empty message_id")
		}
		if err.Error() != "message_id is required" {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestMessageEdit(t *testing.T) {
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

	// Set up test identity
	t.Setenv("THRUM_ROLE", "tester")
	t.Setenv("THRUM_MODULE", "test-module")

	// Register agent and start session
	agentID := identity.GenerateAgentID(repoID, "tester", "test-module", "")
	agentHandler := NewAgentHandler(st)
	registerReq := RegisterRequest{Role: "tester", Module: "test-module"}
	registerParams, _ := json.Marshal(registerReq)
	_, err = agentHandler.HandleRegister(context.Background(), registerParams)
	if err != nil {
		t.Fatalf("failed to register agent: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionReq := SessionStartRequest{AgentID: agentID}
	sessionParams, _ := json.Marshal(sessionReq)
	_, err = sessionHandler.HandleStart(context.Background(), sessionParams)
	if err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	// Create message handler
	handler := NewMessageHandler(st)

	// Create a test message
	sendReq := SendRequest{Content: "Original content"}
	sendParams, _ := json.Marshal(sendReq)
	sendResp, err := handler.HandleSend(context.Background(), sendParams)
	if err != nil {
		t.Fatalf("failed to send message: %v", err)
	}
	sendResponse, ok := sendResp.(*SendResponse)
	if !ok {
		t.Fatalf("expected *SendResponse, got %T", sendResp)
	}
	messageID := sendResponse.MessageID

	t.Run("edit content only", func(t *testing.T) {
		req := EditRequest{
			MessageID: messageID,
			Content:   "Updated content",
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleEdit(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleEdit failed: %v", err)
		}

		editResp, ok := resp.(*EditResponse)
		if !ok {
			t.Fatalf("expected *EditResponse, got %T", resp)
		}

		if editResp.MessageID != messageID {
			t.Errorf("expected message_id '%s', got '%s'", messageID, editResp.MessageID)
		}
		if editResp.UpdatedAt == "" {
			t.Error("expected non-empty updated_at")
		}
		if editResp.Version != 1 {
			t.Errorf("expected version 1, got %d", editResp.Version)
		}

		// Verify content was updated in database
		var content string
		var updatedAt sql.NullString
		query := `SELECT body_content, updated_at FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, messageID).Scan(&content, &updatedAt)
		if err != nil {
			t.Fatalf("failed to query message: %v", err)
		}

		if content != "Updated content" {
			t.Errorf("expected content 'Updated content', got '%s'", content)
		}
		if !updatedAt.Valid || updatedAt.String == "" {
			t.Error("expected non-empty updated_at")
		}
	})

	t.Run("edit structured data", func(t *testing.T) {
		structured := map[string]any{
			"type":   "task",
			"status": "completed",
		}

		req := EditRequest{
			MessageID:  messageID,
			Structured: structured,
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleEdit(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleEdit failed: %v", err)
		}

		editResp, ok := resp.(*EditResponse)
		if !ok {
			t.Fatalf("expected *EditResponse, got %T", resp)
		}
		if editResp.Version != 2 {
			t.Errorf("expected version 2 (second edit), got %d", editResp.Version)
		}

		// Verify structured data was updated
		var structuredJSON sql.NullString
		query := `SELECT body_structured FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, messageID).Scan(&structuredJSON)
		if err != nil {
			t.Fatalf("failed to query message: %v", err)
		}

		if !structuredJSON.Valid || structuredJSON.String == "" {
			t.Error("expected non-empty body_structured")
		}

		var parsedStructured map[string]any
		if err := json.Unmarshal([]byte(structuredJSON.String), &parsedStructured); err != nil {
			t.Fatalf("failed to parse structured data: %v", err)
		}

		if parsedStructured["type"] != "task" {
			t.Errorf("expected type 'task', got '%v'", parsedStructured["type"])
		}
		if parsedStructured["status"] != "completed" {
			t.Errorf("expected status 'completed', got '%v'", parsedStructured["status"])
		}
	})

	t.Run("edit content and structured together", func(t *testing.T) {
		structured := map[string]any{"priority": "high"}

		req := EditRequest{
			MessageID:  messageID,
			Content:    "New content with structured",
			Structured: structured,
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleEdit(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleEdit failed: %v", err)
		}

		editResp, ok := resp.(*EditResponse)
		if !ok {
			t.Fatalf("expected *EditResponse, got %T", resp)
		}
		if editResp.Version != 3 {
			t.Errorf("expected version 3 (third edit), got %d", editResp.Version)
		}

		// Verify both were updated
		var content string
		var structuredJSON sql.NullString
		query := `SELECT body_content, body_structured FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, messageID).Scan(&content, &structuredJSON)
		if err != nil {
			t.Fatalf("failed to query message: %v", err)
		}

		if content != "New content with structured" {
			t.Errorf("expected content 'New content with structured', got '%s'", content)
		}
		if !structuredJSON.Valid {
			t.Error("expected non-empty structured data")
		}
	})

	t.Run("cannot edit deleted message", func(t *testing.T) {
		// Create and delete a message
		sendReq := SendRequest{Content: "Message to delete"}
		sendParams, _ := json.Marshal(sendReq)
		sendResp, err := handler.HandleSend(context.Background(), sendParams)
		if err != nil {
			t.Fatalf("failed to send message: %v", err)
		}
		sendResponse, ok := sendResp.(*SendResponse)
		if !ok {
			t.Fatalf("expected *SendResponse, got %T", sendResp)
		}
		msgID := sendResponse.MessageID

		deleteReq := DeleteMessageRequest{MessageID: msgID}
		deleteParams, _ := json.Marshal(deleteReq)
		_, err = handler.HandleDelete(context.Background(), deleteParams)
		if err != nil {
			t.Fatalf("failed to delete message: %v", err)
		}

		// Try to edit deleted message
		req := EditRequest{
			MessageID: msgID,
			Content:   "Trying to edit deleted",
		}
		params, _ := json.Marshal(req)

		_, err = handler.HandleEdit(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for editing deleted message")
		}
		if !strings.Contains(err.Error(), "cannot edit deleted message") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("cannot edit message from different author", func(t *testing.T) {
		// Create a second agent
		t.Setenv("THRUM_ROLE", "other-agent")
		t.Setenv("THRUM_MODULE", "other-module")

		otherAgentID := identity.GenerateAgentID(repoID, "other-agent", "other-module", "")
		registerReq2 := RegisterRequest{Role: "other-agent", Module: "other-module"}
		registerParams2, _ := json.Marshal(registerReq2)
		_, err := agentHandler.HandleRegister(context.Background(), registerParams2)
		if err != nil {
			t.Fatalf("failed to register second agent: %v", err)
		}

		sessionReq2 := SessionStartRequest{AgentID: otherAgentID}
		sessionParams2, _ := json.Marshal(sessionReq2)
		_, err = sessionHandler.HandleStart(context.Background(), sessionParams2)
		if err != nil {
			t.Fatalf("failed to start second session: %v", err)
		}

		// Try to edit original message (created by first agent)
		req := EditRequest{
			MessageID: messageID,
			Content:   "Unauthorized edit attempt",
		}
		params, _ := json.Marshal(req)

		_, err = handler.HandleEdit(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for editing message from different author")
		}
		if !strings.Contains(err.Error(), "only message author can edit") {
			t.Errorf("unexpected error: %v", err)
		}

		// Switch back to original agent for remaining tests
		t.Setenv("THRUM_ROLE", "tester")
		t.Setenv("THRUM_MODULE", "test-module")
	})

	t.Run("validation - empty message_id", func(t *testing.T) {
		req := EditRequest{
			MessageID: "",
			Content:   "Some content",
		}
		params, _ := json.Marshal(req)

		_, err := handler.HandleEdit(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for empty message_id")
		}
		if err.Error() != "message_id is required" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("edit message with scopes and refs", func(t *testing.T) {
		// Create a message with scopes and refs
		sendReq := SendRequest{
			Content: "Message with scopes and refs",
			Scopes: []types.Scope{
				{Type: "module", Value: "auth"},
			},
			Refs: []types.Ref{
				{Type: "issue", Value: "beads-123"},
			},
		}
		sendParams, _ := json.Marshal(sendReq)
		sendResp, err := handler.HandleSend(context.Background(), sendParams)
		if err != nil {
			t.Fatalf("failed to send message: %v", err)
		}
		sendResponse, ok := sendResp.(*SendResponse)
		if !ok {
			t.Fatalf("expected *SendResponse, got %T", sendResp)
		}
		msgID := sendResponse.MessageID

		// Edit it
		req := EditRequest{
			MessageID: msgID,
			Content:   "Edited message with metadata",
		}
		params, _ := json.Marshal(req)
		resp, err := handler.HandleEdit(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleEdit failed: %v", err)
		}

		editResp, ok := resp.(*EditResponse)
		if !ok {
			t.Fatalf("expected *EditResponse, got %T", resp)
		}
		if editResp.MessageID != msgID {
			t.Errorf("expected message_id '%s', got '%s'", msgID, editResp.MessageID)
		}

		// Verify content was updated
		var content string
		query := `SELECT body_content FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, msgID).Scan(&content)
		if err != nil {
			t.Fatalf("failed to query message: %v", err)
		}
		if content != "Edited message with metadata" {
			t.Errorf("expected content 'Edited message with metadata', got '%s'", content)
		}
	})

	t.Run("validation - no content or structured", func(t *testing.T) {
		req := EditRequest{
			MessageID: messageID,
		}
		params, _ := json.Marshal(req)

		_, err := handler.HandleEdit(context.Background(), params)
		if err == nil {
			t.Fatal("expected error when neither content nor structured provided")
		}
		if err.Error() != "at least one of content or structured must be provided" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("edit non-existent message", func(t *testing.T) {
		req := EditRequest{
			MessageID: "msg_NONEXISTENT",
			Content:   "Trying to edit non-existent",
		}
		params, _ := json.Marshal(req)

		_, err := handler.HandleEdit(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for non-existent message")
		}
		if err.Error() != "message not found: msg_NONEXISTENT" {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestMessageMarkRead(t *testing.T) {
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
	t.Setenv("THRUM_ROLE", "reader")
	t.Setenv("THRUM_MODULE", "test-module")

	// Register agent
	agentID := identity.GenerateAgentID(repoID, "reader", "test-module", "")
	agentHandler := NewAgentHandler(st)
	registerReq := RegisterRequest{
		Role:   "reader",
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
	sessionStartResp, ok := sessionResp.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", sessionResp)
	}
	sessionID := sessionStartResp.SessionID

	// Create message handler
	handler := NewMessageHandler(st)

	// Create some test messages
	var messageIDs []string
	for i := 0; i < 3; i++ {
		req := SendRequest{
			Content: fmt.Sprintf("Test message %d", i+1),
			Format:  "markdown",
		}
		params, _ := json.Marshal(req)
		resp, err := handler.HandleSend(context.Background(), params)
		if err != nil {
			t.Fatalf("failed to send message %d: %v", i+1, err)
		}
		sendResponse, ok := resp.(*SendResponse)
		if !ok {
			t.Fatalf("expected *SendResponse, got %T", resp)
		}
		messageIDs = append(messageIDs, sendResponse.MessageID)
	}

	t.Run("mark single message as read", func(t *testing.T) {
		req := MarkReadRequest{
			MessageIDs: []string{messageIDs[0]},
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleMarkRead(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleMarkRead failed: %v", err)
		}

		markReadResp, ok := resp.(*MarkReadResponse)
		if !ok {
			t.Fatalf("expected *MarkReadResponse, got %T", resp)
		}

		if markReadResp.MarkedCount != 1 {
			t.Errorf("expected marked_count 1, got %d", markReadResp.MarkedCount)
		}

		// Verify read record was created in database
		var count int
		query := `SELECT COUNT(*) FROM message_reads WHERE message_id = ? AND session_id = ? AND agent_id = ?`
		err = st.DB().QueryRow(query, messageIDs[0], sessionID, agentID).Scan(&count)
		if err != nil {
			t.Fatalf("failed to query message_reads: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 read record, found %d", count)
		}
	})

	t.Run("mark multiple messages as read (batch)", func(t *testing.T) {
		req := MarkReadRequest{
			MessageIDs: []string{messageIDs[1], messageIDs[2]},
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleMarkRead(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleMarkRead failed: %v", err)
		}

		markReadResp, ok := resp.(*MarkReadResponse)
		if !ok {
			t.Fatalf("expected *MarkReadResponse, got %T", resp)
		}
		if markReadResp.MarkedCount != 2 {
			t.Errorf("expected marked_count 2, got %d", markReadResp.MarkedCount)
		}

		// Verify all messages have read records
		for _, msgID := range []string{messageIDs[1], messageIDs[2]} {
			var exists bool
			query := `SELECT EXISTS(SELECT 1 FROM message_reads WHERE message_id = ?)`
			err = st.DB().QueryRow(query, msgID).Scan(&exists)
			if err != nil {
				t.Fatalf("failed to check read record for %s: %v", msgID, err)
			}
			if !exists {
				t.Errorf("expected read record for message %s", msgID)
			}
		}
	})

	t.Run("idempotent - marking already-read message updates timestamp", func(t *testing.T) {
		// Mark message again
		req := MarkReadRequest{
			MessageIDs: []string{messageIDs[0]},
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleMarkRead(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleMarkRead failed: %v", err)
		}

		markReadResp, ok := resp.(*MarkReadResponse)
		if !ok {
			t.Fatalf("expected *MarkReadResponse, got %T", resp)
		}
		if markReadResp.MarkedCount != 1 {
			t.Errorf("expected marked_count 1, got %d", markReadResp.MarkedCount)
		}

		// Verify still only 1 record (not duplicated)
		var count int
		query := `SELECT COUNT(*) FROM message_reads WHERE message_id = ? AND session_id = ?`
		err = st.DB().QueryRow(query, messageIDs[0], sessionID).Scan(&count)
		if err != nil {
			t.Fatalf("failed to query message_reads: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 read record after re-marking, found %d", count)
		}
	})

	t.Run("collaboration detection - other agent read same message", func(t *testing.T) {
		// Create second agent
		t.Setenv("THRUM_ROLE", "collaborator")
		t.Setenv("THRUM_MODULE", "test-module2")

		agent2ID := identity.GenerateAgentID(repoID, "collaborator", "test-module2", "")
		registerReq2 := RegisterRequest{
			Role:   "collaborator",
			Module: "test-module2",
		}
		registerParams2, _ := json.Marshal(registerReq2)
		_, err := agentHandler.HandleRegister(context.Background(), registerParams2)
		if err != nil {
			t.Fatalf("failed to register second agent: %v", err)
		}

		sessionReq2 := SessionStartRequest{
			AgentID: agent2ID,
		}
		sessionParams2, _ := json.Marshal(sessionReq2)
		_, err = sessionHandler.HandleStart(context.Background(), sessionParams2)
		if err != nil {
			t.Fatalf("failed to start second session: %v", err)
		}

		// Second agent marks the same message as read
		req := MarkReadRequest{
			MessageIDs: []string{messageIDs[0]},
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleMarkRead(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleMarkRead failed: %v", err)
		}

		markReadResp, ok := resp.(*MarkReadResponse)
		if !ok {
			t.Fatalf("expected *MarkReadResponse, got %T", resp)
		}
		if markReadResp.MarkedCount != 1 {
			t.Errorf("expected marked_count 1, got %d", markReadResp.MarkedCount)
		}

		// Verify also_read_by contains first agent
		if markReadResp.AlsoReadBy == nil {
			t.Fatal("expected also_read_by field, got nil")
		}
		otherAgents, ok := markReadResp.AlsoReadBy[messageIDs[0]]
		if !ok {
			t.Fatalf("expected also_read_by entry for message %s", messageIDs[0])
		}
		if len(otherAgents) != 1 {
			t.Fatalf("expected 1 other agent, got %d", len(otherAgents))
		}
		if otherAgents[0] != agentID {
			t.Errorf("expected other agent %s, got %s", agentID, otherAgents[0])
		}

		// Verify 2 read records exist (one per session)
		var count int
		query := `SELECT COUNT(*) FROM message_reads WHERE message_id = ?`
		err = st.DB().QueryRow(query, messageIDs[0]).Scan(&count)
		if err != nil {
			t.Fatalf("failed to count read records: %v", err)
		}
		if count != 2 {
			t.Errorf("expected 2 read records (one per session), found %d", count)
		}

		// Switch back to original agent
		t.Setenv("THRUM_ROLE", "reader")
		t.Setenv("THRUM_MODULE", "test-module")
	})

	t.Run("skip non-existent message IDs", func(t *testing.T) {
		req := MarkReadRequest{
			MessageIDs: []string{messageIDs[1], "msg_NONEXISTENT", messageIDs[2]},
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleMarkRead(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleMarkRead failed: %v", err)
		}

		markReadResp, ok := resp.(*MarkReadResponse)
		if !ok {
			t.Fatalf("expected *MarkReadResponse, got %T", resp)
		}
		// Should mark 2 (skip the non-existent one)
		if markReadResp.MarkedCount != 2 {
			t.Errorf("expected marked_count 2 (skipping non-existent), got %d", markReadResp.MarkedCount)
		}
	})

	t.Run("validation - empty message_ids", func(t *testing.T) {
		req := MarkReadRequest{
			MessageIDs: []string{},
		}
		params, _ := json.Marshal(req)

		_, err := handler.HandleMarkRead(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for empty message_ids")
		}
		if !strings.Contains(err.Error(), "message_ids is required") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("same agent, multiple sessions - both create separate read records", func(t *testing.T) {
		// Start a second session for the same agent
		sessionReq3 := SessionStartRequest{
			AgentID: agentID,
		}
		sessionParams3, _ := json.Marshal(sessionReq3)
		sessionResp3, err := sessionHandler.HandleStart(context.Background(), sessionParams3)
		if err != nil {
			t.Fatalf("failed to start third session: %v", err)
		}
		sessionStartResp3, ok := sessionResp3.(*SessionStartResponse)
		if !ok {
			t.Fatalf("expected *SessionStartResponse, got %T", sessionResp3)
		}
		session3ID := sessionStartResp3.SessionID

		// Create a new message
		sendReq := SendRequest{Content: "Multi-session test"}
		sendParams, _ := json.Marshal(sendReq)
		sendResp, err := handler.HandleSend(context.Background(), sendParams)
		if err != nil {
			t.Fatalf("failed to send message: %v", err)
		}
		sendResponse, ok := sendResp.(*SendResponse)
		if !ok {
			t.Fatalf("expected *SendResponse, got %T", sendResp)
		}
		newMsgID := sendResponse.MessageID

		// Mark as read from first session (simulate by querying active session)
		req := MarkReadRequest{
			MessageIDs: []string{newMsgID},
		}
		params, _ := json.Marshal(req)

		// This will use the most recent active session (session3)
		resp, err := handler.HandleMarkRead(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleMarkRead failed: %v", err)
		}

		markReadResp, ok := resp.(*MarkReadResponse)
		if !ok {
			t.Fatalf("expected *MarkReadResponse, got %T", resp)
		}
		if markReadResp.MarkedCount != 1 {
			t.Errorf("expected marked_count 1, got %d", markReadResp.MarkedCount)
		}

		// Verify read record exists for session3
		var count int
		query := `SELECT COUNT(*) FROM message_reads WHERE message_id = ? AND session_id = ?`
		err = st.DB().QueryRow(query, newMsgID, session3ID).Scan(&count)
		if err != nil {
			t.Fatalf("failed to query message_reads: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 read record for session3, found %d", count)
		}
	})
}

func TestHandleSend_GroupScope(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create .thrum dir: %v", err)
	}

	repoID := "r_GROUPSCOPE_TEST"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	t.Setenv("THRUM_ROLE", "coordinator")
	t.Setenv("THRUM_MODULE", "core")

	agentID := identity.GenerateAgentID(repoID, "coordinator", "core", "")
	agentHandler := NewAgentHandler(st)
	registerParams, _ := json.Marshal(RegisterRequest{Role: "coordinator", Module: "core"})
	if _, err := agentHandler.HandleRegister(context.Background(), registerParams); err != nil {
		t.Fatalf("register agent: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionParams, _ := json.Marshal(SessionStartRequest{AgentID: agentID})
	if _, err := sessionHandler.HandleStart(context.Background(), sessionParams); err != nil {
		t.Fatalf("start session: %v", err)
	}

	// Create a group
	groupHandler := NewGroupHandler(st)
	createReq, _ := json.Marshal(GroupCreateRequest{Name: "reviewers", Description: "Code reviewers"})
	if _, err := groupHandler.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create group: %v", err)
	}

	handler := NewMessageHandler(st)

	t.Run("send_to_group_stores_group_scope", func(t *testing.T) {
		sendReq, _ := json.Marshal(SendRequest{
			Content:       "Please review",
			Mentions:      []string{"@reviewers"},
			CallerAgentID: agentID,
		})

		resp, err := handler.HandleSend(context.Background(), sendReq)
		if err != nil {
			t.Fatalf("HandleSend: %v", err)
		}
		sendResp := resp.(*SendResponse)

		// Check scopes — should have group scope
		var scopeType, scopeValue string
		err = st.DB().QueryRow(
			"SELECT scope_type, scope_value FROM message_scopes WHERE message_id = ?",
			sendResp.MessageID,
		).Scan(&scopeType, &scopeValue)
		if err != nil {
			t.Fatalf("query scope: %v", err)
		}
		if scopeType != "group" || scopeValue != "reviewers" {
			t.Errorf("expected scope group:reviewers, got %s:%s", scopeType, scopeValue)
		}

		// Check refs — should have group ref (not mention ref)
		var refType, refValue string
		err = st.DB().QueryRow(
			"SELECT ref_type, ref_value FROM message_refs WHERE message_id = ?",
			sendResp.MessageID,
		).Scan(&refType, &refValue)
		if err != nil {
			t.Fatalf("query ref: %v", err)
		}
		if refType != "group" || refValue != "reviewers" {
			t.Errorf("expected ref group:reviewers, got %s:%s", refType, refValue)
		}
	})

	t.Run("send_to_non_group_stores_mention_ref", func(t *testing.T) {
		sendReq, _ := json.Marshal(SendRequest{
			Content:       "Hey alice",
			Mentions:      []string{"@alice"},
			CallerAgentID: agentID,
		})

		resp, err := handler.HandleSend(context.Background(), sendReq)
		if err != nil {
			t.Fatalf("HandleSend: %v", err)
		}
		sendResp := resp.(*SendResponse)

		// Should have mention ref, not group scope
		var refType, refValue string
		err = st.DB().QueryRow(
			"SELECT ref_type, ref_value FROM message_refs WHERE message_id = ?",
			sendResp.MessageID,
		).Scan(&refType, &refValue)
		if err != nil {
			t.Fatalf("query ref: %v", err)
		}
		if refType != "mention" || refValue != "alice" {
			t.Errorf("expected ref mention:alice, got %s:%s", refType, refValue)
		}

		// No group scopes
		var scopeCount int
		err = st.DB().QueryRow(
			"SELECT COUNT(*) FROM message_scopes WHERE message_id = ? AND scope_type = 'group'",
			sendResp.MessageID,
		).Scan(&scopeCount)
		if err != nil {
			t.Fatalf("count scopes: %v", err)
		}
		if scopeCount != 0 {
			t.Errorf("expected 0 group scopes, got %d", scopeCount)
		}
	})

	t.Run("send_to_everyone_stores_group_scope", func(t *testing.T) {
		// Create @everyone
		if err := EnsureEveryoneGroup(context.Background(), st); err != nil {
			t.Fatalf("ensure everyone: %v", err)
		}

		sendReq, _ := json.Marshal(SendRequest{
			Content:       "Hello everyone",
			Mentions:      []string{"@everyone"},
			CallerAgentID: agentID,
		})

		resp, err := handler.HandleSend(context.Background(), sendReq)
		if err != nil {
			t.Fatalf("HandleSend: %v", err)
		}
		sendResp := resp.(*SendResponse)

		var scopeType, scopeValue string
		err = st.DB().QueryRow(
			"SELECT scope_type, scope_value FROM message_scopes WHERE message_id = ?",
			sendResp.MessageID,
		).Scan(&scopeType, &scopeValue)
		if err != nil {
			t.Fatalf("query scope: %v", err)
		}
		if scopeType != "group" || scopeValue != "everyone" {
			t.Errorf("expected scope group:everyone, got %s:%s", scopeType, scopeValue)
		}
	})
}

func TestInboxGroupMembership(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create .thrum dir: %v", err)
	}

	repoID := "r_INBOX_GROUP_TEST"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	// Register sender agent
	t.Setenv("THRUM_ROLE", "coordinator")
	t.Setenv("THRUM_MODULE", "core")

	senderID := identity.GenerateAgentID(repoID, "coordinator", "core", "")
	agentHandler := NewAgentHandler(st)
	registerParams, _ := json.Marshal(RegisterRequest{Role: "coordinator", Module: "core"})
	if _, err := agentHandler.HandleRegister(context.Background(), registerParams); err != nil {
		t.Fatalf("register sender: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionParams, _ := json.Marshal(SessionStartRequest{AgentID: senderID})
	if _, err := sessionHandler.HandleStart(context.Background(), sessionParams); err != nil {
		t.Fatalf("start sender session: %v", err)
	}

	// Register a reviewer agent
	reviewerID := identity.GenerateAgentID(repoID, "reviewer", "auth", "")
	registerParams2, _ := json.Marshal(RegisterRequest{
		Role:   "reviewer",
		Module: "auth",
	})
	if _, err := agentHandler.HandleRegister(context.Background(), registerParams2); err != nil {
		t.Fatalf("register reviewer: %v", err)
	}
	sessionParams2, _ := json.Marshal(SessionStartRequest{AgentID: reviewerID})
	if _, err := sessionHandler.HandleStart(context.Background(), sessionParams2); err != nil {
		t.Fatalf("start reviewer session: %v", err)
	}

	// Create @everyone and @reviewers groups
	if err := EnsureEveryoneGroup(context.Background(), st); err != nil {
		t.Fatalf("ensure everyone: %v", err)
	}

	groupHandler := NewGroupHandler(st)
	createReq, _ := json.Marshal(GroupCreateRequest{Name: "reviewers", Description: "Review team"})
	if _, err := groupHandler.HandleCreate(context.Background(), createReq); err != nil {
		t.Fatalf("create reviewers group: %v", err)
	}
	addReq, _ := json.Marshal(GroupMemberAddRequest{
		Group:       "reviewers",
		MemberType:  "agent",
		MemberValue: reviewerID,
	})
	if _, err := groupHandler.HandleMemberAdd(context.Background(), addReq); err != nil {
		t.Fatalf("add reviewer to group: %v", err)
	}

	msgHandler := NewMessageHandler(st)

	// Send a message to @reviewers
	sendReq, _ := json.Marshal(SendRequest{
		Content:       "Please review auth module",
		Mentions:      []string{"@reviewers"},
		CallerAgentID: senderID,
	})
	resp, err := msgHandler.HandleSend(context.Background(), sendReq)
	if err != nil {
		t.Fatalf("send to reviewers: %v", err)
	}
	reviewMsgID := resp.(*SendResponse).MessageID

	// Send a message to @everyone
	sendReq2, _ := json.Marshal(SendRequest{
		Content:       "Standup in 5",
		Mentions:      []string{"@everyone"},
		CallerAgentID: senderID,
	})
	resp, err = msgHandler.HandleSend(context.Background(), sendReq2)
	if err != nil {
		t.Fatalf("send to everyone: %v", err)
	}
	everyoneMsgID := resp.(*SendResponse).MessageID

	// Send a direct mention to the reviewer
	sendReq3, _ := json.Marshal(SendRequest{
		Content:       "Hey reviewer, quick question",
		Mentions:      []string{"@reviewer"},
		CallerAgentID: senderID,
	})
	resp, err = msgHandler.HandleSend(context.Background(), sendReq3)
	if err != nil {
		t.Fatalf("send direct mention: %v", err)
	}
	directMsgID := resp.(*SendResponse).MessageID

	t.Run("reviewer_sees_group_messages", func(t *testing.T) {
		listReq, _ := json.Marshal(ListMessagesRequest{
			ForAgent:     reviewerID,
			ForAgentRole: "reviewer",
			PageSize:     50,
		})
		resp, err := msgHandler.HandleList(context.Background(), listReq)
		if err != nil {
			t.Fatalf("HandleList: %v", err)
		}
		listResp := resp.(*ListMessagesResponse)

		// Reviewer should see: @reviewers msg, @everyone msg, direct mention msg
		foundReview := false
		foundEveryone := false
		foundDirect := false
		for _, msg := range listResp.Messages {
			switch msg.MessageID {
			case reviewMsgID:
				foundReview = true
			case everyoneMsgID:
				foundEveryone = true
			case directMsgID:
				foundDirect = true
			}
		}

		if !foundReview {
			t.Error("reviewer should see message to @reviewers group")
		}
		if !foundEveryone {
			t.Error("reviewer should see message to @everyone group")
		}
		if !foundDirect {
			t.Error("reviewer should see direct mention message")
		}
	})

	t.Run("non_member_does_not_see_group_messages", func(t *testing.T) {
		// Sender is not in @reviewers, should NOT see that message when filtering for_agent
		listReq, _ := json.Marshal(ListMessagesRequest{
			ForAgent:     senderID,
			ForAgentRole: "coordinator",
			PageSize:     50,
		})
		resp, err := msgHandler.HandleList(context.Background(), listReq)
		if err != nil {
			t.Fatalf("HandleList: %v", err)
		}
		listResp := resp.(*ListMessagesResponse)

		foundReview := false
		foundEveryone := false
		for _, msg := range listResp.Messages {
			switch msg.MessageID {
			case reviewMsgID:
				foundReview = true
			case everyoneMsgID:
				foundEveryone = true
			}
		}

		if foundReview {
			t.Error("coordinator should NOT see message to @reviewers (not a member)")
		}
		if !foundEveryone {
			t.Error("coordinator should see message to @everyone (all agents are members)")
		}
	})
}
