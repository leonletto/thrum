package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// setupPurgeTest creates a state instance for purge tests.
func setupPurgeTest(t *testing.T) (*state.State, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create .thrum dir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "test_repo_purge", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	return st, func() { _ = st.Close() }
}

// insertPurgeMessage inserts a message with a specific created_at timestamp and
// returns the message_id.
func insertPurgeMessage(t *testing.T, st *state.State, agentID, createdAt string) string {
	t.Helper()
	msgID := "msg_purge_" + createdAt
	ctx := context.Background()
	_, err := st.DB().ExecContext(ctx, `
		INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES (?, ?, 'sess_test', ?, 'text', 'hello')
	`, msgID, agentID, createdAt)
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}
	return msgID
}

// insertPurgeMessageRead inserts a message_reads row for a message.
func insertPurgeMessageRead(t *testing.T, st *state.State, msgID string) {
	t.Helper()
	ctx := context.Background()
	_, err := st.DB().ExecContext(ctx, `
		INSERT INTO message_reads (message_id, session_id, agent_id, read_at)
		VALUES (?, 'sess_test', 'agent_test', '2024-01-01T00:00:00Z')
	`, msgID)
	if err != nil {
		t.Fatalf("insert message_reads: %v", err)
	}
}

// insertPurgeSession inserts a session with a specific started_at timestamp and
// returns the session_id.
func insertPurgeSession(t *testing.T, st *state.State, agentID, sessionID, startedAt string) {
	t.Helper()
	ctx := context.Background()
	_, err := st.DB().ExecContext(ctx, `
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES (?, ?, ?, ?)
	`, sessionID, agentID, startedAt, startedAt)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

// insertPurgeEvent inserts an event row with a specific timestamp.
func insertPurgeEvent(t *testing.T, st *state.State, eventID, timestamp string) {
	t.Helper()
	ctx := context.Background()
	_, err := st.DB().ExecContext(ctx, `
		INSERT INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json)
		VALUES (?, (SELECT COALESCE(MAX(sequence),0)+1 FROM events), 'test.event', ?, 'daemon_test', '{}')
	`, eventID, timestamp)
	if err != nil {
		t.Fatalf("insert event %s: %v", eventID, err)
	}
}

// insertPurgeAgent inserts an agent record.
func insertPurgeAgent(t *testing.T, st *state.State, agentID string) {
	t.Helper()
	ctx := context.Background()
	_, err := st.DB().ExecContext(ctx, `
		INSERT INTO agents (agent_id, kind, role, module, registered_at, last_seen_at)
		VALUES (?, 'agent', 'tester', 'purge', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')
	`, agentID)
	if err != nil {
		t.Fatalf("insert agent: %v", err)
	}
}

// countRows returns the number of rows in the given table.
func countRows(t *testing.T, st *state.State, table string) int {
	t.Helper()
	var n int
	//nolint:gosec // table name is test-internal, not user input
	if err := st.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestPurgeHandler_DryRun verifies that dry_run=true counts without deleting.
func TestPurgeHandler_DryRun(t *testing.T) {
	st, cleanup := setupPurgeTest(t)
	defer cleanup()

	old := "2024-01-01T00:00:00Z"
	new_ := "2099-12-31T00:00:00Z"
	cutoff := "2025-01-01T00:00:00Z"

	agentID := "agent_dry_run"
	insertPurgeAgent(t, st, agentID)

	// Insert old and new messages
	insertPurgeMessage(t, st, agentID, old)
	insertPurgeMessage(t, st, agentID, new_)

	// Insert old and new sessions
	insertPurgeSession(t, st, agentID, "sess_old", old)
	insertPurgeSession(t, st, agentID, "sess_new", new_)

	// Insert old and new events
	insertPurgeEvent(t, st, "evt_old", old)
	insertPurgeEvent(t, st, "evt_new", new_)

	handler := NewPurgeHandler(st)
	req := PurgeRequest{Before: cutoff, DryRun: true}
	params, _ := json.Marshal(req)
	result, err := handler.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle (dry_run): %v", err)
	}
	resp, ok := result.(*PurgeResponse)
	if !ok {
		t.Fatalf("expected *PurgeResponse, got %T", result)
	}

	// Should report 1 old message, 1 old session, 1 old event
	if resp.MessagesDeleted != 1 {
		t.Errorf("MessagesDeleted = %d, want 1", resp.MessagesDeleted)
	}
	if resp.SessionsDeleted != 1 {
		t.Errorf("SessionsDeleted = %d, want 1", resp.SessionsDeleted)
	}
	if resp.EventsDeleted != 1 {
		t.Errorf("EventsDeleted = %d, want 1", resp.EventsDeleted)
	}
	if !resp.DryRun {
		t.Error("DryRun should be true")
	}
	if resp.Before != cutoff {
		t.Errorf("Before = %q, want %q", resp.Before, cutoff)
	}

	// Verify nothing was actually deleted
	if n := countRows(t, st, "messages"); n != 2 {
		t.Errorf("messages count = %d, want 2 (dry run should not delete)", n)
	}
	if n := countRows(t, st, "sessions"); n != 2 {
		t.Errorf("sessions count = %d, want 2 (dry run should not delete)", n)
	}
	if n := countRows(t, st, "events"); n != 2 {
		t.Errorf("events count = %d, want 2 (dry run should not delete)", n)
	}
}

// TestPurgeHandler_Execute verifies that dry_run=false deletes old data and preserves new.
func TestPurgeHandler_Execute(t *testing.T) {
	st, cleanup := setupPurgeTest(t)
	defer cleanup()

	old := "2024-01-01T00:00:00Z"
	new_ := "2099-12-31T00:00:00Z"
	cutoff := "2025-01-01T00:00:00Z"

	agentID := "agent_execute"
	insertPurgeAgent(t, st, agentID)

	// Insert old message + child read record
	oldMsgID := insertPurgeMessage(t, st, agentID, old)
	insertPurgeMessageRead(t, st, oldMsgID)

	// Insert new message (should survive)
	newMsgID := insertPurgeMessage(t, st, agentID, new_)

	// Insert old and new sessions (with refs)
	insertPurgeSession(t, st, agentID, "sess_old_exec", old)
	insertPurgeSession(t, st, agentID, "sess_new_exec", new_)

	// Insert session_refs for old session
	ctx := context.Background()
	_, err := st.DB().ExecContext(ctx, `
		INSERT INTO session_refs (session_id, ref_type, ref_value, added_at)
		VALUES ('sess_old_exec', 'worktree', '/tmp/wt', ?)
	`, old)
	if err != nil {
		t.Fatalf("insert session_refs: %v", err)
	}

	// Insert old and new events
	insertPurgeEvent(t, st, "evt_old_exec", old)
	insertPurgeEvent(t, st, "evt_new_exec", new_)

	handler := NewPurgeHandler(st)
	req := PurgeRequest{Before: cutoff, DryRun: false}
	params, _ := json.Marshal(req)
	result, err := handler.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle (execute): %v", err)
	}
	resp, ok := result.(*PurgeResponse)
	if !ok {
		t.Fatalf("expected *PurgeResponse, got %T", result)
	}

	if resp.MessagesDeleted != 1 {
		t.Errorf("MessagesDeleted = %d, want 1", resp.MessagesDeleted)
	}
	if resp.SessionsDeleted != 1 {
		t.Errorf("SessionsDeleted = %d, want 1", resp.SessionsDeleted)
	}
	if resp.EventsDeleted != 1 {
		t.Errorf("EventsDeleted = %d, want 1", resp.EventsDeleted)
	}
	if resp.DryRun {
		t.Error("DryRun should be false")
	}

	// Old message is gone
	var msgCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE message_id = ?`, oldMsgID,
	).Scan(&msgCount); err != nil {
		t.Fatalf("query old message: %v", err)
	}
	if msgCount != 0 {
		t.Errorf("old message still present, count = %d", msgCount)
	}

	// New message survives
	var newMsgCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE message_id = ?`, newMsgID,
	).Scan(&newMsgCount); err != nil {
		t.Fatalf("query new message: %v", err)
	}
	if newMsgCount != 1 {
		t.Errorf("new message gone, count = %d, want 1", newMsgCount)
	}

	// message_reads for old message is gone
	var readCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM message_reads WHERE message_id = ?`, oldMsgID,
	).Scan(&readCount); err != nil {
		t.Fatalf("query message_reads: %v", err)
	}
	if readCount != 0 {
		t.Errorf("message_reads for old message still present, count = %d", readCount)
	}

	// Old session is gone
	var sessCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE session_id = 'sess_old_exec'`,
	).Scan(&sessCount); err != nil {
		t.Fatalf("query old session: %v", err)
	}
	if sessCount != 0 {
		t.Errorf("old session still present, count = %d", sessCount)
	}

	// New session survives
	var newSessCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE session_id = 'sess_new_exec'`,
	).Scan(&newSessCount); err != nil {
		t.Fatalf("query new session: %v", err)
	}
	if newSessCount != 1 {
		t.Errorf("new session gone, count = %d, want 1", newSessCount)
	}

	// session_refs for old session is gone
	var refCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_refs WHERE session_id = 'sess_old_exec'`,
	).Scan(&refCount); err != nil {
		t.Fatalf("query session_refs: %v", err)
	}
	if refCount != 0 {
		t.Errorf("session_refs for old session still present, count = %d", refCount)
	}

	// Agent is NOT deleted
	var agentCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agents WHERE agent_id = ?`, agentID,
	).Scan(&agentCount); err != nil {
		t.Fatalf("query agent: %v", err)
	}
	if agentCount != 1 {
		t.Errorf("agent was deleted, count = %d, want 1", agentCount)
	}

	// Old event is gone
	var evtCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE event_id = 'evt_old_exec'`,
	).Scan(&evtCount); err != nil {
		t.Fatalf("query old event: %v", err)
	}
	if evtCount != 0 {
		t.Errorf("old event still present, count = %d", evtCount)
	}

	// New event survives
	var newEvtCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE event_id = 'evt_new_exec'`,
	).Scan(&newEvtCount); err != nil {
		t.Fatalf("query new event: %v", err)
	}
	if newEvtCount != 1 {
		t.Errorf("new event gone, count = %d, want 1", newEvtCount)
	}
}

// TestPurgeHandler_InvalidRequest verifies that bad input returns errors.
func TestPurgeHandler_InvalidRequest(t *testing.T) {
	st, cleanup := setupPurgeTest(t)
	defer cleanup()

	handler := NewPurgeHandler(st)

	tests := []struct {
		name   string
		params string
	}{
		{
			name:   "empty before",
			params: `{"before":"","dry_run":true}`,
		},
		{
			name:   "invalid timestamp",
			params: `{"before":"not-a-date","dry_run":true}`,
		},
		{
			name:   "missing before",
			params: `{"dry_run":true}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handler.Handle(context.Background(), json.RawMessage(tc.params))
			if err == nil {
				t.Errorf("expected error for %q, got nil", tc.name)
			}
		})
	}
}

