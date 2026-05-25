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
	"github.com/leonletto/thrum/internal/sync/pending"
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

// TestApplyMessageCreate_SelfDelivery_StampsReadAt verifies the projector stamps
// read_at and seen_at on the author's own delivery row when the author appears
// in event.Recipients (a deliberate self-mention reached via HandleSend's
// direct-targeting paths). This drops the message out of --unread queries
// without requiring an explicit markRead round-trip.
func TestApplyMessageCreate_SelfDelivery_StampsReadAt(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	agentID := "coordinator_main"
	now := "2026-05-14T15:00:00Z"

	event := types.MessageCreateEvent{
		Type:       "message.create",
		Timestamp:  now,
		MessageID:  "msg_self_001",
		AgentID:    agentID,
		SessionID:  "ses_self",
		Body:       types.MessageBody{Format: "markdown", Content: "note"},
		Recipients: []string{agentID},
		Refs:       []types.Ref{{Type: "mention", Value: agentID}},
	}

	data, _ := json.Marshal(event)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}

	var deliveredAt, readAt, seenAt sql.NullString
	err := db.QueryRow(
		`SELECT delivered_at, read_at, seen_at FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ?`,
		"msg_self_001", agentID,
	).Scan(&deliveredAt, &readAt, &seenAt)
	if err != nil {
		t.Fatalf("query delivery row: %v", err)
	}
	if !readAt.Valid || readAt.String != now {
		t.Fatalf("expected read_at=%q, got valid=%v value=%q", now, readAt.Valid, readAt.String)
	}
	if !seenAt.Valid || seenAt.String != now {
		t.Fatalf("expected seen_at=%q, got valid=%v value=%q", now, seenAt.Valid, seenAt.String)
	}
}

