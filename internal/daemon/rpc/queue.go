package rpc

import (
	"sync"
	"time"
)

// Queue state constants.
const (
	StateQueued         = "queued"
	StateWaiting        = "waiting"
	StateSent           = "sent"
	StateActive         = "active"
	StateCompleted      = "completed"
	StateTimeoutWaiting = "timeout_waiting"
	StateCancelled      = "cancelled"
	StateInterrupted    = "interrupted"
)

// QueuedCommand represents a single command in a session's queue.
type QueuedCommand struct {
	ID             string
	Text           string
	RequesterAgent string
	Timeout        time.Duration
	State          string
	SubmittedAt    time.Time
	SentAt         time.Time
	CompletedAt    time.Time
	CapturedOutput string

	timer *time.Timer // timeout goroutine handle
}

// SessionQueue manages a FIFO command queue for one tmux session.
type SessionQueue struct {
	Session  string
	commands []*QueuedCommand
	active   *QueuedCommand
	mu       sync.Mutex
}

// NewSessionQueue creates an empty queue for a session.
func NewSessionQueue(session string) *SessionQueue {
	return &SessionQueue{Session: session}
}

// Enqueue adds a command to the back of the queue.
func (q *SessionQueue) Enqueue(cmd *QueuedCommand) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.commands = append(q.commands, cmd)
}

// Peek returns the command at the head of the queue without removing it.
func (q *SessionQueue) Peek() *QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.commands) == 0 {
		return nil
	}
	return q.commands[0]
}

// Pop removes and returns the command at the head of the queue.
func (q *SessionQueue) Pop() *QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.commands) == 0 {
		return nil
	}
	head := q.commands[0]
	q.commands = q.commands[1:]
	return head
}

// Len returns the number of queued commands (not including active).
func (q *SessionQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.commands)
}

// Active returns the currently executing command, or nil.
func (q *SessionQueue) Active() *QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.active
}

// SetActive promotes a command to the active slot.
func (q *SessionQueue) SetActive(cmd *QueuedCommand) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.active = cmd
}

// ClearActive removes the active command.
func (q *SessionQueue) ClearActive() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.active = nil
}

// Snapshot returns a copy of the commands slice.
func (q *SessionQueue) Snapshot() []*QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.commands) == 0 {
		return nil
	}
	out := make([]*QueuedCommand, len(q.commands))
	copy(out, q.commands)
	return out
}