// TestPurgeHandler_SyncFiles verifies that sync JSONL files are filtered when dry_run=false.
func TestPurgeHandler_SyncFiles(t *testing.T) {
	st, cleanup := setupPurgeTest(t)
	defer cleanup()

	syncDir := st.SyncDir()
	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(messagesDir, 0o750); err != nil {
		t.Fatalf("create messages dir: %v", err)
	}

	old := "2024-01-01T00:00:00Z"
	new_ := "2099-12-31T00:00:00Z"
	cutoff := "2025-01-01T00:00:00Z"

	// Write events.jsonl with one old + one new event
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	eventsContent := `{"type":"agent.register","timestamp":"` + old + `"}` + "\n" +
		`{"type":"agent.register","timestamp":"` + new_ + `"}` + "\n"
	if err := os.WriteFile(eventsPath, []byte(eventsContent), 0o600); err != nil {
		t.Fatalf("write events.jsonl: %v", err)
	}

	// Write messages/agent_test.jsonl with one old + one new message
	msgPath := filepath.Join(messagesDir, "agent_test.jsonl")
	msgContent := `{"message_id":"m1","created_at":"` + old + `"}` + "\n" +
		`{"message_id":"m2","created_at":"` + new_ + `"}` + "\n"
	if err := os.WriteFile(msgPath, []byte(msgContent), 0o600); err != nil {
		t.Fatalf("write messages/agent_test.jsonl: %v", err)
	}

	handler := NewPurgeHandler(st)
	req := PurgeRequest{Before: cutoff, DryRun: false}
	params, _ := json.Marshal(req)
	result, err := handler.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle (sync files): %v", err)
	}
	resp, ok := result.(*PurgeResponse)
	if !ok {
		t.Fatalf("expected *PurgeResponse, got %T", result)
	}

	// Should report filtered counts
	if resp.SyncEventsFiltered != 1 {
		t.Errorf("SyncEventsFiltered = %d, want 1", resp.SyncEventsFiltered)
	}
	if resp.SyncMessageFiles != 1 {
		t.Errorf("SyncMessageFiles = %d, want 1 (files with at least one removal)", resp.SyncMessageFiles)
	}

	// Verify events.jsonl has 1 line left
	eventsData, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("read events.jsonl after purge: %v", err)
	}
	lines := nonEmptyLines(string(eventsData))
	if len(lines) != 1 {
		t.Errorf("events.jsonl has %d lines, want 1", len(lines))
	}

	// Verify messages/agent_test.jsonl has 1 line left
	msgData, err := os.ReadFile(msgPath)
	if err != nil {
		t.Fatalf("read messages/agent_test.jsonl after purge: %v", err)
	}
	msgLines := nonEmptyLines(string(msgData))
	if len(msgLines) != 1 {
		t.Errorf("messages/agent_test.jsonl has %d lines, want 1", len(msgLines))
	}
}

