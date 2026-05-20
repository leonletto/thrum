package rpc

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
)

// Queue state constants.
//
// Note: the spec describes a QUEUED → WAITING → SENT transition where WAITING
// means "front of queue, waiting for pane silence before being typed". The
// implementation elides the explicit WAITING marker: HandleCheckPane's
// dispatch checks for an active command first, and only if none exists does
// it peek the front of the queue and send it. Commands therefore go directly
// from QUEUED to SENT when a silence event fires on an idle pane. The
// functional semantics match the spec; we just don't persist the intermediate
// state. No loader or filter references 'waiting', so there is no constant
// for it here.
const (
	StateQueued         = "queued"
	StateSent           = "sent"
	StateActive         = "active"
	StateCompleted      = "completed"
	StateTimeoutWaiting = "timeout_waiting"
	StateCancelled      = "canceled"
	StateInterrupted    = "interrupted"
)

// QueuedCommand represents a single command in a session's queue.
//
// Concurrency: immutable fields (ID, Text, RequesterAgent, Timeout, SilenceMs,
// NotifyOnComplete, SubmittedAt) are set once in HandleQueue and never mutated,
// so they are safe to read without synchronization. Mutable fields
// (State, SentAt, CompletedAt, CapturedOutput, timer) are protected by mu and
// MUST only be read or written while holding it. The three transition paths
// (completeCommand, HandleCancel, handleCommandTimeout) can race — e.g. the
// timeout timer callback may fire at the same instant HandleCheckPane detects
// silence — so the mutex is also used to enforce single-entry to terminal
// transitions via the isTerminal precondition check at the top of each path.
type QueuedCommand struct {
	// Immutable after construction.
	ID               string
	Text             string
	RequesterAgent   string
	Timeout          time.Duration
	SilenceMs        int64 // per-command silence threshold; default 5000
	NotifyOnComplete bool  // if false, skip @system completion message (used by --wait mode)
	SubmittedAt      time.Time

	// Protected by mu.
	mu             sync.Mutex
	State          string
	SentAt         time.Time
	CompletedAt    time.Time
	CapturedOutput string
	timer          *time.Timer // timeout goroutine handle

	// dispatchClaimed is a one-shot atomic guard that ensures sendQueuedCommand
	// is invoked at most once per command. Set via CompareAndSwap at the top
	// of sendQueuedCommand. Closes a dispatch race between (1) HandleQueue's
	// bootstrap IsSilent=true fast path, (2) HandleCheckPane's alert-silence
	// hook firing, (3) completeCommand's next-after-completion continuation,
	// and (4) the thrum-7yhs dispatch watchdog (pollDispatchSilence). Any two
	// of these can fire roughly simultaneously; without this guard both could
	// type the command into the pane.
	dispatchClaimed atomic.Bool

	// Written once by loadPendingCommands during restart recovery; read-only
	// thereafter.
	sessionName string
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

// stateSnapshot returns the current State of the command, acquiring cmd.mu
// for a consistent read. Use this when reading State outside a path that
// already holds cmd.mu (e.g. after losing a transition race).
func (cmd *QueuedCommand) stateSnapshot() string {
	cmd.mu.Lock()
	defer cmd.mu.Unlock()
	return cmd.State
}

// QueuedCommandView is a point-in-time snapshot of a QueuedCommand, safe to
// return across RPC boundaries and to marshal as JSON without acquiring any
// lock. Populated by (*QueuedCommand).view() under cmd.mu so the mutable
// fields (State, SentAt, CompletedAt, CapturedOutput) are internally
// consistent.
//
// JSON shape matches the original *QueuedCommand marshaling so the
// QueueStatusResponse over the wire stays unchanged.
type QueuedCommandView struct {
	ID               string        `json:"id"`
	Text             string        `json:"text"`
	RequesterAgent   string        `json:"requester_agent"`
	Timeout          time.Duration `json:"timeout_ns,omitempty"`
	SilenceMs        int64         `json:"silence_ms,omitempty"`
	NotifyOnComplete bool          `json:"notify_on_complete"`
	State            string        `json:"state"`
	SubmittedAt      time.Time     `json:"submitted_at"`
	SentAt           time.Time     `json:"sent_at,omitempty"`
	CompletedAt      time.Time     `json:"completed_at,omitempty"`
	CapturedOutput   string        `json:"captured_output,omitempty"`
	SessionName      string        `json:"session_name,omitempty"`
}

// view returns an immutable copy of the command's fields, taken under cmd.mu.
// Safe to pass to JSON encoders or return across goroutines.
func (cmd *QueuedCommand) view() QueuedCommandView {
	cmd.mu.Lock()
	defer cmd.mu.Unlock()
	return QueuedCommandView{
		// Immutable fields — OK to read without the lock, but including
		// them here keeps the copy atomic from a reader's perspective.
		ID:               cmd.ID,
		Text:             cmd.Text,
		RequesterAgent:   cmd.RequesterAgent,
		Timeout:          cmd.Timeout,
		SilenceMs:        cmd.SilenceMs,
		NotifyOnComplete: cmd.NotifyOnComplete,
		SubmittedAt:      cmd.SubmittedAt,
		SessionName:      cmd.sessionName,
		// Protected by cmd.mu.
		State:          cmd.State,
		SentAt:         cmd.SentAt,
		CompletedAt:    cmd.CompletedAt,
		CapturedOutput: cmd.CapturedOutput,
	}
}

// StatusSnapshot returns a race-free view of the queue's active command and
// pending commands. Lock ordering: acquire q.mu first, then acquire cmd.mu
// on each command in turn (active and queued). This matches the ordering
// used everywhere else in the file (q.mu → cmd.mu is unidirectional — no
// mutation path takes cmd.mu then q.mu).
func (q *SessionQueue) StatusSnapshot() (active *QueuedCommandView, queued []QueuedCommandView) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.active != nil {
		v := q.active.view()
		active = &v
	}
	if len(q.commands) > 0 {
		queued = make([]QueuedCommandView, 0, len(q.commands))
		for _, c := range q.commands {
			queued = append(queued, c.view())
		}
	}
	return active, queued
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

// RemoveByID removes the first queued command matching id and returns it, or nil if not found.
func (q *SessionQueue) RemoveByID(id string) *QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, cmd := range q.commands {
		if cmd.ID == id {
			q.commands = append(q.commands[:i], q.commands[i+1:]...)
			return cmd
		}
	}
	return nil
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
	notify := 0
	if cmd.NotifyOnComplete {
		notify = 1
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO command_queue
		 (command_id, session_name, requester_agent, command_text, state,
		  timeout_ms, silence_ms, notify_on_complete, submitted_at, position)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cmd.ID, session, cmd.RequesterAgent, cmd.Text, cmd.State,
		cmd.Timeout.Milliseconds(), cmd.SilenceMs, notify,
		cmd.SubmittedAt.UTC().Format(time.RFC3339Nano), position,
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
		        silence_ms, notify_on_complete, submitted_at, sent_at, completed_at, captured_output
		 FROM command_queue WHERE command_id = ?`,
		commandID,
	)

	var cmd QueuedCommand
	var timeoutMs int64
	var notifyOnComplete int
	var submittedAt string
	var sentAt, completedAt, capturedOutput sql.NullString

	if err := row.Scan(&cmd.ID, &cmd.Text, &cmd.RequesterAgent, &cmd.State,
		&timeoutMs, &cmd.SilenceMs, &notifyOnComplete, &submittedAt,
		&sentAt, &completedAt, &capturedOutput); err != nil {
		return nil, err
	}

	cmd.Timeout = time.Duration(timeoutMs) * time.Millisecond
	cmd.NotifyOnComplete = notifyOnComplete != 0
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

// loadPendingCommands reads all non-terminal commands across all sessions,
// ordered by session_name then position. Each returned command has its
// sessionName field populated so callers can route commands to the correct
// SessionQueue without an additional lookup.
//
// Used by RecoverQueueState for both recovery phases: the interrupt phase
// (marking sent/active/timeout_waiting commands as interrupted) and the reload
// phase (rebuilding in-memory queues from queued rows). Not restricted to
// either phase alone.
//
// Note: the 'waiting' state from the spec is not assigned by any production
// code path, so it is not included in the filter (see state-constant comment
// above for rationale).
func loadPendingCommands(ctx context.Context, db *safedb.DB) ([]*QueuedCommand, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT command_id, session_name, command_text, requester_agent, state, timeout_ms,
		        silence_ms, notify_on_complete, submitted_at
		 FROM command_queue
		 WHERE state IN ('queued', 'sent', 'active', 'timeout_waiting')
		 ORDER BY session_name, position ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var cmds []*QueuedCommand
	for rows.Next() {
		var cmd QueuedCommand
		var sessionName string
		var timeoutMs int64
		var notifyOnComplete int
		var submittedAt string
		if err := rows.Scan(&cmd.ID, &sessionName, &cmd.Text, &cmd.RequesterAgent, &cmd.State,
			&timeoutMs, &cmd.SilenceMs, &notifyOnComplete, &submittedAt); err != nil {
			return nil, err
		}
		cmd.Timeout = time.Duration(timeoutMs) * time.Millisecond
		cmd.NotifyOnComplete = notifyOnComplete != 0
		cmd.SubmittedAt, _ = time.Parse(time.RFC3339Nano, submittedAt)
		cmd.sessionName = sessionName
		cmds = append(cmds, &cmd)
	}
	return cmds, rows.Err()
}
