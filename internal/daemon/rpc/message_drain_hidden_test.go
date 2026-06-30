package rpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/identity"
)

// insertHiddenFeeder inserts a message scoped to a group the agent is NOT in,
// plus an unread delivery row for the agent — the f37v3 filter-hidden relay
// shape that accumulates as "N unread outside your filter" backstop fuel.
func insertHiddenFeeder(t *testing.T, handler *MessageHandler, agentID, msgID, createdAt string) {
	t.Helper()
	db := handler.state.RawDB()
	if _, err := db.Exec(`INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES (?, 'agent:author:OTH', 'ses_x', ?, 'markdown', 'storm relay')`, msgID, createdAt); err != nil {
		t.Fatalf("insert feeder message %s: %v", msgID, err)
	}
	if _, err := db.Exec(`INSERT INTO message_scopes (message_id, scope_type, scope_value) VALUES (?, 'group', 'a-group-im-not-in')`, msgID); err != nil {
		t.Fatalf("insert feeder scope %s: %v", msgID, err)
	}
	if _, err := db.Exec(`INSERT INTO message_deliveries (message_id, recipient_agent_id, delivered_at, read_at) VALUES (?, ?, 't', NULL)`, msgID, agentID); err != nil {
		t.Fatalf("insert feeder delivery %s: %v", msgID, err)
	}
}

func deliveryReadAt(t *testing.T, handler *MessageHandler, agentID, msgID string) (read bool) {
	t.Helper()
	var readAt *string
	if err := handler.state.RawDB().QueryRow(
		`SELECT read_at FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ?`, msgID, agentID,
	).Scan(&readAt); err != nil {
		t.Fatalf("query read_at for %s: %v", msgID, err)
	}
	return readAt != nil
}

// TestHandleDrainHidden drains filter-hidden unread deliveries to read, with
// watermark safety, without touching visible mail (thrum-f37v3 Part B). These
// rows fail recipientgate (the agent is not a legitimate recipient — e.g. a
// relay scoped to a group the agent isn't in), so `read --all` / MarkRead can
// never clear them; the drain stamps read_at directly so the "N unread outside
// your filter" residual converges to 0.
func TestHandleDrainHidden(t *testing.T) {
	handler, agentID, cleanup := setupFilterTest(t)
	defer cleanup()
	ctx := context.Background()
	opsID := identity.GenerateAgentID("r_FILTER_TEST", "ops", "core", "")

	// Pre-watermark hidden feeder — MUST be drained.
	insertHiddenFeeder(t, handler, agentID, "m_hidden_pre", "2030-01-01T00:00:00Z")
	// Post-watermark hidden feeder — MUST be preserved (arrived after listing).
	insertHiddenFeeder(t, handler, agentID, "m_hidden_post", "2040-01-01T00:00:00Z")

	// Visible @mention — MUST NOT be drained (it goes through the normal
	// receipt path so its read-state syncs to the mesh).
	visParams, _ := json.Marshal(SendRequest{
		Content: "directed ping", Mentions: []string{"@reviewer"}, CallerAgentID: opsID,
	})
	resp, err := handler.HandleSend(ctx, visParams)
	if err != nil {
		t.Fatalf("send visible: %v", err)
	}
	visibleMsgID := resp.(*SendResponse).MessageID

	// Drain with a watermark between the two hidden feeders.
	drainParams, _ := json.Marshal(DrainHiddenRequest{
		CallerAgentID: agentID,
		MarkedBefore:  "2035-01-01T00:00:00Z",
	})
	drainResp, err := handler.HandleDrainHidden(ctx, drainParams)
	if err != nil {
		t.Fatalf("HandleDrainHidden: %v", err)
	}
	got := drainResp.(*DrainHiddenResponse)
	if got.DrainedCount != 1 {
		t.Errorf("DrainedCount = %d, want 1 (only the pre-watermark hidden feeder)", got.DrainedCount)
	}

	// Pre-watermark hidden feeder is now read.
	if !deliveryReadAt(t, handler, agentID, "m_hidden_pre") {
		t.Errorf("m_hidden_pre must be marked read by the drain")
	}
	// Post-watermark hidden feeder stays unread (watermark safety).
	if deliveryReadAt(t, handler, agentID, "m_hidden_post") {
		t.Errorf("m_hidden_post arrived after the watermark — must stay unread")
	}
	// Visible mention stays unread — the hidden drain must not touch it.
	if deliveryReadAt(t, handler, agentID, visibleMsgID) {
		t.Errorf("visible mention must NOT be drained by the hidden drain")
	}
}

// TestHandleDrainHidden_NoWatermarkDrainsAll: with no watermark supplied the
// drain clears every filter-hidden unread delivery for the caller.
func TestHandleDrainHidden_NoWatermarkDrainsAll(t *testing.T) {
	handler, agentID, cleanup := setupFilterTest(t)
	defer cleanup()
	ctx := context.Background()

	insertHiddenFeeder(t, handler, agentID, "m_h1", "2030-01-01T00:00:00Z")
	insertHiddenFeeder(t, handler, agentID, "m_h2", "2031-01-01T00:00:00Z")

	drainParams, _ := json.Marshal(DrainHiddenRequest{CallerAgentID: agentID})
	drainResp, err := handler.HandleDrainHidden(ctx, drainParams)
	if err != nil {
		t.Fatalf("HandleDrainHidden: %v", err)
	}
	if got := drainResp.(*DrainHiddenResponse).DrainedCount; got != 2 {
		t.Errorf("DrainedCount = %d, want 2", got)
	}
	if !deliveryReadAt(t, handler, agentID, "m_h1") || !deliveryReadAt(t, handler, agentID, "m_h2") {
		t.Errorf("both hidden feeders must be drained with no watermark")
	}
}
