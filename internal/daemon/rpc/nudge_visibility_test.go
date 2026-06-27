package rpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/identity"
)

// TestIsMessageVisibleToAgent_GatesPerMessageNudge is the thrum-f37v3 Part-A
// lock: the per-message tmux delivery-nudge must fire ONLY for a message the
// recipient's for-agent inbox filter would SHOW. A directed @mention is
// visible (gate true → nudge); a message scoped to a group the agent is NOT in
// is hidden (gate false → no nudge) — the filter-hidden relay shape that
// floods resident agents with phantom "check inbox" nudges they can never
// clear from a filtered inbox.
//
// This mirrors the invisible-feeder shape pinned by
// TestCountInboxVisibleUnread_ParityWithHandleList (saj4 backstop gate); here
// the per-message path gets the same predicate.
func TestIsMessageVisibleToAgent_GatesPerMessageNudge(t *testing.T) {
	handler, agentID, cleanup := setupFilterTest(t)
	defer cleanup()
	ctx := context.Background()
	db := handler.state.RawDB()
	opsID := identity.GenerateAgentID("r_FILTER_TEST", "ops", "core", "")

	// VISIBLE: ops @mentions reviewer — lands in the inbox, gate must pass.
	visParams, _ := json.Marshal(SendRequest{
		Content: "directed ping", Mentions: []string{"@reviewer"}, CallerAgentID: opsID,
	})
	resp, err := handler.HandleSend(ctx, visParams)
	if err != nil {
		t.Fatalf("send visible: %v", err)
	}
	visibleMsgID := resp.(*SendResponse).MessageID

	// HIDDEN: a delivery row for the agent on a message scoped to a group the
	// agent is NOT in — the saj4/f37v3 relay shape. Insert raw so no inbox
	// visibility is implied by construction.
	if _, err := db.Exec(`INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES ('m_hidden_relay', 'agent:author:OTH', 'ses_x', '2030-01-01T00:00:00Z', 'markdown', 'storm relay')`); err != nil {
		t.Fatalf("insert hidden message: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO message_scopes (message_id, scope_type, scope_value) VALUES ('m_hidden_relay', 'group', 'a-group-im-not-in')`); err != nil {
		t.Fatalf("insert hidden scope: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO message_deliveries (message_id, recipient_agent_id, delivered_at, read_at) VALUES ('m_hidden_relay', ?, 't', NULL)`, agentID); err != nil {
		t.Fatalf("insert hidden delivery: %v", err)
	}

	gotVisible, err := handler.IsMessageVisibleToAgent(ctx, visibleMsgID, agentID)
	if err != nil {
		t.Fatalf("IsMessageVisibleToAgent(visible): %v", err)
	}
	if !gotVisible {
		t.Errorf("directed @mention must be VISIBLE (gate passes → nudge fires)")
	}

	gotHidden, err := handler.IsMessageVisibleToAgent(ctx, "m_hidden_relay", agentID)
	if err != nil {
		t.Fatalf("IsMessageVisibleToAgent(hidden): %v", err)
	}
	if gotHidden {
		t.Errorf("group-scoped relay the agent can't see must be HIDDEN (gate fails → no nudge)")
	}
}

// TestIsMessageVisibleToAgent_ParityWithInbox pins the per-message gate to the
// inbox listing: a message is visible to the gate iff HandleList shows it for
// the same agent's for-agent + exclude-self view. Anti-drift companion to
// TestCountInboxVisibleUnread_ParityWithHandleList.
func TestIsMessageVisibleToAgent_ParityWithInbox(t *testing.T) {
	handler, agentID, cleanup := setupFilterTest(t)
	defer cleanup()
	ctx := context.Background()
	opsID := identity.GenerateAgentID("r_FILTER_TEST", "ops", "core", "")

	visParams, _ := json.Marshal(SendRequest{
		Content: "directed ping", Mentions: []string{"@reviewer"}, CallerAgentID: opsID,
	})
	resp, err := handler.HandleSend(ctx, visParams)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	msgID := resp.(*SendResponse).MessageID

	gotVisible, err := handler.IsMessageVisibleToAgent(ctx, msgID, agentID)
	if err != nil {
		t.Fatalf("IsMessageVisibleToAgent: %v", err)
	}

	// Oracle: does HandleList's for-agent view contain this message id?
	listParams, _ := json.Marshal(ListMessagesRequest{
		ForAgent: agentID, ForAgentRole: "reviewer",
		ExcludeSelf: true, CallerAgentID: agentID, PageSize: 100,
	})
	listResp, err := handler.HandleList(ctx, listParams)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	inboxHasIt := false
	for _, m := range listResp.(*ListMessagesResponse).Messages {
		if m.MessageID == msgID {
			inboxHasIt = true
			break
		}
	}

	if gotVisible != inboxHasIt {
		t.Errorf("gate visible=%v but inbox-shows=%v — they MUST agree (anti-drift)", gotVisible, inboxHasIt)
	}
}

// TestIsMessageVisibleToAgent_ExcludesSelfAuthored ensures the gate excludes
// the agent's own sends (sender never nudges itself), matching the exclude-self
// arm of the inbox-unread view.
func TestIsMessageVisibleToAgent_ExcludesSelfAuthored(t *testing.T) {
	handler, agentID, cleanup := setupFilterTest(t)
	defer cleanup()
	ctx := context.Background()

	// reviewer @mentions itself; the message is authored by the agent.
	selfParams, _ := json.Marshal(SendRequest{
		Content: "note to self", Mentions: []string{"@reviewer"}, CallerAgentID: agentID,
	})
	resp, err := handler.HandleSend(ctx, selfParams)
	if err != nil {
		t.Fatalf("send self: %v", err)
	}
	msgID := resp.(*SendResponse).MessageID

	got, err := handler.IsMessageVisibleToAgent(ctx, msgID, agentID)
	if err != nil {
		t.Fatalf("IsMessageVisibleToAgent: %v", err)
	}
	if got {
		t.Errorf("self-authored message must NOT count as visible for a nudge to the author")
	}
}
