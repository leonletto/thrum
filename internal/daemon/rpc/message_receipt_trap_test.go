package rpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// countReceiptEventsState returns the number of message.receipt events in the
// durable events table for a specific (messageID, agentID) pair. This is the
// storm vector: each emitted event is broadcast to every mesh peer, so the
// thrum-1846 fix is about EVENT EMISSION, not just delivery-row stamping.
func countReceiptEventsState(t *testing.T, st *state.State, messageID, agentID string) int {
	t.Helper()
	var n int
	if err := st.RawDB().QueryRow(
		`SELECT COUNT(*) FROM events
		 WHERE type = 'message.receipt'
		   AND event_json LIKE '%' || ? || '%'
		   AND event_json LIKE '%' || ? || '%'`,
		messageID, agentID,
	).Scan(&n); err != nil {
		t.Fatalf("count receipt events: %v", err)
	}
	return n
}

// markRead is a small helper that drives HandleMarkRead and returns the typed
// response.
func markRead(t *testing.T, h *MessageHandler, callerAgentID string, messageIDs ...string) *MarkReadResponse {
	t.Helper()
	params, _ := json.Marshal(MarkReadRequest{
		MessageIDs:    messageIDs,
		CallerAgentID: callerAgentID,
	})
	respRaw, err := h.HandleMarkRead(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleMarkRead failed: %v", err)
	}
	resp, ok := respRaw.(*MarkReadResponse)
	if !ok {
		t.Fatalf("expected *MarkReadResponse, got %T", respRaw)
	}
	return resp
}

// send is a small helper that sends a message mentioning the given targets and
// returns the new message ID.
func send(t *testing.T, h *MessageHandler, senderID, content string, mentions ...string) string {
	t.Helper()
	params, _ := json.Marshal(SendRequest{
		Content:       content,
		Mentions:      mentions,
		CallerAgentID: senderID,
	})
	respRaw, err := h.HandleSend(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleSend failed: %v", err)
	}
	return respRaw.(*SendResponse).MessageID
}

// Defect 1: read --all must NOT emit a read receipt for a message the caller is
// not a recipient of. Reproduces the production evidence: leonair emitted a
// read receipt for a message whose sole recipient was leondev.
func TestMarkRead_NonRecipient_EmitsNoReceipt(t *testing.T) {
	st := setupReceiptTestState(t)
	senderID := registerAndStartAgent(t, st, "coordinator_main", "coordinator")
	recipientID := registerAndStartAgent(t, st, "implementer_b", "implementer")
	outsiderID := registerAndStartAgent(t, st, "researcher_c", "researcher")

	h := NewMessageHandler(st)
	msgID := send(t, h, senderID, "for B only", "@implementer_b")

	// Outsider C (not a recipient) tries to mark B's message read — the
	// read --all storm scenario where C's filter-visible inbox lists mail
	// addressed to other agents.
	resp := markRead(t, h, outsiderID, msgID)

	if resp.MarkedCount != 0 {
		t.Fatalf("non-recipient mark should mark 0, got %d", resp.MarkedCount)
	}
	if n := countReceiptEventsState(t, st, msgID, outsiderID); n != 0 {
		t.Fatalf("non-recipient mark must emit 0 receipt events, got %d", n)
	}
	// Sanity: no read-stamped delivery row fabricated for the outsider.
	var rows int
	if err := st.RawDB().QueryRow(
		`SELECT COUNT(*) FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ? AND read_at IS NOT NULL`,
		msgID, outsiderID,
	).Scan(&rows); err != nil {
		t.Fatalf("query delivery rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("non-recipient mark must not stamp a delivery row, got %d", rows)
	}

	// The legitimate recipient is unaffected and can still mark it.
	rresp := markRead(t, h, recipientID, msgID)
	if rresp.MarkedCount != 1 {
		t.Fatalf("recipient mark should mark 1, got %d", rresp.MarkedCount)
	}
}

// Defect 2: re-marking an already-read message must be a no-op — no fresh
// receipt event (production observed duplicate receipts with identical
// timestamps).
func TestMarkRead_AlreadyRead_IsIdempotent(t *testing.T) {
	st := setupReceiptTestState(t)
	senderID := registerAndStartAgent(t, st, "coordinator_main", "coordinator")
	recipientID := registerAndStartAgent(t, st, "implementer_b", "implementer")

	h := NewMessageHandler(st)
	msgID := send(t, h, senderID, "review", "@implementer_b")

	first := markRead(t, h, recipientID, msgID)
	if first.MarkedCount != 1 {
		t.Fatalf("first mark should mark 1, got %d", first.MarkedCount)
	}
	if n := countReceiptEventsState(t, st, msgID, recipientID); n != 1 {
		t.Fatalf("first mark should emit exactly 1 receipt event, got %d", n)
	}

	second := markRead(t, h, recipientID, msgID)
	if second.MarkedCount != 0 {
		t.Fatalf("re-mark should mark 0, got %d", second.MarkedCount)
	}
	if n := countReceiptEventsState(t, st, msgID, recipientID); n != 1 {
		t.Fatalf("re-mark must NOT emit a new receipt event; want 1 total, got %d", n)
	}
}

// Defect 3: MarkableRemaining counts only messages the caller can legitimately
// mark — never other agents' filter-visible mail. This is what kills the
// "N unread remaining (run again to mark more)" infinite-retry trap.
func TestMarkRead_MarkableRemaining_ExcludesNonRecipientMail(t *testing.T) {
	st := setupReceiptTestState(t)
	senderID := registerAndStartAgent(t, st, "coordinator_main", "coordinator")
	bID := registerAndStartAgent(t, st, "implementer_b", "implementer")
	registerAndStartAgent(t, st, "researcher_c", "researcher")

	h := NewMessageHandler(st)
	msg1 := send(t, h, senderID, "B task 1", "@implementer_b")
	msg2 := send(t, h, senderID, "B task 2", "@implementer_b")
	msg3 := send(t, h, senderID, "C task", "@researcher_c") // not B's mail

	// B marks only msg1. One legit unread (msg2) remains; msg3 is not B's
	// and must not count.
	r1 := markRead(t, h, bID, msg1)
	if r1.MarkedCount != 1 {
		t.Fatalf("want MarkedCount 1, got %d", r1.MarkedCount)
	}
	if r1.MarkableRemaining != 1 {
		t.Fatalf("want MarkableRemaining 1 (msg2 only; msg3 excluded), got %d", r1.MarkableRemaining)
	}

	// B marks msg2. No legit unread remains → drop the "run again" hint.
	r2 := markRead(t, h, bID, msg2)
	if r2.MarkedCount != 1 {
		t.Fatalf("want MarkedCount 1, got %d", r2.MarkedCount)
	}
	if r2.MarkableRemaining != 0 {
		t.Fatalf("want MarkableRemaining 0, got %d", r2.MarkableRemaining)
	}

	// The trap itself: supplying msg3 (not B's mail) marks nothing AND
	// reports 0 markable remaining — so the CLI never says "run again".
	r3 := markRead(t, h, bID, msg3)
	if r3.MarkedCount != 0 {
		t.Fatalf("want MarkedCount 0 for non-recipient mail, got %d", r3.MarkedCount)
	}
	if r3.MarkableRemaining != 0 {
		t.Fatalf("want MarkableRemaining 0 (no legit unread left), got %d", r3.MarkableRemaining)
	}
}
