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
)

// QueueRequest is the tmux.queue RPC request.
type QueueRequest struct {
	Session   string `json:"session"`
	Text      string `json:"text"`
	TimeoutMs int64  `json:"timeout_ms"`
	Requester string `json:"requester"`
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

	cmd := &QueuedCommand{
		ID:             generateCommandID(),
		Text:           req.Text,
		RequesterAgent: req.Requester,
		Timeout:        time.Duration(req.TimeoutMs) * time.Millisecond,
		State:          StateQueued,
		SubmittedAt:    time.Now().UTC(),
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

	// Deliver result as @system message
	elapsed := cmd.CompletedAt.Sub(cmd.SentAt)
	msgBody := fmt.Sprintf("Command %s completed.\nSession: %s\nElapsed: %ds\n\nOutput:\n---\n%s\n---",
		cmd.ID, session, int(elapsed.Seconds()), output)
	h.sendSystemMessage(ctx, cmd.RequesterAgent, msgBody)

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

	// Switch monitor-silence to 5s for tight completion detection
	bin := h.thrumBin()
	if err := ttmux.SetMonitorSilence(session, 5, bin, h.thrumDir); err != nil {
		log.Printf("[queue] SetMonitorSilence(5) failed for %s: %v", session, err)
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
// TODO Task 2.3: flesh out full delivery.
func (h *TmuxHandler) sendSystemMessage(ctx context.Context, recipient, body string) {
	maxLen := len(body)
	if maxLen > 100 {
		maxLen = 100
	}
	log.Printf("[queue] would send @system message to %s: %s", recipient, body[:maxLen])
}

// handleCommandTimeout transitions a command to timeout_waiting and notifies requester.
// TODO Task 2.3: flesh out full handling.
func (h *TmuxHandler) handleCommandTimeout(ctx context.Context, session string, cmd *QueuedCommand) {
	log.Printf("[queue] timeout fired for %s (session %s)", cmd.ID, session)
}
