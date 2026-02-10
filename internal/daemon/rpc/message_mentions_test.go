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

func TestMessageSend_WithMentions(t *testing.T) {
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
	_, err = sessionHandler.HandleStart(context.Background(), sessionParams)
	if err != nil {
		t.Fatalf("failed to start session: %v", err)
	}

	// Create message handler
	handler := NewMessageHandler(st)

	// Send message with mentions
	req := SendRequest{
		Content:  "Hey @reviewer, please check this code",
		Format:   "markdown",
		Mentions: []string{"@reviewer", "implementer"}, // Mix of @-prefixed and non-prefixed
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

	// Verify mentions were stored as refs
	query := `SELECT ref_type, ref_value FROM message_refs WHERE message_id = ? ORDER BY ref_value`
	rows, err := st.DB().Query(query, sendResp.MessageID)
	if err != nil {
		t.Fatalf("failed to query refs: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var refs []struct {
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
		refs = append(refs, ref)
	}

	if len(refs) != 2 {
		t.Errorf("expected 2 mention refs, got %d", len(refs))
	}

	// Check that @ was stripped and refs are sorted
	expectedRefs := []struct {
		Type  string
		Value string
	}{
		{"mention", "implementer"},
		{"mention", "reviewer"},
	}

	for i, expected := range expectedRefs {
		if i >= len(refs) {
			break
		}
		if refs[i].Type != expected.Type {
			t.Errorf("ref[%d]: expected type=%s, got %s", i, expected.Type, refs[i].Type)
		}
		if refs[i].Value != expected.Value {
			t.Errorf("ref[%d]: expected value=%s, got %s", i, expected.Value, refs[i].Value)
		}
	}
}
