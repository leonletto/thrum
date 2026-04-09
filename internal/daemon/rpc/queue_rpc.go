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
