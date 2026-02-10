package subscriptions_test

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/internal/subscriptions"
	"github.com/leonletto/thrum/internal/types"
)

// mockNotifier captures notifications for testing.
type mockNotifier struct {
	mu            sync.Mutex
	notifications map[string][]any
}

func newMockNotifier() *mockNotifier {
	return &mockNotifier{
		notifications: make(map[string][]any),
	}
}

func (m *mockNotifier) Notify(sessionID string, notification any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifications[sessionID] = append(m.notifications[sessionID], notification)
	return nil
}

func (m *mockNotifier) GetNotifications(sessionID string) []any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.notifications[sessionID]
}

func TestDispatchForMessage_ScopeMatch(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	svc := subscriptions.NewService(db)
	dispatcher := subscriptions.NewDispatcher(db)

	// Create subscription for scope module:auth
	scope := &types.Scope{Type: "module", Value: "auth"}
	_, err = svc.Subscribe("ses_001", scope, nil, false)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	// Message with matching scope
	msg := &subscriptions.MessageInfo{
		MessageID: "msg_001",
		Scopes: []types.Scope{
			{Type: "module", Value: "auth"},
		},
	}

	matches, err := dispatcher.DispatchForMessage(msg)
	if err != nil {
		t.Fatalf("DispatchForMessage() failed: %v", err)
	}

	if len(matches) != 1 {
		t.Errorf("Expected 1 match, got %d", len(matches))
	}

	if len(matches) > 0 {
		if matches[0].SessionID != "ses_001" {
			t.Errorf("Expected session_id='ses_001', got '%s'", matches[0].SessionID)
		}
		if matches[0].MatchType != "scope" {
			t.Errorf("Expected match_type='scope', got '%s'", matches[0].MatchType)
		}
	}
}

func TestDispatchForMessage_NoMatch(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	svc := subscriptions.NewService(db)
	dispatcher := subscriptions.NewDispatcher(db)

	// Create subscription for scope module:auth
	scope := &types.Scope{Type: "module", Value: "auth"}
	_, err = svc.Subscribe("ses_001", scope, nil, false)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	// Message with different scope
	msg := &subscriptions.MessageInfo{
		MessageID: "msg_001",
		Scopes: []types.Scope{
			{Type: "module", Value: "sync"}, // different value
		},
	}

	matches, err := dispatcher.DispatchForMessage(msg)
	if err != nil {
		t.Fatalf("DispatchForMessage() failed: %v", err)
	}

	if len(matches) != 0 {
		t.Errorf("Expected 0 matches, got %d", len(matches))
	}
}

func TestDispatchForMessage_MentionMatch(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	svc := subscriptions.NewService(db)
	dispatcher := subscriptions.NewDispatcher(db)

	// Create subscription for mentions of @reviewer
	role := "reviewer"
	_, err = svc.Subscribe("ses_001", nil, &role, false)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	// Message with mention ref
	msg := &subscriptions.MessageInfo{
		MessageID: "msg_001",
		Refs: []types.Ref{
			{Type: "mention", Value: "reviewer"},
		},
	}

	matches, err := dispatcher.DispatchForMessage(msg)
	if err != nil {
		t.Fatalf("DispatchForMessage() failed: %v", err)
	}

	if len(matches) != 1 {
		t.Errorf("Expected 1 match, got %d", len(matches))
	}

	if len(matches) > 0 {
		if matches[0].MatchType != "mention" {
			t.Errorf("Expected match_type='mention', got '%s'", matches[0].MatchType)
		}
	}
}

func TestDispatchForMessage_AllMatch(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	svc := subscriptions.NewService(db)
	dispatcher := subscriptions.NewDispatcher(db)

	// Create "all" subscription
	_, err = svc.Subscribe("ses_001", nil, nil, true)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	// Any message should match
	msg := &subscriptions.MessageInfo{
		MessageID: "msg_001",
		Scopes: []types.Scope{
			{Type: "module", Value: "whatever"},
		},
	}

	matches, err := dispatcher.DispatchForMessage(msg)
	if err != nil {
		t.Fatalf("DispatchForMessage() failed: %v", err)
	}

	if len(matches) != 1 {
		t.Errorf("Expected 1 match, got %d", len(matches))
	}

	if len(matches) > 0 {
		if matches[0].MatchType != "all" {
			t.Errorf("Expected match_type='all', got '%s'", matches[0].MatchType)
		}
	}
}

