package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/types"
)

func TestSubscribe(t *testing.T) {
	tests := []struct {
		name    string
		request SubscribeRequest
		wantErr bool
	}{
		{
			name: "subscribe with scope",
			request: SubscribeRequest{
				Scope: &types.Scope{Type: "module", Value: "auth"},
			},
			wantErr: false,
		},
		{
			name: "subscribe with mention_role",
			request: SubscribeRequest{
				MentionRole: stringPtr("implementer"),
			},
			wantErr: false,
		},
		{
			name: "subscribe to all",
			request: SubscribeRequest{
				All: true,
			},
			wantErr: false,
		},
		{
			name:    "validation - missing all parameters",
			request: SubscribeRequest{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment
			tmpDir := t.TempDir()
			thrumDir := filepath.Join(tmpDir, ".thrum")
			if err := os.MkdirAll(thrumDir, 0750); err != nil {
				t.Fatalf("failed to create .thrum directory: %v", err)
			}

			// Create state and handler
			repoID := "r_TEST12345678"
			st, err := state.NewState(thrumDir, thrumDir, repoID)
			if err != nil {
				t.Fatalf("failed to create state: %v", err)
			}
			defer func() { _ = st.Close() }()

			handler := NewSubscriptionHandler(st)

			// Create agent and session first
			testSessionID := setupAgentAndSession(t, st, repoID)
			t.Setenv("THRUM_ROLE", "test-role")
			t.Setenv("THRUM_MODULE", "test-module")

			// Call subscribe
			params, _ := json.Marshal(tt.request)
			ctx := context.Background()
			resp, err := handler.HandleSubscribe(ctx, params)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("HandleSubscribe() error: %v", err)
			}

			subResp, ok := resp.(*SubscribeResponse)
			if !ok {
				t.Fatalf("Expected SubscribeResponse, got %T", resp)
			}

			if subResp.SubscriptionID == 0 {
				t.Error("Expected non-zero subscription ID")
			}
			if subResp.SessionID != testSessionID {
				t.Errorf("Expected session_id='%s', got '%s'", testSessionID, subResp.SessionID)
			}
		})
	}
}

func TestUnsubscribe(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum directory: %v", err)
	}

	repoID := "r_TEST12345678"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	handler := NewSubscriptionHandler(st)

	// Setup agent and session
	testSessionID := setupAgentAndSession(t, st, repoID)
	t.Setenv("THRUM_ROLE", "test-role")
	t.Setenv("THRUM_MODULE", "test-module")

	// Create a subscription first
	subscribeReq := SubscribeRequest{
		Scope: &types.Scope{Type: "module", Value: "auth"},
	}
	params, _ := json.Marshal(subscribeReq)
	ctx := context.Background()
	resp, err := handler.HandleSubscribe(ctx, params)
	if err != nil {
		t.Fatalf("HandleSubscribe() failed: %v", err)
	}

	subResp, ok := resp.(*SubscribeResponse)
	if !ok {
		t.Fatalf("expected *SubscribeResponse, got %T", resp)
	}
	subscriptionID := subResp.SubscriptionID

	// Test unsubscribe
	unsubscribeReq := UnsubscribeRequest{
		SubscriptionID: subscriptionID,
	}
	params, _ = json.Marshal(unsubscribeReq)
	resp, err = handler.HandleUnsubscribe(ctx, params)
	if err != nil {
		t.Fatalf("HandleUnsubscribe() failed: %v", err)
	}

	unsubResp, ok := resp.(*UnsubscribeResponse)
	if !ok {
		t.Fatalf("Expected UnsubscribeResponse, got %T", resp)
	}

	if !unsubResp.Removed {
		t.Error("Expected subscription to be removed")
	}

	// Try to unsubscribe again (should return removed=false)
	resp, err = handler.HandleUnsubscribe(ctx, params)
	if err != nil {
		t.Fatalf("Second HandleUnsubscribe() failed: %v", err)
	}

	unsubResp, ok = resp.(*UnsubscribeResponse)
	if !ok {
		t.Fatalf("expected *UnsubscribeResponse, got %T", resp)
	}
	if unsubResp.Removed {
		t.Error("Expected removed=false on second unsubscribe")
	}

	_ = testSessionID // silence unused variable warning
}

func TestList(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum directory: %v", err)
	}

	repoID := "r_TEST12345678"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	handler := NewSubscriptionHandler(st)

	// Setup agent and session
	_ = setupAgentAndSession(t, st, repoID) // sessionID not used in this test
	t.Setenv("THRUM_ROLE", "test-role")
	t.Setenv("THRUM_MODULE", "test-module")

	ctx := context.Background()

	// Create multiple subscriptions
	subscriptions := []SubscribeRequest{
		{Scope: &types.Scope{Type: "module", Value: "auth"}},
		{MentionRole: stringPtr("reviewer")},
		{All: true},
	}

	for _, sub := range subscriptions {
		params, _ := json.Marshal(sub)
		_, err := handler.HandleSubscribe(ctx, params)
		if err != nil {
			t.Fatalf("HandleSubscribe() failed: %v", err)
		}
	}

	// List subscriptions
	listReq := ListSubscriptionsRequest{}
	params, _ := json.Marshal(listReq)
	resp, err := handler.HandleList(ctx, params)
	if err != nil {
		t.Fatalf("HandleList() failed: %v", err)
	}

	listResp, ok := resp.(*ListSubscriptionsResponse)
	if !ok {
		t.Fatalf("Expected ListSubscriptionsResponse, got %T", resp)
	}

	if len(listResp.Subscriptions) != 3 {
		t.Errorf("Expected 3 subscriptions, got %d", len(listResp.Subscriptions))
	}

	// Verify subscription types
	var foundScope, foundMention, foundAll bool
	for _, sub := range listResp.Subscriptions {
		if sub.ScopeType == "module" && sub.ScopeValue == "auth" {
			foundScope = true
		}
		if sub.MentionRole == "reviewer" {
			foundMention = true
		}
		if sub.All {
			foundAll = true
		}
	}

	if !foundScope {
		t.Error("Expected to find scope subscription")
	}
	if !foundMention {
		t.Error("Expected to find mention_role subscription")
	}
	if !foundAll {
		t.Error("Expected to find 'all' subscription")
	}
}

func stringPtr(s string) *string {
	return &s
}

func setupAgentAndSession(t *testing.T, st *state.State, repoID string) string {
	// Register agent - use identity.GenerateAgentID to match what getCurrentSession does
	agentID := identity.GenerateAgentID(repoID, "test-role", "test-module", "")
	agentEvent := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-01-01T00:00:00Z",
		AgentID:   agentID,
		Kind:      "human",
		Role:      "test-role",
		Module:    "test-module",
	}

	st.Lock()
	if err := st.WriteEvent(agentEvent); err != nil {
		t.Fatalf("WriteEvent(agent.register) failed: %v", err)
	}
	st.Unlock()

	// Start session
	sessionID := "ses_test_001"
	sessionEvent := types.AgentSessionStartEvent{
		Type:      "agent.session.start",
		Timestamp: "2026-01-01T00:01:00Z",
		SessionID: sessionID,
		AgentID:   agentID,
	}

	st.Lock()
	if err := st.WriteEvent(sessionEvent); err != nil {
		t.Fatalf("WriteEvent(session.start) failed: %v", err)
	}
	st.Unlock()

	_ = agentID // agentID not used by callers
	return sessionID
}
