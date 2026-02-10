package projection

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/leonletto/thrum/internal/jsonl"
	"github.com/leonletto/thrum/internal/types"
)

// Projector replays JSONL events into SQLite.
type Projector struct {
	db *sql.DB
}

// NewProjector creates a new projector for the given database.
func NewProjector(db *sql.DB) *Projector {
	return &Projector{db: db}
}

// Apply applies a single event to the database.
func (p *Projector) Apply(event json.RawMessage) error {
	// Parse base event to get type
	var base types.BaseEvent
	if err := json.Unmarshal(event, &base); err != nil {
		return fmt.Errorf("unmarshal base event: %w", err)
	}

	switch base.Type {
	case "message.create":
		return p.applyMessageCreate(event)
	case "message.edit":
		return p.applyMessageEdit(event)
	case "message.delete":
		return p.applyMessageDelete(event)
	case "thread.create":
		return p.applyThreadCreate(event)
	case "agent.register":
		return p.applyAgentRegister(event)
	case "agent.session.start":
		return p.applySessionStart(event)
	case "agent.session.end":
		return p.applySessionEnd(event)
	case "agent.update":
		return p.applyAgentUpdate(event)
	default:
		// Unknown event types are ignored (forward compatibility)
		return nil
	}
}

// eventWithOrder holds an event and its sorting keys.
type eventWithOrder struct {
	event     json.RawMessage
	timestamp string
	eventID   string
}

// Rebuild rebuilds the database by replaying all events from multiple JSONL files.
// It reads from events.jsonl and messages/*.jsonl in the sync worktree directory,
// sorts all events globally by (timestamp, event_id), and applies them in order.
func (p *Projector) Rebuild(syncDir string) error {
	var allEvents []eventWithOrder

	// Read events.jsonl (core events: agent lifecycle, threads)
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); err == nil {
		events, err := readEventsFromFile(eventsPath)
		if err != nil {
			return fmt.Errorf("read events.jsonl: %w", err)
		}
		allEvents = append(allEvents, events...)
	}

	// Read messages/*.jsonl (per-agent message files)
	messagesPattern := filepath.Join(syncDir, "messages", "*.jsonl")
	messageFiles, err := filepath.Glob(messagesPattern)
	if err != nil {
		return fmt.Errorf("glob message files: %w", err)
	}

	for _, filePath := range messageFiles {
		events, err := readEventsFromFile(filePath)
		if err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(filePath), err)
		}
		allEvents = append(allEvents, events...)
	}

	// Sort by (timestamp, event_id) for deterministic ordering
	// ULIDs in event_id make this globally consistent across files
	sort.Slice(allEvents, func(i, j int) bool {
		if allEvents[i].timestamp != allEvents[j].timestamp {
			return allEvents[i].timestamp < allEvents[j].timestamp
		}
		return allEvents[i].eventID < allEvents[j].eventID
	})

	// Apply events in sorted order
	for _, e := range allEvents {
		if err := p.Apply(e.event); err != nil {
			return fmt.Errorf("apply event: %w", err)
		}
	}

	return nil
}

// readEventsFromFile reads all events from a JSONL file and returns them with sorting keys.
func readEventsFromFile(path string) ([]eventWithOrder, error) {
	reader, err := jsonl.NewReader(path)
	if err != nil {
		return nil, err
	}

	messages, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	events := make([]eventWithOrder, 0, len(messages))
	for _, msg := range messages {
		// Parse timestamp and event_id for sorting
		var base types.BaseEvent
		if err := json.Unmarshal(msg, &base); err != nil {
			// Skip unparseable events (forward compatibility)
			continue
		}

		events = append(events, eventWithOrder{
			event:     msg,
			timestamp: base.Timestamp,
			eventID:   base.EventID,
		})
	}

	return events, nil
}

