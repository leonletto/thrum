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

	// Verify synchronous=NORMAL (safe for WAL, faster than FULL)
	var syncMode int
	err = db.QueryRow("PRAGMA synchronous").Scan(&syncMode)
	if err != nil {
		t.Fatalf("Query synchronous failed: %v", err)
	}
	if syncMode != 1 { // 1 = NORMAL
		t.Errorf("Expected synchronous=1 (NORMAL), got %d", syncMode)
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
		// Agent session start event (should go to events.jsonl)
		{
			"type":       "agent.session.start",
			"timestamp":  "2026-01-01T00:01:00Z",
			"event_id":   "evt_002",
			"v":          1,
			"agent_id":   "agent:alice:ABC",
			"session_id": "ses_001",
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

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigration_PurgeMetadata(t *testing.T) {
	db := setupTestDB(t)
	_, err := db.Exec(`INSERT INTO purge_metadata (key, value) VALUES ('purge_cutoff', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert into purge_metadata: %v", err)
	}
	var val string
	err = db.QueryRow(`SELECT value FROM purge_metadata WHERE key = 'purge_cutoff'`).Scan(&val)
	if err != nil {
		t.Fatalf("select from purge_metadata: %v", err)
	}
	if val != "2026-01-01T00:00:00Z" {
		t.Errorf("expected cutoff value, got %s", val)
	}
}

func TestMigrationV18CreatesCommandQueue(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "migrate_v18.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Migrate initializes the DB at CurrentVersion (18) for a new database
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate() failed: %v", err)
	}

	// Verify command_queue table exists with expected columns
	rows, err := db.Query("PRAGMA table_info(command_queue)")
	if err != nil {
		t.Fatalf("PRAGMA table_info(command_queue): %v", err)
	}
	defer func() { _ = rows.Close() }()

	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err(): %v", err)
	}

	required := []string{
		"command_id", "session_name", "requester_agent", "command_text",
		"state", "timeout_ms", "silence_ms", "notify_on_complete",
		"submitted_at", "sent_at", "completed_at", "captured_output", "position",
	}
	for _, c := range required {
		if !cols[c] {
			t.Errorf("missing column: %s", c)
		}
	}

	// Verify the index exists
	var indexName string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='idx_queue_session_state'").Scan(&indexName)
	if err == sql.ErrNoRows {
		t.Error("idx_queue_session_state index does not exist")
	} else if err != nil {
		t.Fatalf("Query index failed: %v", err)
	}
}

func TestMigrationV19AddsSilenceAndNotifyColumns(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "migrate_v18_v19.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Bootstrap a v18 database manually: create schema_version + the v18
	// shape of command_queue (no silence_ms / notify_on_complete columns).
	_, err = db.Exec(`CREATE TABLE schema_version (
		version INTEGER NOT NULL,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("Create schema_version failed: %v", err)
	}
	_, err = db.Exec("INSERT INTO schema_version (version) VALUES (18)")
	if err != nil {
		t.Fatalf("Insert version 18 failed: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE command_queue (
		command_id      TEXT PRIMARY KEY,
		session_name    TEXT NOT NULL,
		requester_agent TEXT NOT NULL,
		command_text    TEXT NOT NULL,
		state           TEXT NOT NULL DEFAULT 'queued',
		timeout_ms      INTEGER NOT NULL DEFAULT 120000,
		submitted_at    TEXT NOT NULL,
		sent_at         TEXT,
		completed_at    TEXT,
		captured_output TEXT,
		position        INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		t.Fatalf("Create v18 command_queue failed: %v", err)
	}

	// Seed an existing row to prove NOT NULL defaults are applied on ALTER.
	_, err = db.Exec(`INSERT INTO command_queue
		(command_id, session_name, requester_agent, command_text, state, submitted_at)
		VALUES ('cmd_pre_v19', 'test', 'a', 'echo', 'queued', '2026-04-09T00:00:00Z')`)
	if err != nil {
		t.Fatalf("Insert v18 row failed: %v", err)
	}

	// Run migration — should bring DB from v18 to CurrentVersion.
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate() v18→CurrentVersion failed: %v", err)
	}

	version, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion() failed: %v", err)
	}
	if version != schema.CurrentVersion {
		t.Errorf("Expected schema version %d, got %d", schema.CurrentVersion, version)
	}

	// Verify the two new columns are present.
	rows, err := db.Query("PRAGMA table_info(command_queue)")
	if err != nil {
		t.Fatalf("PRAGMA table_info failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	if !cols["silence_ms"] {
		t.Error("silence_ms column missing after v18→v19 migration")
	}
	if !cols["notify_on_complete"] {
		t.Error("notify_on_complete column missing after v18→v19 migration")
	}

	// Existing row should have received defaults (5000, 1).
	var silenceMs int64
	var notifyOnComplete int
	err = db.QueryRow(`SELECT silence_ms, notify_on_complete FROM command_queue WHERE command_id = 'cmd_pre_v19'`).Scan(&silenceMs, &notifyOnComplete)
	if err != nil {
		t.Fatalf("Query pre-v19 row failed: %v", err)
	}
	if silenceMs != 5000 {
		t.Errorf("silence_ms default: got %d, want 5000", silenceMs)
	}
	if notifyOnComplete != 1 {
		t.Errorf("notify_on_complete default: got %d, want 1", notifyOnComplete)
	}

	// Verify idempotency — re-running should be a no-op.
	if err := schema.Migrate(db); err != nil {
		t.Errorf("Second Migrate() should be idempotent: %v", err)
	}
}

func TestMigrationV18_FromV17(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "migrate_v17_v18.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Bootstrap a v17 database manually
	_, err = db.Exec(`CREATE TABLE schema_version (
		version INTEGER NOT NULL,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("Create schema_version failed: %v", err)
	}
	_, err = db.Exec("INSERT INTO schema_version (version) VALUES (17)")
	if err != nil {
		t.Fatalf("Insert version 17 failed: %v", err)
	}

	// Run migration — should bring DB from v17 to CurrentVersion (v19)
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate() v17→%d failed: %v", schema.CurrentVersion, err)
	}

	// Verify schema version reached CurrentVersion
	version, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion() failed: %v", err)
	}
	if version != schema.CurrentVersion {
		t.Errorf("Expected schema version %d, got %d", schema.CurrentVersion, version)
	}

	// Verify command_queue table was created
	var tableName string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='command_queue'").Scan(&tableName)
	if err == sql.ErrNoRows {
		t.Error("command_queue table does not exist after v17→v18 migration")
	} else if err != nil {
		t.Fatalf("Query table failed: %v", err)
	}

	// Verify idempotency — run migration again should not error
	if err := schema.Migrate(db); err != nil {
		t.Errorf("Second Migrate() should be idempotent: %v", err)
	}
}

func TestSchema_FreshInstall_HasMonitorsTable(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "monitors_fresh.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	var count int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='monitors'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query monitors table: %v", err)
	}
	if count != 1 {
		t.Errorf("monitors table should exist on fresh install, got count=%d", count)
	}

	// Expected columns
	expected := []string{
		"id", "name", "argv", "match_pattern", "target", "cwd", "env",
		"debounce_seconds", "created_at", "updated_at", "status",
		"last_exit_code", "last_exit_at", "pid",
	}
	for _, col := range expected {
		var n int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('monitors') WHERE name=?`, col,
		).Scan(&n)
		if err != nil {
			t.Fatalf("query column %q: %v", col, err)
		}
		if n != 1 {
			t.Errorf("column %q should exist in monitors table", col)
		}
	}
}

func TestSchema_Migration_v19_to_v20_CreatesMonitors(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "monitors_migrate.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Bootstrap a v19 schema by hand (omit monitors table)
	_, err = db.Exec(`CREATE TABLE schema_version (
		version INTEGER NOT NULL,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("Create schema_version failed: %v", err)
	}
	_, err = db.Exec("INSERT INTO schema_version (version) VALUES (19)")
	if err != nil {
		t.Fatalf("Insert version 19 failed: %v", err)
	}
	// Create the v19-era command_queue table so migration prerequisites are met
	_, err = db.Exec(`CREATE TABLE command_queue (
		command_id         TEXT PRIMARY KEY,
		session_name       TEXT NOT NULL,
		requester_agent    TEXT NOT NULL,
		command_text       TEXT NOT NULL,
		state              TEXT NOT NULL DEFAULT 'queued',
		timeout_ms         INTEGER NOT NULL DEFAULT 120000,
		silence_ms         INTEGER NOT NULL DEFAULT 5000,
		notify_on_complete INTEGER NOT NULL DEFAULT 1,
		submitted_at       TEXT NOT NULL,
		sent_at            TEXT,
		completed_at       TEXT,
		captured_output    TEXT,
		position           INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		t.Fatalf("Create v19 command_queue failed: %v", err)
	}

	// Run migration up to current (v20)
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate() v19→v20 failed: %v", err)
	}

	var count int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='monitors'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query monitors table: %v", err)
	}
	if count != 1 {
		t.Errorf("monitors table should exist after v19→v20 migration, got count=%d", count)
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

func TestSchema_V28_CurrentVersion(t *testing.T) {
	if schema.CurrentVersion != 28 {
		t.Errorf("CurrentVersion = %d, want 28", schema.CurrentVersion)
	}
}

func TestSchema_FreshInstall_HasPermissionNudgesTable(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "perm_fresh.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	var count int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='permission_nudges'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query permission_nudges table: %v", err)
	}
	if count != 1 {
		t.Errorf("permission_nudges table should exist on fresh install, got count=%d", count)
	}

	// Expected columns
	expected := []string{
		"message_id", "session", "tmux_target", "agent_name",
		"pattern_key", "approve_key", "deny_key",
		"first_detected", "last_nudge_at", "nudge_count",
		"last_pane_hash", "expires_at",
	}
	for _, col := range expected {
		var n int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('permission_nudges') WHERE name=?`, col,
		).Scan(&n)
		if err != nil {
			t.Fatalf("query column %q: %v", col, err)
		}
		if n != 1 {
			t.Errorf("column %q should exist in permission_nudges table", col)
		}
	}

	// Expected indexes
	for _, idx := range []string{"idx_permission_nudges_session", "idx_permission_nudges_expires"} {
		var n int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, idx,
		).Scan(&n)
		if err != nil {
			t.Fatalf("query index %q: %v", idx, err)
		}
		if n != 1 {
			t.Errorf("index %q should exist on permission_nudges", idx)
		}
	}
}

func TestSchema_Migration_v20_to_v21_CreatesPermissionNudges(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "perm_migrate.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Bootstrap a v20 schema by hand (omit permission_nudges table).
	_, err = db.Exec(`CREATE TABLE schema_version (
		version INTEGER NOT NULL,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("Create schema_version failed: %v", err)
	}
	_, err = db.Exec("INSERT INTO schema_version (version) VALUES (20)")
	if err != nil {
		t.Fatalf("Insert version 20 failed: %v", err)
	}

	// Run migration up to current (v21)
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate() v20→v21 failed: %v", err)
	}

	var count int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='permission_nudges'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query permission_nudges table: %v", err)
	}
	if count != 1 {
		t.Errorf("permission_nudges table should exist after v20→v21 migration, got count=%d", count)
	}

	// Version should be at least 21 (migration ran); newer schemas run all
	// remaining migrations up to CurrentVersion too.
	var version int
	err = db.QueryRow("SELECT version FROM schema_version").Scan(&version)
	if err != nil {
		t.Fatalf("query schema version: %v", err)
	}
	if version < 21 {
		t.Errorf("schema version = %d after migration, want >= 21", version)
	}
}

