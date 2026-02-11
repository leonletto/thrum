package rpc

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/projection"
	"github.com/leonletto/thrum/internal/sync"
	_ "modernc.org/sqlite"
)

func TestSyncForceHandler_Handle(t *testing.T) {
	tmpDir := setupTestRepo(t)
	setupThrumFiles(t, tmpDir)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	syncer := sync.NewSyncer(tmpDir, syncDir, false)
	projector := setupTestProjector(t, tmpDir)
	loop := sync.NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), 10*time.Second, false)

	ctx := context.Background()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Failed to start loop: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	handler := NewSyncForceHandler(loop)

	// Call handler
	resp, err := handler.Handle(ctx, nil)
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	// Verify response
	syncResp, ok := resp.(SyncForceResponse)
	if !ok {
		t.Fatalf("Expected SyncForceResponse, got %T", resp)
	}

	if !syncResp.Triggered {
		t.Error("Expected Triggered=true")
	}

	if syncResp.SyncState == "" {
		t.Error("Expected non-empty SyncState")
	}

	// Poll until sync completes (with timeout)
	deadline := time.After(2 * time.Second)
	for {
		resp2, err := handler.Handle(ctx, nil)
		if err != nil {
			t.Fatalf("Handle failed: %v", err)
		}
		syncResp2, ok := resp2.(SyncForceResponse)
		if !ok {
			t.Fatalf("expected SyncForceResponse, got %T", resp2)
		}
		if syncResp2.LastSyncAt != "" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Expected non-empty LastSyncAt after sync")
		default:
			// Poll interval - waiting for async sync operation to complete
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestSyncStatusHandler_Handle(t *testing.T) {
	tmpDir := setupTestRepo(t)
	setupThrumFiles(t, tmpDir)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	syncer := sync.NewSyncer(tmpDir, syncDir, false)
	projector := setupTestProjector(t, tmpDir)
	loop := sync.NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), 10*time.Second, false)

	ctx := context.Background()
	handler := NewSyncStatusHandler(loop)

	// Check status before start
	resp, err := handler.Handle(ctx, nil)
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	statusResp, ok := resp.(SyncStatusResponse)
	if !ok {
		t.Fatalf("Expected SyncStatusResponse, got %T", resp)
	}

	if statusResp.Running {
		t.Error("Expected Running=false before start")
	}

	// Start loop
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Failed to start loop: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	// Poll until initial sync completes (with timeout)
	deadline := time.After(2 * time.Second)
	for {
		resp2, err := handler.Handle(ctx, nil)
		if err != nil {
			t.Fatalf("Handle failed: %v", err)
		}
		statusResp2, ok := resp2.(SyncStatusResponse)
		if !ok {
			t.Fatalf("expected SyncStatusResponse, got %T", resp2)
		}
		if statusResp2.LastSyncAt != "" {
			if !statusResp2.Running {
				t.Error("Expected Running=true after start")
			}
			if statusResp2.SyncState == "" {
				t.Error("Expected non-empty SyncState")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("Expected non-empty LastSyncAt after sync")
		default:
			// Poll interval - waiting for async sync operation to complete
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestSyncForceHandler_LocalOnlyMode(t *testing.T) {
	tmpDir := setupTestRepo(t)
	setupThrumFiles(t, tmpDir)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	// Create syncer and loop with localOnly=true
	syncer := sync.NewSyncer(tmpDir, syncDir, true)
	projector := setupTestProjector(t, tmpDir)
	loop := sync.NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), 10*time.Second, true)

	ctx := context.Background()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Failed to start loop: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	handler := NewSyncForceHandler(loop)

	// Poll until sync completes
	deadline := time.After(2 * time.Second)
	for {
		resp, err := handler.Handle(ctx, nil)
		if err != nil {
			t.Fatalf("Handle failed: %v", err)
		}
		syncResp, ok := resp.(SyncForceResponse)
		if !ok {
			t.Fatalf("Expected SyncForceResponse, got %T", resp)
		}
		if syncResp.LastSyncAt != "" {
			// Verify LocalOnly is reported
			if !syncResp.LocalOnly {
				t.Error("Expected LocalOnly=true in SyncForceResponse")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("sync did not complete")
		default:
			// Poll interval - waiting for async sync operation to complete
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestSyncStatusHandler_LocalOnlyMode(t *testing.T) {
	tmpDir := setupTestRepo(t)
	setupThrumFiles(t, tmpDir)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	// Create syncer and loop with localOnly=true
	syncer := sync.NewSyncer(tmpDir, syncDir, true)
	projector := setupTestProjector(t, tmpDir)
	loop := sync.NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), 10*time.Second, true)

	ctx := context.Background()
	handler := NewSyncStatusHandler(loop)

	// Check status before start shows local-only
	resp, err := handler.Handle(ctx, nil)
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
	statusResp, ok := resp.(SyncStatusResponse)
	if !ok {
		t.Fatalf("Expected SyncStatusResponse, got %T", resp)
	}
	if !statusResp.LocalOnly {
		t.Error("Expected LocalOnly=true in SyncStatusResponse before start")
	}

	// Start loop
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Failed to start loop: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	// Poll until sync completes
	deadline := time.After(2 * time.Second)
	for {
		resp, err := handler.Handle(ctx, nil)
		if err != nil {
			t.Fatalf("Handle failed: %v", err)
		}
		statusResp, ok := resp.(SyncStatusResponse)
		if !ok {
			t.Fatalf("Expected SyncStatusResponse, got %T", resp)
		}
		if statusResp.LastSyncAt != "" {
			if !statusResp.LocalOnly {
				t.Error("Expected LocalOnly=true in SyncStatusResponse after sync")
			}
			if !statusResp.Running {
				t.Error("Expected Running=true after start")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("sync did not complete")
		default:
			// Poll interval - waiting for async sync operation to complete
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestGetSyncState(t *testing.T) {
	tests := []struct {
		name     string
		status   sync.SyncStatus
		expected string
	}{
		{
			name:     "stopped",
			status:   sync.SyncStatus{Running: false},
			expected: "stopped",
		},
		{
			name: "error",
			status: sync.SyncStatus{
				Running:   true,
				LastError: "some error",
			},
			expected: "error",
		},
		{
			name:     "idle",
			status:   sync.SyncStatus{Running: true},
			expected: "idle",
		},
		{
			name: "synced",
			status: sync.SyncStatus{
				Running:    true,
				LastSyncAt: time.Now(),
			},
			expected: "synced",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := getSyncState(tt.status)
			if state != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, state)
			}
		})
	}
}

// setupTestRepo creates a temporary git repository for testing.
func setupTestRepo(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	// Configure git user (required for commits)
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to config git user.name: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to config git user.email: %v", err)
	}

	// Create initial commit
	testFile := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(testFile, []byte("# Test\n"), 0600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git add: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git commit: %v", err)
	}

	return tmpDir
}

// setupThrumFiles creates .thrum directory, required files, and the sync worktree.
func setupThrumFiles(t *testing.T, repoPath string) {
	t.Helper()

	thrumDir := filepath.Join(repoPath, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}

	// Create var/ directory
	if err := os.MkdirAll(filepath.Join(thrumDir, "var"), 0750); err != nil {
		t.Fatalf("failed to create .thrum/var: %v", err)
	}

	schemaPath := filepath.Join(thrumDir, "schema_version")
	if err := os.WriteFile(schemaPath, []byte("1\n"), 0600); err != nil {
		t.Fatalf("failed to write schema_version: %v", err)
	}

	// Create a-sync branch and worktree
	bm := sync.NewBranchManager(repoPath, false)
	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("failed to create a-sync branch: %v", err)
	}

	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	if err := bm.CreateSyncWorktree(syncDir); err != nil {
		t.Fatalf("failed to create sync worktree: %v", err)
	}

	// Create initial files in worktree
	if err := os.WriteFile(filepath.Join(syncDir, "events.jsonl"), []byte{}, 0600); err != nil {
		t.Fatalf("failed to create events.jsonl: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(syncDir, "messages"), 0750); err != nil {
		t.Fatalf("failed to create messages dir: %v", err)
	}
}

// setupTestProjector creates a test projector with a file-based database.
func setupTestProjector(t *testing.T, repoPath string) *projection.Projector {
	t.Helper()

	// Ensure .thrum/var directory exists
	varDir := filepath.Join(repoPath, ".thrum", "var")
	if err := os.MkdirAll(varDir, 0750); err != nil {
		t.Fatalf("Failed to create var directory: %v", err)
	}

	// Use a file-based database in .thrum/var for testing
	dbPath := filepath.Join(varDir, "messages.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Initialize schema
	if err := initTestSchema(db); err != nil {
		t.Fatalf("Failed to initialize schema: %v", err)
	}

	return projection.NewProjector(db)
}

// initTestSchema initializes the database schema for testing.
func initTestSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS agents (
		agent_id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		role TEXT NOT NULL,
		module TEXT NOT NULL,
		display TEXT,
		registered_at TEXT NOT NULL,
		last_seen_at TEXT
	);

	CREATE TABLE IF NOT EXISTS sessions (
		session_id TEXT PRIMARY KEY,
		agent_id TEXT NOT NULL,
		started_at TEXT NOT NULL,
		ended_at TEXT,
		end_reason TEXT,
		last_seen_at TEXT NOT NULL,
		FOREIGN KEY (agent_id) REFERENCES agents(agent_id)
	);

	CREATE TABLE IF NOT EXISTS threads (
		thread_id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		created_at TEXT NOT NULL,
		created_by TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS messages (
		message_id TEXT PRIMARY KEY,
		thread_id TEXT,
		agent_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT,
		deleted INTEGER DEFAULT 0,
		deleted_at TEXT,
		delete_reason TEXT,
		body_format TEXT NOT NULL,
		body_content TEXT NOT NULL,
		body_structured TEXT,
		FOREIGN KEY (thread_id) REFERENCES threads(thread_id),
		FOREIGN KEY (agent_id) REFERENCES agents(agent_id),
		FOREIGN KEY (session_id) REFERENCES sessions(session_id)
	);

	CREATE TABLE IF NOT EXISTS message_scopes (
		message_id TEXT NOT NULL,
		scope_type TEXT NOT NULL,
		scope_value TEXT NOT NULL,
		FOREIGN KEY (message_id) REFERENCES messages(message_id)
	);

	CREATE TABLE IF NOT EXISTS message_refs (
		message_id TEXT NOT NULL,
		ref_type TEXT NOT NULL,
		ref_value TEXT NOT NULL,
		FOREIGN KEY (message_id) REFERENCES messages(message_id)
	);
	`

	_, err := db.Exec(schema)
	return err
}