// TestApplyMessageCreate_OtherRecipient_DoesNotStampReadAt verifies that non-self
// recipients still get NULL read_at/seen_at — the self-delivery branch must not
// leak into the standard delivery path.
func TestApplyMessageCreate_OtherRecipient_DoesNotStampReadAt(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	event := types.MessageCreateEvent{
		Type:       "message.create",
		Timestamp:  "2026-05-14T15:00:00Z",
		MessageID:  "msg_other_001",
		AgentID:    "coordinator_main",
		SessionID:  "ses_sender",
		Body:       types.MessageBody{Format: "markdown", Content: "hi alice"},
		Recipients: []string{"alice"},
		Refs:       []types.Ref{{Type: "mention", Value: "alice"}},
	}

	data, _ := json.Marshal(event)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}

	var readAt, seenAt sql.NullString
	err := db.QueryRow(
		`SELECT read_at, seen_at FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ?`,
		"msg_other_001", "alice",
	).Scan(&readAt, &seenAt)
	if err != nil {
		t.Fatalf("query delivery row: %v", err)
	}
	if readAt.Valid {
		t.Fatalf("expected NULL read_at for non-self recipient, got %q", readAt.String)
	}
	if seenAt.Valid {
		t.Fatalf("expected NULL seen_at for non-self recipient, got %q", seenAt.String)
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

// TestProjector_ApplySessionStart_DuplicateIsNoOp guards thrum-9jcb.3: peer
// sync replicates agent.session.start events for sessions that may already
// exist locally (same session_id arriving from a peer daemon after the
// originating daemon wrote it). Before the INSERT OR IGNORE fix, the second
// apply hit a UNIQUE constraint failure on sessions.session_id, aborting the
// remainder of the sync batch and forcing a retry round-trip.
func TestProjector_ApplySessionStart_DuplicateIsNoOp(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	event := types.AgentSessionStartEvent{
		Type:      "agent.session.start",
		Timestamp: "2026-01-01T00:00:00Z",
		SessionID: "ses_dup",
		AgentID:   "agent:test:DUP",
	}

	data, _ := json.Marshal(event)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("second apply failed (regression — should be a no-op, not a UNIQUE constraint error): %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions WHERE session_id = ?", "ses_dup").Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 session row after duplicate apply, got %d", count)
	}

	var agentID, startedAt string
	if err := db.QueryRow("SELECT agent_id, started_at FROM sessions WHERE session_id = ?", "ses_dup").Scan(&agentID, &startedAt); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if agentID != "agent:test:DUP" {
		t.Errorf("expected agent_id 'agent:test:DUP', got '%s'", agentID)
	}
	if startedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("expected started_at '2026-01-01T00:00:00Z', got '%s'", startedAt)
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
	var agentCount, messageCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM agents").Scan(&agentCount); err != nil {
		t.Fatalf("query agents: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messageCount); err != nil {
		t.Fatalf("query messages: %v", err)
	}

	if agentCount != 1 {
		t.Errorf("Expected 1 agent, got %d", agentCount)
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

	// Editing a message that doesn't exist should succeed gracefully (no-op).
	// This supports out-of-order event delivery during peer sync — an edit may
	// arrive before the original message.create. The event is stored in JSONL;
	// the projection just skips the edit history and update when no row matches.
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
	if err != nil {
		t.Errorf("editing non-existent message should succeed gracefully, got: %v", err)
	}
}

func TestProjector_PurgeExecuted(t *testing.T) {
	rawDB := setupTestDB(t)
	db := safedb.New(rawDB)
	p := projection.NewProjector(db)
	ctx := context.Background()

	// Insert some old data
	_, _ = db.ExecContext(ctx, `INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content) VALUES ('msg_old', 'agent_a', 'sess_1', '2026-03-18T00:00:00Z', 'text', 'old')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content) VALUES ('msg_new', 'agent_a', 'sess_1', '2026-03-22T00:00:00Z', 'text', 'new')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sessions (session_id, agent_id, started_at) VALUES ('sess_old', 'agent_a', '2026-03-18T00:00:00Z')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json) VALUES ('evt_old', 1, 'agent.register', '2026-03-18T00:00:00Z', 'peer_1', '{}')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json) VALUES ('evt_new', 2, 'agent.register', '2026-03-22T00:00:00Z', 'peer_1', '{}')`)

	// Apply purge.executed event
	event := []byte(`{"type":"purge.executed","timestamp":"2026-03-25T00:00:00Z","event_id":"evt_purge","cutoff":"2026-03-20T00:00:00Z","v":1,"origin_daemon":"peer_1"}`)
	err := p.Apply(ctx, event)
	if err != nil {
		t.Fatalf("apply purge.executed: %v", err)
	}

	// Old message deleted, new message kept
	var msgCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&msgCount)
	if msgCount != 1 {
		t.Errorf("expected 1 message after purge, got %d", msgCount)
	}

	// Old session deleted
	var sessCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE started_at < '2026-03-20T00:00:00Z'`).Scan(&sessCount)
	if sessCount != 0 {
		t.Errorf("expected 0 old sessions, got %d", sessCount)
	}

	// Old event deleted, new event kept (plus the purge event itself)
	var evtCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE type != 'purge.executed'`).Scan(&evtCount)
	if evtCount != 1 {
		t.Errorf("expected 1 non-purge event after purge, got %d", evtCount)
	}

	// Cutoff stored in purge_metadata
	var stored string
	err = db.QueryRowContext(ctx, `SELECT value FROM purge_metadata WHERE key = 'purge_cutoff'`).Scan(&stored)
	if err != nil {
		t.Fatalf("query purge_metadata: %v", err)
	}
	if stored != "2026-03-20T00:00:00Z" {
		t.Errorf("expected cutoff 2026-03-20T00:00:00Z, got %s", stored)
	}
}

