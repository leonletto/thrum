package rpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/identity"
)

// TestCountInboxVisibleUnread_ParityWithHandleList is the thrum-saj4 anti-drift
// lock (coordinator pin): CountInboxVisibleUnread — the backstop's
// visibility-aware count — must equal what `thrum inbox --unread` shows, i.e.
// the Total HandleList returns for the same agent with the for-agent + unread
// + exclude-self filters. Pinned across several audience shapes INCLUDING the
// invisible-feeder shape (a delivery row for a message scoped to a group the
// agent is NOT a member of — the saj4 phantom-nudge feeder): the raw
// message_deliveries scan counts it, the visibility predicate hides it, so both
// the inbox AND this helper must report 0.
func TestCountInboxVisibleUnread_ParityWithHandleList(t *testing.T) {
	handler, agentID, cleanup := setupFilterTest(t)
	defer cleanup()
	ctx := context.Background()
	db := handler.state.RawDB()
	opsID := identity.GenerateAgentID("r_FILTER_TEST", "ops", "core", "")

	// (1) VISIBLE unread: ops mentions @reviewer — lands in the inbox, unread.
	visParams, _ := json.Marshal(SendRequest{
		Content: "visible mention", Mentions: []string{"@reviewer"}, CallerAgentID: opsID,
	})
	if _, err := handler.HandleSend(ctx, visParams); err != nil {
		t.Fatalf("send visible: %v", err)
	}

	// (2) INVISIBLE feeder: a delivery row for the agent on a message scoped to
	// a group the agent is NOT in — the saj4 shape. Insert raw so no inbox
	// visibility is implied by construction.
	if _, err := db.Exec(`INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES ('m_feeder', 'agent:author:OTH', 'ses_x', '2030-01-01T00:00:00Z', 'markdown', 'storm relay')`); err != nil {
		t.Fatalf("insert feeder message: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO message_scopes (message_id, scope_type, scope_value) VALUES ('m_feeder', 'group', 'a-group-im-not-in')`); err != nil {
		t.Fatalf("insert feeder scope: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO message_deliveries (message_id, recipient_agent_id, delivered_at, read_at) VALUES ('m_feeder', ?, 't', NULL)`, agentID); err != nil {
		t.Fatalf("insert feeder delivery: %v", err)
	}

	// The helper's count.
	got, err := handler.CountInboxVisibleUnread(ctx, agentID)
	if err != nil {
		t.Fatalf("CountInboxVisibleUnread: %v", err)
	}

	// HandleList's count for the SAME inbox-unread view (the parity oracle).
	listParams, _ := json.Marshal(ListMessagesRequest{
		ForAgent: agentID, ForAgentRole: "reviewer",
		UnreadForAgent: agentID, ExcludeSelf: true, CallerAgentID: agentID,
		PageSize: 100,
	})
	resp, err := handler.HandleList(ctx, listParams)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	want := resp.(*ListMessagesResponse).Total

	if got != want {
		t.Errorf("CountInboxVisibleUnread = %d, HandleList Total = %d — they MUST agree (anti-drift)", got, want)
	}
	// And the feeder must be excluded: the visible-unread count is the mention
	// only (1), not 2 — the raw delivery scan would have said the agent has
	// unread (the phantom-nudge feeder).
	if got != 1 {
		t.Errorf("visible-unread = %d, want 1 (the mention; the group-scoped feeder must be hidden)", got)
	}

	// Sanity: the raw scan the backstop USED to do counts both (proving the gap).
	var raw int
	if err := db.QueryRow(`SELECT COUNT(*) FROM message_deliveries WHERE recipient_agent_id = ? AND read_at IS NULL`, agentID).Scan(&raw); err != nil {
		t.Fatalf("raw count: %v", err)
	}
	if raw < 2 {
		t.Fatalf("raw delivery scan = %d, want >=2 (mention delivery + feeder) — fixture not exercising the gap", raw)
	}
}
