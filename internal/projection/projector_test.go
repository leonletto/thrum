package projection_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/jsonl"
	"github.com/leonletto/thrum/internal/projection"
	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/internal/types"
)

func setupTestDB(t *testing.T) *sql.DB {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	return db
}

func TestProjector_ApplyMessageCreate(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	event := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2026-01-01T00:00:00Z",
		MessageID: "msg_001",
		ThreadID:  "thr_001",
		AgentID:   "agent:test:ABC",
		SessionID: "ses_001",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "Hello world",
		},
		Scopes: []types.Scope{
			{Type: "module", Value: "auth"},
		},
		Refs: []types.Ref{
			{Type: "spec", Value: "docs/spec.md"},
		},
	}

	data, _ := json.Marshal(event)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}

	// Verify message was inserted
	var messageID, content string
	err := db.QueryRow("SELECT message_id, body_content FROM messages WHERE message_id = ?", "msg_001").Scan(&messageID, &content)
	if err != nil {
		t.Fatalf("Query message failed: %v", err)
	}
	if content != "Hello world" {
		t.Errorf("Expected content 'Hello world', got '%s'", content)
	}

	// Verify scope was inserted
	var scopeValue string
	err = db.QueryRow("SELECT scope_value FROM message_scopes WHERE message_id = ? AND scope_type = ?", "msg_001", "module").Scan(&scopeValue)
	if err != nil {
		t.Fatalf("Query scope failed: %v", err)
	}
	if scopeValue != "auth" {
		t.Errorf("Expected scope_value 'auth', got '%s'", scopeValue)
	}

	// Verify ref was inserted
	var refValue string
	err = db.QueryRow("SELECT ref_value FROM message_refs WHERE message_id = ? AND ref_type = ?", "msg_001", "spec").Scan(&refValue)
	if err != nil {
		t.Fatalf("Query ref failed: %v", err)
	}
	if refValue != "docs/spec.md" {
		t.Errorf("Expected ref_value 'docs/spec.md', got '%s'", refValue)
	}
}

func TestProjector_ApplyMessageEdit(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	// Create a message first
	createEvent := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2026-01-01T00:00:00Z",
		MessageID: "msg_002",
		AgentID:   "agent:test:ABC",
		SessionID: "ses_001",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "Original content",
		},
	}
	createData, _ := json.Marshal(createEvent)
	if err := p.Apply(context.Background(), createData); err != nil {
		t.Fatalf("apply create: %v", err)
	}

	// Now edit it
	editEvent := types.MessageEditEvent{
		Type:      "message.edit",
		Timestamp: "2026-01-01T01:00:00Z",
		MessageID: "msg_002",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "Updated content",
		},
	}
	editData, _ := json.Marshal(editEvent)
	if err := p.Apply(context.Background(), editData); err != nil {
		t.Fatalf("Apply() edit failed: %v", err)
	}

	// Verify message was updated
	var content, updatedAt string
	err := db.QueryRow("SELECT body_content, updated_at FROM messages WHERE message_id = ?", "msg_002").Scan(&content, &updatedAt)
	if err != nil {
		t.Fatalf("Query message failed: %v", err)
	}
	if content != "Updated content" {
		t.Errorf("Expected updated content 'Updated content', got '%s'", content)
	}
	if updatedAt != "2026-01-01T01:00:00Z" {
		t.Errorf("Expected updated_at '2026-01-01T01:00:00Z', got '%s'", updatedAt)
	}

	// Verify edit history was recorded
	var editID int
	var oldContent, newContent sql.NullString
	var editedAt, editedBy string
	err = db.QueryRow(`
		SELECT id, edited_at, edited_by, old_content, new_content
		FROM message_edits WHERE message_id = ?
	`, "msg_002").Scan(&editID, &editedAt, &editedBy, &oldContent, &newContent)
	if err != nil {
		t.Fatalf("Query edit history failed: %v", err)
	}
	if editedAt != "2026-01-01T01:00:00Z" {
		t.Errorf("Expected edited_at '2026-01-01T01:00:00Z', got '%s'", editedAt)
	}
	if editedBy != "ses_001" {
		t.Errorf("Expected edited_by 'ses_001', got '%s'", editedBy)
	}
	if !oldContent.Valid || oldContent.String != "Original content" {
		t.Errorf("Expected old_content 'Original content', got '%s'", oldContent.String)
	}
	if !newContent.Valid || newContent.String != "Updated content" {
		t.Errorf("Expected new_content 'Updated content', got '%s'", newContent.String)
	}
}