func TestProjector_AgentCleanup_FullScrub(t *testing.T) {
	rawDB := setupTestDB(t)
	db := safedb.New(rawDB)
	p := projection.NewProjector(db)
	ctx := context.Background()

	// Insert agent + related data
	_, _ = db.ExecContext(ctx, `INSERT INTO agents (agent_id, kind, role, module, registered_at) VALUES ('doomed', 'agent', 'test', 'test', '2026-03-20T00:00:00Z')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content) VALUES ('msg_d1', 'doomed', 'sess_d1', '2026-03-20T01:00:00Z', 'text', 'hi')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sessions (session_id, agent_id, started_at) VALUES ('sess_d1', 'doomed', '2026-03-20T00:00:00Z')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json) VALUES ('evt_d1', 10, 'agent.register', '2026-03-20T00:00:00Z', 'peer_1', '{"agent_id":"doomed"}')`)

	// Also insert another agent's data to ensure it's not affected
	_, _ = db.ExecContext(ctx, `INSERT INTO agents (agent_id, kind, role, module, registered_at) VALUES ('keeper', 'agent', 'test', 'test', '2026-03-20T00:00:00Z')`)
	_, _ = db.ExecContext(ctx, `INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content) VALUES ('msg_k1', 'keeper', 'sess_k1', '2026-03-20T01:00:00Z', 'text', 'hi')`)

	// Apply agent.cleanup
	event := []byte(`{"type":"agent.cleanup","timestamp":"2026-03-25T00:00:00Z","event_id":"evt_cleanup","agent_id":"doomed","reason":"manual deletion","method":"manual","v":1}`)
	err := p.Apply(ctx, event)
	if err != nil {
		t.Fatalf("apply agent.cleanup: %v", err)
	}

	// Verify doomed agent is gone
	var agentCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE agent_id = 'doomed'`).Scan(&agentCount)
	if agentCount != 0 {
		t.Error("doomed agent should be deleted")
	}

	// Verify doomed agent's messages are gone
	var msgCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE agent_id = 'doomed'`).Scan(&msgCount)
	if msgCount != 0 {
		t.Error("doomed agent's messages should be deleted")
	}

	// Verify doomed agent's sessions are gone
	var sessCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE agent_id = 'doomed'`).Scan(&sessCount)
	if sessCount != 0 {
		t.Error("doomed agent's sessions should be deleted")
	}

	// Verify doomed agent's events are gone
	var evtCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE event_json LIKE '%"doomed"%' AND type != 'agent.cleanup'`).Scan(&evtCount)
	if evtCount != 0 {
		t.Error("doomed agent's events should be deleted")
	}

	// Verify keeper agent is untouched
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE agent_id = 'keeper'`).Scan(&agentCount)
	if agentCount != 1 {
		t.Error("keeper agent should still exist")
	}
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE agent_id = 'keeper'`).Scan(&msgCount)
	if msgCount != 1 {
		t.Error("keeper agent's messages should still exist")
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

// TestProjector_GroupMemberAddUnknownGroup verifies that a group.member.add
// event referencing a group that doesn't exist locally succeeds gracefully
// instead of raising a FK constraint violation that would poison sync.
func TestProjector_GroupMemberAddUnknownGroup(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))
	ctx := context.Background()

	// Apply a member.add for a group that doesn't exist
	addEvent := types.GroupMemberAddEvent{
		Type:        "group.member.add",
		Timestamp:   "2026-01-01T10:00:00Z",
		GroupID:     "grp_UNKNOWN",
		MemberType:  "agent",
		MemberValue: "agent:test:ABC123",
		AddedBy:     "agent:admin:XYZ",
	}
	data, _ := json.Marshal(addEvent)
	if err := p.Apply(ctx, data); err != nil {
		t.Errorf("group.member.add for unknown group should succeed gracefully, got: %v", err)
	}

	// Verify no rows were inserted into group_members
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM group_members WHERE group_id = ?`, "grp_UNKNOWN").Scan(&count)
	if err != nil {
		t.Fatalf("query group_members: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows for unknown group, got %d", count)
	}
}

// TestProjector_AgentUpdateUnknownSession verifies that an agent.update event
// with a work context referencing a session that doesn't exist locally
// succeeds gracefully — contexts with unknown session_ids are skipped instead
// of raising a FK constraint violation.
func TestProjector_AgentUpdateUnknownSession(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))
	ctx := context.Background()

	// First insert a session so we can verify mixed-batch behavior (one known,
	// one unknown session)
	knownSessionID := "ses_KNOWN_001"
	startEvent := types.AgentSessionStartEvent{
		Type:      "agent.session.start",
		Timestamp: "2026-01-01T09:00:00Z",
		SessionID: knownSessionID,
		AgentID:   "agent:test:ABC123",
	}
	startData, _ := json.Marshal(startEvent)
	if err := p.Apply(ctx, startData); err != nil {
		t.Fatalf("apply session.start: %v", err)
	}

	// Apply agent.update with contexts for both known and unknown sessions
	updateEvent := types.AgentUpdateEvent{
		Type:      "agent.update",
		Timestamp: "2026-01-01T10:00:00Z",
		AgentID:   "agent:test:ABC123",
		WorkContexts: []types.SessionWorkContext{
			{
				SessionID:    knownSessionID,
				Branch:       "main",
				WorktreePath: "/tmp/known",
			},
			{
				SessionID:    "ses_UNKNOWN_999",
				Branch:       "feature",
				WorktreePath: "/tmp/unknown",
			},
		},
	}
	data, _ := json.Marshal(updateEvent)
	if err := p.Apply(ctx, data); err != nil {
		t.Errorf("agent.update with unknown session should succeed gracefully, got: %v", err)
	}

	// Known session context should be inserted
	var knownCount int
	err := db.QueryRow(`SELECT COUNT(*) FROM agent_work_contexts WHERE session_id = ?`, knownSessionID).Scan(&knownCount)
	if err != nil {
		t.Fatalf("query known context: %v", err)
	}
	if knownCount != 1 {
		t.Errorf("expected 1 row for known session, got %d", knownCount)
	}

	// Unknown session context should be skipped
	var unknownCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM agent_work_contexts WHERE session_id = ?`, "ses_UNKNOWN_999").Scan(&unknownCount)
	if err != nil {
		t.Fatalf("query unknown context: %v", err)
	}
	if unknownCount != 0 {
		t.Errorf("expected 0 rows for unknown session, got %d", unknownCount)
	}
}