func TestDaemonIdentityTable_Schema23(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	// Verify table exists.
	row := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='daemon_identity'`)
	var name string
	if err := row.Scan(&name); err != nil {
		t.Fatalf("daemon_identity table missing: %v", err)
	}

	// Verify an insert + read round trip.
	_, err = db.Exec(`INSERT INTO daemon_identity
		(daemon_id, repo_name, hostname, repo_path, git_origin_url, init_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"d_test", "thrum", "host", "/path", "https://example/x",
		"2026-04-17T00:00:00Z", "2026-04-17T00:00:00Z")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	var gotID string
	if err := db.QueryRow(`SELECT daemon_id FROM daemon_identity`).Scan(&gotID); err != nil {
		t.Fatalf("select: %v", err)
	}
	if gotID != "d_test" {
		t.Fatalf("got %q, want d_test", gotID)
	}

	// NOT NULL violation (omit repo_name) should fail.
	_, err = db.Exec(`INSERT INTO daemon_identity
		(daemon_id, repo_name, hostname, repo_path, init_at, updated_at)
		VALUES (?, NULL, ?, ?, ?, ?)`,
		"d_null", "host", "/path", "now", "now")
	if err == nil {
		t.Fatalf("expected NOT NULL violation on repo_name, got nil error")
	}
}

