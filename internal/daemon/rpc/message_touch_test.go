package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
)

// TestMessageSend_TouchesLastSeen covers thrum-7nuj: message.send from a
// registered agent must advance that agent's last_seen_at timestamp.
func TestMessageSend_TouchesLastSeen(t *testing.T) {
	st, senderID, targetID, handler := setupTwoAgents(t, "sender", "target")
	t.Cleanup(func() { _ = st.Close() })

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
	t.Cleanup(func() { _ = st.Close() })

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
	t.Cleanup(func() { _ = st.Close() })

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
	t.Cleanup(func() { _ = st.Close() })

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

// TestMessageSend_ReplyTouchesLastSeen exercises the reply path
// explicitly (SendRequest.ReplyTo set) to pin that thrum-7nuj covers
// `thrum reply` in addition to bare send. The current handler shares
// the touch call site across both, but the dispatch called the reply
// case out explicitly so we pin it.
func TestMessageSend_ReplyTouchesLastSeen(t *testing.T) {
	st, senderID, targetID, handler := setupTwoAgents(t, "sender", "target")
	t.Cleanup(func() { _ = st.Close() })

	// First message from sender so the reply has a parent to point at.
	parentReq, _ := json.Marshal(SendRequest{
		Content:       "parent",
		To:            "@" + targetID,
		CallerAgentID: senderID,
	})
	parentResp, err := handler.HandleSend(context.Background(), parentReq)
	if err != nil {
		t.Fatalf("HandleSend(parent): %v", err)
	}
	parentID := parentResp.(*SendResponse).MessageID

	// Reset target's last_seen so the reply's touch is the only
	// source of change on this assertion.
	stalePast := "2026-01-01T00:00:00Z"
	if _, err := st.RawDB().Exec(`UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`, stalePast, targetID); err != nil {
		t.Fatalf("seed stale last_seen for target: %v", err)
	}

	// Target replies — this hits HandleSend with ReplyTo populated.
	replyReq, _ := json.Marshal(SendRequest{
		Content:       "reply body",
		ReplyTo:       parentID,
		CallerAgentID: targetID,
	})
	if _, err := handler.HandleSend(context.Background(), replyReq); err != nil {
		t.Fatalf("HandleSend(reply): %v", err)
	}

	got := readLastSeen(t, st, targetID)
	if got == stalePast {
		t.Errorf("target last_seen_at unchanged after reply; want advanced from %q", stalePast)
	}
}

// TestMessageList_BareFormTouchesLastSeen covers the dual-review
// finding: bare message.list (no ExcludeSelf/Unread/UnreadForAgent
// flags) must still advance last_seen. The UI's full-inbox view and
// `thrum inbox` (unflagged) hit this path.
func TestMessageList_BareFormTouchesLastSeen(t *testing.T) {
	st, senderID, _, handler := setupTwoAgents(t, "sender", "target")
	t.Cleanup(func() { _ = st.Close() })

	stalePast := "2026-01-01T00:00:00Z"
	if _, err := st.RawDB().Exec(`UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`, stalePast, senderID); err != nil {
		t.Fatalf("seed stale last_seen: %v", err)
	}

	// No flags — bare list.
	listParams, _ := json.Marshal(ListMessagesRequest{
		CallerAgentID: senderID,
	})
	if _, err := handler.HandleList(context.Background(), listParams); err != nil {
		t.Fatalf("HandleList: %v", err)
	}

	got := readLastSeen(t, st, senderID)
	if got == stalePast {
		t.Errorf("last_seen_at unchanged after bare message.list; want advanced from %q", stalePast)
	}
}

// TestSessionSetIntent_TouchesLastSeen pins the session.setIntent wire.
func TestSessionSetIntent_TouchesLastSeen(t *testing.T) {
	st, senderID, _, _ := setupTwoAgents(t, "sender", "target")
	t.Cleanup(func() { _ = st.Close() })

	var senderSessionID string
	if err := st.RawDB().QueryRow(
		`SELECT session_id FROM sessions WHERE agent_id = ? AND ended_at IS NULL ORDER BY started_at DESC LIMIT 1`,
		senderID,
	).Scan(&senderSessionID); err != nil {
		t.Fatalf("look up sender session: %v", err)
	}

	stalePast := "2026-01-01T00:00:00Z"
	if _, err := st.RawDB().Exec(`UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`, stalePast, senderID); err != nil {
		t.Fatalf("seed stale last_seen: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	params, _ := json.Marshal(SetIntentRequest{
		SessionID: senderSessionID,
		Intent:    "shipping 7nuj",
	})
	if _, err := sessionHandler.HandleSetIntent(context.Background(), params); err != nil {
		t.Fatalf("HandleSetIntent: %v", err)
	}

	got := readLastSeen(t, st, senderID)
	if got == stalePast {
		t.Errorf("last_seen_at unchanged after session.setIntent; want advanced from %q", stalePast)
	}
}

// TestSessionSetTask_TouchesLastSeen pins the session.setTask wire.
func TestSessionSetTask_TouchesLastSeen(t *testing.T) {
	st, senderID, _, _ := setupTwoAgents(t, "sender", "target")
	t.Cleanup(func() { _ = st.Close() })

	var senderSessionID string
	if err := st.RawDB().QueryRow(
		`SELECT session_id FROM sessions WHERE agent_id = ? AND ended_at IS NULL ORDER BY started_at DESC LIMIT 1`,
		senderID,
	).Scan(&senderSessionID); err != nil {
		t.Fatalf("look up sender session: %v", err)
	}

	stalePast := "2026-01-01T00:00:00Z"
	if _, err := st.RawDB().Exec(`UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`, stalePast, senderID); err != nil {
		t.Fatalf("seed stale last_seen: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	params, _ := json.Marshal(SetTaskRequest{
		SessionID:   senderSessionID,
		CurrentTask: "7nuj review fixes",
	})
	if _, err := sessionHandler.HandleSetTask(context.Background(), params); err != nil {
		t.Fatalf("HandleSetTask: %v", err)
	}

	got := readLastSeen(t, st, senderID)
	if got == stalePast {
		t.Errorf("last_seen_at unchanged after session.setTask; want advanced from %q", stalePast)
	}
}

// TestHandleMarkRead_MarkedBefore_FiltersNewMessages verifies that
// marked_before excludes messages with created_at > marked_before from the
// mark operation, even when their IDs are passed in the request. Anchors
// the E2 race fix for `thrum message read --all`: an arrival landing
// between the unread listing and the mark call must not be silently marked.
func TestHandleMarkRead_MarkedBefore_FiltersNewMessages(t *testing.T) {
	st, senderID, targetID, handler := setupTwoAgents(t, "sender", "target")
	t.Cleanup(func() { _ = st.Close() })

	// Send msg_old at t=0; capture watermark; send msg_new at t=1.
	oldSendParams, _ := json.Marshal(SendRequest{
		Content:       "old",
		To:            "@" + targetID,
		CallerAgentID: senderID,
	})
	oldResp, err := handler.HandleSend(context.Background(), oldSendParams)
	if err != nil {
		t.Fatalf("HandleSend(old): %v", err)
	}
	oldMsgID := oldResp.(*SendResponse).MessageID

	watermark := time.Now().UTC().Format(time.RFC3339Nano)
	time.Sleep(10 * time.Millisecond) // gap large enough to be reliable under CI load

	newSendParams, _ := json.Marshal(SendRequest{
		Content:       "new",
		To:            "@" + targetID,
		CallerAgentID: senderID,
	})
	newResp, err := handler.HandleSend(context.Background(), newSendParams)
	if err != nil {
		t.Fatalf("HandleSend(new): %v", err)
	}
	newMsgID := newResp.(*SendResponse).MessageID

	// Mark both with marked_before = watermark. Only msg_old should be marked.
	markParams, _ := json.Marshal(MarkReadRequest{
		MessageIDs:    []string{oldMsgID, newMsgID},
		MarkedBefore:  watermark,
		CallerAgentID: targetID,
	})
	markResp, err := handler.HandleMarkRead(context.Background(), markParams)
	if err != nil {
		t.Fatalf("HandleMarkRead: %v", err)
	}
	if got := markResp.(*MarkReadResponse).MarkedCount; got != 1 {
		t.Fatalf("expected marked_count=1, got %d", got)
	}

	// Verify durable state: old has read_at; new does not.
	for _, tc := range []struct {
		id       string
		wantRead bool
	}{
		{oldMsgID, true},
		{newMsgID, false},
	} {
		var readAt sql.NullString
		if err := st.RawDB().QueryRow(
			`SELECT read_at FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ?`,
			tc.id, targetID).Scan(&readAt); err != nil {
			t.Fatalf("query read_at for %s: %v", tc.id, err)
		}
		if tc.wantRead && !readAt.Valid {
			t.Fatalf("expected %s to have read_at, got NULL", tc.id)
		}
		if !tc.wantRead && readAt.Valid {
			t.Fatalf("expected %s to have NULL read_at, got %s", tc.id, readAt.String)
		}
	}
}

// TestHandleMarkRead_CrossAgentRace_NewMessageStaysUnread exercises the
// actual rc.9 motivation. Two distinct external senders deliver to the
// same target; the watermark is captured between them. When the target
// runs `read --all` semantics (mark both supplied IDs with the captured
// watermark), only the pre-watermark message must be marked. The post-
// watermark arrival stays unread and remains visible on the next inbox
// check.
func TestHandleMarkRead_CrossAgentRace_NewMessageStaysUnread(t *testing.T) {
	st, senderID, targetID, handler := setupTwoAgents(t, "bob", "alice")
	t.Cleanup(func() { _ = st.Close() })

	// Register a second, distinct sender ("carol") inline so the post-
	// watermark arrival is genuinely cross-agent — the bug we're closing
	// involves arrivals from a different agent landing during the read
	// --all round-trip.
	repoID := "r_TEST12345678" // matches setupTwoAgents
	carolID := identity.GenerateAgentID(repoID, "carol", "test-module", "")
	agentHandler := NewAgentHandler(st)
	sessionHandler := NewSessionHandler(st)
	carolRegParams, _ := json.Marshal(RegisterRequest{Role: "carol", Module: "test-module"})
	if _, err := agentHandler.HandleRegister(context.Background(), carolRegParams); err != nil {
		t.Fatalf("register carol: %v", err)
	}
	carolSessionParams, _ := json.Marshal(SessionStartRequest{AgentID: carolID})
	if _, err := sessionHandler.HandleStart(context.Background(), carolSessionParams); err != nil {
		t.Fatalf("start carol session: %v", err)
	}

	// msg_a: from bob → alice at t=0
	aParams, _ := json.Marshal(SendRequest{
		Content:       "from bob",
		To:            "@" + targetID,
		CallerAgentID: senderID,
	})
	aResp, err := handler.HandleSend(context.Background(), aParams)
	if err != nil {
		t.Fatalf("HandleSend(a): %v", err)
	}
	aMsgID := aResp.(*SendResponse).MessageID

	// Capture watermark
	watermark := time.Now().UTC().Format(time.RFC3339Nano)
	time.Sleep(10 * time.Millisecond)

	// msg_b: from carol → alice (the racing arrival)
	bParams, _ := json.Marshal(SendRequest{
		Content:       "from carol",
		To:            "@" + targetID,
		CallerAgentID: carolID,
	})
	bResp, err := handler.HandleSend(context.Background(), bParams)
	if err != nil {
		t.Fatalf("HandleSend(b): %v", err)
	}
	bMsgID := bResp.(*SendResponse).MessageID

	// Mark both IDs with the pre-list watermark — alice's read --all flow.
	markParams, _ := json.Marshal(MarkReadRequest{
		MessageIDs:    []string{aMsgID, bMsgID},
		MarkedBefore:  watermark,
		CallerAgentID: targetID,
	})
	markResp, err := handler.HandleMarkRead(context.Background(), markParams)
	if err != nil {
		t.Fatalf("HandleMarkRead: %v", err)
	}
	if got := markResp.(*MarkReadResponse).MarkedCount; got != 1 {
		t.Fatalf("expected marked_count=1 (only msg_a, pre-watermark), got %d", got)
	}

	// Verify state directly: msg_a has read_at; msg_b does not.
	for _, tc := range []struct {
		id       string
		wantRead bool
		label    string
	}{
		{aMsgID, true, "from bob (pre-watermark)"},
		{bMsgID, false, "from carol (post-watermark)"},
	} {
		var readAt sql.NullString
		if err := st.RawDB().QueryRow(
			`SELECT read_at FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ?`,
			tc.id, targetID).Scan(&readAt); err != nil {
			t.Fatalf("query %s (%s): %v", tc.id, tc.label, err)
		}
		if tc.wantRead && !readAt.Valid {
			t.Fatalf("%s: expected read_at, got NULL", tc.label)
		}
		if !tc.wantRead && readAt.Valid {
			t.Fatalf("%s: expected NULL read_at, got %s", tc.label, readAt.String)
		}
	}
}

// TestHandleMarkRead_NoMarkedBefore_BackwardCompat verifies that omitting
// marked_before preserves current behavior (mark all supplied IDs).
func TestHandleMarkRead_NoMarkedBefore_BackwardCompat(t *testing.T) {
	st, senderID, targetID, handler := setupTwoAgents(t, "sender", "target")
	t.Cleanup(func() { _ = st.Close() })

	sendParams, _ := json.Marshal(SendRequest{
		Content:       "x",
		To:            "@" + targetID,
		CallerAgentID: senderID,
	})
	sendResp, err := handler.HandleSend(context.Background(), sendParams)
	if err != nil {
		t.Fatalf("HandleSend: %v", err)
	}
	msgID := sendResp.(*SendResponse).MessageID

	markParams, _ := json.Marshal(MarkReadRequest{
		MessageIDs:    []string{msgID},
		CallerAgentID: targetID,
		// MarkedBefore intentionally unset.
	})
	markResp, err := handler.HandleMarkRead(context.Background(), markParams)
	if err != nil {
		t.Fatalf("HandleMarkRead: %v", err)
	}
	if got := markResp.(*MarkReadResponse).MarkedCount; got != 1 {
		t.Fatalf("expected marked_count=1, got %d", got)
	}
}

// TestAgentWhoami_TouchesLastSeen: agent.whoami is the bare liveness
// signal — an agent invoking it from its own session must bump the row.
func TestAgentWhoami_TouchesLastSeen(t *testing.T) {
	st, senderID, _, _ := setupTwoAgents(t, "sender", "target")
	t.Cleanup(func() { _ = st.Close() })

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