func (p *Projector) applyMessageCreate(data json.RawMessage) error {
	var event types.MessageCreateEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal message.create: %w", err)
	}

	tx, err := p.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Insert message
	_, err = tx.Exec(`
		INSERT INTO messages (
			message_id, thread_id, agent_id, session_id, created_at,
			body_format, body_content, body_structured, authored_by, disclosed
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		event.MessageID,
		sqlNullString(event.ThreadID),
		event.AgentID,
		event.SessionID,
		event.Timestamp,
		event.Body.Format,
		event.Body.Content,
		sqlNullString(event.Body.Structured),
		sqlNullString(event.AuthoredBy),
		boolToInt(event.Disclosed),
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}

	// Insert scopes
	for _, scope := range event.Scopes {
		_, err = tx.Exec(`
			INSERT INTO message_scopes (message_id, scope_type, scope_value)
			VALUES (?, ?, ?)
		`, event.MessageID, scope.Type, scope.Value)
		if err != nil {
			return fmt.Errorf("insert scope: %w", err)
		}
	}

	// Insert refs
	for _, ref := range event.Refs {
		_, err = tx.Exec(`
			INSERT INTO message_refs (message_id, ref_type, ref_value)
			VALUES (?, ?, ?)
		`, event.MessageID, ref.Type, ref.Value)
		if err != nil {
			return fmt.Errorf("insert ref: %w", err)
		}
	}

	return tx.Commit()
}

func (p *Projector) applyMessageEdit(data json.RawMessage) error {
	var event types.MessageEditEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal message.edit: %w", err)
	}

	tx, err := p.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Query current content and session_id before updating
	var oldContent string
	var oldStructured sql.NullString
	var sessionID string
	query := `SELECT body_content, body_structured, session_id FROM messages WHERE message_id = ?`
	err = tx.QueryRow(query, event.MessageID).Scan(&oldContent, &oldStructured, &sessionID)
	if err != nil {
		return fmt.Errorf("query message: %w", err)
	}

	// Insert edit history record
	_, err = tx.Exec(`
		INSERT INTO message_edits (
			message_id, edited_at, edited_by,
			old_content, new_content,
			old_structured, new_structured
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		event.MessageID,
		event.Timestamp,
		sessionID,
		sqlNullString(oldContent),
		sqlNullString(event.Body.Content),
		oldStructured,
		sqlNullString(event.Body.Structured),
	)
	if err != nil {
		return fmt.Errorf("insert edit history: %w", err)
	}

	// Update message content
	_, err = tx.Exec(`
		UPDATE messages
		SET body_content = ?, body_structured = ?, updated_at = ?
		WHERE message_id = ?
	`,
		event.Body.Content,
		sqlNullString(event.Body.Structured),
		event.Timestamp,
		event.MessageID,
	)
	if err != nil {
		return fmt.Errorf("update message: %w", err)
	}

	return tx.Commit()
}

func (p *Projector) applyMessageDelete(data json.RawMessage) error {
	var event types.MessageDeleteEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal message.delete: %w", err)
	}

	_, err := p.db.Exec(`
		UPDATE messages
		SET deleted = 1, deleted_at = ?, delete_reason = ?
		WHERE message_id = ?
	`,
		event.Timestamp,
		sqlNullString(event.Reason),
		event.MessageID,
	)
	if err != nil {
		return fmt.Errorf("delete message: %w", err)
	}

	return nil
}

func (p *Projector) applyThreadCreate(data json.RawMessage) error {
	var event types.ThreadCreateEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal thread.create: %w", err)
	}

	_, err := p.db.Exec(`
		INSERT INTO threads (thread_id, title, created_at, created_by)
		VALUES (?, ?, ?, ?)
	`,
		event.ThreadID,
		event.Title,
		event.Timestamp,
		event.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("insert thread: %w", err)
	}

	return nil
}