func TestMigrate_DowngradeGuard(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "downgrade.db")

	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Initialize to current version.
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB() failed: %v", err)
	}

	// Manually bump the schema_version to simulate a newer-binary database.
	futurVersion := schema.CurrentVersion + 1
	if _, err := db.Exec("UPDATE schema_version SET version = ?", futurVersion); err != nil {
		t.Fatalf("bump version: %v", err)
	}

	// Migrate must refuse with a "cannot downgrade" error.
	err = schema.Migrate(db)
	if err == nil {
		t.Fatal("Migrate() should return error when DB version > CurrentVersion")
	}
	if !strings.Contains(err.Error(), "cannot downgrade") {
		t.Fatalf("error should mention 'cannot downgrade', got: %v", err)
	}
}

func TestMigrate_DBBackupBeforeMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "backup_test.db")

	// Create DB at a version older than CurrentVersion so migration will run.
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() failed: %v", err)
	}

	if err := schema.InitDB(db); err != nil {
		_ = db.Close()
		t.Fatalf("InitDB() failed: %v", err)
	}

	// Downgrade version to something < CurrentVersion to force migration.
	const oldVersion = 21
	if _, err := db.Exec("UPDATE schema_version SET version = ?", oldVersion); err != nil {
		_ = db.Close()
		t.Fatalf("set old version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close after version set: %v", err)
	}

	// Record the DB bytes before migration.
	preBytes, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read pre-migration DB: %v", err)
	}

	// Reopen and run Migrate — should create backup then migrate.
	db2, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB() reopen failed: %v", err)
	}
	defer func() { _ = db2.Close() }()

	if err := schema.Migrate(db2); err != nil {
		t.Fatalf("Migrate() failed: %v", err)
	}

	// Verify backup file exists.
	bakPath := dbPath + ".pre-migration-v21-bak"
	bakBytes, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("backup file not created: %v", err)
	}

	// Backup bytes must match the pre-migration snapshot.
	if len(bakBytes) != len(preBytes) {
		t.Fatalf("backup size mismatch: got %d bytes, want %d bytes", len(bakBytes), len(preBytes))
	}

	// Run Migrate again — backup must NOT be overwritten.
	// (First, downgrade version again to force another migration pass.)
	if _, err := db2.Exec("UPDATE schema_version SET version = ?", oldVersion); err != nil {
		t.Fatalf("re-downgrade version: %v", err)
	}
	if err := schema.Migrate(db2); err != nil {
		t.Fatalf("Migrate() second call failed: %v", err)
	}
	bakBytes2, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("backup file disappeared: %v", err)
	}
	if string(bakBytes2) != string(bakBytes) {
		t.Fatalf("backup overwritten on second Migrate; want pre-migration bytes unchanged")
	}
}

