package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
)

// setupFilterTest creates a test environment with an agent, session, and message handler.
func setupFilterTest(t *testing.T) (handler *MessageHandler, agentID string, cleanup func()) {
	t.Helper()

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create .thrum dir: %v", err)
	}

	repoID := "r_FILTER_TEST"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}

	t.Setenv("THRUM_ROLE", "reviewer")
	t.Setenv("THRUM_MODULE", "core")
	t.Setenv("THRUM_DISPLAY", "Reviewer Agent")

	agentID = identity.GenerateAgentID(repoID, "reviewer", "core", "")
	agentHandler := NewAgentHandler(st)
	registerReq := RegisterRequest{Role: "reviewer", Module: "core"}
	registerParams, _ := json.Marshal(registerReq)
	if _, err := agentHandler.HandleRegister(context.Background(), registerParams); err != nil {
		t.Fatalf("register agent: %v", err)
	}

	// Also register an "ops" agent so tests that send to @ops pass validation
	opsID := identity.GenerateAgentID(repoID, "ops", "core", "")
	opsRegParams, _ := json.Marshal(RegisterRequest{Role: "ops", Module: "core"})
	if _, err := agentHandler.HandleRegister(context.Background(), opsRegParams); err != nil {
		t.Fatalf("register ops agent: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionReq := SessionStartRequest{AgentID: agentID}
	sessionParams, _ := json.Marshal(sessionReq)
	sessionResp, err := sessionHandler.HandleStart(context.Background(), sessionParams)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	sessionStartResp, ok := sessionResp.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", sessionResp)
	}
	_ = sessionStartResp.SessionID // sessionID not used by callers

	// Start a session for the ops agent too
	opsSessionParams, _ := json.Marshal(SessionStartRequest{AgentID: opsID})
	if _, err := sessionHandler.HandleStart(context.Background(), opsSessionParams); err != nil {
		t.Fatalf("start ops session: %v", err)
	}

	handler = NewMessageHandler(st)
	return handler, agentID, func() { _ = st.Close() }
}