func TestDispatchForMessage_MultipleMatches(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	svc := subscriptions.NewService(db)
	dispatcher := subscriptions.NewDispatcher(db)

	// Create multiple subscriptions
	scope := &types.Scope{Type: "module", Value: "auth"}
	_, err = svc.Subscribe("ses_001", scope, nil, false)
	if err != nil {
		t.Fatalf("Subscribe() #1 failed: %v", err)
	}

	_, err = svc.Subscribe("ses_002", nil, nil, true) // all subscription
	if err != nil {
		t.Fatalf("Subscribe() #2 failed: %v", err)
	}

	// Message with matching scope
	msg := &subscriptions.MessageInfo{
		MessageID: "msg_001",
		Scopes: []types.Scope{
			{Type: "module", Value: "auth"},
		},
	}

	matches, err := dispatcher.DispatchForMessage(msg)
	if err != nil {
		t.Fatalf("DispatchForMessage() failed: %v", err)
	}

	// Should match both subscriptions (scope + all)
	if len(matches) != 2 {
		t.Errorf("Expected 2 matches, got %d", len(matches))
	}

	// Verify both sessions are in the matches
	sessions := make(map[string]bool)
	for _, match := range matches {
		sessions[match.SessionID] = true
	}

	if !sessions["ses_001"] {
		t.Error("Expected ses_001 in matches")
	}
	if !sessions["ses_002"] {
		t.Error("Expected ses_002 in matches")
	}
}

func TestDispatchForMessage_MultipleScopes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	svc := subscriptions.NewService(db)
	dispatcher := subscriptions.NewDispatcher(db)

	// Create subscription for module:auth
	scope := &types.Scope{Type: "module", Value: "auth"}
	_, err = svc.Subscribe("ses_001", scope, nil, false)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	// Message with multiple scopes, one matching
	msg := &subscriptions.MessageInfo{
		MessageID: "msg_001",
		Scopes: []types.Scope{
			{Type: "module", Value: "sync"},
			{Type: "module", Value: "auth"}, // matches
			{Type: "priority", Value: "high"},
		},
	}

	matches, err := dispatcher.DispatchForMessage(msg)
	if err != nil {
		t.Fatalf("DispatchForMessage() failed: %v", err)
	}

	if len(matches) != 1 {
		t.Errorf("Expected 1 match, got %d", len(matches))
	}
}

func TestDispatchForMessage_NoSubscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	dispatcher := subscriptions.NewDispatcher(db)

	// Message with no subscriptions in DB
	msg := &subscriptions.MessageInfo{
		MessageID: "msg_001",
		Scopes: []types.Scope{
			{Type: "module", Value: "auth"},
		},
	}

	matches, err := dispatcher.DispatchForMessage(msg)
	if err != nil {
		t.Fatalf("DispatchForMessage() failed: %v", err)
	}

	if len(matches) != 0 {
		t.Errorf("Expected 0 matches, got %d", len(matches))
	}
}

