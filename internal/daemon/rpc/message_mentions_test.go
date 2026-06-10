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

// setupTwoAgents creates a state with two registered+session-started agents and returns
// (state, senderID, targetID, handler). Caller is responsible for closing state.
func setupTwoAgents(t *testing.T, senderRole, targetRole string) (*state.State, string, string, *MessageHandler) {
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

	senderID := identity.GenerateAgentID(repoID, senderRole, "test-module", "")
	senderParams, _ := json.Marshal(RegisterRequest{Role: senderRole, Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), senderParams); err != nil {
		t.Fatalf("failed to register sender: %v", err)
	}
	senderSessionParams, _ := json.Marshal(SessionStartRequest{AgentID: senderID})
	if _, err := sessionHandler.HandleStart(context.Background(), senderSessionParams); err != nil {
		t.Fatalf("failed to start sender session: %v", err)
	}

	targetID := identity.GenerateAgentID(repoID, targetRole, "test-module", "")
	targetParams, _ := json.Marshal(RegisterRequest{Role: targetRole, Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), targetParams); err != nil {
		t.Fatalf("failed to register target: %v", err)
	}
	targetSessionParams, _ := json.Marshal(SessionStartRequest{AgentID: targetID})
	if _, err := sessionHandler.HandleStart(context.Background(), targetSessionParams); err != nil {
		t.Fatalf("failed to start target session: %v", err)
	}

	handler := NewMessageHandler(st)
	return st, senderID, targetID, handler
}

