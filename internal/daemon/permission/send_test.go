package permission

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// newPermissionWithRealState constructs a Permission wired to a fresh
// *state.State backed by a temp directory. Used by send_test to verify
// that SendSupervisorMessage round-trips through the projector all the
// way to the messages table.
func newPermissionWithRealState(t *testing.T) *Permission {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_SENDTEST", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	return New(st, st.RawDB(), "supervisor_thrum", "thrum", thrumDir)
}

func TestSendSupervisorMessage_WritesToMessagesTable(t *testing.T) {
	p := newPermissionWithRealState(t)
	ctx := context.Background()

	msgID, err := p.SendSupervisorMessage(ctx, "@coordinator_main", "# Test nudge\n\nRun: `y`")
	if err != nil {
		t.Fatalf("SendSupervisorMessage: %v", err)
	}
	if msgID == "" {
		t.Fatal("expected non-empty message_id")
	}

	// Verify the row landed in the messages table with the supervisor
	// agent_id (not "system") and the sentinel session_id.
	var agentID, sessionID, bodyContent string
	err = p.state.RawDB().QueryRow(
		"SELECT agent_id, session_id, body_content FROM messages WHERE message_id = ?",
		msgID,
	).Scan(&agentID, &sessionID, &bodyContent)
	if err != nil {
		t.Fatalf("query messages: %v", err)
	}
	if agentID != "supervisor_thrum" {
		t.Errorf("agent_id = %q, want supervisor_thrum", agentID)
	}
	if sessionID != "supervisor" {
		t.Errorf("session_id = %q, want supervisor", sessionID)
	}
	if !strings.Contains(bodyContent, "Test nudge") {
		t.Errorf("body_content = %q, want containing 'Test nudge'", bodyContent)
	}
}

func TestSendSupervisorMessage_RegistersRefAndRecipient(t *testing.T) {
	p := newPermissionWithRealState(t)
	ctx := context.Background()

	recipient := "@coordinator_main"
	msgID, err := p.SendSupervisorMessage(ctx, recipient, "hello")
	if err != nil {
		t.Fatalf("SendSupervisorMessage: %v", err)
	}

	// A mention Ref should have been projected for delivery.
	var refValue string
	err = p.state.RawDB().QueryRow(
		"SELECT ref_value FROM message_refs WHERE message_id = ? AND ref_type = 'mention'",
		msgID,
	).Scan(&refValue)
	if err != nil {
		t.Fatalf("query message_refs: %v", err)
	}
	if refValue != recipient {
		t.Errorf("ref_value = %q, want %q", refValue, recipient)
	}
}

func TestSendSupervisorMessage_NilStateReturnsError(t *testing.T) {
	p := New(nil, nil, "supervisor_thrum", "thrum", ".")
	_, err := p.SendSupervisorMessage(context.Background(), "@foo", "body")
	if err == nil {
		t.Fatal("expected error with nil state")
	}
	if !strings.Contains(err.Error(), "nil state") {
		t.Errorf("error = %v, want to mention nil state", err)
	}
}

func TestSendSupervisorMessage_EmptyRecipientErrors(t *testing.T) {
	p := newPermissionWithRealState(t)
	_, err := p.SendSupervisorMessage(context.Background(), "", "body")
	if err == nil {
		t.Fatal("expected error with empty recipient")
	}
}
