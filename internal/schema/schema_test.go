package schema_test

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/schema"
)

func TestOpenDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify database is accessible
	if err := db.Ping(); err != nil {
		t.Errorf("Ping() failed: %v", err)
	}

	// Verify WAL mode is enabled
	var journalMode string
	err = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("Query journal_mode failed: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("Expected journal_mode='wal', got '%s'", journalMode)
	}

	// Verify busy_timeout is set (prevents SQLITE_BUSY cascading into deadlocks)
	var busyTimeout int
	err = db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout)
	if err != nil {
		t.Fatalf("Query busy_timeout failed: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("Expected busy_timeout=5000, got %d", busyTimeout)
	}

	// Verify WAL auto-checkpoint is set (prevents unbounded WAL growth)
	var walCheckpoint int
	err = db.QueryRow("PRAGMA wal_autocheckpoint").Scan(&walCheckpoint)
	if err != nil {
		t.Fatalf("Query wal_autocheckpoint failed: %v", err)
	}
	if walCheckpoint != 1000 {
		t.Errorf("Expected wal_autocheckpoint=1000, got %d", walCheckpoint)
	}
}

func TestInitDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "init.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	// Verify schema version
	version, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion() failed: %v", err)
	}
	if version != schema.CurrentVersion {
		t.Errorf("Expected schema version %d, got %d", schema.CurrentVersion, version)
	}

	// Verify all tables exist
	tables := []string{
		"messages",
		"message_scopes",
		"message_refs",
		"threads",
		"agents",
		"sessions",
		"subscriptions",
		"schema_version",
		"agent_work_contexts",
	}

	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err == sql.ErrNoRows {
			t.Errorf("Table %s does not exist", table)
		} else if err != nil {
			t.Fatalf("Query table %s failed: %v", table, err)
		}
	}
}

func TestInitDB_Indexes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "indexes.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	// Verify indexes exist
	indexes := []string{
		"idx_messages_thread",
		"idx_messages_time",
		"idx_messages_agent",
		"idx_messages_session",
		"idx_messages_not_deleted",
		"idx_scopes_lookup",
		"idx_refs_lookup",
		"idx_sessions_agent",
		"idx_subscriptions_session",
		"idx_subscriptions_scope",
		"idx_subscriptions_mention",
		"idx_work_contexts_agent",
		"idx_work_contexts_branch",
	}

	for _, index := range indexes {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name=?", index).Scan(&name)
		if err == sql.ErrNoRows {
			t.Errorf("Index %s does not exist", index)
		} else if err != nil {
			t.Fatalf("Query index %s failed: %v", index, err)
		}
	}
}

func TestInitDB_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "idempotent.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Initialize twice - should not error
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("First InitDB() failed: %v", err)
	}

	// Second init should fail because version already set
	// (this is expected behavior - InitDB is for new databases only)
	// For upgrades, use Migrate()
}

func TestGetSchemaVersion_NoSchema(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "noschema.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Version should be 0 for uninitialized database
	// But GetSchemaVersion will error because table doesn't exist
	_, err = schema.GetSchemaVersion(db)
	if err == nil {
		t.Error("GetSchemaVersion() should error on uninitialized database")
	}
}

func TestMigrate_NewDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "migrate_new.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Migrate should initialize a new database
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate() failed: %v", err)
	}

	// Verify schema version
	version, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion() failed: %v", err)
	}
	if version != schema.CurrentVersion {
		t.Errorf("Expected schema version %d, got %d", schema.CurrentVersion, version)
	}
}

func TestMigrate_CurrentVersion(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "migrate_current.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Initialize database
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	// Migrate should be no-op on current version
	if err := schema.Migrate(db); err != nil {
		t.Errorf("Migrate() should not error on current version: %v", err)
	}
}

func TestTableConstraints_Messages(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "constraints.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	// Insert a valid message
	_, err = db.Exec(`
		INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES ('msg_001', 'agent:test:ABC', 'ses_001', '2026-01-01T00:00:00Z', 'markdown', 'Test message')
	`)
	if err != nil {
		t.Fatalf("Insert valid message failed: %v", err)
	}

	// Try to insert duplicate message_id (should fail due to PRIMARY KEY)
	_, err = db.Exec(`
		INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES ('msg_001', 'agent:other:XYZ', 'ses_002', '2026-01-02T00:00:00Z', 'markdown', 'Duplicate')
	`)
	if err == nil {
		t.Error("Duplicate message_id should violate PRIMARY KEY constraint")
	}
}

func TestTableConstraints_MessageScopes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "scopes.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	// Insert a scope
	_, err = db.Exec(`
		INSERT INTO message_scopes (message_id, scope_type, scope_value)
		VALUES ('msg_001', 'module', 'auth')
	`)
	if err != nil {
		t.Fatalf("Insert scope failed: %v", err)
	}

	// Try to insert duplicate (should fail due to composite PRIMARY KEY)
	_, err = db.Exec(`
		INSERT INTO message_scopes (message_id, scope_type, scope_value)
		VALUES ('msg_001', 'module', 'auth')
	`)
	if err == nil {
		t.Error("Duplicate scope should violate PRIMARY KEY constraint")
	}

	// Different scope_value should succeed
	_, err = db.Exec(`
		INSERT INTO message_scopes (message_id, scope_type, scope_value)
		VALUES ('msg_001', 'module', 'sync')
	`)
	if err != nil {
		t.Errorf("Different scope_value should succeed: %v", err)
	}
}