func TestMessageListMentionRoleFilter(t *testing.T) {
	handler, _, cleanup := setupFilterTest(t)
	defer cleanup()

	ctx := context.Background()

	// Send 3 messages: 2 mentioning @reviewer, 1 mentioning @ops
	for _, mention := range []string{"@reviewer", "@reviewer", "@ops"} {
		req := SendRequest{
			Content:  "Message mentioning " + mention,
			Mentions: []string{mention},
		}
		params, _ := json.Marshal(req)
		if _, err := handler.HandleSend(ctx, params); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	t.Run("filter by explicit MentionRole", func(t *testing.T) {
		req := ListMessagesRequest{MentionRole: "reviewer"}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(ctx, params)
		if err != nil {
			t.Fatalf("HandleList: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 2 {
			t.Errorf("expected 2 messages mentioning reviewer, got %d", listResp.Total)
		}
	})

	t.Run("filter by MentionRole ops", func(t *testing.T) {
		req := ListMessagesRequest{MentionRole: "ops"}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(ctx, params)
		if err != nil {
			t.Fatalf("HandleList: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 1 {
			t.Errorf("expected 1 message mentioning ops, got %d", listResp.Total)
		}
	})

	t.Run("no mention filter returns all", func(t *testing.T) {
		req := ListMessagesRequest{}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(ctx, params)
		if err != nil {
			t.Fatalf("HandleList: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 3 {
			t.Errorf("expected 3 total messages, got %d", listResp.Total)
		}
	})
}

func TestMessageListUnreadFilter(t *testing.T) {
	handler, agentID, cleanup := setupFilterTest(t)
	defer cleanup()

	ctx := context.Background()

	// Send 3 messages
	var messageIDs []string
	for i := 0; i < 3; i++ {
		req := SendRequest{Content: "Unread test message"}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleSend(ctx, params)
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		sendResp, ok := resp.(*SendResponse)
		if !ok {
			t.Fatalf("expected *SendResponse, got %T", resp)
		}
		messageIDs = append(messageIDs, sendResp.MessageID)
	}

	// Mark first message as read
	markReq := MarkReadRequest{MessageIDs: []string{messageIDs[0]}}
	markParams, _ := json.Marshal(markReq)
	if _, err := handler.HandleMarkRead(ctx, markParams); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	t.Run("unread filter with explicit agent ID", func(t *testing.T) {
		req := ListMessagesRequest{UnreadForAgent: agentID}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(ctx, params)
		if err != nil {
			t.Fatalf("HandleList: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 2 {
			t.Errorf("expected 2 unread messages, got %d", listResp.Total)
		}
	})

	t.Run("without unread filter returns all", func(t *testing.T) {
		req := ListMessagesRequest{}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(ctx, params)
		if err != nil {
			t.Fatalf("HandleList: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 3 {
			t.Errorf("expected 3 total messages, got %d", listResp.Total)
		}
	})
}

func TestMessageListCombinedFilters(t *testing.T) {
	handler, agentID, cleanup := setupFilterTest(t)
	defer cleanup()

	ctx := context.Background()

	// Send messages: 2 mentioning @reviewer, 1 mentioning @ops
	var reviewerMsgIDs []string
	for i := 0; i < 2; i++ {
		req := SendRequest{
			Content:  "Reviewer message",
			Mentions: []string{"@reviewer"},
		}
		params, _ := json.Marshal(req)
		resp, err := handler.HandleSend(ctx, params)
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		sendResp, ok := resp.(*SendResponse)
		if !ok {
			t.Fatalf("expected *SendResponse, got %T", resp)
		}
		reviewerMsgIDs = append(reviewerMsgIDs, sendResp.MessageID)
	}

	opsReq := SendRequest{Content: "Ops message", Mentions: []string{"@ops"}}
	opsParams, _ := json.Marshal(opsReq)
	if _, err := handler.HandleSend(ctx, opsParams); err != nil {
		t.Fatalf("send ops: %v", err)
	}

	// Mark first reviewer message as read
	markReq := MarkReadRequest{MessageIDs: []string{reviewerMsgIDs[0]}}
	markParams, _ := json.Marshal(markReq)
	if _, err := handler.HandleMarkRead(ctx, markParams); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	t.Run("unread mentions for reviewer", func(t *testing.T) {
		req := ListMessagesRequest{
			MentionRole:    "reviewer",
			UnreadForAgent: agentID,
		}
		params, _ := json.Marshal(req)

		resp, err := handler.HandleList(ctx, params)
		if err != nil {
			t.Fatalf("HandleList: %v", err)
		}

		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		if listResp.Total != 1 {
			t.Errorf("expected 1 unread message mentioning reviewer, got %d", listResp.Total)
		}
	})
}

// TestMessageListUnreadCountWithoutSession verifies that the unread count is
// computed correctly even when the caller has no active session. This is the
// fix for thrum-pwaa: ContextPrime calls Inbox during startup before a session
// exists, and the unread count must still be non-zero.
func TestMessageListUnreadCountWithoutSession(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create .thrum dir: %v", err)
	}

	repoID := "r_NOSESS_TEST"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()

	// Register sender agent and start a session for it (needed to send messages)
	t.Setenv("THRUM_ROLE", "sender")
	t.Setenv("THRUM_MODULE", "test")
	senderID := identity.GenerateAgentID(repoID, "sender", "test", "")
	agentHandler := NewAgentHandler(st)
	registerParams, _ := json.Marshal(RegisterRequest{Role: "sender", Module: "test"})
	if _, err := agentHandler.HandleRegister(ctx, registerParams); err != nil {
		t.Fatalf("register sender: %v", err)
	}
	sessionHandler := NewSessionHandler(st)
	sessionParams, _ := json.Marshal(SessionStartRequest{AgentID: senderID})
	if _, err := sessionHandler.HandleStart(ctx, sessionParams); err != nil {
		t.Fatalf("start sender session: %v", err)
	}

	// Register receiver agent but do NOT start a session for it
	receiverID := identity.GenerateAgentID(repoID, "receiver", "test", "")
	registerParams2, _ := json.Marshal(RegisterRequest{Role: "receiver", Module: "test"})
	if _, err := agentHandler.HandleRegister(ctx, registerParams2); err != nil {
		t.Fatalf("register receiver: %v", err)
	}

	// Send 3 messages from sender
	handler := NewMessageHandler(st)
	for i := 0; i < 3; i++ {
		req := SendRequest{
			Content:       "Test message for receiver",
			CallerAgentID: senderID,
		}
		params, _ := json.Marshal(req)
		if _, err := handler.HandleSend(ctx, params); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	// Query inbox with exclude_self and caller_agent_id for the receiver (who has NO session)
	listReq := ListMessagesRequest{
		ExcludeSelf:   true,
		CallerAgentID: receiverID,
		PageSize:      10,
	}
	listParams, _ := json.Marshal(listReq)
	resp, err := handler.HandleList(ctx, listParams)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}

	listResp, ok := resp.(*ListMessagesResponse)
	if !ok {
		t.Fatalf("expected *ListMessagesResponse, got %T", resp)
	}

	// The unread count should be 3 (all messages unread by receiver),
	// NOT 0 which was the bug when resolveAgentAndSession required a session.
	if listResp.Unread != 3 {
		t.Errorf("expected 3 unread messages, got %d (bug thrum-pwaa: unread count should work without active session)", listResp.Unread)
	}
	if listResp.Total != 3 {
		t.Errorf("expected 3 total messages, got %d", listResp.Total)
	}
}
