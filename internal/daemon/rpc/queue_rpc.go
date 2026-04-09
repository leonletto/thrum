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

	queue := h.getOrCreateQueue(req.Session)
	position := queue.Len() + 1

	// Persist to DB first — if this fails, do not enqueue in-memory.
	h.state.Lock()
	err := persistCommand(ctx, h.state.DB(), req.Session, cmd, position)
	h.state.Unlock()
	if err != nil {
		return nil, fmt.Errorf("persist command: %w", err)
	}

	queue.Enqueue(cmd)

	return &QueueResponse{
		CommandID: cmd.ID,
		Position:  position,
	}, nil
}

// completeCommand captures pane output, delivers the result, and advances the queue.
func (h *TmuxHandler) completeCommand(ctx context.Context, session string, queue *SessionQueue, cmd *QueuedCommand) {
	// Capture last 500 lines of pane; tolerate failure (tmux may not be running in tests)
	output, err := ttmux.CapturePane(session+":0.0", 500)
	if err != nil {
		log.Printf("[queue] capture-pane failed for %s: %v", session, err)
		output = ""
	}

	cmd.State = StateCompleted
	cmd.CompletedAt = time.Now().UTC()
	cmd.CapturedOutput = output
	if cmd.timer != nil {
		cmd.timer.Stop()
	}

	h.state.Lock()
	_ = updateCommandState(ctx, h.state.DB(), cmd)
	h.state.Unlock()

	queue.ClearActive()

	// Deliver result as @system message unless the caller opted out (e.g. --wait
	// mode, where the result is returned via the queue-wait RPC response).
	if cmd.NotifyOnComplete {
		elapsed := cmd.CompletedAt.Sub(cmd.SentAt)
		msgBody := fmt.Sprintf("Command %s completed.\nSession: %s\nElapsed: %ds\n\nOutput:\n---\n%s\n---",
			cmd.ID, session, int(elapsed.Seconds()), output)
		h.sendSystemMessage(ctx, cmd.RequesterAgent, msgBody)
	}

	// Send the next queued command if any
	if next := queue.Peek(); next != nil {
		h.sendQueuedCommand(ctx, session, queue, next)
	} else {
		// Queue empty — restore 60s silence
		bin := h.thrumBin()
		if err := ttmux.SetMonitorSilence(session, 60, bin, h.thrumDir); err != nil {
			log.Printf("[queue] SetMonitorSilence(60) failed for %s: %v", session, err)
		}
	}
}

// sendQueuedCommand pops the head of the queue, types the command, and starts timeout tracking.
func (h *TmuxHandler) sendQueuedCommand(ctx context.Context, session string, queue *SessionQueue, cmd *QueuedCommand) {
	target := session + ":0.0"

	// Type the command and press Enter
	if err := ttmux.SendKeys(target, cmd.Text); err != nil {
		log.Printf("[queue] SendKeys failed: %v", err)
		return
	}
	if err := ttmux.SendSpecialKey(target, "Enter"); err != nil {
		log.Printf("[queue] SendSpecialKey failed: %v", err)
		return
	}

	cmd.State = StateSent
	cmd.SentAt = time.Now().UTC()

	h.state.Lock()
	_ = updateCommandState(ctx, h.state.DB(), cmd)
	h.state.Unlock()

	queue.Pop()
	queue.SetActive(cmd)

	// Switch monitor-silence to the command's configured silence threshold.
	// SetMonitorSilence takes seconds; round cmd.SilenceMs up so values below
	// 1000ms still produce a 1s timer. Defensive fallback to 5s if unset.
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

	// Start timeout goroutine
	cmd.timer = time.AfterFunc(cmd.Timeout, func() {
		h.handleCommandTimeout(context.Background(), session, cmd)
	})
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

	// Determine start time from initial load.
	startLoad, _ := loadCommand(ctx, h.state.DB(), req.CommandID)
	start := time.Now()
	if startLoad != nil && !startLoad.SubmittedAt.IsZero() {
		start = startLoad.SubmittedAt
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

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
				ElapsedMs: time.Since(start).Milliseconds(),
			}, nil
		}

		if time.Now().After(deadline) {
			return &QueueWaitResponse{
				CommandID: cmd.ID,
				State:     cmd.State,
				ElapsedMs: time.Since(start).Milliseconds(),
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
		SessionID: "",
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

// handleCommandTimeout transitions a command to timeout_waiting and notifies requester.
func (h *TmuxHandler) handleCommandTimeout(ctx context.Context, session string, cmd *QueuedCommand) {
	cmd.State = StateTimeoutWaiting

	h.state.Lock()
	_ = updateCommandState(ctx, h.state.DB(), cmd)
	h.state.Unlock()

	body := fmt.Sprintf("Command %s still processing after %ds.\nSession: %s\nSend \"thrum tmux cancel %s\" to abort.",
		cmd.ID, int(cmd.Timeout.Seconds()), session, cmd.ID)
	h.sendSystemMessage(ctx, cmd.RequesterAgent, body)
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
func (h *TmuxHandler) HandleCancel(ctx context.Context, params json.RawMessage) (any, error) {
	var req CancelRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.CommandID == "" {
		return nil, fmt.Errorf("command_id is required")
	}

	// Find the command as an active command across all queues.
	h.queuesMu.Lock()
	var foundSession string
	var foundCmd *QueuedCommand
	var foundQueue *SessionQueue
	for session, queue := range h.queues {
		if active := queue.Active(); active != nil && active.ID == req.CommandID {
			foundSession = session
			foundCmd = active
			foundQueue = queue
			break
		}
	}
	h.queuesMu.Unlock()

	if foundCmd != nil {
		// Active command — capture current output, stop timer, transition to cancelled.
		output, _ := ttmux.CapturePane(foundSession+":0.0", 500)
		foundCmd.State = StateCancelled
		foundCmd.CompletedAt = time.Now().UTC()
		foundCmd.CapturedOutput = output
		if foundCmd.timer != nil {
			foundCmd.timer.Stop()
		}

		h.state.Lock()
		_ = updateCommandState(ctx, h.state.DB(), foundCmd)
		h.state.Unlock()

		foundQueue.ClearActive()

		// Notify requester.
		body := fmt.Sprintf("Command %s cancelled.\nSession: %s\n\nPartial output:\n---\n%s\n---",
			foundCmd.ID, foundSession, output)
		h.sendSystemMessage(ctx, foundCmd.RequesterAgent, body)

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

	// Not active — fall back to DB for queued/waiting commands.
	loaded, err := loadCommand(ctx, h.state.DB(), req.CommandID)
	if err != nil {
		return nil, fmt.Errorf("command not found: %s", req.CommandID)
	}
	loaded.State = StateCancelled
	loaded.CompletedAt = time.Now().UTC()

	h.state.Lock()
	_ = updateCommandState(ctx, h.state.DB(), loaded)
	h.state.Unlock()

	// Remove from in-memory queue if present.
	h.queuesMu.Lock()
	for _, queue := range h.queues {
		queue.RemoveByID(req.CommandID)
	}
	h.queuesMu.Unlock()

	return &CancelResponse{CommandID: loaded.ID, State: StateCancelled}, nil
}
