package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/identity"
	ttmux "github.com/leonletto/thrum/internal/tmux"
	"github.com/leonletto/thrum/internal/types"
)

// QueueRequest is the tmux.queue RPC request.
type QueueRequest struct {
	Session   string `json:"session"`
	Text      string `json:"text"`
	TimeoutMs int64  `json:"timeout_ms"`
	Requester string `json:"requester"`
	// SilenceMs is the per-command silence threshold for completion detection.
	// Default 5000ms. --wait mode users with fast shell commands may lower this.
	SilenceMs int64 `json:"silence_ms,omitempty"`
	// NotifyOnComplete controls whether the daemon sends an @system inbox
	// message when the command reaches a terminal state. Default true. --wait
	// mode sets this to false — the caller gets the result via the queue-wait
	// RPC response instead.
	//
	// Uses a pointer so we can distinguish "not set" (default → true) from
	// "explicitly false". Omitted/null ⇒ default true; true ⇒ true; false ⇒ false.
	NotifyOnComplete *bool `json:"notify_on_complete,omitempty"`
}

// QueueResponse is the tmux.queue RPC response.
type QueueResponse struct {
	CommandID string `json:"command_id"`
	Position  int    `json:"position"`
}

// generateCommandID generates a ULID-based command identifier with a "cmd_" prefix.
func generateCommandID() string {
	raw := identity.GenerateEventID() // returns "evt_<ULID>"
	return "cmd_" + strings.TrimPrefix(raw, "evt_")
}

