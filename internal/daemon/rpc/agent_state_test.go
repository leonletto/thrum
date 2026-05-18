package rpc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/escalation"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// fakeRouter records every Route call for assertion. Goroutine-
// safe (the HandleMarkStateCorruption path doesn't fan out, but
// the race detector likes the lock anyway).
type fakeRouter struct {
	mu        sync.Mutex
	calls     []routerCall
	returnErr error
}

type routerCall struct {
	Alert   escalation.Alert
	Subject string
	Body    string
}

func (f *fakeRouter) Route(_ context.Context, alert escalation.Alert, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, routerCall{Alert: alert, Subject: subject, Body: body})
	return f.returnErr
}

func (f *fakeRouter) firstCall(t *testing.T) routerCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("expected at least one Route call; got none")
	}
	return f.calls[0]
}

// newCorruptionTestHandler builds a state-backed handler + fake
// router + registers one test agent in the agents table.
func newCorruptionTestHandler(t *testing.T) (*AgentStateCorruptionHandler, *fakeRouter, string) {
	t.Helper()
	thrumDir := filepath.Join(t.TempDir(), ".thrum")

	st, err := state.NewState(thrumDir, thrumDir, "test_repo_corruption", "")
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Register a test agent via the existing AgentHandler so the
	// agents table has a row to update.
	registerReq := RegisterRequest{
		Role:        "implementer",
		Module:      "test",
		Mode:        "persistent",
		Identity:    "long_lived",
		AutoRespawn: false,
	}
	registerJSON, _ := json.Marshal(registerReq)
	registerResp, err := NewAgentHandler(st).HandleRegister(context.Background(), registerJSON)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	regResp, ok := registerResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp)
	}

	router := &fakeRouter{}
	handler := NewAgentStateCorruptionHandler(st, router)
	return handler, router, regResp.AgentID
}

func TestHandleMarkStateCorruption_EmptyAgentName_Fails(t *testing.T) {
	h, _, _ := newCorruptionTestHandler(t)

	params, _ := json.Marshal(MarkStateCorruptionRequest{AgentName: ""})
	_, err := h.HandleMarkStateCorruption(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for empty agent_name")
	}
	if !strings.Contains(err.Error(), "agent_name is required") {
		t.Errorf("error should mention required: %v", err)
	}
}

func TestHandleMarkStateCorruption_UnknownAgent_Fails(t *testing.T) {
	h, _, _ := newCorruptionTestHandler(t)

	params, _ := json.Marshal(MarkStateCorruptionRequest{AgentName: "ghost"})
	_, err := h.HandleMarkStateCorruption(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("error should mention 'not registered': %v", err)
	}
}

