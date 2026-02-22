package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/types"
)

// setupArchiveTest creates a state with a registered agent and active session for archive tests.
func setupArchiveTest(t *testing.T) (*MessageHandler, *state.State, string, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create .thrum dir: %v", err)
	}

	repoID := "r_ARCHIVE_TEST"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}

	t.Setenv("THRUM_ROLE", "backend")
	t.Setenv("THRUM_MODULE", "core")

	agentID := identity.GenerateAgentID(repoID, "backend", "core", "")
	agentHandler := NewAgentHandler(st)
	registerParams, _ := json.Marshal(RegisterRequest{Role: "backend", Module: "core"})
	if _, err := agentHandler.HandleRegister(context.Background(), registerParams); err != nil {
		t.Fatalf("register agent: %v", err)
	}

	sessionHandler := NewSessionHandler(st)
	sessionParams, _ := json.Marshal(SessionStartRequest{AgentID: agentID})
	if _, err := sessionHandler.HandleStart(context.Background(), sessionParams); err != nil {
		t.Fatalf("start session: %v", err)
	}

	handler := NewMessageHandler(st)
	return handler, st, agentID, func() { _ = st.Close() }
}

// sendArchiveTestMessage is a helper to send a message and return its ID.
func sendArchiveTestMessage(t *testing.T, handler *MessageHandler, content string, scopes []types.Scope, agentID string) string {
	t.Helper()
	req := SendRequest{
		Content:       content,
		Format:        "markdown",
		Scopes:        scopes,
		CallerAgentID: agentID,
	}
	params, _ := json.Marshal(req)
	resp, err := handler.HandleSend(context.Background(), params)
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	return resp.(*SendResponse).MessageID
}

