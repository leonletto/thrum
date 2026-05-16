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

// setupSingleAgent creates a state with one registered+session-started agent and
// returns (state, agentID, handler). Caller is responsible for closing state.
func setupSingleAgent(t *testing.T, role string) (*state.State, string, *MessageHandler) {
	t.Helper()
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

	agentHandler := NewAgentHandler(st)
	sessionHandler := NewSessionHandler(st)

	agentID := identity.GenerateAgentID(repoID, role, "test-module", "")
	registerParams, _ := json.Marshal(RegisterRequest{Role: role, Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), registerParams); err != nil {
		t.Fatalf("failed to register agent: %v", err)
	}
	sessionParams, _ := json.Marshal(SessionStartRequest{AgentID: agentID})
	if _, err := sessionHandler.HandleStart(context.Background(), sessionParams); err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	return st, agentID, NewMessageHandler(st)
}

// callSend is a small ergonomic wrapper around HandleSend for tests.
func callSend(t *testing.T, h *MessageHandler, req SendRequest) *SendResponse {
	t.Helper()
	params, _ := json.Marshal(req)
	resp, err := h.HandleSend(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleSend failed: %v", err)
	}
	sendResp, ok := resp.(*SendResponse)
	if !ok {
		t.Fatalf("expected *SendResponse, got %T", resp)
	}
	return sendResp
}

// TestHandleSend_ExplicitSelfMention_ToFlag verifies --to @self keeps the
// author in the recipient set.
func TestHandleSend_ExplicitSelfMention_ToFlag(t *testing.T) {
	st, agentID, h := setupSingleAgent(t, "coordinator")
	defer func() { _ = st.Close() }()

	resp := callSend(t, h, SendRequest{
		To:            agentID,
		Content:       "note to self",
		CallerAgentID: agentID,
	})

	gotSelf := false
	for _, r := range resp.Recipients {
		if r.AgentID == agentID {
			gotSelf = true
		}
	}
	if !gotSelf {
		t.Fatalf("expected author %q in recipients, got %+v", agentID, resp.Recipients)
	}
}

// TestHandleSend_ExplicitSelfMention_RoleMention verifies --mentions @<my-role>
// keeps the author when the author has that role.
func TestHandleSend_ExplicitSelfMention_RoleMention(t *testing.T) {
	st, agentID, h := setupSingleAgent(t, "coordinator")
	defer func() { _ = st.Close() }()

	resp := callSend(t, h, SendRequest{
		Mentions:      []string{"coordinator"}, // role mention matches author
		Content:       "ping role including self",
		CallerAgentID: agentID,
	})

	gotSelf := false
	for _, r := range resp.Recipients {
		if r.AgentID == agentID {
			gotSelf = true
		}
	}
	if !gotSelf {
		t.Fatalf("expected author %q in recipients on role-self mention, got %+v", agentID, resp.Recipients)
	}
}

// TestHandleSend_BroadcastEveryone_StripsSelf verifies --to everyone still strips author.
func TestHandleSend_BroadcastEveryone_StripsSelf(t *testing.T) {
	st, agentID, h := setupSingleAgent(t, "coordinator")
	defer func() { _ = st.Close() }()

	resp := callSend(t, h, SendRequest{
		To:            "everyone",
		Content:       "global broadcast",
		CallerAgentID: agentID,
	})

	for _, r := range resp.Recipients {
		if r.AgentID == agentID {
			t.Fatalf("author %q should not be in recipients on --to everyone, got %+v", agentID, resp.Recipients)
		}
	}
}

// TestHandleSend_ImplicitBroadcast_StripsSelf verifies no-audience send still strips author.
func TestHandleSend_ImplicitBroadcast_StripsSelf(t *testing.T) {
	st, agentID, h := setupSingleAgent(t, "coordinator")
	defer func() { _ = st.Close() }()

	resp := callSend(t, h, SendRequest{
		Content:       "implicit broadcast",
		CallerAgentID: agentID,
	})

	for _, r := range resp.Recipients {
		if r.AgentID == agentID {
			t.Fatalf("author %q should not be in recipients on implicit broadcast, got %+v", agentID, resp.Recipients)
		}
	}
}

// TestSelfMention_FullPath_NotInUnread verifies the full HandleSend → projector
// roundtrip: sending --to @self creates a delivery row stamped with read_at,
// so the message does not appear in the author's --unread inbox even though
// the author is now in the recipient set.
func TestSelfMention_FullPath_NotInUnread(t *testing.T) {
	st, agentID, h := setupSingleAgent(t, "coordinator")
	defer func() { _ = st.Close() }()

	// Send to self via --to.
	_ = callSend(t, h, SendRequest{
		To:            agentID,
		Content:       "remind me about X",
		CallerAgentID: agentID,
	})

	// List unread, excluding self-authored. The self-mention should NOT
	// surface here: the projector pre-stamped read_at on the author's
	// delivery row.
	listParams, _ := json.Marshal(ListMessagesRequest{
		Unread:        true,
		ExcludeSelf:   true,
		CallerAgentID: agentID,
		ForAgent:      agentID,
	})
	resp, err := h.HandleList(context.Background(), listParams)
	if err != nil {
		t.Fatalf("HandleList failed: %v", err)
	}
	listResp, ok := resp.(*ListMessagesResponse)
	if !ok {
		t.Fatalf("expected *ListMessagesResponse, got %T", resp)
	}
	if listResp.Unread != 0 {
		t.Fatalf("expected 0 unread (self-mention pre-stamped read_at), got %d", listResp.Unread)
	}
}
