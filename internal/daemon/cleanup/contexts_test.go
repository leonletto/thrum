package cleanup_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/cleanup"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/schema"
)

func TestCleanupStaleContexts(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	now := time.Now().UTC()

	// Insert test data
	insertAgent := func(agentID string) {
		_, err := db.Exec(`
			INSERT INTO agents (agent_id, kind, role, module, registered_at)
			VALUES (?, 'test', 'tester', 'test', ?)
		`, agentID, now.Format(time.RFC3339))
		if err != nil {
			t.Fatalf("insert agent: %v", err)
		}
	}

	insertSession := func(sessionID, agentID string, endedAt *string) {
		_, err := db.Exec(`
			INSERT INTO sessions (session_id, agent_id, started_at, ended_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?)
		`, sessionID, agentID, now.Format(time.RFC3339), endedAt, now.Format(time.RFC3339))
		if err != nil {
			t.Fatalf("insert session: %v", err)
		}
	}

	insertContext := func(sessionID, agentID string, gitUpdatedAt *string, unmergedCommits string) {
		_, err := db.Exec(`
			INSERT INTO agent_work_contexts (
				session_id, agent_id, git_updated_at, unmerged_commits
			) VALUES (?, ?, ?, ?)
		`, sessionID, agentID, gitUpdatedAt, unmergedCommits)
		if err != nil {
			t.Fatalf("insert context: %v", err)
		}
	}

	// Setup test data
	insertAgent("agent1")
	insertAgent("agent2")
	insertAgent("agent3")
	insertAgent("agent4")

	// Session 1: Active with fresh git data
	insertSession("ses1", "agent1", nil)
	gitFresh := now.Add(-1 * time.Hour).Format(time.RFC3339)
	insertContext("ses1", "agent1", &gitFresh, `[]`)

	// Session 2: Active with old git data but has unmerged commits (KEEP)
	insertSession("ses2", "agent2", nil)
	gitOld := now.Add(-48 * time.Hour).Format(time.RFC3339)
	insertContext("ses2", "agent2", &gitOld, `[{"sha":"abc123","message":"WIP"}]`)

	// Session 3: Active with old git data and no commits (DELETE)
	insertSession("ses3", "agent3", nil)
	gitVeryOld := now.Add(-72 * time.Hour).Format(time.RFC3339)
	insertContext("ses3", "agent3", &gitVeryOld, `[]`)

	// Session 4: Ended 8 days ago (DELETE)
	ended8d := now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	insertSession("ses4", "agent4", &ended8d)
	insertContext("ses4", "agent4", &gitFresh, `[]`)

	t.Run("cleanup_removes_stale", func(t *testing.T) {
		sdb := safedb.New(db)
		deleted, err := cleanup.CleanupStaleContexts(context.Background(), sdb, now)
		if err != nil {
			t.Fatalf("CleanupStaleContexts failed: %v", err)
		}

		// Should delete ses3 (old+no commits) and ses4 (ended >7d)
		if deleted != 2 {
			t.Errorf("Expected 2 contexts deleted, got %d", deleted)
		}

		// Verify remaining contexts
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM agent_work_contexts").Scan(&count)
		if err != nil {
			t.Fatalf("count contexts: %v", err)
		}

		if count != 2 {
			t.Errorf("Expected 2 contexts remaining, got %d", count)
		}

		// Verify ses1 and ses2 still exist
		var exists bool
		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM agent_work_contexts WHERE session_id = 'ses1')").Scan(&exists)
		if err != nil || !exists {
			t.Error("ses1 should still exist")
		}

		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM agent_work_contexts WHERE session_id = 'ses2')").Scan(&exists)
		if err != nil || !exists {
			t.Error("ses2 should still exist (has unmerged commits)")
		}

		// Verify ses3 and ses4 are gone
		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM agent_work_contexts WHERE session_id = 'ses3')").Scan(&exists)
		if err != nil {
			t.Fatalf("check ses3: %v", err)
		}
		if exists {
			t.Error("ses3 should be deleted (old+no commits)")
		}

		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM agent_work_contexts WHERE session_id = 'ses4')").Scan(&exists)
		if err != nil {
			t.Fatalf("check ses4: %v", err)
		}
		if exists {
			t.Error("ses4 should be deleted (session ended >7d)")
		}
	})

	t.Run("cleanup_idempotent", func(t *testing.T) {
		// Run cleanup again - should delete nothing
		sdb := safedb.New(db)
		deleted, err := cleanup.CleanupStaleContexts(context.Background(), sdb, now)
		if err != nil {
			t.Fatalf("CleanupStaleContexts failed: %v", err)
		}

		if deleted != 0 {
			t.Errorf("Expected 0 contexts deleted on second run, got %d", deleted)
		}
	})
}

func TestCleanupNoGitData(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	now := time.Now().UTC()

	// Insert agent and session
	_, err = db.Exec(`
		INSERT INTO agents (agent_id, kind, role, module, registered_at)
		VALUES ('agent1', 'test', 'tester', 'test', ?)
	`, now.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert agent: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES ('ses1', 'agent1', ?, ?)
	`, now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// Insert context with no git data
	_, err = db.Exec(`
		INSERT INTO agent_work_contexts (session_id, agent_id, git_updated_at, unmerged_commits)
		VALUES ('ses1', 'agent1', NULL, NULL)
	`)
	if err != nil {
		t.Fatalf("insert context: %v", err)
	}

	// Should delete context with no git data
	sdb := safedb.New(db)
	deleted, err := cleanup.CleanupStaleContexts(context.Background(), sdb, now)
	if err != nil {
		t.Fatalf("CleanupStaleContexts failed: %v", err)
	}

	if deleted != 1 {
		t.Errorf("Expected 1 context deleted (no git data), got %d", deleted)
	}
}

func TestFilterStaleContexts(t *testing.T) {
	now := time.Now().UTC()
	fresh := now.Add(-1 * time.Hour)
	old := now.Add(-48 * time.Hour)
	veryOld := now.Add(-8 * 24 * time.Hour)

	contexts := []cleanup.SessionWorkContext{
		// Fresh, should keep
		{
			SessionID:       "ses1",
			GitUpdatedAt:    &fresh,
			UnmergedCommits: "[]",
		},
		// Old but has commits, should keep
		{
			SessionID:       "ses2",
			GitUpdatedAt:    &old,
			UnmergedCommits: `[{"sha":"abc"}]`,
		},
		// Old + no commits, should remove
		{
			SessionID:       "ses3",
			GitUpdatedAt:    &old,
			UnmergedCommits: "[]",
		},
		// Session ended >7d, should remove
		{
			SessionID:      "ses4",
			GitUpdatedAt:   &fresh,
			SessionEndedAt: &veryOld,
		},
		// No git data, should remove
		{
			SessionID:    "ses5",
			GitUpdatedAt: nil,
		},
	}

	filtered := cleanup.FilterStaleContexts(contexts, now)

	if len(filtered) != 2 {
		t.Errorf("Expected 2 contexts kept, got %d", len(filtered))
	}

	// Verify kept contexts
	if len(filtered) >= 1 && filtered[0].SessionID != "ses1" {
		t.Errorf("Expected first kept context to be ses1, got %s", filtered[0].SessionID)
	}
	if len(filtered) >= 2 && filtered[1].SessionID != "ses2" {
		t.Errorf("Expected second kept context to be ses2, got %s", filtered[1].SessionID)
	}
}