// TestPurgeHandler_DryRun_SyncFiles verifies that dry_run counts sync files without filtering them.
func TestPurgeHandler_DryRun_SyncFiles(t *testing.T) {
	st, cleanup := setupPurgeTest(t)
	defer cleanup()

	syncDir := st.SyncDir()
	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(messagesDir, 0o750); err != nil {
		t.Fatalf("create messages dir: %v", err)
	}

	old := "2024-01-01T00:00:00Z"
	new_ := "2099-12-31T00:00:00Z"
	cutoff := "2025-01-01T00:00:00Z"

	// Write events.jsonl with one old + one new event
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	eventsContent := `{"type":"agent.register","timestamp":"` + old + `"}` + "\n" +
		`{"type":"agent.register","timestamp":"` + new_ + `"}` + "\n"
	if err := os.WriteFile(eventsPath, []byte(eventsContent), 0o600); err != nil {
		t.Fatalf("write events.jsonl: %v", err)
	}

	// Write a message file with one old line
	msgPath := filepath.Join(messagesDir, "dry_agent.jsonl")
	msgContent := `{"message_id":"m_dry","created_at":"` + old + `"}` + "\n"
	if err := os.WriteFile(msgPath, []byte(msgContent), 0o600); err != nil {
		t.Fatalf("write messages/dry_agent.jsonl: %v", err)
	}

	handler := NewPurgeHandler(st)
	req := PurgeRequest{Before: cutoff, DryRun: true}
	params, _ := json.Marshal(req)
	result, err := handler.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle (dry_run sync files): %v", err)
	}
	resp, ok := result.(*PurgeResponse)
	if !ok {
		t.Fatalf("expected *PurgeResponse, got %T", result)
	}

	// Dry run should still count sync files / events
	if resp.SyncEventsFiltered != 1 {
		t.Errorf("SyncEventsFiltered = %d, want 1", resp.SyncEventsFiltered)
	}

	// But the file contents should be untouched
	eventsData, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("read events.jsonl: %v", err)
	}
	lines := nonEmptyLines(string(eventsData))
	if len(lines) != 2 {
		t.Errorf("events.jsonl modified during dry run, have %d lines, want 2", len(lines))
	}

	_ = time.Now() // just to use the time import
}