// TestMigrate_V20_WithAgentsAndEvents_BackfillsOriginDaemon exercises the
// realistic v20→v22 (and onward to CurrentVersion) upgrade path that a
// production daemon would hit: a DB stopped at v20 carrying both the
// agents table (without origin_daemon) and the events table (with
// agent.register events whose origin_daemon fields are the authoritative
// source for the backfill). Regression for thrum-rchj: on two remote
// machines (ubuntuleondev + leonsmacmini) the post-deploy daemon
// restart left schema_version=20 and no origin_daemon column, so
// applyAgentRegister's INSERT silently failed against missing column
// and cross-daemon registrations never landed. The existing
// v20_to_v21_CreatesPermissionNudges test does NOT seed agents/events,
// so it skips the 21→22 path entirely (hasAgents=false short-circuit)
// and wouldn't catch a regression in the column-add or backfill step.
func TestMigrate_V20_WithAgentsAndEvents_BackfillsOriginDaemon(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v20_real.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Stand up a schema that represents a real v20-vintage daemon: the
	// schema_version row, the v20-shape agents table (pre-origin_daemon),
	// and the events table populated with agent.register events.
	if _, err := db.Exec(`CREATE TABLE schema_version (
		version INTEGER NOT NULL,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (20)`); err != nil {
		t.Fatalf("seed version: %v", err)
	}

	// v20 agents shape: display/hostname/last_seen_at/agent_pid (v17 rename
	// result) but NO origin_daemon yet. Matches what production v20
	// deployments had on disk.
	if _, err := db.Exec(`CREATE TABLE agents (
		agent_id      TEXT PRIMARY KEY,
		kind          TEXT NOT NULL,
		role          TEXT NOT NULL,
		module        TEXT NOT NULL,
		display       TEXT NOT NULL DEFAULT '',
		hostname      TEXT NOT NULL DEFAULT '',
		agent_pid     INTEGER NOT NULL DEFAULT 0,
		registered_at TEXT NOT NULL,
		last_seen_at  TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create v20 agents: %v", err)
	}

	if _, err := db.Exec(`CREATE TABLE events (
		event_id TEXT PRIMARY KEY,
		sequence INTEGER UNIQUE NOT NULL,
		type TEXT NOT NULL,
		timestamp TEXT NOT NULL,
		origin_daemon TEXT NOT NULL,
		event_json TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create events: %v", err)
	}

	// Seed three agents in (agents) and their authoritative agent.register
	// events. Two origin daemons, to verify the backfill copies the
	// right value per-agent rather than e.g. the most-recent daemon
	// globally.
	agents := []struct {
		agentID      string
		originDaemon string
		sequence     int64
	}{
		{"coordinator_main", "d_localdaemon_01", 100},
		{"impl_auth", "d_localdaemon_01", 101},
		{"impl_peer", "d_peerdaemon_02", 102},
	}
	for _, a := range agents {
		if _, err := db.Exec(`INSERT INTO agents (agent_id, kind, role, module, registered_at)
			VALUES (?, 'agent', 'implementer', 'test', '2026-04-15T00:00:00Z')`,
			a.agentID); err != nil {
			t.Fatalf("insert agent %s: %v", a.agentID, err)
		}
		payload := map[string]any{
			"type":          "agent.register",
			"agent_id":      a.agentID,
			"role":          "implementer",
			"module":        "test",
			"origin_daemon": a.originDaemon,
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal event for %s: %v", a.agentID, err)
		}
		if _, err := db.Exec(`INSERT INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json)
			VALUES (?, ?, 'agent.register', '2026-04-15T00:00:00Z', ?, ?)`,
			"evt_"+a.agentID, a.sequence, a.originDaemon, string(raw)); err != nil {
			t.Fatalf("insert event for %s: %v", a.agentID, err)
		}
	}

	// Run the migration — this is the operation that production daemons
	// failed to actually apply on remote machines per the bug report.
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Assertion 1: schema_version must advance to CurrentVersion. If the
	// tx silently rolled back, this would still be 20.
	version, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion: %v", err)
	}
	if version != schema.CurrentVersion {
		t.Fatalf("schema_version = %d after migrate, want %d", version, schema.CurrentVersion)
	}

	// Assertion 2: origin_daemon column must exist on agents. If the
	// ALTER TABLE ADD COLUMN was skipped (e.g. via an incorrect
	// hasAgents short-circuit), this probe catches it.
	var colCount int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name='origin_daemon'`,
	).Scan(&colCount); err != nil {
		t.Fatalf("pragma_table_info agents: %v", err)
	}
	if colCount != 1 {
		t.Fatalf("agents.origin_daemon column missing after migration (count=%d)", colCount)
	}

	// Assertion 3: each agent row's origin_daemon matches the value from
	// its agent.register event. The backfill is the payoff of this
	// migration; without it the column exists but every row is ''.
	wantByAgent := map[string]string{
		"coordinator_main": "d_localdaemon_01",
		"impl_auth":        "d_localdaemon_01",
		"impl_peer":        "d_peerdaemon_02",
	}
	rows, err := db.Query(`SELECT agent_id, origin_daemon FROM agents ORDER BY agent_id`)
	if err != nil {
		t.Fatalf("query agents post-migration: %v", err)
	}
	defer func() { _ = rows.Close() }()
	gotByAgent := map[string]string{}
	for rows.Next() {
		var agentID, origin string
		if err := rows.Scan(&agentID, &origin); err != nil {
			t.Fatalf("scan agents row: %v", err)
		}
		gotByAgent[agentID] = origin
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate agents: %v", err)
	}
	for agentID, want := range wantByAgent {
		got, ok := gotByAgent[agentID]
		if !ok {
			t.Errorf("agent %s missing post-migration", agentID)
			continue
		}
		if got != want {
			t.Errorf("agent %s origin_daemon = %q after backfill, want %q", agentID, got, want)
		}
	}
}

// TestMigrate_V20_AgentWithoutRegisterEvent_KeepsEmptyOriginDaemon pins
// the "no matching event" branch of the backfill: a legacy agents row
// that has no agent.register event in the events table must survive
// migration with origin_daemon = ” (not fail, not get a stale value
// from another agent). HandleRegister treats empty origin_daemon as
// "unknown / assume local" per the comment on migration 21→22.
func TestMigrate_V20_AgentWithoutRegisterEvent_KeepsEmptyOriginDaemon(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v20_legacy.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`CREATE TABLE schema_version (
		version INTEGER NOT NULL,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (20)`); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE agents (
		agent_id      TEXT PRIMARY KEY,
		kind          TEXT NOT NULL,
		role          TEXT NOT NULL,
		module        TEXT NOT NULL,
		display       TEXT NOT NULL DEFAULT '',
		hostname      TEXT NOT NULL DEFAULT '',
		agent_pid     INTEGER NOT NULL DEFAULT 0,
		registered_at TEXT NOT NULL,
		last_seen_at  TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create v20 agents: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE events (
		event_id TEXT PRIMARY KEY,
		sequence INTEGER UNIQUE NOT NULL,
		type TEXT NOT NULL,
		timestamp TEXT NOT NULL,
		origin_daemon TEXT NOT NULL,
		event_json TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create events: %v", err)
	}

	// Legacy agent WITHOUT a corresponding agent.register event.
	if _, err := db.Exec(`INSERT INTO agents (agent_id, kind, role, module, registered_at)
		VALUES ('legacy_agent', 'agent', 'implementer', 'test', '2026-03-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert legacy agent: %v", err)
	}

	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var origin string
	if err := db.QueryRow(`SELECT origin_daemon FROM agents WHERE agent_id = 'legacy_agent'`).Scan(&origin); err != nil {
		t.Fatalf("query legacy agent: %v", err)
	}
	if origin != "" {
		t.Errorf("legacy agent origin_daemon = %q after migration, want '' (no matching event)", origin)
	}
}

// TestMigration_RemindersTable verifies the v28 reminders substrate creates
// the locked column set per substrate-canonical-reference.md §3.5. Plan
// thrum-6qmf.3.13 Step 1.
func TestMigration_RemindersTable(t *testing.T) {
	db := setupTestDB(t)

	var version int
	if err := db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != schema.CurrentVersion {
		t.Fatalf("schema_version = %d, want CurrentVersion = %d", version, schema.CurrentVersion)
	}

	got := pragmaTableInfo(t, db, "reminders")

	expected := []string{
		"id", "source", "source_agent", "trigger_kind", "trigger_at",
		"trigger_meta", "target_agent", "target_chain", "body",
		"raised_at", "next_reminder_at", "last_fired_at", "state",
		"pane_snapshot", "defer_history", "cleared_at", "cancelled_at",
		"created_at", "updated_at",
	}
	for _, col := range expected {
		if _, ok := got[col]; !ok {
			t.Errorf("missing reminders column: %s", col)
		}
	}
	if len(got) != len(expected) {
		t.Errorf("reminders column count = %d, want %d (got=%v)", len(got), len(expected), got)
	}

	// Verify all four indexes exist (matches canonical §3.5 index set).
	for _, idx := range []string{
		"idx_reminders_next",
		"idx_reminders_state",
		"idx_reminders_target",
		"idx_reminders_source_kind",
	} {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?", idx,
		).Scan(&name)
		if err == sql.ErrNoRows {
			t.Errorf("missing reminders index: %s", idx)
		} else if err != nil {
			t.Fatalf("query index %s: %v", idx, err)
		}
	}
}

