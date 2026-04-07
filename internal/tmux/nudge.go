package tmux

import (
	"fmt"
	"sync"
	"time"
)

const (
	defaultChunkSize = 512
	interChunkDelay  = 10 * time.Millisecond
	escToEnterDelay  = 600 * time.Millisecond
)

// sessionMutexes serializes nudges to the same session.
var (
	sessionMu    sync.Mutex
	sessionLocks = make(map[string]*sync.Mutex)
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

// Nudge sends a notification into a tmux session with safety measures.
// It acquires a per-session mutex to prevent interleaved keystrokes.
func Nudge(target, senderName string) error {
	session, _, _ := ParseTarget(target)
	mu := getSessionLock(session)
	mu.Lock()
	defer mu.Unlock()

	text := FormatNudge(senderName)
	chunks := ChunkText(text, defaultChunkSize)

	for i, chunk := range chunks {
		if err := SendKeys(target, chunk); err != nil {
			return fmt.Errorf("nudge send-keys chunk %d: %w", i, err)
		}
		if i < len(chunks)-1 {
			time.Sleep(interChunkDelay)
		}
	}

	// ESC to exit any mode (vim INSERT, copy mode)
	if err := SendSpecialKey(target, "Escape"); err != nil {
		return fmt.Errorf("nudge escape: %w", err)
	}

	// Pause to let ESC register (exceeds readline keyseq-timeout of 500ms)
	time.Sleep(escToEnterDelay)

	// Enter to submit
	if err := SendSpecialKey(target, "Enter"); err != nil {
		return fmt.Errorf("nudge enter: %w", err)
	}

	return nil
}