// thrum-qb62: ensure receipt events from non-recipients do not create phantom
// delivery rows. The INSERT OR IGNORE at projector line 360 previously created
// a delivery row for any agent that marked a message read, even if the agent
// was never a legitimate recipient (e.g. a directly-targeted @impl_skills
// send was phantom-delivered to impl_permission_prompts because impl_permission_prompts
// ran `thrum message read --all` and the projector blindly inserted a row).

// insertMessageWithRef inserts a message row with a single mention ref and the
// given recipients (as message_deliveries rows).
func insertMessageWithRef(t *testing.T, p *projection.Projector, id string, mention string, recipients []string) {
	t.Helper()
	var refs []types.Ref
	if mention != "" {
		refs = []types.Ref{{Type: "mention", Value: mention}}
	}
	ev := types.MessageCreateEvent{
		Type:       "message.create",
		Timestamp:  "2026-01-01T00:00:00Z",
		MessageID:  id,
		AgentID:    "sender_test",
		SessionID:  "ses_sender",
		Body:       types.MessageBody{Format: "markdown", Content: "hi"},
		Refs:       refs,
		Recipients: recipients,
	}
	data, _ := json.Marshal(ev)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("apply create %s: %v", id, err)
	}
}

func insertMessageWithScope(t *testing.T, p *projection.Projector, id string, scopeType, scopeValue string, recipients []string) {
	t.Helper()
	ev := types.MessageCreateEvent{
		Type:       "message.create",
		Timestamp:  "2026-01-01T00:00:00Z",
		MessageID:  id,
		AgentID:    "sender_test",
		SessionID:  "ses_sender",
		Body:       types.MessageBody{Format: "markdown", Content: "hi"},
		Scopes:     []types.Scope{{Type: scopeType, Value: scopeValue}},
		Recipients: recipients,
	}
	data, _ := json.Marshal(ev)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("apply create %s: %v", id, err)
	}
}

func insertAgent(t *testing.T, db *sql.DB, agentID, role string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO agents (agent_id, kind, role, module, registered_at) VALUES (?, 'named', ?, 'test', '2026-01-01T00:00:00Z')`,
		agentID, role)
	if err != nil {
		t.Fatalf("insert agent %s: %v", agentID, err)
	}
}

func applyReceipt(t *testing.T, p *projection.Projector, messageID, agentID, receiptType, timestamp string) {
	t.Helper()
	ev := types.MessageReceiptEvent{
		Type:        "message.receipt",
		Timestamp:   timestamp,
		MessageID:   messageID,
		AgentID:     agentID,
		ReceiptType: receiptType,
	}
	data, _ := json.Marshal(ev)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("apply receipt for %s by %s: %v", messageID, agentID, err)
	}
}

func deliveryCount(t *testing.T, db *sql.DB, messageID, agentID string) int {
	t.Helper()
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ?`,
		messageID, agentID).Scan(&n)
	if err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	return n
}

func readAtOf(t *testing.T, db *sql.DB, messageID, agentID string) sql.NullString {
	t.Helper()
	var readAt sql.NullString
	err := db.QueryRow(`SELECT read_at FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ?`,
		messageID, agentID).Scan(&readAt)
	if err == sql.ErrNoRows {
		return sql.NullString{}
	}
	if err != nil {
		t.Fatalf("query read_at: %v", err)
	}
	return readAt
}