// HandleQueue handles the tmux.queue RPC — submit a command to a session's queue.
func (h *TmuxHandler) HandleQueue(ctx context.Context, params json.RawMessage) (any, error) {
	var req QueueRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.Session == "" {
		return nil, fmt.Errorf("session is required")
	}
	if req.Text == "" {
		return nil, fmt.Errorf("text is required")
	}
	if req.Requester == "" {
		return nil, fmt.Errorf("requester is required")
	}
	if req.TimeoutMs == 0 {
		req.TimeoutMs = 120000 // default 2 minutes
	}
	if req.SilenceMs <= 0 {
		req.SilenceMs = 5000 // default 5s silence threshold
	}
	notify := true
	if req.NotifyOnComplete != nil {
		notify = *req.NotifyOnComplete
	}

	cmd := &QueuedCommand{
		ID:               generateCommandID(),
		Text:             req.Text,
		RequesterAgent:   req.Requester,
		Timeout:          time.Duration(req.TimeoutMs) * time.Millisecond,
		SilenceMs:        req.SilenceMs,
		NotifyOnComplete: notify,
		State:            StateQueued,
		SubmittedAt:      time.Now().UTC(),
	}

	// Position is computed inside the state lock to eliminate a TOCTOU race
	// between concurrent HandleQueue calls for the same session. We count the
	// rows that are still live in the queue (non-terminal states) and assign
	// position = count + 1. Holding state.Lock() across both the SELECT and
	// the INSERT serialises concurrent submitters.
	h.state.Lock()
	var count int
	if err := h.state.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM command_queue
		 WHERE session_name = ?
		   AND state IN ('queued', 'sent', 'active', 'timeout_waiting')`,
		req.Session,
	).Scan(&count); err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("count queue: %w", err)
	}
	position := count + 1
	if err := persistCommand(ctx, h.state.DB(), req.Session, cmd, position); err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("persist command: %w", err)
	}
	h.state.Unlock()

	// Persist succeeded — publish to the in-memory queue.
	h.getOrCreateQueue(req.Session).Enqueue(cmd)

	return &QueueResponse{
		CommandID: cmd.ID,
		Position:  position,
	}, nil
}

// completeCommand captures pane output, delivers the result, and advances the queue.
//
// Concurrency: completeCommand races against HandleCancel and the timeout timer
// (handleCommandTimeout). All three paths mutate cmd.State, so we serialise
// them via cmd.mu. The first entrant transitions the command to StateCompleted
// while holding the lock; any subsequent caller that observes a terminal state
// bails immediately. Pane capture (I/O) runs BEFORE acquiring the lock so we
// don't hold cmd.mu during a potentially-slow tmux invocation.
func (h *TmuxHandler) completeCommand(ctx context.Context, session string, queue *SessionQueue, cmd *QueuedCommand) {
	// Capture last 500 lines of pane; tolerate failure (tmux may not be running in tests).
	output, err := ttmux.CapturePane(session+":0.0", 500)
	if err != nil {
		log.Printf("[queue] capture-pane failed for %s: %v", session, err)
		output = ""
	}

	// Mutate cmd and persist under cmd.mu. We keep the state lock nested
	// INSIDE cmd.mu (lock order: cmd.mu → state.Lock()) so updateCommandState
	// reads cmd's protected fields safely.
	var (
		sentAt   time.Time
		notify   bool
		body     string
		skip     bool
		shortCut bool // true if another path already finalised this command
	)
	cmd.mu.Lock()
	if isTerminalState(cmd.State) {
		shortCut = true
	} else {
		cmd.State = StateCompleted
		cmd.CompletedAt = time.Now().UTC()
		cmd.CapturedOutput = output
		if cmd.timer != nil {
			cmd.timer.Stop()
		}

		h.state.Lock()
		_ = updateCommandState(ctx, h.state.DB(), cmd)
		h.state.Unlock()

		// Snapshot fields we need after releasing cmd.mu (so the I/O path
		// below never touches the mutex).
		sentAt = cmd.SentAt
		notify = cmd.NotifyOnComplete
		skip = !notify
		if notify {
			elapsed := cmd.CompletedAt.Sub(sentAt)
			body = fmt.Sprintf("Command %s completed.\nSession: %s\nElapsed: %ds\n\nOutput:\n---\n%s\n---",
				cmd.ID, session, int(elapsed.Seconds()), output)
		}
	}
	cmd.mu.Unlock()

	if shortCut {
		return
	}

	queue.ClearActive()

	// Deliver result as @system message unless the caller opted out (e.g. --wait
	// mode, where the result is returned via the queue-wait RPC response).
	if !skip {
		h.sendSystemMessage(ctx, cmd.RequesterAgent, body)
	}

	// Send the next queued command if any.
	if next := queue.Peek(); next != nil {
		h.sendQueuedCommand(ctx, session, queue, next)
	} else {
		// Queue empty — restore 60s silence.
		bin := h.thrumBin()
		if err := ttmux.SetMonitorSilence(session, 60, bin, h.thrumDir); err != nil {
			log.Printf("[queue] SetMonitorSilence(60) failed for %s: %v", session, err)
		}
	}
}

// sendQueuedCommand pops the head of the queue, types the command, and starts
// timeout tracking. cmd.State, cmd.SentAt, and cmd.timer are all protected by
// cmd.mu; we acquire it around the mutation + persist sequence. Typing keys is
// done OUTSIDE the lock (it's I/O and the command is not yet visible as
// "active" to any other goroutine).
func (h *TmuxHandler) sendQueuedCommand(ctx context.Context, session string, queue *SessionQueue, cmd *QueuedCommand) {
	target := session + ":0.0"

	// Type the command and press Enter — do this before taking cmd.mu so
	// slow tmux calls don't block concurrent cancel attempts.
	if err := ttmux.SendKeys(target, cmd.Text); err != nil {
		log.Printf("[queue] SendKeys failed: %v", err)
		return
	}
	if err := ttmux.SendSpecialKey(target, "Enter"); err != nil {
		log.Printf("[queue] SendSpecialKey failed: %v", err)
		return
	}

	cmd.mu.Lock()
	if isTerminalState(cmd.State) {
		// Cancelled between our dispatch decision and the actual send.
		cmd.mu.Unlock()
		return
	}
	cmd.State = StateSent
	cmd.SentAt = time.Now().UTC()

	h.state.Lock()
	_ = updateCommandState(ctx, h.state.DB(), cmd)
	h.state.Unlock()

	// Start timeout goroutine while still holding cmd.mu so the timer
	// reference is published atomically with the state transition.
	cmd.timer = time.AfterFunc(cmd.Timeout, func() {
		h.handleCommandTimeout(context.Background(), session, cmd)
	})
	cmd.mu.Unlock()

	queue.Pop()
	queue.SetActive(cmd)

	// Switch monitor-silence to the command's configured silence threshold.
	// SetMonitorSilence takes seconds; round cmd.SilenceMs up so sub-second
	// values still produce a 1s timer. Defensive fallback to 5s if unset.
	silenceSec := 5
	if cmd.SilenceMs > 0 {
		silenceSec = int((cmd.SilenceMs + 999) / 1000)
		if silenceSec < 1 {
			silenceSec = 1
		}
	}
	bin := h.thrumBin()
	if err := ttmux.SetMonitorSilence(session, silenceSec, bin, h.thrumDir); err != nil {
		log.Printf("[queue] SetMonitorSilence(%d) failed for %s: %v", silenceSec, session, err)
	}
}

// thrumBin returns the absolute path to the thrum binary.
func (h *TmuxHandler) thrumBin() string {
	exe, err := os.Executable()
	if err != nil {
		return "thrum"
	}
	return exe
}

// QueueWaitRequest is the tmux.queue-wait RPC request.
type QueueWaitRequest struct {
	CommandID string `json:"command_id"`
	TimeoutMs int64  `json:"timeout_ms"`
}

// QueueWaitResponse is the tmux.queue-wait RPC response.
type QueueWaitResponse struct {
	CommandID string `json:"command_id"`
	State     string `json:"state"`
	Output    string `json:"output,omitempty"`
	ElapsedMs int64  `json:"elapsed_ms,omitempty"`
}

// isTerminalState reports whether a state is final.
func isTerminalState(s string) bool {
	return s == StateCompleted || s == StateCancelled || s == StateInterrupted
}

// HandleQueueWait blocks until the command reaches a terminal state or the timeout elapses.
func (h *TmuxHandler) HandleQueueWait(ctx context.Context, params json.RawMessage) (any, error) {
	var req QueueWaitRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.CommandID == "" {
		return nil, fmt.Errorf("command_id is required")
	}
	if req.TimeoutMs <= 0 {
		req.TimeoutMs = 120000
	}

	deadline := time.Now().Add(time.Duration(req.TimeoutMs) * time.Millisecond)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// elapsedMs returns how long the command has been running. We prefer
	// SentAt (matches the completion-notification convention: "elapsed since
	// the daemon actually typed the command") and fall back to SubmittedAt
	// for commands that haven't been typed yet.
	elapsedMs := func(cmd *QueuedCommand) int64 {
		if !cmd.SentAt.IsZero() {
			return time.Since(cmd.SentAt).Milliseconds()
		}
		if !cmd.SubmittedAt.IsZero() {
			return time.Since(cmd.SubmittedAt).Milliseconds()
		}
		return 0
	}

	for {
		cmd, err := loadCommand(ctx, h.state.DB(), req.CommandID)
		if err != nil {
			return nil, fmt.Errorf("load command: %w", err)
		}

		if isTerminalState(cmd.State) {
			return &QueueWaitResponse{
				CommandID: cmd.ID,
				State:     cmd.State,
				Output:    cmd.CapturedOutput,
				ElapsedMs: elapsedMs(cmd),
			}, nil
		}

		if time.Now().After(deadline) {
			return &QueueWaitResponse{
				CommandID: cmd.ID,
				State:     cmd.State,
				ElapsedMs: elapsedMs(cmd),
			}, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			// continue polling
		}
	}
}

// QueueStatusRequest is the tmux.queue-status RPC request.
type QueueStatusRequest struct {
	Session string `json:"session"`
}

// QueueStatusResponse is the tmux.queue-status RPC response.
type QueueStatusResponse struct {
	Session string           `json:"session"`
	Active  *QueuedCommand   `json:"active,omitempty"`
	Queued  []*QueuedCommand `json:"queued,omitempty"`
}

// HandleQueueStatus returns the current queue state for a session.
func (h *TmuxHandler) HandleQueueStatus(ctx context.Context, params json.RawMessage) (any, error) {
	var req QueueStatusRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.Session == "" {
		return nil, fmt.Errorf("session is required")
	}

	q := h.getQueue(req.Session)
	if q == nil {
		return &QueueStatusResponse{Session: req.Session}, nil
	}

	// Use public accessors — they handle locking internally.
	// Never call q.mu.Lock() directly here — that would deadlock.
	return &QueueStatusResponse{
		Session: req.Session,
		Active:  q.Active(),
		Queued:  q.Snapshot(),
	}, nil
}

// sendSystemMessage writes a message from @system to the recipient.
func (h *TmuxHandler) sendSystemMessage(ctx context.Context, recipient, body string) {
	if recipient == "" {
		log.Printf("[queue] sendSystemMessage: empty recipient, skipping")
		return
	}

	event := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		EventID:   identity.GenerateEventID(),
		Version:   1,
		MessageID: identity.GenerateMessageID(),
		AgentID:   "system",
		// Sentinel session_id so the messages row stays queryable. The
		// @system identity has no real session; using a well-known literal
		// makes "find all system-authored messages" trivial via either
		// agent_id OR session_id.
		SessionID: "system",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: body,
		},
		Refs:       []types.Ref{{Type: "mention", Value: recipient}},
		Recipients: []string{recipient},
	}

	h.state.Lock()
	if err := h.state.WriteEvent(ctx, event); err != nil {
		log.Printf("[queue] write @system message failed: %v", err)
	}
	h.state.Unlock()
}

// handleCommandTimeout is invoked by the per-command timer when the configured
// timeout elapses. It races against completeCommand (silence detected shortly
// after the timer fires) and HandleCancel, so all mutations run under cmd.mu.
// If the command has already reached a terminal state or is already in
// timeout_waiting, we bail — the timer.Stop() on the "winning" path may have
// returned false while the callback was already enqueued on the runtime.
func (h *TmuxHandler) handleCommandTimeout(ctx context.Context, session string, cmd *QueuedCommand) {
	var (
		shouldSend bool
		body       string
	)
	cmd.mu.Lock()
	if isTerminalState(cmd.State) || cmd.State == StateTimeoutWaiting {
		cmd.mu.Unlock()
		return
	}
	cmd.State = StateTimeoutWaiting

	h.state.Lock()
	_ = updateCommandState(ctx, h.state.DB(), cmd)
	h.state.Unlock()

	// --wait callers are blocked in the CLI (not reading inbox); if they want
	// to intervene on long commands they should not use --wait.
	if cmd.NotifyOnComplete {
		shouldSend = true
		body = fmt.Sprintf("Command %s still processing after %ds.\nSession: %s\nSend \"thrum tmux cancel %s\" to abort.",
			cmd.ID, int(cmd.Timeout.Seconds()), session, cmd.ID)
	}
	cmd.mu.Unlock()

	if shouldSend {
		h.sendSystemMessage(ctx, cmd.RequesterAgent, body)
	}
}

// CancelRequest is the tmux.cancel RPC request.
type CancelRequest struct {
	CommandID string `json:"command_id"`
}

// CancelResponse is the tmux.cancel RPC response.
type CancelResponse struct {
	CommandID string `json:"command_id"`
	State     string `json:"state"`
	Output    string `json:"output,omitempty"`
}

// HandleCancel handles the tmux.cancel RPC.
//
// Lock order: we snapshot h.queues while holding h.queuesMu, then release
// queuesMu before calling any queue methods. This avoids a nested
// queuesMu → q.mu acquisition pattern (via queue.Active()) and respects the
// "don't hold queue.mu from outside queue.go" rule. Command-level mutations go
// through cmd.mu; HandleCancel serialises against completeCommand and the
// timeout timer via that mutex.
func (h *TmuxHandler) HandleCancel(ctx context.Context, params json.RawMessage) (any, error) {
	var req CancelRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.CommandID == "" {
		return nil, fmt.Errorf("command_id is required")
	}

	// Snapshot the map so we can iterate without holding queuesMu during any
	// subsequent per-queue calls (each of which acquires q.mu internally).
	type queueEntry struct {
		session string
		queue   *SessionQueue
	}
	h.queuesMu.Lock()
	entries := make([]queueEntry, 0, len(h.queues))
	for session, queue := range h.queues {
		entries = append(entries, queueEntry{session: session, queue: queue})
	}
	h.queuesMu.Unlock()

	// Search for the command among the active slots.
	var foundSession string
	var foundCmd *QueuedCommand
	var foundQueue *SessionQueue
	for _, entry := range entries {
		if active := entry.queue.Active(); active != nil && active.ID == req.CommandID {
			foundSession = entry.session
			foundCmd = active
			foundQueue = entry.queue
			break
		}
	}

	if foundCmd != nil {
		// Active command — capture current output BEFORE taking cmd.mu so
		// the mutex is never held during tmux I/O.
		output, _ := ttmux.CapturePane(foundSession+":0.0", 500)

		var (
			shortCut bool
			notify   bool
			body     string
		)
		foundCmd.mu.Lock()
		if isTerminalState(foundCmd.State) {
			// Another path won the race.
			shortCut = true
		} else {
			foundCmd.State = StateCancelled
			foundCmd.CompletedAt = time.Now().UTC()
			foundCmd.CapturedOutput = output
			if foundCmd.timer != nil {
				foundCmd.timer.Stop()
			}

			h.state.Lock()
			_ = updateCommandState(ctx, h.state.DB(), foundCmd)
			h.state.Unlock()

			notify = foundCmd.NotifyOnComplete
			if notify {
				body = fmt.Sprintf("Command %s cancelled.\nSession: %s\n\nPartial output:\n---\n%s\n---",
					foundCmd.ID, foundSession, output)
			}
		}
		foundCmd.mu.Unlock()

		if shortCut {
			// Report the current terminal state rather than pretending we
			// cancelled.
			return &CancelResponse{
				CommandID: foundCmd.ID,
				State:     foundCmd.stateSnapshot(),
				Output:    output,
			}, nil
		}

		foundQueue.ClearActive()

		// Notify requester unless they opted out (queue-wait returns on any
		// terminal state including CANCELLED, so --wait callers get the
		// result via the RPC response and the @system message is redundant).
		if notify {
			h.sendSystemMessage(ctx, foundCmd.RequesterAgent, body)
		}

		// Send next queued command if any.
		if next := foundQueue.Peek(); next != nil {
			h.sendQueuedCommand(ctx, foundSession, foundQueue, next)
		} else {
			_ = ttmux.SetMonitorSilence(foundSession, 60, h.thrumBin(), h.thrumDir)
		}

		return &CancelResponse{
			CommandID: foundCmd.ID,
			State:     StateCancelled,
			Output:    output,
		}, nil
	}

	// Not active — fall back to DB for queued commands.
	loaded, err := loadCommand(ctx, h.state.DB(), req.CommandID)
	if err != nil {
		return nil, fmt.Errorf("command not found: %s", req.CommandID)
	}
	// loaded is a fresh struct with zero-value mutex, so no concurrent access
	// is possible — no need to acquire loaded.mu.
	loaded.State = StateCancelled
	loaded.CompletedAt = time.Now().UTC()

	h.state.Lock()
	_ = updateCommandState(ctx, h.state.DB(), loaded)
	h.state.Unlock()

	// Remove from in-memory queue if present. Iterate the pre-snapshotted
	// slice so we still don't hold queuesMu while calling queue methods.
	for _, entry := range entries {
		entry.queue.RemoveByID(req.CommandID)
	}

	return &CancelResponse{CommandID: loaded.ID, State: StateCancelled}, nil
}
