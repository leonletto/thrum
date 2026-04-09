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

// setupTmuxHandlerTest creates a TmuxHandler backed by an in-memory state instance.
// Returns the handler and a cleanup function. Mirrors setupPurgeTest in purge_test.go.
func setupTmuxHandlerTest(t *testing.T) (*TmuxHandler, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create .thrum dir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "test_repo_queue", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	handler := NewTmuxHandler(thrumDir, st)
	cleanup := func() { _ = st.Close() }
	return handler, cleanup
}

func TestPersistCommand(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	cmd := &QueuedCommand{
		ID:               "cmd_persist",
		Text:             "echo test",
		RequesterAgent:   "test_coord",
		Timeout:          120 * time.Second,
		SilenceMs:        2500,
		NotifyOnComplete: false,
		State:            StateQueued,
		SubmittedAt:      time.Now().UTC(),
	}

	if err := persistCommand(ctx, h.state.DB(), "test-session", cmd, 0); err != nil {
		t.Fatalf("persistCommand: %v", err)
	}

	loaded, err := loadCommand(ctx, h.state.DB(), "cmd_persist")
	if err != nil {
		t.Fatalf("loadCommand: %v", err)
	}
	if loaded.ID != cmd.ID {
		t.Errorf("ID=%s, want %s", loaded.ID, cmd.ID)
	}
	if loaded.State != StateQueued {
		t.Errorf("State=%s, want %s", loaded.State, StateQueued)
	}
	if loaded.SilenceMs != 2500 {
		t.Errorf("SilenceMs=%d, want 2500", loaded.SilenceMs)
	}
	if loaded.NotifyOnComplete != false {
		t.Errorf("NotifyOnComplete=%v, want false", loaded.NotifyOnComplete)
	}
}

func TestHandleQueueEnqueuesCommand(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	req := `{"session":"test-session","text":"echo hi","timeout_ms":60000,"requester":"test_coord"}`
	resp, err := h.HandleQueue(context.Background(), json.RawMessage(req))
	if err != nil {
		t.Fatalf("HandleQueue: %v", err)
	}

	result, ok := resp.(*QueueResponse)
	if !ok {
		t.Fatalf("wrong response type: %T", resp)
	}
	if result.CommandID == "" {
		t.Error("empty command_id")
	}
	if result.Position != 1 {
		t.Errorf("position=%d, want 1", result.Position)
	}

	// Verify in-memory queue has the command
	q := h.getQueue("test-session")
	if q == nil || q.Len() != 1 {
		t.Errorf("queue not populated: q=%v", q)
	}

	// Verify DB has the row
	loaded, err := loadCommand(context.Background(), h.state.DB(), result.CommandID)
	if err != nil {
		t.Fatalf("loadCommand: %v", err)
	}
	if loaded.Text != "echo hi" {
		t.Errorf("text=%s, want 'echo hi'", loaded.Text)
	}
	// Defaults: silence_ms=5000, notify_on_complete=true (both unset in request).
	if loaded.SilenceMs != 5000 {
		t.Errorf("default SilenceMs=%d, want 5000", loaded.SilenceMs)
	}
	if !loaded.NotifyOnComplete {
		t.Errorf("default NotifyOnComplete=%v, want true", loaded.NotifyOnComplete)
	}
}

func TestHandleQueueRespectsSilenceAndNotifyOverrides(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	// Explicit silence_ms override, notify_on_complete=false (e.g. --wait mode).
	req := `{"session":"test-session","text":"echo hi","timeout_ms":60000,"requester":"test_coord","silence_ms":2500,"notify_on_complete":false}`
	resp, err := h.HandleQueue(context.Background(), json.RawMessage(req))
	if err != nil {
		t.Fatalf("HandleQueue: %v", err)
	}
	result := resp.(*QueueResponse)

	loaded, err := loadCommand(context.Background(), h.state.DB(), result.CommandID)
	if err != nil {
		t.Fatalf("loadCommand: %v", err)
	}
	if loaded.SilenceMs != 2500 {
		t.Errorf("SilenceMs=%d, want 2500", loaded.SilenceMs)
	}
	if loaded.NotifyOnComplete {
		t.Errorf("NotifyOnComplete=%v, want false", loaded.NotifyOnComplete)
	}

	// In-memory copy should also reflect the overrides.
	q := h.getQueue("test-session")
	snap := q.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot len=%d, want 1", len(snap))
	}
	if snap[0].SilenceMs != 2500 || snap[0].NotifyOnComplete {
		t.Errorf("in-memory cmd: SilenceMs=%d NotifyOnComplete=%v, want 2500/false",
			snap[0].SilenceMs, snap[0].NotifyOnComplete)
	}
}

