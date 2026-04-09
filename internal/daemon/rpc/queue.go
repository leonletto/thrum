package rpc

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
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

	sessionName string      // populated by loadPendingCommands for restart recovery
	timer       *time.Timer // timeout goroutine handle
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

// persistCommand writes a new command row to the DB.
func persistCommand(ctx context.Context, db *safedb.DB, session string, cmd *QueuedCommand, position int) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO command_queue
		 (command_id, session_name, requester_agent, command_text, state, timeout_ms, submitted_at, position)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		cmd.ID, session, cmd.RequesterAgent, cmd.Text, cmd.State,
		cmd.Timeout.Milliseconds(), cmd.SubmittedAt.UTC().Format(time.RFC3339Nano), position,
	)
	return err
}

// updateCommandState updates state and optionally timestamps on an existing row.
func updateCommandState(ctx context.Context, db *safedb.DB, cmd *QueuedCommand) error {
	var sentAt, completedAt sql.NullString
	if !cmd.SentAt.IsZero() {
		sentAt = sql.NullString{String: cmd.SentAt.UTC().Format(time.RFC3339Nano), Valid: true}
	}
	if !cmd.CompletedAt.IsZero() {
		completedAt = sql.NullString{String: cmd.CompletedAt.UTC().Format(time.RFC3339Nano), Valid: true}
	}

	_, err := db.ExecContext(ctx,
		`UPDATE command_queue
		 SET state = ?, sent_at = ?, completed_at = ?, captured_output = ?
		 WHERE command_id = ?`,
		cmd.State, sentAt, completedAt, cmd.CapturedOutput, cmd.ID,
	)
	return err
}

// loadCommand reads a single command by ID.
func loadCommand(ctx context.Context, db *safedb.DB, commandID string) (*QueuedCommand, error) {
	row := db.QueryRowContext(ctx,
		`SELECT command_id, command_text, requester_agent, state, timeout_ms,
		        submitted_at, sent_at, completed_at, captured_output
		 FROM command_queue WHERE command_id = ?`,
		commandID,
	)

	var cmd QueuedCommand
	var timeoutMs int64
	var submittedAt string
	var sentAt, completedAt, capturedOutput sql.NullString

	if err := row.Scan(&cmd.ID, &cmd.Text, &cmd.RequesterAgent, &cmd.State,
		&timeoutMs, &submittedAt, &sentAt, &completedAt, &capturedOutput); err != nil {
		return nil, err
	}

	cmd.Timeout = time.Duration(timeoutMs) * time.Millisecond
	cmd.SubmittedAt, _ = time.Parse(time.RFC3339Nano, submittedAt)
	if sentAt.Valid {
		cmd.SentAt, _ = time.Parse(time.RFC3339Nano, sentAt.String)
	}
	if completedAt.Valid {
		cmd.CompletedAt, _ = time.Parse(time.RFC3339Nano, completedAt.String)
	}
	if capturedOutput.Valid {
		cmd.CapturedOutput = capturedOutput.String
	}
	return &cmd, nil
}

// loadPendingCommands reads all non-terminal commands across all sessions, ordered by position.
// Each returned command has its sessionName field populated for restart recovery.
func loadPendingCommands(ctx context.Context, db *safedb.DB) ([]*QueuedCommand, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT command_id, session_name, command_text, requester_agent, state, timeout_ms, submitted_at
		 FROM command_queue
		 WHERE state IN ('queued', 'waiting', 'sent', 'active', 'timeout_waiting')
		 ORDER BY session_name, position ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cmds []*QueuedCommand
	for rows.Next() {
		var cmd QueuedCommand
		var sessionName string
		var timeoutMs int64
		var submittedAt string
		if err := rows.Scan(&cmd.ID, &sessionName, &cmd.Text, &cmd.RequesterAgent, &cmd.State,
			&timeoutMs, &submittedAt); err != nil {
			return nil, err
		}
		cmd.Timeout = time.Duration(timeoutMs) * time.Millisecond
		cmd.SubmittedAt, _ = time.Parse(time.RFC3339Nano, submittedAt)
		cmd.sessionName = sessionName
		cmds = append(cmds, &cmd)
	}
	return cmds, rows.Err()
}