// TestHandleMarkStateCorruption_FullFlow verifies spec §6.5 +
// the bd Task 26 acceptance criteria: DB flag set, lifecycle
// event appended, RouteEscalation called with correct Source.
func TestHandleMarkStateCorruption_FullFlow(t *testing.T) {
	h, router, agentID := newCorruptionTestHandler(t)

	brokenPath := "/some/.thrum/agents/alpha/state.md.broken"
	params, _ := json.Marshal(MarkStateCorruptionRequest{
		AgentName:  agentID,
		BrokenPath: brokenPath,
	})
	resp, err := h.HandleMarkStateCorruption(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := resp.(*MarkStateCorruptionResponse)
	if !ok {
		t.Fatalf("expected *MarkStateCorruptionResponse, got %T", resp)
	}
	if r.AgentName != agentID {
		t.Errorf("AgentName: got %q, want %q", r.AgentName, agentID)
	}
	if r.FailedAt == "" {
		t.Error("FailedAt should be populated")
	}
	if !r.Escalated {
		t.Error("Escalated should be true (router non-nil)")
	}

	// DB invariant: agents.state_md_parse_failed_at is set.
	var failedAt int64
	if err := h.state.DB().QueryRowContext(context.Background(),
		`SELECT state_md_parse_failed_at FROM agents WHERE agent_id = ?`, agentID,
	).Scan(&failedAt); err != nil {
		t.Fatalf("read state_md_parse_failed_at: %v", err)
	}
	if failedAt == 0 {
		t.Error("state_md_parse_failed_at should be non-zero after corruption mark")
	}

	// DB invariant: agent_lifecycle_events has the row.
	var eventCount int
	if err := h.state.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM agent_lifecycle_events WHERE agent_name = ? AND event_kind = ?`,
		agentID, "state_md_parse_failed",
	).Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("expected 1 state_md_parse_failed event, got %d", eventCount)
	}

	// DB invariant: event details include broken_path.
	var details string
	if err := h.state.DB().QueryRowContext(context.Background(),
		`SELECT details FROM agent_lifecycle_events WHERE agent_name = ? AND event_kind = ? LIMIT 1`,
		agentID, "state_md_parse_failed",
	).Scan(&details); err != nil {
		t.Fatalf("read details: %v", err)
	}
	if !strings.Contains(details, brokenPath) {
		t.Errorf("event details should contain broken_path %q; got %q", brokenPath, details)
	}

	// Escalation invariant: router was called with the canonical
	// Source string + the agent's name + a useful subject/body.
	call := router.firstCall(t)
	if call.Alert.Source != "b-b1.state_md_parse_failed" {
		t.Errorf("Alert.Source: got %q, want 'b-b1.state_md_parse_failed'", call.Alert.Source)
	}
	if call.Alert.AgentName != agentID {
		t.Errorf("Alert.AgentName: got %q, want %q", call.Alert.AgentName, agentID)
	}
	if !strings.Contains(call.Subject, "unparseable") {
		t.Errorf("Subject should mention 'unparseable': %q", call.Subject)
	}
	if !strings.Contains(call.Subject, agentID) {
		t.Errorf("Subject should mention agent name %q: %q", agentID, call.Subject)
	}
	if !strings.Contains(call.Body, brokenPath) {
		t.Errorf("Body should include broken_path %q: %q", brokenPath, call.Body)
	}
	if !strings.Contains(call.Body, "ack-state-corruption") {
		t.Errorf("Body should reference the operator-clear path 'ack-state-corruption': %q", call.Body)
	}
}

// TestHandleMarkStateCorruption_NilRouter_StillSetsFlag verifies
// that when the escalation router is nil (degraded daemon boot,
// test fixture without router), the DB writes still complete
// and the response shows Escalated=false rather than failing.
// Pattern matches agentdispatch's routeEscalation nil-guard.
func TestHandleMarkStateCorruption_NilRouter_StillSetsFlag(t *testing.T) {
	h, _, agentID := newCorruptionTestHandler(t)
	h.router = nil // simulate degraded boot

	params, _ := json.Marshal(MarkStateCorruptionRequest{
		AgentName:  agentID,
		BrokenPath: "/path/to/broken",
	})
	resp, err := h.HandleMarkStateCorruption(context.Background(), params)
	if err != nil {
		t.Fatalf("nil router should not cause error: %v", err)
	}
	r := resp.(*MarkStateCorruptionResponse)
	if r.Escalated {
		t.Error("Escalated should be false when router is nil")
	}

	// DB flag still set.
	var failedAt int64
	_ = h.state.DB().QueryRowContext(context.Background(),
		`SELECT state_md_parse_failed_at FROM agents WHERE agent_id = ?`, agentID,
	).Scan(&failedAt)
	if failedAt == 0 {
		t.Error("nil router shouldn't bypass the DB flag-set")
	}
}

func TestHandleMarkStateCorruption_RouterError_Propagates(t *testing.T) {
	h, router, agentID := newCorruptionTestHandler(t)
	router.returnErr = context.DeadlineExceeded

	params, _ := json.Marshal(MarkStateCorruptionRequest{
		AgentName:  agentID,
		BrokenPath: "/path",
	})
	_, err := h.HandleMarkStateCorruption(context.Background(), params)
	if err == nil {
		t.Fatal("expected error when router returns error")
	}
	if !strings.Contains(err.Error(), "escalation route") {
		t.Errorf("error should mention 'escalation route': %v", err)
	}

	// DB writes already happened before the route call — verify.
	var failedAt int64
	_ = h.state.DB().QueryRowContext(context.Background(),
		`SELECT state_md_parse_failed_at FROM agents WHERE agent_id = ?`, agentID,
	).Scan(&failedAt)
	if failedAt == 0 {
		t.Error("DB flag should be set even when route fails (DB writes are pre-route)")
	}
}