func TestProjector_ApplyMessageDelete(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	// Create a message first
	createEvent := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2026-01-01T00:00:00Z",
		MessageID: "msg_003",
		AgentID:   "agent:test:ABC",
		SessionID: "ses_001",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "To be deleted",
		},
	}
	createData, _ := json.Marshal(createEvent)
	if err := p.Apply(context.Background(), createData); err != nil {
		t.Fatalf("apply create: %v", err)
	}

	// Delete it
	deleteEvent := types.MessageDeleteEvent{
		Type:      "message.delete",
		Timestamp: "2026-01-01T02:00:00Z",
		MessageID: "msg_003",
		Reason:    "spam",
	}
	deleteData, _ := json.Marshal(deleteEvent)
	if err := p.Apply(context.Background(), deleteData); err != nil {
		t.Fatalf("Apply() delete failed: %v", err)
	}

	// Verify message is marked as deleted
	var deleted int
	var deletedAt, deleteReason sql.NullString
	err := db.QueryRow("SELECT deleted, deleted_at, delete_reason FROM messages WHERE message_id = ?", "msg_003").Scan(&deleted, &deletedAt, &deleteReason)
	if err != nil {
		t.Fatalf("Query message failed: %v", err)
	}
	if deleted != 1 {
		t.Errorf("Expected deleted=1, got %d", deleted)
	}
	if !deletedAt.Valid || deletedAt.String != "2026-01-01T02:00:00Z" {
		t.Errorf("Expected deleted_at '2026-01-01T02:00:00Z', got '%s'", deletedAt.String)
	}
	if !deleteReason.Valid || deleteReason.String != "spam" {
		t.Errorf("Expected delete_reason 'spam', got '%s'", deleteReason.String)
	}
}

func TestProjector_ApplyThreadCreate(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	event := types.ThreadCreateEvent{
		Type:      "thread.create",
		Timestamp: "2026-01-01T00:00:00Z",
		ThreadID:  "thr_100",
		Title:     "Test Thread",
		CreatedBy: "agent:test:ABC",
	}

	data, _ := json.Marshal(event)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}

	// Verify thread was inserted
	var title string
	err := db.QueryRow("SELECT title FROM threads WHERE thread_id = ?", "thr_100").Scan(&title)
	if err != nil {
		t.Fatalf("Query thread failed: %v", err)
	}
	if title != "Test Thread" {
		t.Errorf("Expected title 'Test Thread', got '%s'", title)
	}
}

func TestProjector_ApplyAgentRegister(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	event := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-01-01T00:00:00Z",
		AgentID:   "agent:implementer:XYZ",
		Kind:      "agent",
		Role:      "implementer",
		Module:    "auth",
		Display:   "Auth Implementer",
	}

	data, _ := json.Marshal(event)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}

	// Verify agent was inserted
	var role, module string
	err := db.QueryRow("SELECT role, module FROM agents WHERE agent_id = ?", "agent:implementer:XYZ").Scan(&role, &module)
	if err != nil {
		t.Fatalf("Query agent failed: %v", err)
	}
	if role != "implementer" {
		t.Errorf("Expected role 'implementer', got '%s'", role)
	}
	if module != "auth" {
		t.Errorf("Expected module 'auth', got '%s'", module)
	}
}

func TestProjector_ApplySessionStart(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	event := types.AgentSessionStartEvent{
		Type:      "agent.session.start",
		Timestamp: "2026-01-01T00:00:00Z",
		SessionID: "ses_200",
		AgentID:   "agent:test:ABC",
	}

	data, _ := json.Marshal(event)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}

	// Verify session was inserted
	var agentID, startedAt string
	err := db.QueryRow("SELECT agent_id, started_at FROM sessions WHERE session_id = ?", "ses_200").Scan(&agentID, &startedAt)
	if err != nil {
		t.Fatalf("Query session failed: %v", err)
	}
	if agentID != "agent:test:ABC" {
		t.Errorf("Expected agent_id 'agent:test:ABC', got '%s'", agentID)
	}
	if startedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("Expected started_at '2026-01-01T00:00:00Z', got '%s'", startedAt)
	}
}

