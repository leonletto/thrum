package tmux

import (
	"fmt"
	"sync"
	"time"
)

const (
	defaultChunkSize    = 512
	interChunkDelay     = 10 * time.Millisecond
	escToEnterDelay     = 600 * time.Millisecond
	safeNudgeSettleDelay = 50 * time.Millisecond
)

// sessionMutexes serializes nudges to the same session.
var (
	sessionMu    sync.Mutex
	sessionLocks = make(map[string]*sync.Mutex)
)

// Package-private indirection so tests can substitute fake senders.
var (
	nudgeSendKeys       = SendKeys
	nudgeSendSpecialKey = SendSpecialKey
)

func getSessionLock(session string) *sync.Mutex {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if mu, ok := sessionLocks[session]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	sessionLocks[session] = mu
	return mu
}

// FormatNudge returns the notification text for a new message.
func FormatNudge(senderName string) string {
	return fmt.Sprintf(
		"New message from @%s -- run `thrum inbox --unread` to read",
		senderName,
	)
}

// ChunkText splits text into chunks of at most maxSize bytes.
func ChunkText(text string, maxSize int) []string {
	if len(text) <= maxSize {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		end := min(maxSize, len(text))
		chunks = append(chunks, text[:end])
		text = text[end:]
	}
	return chunks
}

// sendNudgeText sends the formatted nudge text chunks to the target, acquiring
// the per-session mutex. It returns the session lock held; the caller is
// responsible for unlocking it.
func sendNudgeText(target, senderName string) (*sync.Mutex, error) {
	session, _, _ := ParseTarget(target)
	mu := getSessionLock(session)
	mu.Lock()

	text := FormatNudge(senderName)
	chunks := ChunkText(text, defaultChunkSize)

	for i, chunk := range chunks {
		if err := nudgeSendKeys(target, chunk); err != nil {
			mu.Unlock()
			return nil, fmt.Errorf("nudge send-keys chunk %d: %w", i, err)
		}
		if i < len(chunks)-1 {
			time.Sleep(interChunkDelay)
		}
	}
	return mu, nil
}

// Nudge sends a notification into a tmux session safely — without sending
// Escape. This means in-progress work (running sub-agents, mid-generation
// responses) is not interrupted; the nudge text is queued as the next turn
// instead, matching human-typing-queue semantics.
//
// Use this for all normal message-delivery nudges.
func Nudge(target, senderName string) error {
	mu, err := sendNudgeText(target, senderName)
	if err != nil {
		return err
	}
	defer mu.Unlock()

	// Brief settle — enough for tmux to flush the buffer, without the 600ms
	// that was only needed to let readline's keyseq-timeout expire after Escape.
	time.Sleep(safeNudgeSettleDelay)

	// Enter to submit
	if err := nudgeSendSpecialKey(target, "Enter"); err != nil {
		return fmt.Errorf("nudge enter: %w", err)
	}

	return nil
}

// InterruptNudge sends a notification that intentionally interrupts any
// in-progress work in the target tmux session. It sends Escape before Enter,
// which exits vim INSERT / copy mode and cancels mid-generation output.
//
// Use this ONLY for restart flows where interruption is the explicit intent.
// For normal message delivery, use Nudge.
func InterruptNudge(target, senderName string) error {
	mu, err := sendNudgeText(target, senderName)
	if err != nil {
		return err
	}
	defer mu.Unlock()

	// ESC to exit any mode (vim INSERT, copy mode) and cancel in-progress work.
	if err := nudgeSendSpecialKey(target, "Escape"); err != nil {
		return fmt.Errorf("interrupt nudge escape: %w", err)
	}

	// Pause to let ESC register (exceeds readline keyseq-timeout of 500ms).
	time.Sleep(escToEnterDelay)

	// Enter to submit
	if err := nudgeSendSpecialKey(target, "Enter"); err != nil {
		return fmt.Errorf("interrupt nudge enter: %w", err)
	}

	return nil
}