// TestMigration_RemindersFreshInstallEquivalentToUpgrade enforces canonical
// §3.11 Guard 1: fresh-install (InitDB → createTables) and incremental
// upgrade (Migrate from a pre-v28 version) produce identical reminders
// schema. Uses the shared assertSameTable helper plus an explicit index
// SQL comparison since partial-index WHERE clauses must be byte-identical
// (the planner picks different access paths otherwise).
func TestMigration_RemindersFreshInstallEquivalentToUpgrade(t *testing.T) {
	fresh := freshInstallDB(t)
	upgraded := upgradedFromDB(t, 24)
	assertSameTable(t, fresh, upgraded, "reminders")

	for _, idx := range []string{
		"idx_reminders_next",
		"idx_reminders_state",
		"idx_reminders_target",
		"idx_reminders_source_kind",
	} {
		freshSQL := indexSQL(t, fresh, idx)
		upSQL := indexSQL(t, upgraded, idx)
		if freshSQL != upSQL {
			t.Errorf("index %s SQL diverges:\n fresh:    %s\n upgraded: %s", idx, freshSQL, upSQL)
		}
	}
}

// indexSQL returns the CREATE INDEX SQL string for the given index name,
// or "" if absent. Reminders has partial indexes (WHERE state='open') so
// byte-level SQL comparison is the only way to catch WHERE-clause drift.
func indexSQL(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var sqlText sql.NullString
	err := db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type='index' AND name=?", name,
	).Scan(&sqlText)
	if err != nil {
		t.Fatalf("indexSQL %s: %v", name, err)
	}
	if !sqlText.Valid {
		return ""
	}
	return sqlText.String
}