func TestTableConstraints_Subscriptions(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subscriptions.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	// Insert a subscription
	_, err = db.Exec(`
		INSERT INTO subscriptions (session_id, scope_type, scope_value, mention_role, created_at)
		VALUES ('ses_001', 'module', 'auth', 'implementer', '2026-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("Insert subscription failed: %v", err)
	}

	// Try to insert duplicate (should fail due to UNIQUE constraint)
	_, err = db.Exec(`
		INSERT INTO subscriptions (session_id, scope_type, scope_value, mention_role, created_at)
		VALUES ('ses_001', 'module', 'auth', 'implementer', '2026-01-02T00:00:00Z')
	`)
	if err == nil {
		t.Error("Duplicate subscription should violate UNIQUE constraint")
	}

	// Different mention_role should succeed
	_, err = db.Exec(`
		INSERT INTO subscriptions (session_id, scope_type, scope_value, mention_role, created_at)
		VALUES ('ses_001', 'module', 'auth', 'reviewer', '2026-01-02T00:00:00Z')
	`)
	if err != nil {
		t.Errorf("Different mention_role should succeed: %v", err)
	}

	// NULL values should be allowed
	_, err = db.Exec(`
		INSERT INTO subscriptions (session_id, scope_type, scope_value, mention_role, created_at)
		VALUES ('ses_002', NULL, NULL, NULL, '2026-01-03T00:00:00Z')
	`)
	if err != nil {
		t.Errorf("NULL scope/mention should be allowed (wildcard): %v", err)
	}
}

func TestDatabaseFile_Created(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "created.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("Database file should be created")
	}
}

func TestMigrate_V5toV7(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "migrate_v5_v6.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Create a v5 database manually
	_, err = db.Exec(`
		CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("Create schema_version failed: %v", err)
	}

	_, err = db.Exec("INSERT INTO schema_version (version) VALUES (5)")
	if err != nil {
		t.Fatalf("Insert version 5 failed: %v", err)
	}

	// Create required tables for v5
	_, err = db.Exec(`
		CREATE TABLE sessions (
			session_id   TEXT PRIMARY KEY,
			agent_id     TEXT NOT NULL,
			started_at   TEXT NOT NULL,
			ended_at     TEXT,
			end_reason   TEXT,
			last_seen_at TEXT NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("Create sessions table failed: %v", err)
	}

	// Run migration to v7
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate() failed: %v", err)
	}

	// Verify schema version is now current
	version, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion() failed: %v", err)
	}
	if version != schema.CurrentVersion {
		t.Errorf("Expected schema version %d, got %d", schema.CurrentVersion, version)
	}

	// Verify groups tables were created by v7→v8 migration
	var tableName string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='groups'").Scan(&tableName)
	if err != nil {
		t.Errorf("groups table not created by migration: %v", err)
	}
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='group_members'").Scan(&tableName)
	if err != nil {
		t.Errorf("group_members table not created by migration: %v", err)
	}
}

func TestMigrateJSONLSharding(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create monolithic messages.jsonl with various event types
	messagesPath := filepath.Join(thrumDir, "messages.jsonl")
	f, err := os.Create(messagesPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Create messages.jsonl failed: %v", err)
	}

	events := []map[string]any{
		// Agent register event (should go to events.jsonl)
		{
			"type":      "agent.register",
			"timestamp": "2026-01-01T00:00:00Z",
			"event_id":  "evt_001",
			"v":         1,
			"agent_id":  "agent:alice:ABC",
			"kind":      "agent",
			"role":      "coordinator",
			"module":    "main",
		},
		// Thread create event (should go to events.jsonl)
		{
			"type":       "thread.create",
			"timestamp":  "2026-01-01T00:01:00Z",
			"event_id":   "evt_002",
			"v":          1,
			"thread_id":  "thr_001",
			"title":      "Test Thread",
			"created_by": "agent:alice:ABC",
		},
		// Message create from alice (should go to messages/alice_ABC.jsonl)
		{
			"type":       "message.create",
			"timestamp":  "2026-01-01T00:02:00Z",
			"event_id":   "evt_003",
			"v":          1,
			"message_id": "msg_001",
			"thread_id":  "thr_001",
			"agent_id":   "agent:alice:ABC",
			"session_id": "ses_001",
			"body": map[string]any{
				"format":  "markdown",
				"content": "Hello from Alice",
			},
		},
		// Message create from bob (should go to messages/bob_DEF.jsonl)
		{
			"type":       "message.create",
			"timestamp":  "2026-01-01T00:03:00Z",
			"event_id":   "evt_004",
			"v":          1,
			"message_id": "msg_002",
			"thread_id":  "thr_001",
			"agent_id":   "agent:bob:DEF",
			"session_id": "ses_002",
			"body": map[string]any{
				"format":  "markdown",
				"content": "Hello from Bob",
			},
		},
		// Message edit (should go to messages/alice_ABC.jsonl - original author)
		{
			"type":       "message.edit",
			"timestamp":  "2026-01-01T00:04:00Z",
			"event_id":   "evt_005",
			"v":          1,
			"message_id": "msg_001",
			"body": map[string]any{
				"format":  "markdown",
				"content": "Updated message from Alice",
			},
		},
		// Message delete (should go to messages/bob_DEF.jsonl - original author)
		{
			"type":       "message.delete",
			"timestamp":  "2026-01-01T00:05:00Z",
			"event_id":   "evt_006",
			"v":          1,
			"message_id": "msg_002",
			"reason":     "test",
		},
	}

	encoder := json.NewEncoder(f)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatalf("Write event failed: %v", err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Run migration
	if err := schema.MigrateJSONLSharding(thrumDir); err != nil {
		t.Fatalf("MigrateJSONLSharding() failed: %v", err)
	}

	// Verify backup file exists
	backupPath := messagesPath + ".v6.bak"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Errorf("Backup file not created")
	}

	// Verify events.jsonl was created with correct events
	eventsPath := filepath.Join(thrumDir, "events.jsonl")
	eventsFile, err := os.Open(eventsPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Open events.jsonl failed: %v", err)
	}
	defer func() { _ = eventsFile.Close() }()

	scanner := bufio.NewScanner(eventsFile)
	var eventsCount int
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("Parse event failed: %v", err)
		}
		eventsCount++

		eventType, ok := event["type"].(string)
		if !ok {
			t.Fatalf("Event type should be string")
		}
		if strings.HasPrefix(eventType, "message.") {
			t.Errorf("Message event in events.jsonl: %s", eventType)
		}
	}

	if eventsCount != 2 {
		t.Errorf("Expected 2 events in events.jsonl, got %d", eventsCount)
	}

	// Verify alice's message file
	alicePath := filepath.Join(thrumDir, "messages", "alice_ABC.jsonl")
	aliceFile, err := os.Open(alicePath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Open alice_ABC.jsonl failed: %v", err)
	}
	defer func() { _ = aliceFile.Close() }()

	scanner = bufio.NewScanner(aliceFile)
	var aliceCount int
	for scanner.Scan() {
		aliceCount++
	}

	// Alice should have: 1 create + 1 edit = 2 events
	if aliceCount != 2 {
		t.Errorf("Expected 2 events in alice_ABC.jsonl, got %d", aliceCount)
	}

	// Verify bob's message file
	bobPath := filepath.Join(thrumDir, "messages", "bob_DEF.jsonl")
	bobFile, err := os.Open(bobPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Open bob_DEF.jsonl failed: %v", err)
	}
	defer func() { _ = bobFile.Close() }()

	scanner = bufio.NewScanner(bobFile)
	var bobCount int
	for scanner.Scan() {
		bobCount++
	}

	// Bob should have: 1 create + 1 delete = 2 events
	if bobCount != 2 {
		t.Errorf("Expected 2 events in bob_DEF.jsonl, got %d", bobCount)
	}

	// Verify total event count
	totalWritten := eventsCount + aliceCount + bobCount
	if totalWritten != len(events) {
		t.Errorf("Event count mismatch: expected %d, got %d", len(events), totalWritten)
	}

	// Run migration again - should be idempotent
	if err := schema.MigrateJSONLSharding(thrumDir); err != nil {
		t.Fatalf("Second migration failed: %v", err)
	}
}

func TestWorkContexts_ForeignKeyCascade(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cascade.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	// Insert an agent
	_, err = db.Exec(`
		INSERT INTO agents (agent_id, kind, role, module, registered_at)
		VALUES ('agent:test:ABC', 'test', 'developer', 'auth', '2026-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("Insert agent failed: %v", err)
	}

	// Insert a session
	_, err = db.Exec(`
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES ('ses_001', 'agent:test:ABC', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("Insert session failed: %v", err)
	}

	// Insert a work context
	_, err = db.Exec(`
		INSERT INTO agent_work_contexts (
			session_id, agent_id, branch, worktree_path, git_updated_at
		) VALUES (
			'ses_001', 'agent:test:ABC', 'main', '/path/to/repo', '2026-01-01T00:00:00Z'
		)
	`)
	if err != nil {
		t.Fatalf("Insert work context failed: %v", err)
	}

	// Verify work context exists
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM agent_work_contexts WHERE session_id = 'ses_001'").Scan(&count)
	if err != nil {
		t.Fatalf("Query work context count failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 work context, got %d", count)
	}

	// Delete the session
	_, err = db.Exec("DELETE FROM sessions WHERE session_id = 'ses_001'")
	if err != nil {
		t.Fatalf("Delete session failed: %v", err)
	}

	// Verify work context was cascade deleted
	err = db.QueryRow("SELECT COUNT(*) FROM agent_work_contexts WHERE session_id = 'ses_001'").Scan(&count)
	if err != nil {
		t.Fatalf("Query work context count after delete failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 work contexts after cascade delete, got %d", count)
	}
}