func TestProjector_ApplySessionEnd(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	// Start a session first
	startEvent := types.AgentSessionStartEvent{
		Type:      "agent.session.start",
		Timestamp: "2026-01-01T00:00:00Z",
		SessionID: "ses_300",
		AgentID:   "agent:test:ABC",
	}
	startData, _ := json.Marshal(startEvent)
	if err := p.Apply(context.Background(), startData); err != nil {
		t.Fatalf("apply session start: %v", err)
	}

	// End the session
	endEvent := types.AgentSessionEndEvent{
		Type:      "agent.session.end",
		Timestamp: "2026-01-01T01:00:00Z",
		SessionID: "ses_300",
		Reason:    "completed",
	}
	endData, _ := json.Marshal(endEvent)
	if err := p.Apply(context.Background(), endData); err != nil {
		t.Fatalf("Apply() session end failed: %v", err)
	}

	// Verify session was updated
	var endedAt, endReason sql.NullString
	err := db.QueryRow("SELECT ended_at, end_reason FROM sessions WHERE session_id = ?", "ses_300").Scan(&endedAt, &endReason)
	if err != nil {
		t.Fatalf("Query session failed: %v", err)
	}
	if !endedAt.Valid || endedAt.String != "2026-01-01T01:00:00Z" {
		t.Errorf("Expected ended_at '2026-01-01T01:00:00Z', got '%s'", endedAt.String)
	}
	if !endReason.Valid || endReason.String != "completed" {
		t.Errorf("Expected end_reason 'completed', got '%s'", endReason.String)
	}
}