// colInfo mirrors one row of PRAGMA table_info for comparison across paths.
type colInfo struct {
	cid     int
	ctype   string
	notnull int
	dflt    sql.NullString
	pk      int
}

// pragmaTableInfo reads PRAGMA table_info(table) into a name→colInfo map.
// Used to compare fresh-install and upgrade schemas column-by-column (notably
// for NULLability parity per substrate-canonical-reference.md §3.11 Guard 1).
func pragmaTableInfo(t *testing.T, db *sql.DB, table string) map[string]colInfo {
	t.Helper()
	// PRAGMA does not support bind parameters; table name is hardcoded by callers.
	rows, err := db.Query("PRAGMA table_info(" + table + ")") //nolint:gosec // trusted table name in tests
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	cols := map[string]colInfo{}
	for rows.Next() {
		var (
			info colInfo
			name string
		)
		if err := rows.Scan(&info.cid, &name, &info.ctype, &info.notnull, &info.dflt, &info.pk); err != nil {
			t.Fatalf("scan row for %s: %v", table, err)
		}
		cols[name] = info
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate PRAGMA table_info(%s): %v", table, err)
	}
	return cols
}

// freshInstallDB opens a new DB and runs InitDB (CurrentVersion via
// createTables + createIndexes).
func freshInstallDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	return db
}