// TestArchiveAgentMessages verifies that agent messages are exported to JSONL and then deleted.
func TestArchiveAgentMessages(t *testing.T) {
	handler, st, agentID, cleanup := setupArchiveTest(t)
	defer cleanup()

	ctx := context.Background()

	// Send two messages from the agent
	msgID1 := sendArchiveTestMessage(t, handler, "First agent message", nil, agentID)
	msgID2 := sendArchiveTestMessage(t, handler, "Second agent message", nil, agentID)

	// Archive by agent_id
	archiveParams, _ := json.Marshal(ArchiveRequest{
		ArchiveType: "agent",
		Identifier:  agentID,
	})
	result, err := handler.HandleArchive(ctx, archiveParams)
	if err != nil {
		t.Fatalf("HandleArchive: %v", err)
	}
	resp := result.(*ArchiveResponse)

	if resp.ArchivedCount != 2 {
		t.Errorf("ArchivedCount = %d, want 2", resp.ArchivedCount)
	}
	if resp.ArchivePath == "" {
		t.Error("ArchivePath should not be empty")
	}

	// Verify archive file exists
	if _, err := os.Stat(resp.ArchivePath); err != nil {
		t.Fatalf("archive file not found: %v", err)
	}

	// Read and parse archive lines
	lines := readJSONLLines(t, resp.ArchivePath)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines in archive, got %d", len(lines))
	}

	// Verify message IDs appear in archive
	foundIDs := map[string]bool{}
	for _, line := range lines {
		id, ok := line["message_id"].(string)
		if !ok || id == "" {
			t.Error("archive line missing message_id")
		}
		foundIDs[id] = true
	}
	if !foundIDs[msgID1] {
		t.Errorf("msgID1 %s not found in archive", msgID1)
	}
	if !foundIDs[msgID2] {
		t.Errorf("msgID2 %s not found in archive", msgID2)
	}

	// Verify messages are deleted from the database
	var count int
	err = st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE message_id IN (?, ?)`, msgID1, msgID2,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query after archive: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 messages after archive, got %d", count)
	}
}

// TestArchiveGroupMessages verifies that messages with a group scope are exported and deleted.
func TestArchiveGroupMessages(t *testing.T) {
	handler, st, agentID, cleanup := setupArchiveTest(t)
	defer cleanup()

	ctx := context.Background()

	groupScope := []types.Scope{{Type: "group", Value: "backend-team"}}
	otherScope := []types.Scope{{Type: "group", Value: "other-team"}}

	// Send one message scoped to the target group, one to another group
	msgID1 := sendArchiveTestMessage(t, handler, "Backend team message", groupScope, agentID)
	_ = sendArchiveTestMessage(t, handler, "Other team message", otherScope, agentID)

	archiveParams, _ := json.Marshal(ArchiveRequest{
		ArchiveType: "group",
		Identifier:  "backend-team",
	})
	result, err := handler.HandleArchive(ctx, archiveParams)
	if err != nil {
		t.Fatalf("HandleArchive: %v", err)
	}
	resp := result.(*ArchiveResponse)

	if resp.ArchivedCount != 1 {
		t.Errorf("ArchivedCount = %d, want 1", resp.ArchivedCount)
	}

	lines := readJSONLLines(t, resp.ArchivePath)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line in archive, got %d", len(lines))
	}
	if id, _ := lines[0]["message_id"].(string); id != msgID1 {
		t.Errorf("archive contains message_id %q, want %q", id, msgID1)
	}

	// Verify archived message is deleted
	var count int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE message_id = ?`, msgID1,
	).Scan(&count); err != nil {
		t.Fatalf("query after archive: %v", err)
	}
	if count != 0 {
		t.Errorf("expected message to be deleted, count = %d", count)
	}

	// Verify the other-team message is still present
	var otherCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE agent_id = ?`, agentID,
	).Scan(&otherCount); err != nil {
		t.Fatalf("query other messages: %v", err)
	}
	if otherCount != 1 {
		t.Errorf("expected 1 remaining message, got %d", otherCount)
	}
}

// TestArchiveReturnsZeroWhenNoMatch verifies a zero count when no messages match.
func TestArchiveReturnsZeroWhenNoMatch(t *testing.T) {
	handler, _, _, cleanup := setupArchiveTest(t)
	defer cleanup()

	ctx := context.Background()

	archiveParams, _ := json.Marshal(ArchiveRequest{
		ArchiveType: "agent",
		Identifier:  "nonexistent-agent-id",
	})
	result, err := handler.HandleArchive(ctx, archiveParams)
	if err != nil {
		t.Fatalf("HandleArchive: %v", err)
	}
	resp := result.(*ArchiveResponse)
	if resp.ArchivedCount != 0 {
		t.Errorf("ArchivedCount = %d, want 0", resp.ArchivedCount)
	}
	if resp.ArchivePath == "" {
		t.Error("ArchivePath should not be empty even when count is 0")
	}
}

// TestArchiveErrorOnInvalidArchiveType verifies that an invalid archive_type returns an error.
func TestArchiveErrorOnInvalidArchiveType(t *testing.T) {
	handler, _, _, cleanup := setupArchiveTest(t)
	defer cleanup()

	ctx := context.Background()

	archiveParams, _ := json.Marshal(ArchiveRequest{
		ArchiveType: "invalid",
		Identifier:  "some-id",
	})
	_, err := handler.HandleArchive(ctx, archiveParams)
	if err == nil {
		t.Fatal("expected error for invalid archive_type, got nil")
	}
}

// TestArchiveErrorOnEmptyIdentifier verifies that an empty identifier returns an error.
func TestArchiveErrorOnEmptyIdentifier(t *testing.T) {
	handler, _, _, cleanup := setupArchiveTest(t)
	defer cleanup()

	ctx := context.Background()

	archiveParams, _ := json.Marshal(ArchiveRequest{
		ArchiveType: "agent",
		Identifier:  "",
	})
	_, err := handler.HandleArchive(ctx, archiveParams)
	if err == nil {
		t.Fatal("expected error for empty identifier, got nil")
	}
}

// TestArchiveFileFormat verifies that every line in the archive is valid JSON
// and contains the required fields.
func TestArchiveFileFormat(t *testing.T) {
	handler, _, agentID, cleanup := setupArchiveTest(t)
	defer cleanup()

	ctx := context.Background()

	scopes := []types.Scope{{Type: "group", Value: "squad"}}
	refs := []types.Ref{{Type: "issue", Value: "issue-42"}}

	req := SendRequest{
		Content:       "Formatted message",
		Format:        "markdown",
		Scopes:        scopes,
		Refs:          refs,
		CallerAgentID: agentID,
	}
	params, _ := json.Marshal(req)
	_, err := handler.HandleSend(ctx, params)
	if err != nil {
		t.Fatalf("send message: %v", err)
	}

	archiveParams, _ := json.Marshal(ArchiveRequest{
		ArchiveType: "agent",
		Identifier:  agentID,
	})
	result, err := handler.HandleArchive(ctx, archiveParams)
	if err != nil {
		t.Fatalf("HandleArchive: %v", err)
	}
	resp := result.(*ArchiveResponse)

	lines := readJSONLLines(t, resp.ArchivePath)
	if len(lines) != 1 {
		t.Fatalf("expected 1 archive line, got %d", len(lines))
	}
	line := lines[0]

	// Required top-level fields
	for _, field := range []string{"message_id", "agent_id", "created_at", "body", "scopes", "refs"} {
		if _, ok := line[field]; !ok {
			t.Errorf("archive line missing field %q", field)
		}
	}

	// Verify body sub-fields
	body, ok := line["body"].(map[string]any)
	if !ok {
		t.Fatal("body field is not an object")
	}
	if body["format"] != "markdown" {
		t.Errorf("body.format = %v, want \"markdown\"", body["format"])
	}
	if body["content"] != "Formatted message" {
		t.Errorf("body.content = %v, want \"Formatted message\"", body["content"])
	}

	// Verify scopes
	scopesRaw, ok := line["scopes"].([]any)
	if !ok {
		t.Fatal("scopes field is not an array")
	}
	if len(scopesRaw) != 1 {
		t.Errorf("expected 1 scope, got %d", len(scopesRaw))
	}

	// Verify refs
	refsRaw, ok := line["refs"].([]any)
	if !ok {
		t.Fatal("refs field is not an array")
	}
	if len(refsRaw) != 1 {
		t.Errorf("expected 1 ref, got %d", len(refsRaw))
	}
}

// TestArchiveMessagesDeletedAfterArchive verifies related rows are also removed.
func TestArchiveMessagesDeletedAfterArchive(t *testing.T) {
	handler, st, agentID, cleanup := setupArchiveTest(t)
	defer cleanup()

	ctx := context.Background()

	scopes := []types.Scope{{Type: "group", Value: "cleanup-squad"}}
	refs := []types.Ref{{Type: "commit", Value: "abc123"}}

	req := SendRequest{
		Content:       "Message to be hard-deleted",
		Format:        "markdown",
		Scopes:        scopes,
		Refs:          refs,
		CallerAgentID: agentID,
	}
	params, _ := json.Marshal(req)
	sendResult, err := handler.HandleSend(ctx, params)
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	msgID := sendResult.(*SendResponse).MessageID

	// Confirm related rows exist before archive
	var scopeCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM message_scopes WHERE message_id = ?`, msgID,
	).Scan(&scopeCount); err != nil {
		t.Fatalf("query scopes before archive: %v", err)
	}
	if scopeCount == 0 {
		t.Fatal("expected scopes to exist before archive")
	}

	// Archive
	archiveParams, _ := json.Marshal(ArchiveRequest{
		ArchiveType: "agent",
		Identifier:  agentID,
	})
	if _, err := handler.HandleArchive(ctx, archiveParams); err != nil {
		t.Fatalf("HandleArchive: %v", err)
	}

	// Verify message row is gone
	var msgCount int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE message_id = ?`, msgID,
	).Scan(&msgCount); err != nil {
		t.Fatalf("query messages after archive: %v", err)
	}
	if msgCount != 0 {
		t.Errorf("messages row still present after archive, count = %d", msgCount)
	}

	// Verify scopes are gone
	var scopeCountAfter int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM message_scopes WHERE message_id = ?`, msgID,
	).Scan(&scopeCountAfter); err != nil {
		t.Fatalf("query scopes after archive: %v", err)
	}
	if scopeCountAfter != 0 {
		t.Errorf("message_scopes rows still present after archive, count = %d", scopeCountAfter)
	}

	// Verify refs are gone
	var refCountAfter int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM message_refs WHERE message_id = ?`, msgID,
	).Scan(&refCountAfter); err != nil {
		t.Fatalf("query refs after archive: %v", err)
	}
	if refCountAfter != 0 {
		t.Errorf("message_refs rows still present after archive, count = %d", refCountAfter)
	}
}

// readJSONLLines reads a JSONL file and returns each line parsed as map[string]any.
func readJSONLLines(t *testing.T, path string) []map[string]any {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive file %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	var lines []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		text := scanner.Text()
		if text == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(text), &obj); err != nil {
			t.Fatalf("parse archive line %q: %v", text, err)
		}
		lines = append(lines, obj)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan archive file: %v", err)
	}
	return lines
}
