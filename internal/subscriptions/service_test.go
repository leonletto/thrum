package subscriptions_test

import (
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/internal/subscriptions"
	"github.com/leonletto/thrum/internal/types"
)

func TestSubscribe_Scope(t *testing.T) {
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

	scope := &types.Scope{Type: "module", Value: "auth"}
	sub, err := svc.Subscribe("ses_001", scope, nil, false)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	if sub.ID == 0 {
		t.Error("Expected non-zero subscription ID")
	}
	if sub.SessionID != "ses_001" {
		t.Errorf("Expected session_id='ses_001', got '%s'", sub.SessionID)
	}
	if sub.ScopeType == nil || *sub.ScopeType != "module" {
		t.Error("Expected scope_type='module'")
	}
	if sub.ScopeValue == nil || *sub.ScopeValue != "auth" {
		t.Error("Expected scope_value='auth'")
	}
	if sub.MentionRole != nil {
		t.Error("Expected mention_role to be nil")
	}
}

func TestSubscribe_MentionRole(t *testing.T) {
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

	role := "implementer"
	sub, err := svc.Subscribe("ses_001", nil, &role, false)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	if sub.MentionRole == nil || *sub.MentionRole != "implementer" {
		t.Error("Expected mention_role='implementer'")
	}
	if sub.ScopeType != nil {
		t.Error("Expected scope_type to be nil")
	}
}

func TestSubscribe_All(t *testing.T) {
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

	sub, err := svc.Subscribe("ses_001", nil, nil, true)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	if sub.ScopeType != nil {
		t.Error("Expected scope_type to be nil for 'all' subscription")
	}
	if sub.ScopeValue != nil {
		t.Error("Expected scope_value to be nil for 'all' subscription")
	}
	if sub.MentionRole != nil {
		t.Error("Expected mention_role to be nil for 'all' subscription")
	}
}

func TestSubscribe_Validation(t *testing.T) {
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

	// Should fail: no scope, mention_role, or all
	_, err = svc.Subscribe("ses_001", nil, nil, false)
	if err == nil {
		t.Error("Expected validation error when all parameters are missing")
	}
}

func TestSubscribe_Duplicate(t *testing.T) {
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

	scope := &types.Scope{Type: "module", Value: "auth"}

	// First subscription should succeed
	_, err = svc.Subscribe("ses_001", scope, nil, false)
	if err != nil {
		t.Fatalf("First Subscribe() failed: %v", err)
	}

	// Duplicate subscription should fail
	_, err = svc.Subscribe("ses_001", scope, nil, false)
	if err == nil {
		t.Error("Expected error for duplicate subscription")
	}
}

func TestUnsubscribe(t *testing.T) {
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

	scope := &types.Scope{Type: "module", Value: "auth"}
	sub, err := svc.Subscribe("ses_001", scope, nil, false)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	// Unsubscribe with correct session
	removed, err := svc.Unsubscribe(sub.ID, "ses_001")
	if err != nil {
		t.Fatalf("Unsubscribe() failed: %v", err)
	}
	if !removed {
		t.Error("Expected subscription to be removed")
	}

	// Try to unsubscribe again
	removed, err = svc.Unsubscribe(sub.ID, "ses_001")
	if err != nil {
		t.Fatalf("Second Unsubscribe() failed: %v", err)
	}
	if removed {
		t.Error("Expected no subscription to be removed on second call")
	}
}

func TestUnsubscribe_WrongSession(t *testing.T) {
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

	scope := &types.Scope{Type: "module", Value: "auth"}
	sub, err := svc.Subscribe("ses_001", scope, nil, false)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	// Try to unsubscribe with different session
	removed, err := svc.Unsubscribe(sub.ID, "ses_002")
	if err != nil {
		t.Fatalf("Unsubscribe() failed: %v", err)
	}
	if removed {
		t.Error("Should not be able to unsubscribe from another session's subscription")
	}
}

func TestList(t *testing.T) {
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

	// Create multiple subscriptions
	scope1 := &types.Scope{Type: "module", Value: "auth"}
	_, err = svc.Subscribe("ses_001", scope1, nil, false)
	if err != nil {
		t.Fatalf("Subscribe() #1 failed: %v", err)
	}

	role := "reviewer"
	_, err = svc.Subscribe("ses_001", nil, &role, false)
	if err != nil {
		t.Fatalf("Subscribe() #2 failed: %v", err)
	}

	// Create subscription for different session
	scope2 := &types.Scope{Type: "module", Value: "sync"}
	_, err = svc.Subscribe("ses_002", scope2, nil, false)
	if err != nil {
		t.Fatalf("Subscribe() #3 failed: %v", err)
	}

	// List subscriptions for ses_001
	subs, err := svc.List("ses_001")
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}

	if len(subs) != 2 {
		t.Errorf("Expected 2 subscriptions for ses_001, got %d", len(subs))
	}

	// List subscriptions for ses_002
	subs, err = svc.List("ses_002")
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}

	if len(subs) != 1 {
		t.Errorf("Expected 1 subscription for ses_002, got %d", len(subs))
	}
}

func TestList_Empty(t *testing.T) {
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

	subs, err := svc.List("ses_nonexistent")
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}

	if len(subs) != 0 {
		t.Errorf("Expected 0 subscriptions, got %d", len(subs))
	}
}
