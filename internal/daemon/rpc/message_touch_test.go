package rpc

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// TestMessageSend_TouchesLastSeen covers thrum-7nuj: message.send from a
// registered agent must advance that agent's last_seen_at timestamp.
func TestMessageSend_TouchesLastSeen(t *testing.T) {
	st, senderID, targetID, handler := setupTwoAgents(t, "sender", "target")
	defer func() { _ = st.Close() }()

	// Freeze last_seen_at to a known past value so the assertion is
	// unambiguous (bypass session.heartbeat's own write).
	stalePast := "2026-01-01T00:00:00Z"
	if _, err := st.RawDB().Exec(`UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`, stalePast, senderID); err != nil {
		t.Fatalf("seed stale last_seen: %v", err)
	}

	req := SendRequest{
		Content:       "ping",
		To:            "@" + targetID,
		CallerAgentID: senderID,
	}
	params, _ := json.Marshal(req)
	if _, err := handler.HandleSend(context.Background(), params); err != nil {
		t.Fatalf("HandleSend: %v", err)
	}

	got := readLastSeen(t, st, senderID)
	if got == stalePast {
		t.Errorf("last_seen_at unchanged after message.send; want advanced from %q", stalePast)
	}
	// Verify it's a recent timestamp (within the last minute).
	parsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatalf("parse last_seen_at %q: %v", got, err)
	}
	if time.Since(parsed) > time.Minute {
		t.Errorf("last_seen_at %q is older than 1 minute — touch not recent", got)
	}
}

// TestMessageMarkRead_TouchesLastSeen: message.markRead (thrum inbox read)
// is a liveness signal for the reader.
func TestMessageMarkRead_TouchesLastSeen(t *testing.T) {
	st, senderID, targetID, handler := setupTwoAgents(t, "sender", "target")
	defer func() { _ = st.Close() }()

	// Send a message so there's something to mark read.
	sendParams, _ := json.Marshal(SendRequest{
		Content:       "to be read",
		To:            "@" + targetID,
		CallerAgentID: senderID,
	})
	sendResp, err := handler.HandleSend(context.Background(), sendParams)
	if err != nil {
		t.Fatalf("HandleSend: %v", err)
	}
	msgID := sendResp.(*SendResponse).MessageID

	// Freeze target's last_seen_at stale.
	stalePast := "2026-01-01T00:00:00Z"
	if _, err := st.RawDB().Exec(`UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`, stalePast, targetID); err != nil {
		t.Fatalf("seed stale last_seen: %v", err)
	}

	markParams, _ := json.Marshal(MarkReadRequest{
		MessageIDs:    []string{msgID},
		CallerAgentID: targetID,
	})
	if _, err := handler.HandleMarkRead(context.Background(), markParams); err != nil {
		t.Fatalf("HandleMarkRead: %v", err)
	}

	got := readLastSeen(t, st, targetID)
	if got == stalePast {
		t.Errorf("target last_seen_at unchanged after message.markRead; want advanced from %q", stalePast)
	}
}

// TestMessageList_TouchesLastSeen: inbox checks (thrum inbox --unread)
// map to message.list with Unread=true, which the coordinator hits
// constantly. This must advance last_seen for the listing agent.
func TestMessageList_TouchesLastSeen(t *testing.T) {
	st, senderID, _, handler := setupTwoAgents(t, "sender", "target")
	defer func() { _ = st.Close() }()

	stalePast := "2026-01-01T00:00:00Z"
	if _, err := st.RawDB().Exec(`UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`, stalePast, senderID); err != nil {
		t.Fatalf("seed stale last_seen: %v", err)
	}

	listParams, _ := json.Marshal(ListMessagesRequest{
		Unread:        true,
		CallerAgentID: senderID,
	})
	if _, err := handler.HandleList(context.Background(), listParams); err != nil {
		t.Fatalf("HandleList: %v", err)
	}

	got := readLastSeen(t, st, senderID)
	if got == stalePast {
		t.Errorf("sender last_seen_at unchanged after message.list; want advanced from %q", stalePast)
	}
}

// TestMessageSend_DebouncesRapidCalls: two message.send calls from the
// same caller within the debounce window (30s) must collapse into a
// single last_seen_at update. Prevents SQLite write storms on hot loops.
func TestMessageSend_DebouncesRapidCalls(t *testing.T) {
	st, senderID, targetID, handler := setupTwoAgents(t, "sender", "target")
	defer func() { _ = st.Close() }()

	stalePast := "2026-01-01T00:00:00Z"
	if _, err := st.RawDB().Exec(`UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`, stalePast, senderID); err != nil {
		t.Fatalf("seed stale last_seen: %v", err)
	}

	sendOnce := func(content string) {
		t.Helper()
		params, _ := json.Marshal(SendRequest{Content: content, To: "@" + targetID, CallerAgentID: senderID})
		if _, err := handler.HandleSend(context.Background(), params); err != nil {
			t.Fatalf("HandleSend(%s): %v", content, err)
		}
	}

	sendOnce("first")
	firstSeen := readLastSeen(t, st, senderID)
	if firstSeen == stalePast {
		t.Fatalf("first send did not touch last_seen_at")
	}

	// Immediately send again — inside the 30s debounce window, same
	// last_seen_at is expected.
	sendOnce("second")
	secondSeen := readLastSeen(t, st, senderID)
	if secondSeen != firstSeen {
		t.Errorf("second send within debounce window changed last_seen_at: %q → %q", firstSeen, secondSeen)
	}
}

// TestAgentWhoami_TouchesLastSeen: agent.whoami is the bare liveness
// signal — an agent invoking it from its own session must bump the row.
func TestAgentWhoami_TouchesLastSeen(t *testing.T) {
	st, senderID, _, _ := setupTwoAgents(t, "sender", "target")
	defer func() { _ = st.Close() }()

	stalePast := "2026-01-01T00:00:00Z"
	if _, err := st.RawDB().Exec(`UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`, stalePast, senderID); err != nil {
		t.Fatalf("seed stale last_seen: %v", err)
	}

	agentHandler := NewAgentHandler(st)
	params, _ := json.Marshal(struct {
		CallerAgentID string `json:"caller_agent_id"`
	}{CallerAgentID: senderID})
	if _, err := agentHandler.HandleWhoami(context.Background(), params); err != nil {
		t.Fatalf("HandleWhoami: %v", err)
	}

	got := readLastSeen(t, st, senderID)
	if got == stalePast {
		t.Errorf("last_seen_at unchanged after agent.whoami; want advanced from %q", stalePast)
	}
}

// readLastSeen is a shared helper for touch-behavior tests.
func readLastSeen(t *testing.T, st *state.State, agentID string) string {
	t.Helper()
	var lastSeen string
	if err := st.RawDB().QueryRow(`SELECT last_seen_at FROM agents WHERE agent_id = ?`, agentID).Scan(&lastSeen); err != nil {
		t.Fatalf("read last_seen_at: %v", err)
	}
	return lastSeen
}