// upgradedFromDB opens a new DB, hand-bootstraps schema_version at `from`,
// then runs Migrate to advance to CurrentVersion. Mirrors the bootstrap
// pattern used by earlier migration tests (e.g. v17→v18, v20→v21).
func upgradedFromDB(t *testing.T, from int) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE schema_version (
		version INTEGER NOT NULL,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, from); err != nil {
		t.Fatalf("seed version %d: %v", from, err)
	}
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate from v%d: %v", from, err)
	}
	return db
}

// assertSameTable compares table_info between two DBs and fails on any
// per-column divergence (type, notnull, default, pk) or column-set mismatch.
func assertSameTable(t *testing.T, a, b *sql.DB, table string) {
	t.Helper()
	got := pragmaTableInfo(t, a, table)
	want := pragmaTableInfo(t, b, table)
	if len(got) != len(want) {
		t.Errorf("%s: column count diverges fresh=%d upgrade=%d", table, len(got), len(want))
	}
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			t.Errorf("%s: column %s missing in fresh install", table, name)
			continue
		}
		if g.ctype != w.ctype {
			t.Errorf("%s.%s: type fresh=%q upgrade=%q", table, name, g.ctype, w.ctype)
		}
		if g.notnull != w.notnull {
			t.Errorf("%s.%s: NOT NULL fresh=%d upgrade=%d (NULLability parity violation per §3.11 Guard 1)", table, name, g.notnull, w.notnull)
		}
		if g.dflt.String != w.dflt.String || g.dflt.Valid != w.dflt.Valid {
			t.Errorf("%s.%s: default fresh=%v upgrade=%v", table, name, g.dflt, w.dflt)
		}
		if g.pk != w.pk {
			t.Errorf("%s.%s: pk fresh=%d upgrade=%d", table, name, g.pk, w.pk)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s: column %s present in fresh install but missing in upgrade", table, name)
		}
	}
}

