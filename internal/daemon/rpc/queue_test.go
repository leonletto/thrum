package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

// TestHandleQueuePositionNoTOCTOU fires many concurrent HandleQueue calls for
// the same session and asserts every row receives a unique DB position. This
// would fail under the original implementation (queue.Len() was read outside
// the state lock, so concurrent submitters could compute the same position).
func TestHandleQueuePositionNoTOCTOU(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	const n = 20
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	positions := make([]int, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := fmt.Sprintf(`{"session":"test-session","text":"echo %d","requester":"test_coord"}`, idx)
			resp, err := h.HandleQueue(context.Background(), json.RawMessage(req))
			if err != nil {
				errCh <- err
				return
			}
			qr := resp.(*QueueResponse)
			positions[idx] = qr.Position
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("HandleQueue error: %v", err)
	}

	// Every position must be unique AND in the range [1, n].
	seen := make(map[int]bool, n)
	for i, p := range positions {
		if p < 1 || p > n {
			t.Errorf("idx=%d position=%d out of range [1,%d]", i, p, n)
		}
		if seen[p] {
			t.Errorf("duplicate position %d (idx=%d) — TOCTOU race not fixed", p, i)
		}
		seen[p] = true
	}

	// DB row positions should also be unique per session.
	rows, err := h.state.DB().QueryContext(context.Background(),
		`SELECT position, COUNT(*) FROM command_queue WHERE session_name = 'test-session' GROUP BY position`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var pos, count int
		if err := rows.Scan(&pos, &count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("position %d has %d rows — duplicates in DB", pos, count)
		}
	}
}

// TestConcurrentTransitionsSingleFinalState fires completeCommand,
// HandleCancel, and the timeout callback for the SAME command on three
// goroutines "simultaneously" to stress the cmd.mu serialisation. Under -race
// this will flag any unsynchronised read/write on cmd.State / SentAt /
// CompletedAt / CapturedOutput / timer. Only ONE path should win the
// transition; the other two must short-circuit on the isTerminalState check.
func TestConcurrentTransitionsSingleFinalState(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	q := h.getOrCreateQueue("test-session")

	cmd := &QueuedCommand{
		ID:               "cmd_race",
		Text:             "echo race",
		RequesterAgent:   "test_coord",
		State:            StateSent,
		Timeout:          60 * time.Second,
		SilenceMs:        5000,
		NotifyOnComplete: false, // suppress sendSystemMessage so the test is deterministic
		SubmittedAt:      time.Now().UTC(),
		SentAt:           time.Now().UTC(),
	}
	if err := persistCommand(ctx, h.state.DB(), "test-session", cmd, 0); err != nil {
		t.Fatal(err)
	}
	q.SetActive(cmd)

	// Barrier so all three goroutines start as close together as possible.
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		<-start
		h.completeCommand(ctx, "test-session", q, cmd)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, _ = h.HandleCancel(ctx, json.RawMessage(`{"command_id":"cmd_race"}`))
	}()
	go func() {
		defer wg.Done()
		<-start
		h.handleCommandTimeout(ctx, "test-session", cmd)
	}()

	close(start)
	wg.Wait()

	// Final state must be terminal. Read via the thread-safe helper.
	final := cmd.stateSnapshot()
	if !isTerminalState(final) {
		t.Errorf("final state = %q, want one of completed/cancelled/interrupted", final)
	}

	// DB should agree with the in-memory state.
	loaded, err := loadCommand(ctx, h.state.DB(), "cmd_race")
	if err != nil {
		t.Fatalf("loadCommand: %v", err)
	}
	// Either StateCompleted or StateCancelled is valid (whichever path won
	// the race). handleCommandTimeout transitions through StateTimeoutWaiting
	// but that's not terminal, so if it ran first the completeCommand or
	// cancel path would still have run and produced a terminal state.
	if loaded.State != StateCompleted && loaded.State != StateCancelled {
		t.Errorf("DB state = %q, want completed or cancelled", loaded.State)
	}
}

