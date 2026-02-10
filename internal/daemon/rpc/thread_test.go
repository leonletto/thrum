package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
)

func TestThreadCreate(t *testing.T) {
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

	// Register agent and start session (required for resolveAgentAndSession)
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

	// Create thread handler
	handler := NewThreadHandler(st)

	t.Run("create thread with title", func(t *testing.T) {
		req := CreateThreadRequest{
			Title: "Test Thread",
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleCreate(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleCreate failed: %v", err)
		}

		createResp, ok := resp.(*CreateThreadResponse)
		if !ok {
			t.Fatalf("expected *CreateThreadResponse, got %T", resp)
		}

		if createResp.ThreadID == "" {
			t.Error("expected non-empty thread_id")
		}
		if createResp.CreatedAt == "" {
			t.Error("expected non-empty created_at")
		}

		// Verify thread was written to database
		var title string
		query := `SELECT title FROM threads WHERE thread_id = ?`
		err = st.DB().QueryRow(query, createResp.ThreadID).Scan(&title)
		if err != nil {
			t.Fatalf("failed to query thread: %v", err)
		}
		if title != "Test Thread" {
			t.Errorf("expected title 'Test Thread', got '%s'", title)
		}
	})

	t.Run("validation - empty title", func(t *testing.T) {
		req := CreateThreadRequest{
			Title: "",
		}
		params, _ := json.Marshal(req)

		_, err := handler.HandleCreate(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for empty title")
		}
		if err.Error() != "title is required" {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestThreadCreateWithInitialMessage(t *testing.T) {
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
	t.Setenv("THRUM_ROLE", "sender")
	t.Setenv("THRUM_MODULE", "test-module")

	// Register agent and start session
	agentID := identity.GenerateAgentID(repoID, "sender", "test-module", "")
	agentHandler := NewAgentHandler(st)
	registerReq := RegisterRequest{Role: "sender", Module: "test-module"}
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

	// Create thread handler
	handler := NewThreadHandler(st)

	t.Run("create thread with initial message", func(t *testing.T) {
		recipient := "agent:recipient"
		req := CreateThreadRequest{
			Title:     "Thread with initial message",
			Recipient: &recipient,
			Message: &MessageContent{
				Content: "Hello, this is the first message",
				Format:  "markdown",
			},
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleCreate(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleCreate failed: %v", err)
		}

		createResp, ok := resp.(*CreateThreadResponse)
		if !ok {
			t.Fatalf("expected *CreateThreadResponse, got %T", resp)
		}

		if createResp.ThreadID == "" {
			t.Error("expected non-empty thread_id")
		}
		if createResp.CreatedAt == "" {
			t.Error("expected non-empty created_at")
		}
		if createResp.MessageID == nil {
			t.Fatal("expected non-nil message_id")
		}
		if *createResp.MessageID == "" {
			t.Error("expected non-empty message_id")
		}

		// Verify thread was created
		var title string
		query := `SELECT title FROM threads WHERE thread_id = ?`
		err = st.DB().QueryRow(query, createResp.ThreadID).Scan(&title)
		if err != nil {
			t.Fatalf("failed to query thread: %v", err)
		}
		if title != "Thread with initial message" {
			t.Errorf("expected title 'Thread with initial message', got '%s'", title)
		}

		// Verify initial message was created
		var content, threadID string
		query = `SELECT body_content, thread_id FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, *createResp.MessageID).Scan(&content, &threadID)
		if err != nil {
			t.Fatalf("failed to query message: %v", err)
		}
		if content != "Hello, this is the first message" {
			t.Errorf("expected content 'Hello, this is the first message', got '%s'", content)
		}
		if threadID != createResp.ThreadID {
			t.Errorf("expected message thread_id '%s', got '%s'", createResp.ThreadID, threadID)
		}
	})

	t.Run("create thread without initial message (backward compatible)", func(t *testing.T) {
		req := CreateThreadRequest{
			Title: "Thread without initial message",
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleCreate(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleCreate failed: %v", err)
		}

		createResp, ok := resp.(*CreateThreadResponse)
		if !ok {
			t.Fatalf("expected *CreateThreadResponse, got %T", resp)
		}
		if createResp.MessageID != nil {
			t.Errorf("expected nil message_id for thread without initial message, got %v", createResp.MessageID)
		}

		// Verify no messages in this thread
		var count int
		query := `SELECT COUNT(*) FROM messages WHERE thread_id = ?`
		err = st.DB().QueryRow(query, createResp.ThreadID).Scan(&count)
		if err != nil {
			t.Fatalf("failed to count messages: %v", err)
		}
		if count != 0 {
			t.Errorf("expected 0 messages, got %d", count)
		}
	})

	t.Run("create thread with initial message and structured data", func(t *testing.T) {
		recipient := "agent:recipient"
		structured := map[string]any{
			"priority": "high",
			"tags":     []string{"important", "urgent"},
		}

		req := CreateThreadRequest{
			Title:     "Thread with structured message",
			Recipient: &recipient,
			Message: &MessageContent{
				Content:    "Structured message content",
				Format:     "markdown",
				Structured: structured,
			},
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleCreate(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleCreate failed: %v", err)
		}

		createResp, ok := resp.(*CreateThreadResponse)
		if !ok {
			t.Fatalf("expected *CreateThreadResponse, got %T", resp)
		}
		if createResp.MessageID == nil {
			t.Fatal("expected non-nil message_id")
		}

		// Verify structured data was stored
		var structuredJSON string
		query := `SELECT body_structured FROM messages WHERE message_id = ?`
		err = st.DB().QueryRow(query, *createResp.MessageID).Scan(&structuredJSON)
		if err != nil {
			t.Fatalf("failed to query message structured data: %v", err)
		}
		if structuredJSON == "" {
			t.Error("expected non-empty structured data")
		}
	})

	t.Run("validation - recipient without message", func(t *testing.T) {
		recipient := "agent:recipient"
		req := CreateThreadRequest{
			Title:     "Invalid thread",
			Recipient: &recipient,
			// Message is nil
		}
		params, _ := json.Marshal(req)

		_, err := handler.HandleCreate(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for recipient without message")
		}
		if err.Error() != "recipient and message must both be provided or both be nil" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("validation - message without recipient", func(t *testing.T) {
		req := CreateThreadRequest{
			Title: "Invalid thread",
			// Recipient is nil
			Message: &MessageContent{
				Content: "Message without recipient",
			},
		}
		params, _ := json.Marshal(req)

		_, err := handler.HandleCreate(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for message without recipient")
		}
		if err.Error() != "recipient and message must both be provided or both be nil" {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestThreadList(t *testing.T) {
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

	// Create thread and message handlers
	threadHandler := NewThreadHandler(st)
	messageHandler := NewMessageHandler(st)

	// Create test threads
	threads := []string{"Thread 1", "Thread 2", "Thread 3"}
	threadIDs := []string{}

	for _, title := range threads {
		req := CreateThreadRequest{Title: title}
		params, _ := json.Marshal(req)
		resp, err := threadHandler.HandleCreate(context.Background(), params)
		if err != nil {
			t.Fatalf("failed to create thread: %v", err)
		}
		createResp, ok := resp.(*CreateThreadResponse)
		if !ok {
			t.Fatalf("expected *CreateThreadResponse, got %T", resp)
		}
		threadIDs = append(threadIDs, createResp.ThreadID)
	}

	// Add messages to first thread
	for i := 0; i < 3; i++ {
		req := SendRequest{
			Content:  "Test message",
			ThreadID: threadIDs[0],
		}
		params, _ := json.Marshal(req)
		_, err := messageHandler.HandleSend(context.Background(), params)
		if err != nil {
			t.Fatalf("failed to send message: %v", err)
		}
	}

	t.Run("list all threads", func(t *testing.T) {
		req := ListThreadsRequest{}
		params, _ := json.Marshal(req)

		resp, err := threadHandler.HandleList(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok := resp.(*ListThreadsResponse)
		if !ok {
			t.Fatalf("expected *ListThreadsResponse, got %T", resp)
		}
		if listResp.Total != 3 {
			t.Errorf("expected total 3, got %d", listResp.Total)
		}
		if len(listResp.Threads) != 3 {
			t.Errorf("expected 3 threads, got %d", len(listResp.Threads))
		}
		if listResp.Page != 1 {
			t.Errorf("expected page 1, got %d", listResp.Page)
		}
		if listResp.PageSize != 10 {
			t.Errorf("expected page_size 10, got %d", listResp.PageSize)
		}

		// Verify message count for first thread
		var foundThread *ThreadSummary
		for _, thread := range listResp.Threads {
			if thread.ThreadID == threadIDs[0] {
				foundThread = &thread
				break
			}
		}
		if foundThread == nil {
			t.Fatal("thread with messages not found")
		}
		if foundThread.MessageCount != 3 {
			t.Errorf("expected message_count 3, got %d", foundThread.MessageCount)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		req := ListThreadsRequest{
			PageSize: 2,
			Page:     1,
		}
		params, _ := json.Marshal(req)

		resp, err := threadHandler.HandleList(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok := resp.(*ListThreadsResponse)
		if !ok {
			t.Fatalf("expected *ListThreadsResponse, got %T", resp)
		}
		if listResp.Total != 3 {
			t.Errorf("expected total 3, got %d", listResp.Total)
		}
		if len(listResp.Threads) != 2 {
			t.Errorf("expected 2 threads on page 1, got %d", len(listResp.Threads))
		}
		if listResp.TotalPages != 2 {
			t.Errorf("expected total_pages 2, got %d", listResp.TotalPages)
		}
	})

	t.Run("unread count, preview, and last_sender fields", func(t *testing.T) {
		// Add messages to thread 1 and mark some as read
		var messageIDs []string
		for i := 0; i < 5; i++ {
			req := SendRequest{
				Content:  fmt.Sprintf("Test message %d in thread 1", i+1),
				ThreadID: threadIDs[0],
			}
			params, _ := json.Marshal(req)
			resp, err := messageHandler.HandleSend(context.Background(), params)
			if err != nil {
				t.Fatalf("failed to send message: %v", err)
			}
			sendResp, ok := resp.(*SendResponse)
			if !ok {
				t.Fatalf("expected *SendResponse, got %T", resp)
			}
			messageIDs = append(messageIDs, sendResp.MessageID)
		}

		// Mark first 2 messages as read
		markReadReq := MarkReadRequest{
			MessageIDs: messageIDs[0:2],
		}
		markReadParams, _ := json.Marshal(markReadReq)
		_, err := messageHandler.HandleMarkRead(context.Background(), markReadParams)
		if err != nil {
			t.Fatalf("failed to mark messages as read: %v", err)
		}

		// List threads and verify unread count
		listReq := ListThreadsRequest{}
		listParams, _ := json.Marshal(listReq)
		resp, err := threadHandler.HandleList(context.Background(), listParams)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok := resp.(*ListThreadsResponse)
		if !ok {
			t.Fatalf("expected *ListThreadsResponse, got %T", resp)
		}

		// Find thread 1 in results
		var thread1 *ThreadSummary
		for i := range listResp.Threads {
			if listResp.Threads[i].ThreadID == threadIDs[0] {
				thread1 = &listResp.Threads[i]
				break
			}
		}
		if thread1 == nil {
			t.Fatal("thread 1 not found in results")
		}

		// Verify unread count (should be 6: 3 original + 5 new - 2 marked read)
		expectedUnread := 6
		if thread1.UnreadCount != expectedUnread {
			t.Errorf("expected unread_count %d, got %d", expectedUnread, thread1.UnreadCount)
		}

		// Verify last_sender is set
		if thread1.LastSender == "" {
			t.Error("expected non-empty last_sender")
		}
		if thread1.LastSender != agentID {
			t.Errorf("expected last_sender %s, got %s", agentID, thread1.LastSender)
		}

		// Verify preview is set and truncated appropriately
		if thread1.Preview == nil {
			t.Error("expected non-nil preview")
		} else {
			if *thread1.Preview == "" {
				t.Error("expected non-empty preview")
			}
			// Preview should contain part of the last message
			expectedPreviewStart := "Test message 5 in thread 1"
			if len(*thread1.Preview) < len(expectedPreviewStart) {
				t.Errorf("preview too short: got %q", *thread1.Preview)
			} else if (*thread1.Preview)[:len(expectedPreviewStart)] != expectedPreviewStart {
				t.Errorf("expected preview to start with %q, got %q", expectedPreviewStart, *thread1.Preview)
			}
			// Preview should not exceed 100 chars
			if len(*thread1.Preview) > 100 {
				t.Errorf("preview should be <= 100 chars, got %d", len(*thread1.Preview))
			}
		}

		// Verify message count includes all messages
		totalMessages := 8 // 3 from original setup + 5 new messages
		if thread1.MessageCount != totalMessages {
			t.Errorf("expected message_count %d, got %d", totalMessages, thread1.MessageCount)
		}
	})

	t.Run("empty thread has no preview or last_sender", func(t *testing.T) {
		// Thread 2 and 3 have no messages
		listReq := ListThreadsRequest{}
		listParams, _ := json.Marshal(listReq)
		resp, err := threadHandler.HandleList(context.Background(), listParams)
		if err != nil {
			t.Fatalf("HandleList failed: %v", err)
		}

		listResp, ok := resp.(*ListThreadsResponse)
		if !ok {
			t.Fatalf("expected *ListThreadsResponse, got %T", resp)
		}

		// Find thread 2 (should be empty)
		var thread2 *ThreadSummary
		for i := range listResp.Threads {
			if listResp.Threads[i].ThreadID == threadIDs[1] {
				thread2 = &listResp.Threads[i]
				break
			}
		}
		if thread2 == nil {
			t.Fatal("thread 2 not found in results")
		}

		// Verify no messages
		if thread2.MessageCount != 0 {
			t.Errorf("expected message_count 0, got %d", thread2.MessageCount)
		}

		// Verify no preview
		if thread2.Preview != nil {
			t.Errorf("expected nil preview for empty thread, got %v", thread2.Preview)
		}

		// Verify no last_sender
		if thread2.LastSender != "" {
			t.Errorf("expected empty last_sender for empty thread, got %s", thread2.LastSender)
		}

		// Unread count should be 0 for empty thread
		if thread2.UnreadCount != 0 {
			t.Errorf("expected unread_count 0 for empty thread, got %d", thread2.UnreadCount)
		}
	})
}

func TestThreadGet(t *testing.T) {
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

	// Create handlers
	threadHandler := NewThreadHandler(st)
	messageHandler := NewMessageHandler(st)

	// Create thread
	createReq := CreateThreadRequest{Title: "Test Thread"}
	createParams, _ := json.Marshal(createReq)
	createResp, err := threadHandler.HandleCreate(context.Background(), createParams)
	if err != nil {
		t.Fatalf("failed to create thread: %v", err)
	}
	createThreadResp, ok := createResp.(*CreateThreadResponse)
	if !ok {
		t.Fatalf("expected *CreateThreadResponse, got %T", createResp)
	}
	threadID := createThreadResp.ThreadID

	// Add messages to thread
	for i := 0; i < 5; i++ {
		req := SendRequest{
			Content:  fmt.Sprintf("Message %d", i+1),
			ThreadID: threadID,
		}
		params, _ := json.Marshal(req)
		_, err := messageHandler.HandleSend(context.Background(), params)
		if err != nil {
			t.Fatalf("failed to send message: %v", err)
		}
	}

	t.Run("get thread with messages", func(t *testing.T) {
		req := GetThreadRequest{ThreadID: threadID}
		params, _ := json.Marshal(req)

		resp, err := threadHandler.HandleGet(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleGet failed: %v", err)
		}

		getResp, ok := resp.(*GetThreadResponse)
		if !ok {
			t.Fatalf("expected *GetThreadResponse, got %T", resp)
		}

		if getResp.Thread.ThreadID != threadID {
			t.Errorf("expected thread_id '%s', got '%s'", threadID, getResp.Thread.ThreadID)
		}
		if getResp.Thread.Title != "Test Thread" {
			t.Errorf("expected title 'Test Thread', got '%s'", getResp.Thread.Title)
		}

		if getResp.Total != 5 {
			t.Errorf("expected total 5, got %d", getResp.Total)
		}
		if len(getResp.Messages) != 5 {
			t.Errorf("expected 5 messages, got %d", len(getResp.Messages))
		}

		// Messages should be sorted chronologically (oldest first)
		if getResp.Messages[0].Body.Content != "Message 1" {
			t.Errorf("expected first message 'Message 1', got '%s'", getResp.Messages[0].Body.Content)
		}
		if getResp.Messages[4].Body.Content != "Message 5" {
			t.Errorf("expected last message 'Message 5', got '%s'", getResp.Messages[4].Body.Content)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		req := GetThreadRequest{
			ThreadID: threadID,
			PageSize: 2,
			Page:     1,
		}
		params, _ := json.Marshal(req)

		resp, err := threadHandler.HandleGet(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleGet failed: %v", err)
		}

		getResp, ok := resp.(*GetThreadResponse)
		if !ok {
			t.Fatalf("expected *GetThreadResponse, got %T", resp)
		}
		if getResp.Total != 5 {
			t.Errorf("expected total 5, got %d", getResp.Total)
		}
		if len(getResp.Messages) != 2 {
			t.Errorf("expected 2 messages on page 1, got %d", len(getResp.Messages))
		}
		if getResp.TotalPages != 3 {
			t.Errorf("expected total_pages 3, got %d", getResp.TotalPages)
		}

		// Page 2
		req.Page = 2
		params, _ = json.Marshal(req)
		resp, err = threadHandler.HandleGet(context.Background(), params)
		if err != nil {
			t.Fatalf("HandleGet failed: %v", err)
		}

		getResp, ok = resp.(*GetThreadResponse)
		if !ok {
			t.Fatalf("expected *GetThreadResponse, got %T", resp)
		}
		if len(getResp.Messages) != 2 {
			t.Errorf("expected 2 messages on page 2, got %d", len(getResp.Messages))
		}
	})

	t.Run("get non-existent thread", func(t *testing.T) {
		req := GetThreadRequest{ThreadID: "thr_NONEXISTENT"}
		params, _ := json.Marshal(req)

		_, err := threadHandler.HandleGet(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for non-existent thread")
		}
		if err.Error() != "thread not found: thr_NONEXISTENT" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("validation - empty thread_id", func(t *testing.T) {
		req := GetThreadRequest{ThreadID: ""}
		params, _ := json.Marshal(req)

		_, err := threadHandler.HandleGet(context.Background(), params)
		if err == nil {
			t.Fatal("expected error for empty thread_id")
		}
		if err.Error() != "thread_id is required" {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