// TestSendToStrictTo_AgentWorks verifies that SendRequest.To with a valid agent_id
// succeeds and creates a mention ref for the target agent.
func TestSendToStrictTo_AgentWorks(t *testing.T) {
	st, senderID, targetID, handler := setupTwoAgents(t, "sender", "target")
	defer func() { _ = st.Close() }()

	req := SendRequest{
		Content:       "direct message to agent",
		To:            "@" + targetID,
		CallerAgentID: senderID,
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
	if sendResp.ResolvedTo < 1 {
		t.Errorf("expected at least 1 resolved recipient, got %d", sendResp.ResolvedTo)
	}

	// Verify a mention ref was created for the target agent_id
	var refValue string
	err = st.RawDB().QueryRow(
		`SELECT ref_value FROM message_refs WHERE message_id = ? AND ref_type = 'mention'`,
		sendResp.MessageID,
	).Scan(&refValue)
	if err != nil {
		t.Fatalf("failed to query mention ref: %v", err)
	}
	if refValue != targetID {
		t.Errorf("expected mention ref for %q, got %q", targetID, refValue)
	}
}

// TestSendToStrictTo_EveryoneWorks verifies that SendRequest.To = "@everyone"
// succeeds and creates a broadcast scope (scope_type='broadcast', scope_value='everyone').
func TestSendToStrictTo_EveryoneWorks(t *testing.T) {
	st, senderID, _, handler := setupTwoAgents(t, "sender", "target")
	defer func() { _ = st.Close() }()

	req := SendRequest{
		Content:       "broadcast to everyone",
		To:            "@everyone",
		CallerAgentID: senderID,
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
	if sendResp.ResolvedTo < 1 {
		t.Errorf("expected at least 1 resolved recipient, got %d", sendResp.ResolvedTo)
	}

	// Verify broadcast scope was stored in message_scopes
	var scopeType, scopeValue string
	err = st.RawDB().QueryRow(
		`SELECT scope_type, scope_value FROM message_scopes WHERE message_id = ? AND scope_type = 'broadcast'`,
		sendResp.MessageID,
	).Scan(&scopeType, &scopeValue)
	if err != nil {
		t.Fatalf("failed to query broadcast scope: %v", err)
	}
	if scopeType != "broadcast" || scopeValue != "everyone" {
		t.Errorf("expected scope broadcast/everyone, got %s/%s", scopeType, scopeValue)
	}
}

// TestSendToStrictTo_RoleRejected verifies that SendRequest.To with a role name
// (not an agent_id) is rejected with "unknown recipient".
func TestSendToStrictTo_RoleRejected(t *testing.T) {
	st, senderID, _, handler := setupTwoAgents(t, "sender", "implementer")
	defer func() { _ = st.Close() }()

	// "implementer" is a role, not an agent_id — strict lookup must reject it
	req := SendRequest{
		Content:       "trying to send to a role",
		To:            "@implementer",
		CallerAgentID: senderID,
	}
	params, _ := json.Marshal(req)

	resp, err := handler.HandleSend(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for role-based To, got nil")
	}
	if !strings.Contains(err.Error(), "unknown recipient") {
		t.Errorf("error should contain 'unknown recipient', got: %v", err)
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
}

// TestSendToDefunctGroup verifies that sending via Mentions to an unknown group/handle
// returns an error containing "unknown recipient".
func TestSendToDefunctGroup(t *testing.T) {
	st, senderID, _, handler := setupTwoAgents(t, "sender", "target")
	defer func() { _ = st.Close() }()

	req := SendRequest{
		Content:       "ping",
		Mentions:      []string{"@some_old_group"},
		CallerAgentID: senderID,
	}
	params, _ := json.Marshal(req)

	resp, err := handler.HandleSend(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for defunct group, got nil")
	}
	if !strings.Contains(err.Error(), "unknown recipient") {
		t.Errorf("error should contain 'unknown recipient', got: %v", err)
	}
	if !strings.Contains(err.Error(), "@some_old_group") {
		t.Errorf("error should list '@some_old_group', got: %v", err)
	}
	if resp != nil {
		t.Error("response should be nil when error is returned")
	}
}

// TestSendToEveryone_BroadcastScope verifies that sending via Mentions @everyone
// results in scope_type='broadcast' and scope_value='everyone' in message_scopes.
func TestSendToEveryone_BroadcastScope(t *testing.T) {
	st, senderID, _, handler := setupTwoAgents(t, "sender", "target")
	defer func() { _ = st.Close() }()

	req := SendRequest{
		Content:       "broadcast via mentions",
		Mentions:      []string{"@everyone"},
		CallerAgentID: senderID,
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

	// Verify broadcast scope in message_scopes
	var scopeType, scopeValue string
	err = st.RawDB().QueryRow(
		`SELECT scope_type, scope_value FROM message_scopes WHERE message_id = ? AND scope_type = 'broadcast'`,
		sendResp.MessageID,
	).Scan(&scopeType, &scopeValue)
	if err != nil {
		t.Fatalf("failed to query broadcast scope: %v", err)
	}
	if scopeType != "broadcast" {
		t.Errorf("expected scope_type='broadcast', got %q", scopeType)
	}
	if scopeValue != "everyone" {
		t.Errorf("expected scope_value='everyone', got %q", scopeValue)
	}
}

// TestSendToStrictTo_ImplicitBroadcastBlocked verifies that using SendRequest.To
// with an agent_id delivers to ONLY that agent, not all agents — confirming the fix
// for the implicit broadcast bug (len(req.Mentions)==0 with no To set).
func TestSendToStrictTo_ImplicitBroadcastBlocked(t *testing.T) {
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

	agentHandler := NewAgentHandler(st)
	sessionHandler := NewSessionHandler(st)

	// Register 3 agents: sender, target, bystander
	senderID := identity.GenerateAgentID(repoID, "sender", "test-module", "")
	senderParams, _ := json.Marshal(RegisterRequest{Role: "sender", Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), senderParams); err != nil {
		t.Fatalf("failed to register sender: %v", err)
	}
	senderSessionParams, _ := json.Marshal(SessionStartRequest{AgentID: senderID})
	if _, err := sessionHandler.HandleStart(context.Background(), senderSessionParams); err != nil {
		t.Fatalf("failed to start sender session: %v", err)
	}

	targetID := identity.GenerateAgentID(repoID, "target", "test-module", "")
	targetParams, _ := json.Marshal(RegisterRequest{Role: "target", Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), targetParams); err != nil {
		t.Fatalf("failed to register target: %v", err)
	}
	targetSessionParams, _ := json.Marshal(SessionStartRequest{AgentID: targetID})
	if _, err := sessionHandler.HandleStart(context.Background(), targetSessionParams); err != nil {
		t.Fatalf("failed to start target session: %v", err)
	}

	bystanderID := identity.GenerateAgentID(repoID, "bystander", "test-module", "")
	bystanderParams, _ := json.Marshal(RegisterRequest{Role: "bystander", Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), bystanderParams); err != nil {
		t.Fatalf("failed to register bystander: %v", err)
	}
	bystanderSessionParams, _ := json.Marshal(SessionStartRequest{AgentID: bystanderID})
	if _, err := sessionHandler.HandleStart(context.Background(), bystanderSessionParams); err != nil {
		t.Fatalf("failed to start bystander session: %v", err)
	}

	handler := NewMessageHandler(st)

	req := SendRequest{
		Content:       "private message to target only",
		To:            "@" + targetID,
		CallerAgentID: senderID,
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

	// Verify delivery records: target should have a delivery, bystander should NOT
	var targetDeliveries int
	_ = st.RawDB().QueryRow(
		`SELECT COUNT(*) FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ?`,
		sendResp.MessageID, targetID,
	).Scan(&targetDeliveries)
	if targetDeliveries != 1 {
		t.Errorf("expected 1 delivery record for target, got %d", targetDeliveries)
	}

	var bystanderDeliveries int
	_ = st.RawDB().QueryRow(
		`SELECT COUNT(*) FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ?`,
		sendResp.MessageID, bystanderID,
	).Scan(&bystanderDeliveries)
	if bystanderDeliveries != 0 {
		t.Errorf("expected 0 delivery records for bystander, got %d (implicit broadcast bug)", bystanderDeliveries)
	}

	// Confirm total recipient deliveries == 1 (target only). The sender's own
	// read-stamped self-delivery row (thrum-b6qw Option C) is excluded — it is
	// My-Inbox bookkeeping, not an implicit-broadcast leak.
	var totalDeliveries int
	_ = st.RawDB().QueryRow(
		`SELECT COUNT(*) FROM message_deliveries WHERE message_id = ? AND recipient_agent_id != ?`,
		sendResp.MessageID, senderID,
	).Scan(&totalDeliveries)
	if totalDeliveries != 1 {
		t.Errorf("expected exactly 1 total recipient delivery record, got %d", totalDeliveries)
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

// thrum-qb62 Bug 1: message.get should return Audiences with Type="agent" when
// the mention ref's value matches a known agent_id — not Type="mention". The
// previous implementation derived audiences from refs alone and lost the
// distinction between direct agent targeting (--to @agent_id) and permissive
// role/name mentions. This caused the misleading CLI display that seeded the
// original qb62 diagnosis.
func TestMessageGet_AudiencesPreserveAgentType(t *testing.T) {
	st, senderID, targetID, handler := setupTwoAgents(t, "sender", "target")
	defer func() { _ = st.Close() }()

	// Send directly to the target agent_id via the strict --to path.
	sendReq := SendRequest{
		Content:       "direct agent-targeted message",
		To:            "@" + targetID,
		CallerAgentID: senderID,
	}
	sendParams, _ := json.Marshal(sendReq)
	sendResp, err := handler.HandleSend(context.Background(), sendParams)
	if err != nil {
		t.Fatalf("HandleSend: %v", err)
	}
	sr, ok := sendResp.(*SendResponse)
	if !ok {
		t.Fatalf("expected *SendResponse, got %T", sendResp)
	}

	// HandleGet on the stored message should reconstruct Audiences as
	// [{Type:"agent", Value:<targetID>}] — matching what the send-time
	// response reported, not the regressed [{Type:"mention"}].
	getReq := GetMessageRequest{MessageID: sr.MessageID}
	getParams, _ := json.Marshal(getReq)
	getResp, err := handler.HandleGet(context.Background(), getParams)
	if err != nil {
		t.Fatalf("HandleGet: %v", err)
	}
	gr, ok := getResp.(*GetMessageResponse)
	if !ok {
		t.Fatalf("expected *GetMessageResponse, got %T", getResp)
	}

	if len(gr.Message.Audiences) != 1 {
		t.Fatalf("expected 1 audience, got %d: %+v", len(gr.Message.Audiences), gr.Message.Audiences)
	}
	got := gr.Message.Audiences[0]
	if got.Type != "agent" {
		t.Errorf("expected Audience.Type=\"agent\" for known agent_id, got %q (value=%q)", got.Type, got.Value)
	}
	if got.Value != targetID {
		t.Errorf("expected Audience.Value=%q, got %q", targetID, got.Value)
	}
}

// Role-based mentions should remain Type="mention" — extractAudiences must
// only promote to "agent" when ref.value is an exact agent_id.
func TestMessageGet_AudiencesKeepMentionForRoleReferences(t *testing.T) {
	st, senderID, _, handler := setupTwoAgents(t, "sender", "reviewer")
	defer func() { _ = st.Close() }()

	// Send via Mentions (the permissive path) to the "reviewer" role — this
	// targets the role, which happens to resolve to a single agent but is
	// stored as a role mention, not a direct agent mention.
	sendReq := SendRequest{
		Content:       "role-targeted message",
		Mentions:      []string{"@reviewer"},
		CallerAgentID: senderID,
	}
	sendParams, _ := json.Marshal(sendReq)
	sendResp, err := handler.HandleSend(context.Background(), sendParams)
	if err != nil {
		t.Fatalf("HandleSend: %v", err)
	}
	sr := sendResp.(*SendResponse)

	getReq := GetMessageRequest{MessageID: sr.MessageID}
	getParams, _ := json.Marshal(getReq)
	getResp, err := handler.HandleGet(context.Background(), getParams)
	if err != nil {
		t.Fatalf("HandleGet: %v", err)
	}
	gr := getResp.(*GetMessageResponse)

	if len(gr.Message.Audiences) != 1 {
		t.Fatalf("expected 1 audience, got %d: %+v", len(gr.Message.Audiences), gr.Message.Audiences)
	}
	got := gr.Message.Audiences[0]
	// Role-based mention — "reviewer" is not a literal agent_id (agent_ids
	// are structured like reviewer_test_module_...) so audience Type must
	// stay at "mention".
	if got.Type != "mention" {
		t.Errorf("expected Audience.Type=\"mention\" for role-based mention, got %q (value=%q)", got.Type, got.Value)
	}
	if got.Value != "reviewer" {
		t.Errorf("expected Audience.Value=\"reviewer\", got %q", got.Value)
	}
}