// TestSendSystemMessageUsesSentinelSessionID verifies that @system messages
// are written with session_id='system' so they remain queryable by either
// agent_id OR session_id.
func TestSendSystemMessageUsesSentinelSessionID(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	h.sendSystemMessage(ctx, "test_coord", "hello from system")

	var sessionID string
	err := h.state.DB().QueryRowContext(ctx,
		`SELECT session_id FROM messages WHERE agent_id = 'system' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&sessionID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if sessionID != "system" {
		t.Errorf("session_id=%q, want %q", sessionID, "system")
	}
}

// TestStatusSnapshotIsRaceFreeVsTransitions fires HandleQueueStatus
// concurrently with a transition path (completeCommand) for the same
// command. Under -race any unsynchronised read of cmd.State / SentAt /
// CompletedAt / CapturedOutput via the snapshot path is caught.
//
// The test runs several iterations to increase the chance of overlap — a
// single pairing might serialise cleanly by luck; N pairings make a latent
// race far more likely to manifest.
func TestStatusSnapshotIsRaceFreeVsTransitions(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()

	const iterations = 50
	for i := 0; i < iterations; i++ {
		sess := fmt.Sprintf("sess-%d", i)
		q := h.getOrCreateQueue(sess)

		cmd := &QueuedCommand{
			ID:               fmt.Sprintf("cmd_race_%d", i),
			Text:             "echo",
			RequesterAgent:   "test_coord",
			State:            StateSent,
			Timeout:          60 * time.Second,
			SilenceMs:        5000,
			NotifyOnComplete: false,
			SubmittedAt:      time.Now().UTC(),
			SentAt:           time.Now().UTC(),
		}
		if err := persistCommand(ctx, h.state.DB(), sess, cmd, 0); err != nil {
			t.Fatalf("iter %d: persist: %v", i, err)
		}
		q.SetActive(cmd)

		// Start barrier: two goroutines, one transitions the command to
		// terminal, the other repeatedly calls HandleQueueStatus.
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			h.completeCommand(ctx, sess, q, cmd)
		}()

		go func() {
			defer wg.Done()
			<-start
			// Hammer the status path a few times so at least one call
			// overlaps with the mutation window.
			req := json.RawMessage(fmt.Sprintf(`{"session":%q}`, sess))
			for j := 0; j < 10; j++ {
				_, _ = h.HandleQueueStatus(ctx, req)
			}
		}()

		close(start)
		wg.Wait()

		// Final state must be terminal.
		if s := cmd.stateSnapshot(); !isTerminalState(s) {
			t.Errorf("iter %d: final state %q not terminal", i, s)
		}
	}
}

// TestStatusSnapshotFieldsMatchCommand is a correctness check on the view
// conversion — every exposed field in QueuedCommandView must reflect the
// original command's value.
func TestStatusSnapshotFieldsMatchCommand(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	q := h.getOrCreateQueue("test-session")

	cmd := &QueuedCommand{
		ID:               "cmd_view",
		Text:             "echo hello",
		RequesterAgent:   "test_coord",
		State:            StateActive,
		Timeout:          120 * time.Second,
		SilenceMs:        2500,
		NotifyOnComplete: false,
		SubmittedAt:      time.Now().UTC().Add(-5 * time.Second),
		SentAt:           time.Now().UTC().Add(-3 * time.Second),
	}
	if err := persistCommand(ctx, h.state.DB(), "test-session", cmd, 1); err != nil {
		t.Fatalf("persist: %v", err)
	}
	q.Enqueue(cmd)

	active, queued := q.StatusSnapshot()
	if active != nil {
		t.Errorf("expected no active command, got %+v", active)
	}
	if len(queued) != 1 {
		t.Fatalf("queued len=%d, want 1", len(queued))
	}
	v := queued[0]

	if v.ID != cmd.ID {
		t.Errorf("ID=%q, want %q", v.ID, cmd.ID)
	}
	if v.Text != cmd.Text {
		t.Errorf("Text=%q, want %q", v.Text, cmd.Text)
	}
	if v.RequesterAgent != cmd.RequesterAgent {
		t.Errorf("RequesterAgent=%q, want %q", v.RequesterAgent, cmd.RequesterAgent)
	}
	if v.State != StateActive {
		t.Errorf("State=%q, want %q", v.State, StateActive)
	}
	if v.SilenceMs != 2500 {
		t.Errorf("SilenceMs=%d, want 2500", v.SilenceMs)
	}
	if v.NotifyOnComplete {
		t.Errorf("NotifyOnComplete=%v, want false", v.NotifyOnComplete)
	}
	if !v.SubmittedAt.Equal(cmd.SubmittedAt) {
		t.Errorf("SubmittedAt: got %v, want %v", v.SubmittedAt, cmd.SubmittedAt)
	}
	if !v.SentAt.Equal(cmd.SentAt) {
		t.Errorf("SentAt: got %v, want %v", v.SentAt, cmd.SentAt)
	}
}

// TestHandleQueueWaitElapsedFromSentAt verifies that the elapsed_ms field in
// a queue-wait response is measured from SentAt (not SubmittedAt) once the
// command has been typed — matching the convention used by the completion
// notification message body.
func TestHandleQueueWaitElapsedFromSentAt(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	// Seed a command in the DB with a SubmittedAt 10s ago and a SentAt 2s ago
	// that has already reached the Completed terminal state.
	submitted := time.Now().UTC().Add(-10 * time.Second).Format(time.RFC3339Nano)
	sent := time.Now().UTC().Add(-2 * time.Second).Format(time.RFC3339Nano)
	completed := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := h.state.DB().ExecContext(ctx,
		`INSERT INTO command_queue
		 (command_id, session_name, requester_agent, command_text, state,
		  timeout_ms, silence_ms, notify_on_complete, submitted_at, sent_at, completed_at, captured_output, position)
		 VALUES ('cmd_elapsed', 'test-session', 'test_coord', 'echo hi', 'completed',
		         60000, 5000, 1, ?, ?, ?, 'done', 0)`,
		submitted, sent, completed,
	)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := `{"command_id":"cmd_elapsed","timeout_ms":5000}`
	resp, err := h.HandleQueueWait(ctx, json.RawMessage(req))
	if err != nil {
		t.Fatalf("HandleQueueWait: %v", err)
	}
	wr := resp.(*QueueWaitResponse)

	// Elapsed should be ~2s (from SentAt), not ~10s (from SubmittedAt).
	// Allow a generous tolerance window for clock slop / test scheduling:
	// anything under 5s means we're measuring from SentAt, not SubmittedAt.
	if wr.ElapsedMs > 5000 {
		t.Errorf("ElapsedMs=%d — expected < 5000 (measured from SentAt, not SubmittedAt=10s ago)", wr.ElapsedMs)
	}
	if wr.ElapsedMs < 1000 {
		t.Errorf("ElapsedMs=%d — expected >= 1000 (seeded SentAt is 2s ago)", wr.ElapsedMs)
	}
}

func TestRestartRecoveryMarksActiveAsInterrupted(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()

	// Insert a "sent" command — simulating a command that was in-flight when
	// the daemon previously exited.
	sentCmd := &QueuedCommand{
		ID:             "cmd_interrupted",
		Text:           "still running",
		RequesterAgent: "test_coord",
		State:          StateSent,
		SubmittedAt:    time.Now(),
		SentAt:         time.Now(),
	}
	if err := persistCommand(ctx, h.state.DB(), "test-session", sentCmd, 0); err != nil {
		t.Fatalf("seed sent command: %v", err)
	}

	// Insert a "queued" command — simulating a command waiting its turn.
	queuedCmd := &QueuedCommand{
		ID:             "cmd_queued",
		Text:           "waiting in line",
		RequesterAgent: "test_coord",
		State:          StateQueued,
		SubmittedAt:    time.Now(),
	}
	if err := persistCommand(ctx, h.state.DB(), "test-session", queuedCmd, 1); err != nil {
		t.Fatalf("seed queued command: %v", err)
	}

	if err := h.RecoverQueueState(ctx); err != nil {
		t.Fatalf("RecoverQueueState: %v", err)
	}

	// The sent command must now be interrupted in the DB.
	loaded, err := loadCommand(ctx, h.state.DB(), "cmd_interrupted")
	if err != nil {
		t.Fatalf("loadCommand(cmd_interrupted): %v", err)
	}
	if loaded.State != StateInterrupted {
		t.Errorf("cmd_interrupted state=%s, want %s", loaded.State, StateInterrupted)
	}

	// The queued command must be reloaded into the in-memory queue.
	q := h.getQueue("test-session")
	if q == nil {
		t.Fatal("no in-memory queue for test-session after recovery")
	}
	if q.Len() != 1 {
		t.Errorf("queue len=%d, want 1", q.Len())
	}

	// The queued command's DB state must remain "queued" — reload doesn't mutate it.
	loadedQ, err := loadCommand(ctx, h.state.DB(), "cmd_queued")
	if err != nil {
		t.Fatalf("loadCommand(cmd_queued): %v", err)
	}
	if loadedQ.State != StateQueued {
		t.Errorf("cmd_queued state=%s, want %s", loadedQ.State, StateQueued)
	}
}

// TestSendQueuedCommandDrainsOnDeadSession verifies that when SendKeys fails
// and the tmux session no longer exists, sendQueuedCommand drains the whole
// queue: every command transitions to StateInterrupted, the in-memory queue
// is removed, and the loop does not leave commands stranded in StateQueued.
//
// The test uses a session name that definitely does not exist in tmux
// ("ghost-session-for-test") so ttmux.SendKeys will fail and
// ttmux.HasSession will return false — matching the production code path
// for a session killed externally between enqueue and dispatch.
func TestSendQueuedCommandDrainsOnDeadSession(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	session := "ghost-session-for-test"
	q := h.getOrCreateQueue(session)

	// Enqueue two commands so the drain has something to transition.
	cmd1 := &QueuedCommand{
		ID:               "cmd_ghost_1",
		Text:             "echo never",
		RequesterAgent:   "test_coord",
		State:            StateQueued,
		Timeout:          60 * time.Second,
		SilenceMs:        5000,
		NotifyOnComplete: false,
		SubmittedAt:      time.Now().UTC(),
	}
	cmd2 := &QueuedCommand{
		ID:               "cmd_ghost_2",
		Text:             "echo also never",
		RequesterAgent:   "test_coord",
		State:            StateQueued,
		Timeout:          60 * time.Second,
		SilenceMs:        5000,
		NotifyOnComplete: false,
		SubmittedAt:      time.Now().UTC(),
	}
	if err := persistCommand(ctx, h.state.DB(), session, cmd1, 1); err != nil {
		t.Fatal(err)
	}
	if err := persistCommand(ctx, h.state.DB(), session, cmd2, 2); err != nil {
		t.Fatal(err)
	}
	q.Enqueue(cmd1)
	q.Enqueue(cmd2)

	// Attempt to dispatch the front command. SendKeys will fail because
	// the session does not exist, and HasSession will return false.
	h.sendQueuedCommand(ctx, session, q, cmd1)

	// Both commands should now be marked interrupted in the DB.
	for _, id := range []string{"cmd_ghost_1", "cmd_ghost_2"} {
		loaded, err := loadCommand(ctx, h.state.DB(), id)
		if err != nil {
			t.Fatalf("loadCommand %s: %v", id, err)
		}
		if loaded.State != StateInterrupted {
			t.Errorf("command %s state=%s, want %s (session-death drain)", id, loaded.State, StateInterrupted)
		}
	}

	// The in-memory queue should have been removed from the map.
	if h.getQueue(session) != nil {
		t.Error("expected queue removed from in-memory map after dead-session drain")
	}
}

func TestHandleKillDrainsQueue(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	q := h.getOrCreateQueue("doomed-session")

	// Add an active + queued command.
	active := &QueuedCommand{
		ID:             "cmd_active_kill",
		Text:           "still running",
		RequesterAgent: "test_coord",
		State:          StateActive,
		SubmittedAt:    time.Now(),
		SentAt:         time.Now(),
	}
	queued := &QueuedCommand{
		ID:             "cmd_queued_kill",
		Text:           "waiting",
		RequesterAgent: "test_coord",
		State:          StateQueued,
		SubmittedAt:    time.Now(),
	}
	q.SetActive(active)
	q.Enqueue(queued)
	_ = persistCommand(ctx, h.state.DB(), "doomed-session", active, 0)
	_ = persistCommand(ctx, h.state.DB(), "doomed-session", queued, 1)

	// Call drainQueueOnKill directly (avoids invoking real tmux KillSession).
	h.drainQueueOnKill(ctx, "doomed-session")

	// Both commands should be marked interrupted in the DB.
	for _, id := range []string{"cmd_active_kill", "cmd_queued_kill"} {
		loaded, err := loadCommand(ctx, h.state.DB(), id)
		if err != nil {
			t.Fatalf("loadCommand %s: %v", id, err)
		}
		if loaded.State != StateInterrupted {
			t.Errorf("command %s state=%s, want interrupted", id, loaded.State)
		}
	}

	// The in-memory queue should be gone.
	if h.getQueue("doomed-session") != nil {
		t.Error("queue not removed on drain")
	}
}