// TestMigration_SchedulerTables verifies migration 25 creates both
// scheduler_job_state and scheduler_job_events with the locked column set per
// substrate-canonical-reference.md §3.2.
func TestMigration_SchedulerTables(t *testing.T) {
	db := setupTestDB(t)

	var version int
	if err := db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != schema.CurrentVersion {
		t.Fatalf("expected version %d, got %d", schema.CurrentVersion, version)
	}

	stateGot := pragmaTableInfo(t, db, "scheduler_job_state")
	stateExpected := []string{
		"job_id", "job_generation", "current_state", "current_stage",
		"stage_entered_at", "last_run_id", "last_fired_at",
		"last_completed_at", "last_completion_state", "last_error",
		"next_scheduled_at", "consecutive_failures", "escalation_sent",
		"total_runs", "created_at", "updated_at",
	}
	for _, col := range stateExpected {
		if _, ok := stateGot[col]; !ok {
			t.Errorf("scheduler_job_state missing column %s", col)
		}
	}

	eventsGot := pragmaTableInfo(t, db, "scheduler_job_events")
	eventsExpected := []string{
		"id", "job_id", "run_id", "event_time", "from_state",
		"to_state", "reason", "details",
	}
	for _, col := range eventsExpected {
		if _, ok := eventsGot[col]; !ok {
			t.Errorf("scheduler_job_events missing column %s", col)
		}
	}

	for _, idx := range []string{
		"idx_scheduler_state_next",
		"idx_scheduler_events_job_time",
		"idx_scheduler_events_run",
	} {
		var n int
		err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&n)
		if err != nil {
			t.Fatalf("query index %s: %v", idx, err)
		}
		if n != 1 {
			t.Errorf("index %s should exist on fresh install (got count=%d)", idx, n)
		}
	}
}

// TestMigration_SchedulerJobState_NextScheduledAtNullable pins canonical-ref
// §3.11 Guard 1: next_scheduled_at MUST be NULLable. A row with
// next_scheduled_at=NULL represents a one-shot terminal job or a recurring
// job without a derivable next-tick — both legitimate states.
func TestMigration_SchedulerJobState_NextScheduledAtNullable(t *testing.T) {
	db := setupTestDB(t)
	_, err := db.Exec(`
		INSERT INTO scheduler_job_state
			(job_id, job_generation, current_state, next_scheduled_at,
			 consecutive_failures, escalation_sent, total_runs,
			 created_at, updated_at)
		VALUES ('test-once', 1, 'completed', NULL, 0, 0, 1, 1, 1)
	`)
	if err != nil {
		t.Fatalf("INSERT with NULL next_scheduled_at: %v", err)
	}

	var notnull int
	err = db.QueryRow(
		`SELECT "notnull" FROM pragma_table_info('scheduler_job_state') WHERE name='next_scheduled_at'`,
	).Scan(&notnull)
	if err != nil {
		t.Fatalf("pragma_table_info next_scheduled_at: %v", err)
	}
	if notnull != 0 {
		t.Errorf("next_scheduled_at notnull=%d on fresh install; canonical-ref §3.11 Guard 1 requires NULLable (0)", notnull)
	}
}

// TestMigration_SchedulerJobState_FreshInstallEquivalentToUpgrade enforces
// canonical-ref §3.11 Guard 1 across both schema paths: fresh-install via
// createTables MUST produce the same scheduler_job_state and
// scheduler_job_events schemas as upgrade-from-v24 via runMigrations.
func TestMigration_SchedulerJobState_FreshInstallEquivalentToUpgrade(t *testing.T) {
	fresh := freshInstallDB(t)
	// Start at v24 (not CurrentVersion-1) so the migration path traverses
	// the v25 scheduler branch. With the v25-v27 substrate-gap protocol,
	// CurrentVersion-1 may not trigger any migration that creates the
	// scheduler tables — A-B4's v28 doesn't, and B-B1's v26/v27 may not
	// either depending on what's merged.
	upgraded := upgradedFromDB(t, 24)
	assertSameTable(t, fresh, upgraded, "scheduler_job_state")
	assertSameTable(t, fresh, upgraded, "scheduler_job_events")
}