func TestDispatcher_WithClientNotifier(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	svc := subscriptions.NewService(db)
	dispatcher := subscriptions.NewDispatcher(db)
	notifier := newMockNotifier()

	// Set the client notifier
	dispatcher.SetClientNotifier(notifier)

	// Create subscription
	scope := &types.Scope{Type: "module", Value: "auth"}
	sub, err := svc.Subscribe("ses_001", scope, nil, false)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	// Dispatch message with legacy agent ID format
	msg := &subscriptions.MessageInfo{
		MessageID: "msg_123",
		ThreadID:  "thread_456",
		AgentID:   "agent:reviewer:1B9K33T6RK", // Legacy format: agent:role:hash
		SessionID: "ses_001",
		Scopes: []types.Scope{
			{Type: "module", Value: "auth"},
		},
		Timestamp: "2026-01-01T12:00:00Z",
		Preview:   "This is a test message",
	}

	matches, err := dispatcher.DispatchForMessage(msg)
	if err != nil {
		t.Fatalf("DispatchForMessage() failed: %v", err)
	}

	if len(matches) != 1 {
		t.Errorf("Expected 1 match, got %d", len(matches))
	}

	// Verify notification was sent
	notifications := notifier.GetNotifications("ses_001")
	if len(notifications) != 1 {
		t.Fatalf("Expected 1 notification, got %d", len(notifications))
	}

	// Verify notification structure
	notif, ok := notifications[0].(map[string]any)
	if !ok {
		t.Fatalf("Expected notification to be map[string]any, got %T", notifications[0])
	}

	if notif["method"] != "notification.message" {
		t.Errorf("Expected method='notification.message', got %v", notif["method"])
	}

	params, ok := notif["params"].(map[string]any)
	if !ok {
		t.Fatalf("Expected params to be map[string]any, got %T", notif["params"])
	}

	if params["message_id"] != "msg_123" {
		t.Errorf("Expected message_id='msg_123', got %v", params["message_id"])
	}

	if params["thread_id"] != "thread_456" {
		t.Errorf("Expected thread_id='thread_456', got %v", params["thread_id"])
	}

	if params["preview"] != "This is a test message" {
		t.Errorf("Expected preview='This is a test message', got %v", params["preview"])
	}

	author, ok := params["author"].(struct {
		AgentID string `json:"agent_id"`
		Name    string `json:"name,omitempty"`
		Role    string `json:"role,omitempty"`
		Module  string `json:"module,omitempty"`
	})
	if !ok {
		t.Fatalf("Expected author to be struct, got %T", params["author"])
	}

	if author.AgentID != "agent:reviewer:1B9K33T6RK" {
		t.Errorf("Expected author.agent_id='agent:reviewer:1B9K33T6RK', got %v", author.AgentID)
	}

	if author.Role != "reviewer" {
		t.Errorf("Expected author.role='reviewer', got %v", author.Role)
	}

	// Module is not extractable from legacy agent ID format - would require database lookup
	if author.Module != "" {
		t.Errorf("Expected author.module='', got %v", author.Module)
	}

	matchedSub, ok := params["matched_subscription"].(map[string]any)
	if !ok {
		t.Fatalf("Expected matched_subscription to be map[string]any, got %T", params["matched_subscription"])
	}

	subID, ok := matchedSub["subscription_id"].(int)
	if !ok {
		t.Fatalf("Expected subscription_id to be int, got %T", matchedSub["subscription_id"])
	}

	if subID != sub.ID {
		t.Errorf("Expected subscription_id=%d, got %d", sub.ID, subID)
	}

	if matchedSub["match_type"] != "scope" {
		t.Errorf("Expected match_type='scope', got %v", matchedSub["match_type"])
	}
}

func TestDispatcher_PreviewTruncation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	svc := subscriptions.NewService(db)
	dispatcher := subscriptions.NewDispatcher(db)
	notifier := newMockNotifier()
	dispatcher.SetClientNotifier(notifier)

	// Create subscription
	_, err = svc.Subscribe("ses_001", nil, nil, true)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	// Message with long preview (>100 chars)
	longPreview := "This is a very long message that exceeds the 100 character limit and should be truncated in the notification preview field"
	msg := &subscriptions.MessageInfo{
		MessageID: "msg_001",
		Preview:   longPreview,
		Timestamp: "2026-01-01T12:00:00Z",
	}

	_, err = dispatcher.DispatchForMessage(msg)
	if err != nil {
		t.Fatalf("DispatchForMessage() failed: %v", err)
	}

	notifications := notifier.GetNotifications("ses_001")
	if len(notifications) != 1 {
		t.Fatalf("Expected 1 notification, got %d", len(notifications))
	}

	notif, ok := notifications[0].(map[string]any)
	if !ok {
		t.Fatalf("expected notification to be map[string]any, got %T", notifications[0])
	}
	params, ok := notif["params"].(map[string]any)
	if !ok {
		t.Fatalf("expected params to be map[string]any, got %T", notif["params"])
	}
	preview, ok := params["preview"].(string)
	if !ok {
		t.Fatalf("expected preview to be string, got %T", params["preview"])
	}

	if len(preview) != 103 { // 100 chars + "..."
		t.Errorf("Expected preview length=103, got %d", len(preview))
	}

	if preview[len(preview)-3:] != "..." {
		t.Errorf("Expected preview to end with '...', got '%s'", preview[len(preview)-3:])
	}
}

func TestDispatcher_NotificationWithoutClientNotifier(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	svc := subscriptions.NewService(db)
	dispatcher := subscriptions.NewDispatcher(db)
	// Don't set client notifier

	// Create subscription
	scope := &types.Scope{Type: "module", Value: "auth"}
	_, err = svc.Subscribe("ses_001", scope, nil, false)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	// Dispatch message - should not panic
	msg := &subscriptions.MessageInfo{
		MessageID: "msg_001",
		Scopes: []types.Scope{
			{Type: "module", Value: "auth"},
		},
	}

	matches, err := dispatcher.DispatchForMessage(msg)
	if err != nil {
		t.Fatalf("DispatchForMessage() failed: %v", err)
	}

	if len(matches) != 1 {
		t.Errorf("Expected 1 match, got %d", len(matches))
	}
}
