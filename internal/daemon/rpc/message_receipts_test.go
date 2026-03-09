package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
)

func setupReceiptTestState(t *testing.T) *state.State {
	t.Helper()

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create .thrum dir: %v", err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "r_RECEIPT_TEST")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func registerAndStartAgent(t *testing.T, st *state.State, name, role string) string {
	t.Helper()

	agentHandler := NewAgentHandler(st)
	registerParams, _ := json.Marshal(RegisterRequest{
		Name:   name,
		Role:   role,
		Module: name,
	})
	resp, err := agentHandler.HandleRegister(context.Background(), registerParams)
	if err != nil {
		t.Fatalf("register agent %s: %v", name, err)
	}
	registerResp, ok := resp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", resp)
	}

	sessionHandler := NewSessionHandler(st)
	sessionParams, _ := json.Marshal(SessionStartRequest{AgentID: registerResp.AgentID})
	if _, err := sessionHandler.HandleStart(context.Background(), sessionParams); err != nil {
		t.Fatalf("start session for %s: %v", name, err)
	}

	return registerResp.AgentID
}

func TestHandleSendSnapshotsRecipients(t *testing.T) {
	st := setupReceiptTestState(t)
	senderID := registerAndStartAgent(t, st, "coordinator_main", "coordinator")
	implAPI := registerAndStartAgent(t, st, "implementer_api", "implementer")
	implUI := registerAndStartAgent(t, st, "implementer_ui", "implementer")

	handler := NewMessageHandler(st)
	sendParams, _ := json.Marshal(SendRequest{
		Content:       "Implement the endpoint",
		Mentions:      []string{"@implementer"},
		CallerAgentID: senderID,
	})

	resp, err := handler.HandleSend(context.Background(), sendParams)
	if err != nil {
		t.Fatalf("HandleSend failed: %v", err)
	}
	sendResp := resp.(*SendResponse)

	if len(sendResp.Recipients) != 2 {
		t.Fatalf("expected 2 recipients, got %d", len(sendResp.Recipients))
	}

	got := map[string]bool{}
	for _, recipient := range sendResp.Recipients {
		got[recipient.AgentID] = true
		if recipient.DeliveredAt == "" {
			t.Fatalf("expected delivered_at for recipient %s", recipient.AgentID)
		}
	}
	if !got[implAPI] || !got[implUI] {
		t.Fatalf("expected recipients %s and %s, got %#v", implAPI, implUI, got)
	}

	var count int
	if err := st.RawDB().QueryRow(
		`SELECT COUNT(*) FROM message_deliveries WHERE message_id = ?`,
		sendResp.MessageID,
	).Scan(&count); err != nil {
		t.Fatalf("count message deliveries: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 delivery rows, got %d", count)
	}
}

func TestHandleMarkReadWritesDurableReceiptsAndOutbox(t *testing.T) {
	st := setupReceiptTestState(t)
	senderID := registerAndStartAgent(t, st, "coordinator_main", "coordinator")
	recipientID := registerAndStartAgent(t, st, "implementer_api", "implementer")

	handler := NewMessageHandler(st)
	sendParams, _ := json.Marshal(SendRequest{
		Content:       "Review this message",
		Mentions:      []string{"@implementer_api"},
		CallerAgentID: senderID,
	})

	sendRespRaw, err := handler.HandleSend(context.Background(), sendParams)
	if err != nil {
		t.Fatalf("HandleSend failed: %v", err)
	}
	sendResp := sendRespRaw.(*SendResponse)

	markParams, _ := json.Marshal(MarkReadRequest{
		MessageIDs:    []string{sendResp.MessageID},
		CallerAgentID: recipientID,
	})
	if _, err := handler.HandleMarkRead(context.Background(), markParams); err != nil {
		t.Fatalf("HandleMarkRead failed: %v", err)
	}

	var readAt string
	if err := st.RawDB().QueryRow(
		`SELECT read_at FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ?`,
		sendResp.MessageID,
		recipientID,
	).Scan(&readAt); err != nil {
		t.Fatalf("query durable read receipt: %v", err)
	}
	if readAt == "" {
		t.Fatalf("expected durable read receipt timestamp")
	}

	outboxParams, _ := json.Marshal(OutboxRequest{CallerAgentID: senderID})
	outboxRespRaw, err := handler.HandleOutbox(context.Background(), outboxParams)
	if err != nil {
		t.Fatalf("HandleOutbox failed: %v", err)
	}
	outboxResp := outboxRespRaw.(*OutboxResponse)
	if len(outboxResp.Messages) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(outboxResp.Messages))
	}
	if outboxResp.Messages[0].ReadCount != 1 {
		t.Fatalf("expected read_count=1, got %d", outboxResp.Messages[0].ReadCount)
	}
	if len(outboxResp.Messages[0].Recipients) != 1 || outboxResp.Messages[0].Recipients[0].ReadAt == "" {
		t.Fatalf("expected outbox recipient read state, got %#v", outboxResp.Messages[0].Recipients)
	}
}
