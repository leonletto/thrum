package rpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/identity"
)

// TestHandleList_HiddenByFilter_ExcludesNoDeliveryRow is thrum-eeio (backport of
// thrum-vr0i).
//
// The hidden_by_filter advisory count ("N additional unread outside filter")
// is computed as (identity-visible-without-for-agent unread) minus
// (for-agent-visible unread). With no mention/scope/ref filter the superset is
// otherwise unbounded — it counts EVERY unread message in the daemon minus the
// agent's own visible set, because forAgentClause is intentionally omitted. A
// message addressed to a DIFFERENT agent (no delivery row for the caller) was
// never delivered to the caller per event.Recipients semantics, so it must not
// inflate the caller's advisory count. The fix restricts the superset to
// delivery-backed messages.
func TestHandleList_HiddenByFilter_ExcludesNoDeliveryRow(t *testing.T) {
	st, senderID, targetID, handler := setupTwoAgents(t, "sender", "target")
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()

	// Register a third agent X and send a DIRECT message to X only. The target
	// has no delivery row for it and cannot see it via for-agent visibility.
	repoID := "r_TEST12345678" // matches setupTwoAgents
	xID := identity.GenerateAgentID(repoID, "other", "test-module", "")
	xRegParams, _ := json.Marshal(RegisterRequest{Role: "other", Module: "test-module"})
	if _, err := NewAgentHandler(st).HandleRegister(ctx, xRegParams); err != nil {
		t.Fatalf("register X: %v", err)
	}
	xSessParams, _ := json.Marshal(SessionStartRequest{AgentID: xID})
	if _, err := NewSessionHandler(st).HandleStart(ctx, xSessParams); err != nil {
		t.Fatalf("start X session: %v", err)
	}

	sendParams, _ := json.Marshal(SendRequest{
		Content: "to X only", To: "@" + xID, CallerAgentID: senderID,
	})
	if _, err := handler.HandleSend(ctx, sendParams); err != nil {
		t.Fatalf("send to X: %v", err)
	}

	// Target's inbox: the X-addressed message must NOT be counted as hidden
	// unread for the target (target holds no delivery row for it).
	listParams, _ := json.Marshal(ListMessagesRequest{
		ForAgent:      targetID,
		ForAgentRole:  "target",
		Unread:        true,
		CallerAgentID: targetID,
		PageSize:      100,
	})
	resp, err := handler.HandleList(ctx, listParams)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	listResp, ok := resp.(*ListMessagesResponse)
	if !ok {
		t.Fatalf("expected *ListMessagesResponse, got %T", resp)
	}
	if listResp.HiddenByFilter != 0 {
		t.Errorf("expected hidden_by_filter=0 (message to X has no delivery row for target), got %d", listResp.HiddenByFilter)
	}
}