func (p *Projector) applyAgentRegister(data json.RawMessage) error {
	var event types.AgentRegisterEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal agent.register: %w", err)
	}

	_, err := p.db.Exec(`
		INSERT OR REPLACE INTO agents (agent_id, kind, role, module, display, registered_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		event.AgentID,
		event.Kind,
		event.Role,
		event.Module,
		sqlNullString(event.Display),
		event.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("insert agent: %w", err)
	}

	return nil
}

func (p *Projector) applySessionStart(data json.RawMessage) error {
	var event types.AgentSessionStartEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal agent.session.start: %w", err)
	}

	_, err := p.db.Exec(`
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES (?, ?, ?, ?)
	`,
		event.SessionID,
		event.AgentID,
		event.Timestamp,
		event.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}

	return nil
}

func (p *Projector) applySessionEnd(data json.RawMessage) error {
	var event types.AgentSessionEndEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal agent.session.end: %w", err)
	}

	_, err := p.db.Exec(`
		UPDATE sessions
		SET ended_at = ?, end_reason = ?
		WHERE session_id = ?
	`,
		event.Timestamp,
		sqlNullString(event.Reason),
		event.SessionID,
	)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}

	return nil
}

func (p *Projector) applyAgentUpdate(data json.RawMessage) error {
	var event types.AgentUpdateEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal agent.update: %w", err)
	}

	// Get existing work contexts for this agent
	existing, err := p.getWorkContexts(event.AgentID)
	if err != nil {
		return fmt.Errorf("get existing contexts: %w", err)
	}

	// Merge incoming with existing
	merged := mergeWorkContexts(existing, event.WorkContexts)

	// Update database with merged result
	if err := p.setWorkContexts(event.AgentID, merged); err != nil {
		return fmt.Errorf("set work contexts: %w", err)
	}

	return nil
}

// getWorkContexts retrieves all work contexts for an agent from the database.
func (p *Projector) getWorkContexts(agentID string) ([]types.SessionWorkContext, error) {
	query := `SELECT session_id, branch, worktree_path,
	                 unmerged_commits, uncommitted_files, changed_files, git_updated_at,
	                 current_task, task_updated_at, intent, intent_updated_at
	          FROM agent_work_contexts
	          WHERE agent_id = ?`

	rows, err := p.db.Query(query, agentID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var contexts []types.SessionWorkContext

	for rows.Next() {
		var ctx types.SessionWorkContext
		var branch, worktreePath, unmergedCommitsJSON, uncommittedFilesJSON, changedFilesJSON, gitUpdatedAt sql.NullString
		var currentTask, taskUpdatedAt, intent, intentUpdatedAt sql.NullString

		err := rows.Scan(
			&ctx.SessionID,
			&branch,
			&worktreePath,
			&unmergedCommitsJSON,
			&uncommittedFilesJSON,
			&changedFilesJSON,
			&gitUpdatedAt,
			&currentTask,
			&taskUpdatedAt,
			&intent,
			&intentUpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		// Set optional fields
		if branch.Valid {
			ctx.Branch = branch.String
		}
		if worktreePath.Valid {
			ctx.WorktreePath = worktreePath.String
		}
		if gitUpdatedAt.Valid {
			ctx.GitUpdatedAt = gitUpdatedAt.String
		}
		if currentTask.Valid {
			ctx.CurrentTask = currentTask.String
		}
		if taskUpdatedAt.Valid {
			ctx.TaskUpdatedAt = taskUpdatedAt.String
		}
		if intent.Valid {
			ctx.Intent = intent.String
		}
		if intentUpdatedAt.Valid {
			ctx.IntentUpdatedAt = intentUpdatedAt.String
		}

		// Unmarshal JSON fields
		if unmergedCommitsJSON.Valid && unmergedCommitsJSON.String != "" {
			var commits []types.CommitSummary
			if err := json.Unmarshal([]byte(unmergedCommitsJSON.String), &commits); err == nil {
				ctx.UnmergedCommits = commits
			}
		}

		if uncommittedFilesJSON.Valid && uncommittedFilesJSON.String != "" {
			var files []string
			if err := json.Unmarshal([]byte(uncommittedFilesJSON.String), &files); err == nil {
				ctx.UncommittedFiles = files
			}
		}

		if changedFilesJSON.Valid && changedFilesJSON.String != "" {
			var files []string
			if err := json.Unmarshal([]byte(changedFilesJSON.String), &files); err == nil {
				ctx.ChangedFiles = files
			}
		}

		contexts = append(contexts, ctx)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return contexts, nil
}

// setWorkContexts replaces all work contexts for an agent in the database.
func (p *Projector) setWorkContexts(agentID string, contexts []types.SessionWorkContext) error {
	tx, err := p.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Delete all existing contexts for this agent
	_, err = tx.Exec("DELETE FROM agent_work_contexts WHERE agent_id = ?", agentID)
	if err != nil {
		return fmt.Errorf("delete existing contexts: %w", err)
	}

	// Insert new contexts
	for _, ctx := range contexts {
		// Marshal JSON fields
		unmergedCommitsJSON, _ := json.Marshal(ctx.UnmergedCommits)
		uncommittedFilesJSON, _ := json.Marshal(ctx.UncommittedFiles)
		changedFilesJSON, _ := json.Marshal(ctx.ChangedFiles)

		_, err = tx.Exec(`
			INSERT INTO agent_work_contexts (
				session_id, agent_id, branch, worktree_path,
				unmerged_commits, uncommitted_files, changed_files, git_updated_at,
				current_task, task_updated_at, intent, intent_updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			ctx.SessionID,
			agentID,
			sqlNullString(ctx.Branch),
			sqlNullString(ctx.WorktreePath),
			string(unmergedCommitsJSON),
			string(uncommittedFilesJSON),
			string(changedFilesJSON),
			sqlNullString(ctx.GitUpdatedAt),
			sqlNullString(ctx.CurrentTask),
			sqlNullString(ctx.TaskUpdatedAt),
			sqlNullString(ctx.Intent),
			sqlNullString(ctx.IntentUpdatedAt),
		)
		if err != nil {
			return fmt.Errorf("insert context: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// mergeWorkContexts merges two slices of work contexts by session_id.
// For contexts with the same session_id, keeps the one with newer git_updated_at.
func mergeWorkContexts(a, b []types.SessionWorkContext) []types.SessionWorkContext {
	bySession := make(map[string]types.SessionWorkContext)

	// Add all from a
	for _, ctx := range a {
		bySession[ctx.SessionID] = ctx
	}

	// Merge in from b
	for _, ctx := range b {
		if existing, ok := bySession[ctx.SessionID]; ok {
			// Both have this session - keep newer by git_updated_at
			if ctx.GitUpdatedAt > existing.GitUpdatedAt {
				bySession[ctx.SessionID] = ctx
			}
			// else keep existing (it's newer or same)
		} else {
			// Only in b, add it
			bySession[ctx.SessionID] = ctx
		}
	}

	// Convert map to slice
	result := make([]types.SessionWorkContext, 0, len(bySession))
	for _, ctx := range bySession {
		result = append(result, ctx)
	}

	return result
}

// sqlNullString returns a sql.NullString for optional string fields.
func sqlNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// boolToInt converts a boolean to an integer (0 or 1) for SQLite.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