// countSystemMessages returns the number of message rows authored by @system.
// Shared helper for tests that verify NotifyOnComplete suppression.
func countSystemMessages(t *testing.T, h *TmuxHandler) int {
	t.Helper()
	var n int
	if err := h.state.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM messages WHERE agent_id = 'system'`).Scan(&n); err != nil {
		t.Fatalf("count system messages: %v", err)
	}
	return n
}

// TestHandleCancelSkipsSystemMessageWhenNotifyFalse verifies that cancelling a
// command with NotifyOnComplete=false does NOT write a @system message.
// --wait callers get the cancelled terminal state via the queue-wait RPC
// response directly, so an inbox message would be redundant.
func TestHandleCancelSkipsSystemMessageWhenNotifyFalse(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	q := h.getOrCreateQueue("test-session")

	cmd := &QueuedCommand{
		ID:               "cmd_quiet_cancel",
		Text:             "sleep 999",
		RequesterAgent:   "test_coord",
		State:            StateSent,
		Timeout:          60 * time.Second,
		SilenceMs:        5000,
		NotifyOnComplete: false,
		SubmittedAt:      time.Now().UTC(),
		SentAt:           time.Now().UTC(),
	}
	if err := persistCommand(ctx, h.state.DB(), "test-session", cmd, 0); err != nil {
		t.Fatal(err)
	}
	q.SetActive(cmd)

	before := countSystemMessages(t, h)

	params := json.RawMessage(`{"command_id":"cmd_quiet_cancel"}`)
	resp, err := h.HandleCancel(ctx, params)
	if err != nil {
		t.Fatalf("HandleCancel: %v", err)
	}
	cancelResp, ok := resp.(*CancelResponse)
	if !ok || cancelResp.State != StateCancelled {
		t.Fatalf("unexpected cancel response: %+v", resp)
	}
	if q.Active() != nil {
		t.Error("expected active cleared after cancel")
	}

	after := countSystemMessages(t, h)
	if after != before {
		t.Errorf("@system message count changed: before=%d after=%d — expected no new messages when NotifyOnComplete=false", before, after)
	}
}

// TestHandleCommandTimeoutSkipsSystemMessageWhenNotifyFalse verifies that a
// timeout with NotifyOnComplete=false does NOT write a @system message —
// --wait callers are blocked in the CLI and not reading inbox anyway.
func TestHandleCommandTimeoutSkipsSystemMessageWhenNotifyFalse(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	cmd := &QueuedCommand{
		ID:               "cmd_quiet_timeout",
		Text:             "sleep 999",
		RequesterAgent:   "test_coord",
		State:            StateActive,
		Timeout:          30 * time.Second,
		SilenceMs:        5000,
		NotifyOnComplete: false,
		SubmittedAt:      time.Now().UTC(),
		SentAt:           time.Now().UTC(),
	}
	if err := persistCommand(ctx, h.state.DB(), "test-session", cmd, 0); err != nil {
		t.Fatal(err)
	}

	before := countSystemMessages(t, h)

	h.handleCommandTimeout(ctx, "test-session", cmd)

	// State should still transition to timeout_waiting in DB.
	loaded, err := loadCommand(ctx, h.state.DB(), "cmd_quiet_timeout")
	if err != nil {
		t.Fatalf("loadCommand: %v", err)
	}
	if loaded.State != StateTimeoutWaiting {
		t.Errorf("state=%s, want %s", loaded.State, StateTimeoutWaiting)
	}

	after := countSystemMessages(t, h)
	if after != before {
		t.Errorf("@system message count changed: before=%d after=%d — expected no new messages when NotifyOnComplete=false", before, after)
	}
}

// TestCompleteCommandSkipsSystemMessageWhenNotifyFalse verifies that a command
// with NotifyOnComplete=false does NOT write a @system message on completion.
// This is the --wait mode's quiet path — the caller gets the result via the
// queue-wait RPC response instead of an inbox notification.
func TestCompleteCommandSkipsSystemMessageWhenNotifyFalse(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	q := h.getOrCreateQueue("test-session")

	before := countSystemMessages(t, h)

	cmd := &QueuedCommand{
		ID:               "cmd_quiet",
		Text:             "echo quiet",
		RequesterAgent:   "test_coord",
		State:            StateActive,
		SilenceMs:        5000,
		NotifyOnComplete: false, // --wait mode: suppress @system completion notification
		SubmittedAt:      time.Now().UTC(),
		SentAt:           time.Now().UTC(),
	}
	if err := persistCommand(ctx, h.state.DB(), "test-session", cmd, 0); err != nil {
		t.Fatal(err)
	}
	q.SetActive(cmd)

	h.completeCommand(ctx, "test-session", q, cmd)

	// Verify state transitioned to completed in DB.
	loaded, err := loadCommand(ctx, h.state.DB(), "cmd_quiet")
	if err != nil {
		t.Fatalf("loadCommand: %v", err)
	}
	if loaded.State != StateCompleted {
		t.Errorf("state=%s, want %s", loaded.State, StateCompleted)
	}

	// No new @system messages should have been written.
	after := countSystemMessages(t, h)
	if after != before {
		t.Errorf("@system message count changed: before=%d after=%d — expected no new messages when NotifyOnComplete=false", before, after)
	}
}

func TestCheckPaneCompletesActiveCommand(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	q := h.getOrCreateQueue("test-session")

	// Simulate an active command
	cmd := &QueuedCommand{
		ID:             "cmd_active",
		Text:           "echo hi",
		RequesterAgent: "test_coord",
		State:          StateActive,
		SubmittedAt:    time.Now(),
		SentAt:         time.Now(),
	}
	q.SetActive(cmd)
	if err := persistCommand(ctx, h.state.DB(), "test-session", cmd, 0); err != nil {
		t.Fatal(err)
	}

	// Fire check-pane (simulating silence detected)
	params := json.RawMessage(`{"session":"test-session","reason":""}`)
	resp, err := h.HandleCheckPane(ctx, params)
	if err != nil {
		t.Fatalf("HandleCheckPane: %v", err)
	}

	result, ok := resp.(*CheckPaneResponse)
	if !ok {
		t.Fatalf("wrong response type: %T", resp)
	}

	// Active command should be cleared
	if q.Active() != nil {
		t.Error("active command not cleared")
	}

	// State should have transitioned to completed in DB
	loaded, err := loadCommand(ctx, h.state.DB(), "cmd_active")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != StateCompleted {
		t.Errorf("state=%s, want completed", loaded.State)
	}

	_ = result
}

func TestHandleQueueWaitReturnsOnCompletion(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()

	// Insert a command already in StateCompleted.
	cmd := &QueuedCommand{
		ID:             "cmd_wait_test",
		Text:           "echo done",
		RequesterAgent: "test_coord",
		State:          StateCompleted,
		SubmittedAt:    time.Now(),
		SentAt:         time.Now(),
		CompletedAt:    time.Now(),
		CapturedOutput: "done\n",
	}
	if err := persistCommand(ctx, h.state.DB(), "test-session", cmd, 0); err != nil {
		t.Fatal(err)
	}
	h.state.Lock()
	_ = updateCommandState(ctx, h.state.DB(), cmd)
	h.state.Unlock()

	params := json.RawMessage(`{"command_id":"cmd_wait_test","timeout_ms":5000}`)
	resp, err := h.HandleQueueWait(ctx, params)
	if err != nil {
		t.Fatalf("HandleQueueWait: %v", err)
	}

	result, ok := resp.(*QueueWaitResponse)
	if !ok {
		t.Fatalf("wrong response type: %T", resp)
	}
	if result.State != StateCompleted {
		t.Errorf("state=%s, want completed", result.State)
	}
	if result.Output != "done\n" {
		t.Errorf("output=%q, want 'done\\n'", result.Output)
	}
}

func TestHandleCancelActiveCommand(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	q := h.getOrCreateQueue("test-session")

	cmd := &QueuedCommand{
		ID:             "cmd_cancel",
		Text:           "long-running",
		RequesterAgent: "test_coord",
		State:          StateActive,
		SubmittedAt:    time.Now(),
		SentAt:         time.Now(),
	}
	q.SetActive(cmd)
	if err := persistCommand(ctx, h.state.DB(), "test-session", cmd, 0); err != nil {
		t.Fatal(err)
	}

	params := json.RawMessage(`{"command_id":"cmd_cancel"}`)
	_, err := h.HandleCancel(ctx, params)
	if err != nil {
		t.Fatalf("HandleCancel: %v", err)
	}

	loaded, err := loadCommand(ctx, h.state.DB(), "cmd_cancel")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != StateCancelled {
		t.Errorf("state=%s, want cancelled", loaded.State)
	}
	if q.Active() != nil {
		t.Error("active command not cleared")
	}
}

func TestSessionQueueFIFO(t *testing.T) {
	q := NewSessionQueue("test-session")

	q.Enqueue(&QueuedCommand{ID: "cmd_1", Text: "first"})
	q.Enqueue(&QueuedCommand{ID: "cmd_2", Text: "second"})
	q.Enqueue(&QueuedCommand{ID: "cmd_3", Text: "third"})

	if got := q.Len(); got != 3 {
		t.Errorf("Len=%d, want 3", got)
	}

	head := q.Peek()
	if head == nil || head.ID != "cmd_1" {
		t.Errorf("Peek=%v, want cmd_1", head)
	}

	popped := q.Pop()
	if popped == nil || popped.ID != "cmd_1" {
		t.Errorf("Pop=%v, want cmd_1", popped)
	}

	if got := q.Len(); got != 2 {
		t.Errorf("Len after pop=%d, want 2", got)
	}
}