func TestProjector_ApplyMessageReceipt_NonRecipientDoesNotCreatePhantomRow(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	// Register agents — "alice" is targeted; "bob" is NOT a recipient.
	insertAgent(t, db, "alice", "implementer")
	insertAgent(t, db, "bob", "implementer")

	// Message targets alice via direct mention, not bob.
	insertMessageWithRef(t, p, "msg_phantom", "alice", []string{"alice"})

	// Bob sends a read receipt — simulating `thrum message read --all`.
	applyReceipt(t, p, "msg_phantom", "bob", "read", "2026-01-01T00:00:05Z")

	if got := deliveryCount(t, db, "msg_phantom", "bob"); got != 0 {
		t.Fatalf("bob is not a recipient — expected 0 delivery rows, got %d", got)
	}
	// Alice's legitimate row should remain intact.
	if got := deliveryCount(t, db, "msg_phantom", "alice"); got != 1 {
		t.Fatalf("alice is the recipient — expected 1 delivery row, got %d", got)
	}
}

func TestProjector_ApplyMessageReceipt_ExistingRecipientRowIsUpdated(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	insertAgent(t, db, "alice", "implementer")
	insertMessageWithRef(t, p, "msg_ok", "alice", []string{"alice"})

	// Normal post-v14 path: legitimate recipient has a delivery row; read updates it.
	applyReceipt(t, p, "msg_ok", "alice", "read", "2026-01-01T00:00:05Z")

	if got := deliveryCount(t, db, "msg_ok", "alice"); got != 1 {
		t.Fatalf("alice has existing delivery row — expected 1 row, got %d", got)
	}
	readAt := readAtOf(t, db, "msg_ok", "alice")
	if !readAt.Valid || readAt.String != "2026-01-01T00:00:05Z" {
		t.Fatalf("alice's read_at should be set to receipt timestamp, got %v", readAt)
	}
}

func TestProjector_ApplyMessageReceipt_MentionedAgentCreatesRowForPreV14(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	insertAgent(t, db, "alice", "implementer")
	// Pre-v14 simulation: message has mention ref for alice but NO delivery row
	// (empty Recipients). A legitimate recipient reading it should still create
	// their own delivery row on receipt.
	insertMessageWithRef(t, p, "msg_prev14", "alice", nil)

	applyReceipt(t, p, "msg_prev14", "alice", "read", "2026-01-01T00:00:05Z")

	if got := deliveryCount(t, db, "msg_prev14", "alice"); got != 1 {
		t.Fatalf("mentioned agent alice should create a delivery row on receipt, got %d", got)
	}
	readAt := readAtOf(t, db, "msg_prev14", "alice")
	if !readAt.Valid {
		t.Fatalf("alice's read_at should be populated")
	}
}

func TestProjector_ApplyMessageReceipt_RoleMentionCreatesRow(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	insertAgent(t, db, "alice", "reviewer")
	// Message mentions @reviewer (role), not @alice directly. Alice fills that role.
	insertMessageWithRef(t, p, "msg_role", "reviewer", nil)

	applyReceipt(t, p, "msg_role", "alice", "read", "2026-01-01T00:00:05Z")

	if got := deliveryCount(t, db, "msg_role", "alice"); got != 1 {
		t.Fatalf("agent with matching role should create a delivery row, got %d", got)
	}
}

func TestProjector_ApplyMessageReceipt_RoleMentionDoesNotMatchOtherRole(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	insertAgent(t, db, "alice", "reviewer")
	insertAgent(t, db, "bob", "implementer")
	insertMessageWithRef(t, p, "msg_role_other", "reviewer", nil)

	// Bob is an implementer — the @reviewer mention does not include him.
	applyReceipt(t, p, "msg_role_other", "bob", "read", "2026-01-01T00:00:05Z")

	if got := deliveryCount(t, db, "msg_role_other", "bob"); got != 0 {
		t.Fatalf("bob's role does not match — expected 0 delivery rows, got %d", got)
	}
}

func TestProjector_ApplyMessageReceipt_BroadcastCreatesRow(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	insertAgent(t, db, "alice", "implementer")
	insertAgent(t, db, "bob", "implementer")
	insertMessageWithScope(t, p, "msg_bcast", "broadcast", "everyone", nil)

	applyReceipt(t, p, "msg_bcast", "bob", "read", "2026-01-01T00:00:05Z")

	if got := deliveryCount(t, db, "msg_bcast", "bob"); got != 1 {
		t.Fatalf("broadcast scope should accept any agent, got %d rows for bob", got)
	}
}