// TestPurgeHandler_Integration_DryRunThenExecute verifies the full purge flow:
// insert data across all table types, dry-run (nothing deleted), execute (all
// old data deleted), then confirm survivors are intact.
func TestPurgeHandler_Integration_DryRunThenExecute(t *testing.T) {
	st, cleanup := setupPurgeTest(t)
	defer cleanup()

	old := "2024-06-01T00:00:00Z"
	new_ := "2099-06-01T00:00:00Z"
	cutoff := "2025-01-01T00:00:00Z"
	ctx := context.Background()

	// --- Agents (must survive purge) ---
	agentOld := "agent_integ_old"
	agentNew := "agent_integ_new"
	insertPurgeAgent(t, st, agentOld)
	insertPurgeAgent(t, st, agentNew)

	// --- Old message + all child tables ---
	oldMsgID := insertPurgeMessage(t, st, agentOld, old)

	// message_reads
	insertPurgeMessageRead(t, st, oldMsgID)

	// message_deliveries
	_, err := st.DB().ExecContext(ctx, `
		INSERT INTO message_deliveries (message_id, recipient_agent_id, delivered_at)
		VALUES (?, 'agent_recv', ?)
	`, oldMsgID, old)
	if err != nil {
		t.Fatalf("insert message_deliveries: %v", err)
	}

	// message_edits
	_, err = st.DB().ExecContext(ctx, `
		INSERT INTO message_edits (message_id, edited_at, edited_by, old_content, new_content)
		VALUES (?, ?, 'agent_editor', 'old body', 'new body')
	`, oldMsgID, old)
	if err != nil {
		t.Fatalf("insert message_edits: %v", err)
	}

	// message_refs
	_, err = st.DB().ExecContext(ctx, `
		INSERT INTO message_refs (message_id, ref_type, ref_value)
		VALUES (?, 'mention', 'some_agent')
	`, oldMsgID)
	if err != nil {
		t.Fatalf("insert message_refs: %v", err)
	}

	// message_scopes
	_, err = st.DB().ExecContext(ctx, `
		INSERT INTO message_scopes (message_id, scope_type, scope_value)
		VALUES (?, 'group', 'all')
	`, oldMsgID)
	if err != nil {
		t.Fatalf("insert message_scopes: %v", err)
	}

	// --- New message (should survive) ---
	newMsgID := insertPurgeMessage(t, st, agentNew, new_)

	// --- Old session + child tables ---
	insertPurgeSession(t, st, agentOld, "sess_integ_old", old)

	_, err = st.DB().ExecContext(ctx, `
		INSERT INTO session_refs (session_id, ref_type, ref_value, added_at)
		VALUES ('sess_integ_old', 'worktree', '/tmp/old_wt', ?)
	`, old)
	if err != nil {
		t.Fatalf("insert session_refs: %v", err)
	}

	_, err = st.DB().ExecContext(ctx, `
		INSERT INTO session_scopes (session_id, scope_type, scope_value, added_at)
		VALUES ('sess_integ_old', 'project', 'thrum', ?)
	`, old)
	if err != nil {
		t.Fatalf("insert session_scopes: %v", err)
	}

	// --- New session (should survive) ---
	insertPurgeSession(t, st, agentNew, "sess_integ_new", new_)

	// --- Old and new events ---
	insertPurgeEvent(t, st, "evt_integ_old", old)
	insertPurgeEvent(t, st, "evt_integ_new", new_)

	handler := NewPurgeHandler(st)

	// ── Phase 1: dry run ──────────────────────────────────────────────────────
	dryReq := PurgeRequest{Before: cutoff, DryRun: true}
	dryParams, _ := json.Marshal(dryReq)
	dryResult, err := handler.Handle(ctx, dryParams)
	if err != nil {
		t.Fatalf("dry-run Handle: %v", err)
	}
	dryResp, ok := dryResult.(*PurgeResponse)
	if !ok {
		t.Fatalf("expected *PurgeResponse, got %T", dryResult)
	}

	if !dryResp.DryRun {
		t.Error("dry-run: DryRun should be true")
	}
	if dryResp.MessagesDeleted != 1 {
		t.Errorf("dry-run: MessagesDeleted = %d, want 1", dryResp.MessagesDeleted)
	}
	if dryResp.SessionsDeleted != 1 {
		t.Errorf("dry-run: SessionsDeleted = %d, want 1", dryResp.SessionsDeleted)
	}
	if dryResp.EventsDeleted != 1 {
		t.Errorf("dry-run: EventsDeleted = %d, want 1", dryResp.EventsDeleted)
	}

	// Verify nothing was actually deleted after dry run
	if n := countRows(t, st, "messages"); n != 2 {
		t.Errorf("dry-run: messages count = %d, want 2", n)
	}
	if n := countRows(t, st, "sessions"); n != 2 {
		t.Errorf("dry-run: sessions count = %d, want 2", n)
	}
	if n := countRows(t, st, "events"); n != 2 {
		t.Errorf("dry-run: events count = %d, want 2", n)
	}
	if n := countRows(t, st, "message_reads"); n != 1 {
		t.Errorf("dry-run: message_reads count = %d, want 1", n)
	}
	if n := countRows(t, st, "message_deliveries"); n != 1 {
		t.Errorf("dry-run: message_deliveries count = %d, want 1", n)
	}
	if n := countRows(t, st, "message_edits"); n != 1 {
		t.Errorf("dry-run: message_edits count = %d, want 1", n)
	}
	if n := countRows(t, st, "message_refs"); n != 1 {
		t.Errorf("dry-run: message_refs count = %d, want 1", n)
	}
	if n := countRows(t, st, "message_scopes"); n != 1 {
		t.Errorf("dry-run: message_scopes count = %d, want 1", n)
	}
	if n := countRows(t, st, "session_refs"); n != 1 {
		t.Errorf("dry-run: session_refs count = %d, want 1", n)
	}
	if n := countRows(t, st, "session_scopes"); n != 1 {
		t.Errorf("dry-run: session_scopes count = %d, want 1", n)
	}

	// ── Phase 2: execute ──────────────────────────────────────────────────────
	execReq := PurgeRequest{Before: cutoff, DryRun: false}
	execParams, _ := json.Marshal(execReq)
	execResult, err := handler.Handle(ctx, execParams)
	if err != nil {
		t.Fatalf("execute Handle: %v", err)
	}
	execResp, ok := execResult.(*PurgeResponse)
	if !ok {
		t.Fatalf("expected *PurgeResponse, got %T", execResult)
	}

	if execResp.DryRun {
		t.Error("execute: DryRun should be false")
	}
	if execResp.MessagesDeleted != 1 {
		t.Errorf("execute: MessagesDeleted = %d, want 1", execResp.MessagesDeleted)
	}
	if execResp.SessionsDeleted != 1 {
		t.Errorf("execute: SessionsDeleted = %d, want 1", execResp.SessionsDeleted)
	}
	if execResp.EventsDeleted != 1 {
		t.Errorf("execute: EventsDeleted = %d, want 1", execResp.EventsDeleted)
	}

	// ── Phase 3: verify deletions ─────────────────────────────────────────────

	// Old message gone, new message survives
	var cnt int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE message_id = ?`, oldMsgID).Scan(&cnt); err != nil {
		t.Fatalf("query old message: %v", err)
	}
	if cnt != 0 {
		t.Errorf("old message still present")
	}
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE message_id = ?`, newMsgID).Scan(&cnt); err != nil {
		t.Fatalf("query new message: %v", err)
	}
	if cnt != 1 {
		t.Errorf("new message gone, want 1 row")
	}

	// All message child records for old message gone
	for _, table := range []string{"message_reads", "message_deliveries", "message_edits", "message_refs", "message_scopes"} {
		//nolint:gosec // table name is a hardcoded constant, not user input
		q := `SELECT COUNT(*) FROM ` + table + ` WHERE message_id = ?`
		if err := st.DB().QueryRowContext(ctx, q, oldMsgID).Scan(&cnt); err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if cnt != 0 {
			t.Errorf("%s still has %d row(s) for deleted message", table, cnt)
		}
	}

	// Old session gone, new session survives
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE session_id = 'sess_integ_old'`).Scan(&cnt); err != nil {
		t.Fatalf("query old session: %v", err)
	}
	if cnt != 0 {
		t.Errorf("old session still present")
	}
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE session_id = 'sess_integ_new'`).Scan(&cnt); err != nil {
		t.Fatalf("query new session: %v", err)
	}
	if cnt != 1 {
		t.Errorf("new session gone, want 1 row")
	}

	// Session child records for old session gone
	for _, table := range []string{"session_refs", "session_scopes"} {
		//nolint:gosec // table name is a hardcoded constant, not user input
		q := `SELECT COUNT(*) FROM ` + table + ` WHERE session_id = 'sess_integ_old'`
		if err := st.DB().QueryRowContext(ctx, q).Scan(&cnt); err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if cnt != 0 {
			t.Errorf("%s still has %d row(s) for deleted session", table, cnt)
		}
	}

	// Old event gone, new event survives
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE event_id = 'evt_integ_old'`).Scan(&cnt); err != nil {
		t.Fatalf("query old event: %v", err)
	}
	if cnt != 0 {
		t.Errorf("old event still present")
	}
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE event_id = 'evt_integ_new'`).Scan(&cnt); err != nil {
		t.Fatalf("query new event: %v", err)
	}
	if cnt != 1 {
		t.Errorf("new event gone, want 1 row")
	}

	// ── Phase 4: agents must NOT be deleted ───────────────────────────────────
	for _, agentID := range []string{agentOld, agentNew} {
		if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE agent_id = ?`, agentID).Scan(&cnt); err != nil {
			t.Fatalf("query agent %s: %v", agentID, err)
		}
		if cnt != 1 {
			t.Errorf("agent %s was deleted (count=%d), agents must survive purge", agentID, cnt)
		}
	}
}

// nonEmptyLines splits text by newline, filtering blank lines.
func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range splitLines(s) {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// splitLines splits a string into lines.
func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
