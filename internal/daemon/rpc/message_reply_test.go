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

func setupReplyTest(t *testing.T) (*MessageHandler, *state.State, string) {
	t.Helper()

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum directory: %v", err)
	}

	repoID := "r_REPLYTEST1234"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	t.Setenv("THRUM_ROLE", "tester")
	t.Setenv("THRUM_MODULE", "test-module")
	t.Setenv("THRUM_DISPLAY", "Test Agent")

	agentID := identity.GenerateAgentID(repoID, "tester", "test-module", "")
	agentHandler := NewAgentHandler(st)
	registerParams, _ := json.Marshal(RegisterRequest{Role: "tester", Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), registerParams); err != nil {
		t.Fatalf("register: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionParams, _ := json.Marshal(SessionStartRequest{AgentID: agentID})
	if _, err := sessionHandler.HandleStart(context.Background(), sessionParams); err != nil {
		t.Fatalf("session: %v", err)
	}

	handler := NewMessageHandler(st)
	return handler, st, agentID
}

func sendTestMessage(t *testing.T, handler *MessageHandler, content string) string {
	t.Helper()
	params, _ := json.Marshal(SendRequest{Content: content})
	resp, err := handler.HandleSend(context.Background(), params)
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	return resp.(*SendResponse).MessageID
}

func TestReplyTo_RefCreated(t *testing.T) {
	handler, st, _ := setupReplyTest(t)

	// Send parent message
	parentID := sendTestMessage(t, handler, "Parent message")

	// Send reply
	replyParams, _ := json.Marshal(SendRequest{
		Content: "This is a reply",
		ReplyTo: parentID,
	})
	resp, err := handler.HandleSend(context.Background(), replyParams)
	if err != nil {
		t.Fatalf("send reply: %v", err)
	}
	replyID := resp.(*SendResponse).MessageID

	// Verify reply_to ref exists in message_refs
	var refType, refValue string
	err = st.RawDB().QueryRow(
		`SELECT ref_type, ref_value FROM message_refs WHERE message_id = ? AND ref_type = 'reply_to'`,
		replyID,
	).Scan(&refType, &refValue)
	if err != nil {
		t.Fatalf("query reply_to ref: %v", err)
	}
	if refValue != parentID {
		t.Errorf("expected reply_to ref value %q, got %q", parentID, refValue)
	}
}

func TestReplyTo_InvalidParent(t *testing.T) {
	handler, _, _ := setupReplyTest(t)

	params, _ := json.Marshal(SendRequest{
		Content: "Reply to nothing",
		ReplyTo: "msg_nonexistent",
	})
	_, err := handler.HandleSend(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for invalid reply_to, got nil")
	}
	if got := err.Error(); got != "reply_to message not found: msg_nonexistent" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestReplyTo_NoReplyTo_BackwardCompat(t *testing.T) {
	handler, st, _ := setupReplyTest(t)

	msgID := sendTestMessage(t, handler, "Regular message")

	// Verify no reply_to ref exists
	var count int
	err := st.RawDB().QueryRow(
		`SELECT COUNT(1) FROM message_refs WHERE message_id = ? AND ref_type = 'reply_to'`,
		msgID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no reply_to ref, got %d", count)
	}
}

func TestReplyTo_GetReturnsReplyTo(t *testing.T) {
	handler, _, _ := setupReplyTest(t)

	parentID := sendTestMessage(t, handler, "Parent")
	replyParams, _ := json.Marshal(SendRequest{Content: "Reply", ReplyTo: parentID})
	resp, err := handler.HandleSend(context.Background(), replyParams)
	if err != nil {
		t.Fatalf("send reply: %v", err)
	}
	replyID := resp.(*SendResponse).MessageID

	// Get the reply message
	getParams, _ := json.Marshal(GetMessageRequest{MessageID: replyID})
	getResp, err := handler.HandleGet(context.Background(), getParams)
	if err != nil {
		t.Fatalf("get reply: %v", err)
	}
	msg := getResp.(*GetMessageResponse).Message
	if msg.ReplyTo != parentID {
		t.Errorf("expected ReplyTo=%q in get response, got %q", parentID, msg.ReplyTo)
	}
}

func TestReplyTo_ListReturnsReplyTo(t *testing.T) {
	handler, _, _ := setupReplyTest(t)

	parentID := sendTestMessage(t, handler, "Parent for list")
	replyParams, _ := json.Marshal(SendRequest{Content: "Reply for list", ReplyTo: parentID})
	if _, err := handler.HandleSend(context.Background(), replyParams); err != nil {
		t.Fatalf("send reply: %v", err)
	}

	// List messages
	listParams, _ := json.Marshal(ListMessagesRequest{
		PageSize:  50,
		SortOrder: "asc",
	})
	listResp, err := handler.HandleList(context.Background(), listParams)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	msgs := listResp.(*ListMessagesResponse).Messages

	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}

	// Find the reply
	var foundReply bool
	for _, m := range msgs {
		if m.Body.Content == "Reply for list" {
			foundReply = true
			if m.ReplyTo != parentID {
				t.Errorf("expected ReplyTo=%q in list, got %q", parentID, m.ReplyTo)
			}
		}
		if m.Body.Content == "Parent for list" {
			if m.ReplyTo != "" {
				t.Errorf("parent should have empty ReplyTo, got %q", m.ReplyTo)
			}
		}
	}
	if !foundReply {
		t.Error("reply message not found in list")
	}
}

func TestAutoThreadOnReply(t *testing.T) {
	handler, st, _ := setupReplyTest(t)

	// Send root message (no thread_id)
	rootID := sendTestMessage(t, handler, "Root message")

	// Send a reply to it
	replyParams, _ := json.Marshal(SendRequest{
		Content: "Reply to root",
		ReplyTo: rootID,
	})
	replyResp, err := handler.HandleSend(context.Background(), replyParams)
	if err != nil {
		t.Fatalf("send reply: %v", err)
	}
	replyID := replyResp.(*SendResponse).MessageID
	threadID := replyResp.(*SendResponse).ThreadID

	// Assert thread_id starts with "thr_"
	if threadID == "" {
		t.Fatal("expected thread_id to be set on reply, got empty string")
	}
	if len(threadID) < 4 || threadID[:4] != "thr_" {
		t.Errorf("expected thread_id to start with 'thr_', got %q", threadID)
	}

	// Assert both root and reply share the same thread_id
	var rootThreadID, replyThreadID string
	if err := st.RawDB().QueryRow(
		`SELECT COALESCE(thread_id, '') FROM messages WHERE message_id = ?`, rootID,
	).Scan(&rootThreadID); err != nil {
		t.Fatalf("query root thread_id: %v", err)
	}
	if err := st.RawDB().QueryRow(
		`SELECT COALESCE(thread_id, '') FROM messages WHERE message_id = ?`, replyID,
	).Scan(&replyThreadID); err != nil {
		t.Fatalf("query reply thread_id: %v", err)
	}

	if rootThreadID != threadID {
		t.Errorf("root thread_id = %q, want %q", rootThreadID, threadID)
	}
	if replyThreadID != threadID {
		t.Errorf("reply thread_id = %q, want %q", replyThreadID, threadID)
	}
}

func TestAutoThreadJoinsExistingThread(t *testing.T) {
	handler, st, _ := setupReplyTest(t)

	// Send root message
	rootID := sendTestMessage(t, handler, "Root message")

	// First reply — creates a new thread
	reply1Params, _ := json.Marshal(SendRequest{Content: "First reply", ReplyTo: rootID})
	reply1Resp, err := handler.HandleSend(context.Background(), reply1Params)
	if err != nil {
		t.Fatalf("send first reply: %v", err)
	}
	reply1ID := reply1Resp.(*SendResponse).MessageID
	threadID := reply1Resp.(*SendResponse).ThreadID

	if threadID == "" {
		t.Fatal("expected thread_id after first reply")
	}

	// Second reply to the same root
	reply2Params, _ := json.Marshal(SendRequest{Content: "Second reply", ReplyTo: rootID})
	reply2Resp, err := handler.HandleSend(context.Background(), reply2Params)
	if err != nil {
		t.Fatalf("send second reply: %v", err)
	}
	reply2ID := reply2Resp.(*SendResponse).MessageID

	// All three should share the same thread_id
	for label, msgID := range map[string]string{"root": rootID, "reply1": reply1ID, "reply2": reply2ID} {
		var tid string
		if err := st.RawDB().QueryRow(
			`SELECT COALESCE(thread_id, '') FROM messages WHERE message_id = ?`, msgID,
		).Scan(&tid); err != nil {
			t.Fatalf("query %s thread_id: %v", label, err)
		}
		if tid != threadID {
			t.Errorf("%s thread_id = %q, want %q", label, tid, threadID)
		}
	}
}

func TestAutoThreadDeepChain(t *testing.T) {
	handler, st, _ := setupReplyTest(t)

	// A -> B (reply to A) -> C (reply to B)
	aID := sendTestMessage(t, handler, "Message A")

	bParams, _ := json.Marshal(SendRequest{Content: "Message B", ReplyTo: aID})
	bResp, err := handler.HandleSend(context.Background(), bParams)
	if err != nil {
		t.Fatalf("send B: %v", err)
	}
	bID := bResp.(*SendResponse).MessageID
	threadID := bResp.(*SendResponse).ThreadID

	cParams, _ := json.Marshal(SendRequest{Content: "Message C", ReplyTo: bID})
	cResp, err := handler.HandleSend(context.Background(), cParams)
	if err != nil {
		t.Fatalf("send C: %v", err)
	}
	cID := cResp.(*SendResponse).MessageID

	// A, B, C should all share the same thread_id
	for label, msgID := range map[string]string{"A": aID, "B": bID, "C": cID} {
		var tid string
		if err := st.RawDB().QueryRow(
			`SELECT COALESCE(thread_id, '') FROM messages WHERE message_id = ?`, msgID,
		).Scan(&tid); err != nil {
			t.Fatalf("query %s thread_id: %v", label, err)
		}
		if tid != threadID {
			t.Errorf("%s thread_id = %q, want %q", label, tid, threadID)
		}
	}
}