func TestProjector_ApplyMessageReceipt_GroupMemberCreatesRow(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	insertAgent(t, db, "alice", "implementer")

	// Create a group with alice as a member.
	_, err := db.Exec(`INSERT INTO groups (group_id, name, created_at, created_by) VALUES ('grp_1','team_a','2026-01-01T00:00:00Z','sender_test')`)
	if err != nil {
		t.Fatalf("insert group: %v", err)
	}
	_, err = db.Exec(`INSERT INTO group_members (group_id, member_type, member_value, added_at) VALUES ('grp_1','agent','alice','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert group member: %v", err)
	}

	insertMessageWithScope(t, p, "msg_grp", "group", "team_a", nil)

	applyReceipt(t, p, "msg_grp", "alice", "read", "2026-01-01T00:00:05Z")

	if got := deliveryCount(t, db, "msg_grp", "alice"); got != 1 {
		t.Fatalf("group member alice should create a delivery row, got %d", got)
	}
}

func TestProjector_ApplyMessageReceipt_GroupRoleMemberCreatesRow(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	// Register an agent whose role matches a role-type group member.
	insertAgent(t, db, "alice", "reviewer")

	_, err := db.Exec(`INSERT INTO groups (group_id, name, created_at, created_by) VALUES ('grp_r','reviewers','2026-01-01T00:00:00Z','sender_test')`)
	if err != nil {
		t.Fatalf("insert group: %v", err)
	}
	// Group member stored by role, not by agent_id — exercises the role
	// lookup branch in the legitimacy gate.
	_, err = db.Exec(`INSERT INTO group_members (group_id, member_type, member_value, added_at) VALUES ('grp_r','role','reviewer','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert group role member: %v", err)
	}

	insertMessageWithScope(t, p, "msg_grp_role", "group", "reviewers", nil)

	applyReceipt(t, p, "msg_grp_role", "alice", "read", "2026-01-01T00:00:05Z")

	if got := deliveryCount(t, db, "msg_grp_role", "alice"); got != 1 {
		t.Fatalf("agent with matching role in role-type group member should create a delivery row, got %d", got)
	}
}

func TestProjector_ApplyMessageReceipt_NonGroupMemberDoesNotCreateRow(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	p := projection.NewProjector(safedb.New(db))

	insertAgent(t, db, "alice", "implementer")
	insertAgent(t, db, "carol", "implementer")

	_, err := db.Exec(`INSERT INTO groups (group_id, name, created_at, created_by) VALUES ('grp_a','team_a','2026-01-01T00:00:00Z','sender_test')`)
	if err != nil {
		t.Fatalf("insert group: %v", err)
	}
	_, err = db.Exec(`INSERT INTO group_members (group_id, member_type, member_value, added_at) VALUES ('grp_a','agent','alice','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert group member: %v", err)
	}

	insertMessageWithScope(t, p, "msg_grp_no", "group", "team_a", nil)

	// carol is not in team_a — her receipt must not create a delivery row.
	applyReceipt(t, p, "msg_grp_no", "carol", "read", "2026-01-01T00:00:05Z")

	if got := deliveryCount(t, db, "msg_grp_no", "carol"); got != 0 {
		t.Fatalf("carol is not a group member — expected 0 rows, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// E11 / thrum-s6os.12: pending_route_resolution + pending pool integration
// ---------------------------------------------------------------------------

// setupSyncDir creates a minimal sync directory structure and returns its path.
func setupSyncDir(t *testing.T) string {
	t.Helper()
	syncDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(syncDir, "state", "agents"), 0750); err != nil {
		t.Fatalf("mkdir state/agents: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(syncDir, "state", "bridge-groups"), 0750); err != nil {
		t.Fatalf("mkdir state/bridge-groups: %v", err)
	}
	return syncDir
}

// writeStateFile creates a stub state file for the given ID. It writes an
// empty JSON object so os.Stat succeeds (content is not validated by
// stateFileExists). The kind parameter is "agents" or "bridge-groups".
func writeStateFile(t *testing.T, syncDir, kind, id string) {
	t.Helper()
	path := filepath.Join(syncDir, "state", kind, id+".json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatalf("write state file %s: %v", path, err)
	}
}

// TestProjector_MessageIngest_MissingBridgeGroup_AddsToPool verifies that
// when a message.create event arrives referencing a bridge-group whose state
// file does not exist in syncDir, the message row is inserted with
// pending_route_resolution=1 AND the orphan is added to the pending pool.
// This covers E7.AC.5 (pool-add side).
func TestProjector_MessageIngest_MissingBridgeGroup_AddsToPool(t *testing.T) {
	rawDB := setupTestDB(t)
	defer func() { _ = rawDB.Close() }()
	db := safedb.New(rawDB)

	syncDir := setupSyncDir(t)
	pool := pending.New()

	p := projection.NewProjector(db)
	p.SetPendingPool(syncDir, pool)
	// No resolver needed for this test (we only test Add, not Resolve).

	// Bridge-group ID that has NO state file on disk.
	missingGroupID := "brg_missing_001"

	event := types.MessageCreateEvent{
		Type:       "message.create",
		Timestamp:  "2026-05-18T10:00:00Z",
		MessageID:  "msg_pending_001",
		AgentID:    missingGroupID, // author is the missing bridge-group agent
		SessionID:  "ses_p001",
		Body:       types.MessageBody{Format: "text", Content: "hi"},
		Recipients: []string{"alice"},
	}
	data, _ := json.Marshal(event)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Pool should hold one orphan.
	if got := pool.Size(); got != 1 {
		t.Errorf("pool.Size() = %d, want 1", got)
	}

	// The messages row must have pending_route_resolution=1.
	var flag int
	err := rawDB.QueryRow(`SELECT pending_route_resolution FROM messages WHERE message_id = ?`, "msg_pending_001").Scan(&flag)
	if err != nil {
		t.Fatalf("query pending flag: %v", err)
	}
	if flag != 1 {
		t.Errorf("pending_route_resolution = %d, want 1", flag)
	}
}

// TestProjectionResolver_Resolve_StateFileLands verifies that once the missing
// state file is written to disk, ProjectionResolver.Resolve returns (true, nil)
// and the messages row has pending_route_resolution cleared to 0.
// This covers E7.AC.5 (resolve side).
func TestProjectionResolver_Resolve_StateFileLands(t *testing.T) {
	rawDB := setupTestDB(t)
	defer func() { _ = rawDB.Close() }()
	db := safedb.New(rawDB)

	syncDir := setupSyncDir(t)
	pool := pending.New()

	p := projection.NewProjector(db)
	p.SetPendingPool(syncDir, pool)
	resolver := projection.NewProjectionResolver(p)
	p.SetPendingResolver(resolver)

	missingAgentID := "agent_missing_001"
	// The recipient also needs a state file (or the orphan stays blocked by it
	// too). We use the same agent as both author and sole recipient to keep the
	// fixture simple — after the single state file lands, all BlockedBy entries
	// are satisfied.
	event := types.MessageCreateEvent{
		Type:       "message.create",
		Timestamp:  "2026-05-18T10:01:00Z",
		MessageID:  "msg_resolve_001",
		AgentID:    missingAgentID,
		SessionID:  "ses_r001",
		Body:       types.MessageBody{Format: "text", Content: "hello"},
		Recipients: []string{missingAgentID}, // self-send; only one state file to satisfy
	}
	data, _ := json.Marshal(event)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Confirm the orphan is in the pool with pending_route_resolution=1.
	if pool.Size() != 1 {
		t.Fatalf("pool.Size() = %d, want 1 (pre-resolve)", pool.Size())
	}

	// Now write the missing state file to disk.
	writeStateFile(t, syncDir, "agents", missingAgentID)

	// Manually call Resolve (normally driven by ResolveOnStateLand).
	orphans := pool.List()
	if len(orphans) != 1 {
		t.Fatalf("pool.List() returned %d orphans, want 1", len(orphans))
	}
	ok, err := resolver.Resolve(context.Background(), orphans[0])
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if !ok {
		t.Fatalf("Resolve returned false — expected true after state file landed")
	}

	// pending_route_resolution must be 0 now.
	var flag int
	if err := rawDB.QueryRow(`SELECT pending_route_resolution FROM messages WHERE message_id = ?`, "msg_resolve_001").Scan(&flag); err != nil {
		t.Fatalf("query pending flag: %v", err)
	}
	if flag != 0 {
		t.Errorf("pending_route_resolution = %d after resolve, want 0", flag)
	}
}

// TestProjector_AgentRegister_TriggersResolveOnStateLand verifies that
// ingesting an agent.register event for an agent whose ID is in the pool's
// BlockedBy list calls ResolveOnStateLand, which removes the orphan from the
// pool (assuming the state file is also present on disk).
// This covers the agent.register → pool.ResolveOnStateLand wiring.
func TestProjector_AgentRegister_TriggersResolveOnStateLand(t *testing.T) {
	rawDB := setupTestDB(t)
	defer func() { _ = rawDB.Close() }()
	db := safedb.New(rawDB)

	syncDir := setupSyncDir(t)
	pool := pending.New()

	p := projection.NewProjector(db)
	p.SetPendingPool(syncDir, pool)
	resolver := projection.NewProjectionResolver(p)
	p.SetPendingResolver(resolver)

	blockedByAgent := "agent:coordinator:BLOCKED01"

	// Pre-populate pool with an orphan blocked by blockedByAgent.
	// We insert the message row directly (bypassing applyMessageCreate) so
	// the test is not sensitive to state-file presence at message-ingest time.
	_, err := rawDB.Exec(`
		INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content, pending_route_resolution)
		VALUES ('msg_unblock_001', ?, 'ses_ub001', '2026-05-18T10:02:00Z', 'text', 'unblock me', 1)
	`, blockedByAgent)
	if err != nil {
		t.Fatalf("insert orphan message: %v", err)
	}
	pool.Add(pending.OrphanedMessage{
		MessageID:  "msg_unblock_001",
		AuthorID:   blockedByAgent,
		Recipients: []string{"dave"},
		BlockedBy:  []string{blockedByAgent},
	})
	if pool.Size() != 1 {
		t.Fatalf("pre-condition: pool.Size() = %d, want 1", pool.Size())
	}

	// Write the state file so the resolver's disk check passes.
	writeStateFile(t, syncDir, "agents", blockedByAgent)

	// Now ingest the agent.register event — this should trigger ResolveOnStateLand.
	registerEvent := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-05-18T10:02:30Z",
		AgentID:   blockedByAgent,
		Kind:      "agent",
		Role:      "coordinator",
		Module:    "main",
	}
	regData, _ := json.Marshal(registerEvent)
	if err := p.Apply(context.Background(), regData); err != nil {
		t.Fatalf("Apply agent.register: %v", err)
	}

	// The orphan should be resolved and removed from the pool.
	if got := pool.Size(); got != 0 {
		t.Errorf("pool.Size() = %d after agent.register, want 0", got)
	}

	// And pending_route_resolution must be 0.
	var flag int
	if err := rawDB.QueryRow(`SELECT pending_route_resolution FROM messages WHERE message_id = 'msg_unblock_001'`).Scan(&flag); err != nil {
		t.Fatalf("query pending flag: %v", err)
	}
	if flag != 0 {
		t.Errorf("pending_route_resolution = %d after resolve, want 0", flag)
	}
}

// TestProjector_MessageIngest_AllStateFilesPresent_NoPending verifies that
// when all referenced state files ARE present on disk, the message row is
// inserted with pending_route_resolution=0 and the pool remains empty.
func TestProjector_MessageIngest_AllStateFilesPresent_NoPending(t *testing.T) {
	rawDB := setupTestDB(t)
	defer func() { _ = rawDB.Close() }()
	db := safedb.New(rawDB)

	syncDir := setupSyncDir(t)
	pool := pending.New()

	p := projection.NewProjector(db)
	p.SetPendingPool(syncDir, pool)
	resolver := projection.NewProjectionResolver(p)
	p.SetPendingResolver(resolver)

	authorID := "agent:impl:PRESENT01"
	recipientID := "agent:coord:PRESENT02"

	// Write both state files so they are present on disk.
	writeStateFile(t, syncDir, "agents", authorID)
	writeStateFile(t, syncDir, "agents", recipientID)

	event := types.MessageCreateEvent{
		Type:       "message.create",
		Timestamp:  "2026-05-18T10:03:00Z",
		MessageID:  "msg_present_001",
		AgentID:    authorID,
		SessionID:  "ses_pr001",
		Body:       types.MessageBody{Format: "text", Content: "all present"},
		Recipients: []string{recipientID},
	}
	data, _ := json.Marshal(event)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Pool must be empty — no orphans.
	if got := pool.Size(); got != 0 {
		t.Errorf("pool.Size() = %d, want 0 (all state files present)", got)
	}

	// Flag must be 0.
	var flag int
	if err := rawDB.QueryRow(`SELECT pending_route_resolution FROM messages WHERE message_id = 'msg_present_001'`).Scan(&flag); err != nil {
		t.Fatalf("query pending flag: %v", err)
	}
	if flag != 0 {
		t.Errorf("pending_route_resolution = %d, want 0 (all state files present)", flag)
	}
}
