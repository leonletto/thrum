package rpc

// Inbox default-sort tests (thrum-3vl0, backport of thrum-4yjc). The inbox-mode
// branch of MessageHandler.HandleList (for_agent/for_agent_role set, no explicit
// sort_order) previously returned oldest-first (thread-clustered), burying
// recent messages under backlog. It now defaults to NEWEST-first; the
// oldest-first, reply-clustered view is opt-in via Chronological. These tests
// pin both directions plus the explicit-order-wins escape hatch — the
// inbox-default path had no order coverage before.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/identity"
)

func TestMessageList_InboxDefaultSort_NewestFirstAndChronologicalOptIn(t *testing.T) {
	handler, agentID, cleanup := setupFilterTest(t)
	defer cleanup()
	ctx := context.Background()

	// ops (registered by setupFilterTest) sends three messages addressed to
	// @reviewer, in order, so they land in the reviewer's inbox-mode view.
	opsID := identity.GenerateAgentID("r_FILTER_TEST", "ops", "core", "")
	for _, content := range []string{"oldest", "middle", "newest"} {
		sendParams, _ := json.Marshal(SendRequest{
			Content:       content,
			Mentions:      []string{"@reviewer"},
			CallerAgentID: opsID,
		})
		if _, err := handler.HandleSend(ctx, sendParams); err != nil {
			t.Fatalf("send %q: %v", content, err)
		}
	}

	listInbox := func(t *testing.T, req ListMessagesRequest) *ListMessagesResponse {
		t.Helper()
		params, _ := json.Marshal(req)
		resp, err := handler.HandleList(ctx, params)
		if err != nil {
			t.Fatalf("HandleList: %v", err)
		}
		listResp, ok := resp.(*ListMessagesResponse)
		if !ok {
			t.Fatalf("expected *ListMessagesResponse, got %T", resp)
		}
		return listResp
	}

	inboxReq := func(chrono bool) ListMessagesRequest {
		return ListMessagesRequest{
			ForAgent:      agentID,
			ForAgentRole:  "reviewer",
			PageSize:      100,
			Chronological: chrono,
		}
	}

	t.Run("default is newest-first", func(t *testing.T) {
		resp := listInbox(t, inboxReq(false))
		if len(resp.Messages) < 3 {
			t.Fatalf("want >=3 messages, got %d: %+v", len(resp.Messages), resp.Messages)
		}
		if resp.Messages[0].Body.Content != "newest" {
			t.Errorf("default inbox order Messages[0]=%q, want newest-first 'newest'", resp.Messages[0].Body.Content)
		}
	})

	t.Run("chronological opt-in is oldest-first", func(t *testing.T) {
		resp := listInbox(t, inboxReq(true))
		if len(resp.Messages) < 3 {
			t.Fatalf("want >=3 messages, got %d: %+v", len(resp.Messages), resp.Messages)
		}
		if resp.Messages[0].Body.Content != "oldest" {
			t.Errorf("chronological inbox order Messages[0]=%q, want oldest-first 'oldest'", resp.Messages[0].Body.Content)
		}
	})

	// An explicit sort_order must still win over the inbox-mode default — the
	// path MCP check_messages (asc) and wait (desc) rely on.
	t.Run("explicit sort_order overrides inbox default", func(t *testing.T) {
		req := inboxReq(false)
		req.SortOrder = "asc"
		resp := listInbox(t, req)
		if len(resp.Messages) < 3 {
			t.Fatalf("want >=3 messages, got %d", len(resp.Messages))
		}
		if resp.Messages[0].Body.Content != "oldest" {
			t.Errorf("explicit asc Messages[0]=%q, want 'oldest'", resp.Messages[0].Body.Content)
		}
	})
}