func TestProjector_Rebuild(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Create sync directory structure (simulates .thrum/sync/)
	syncDir := filepath.Join(t.TempDir(), "sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "messages"), 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write core events to events.jsonl
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	eventsWriter, _ := jsonl.NewWriter(eventsPath)
	if err := eventsWriter.Append(types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-01-01T00:00:00Z",
		EventID:   "01JKHM00000000000000000001",
		Version:   1,
		AgentID:   "agent:test:ABC",
		Kind:      "agent",
		Role:      "test",
		Module:    "test",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := eventsWriter.Append(types.ThreadCreateEvent{
		Type:      "thread.create",
		Timestamp: "2026-01-01T00:01:00Z",
		EventID:   "01JKHM00000000000000000002",
		Version:   1,
		ThreadID:  "thr_001",
		Title:     "Test Thread",
		CreatedBy: "agent:test:ABC",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := eventsWriter.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Write message events to messages/test.jsonl
	messagesPath := filepath.Join(syncDir, "messages", "test.jsonl")
	messagesWriter, _ := jsonl.NewWriter(messagesPath)
	if err := messagesWriter.Append(types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2026-01-01T00:02:00Z",
		EventID:   "01JKHM00000000000000000003",
		Version:   1,
		MessageID: "msg_001",
		ThreadID:  "thr_001",
		AgentID:   "agent:test:ABC",
		SessionID: "ses_001",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "Test message",
		},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := messagesWriter.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Rebuild database from multi-file JSONL
	p := projection.NewProjector(safedb.New(db))
	if err := p.Rebuild(context.Background(), syncDir); err != nil {
		t.Fatalf("Rebuild() failed: %v", err)
	}

	// Verify all events were applied
	var agentCount, threadCount, messageCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM agents").Scan(&agentCount); err != nil {
		t.Fatalf("query agents: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM threads").Scan(&threadCount); err != nil {
		t.Fatalf("query threads: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messageCount); err != nil {
		t.Fatalf("query messages: %v", err)
	}

	if agentCount != 1 {
		t.Errorf("Expected 1 agent, got %d", agentCount)
	}
	if threadCount != 1 {
		t.Errorf("Expected 1 thread, got %d", threadCount)
	}
	if messageCount != 1 {
		t.Errorf("Expected 1 message, got %d", messageCount)
	}
}

func TestProjector_MessageEditHistory(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	// Create a message with structured data
	createEvent := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2026-01-01T00:00:00Z",
		MessageID: "msg_edit_test",
		AgentID:   "agent:test:ABC",
		SessionID: "ses_edit_test",
		Body: types.MessageBody{
			Format:     "markdown",
			Content:    "Original content",
			Structured: `{"status":"draft"}`,
		},
	}
	createData, _ := json.Marshal(createEvent)
	if err := p.Apply(context.Background(), createData); err != nil {
		t.Fatalf("apply create: %v", err)
	}

	// Edit 1: Update content only
	edit1Event := types.MessageEditEvent{
		Type:      "message.edit",
		Timestamp: "2026-01-01T01:00:00Z",
		MessageID: "msg_edit_test",
		Body: types.MessageBody{
			Format:     "markdown",
			Content:    "First edit",
			Structured: `{"status":"draft"}`,
		},
	}
	edit1Data, _ := json.Marshal(edit1Event)
	if err := p.Apply(context.Background(), edit1Data); err != nil {
		t.Fatalf("Apply() first edit failed: %v", err)
	}

	// Edit 2: Update structured data
	edit2Event := types.MessageEditEvent{
		Type:      "message.edit",
		Timestamp: "2026-01-01T02:00:00Z",
		MessageID: "msg_edit_test",
		Body: types.MessageBody{
			Format:     "markdown",
			Content:    "Second edit",
			Structured: `{"status":"published"}`,
		},
	}
	edit2Data, _ := json.Marshal(edit2Event)
	if err := p.Apply(context.Background(), edit2Data); err != nil {
		t.Fatalf("Apply() second edit failed: %v", err)
	}

	// Verify final message state
	var content string
	var structured sql.NullString
	err := db.QueryRow("SELECT body_content, body_structured FROM messages WHERE message_id = ?",
		"msg_edit_test").Scan(&content, &structured)
	if err != nil {
		t.Fatalf("Query message failed: %v", err)
	}
	if content != "Second edit" {
		t.Errorf("Expected final content 'Second edit', got '%s'", content)
	}
	if !structured.Valid || structured.String != `{"status":"published"}` {
		t.Errorf("Expected final structured '{\"status\":\"published\"}', got '%s'", structured.String)
	}

	// Verify edit history (should have 2 edits)
	rows, err := db.Query(`
		SELECT edited_at, old_content, new_content, old_structured, new_structured
		FROM message_edits WHERE message_id = ?
		ORDER BY edited_at
	`, "msg_edit_test")
	if err != nil {
		t.Fatalf("Query edit history failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	edits := []struct {
		editedAt, oldContent, newContent, oldStructured, newStructured sql.NullString
	}{}
	for rows.Next() {
		var edit struct {
			editedAt, oldContent, newContent, oldStructured, newStructured sql.NullString
		}
		if err := rows.Scan(&edit.editedAt, &edit.oldContent, &edit.newContent, &edit.oldStructured, &edit.newStructured); err != nil {
			t.Fatalf("Scan edit failed: %v", err)
		}
		edits = append(edits, edit)
	}

	if len(edits) != 2 {
		t.Fatalf("Expected 2 edits, got %d", len(edits))
	}

	// Verify first edit
	if !edits[0].oldContent.Valid || edits[0].oldContent.String != "Original content" {
		t.Errorf("Edit 1: Expected old_content 'Original content', got '%s'", edits[0].oldContent.String)
	}
	if !edits[0].newContent.Valid || edits[0].newContent.String != "First edit" {
		t.Errorf("Edit 1: Expected new_content 'First edit', got '%s'", edits[0].newContent.String)
	}

	// Verify second edit
	if !edits[1].oldContent.Valid || edits[1].oldContent.String != "First edit" {
		t.Errorf("Edit 2: Expected old_content 'First edit', got '%s'", edits[1].oldContent.String)
	}
	if !edits[1].newContent.Valid || edits[1].newContent.String != "Second edit" {
		t.Errorf("Edit 2: Expected new_content 'Second edit', got '%s'", edits[1].newContent.String)
	}
	if !edits[1].oldStructured.Valid || edits[1].oldStructured.String != `{"status":"draft"}` {
		t.Errorf("Edit 2: Expected old_structured '{\"status\":\"draft\"}', got '%s'", edits[1].oldStructured.String)
	}
	if !edits[1].newStructured.Valid || edits[1].newStructured.String != `{"status":"published"}` {
		t.Errorf("Edit 2: Expected new_structured '{\"status\":\"published\"}', got '%s'", edits[1].newStructured.String)
	}
}

func TestProjector_MessageEditNonExistent(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	// Try to edit a message that doesn't exist
	editEvent := types.MessageEditEvent{
		Type:      "message.edit",
		Timestamp: "2026-01-01T01:00:00Z",
		MessageID: "msg_NONEXISTENT",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "Trying to edit non-existent",
		},
	}

	data, _ := json.Marshal(editEvent)
	err := p.Apply(context.Background(), data)
	if err == nil {
		t.Error("Expected error when editing non-existent message")
	}
}

func TestProjector_UnknownEventType(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	// Unknown event type should be ignored (forward compatibility)
	unknownEvent := map[string]string{
		"type":      "unknown.event",
		"timestamp": "2026-01-01T00:00:00Z",
		"data":      "test",
	}

	data, _ := json.Marshal(unknownEvent)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Errorf("Apply() should not error on unknown event type: %v", err)
	}
}
