package schema_test

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		// v47 forward-port (thrum-399av): idx_messages_time was replaced by the
		// idx_messages_time_id composite keyset index on fresh DBs.
		"idx_messages_time_id",
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

	// v47 forward-port (thrum-399av): idx_messages_time was replaced by
	// idx_messages_time_id and must NOT exist on a fresh v51 DB. Guards against
	// the dropped index silently reappearing in createIndexes.
	var stale string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='idx_messages_time'").Scan(&stale)
	if err == nil {
		t.Error("idx_messages_time should NOT exist on a fresh v51 DB (replaced by idx_messages_time_id)")
	} else if err != sql.ErrNoRows {
		t.Fatalf("Query idx_messages_time failed: %v", err)
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

	// Expected columns (schedule added in v39 — thrum-puhr.9).
	expected := []string{
		"id", "name", "argv", "match_pattern", "target", "cwd", "env",
		"debounce_seconds", "created_at", "updated_at", "status",
		"last_exit_code", "last_exit_at", "pid", "schedule",
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

func TestSchema_V51_CurrentVersion(t *testing.T) {
	if schema.CurrentVersion != 51 {
		t.Errorf("CurrentVersion = %d, want 51 (v40 read-state marker + v41–v51 dead-end DDL forward-port from thrum-agents per thrum-399av)", schema.CurrentVersion)
	}
	// The read-state crossing constant stays at the v40 marker version — the
	// state.NewState gate compares the pre-migration version against it, and the
	// v40 backfill must NOT re-fire on a v40→v51 upgrade. Forward-porting the
	// schema does not move the read-state boundary.
	if schema.SchemaVersionReadState != 40 {
		t.Errorf("SchemaVersionReadState = %d, want 40 (unchanged by the v51 forward-port)", schema.SchemaVersionReadState)
	}
}

// TestSchema_V36_AgentAPIErrorRemediation pins the thrum-sdzk v36 table on
// BOTH paths (canonical-ref §3.11 Guard-1): fresh install (createTables) and
// upgrade (the v36 migration block). The shared DDL const makes drift
// impossible, but this guards the wiring — that the const is actually
// referenced in both places.
func TestSchema_V36_AgentAPIErrorRemediation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v36.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	tableExists := func() bool {
		var n int
		if qErr := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agent_api_error_remediation'`,
		).Scan(&n); qErr != nil {
			t.Fatalf("query table: %v", qErr)
		}
		return n == 1
	}

	// Fresh-install path.
	if !tableExists() {
		t.Fatal("fresh install missing agent_api_error_remediation table (createTables path)")
	}

	// Expected columns (NULL/DEFAULT shape lives in the shared const).
	expected := map[string]bool{
		"agent_name": false, "last_nudge_at": false, "consecutive_nudge_count": false,
		"last_error": false, "escalation_sent": false, "updated_at": false,
	}
	rows, err := db.Query("PRAGMA table_info(agent_api_error_remediation)")
	if err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if _, ok := expected[name]; ok {
			expected[name] = true
		}
	}
	_ = rows.Close()
	for col, seen := range expected {
		if !seen {
			t.Errorf("agent_api_error_remediation missing column %q", col)
		}
	}

	// Migration-block path: drop the table, rewind to v35, migrate — the v36
	// block must recreate it (proves the migration, not just createTables).
	if _, err := db.Exec("DROP TABLE agent_api_error_remediation"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := db.Exec("UPDATE schema_version SET version = 35"); err != nil {
		t.Fatalf("rewind to v35: %v", err)
	}
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate v35→v36: %v", err)
	}
	if !tableExists() {
		t.Fatal("v36 migration block did not recreate agent_api_error_remediation table")
	}
}

// TestSchema_V35_EventKindCheck_FreshInstall pins the thrum-6qmf.17 v35
// event_kind CHECK on the fresh-install path (createTables). A bogus
// event_kind must be rejected — proving Guard-1 parity (fresh installs carry
// the same constraint the v35 rebuild adds to upgraded DBs).
func TestSchema_V35_EventKindCheck_FreshInstall(t *testing.T) {
	db := setupTestDB(t) // fresh install at CurrentVersion via createTables
	_, err := db.Exec(
		`INSERT INTO agent_lifecycle_events (agent_name, event_kind, event_time) VALUES ('a', 'bogus_kind', 1)`)
	if err == nil {
		t.Fatal("fresh-install agent_lifecycle_events accepted a bogus event_kind; CHECK missing (Guard-1 parity broken)")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "CONSTRAINT") {
		t.Errorf("expected a CHECK constraint error, got: %v", err)
	}
	// A valid kind still inserts.
	if _, err := db.Exec(
		`INSERT INTO agent_lifecycle_events (agent_name, event_kind, event_time) VALUES ('a', 'crash_detected', 1)`); err != nil {
		t.Errorf("valid event_kind should insert: %v", err)
	}
}

// TestSchema_V35_RebuildPreservesRows builds a pre-CHECK agent_lifecycle_events
// (the original v27 shape, no event_kind CHECK) with rows covering all 7 kinds,
// then migrates v34→36 and asserts the rebuild preserved every row + id and
// that the CHECK is now live.
func TestSchema_V35_RebuildPreservesRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v35rebuild.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Hand-build the OLD (pre-CHECK) table + a v34 schema_version marker.
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("schema_version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (34)`); err != nil {
		t.Fatalf("seed v34: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE agent_lifecycle_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		agent_name TEXT NOT NULL,
		event_kind TEXT NOT NULL,
		event_time INTEGER NOT NULL,
		detection_method TEXT,
		reason TEXT,
		details TEXT
	)`); err != nil {
		t.Fatalf("create old table: %v", err)
	}

	kinds := []string{
		"respawn_fired", "respawn_skipped_loopguard", "crash_detected",
		"state_md_parse_failed", "state_md_ack_cleared", "respawn_ack_cleared",
		"reconcile_worktree_discrepancy",
	}
	for i, k := range kinds {
		// Mix detection_method set vs NULL across rows.
		if i%2 == 0 {
			if _, err := db.Exec(
				`INSERT INTO agent_lifecycle_events (agent_name, event_kind, event_time, detection_method) VALUES (?, ?, ?, 'health_check_tick')`,
				"impl_"+k, k, 1000+i); err != nil {
				t.Fatalf("seed row %s: %v", k, err)
			}
		} else {
			if _, err := db.Exec(
				`INSERT INTO agent_lifecycle_events (agent_name, event_kind, event_time) VALUES (?, ?, ?)`,
				"impl_"+k, k, 1000+i); err != nil {
				t.Fatalf("seed row %s: %v", k, err)
			}
		}
	}

	var preCount, preMaxID int
	_ = db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(id),0) FROM agent_lifecycle_events`).Scan(&preCount, &preMaxID)

	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate v34→36: %v", err)
	}

	var postCount, postMaxID int
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(id),0) FROM agent_lifecycle_events`).Scan(&postCount, &postMaxID); err != nil {
		t.Fatalf("post-migrate count: %v", err)
	}
	if postCount != preCount || postCount != len(kinds) {
		t.Errorf("row count changed: pre=%d post=%d want=%d", preCount, postCount, len(kinds))
	}
	if postMaxID != preMaxID {
		t.Errorf("MAX(id) not preserved: pre=%d post=%d", preMaxID, postMaxID)
	}

	// Sample a row intact (id 1 = first kind, detection_method set).
	var agent, kind string
	var dm sql.NullString
	if err := db.QueryRow(`SELECT agent_name, event_kind, detection_method FROM agent_lifecycle_events WHERE id = 1`).Scan(&agent, &kind, &dm); err != nil {
		t.Fatalf("sample row: %v", err)
	}
	if agent != "impl_respawn_fired" || kind != "respawn_fired" || !dm.Valid || dm.String != "health_check_tick" {
		t.Errorf("row 1 not preserved intact: agent=%q kind=%q dm=%v", agent, kind, dm)
	}

	// The CHECK is now live on the rebuilt table.
	if _, err := db.Exec(
		`INSERT INTO agent_lifecycle_events (agent_name, event_kind, event_time) VALUES ('x', 'bogus_kind', 1)`); err == nil {
		t.Error("post-migration bogus event_kind accepted; v35 rebuild did not add the CHECK")
	}
}

// TestSchema_GapFill_V32_to_V36 is the end-to-end proof of the v0.10.6 dead-end
// gap-fill: seed a bare v32 DB carrying only the tables the 33-36 blocks touch
// (messages for v33, agent_lifecycle_events for the v35 rebuild), run Migrate,
// and assert the DB reaches v36 with all four new schema shapes live — the
// pending_route_resolution column, the memories table, the agent_lifecycle
// event_kind CHECK, and the agent_api_error_remediation table.
func TestSchema_GapFill_V32_to_V36(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gapfill_v32_v36.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Bare v32 fixture: schema_version=32 + the pre-v33 shapes the migration
	// blocks reference. messages WITHOUT pending_route_resolution (v33 adds it);
	// agent_lifecycle_events in the pre-CHECK v27 shape (v35 rebuilds it).
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("schema_version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (32)`); err != nil {
		t.Fatalf("seed v32: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE messages (
		message_id   TEXT PRIMARY KEY,
		agent_id     TEXT NOT NULL,
		session_id   TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		body_format  TEXT NOT NULL,
		body_content TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create messages: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE agent_lifecycle_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		agent_name TEXT NOT NULL,
		event_kind TEXT NOT NULL,
		event_time INTEGER NOT NULL,
		detection_method TEXT,
		reason TEXT,
		details TEXT
	)`); err != nil {
		t.Fatalf("create agent_lifecycle_events: %v", err)
	}

	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate v32→v36: %v", err)
	}

	v, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion: %v", err)
	}
	if v != schema.CurrentVersion {
		t.Fatalf("schema_version after gap-fill migrate = %d; want %d (schema.CurrentVersion)", v, schema.CurrentVersion)
	}

	// v33: pending_route_resolution column present on messages.
	var colN int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name='pending_route_resolution'`,
	).Scan(&colN); err != nil {
		t.Fatalf("query messages column: %v", err)
	}
	if colN != 1 {
		t.Error("v33 did not add pending_route_resolution column to messages")
	}

	// v34: memories table present.
	var memN int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='memories'`,
	).Scan(&memN); err != nil {
		t.Fatalf("query memories table: %v", err)
	}
	if memN != 1 {
		t.Error("v34 did not create memories table")
	}

	// v35: event_kind CHECK live on the rebuilt agent_lifecycle_events.
	if _, err := db.Exec(
		`INSERT INTO agent_lifecycle_events (agent_name, event_kind, event_time) VALUES ('x', 'bogus_kind', 1)`); err == nil {
		t.Error("v35 rebuild did not add the event_kind CHECK (bogus kind accepted)")
	}

	// v36: agent_api_error_remediation table present.
	var remN int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agent_api_error_remediation'`,
	).Scan(&remN); err != nil {
		t.Fatalf("query remediation table: %v", err)
	}
	if remN != 1 {
		t.Error("v36 did not create agent_api_error_remediation table")
	}
}

// seedBareV32DB writes a minimal on-disk v32 fixture (schema_version=32 only)
// at dbPath and returns the open handle. All v33-v36 blocks are
// bare-fixture-tolerant, so Migrate reaches v36 from this seed.
func seedBareV32DB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("schema_version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (32)`); err != nil {
		t.Fatalf("seed v32: %v", err)
	}
	return db
}

// TestSchema_Migrate_BackupFailureHalts proves the RC1 hardening: when the
// pre-migration backup cannot be written (here, a read-only DB directory), the
// migration aborts — Migrate returns an error AND the on-disk schema version
// is unchanged, so no partial migration ran. The DB is not rebuildable from
// JSONL, so refusing to migrate without a recovery snapshot is the safe default.
func TestSchema_Migrate_BackupFailureHalts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rodir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbPath := filepath.Join(dir, "state.db")
	db := seedBareV32DB(t, dbPath)
	defer func() { _ = db.Close() }()

	// Make the DB's directory unwritable so the backup WriteFile fails while
	// the source DB stays readable. Restore perms before TempDir cleanup.
	// Directory perms need the execute bit, so 0o500/0o700 are intentional here
	// (gosec G302 targets file perms; this is a directory in a TempDir test).
	if err := os.Chmod(dir, 0o500); err != nil { // #nosec G302 -- read-only dir for a backup-failure test
		t.Fatalf("chmod ro: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }() // #nosec G302 -- restore dir perms for TempDir cleanup

	err := schema.Migrate(db)
	if err == nil {
		t.Fatal("Migrate should have failed when the pre-migration backup could not be written")
	}
	if !strings.Contains(err.Error(), "pre-migration DB backup failed") {
		t.Errorf("expected backup-failure halt error, got: %v", err)
	}

	// Schema version must be UNCHANGED — no partial migration.
	v, gErr := schema.GetSchemaVersion(db)
	if gErr != nil {
		t.Fatalf("GetSchemaVersion: %v", gErr)
	}
	if v != 32 {
		t.Errorf("schema version changed despite halted migration: got %d, want 32", v)
	}
}

// TestSchema_Migrate_TimestampedBackups proves the RC1 hardening's timestamped,
// always-fresh naming: each migration event writes a backup matching
// .pre-migration-v<N>-<UTC>.bak, and two sequential migrations of fresh
// fixtures produce two DISTINCT, lexically-ordered (== chronologically ordered)
// backup files — never skipped by a stale backup-once snapshot.
func TestSchema_Migrate_TimestampedBackups(t *testing.T) {
	backupOf := func(dbPath string) string {
		t.Helper()
		matches, err := filepath.Glob(dbPath + ".pre-migration-v32-*.bak")
		if err != nil {
			t.Fatalf("glob: %v", err)
		}
		if len(matches) != 1 {
			t.Fatalf("expected exactly 1 timestamped backup for %s, got %d: %v", dbPath, len(matches), matches)
		}
		return filepath.Base(matches[0])
	}

	// First fresh v32 fixture → migrate → one timestamped backup.
	dbA := filepath.Join(t.TempDir(), "state.db")
	dbaHandle := seedBareV32DB(t, dbA)
	if err := schema.Migrate(dbaHandle); err != nil {
		_ = dbaHandle.Close()
		t.Fatalf("Migrate A: %v", err)
	}
	_ = dbaHandle.Close()
	nameA := backupOf(dbA)

	// Ensure the second migration lands in a strictly later UTC second so the
	// timestamps (and thus the lexical order) differ deterministically.
	time.Sleep(1100 * time.Millisecond)

	// Second fresh v32 fixture (same db basename, different dir) → migrate.
	dbB := filepath.Join(t.TempDir(), "state.db")
	dbbHandle := seedBareV32DB(t, dbB)
	if err := schema.Migrate(dbbHandle); err != nil {
		_ = dbbHandle.Close()
		t.Fatalf("Migrate B: %v", err)
	}
	_ = dbbHandle.Close()
	nameB := backupOf(dbB)

	// Pattern check: .pre-migration-v32-<UTC>.bak (UTC form 20060102T150405Z).
	for _, n := range []string{nameA, nameB} {
		if !strings.HasPrefix(n, "state.db.pre-migration-v32-") || !strings.HasSuffix(n, "Z.bak") {
			t.Errorf("backup name %q does not match .pre-migration-v32-<UTC>.bak", n)
		}
	}
	// Distinct + lexically ordered (timestamp form is lexically == chronologically sortable).
	if nameA == nameB {
		t.Errorf("two sequential migrations produced identical backup names: %q", nameA)
	}
	if nameA >= nameB {
		t.Errorf("backup names not lexically (chronologically) ordered: A=%q B=%q", nameA, nameB)
	}
}

// TestSchema_ForwardPort_V25_to_V32_AllTablesPresent exercises the
// forward-ported migrations end-to-end. Initializes a DB at v24's
// shape (telegram_msg_map being the last table added pre-forward-port),
// stamps schema_version to 24, then runs Migrate and asserts the 7
// new tables + agents-table column additions all reached the live DB.
// This is the smoke that proves the v25-v32 blocks fire in sequence
// without conflict and that createTables/runMigrations agree on the
// final shape (canonical §3.11 Guard 1 fresh-vs-upgrade parity).
func TestSchema_ForwardPort_V25_to_V32_AllTablesPresent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v24_to_v32.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Bootstrap a fresh DB then rewind schema_version to v24 so the
	// migration runner replays 25-32. (We can't easily create a "true"
	// v24-shaped DB on this branch since createTables now includes the
	// v32 tables; the rewind exercises the migration code path that
	// existing-DB users will hit, and the IF NOT EXISTS guards make
	// the CREATEs idempotent.)
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	if _, err := db.Exec("UPDATE schema_version SET version = 24"); err != nil {
		t.Fatalf("rewind to v24: %v", err)
	}

	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate v24→v36: %v", err)
	}

	v, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion: %v", err)
	}
	if v != schema.CurrentVersion {
		t.Errorf("schema_version after migrate = %d; want %d (schema.CurrentVersion)", v, schema.CurrentVersion)
	}

	// All forward-ported tables must be present (v25-v36).
	for _, tbl := range []string{
		"scheduler_job_state",
		"scheduler_job_events",
		"agent_lifecycle_events",
		"reminders",
		"email_msg_seen",
		"email_outbound_queue",
		"email_peer_rate_state",
		"memories",
		"memory_scopes",
		"agent_api_error_remediation",
	} {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing post-migrate: %v", tbl, err)
		}
	}

	// agents table must carry the v26 column additions.
	cols, err := db.Query("PRAGMA table_info(agents)")
	if err != nil {
		t.Fatalf("PRAGMA agents: %v", err)
	}
	defer func() { _ = cols.Close() }()
	have := map[string]bool{}
	for cols.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := cols.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		have[name] = true
	}
	for _, c := range []string{
		"mode",
		"identity",
		"auto_respawn_enabled",
		"auto_respawn_disabled_at",
		"state_md_parse_failed_at",
		"last_pane_alive_at",
	} {
		if !have[c] {
			t.Errorf("agents column %q missing post-migrate", c)
		}
	}
}

// TestSchema_FreshInstall_HasForwardPortedTables proves
// createTables/runMigrations agree on the v32 end-state for a brand-new
// DB (no pre-existing rows). All 7 forward-ported tables must exist
// after InitDB with no migration replay needed.
func TestSchema_FreshInstall_HasForwardPortedTables(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "fresh_v32.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	v, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion: %v", err)
	}
	if v != schema.CurrentVersion {
		t.Errorf("fresh install schema_version = %d; want %d (schema.CurrentVersion)", v, schema.CurrentVersion)
	}
	for _, tbl := range []string{
		"scheduler_job_state",
		"scheduler_job_events",
		"agent_lifecycle_events",
		"reminders",
		"email_msg_seen",
		"email_outbound_queue",
		"email_peer_rate_state",
		"memories",
		"memory_scopes",
		"agent_api_error_remediation",
	} {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&name)
		if err != nil {
			t.Errorf("fresh-install missing forward-ported table %q: %v", tbl, err)
		}
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
	errStr := err.Error()
	if !strings.Contains(errStr, "cannot downgrade") {
		t.Fatalf("error should mention 'cannot downgrade', got: %v", err)
	}
	// thrum-quth: the user-facing error must carry the version pair AND
	// both recovery paths. Pin each so future refactors can't silently
	// drop the help the operator depends on.
	if !strings.Contains(errStr, fmt.Sprintf("version %d", futurVersion)) {
		t.Errorf("error should mention DB version %d; got: %v", futurVersion, err)
	}
	if !strings.Contains(errStr, fmt.Sprintf("supports up to %d", schema.CurrentVersion)) {
		t.Errorf("error should mention binary max version %d; got: %v", schema.CurrentVersion, err)
	}
	if !strings.Contains(errStr, "Re-install a newer binary") {
		t.Errorf("error should include the reinstall recovery hint; got: %v", err)
	}
	if !strings.Contains(errStr, "make install") {
		t.Errorf("error should include the concrete 'make install' command; got: %v", err)
	}
	if !strings.Contains(errStr, "thrum daemon stop") {
		t.Errorf("error should instruct stopping the daemon before rm so file locks release; got: %v", err)
	}
	if !strings.Contains(errStr, "LOSES local message history") {
		t.Errorf("error should warn that rm-the-DB destroys local history; got: %v", err)
	}
	if !strings.Contains(errStr, dbPath) {
		t.Errorf("error should include the on-disk DB path %q so user knows what to rm; got: %v", dbPath, err)
	}
	if !strings.Contains(errStr, "Multi-binary worktree footgun") {
		t.Errorf("error should point at the CLAUDE.md prevention section; got: %v", err)
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

	// Verify exactly one timestamped backup exists (RC1 hardening: the suffix
	// is now .pre-migration-v<N>-<UTC>.bak, not the old fixed -bak form).
	v21Backups := func() []string {
		m, gErr := filepath.Glob(dbPath + ".pre-migration-v21-*.bak")
		if gErr != nil {
			t.Fatalf("glob: %v", gErr)
		}
		return m
	}
	first := v21Backups()
	if len(first) != 1 {
		t.Fatalf("expected exactly 1 timestamped backup after first Migrate, got %d: %v", len(first), first)
	}

	// Backup bytes must match the pre-migration snapshot.
	bakBytes, err := os.ReadFile(first[0])
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if len(bakBytes) != len(preBytes) {
		t.Fatalf("backup size mismatch: got %d bytes, want %d bytes", len(bakBytes), len(preBytes))
	}

	// Run Migrate again after re-downgrading — RC1 hardening makes each
	// migration event ALWAYS-FRESH, so a SECOND, distinct timestamped backup
	// must appear (the old backup-once/never-overwrite behavior is gone). Sleep
	// past the UTC-second boundary so the second timestamp differs.
	if _, err := db2.Exec("UPDATE schema_version SET version = ?", oldVersion); err != nil {
		t.Fatalf("re-downgrade version: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := schema.Migrate(db2); err != nil {
		t.Fatalf("Migrate() second call failed: %v", err)
	}
	second := v21Backups()
	if len(second) != 2 {
		t.Fatalf("expected 2 distinct timestamped backups after second Migrate (always-fresh), got %d: %v", len(second), second)
	}
	// The original snapshot must still be intact among them.
	stillBytes, err := os.ReadFile(first[0])
	if err != nil {
		t.Fatalf("original backup disappeared: %v", err)
	}
	if string(stillBytes) != string(bakBytes) {
		t.Fatalf("original pre-migration snapshot was clobbered; want bytes unchanged")
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

// --- thrum-7ojv: v37 placeholder + v38 events.timestamp index ----------

// indexExists returns true if the named index is present in sqlite_master.
// Helper for the thrum-7ojv migration tests.
func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name = ?`, name,
	).Scan(&n); err != nil {
		t.Fatalf("query sqlite_master for index %q: %v", name, err)
	}
	return n == 1
}

// schemaVersion is a t.Fatalf-wrapping helper around
// schema.GetSchemaVersion for the thrum-7ojv tests where the
// 3-line err-handling pattern adds noise without value. Existing
// schema tests use schema.GetSchemaVersion + explicit error handling
// directly; either pattern is fine, this just centralises the wrap
// for the 7ojv test trio.
func schemaVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	v, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion: %v", err)
	}
	return v
}

// TestSchema_V36_to_V38_FreshUpgrade — thrum-7ojv. Fresh release-line
// DB at v36 (a binary built before the 7ojv change shipped) gets
// migrated forward by a new release-line binary: must run the v37
// no-op placeholder and the v38 index creation, end at v38 with the
// idx_events_timestamp index present.
func TestSchema_V36_to_V38_FreshUpgrade(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v36-to-v38.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	// Rewind to v36 and DROP the index that createTables/createIndexes
	// stamped at fresh-install time (the v36→v38 migration path is the
	// load-bearing one being exercised; we need to prove the migration
	// itself creates the index, not just that fresh-install does).
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_events_timestamp`); err != nil {
		t.Fatalf("drop pre-existing idx_events_timestamp: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_version SET version = 36`); err != nil {
		t.Fatalf("rewind to v36: %v", err)
	}

	// Sanity: pre-Migrate state.
	if got := schemaVersion(t, db); got != 36 {
		t.Fatalf("pre-migrate schema_version = %d, want 36", got)
	}
	if indexExists(t, db, "idx_events_timestamp") {
		t.Fatal("pre-migrate idx_events_timestamp should not exist after the drop+rewind")
	}

	// Migrate: v36 → v38 (runs v37 no-op + v38 index).
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate v36→v38: %v", err)
	}

	if got := schemaVersion(t, db); got != schema.CurrentVersion {
		t.Errorf("post-migrate schema_version = %d, want CurrentVersion", got)
	}
	if !indexExists(t, db, "idx_events_timestamp") {
		t.Error("v38 migration did not create idx_events_timestamp index")
	}
}

// TestSchema_V37_to_V38_CrossBinary — thrum-7ojv. Critical cross-binary
// case: a DB already at v37 from a thrum-agents binary (which stamps
// v37 with the memory-tables migration — see CurrentVersion doc) gets
// migrated forward by a release-line binary. The release line's v37
// branch is a no-op (must not error or attempt to re-run thrum-agents
// memory-tables DDL we don't have), then v38 creates the index. Ends
// at v38 with the index present + the existing v37 schema_version row
// not double-stamped or downgraded.
func TestSchema_V37_to_V38_CrossBinary(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v37-to-v38.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	// Simulate the "DB previously stamped v37 by a thrum-agents binary"
	// scenario. Drop the index that fresh-install added so we can prove
	// the v38 migration creates it on this v37 path too.
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_events_timestamp`); err != nil {
		t.Fatalf("drop pre-existing idx_events_timestamp: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_version SET version = 37`); err != nil {
		t.Fatalf("rewind to v37: %v", err)
	}

	if got := schemaVersion(t, db); got != 37 {
		t.Fatalf("pre-migrate schema_version = %d, want 37", got)
	}

	// Migrate: v37 → v38. The release-line binary must SKIP its v37
	// no-op block (already at v37) and just run v38's index creation.
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate v37→v38 (cross-binary path): %v", err)
	}

	if got := schemaVersion(t, db); got != schema.CurrentVersion {
		t.Errorf("post-migrate schema_version = %d, want CurrentVersion", got)
	}
	if !indexExists(t, db, "idx_events_timestamp") {
		t.Error("v38 migration did not create idx_events_timestamp index on v37 starting state")
	}
}

// TestSchema_V36_to_V38_CreatesMemoryTables — thrum-7ojv (back-port
// pattern). Asserts the v37 dummy-tables back-port actually creates
// the 6 memory.* tables (memory_record / memory_tag / memory_edge /
// memory_fts / memory_embeddings / memory_embed_queue) and their 10
// indexes when migrating forward from v36. Without the back-port a
// fresh rc.3 install would create a v38 DB with NO memory tables,
// causing crash on every memory.* operation if a thrum-agents binary
// later runs against the same DB.
func TestSchema_V36_to_V38_CreatesMemoryTables(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v36-memory-backport.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	// Drop the memory tables and indexes that createTables/createIndexes
	// stamped at fresh-install time, then rewind to v36 so the v37
	// migration block is the load-bearing thing being exercised.
	dropStmts := []string{
		`DROP INDEX IF EXISTS idx_memory_kind`,
		`DROP INDEX IF EXISTS idx_memory_agent`,
		`DROP INDEX IF EXISTS idx_memory_created`,
		`DROP INDEX IF EXISTS idx_memory_updated`,
		`DROP INDEX IF EXISTS idx_memory_status`,
		`DROP INDEX IF EXISTS idx_memory_scope`,
		`DROP INDEX IF EXISTS idx_memory_tag_tag`,
		`DROP INDEX IF EXISTS idx_memory_edge_to`,
		`DROP INDEX IF EXISTS idx_memory_edge_kind`,
		`DROP INDEX IF EXISTS idx_memory_embed_status`,
		// Order matters for the table drops: drop children before parents
		// (FK constraints). memory_fts has no FK so can drop standalone.
		`DROP TABLE IF EXISTS memory_embed_queue`,
		`DROP TABLE IF EXISTS memory_embeddings`,
		`DROP TABLE IF EXISTS memory_tag`,
		`DROP TABLE IF EXISTS memory_edge`,
		`DROP TABLE IF EXISTS memory_fts`,
		`DROP TABLE IF EXISTS memory_record`,
	}
	for _, s := range dropStmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("drop pre-existing %s: %v", s, err)
		}
	}
	if _, err := db.Exec(`UPDATE schema_version SET version = 36`); err != nil {
		t.Fatalf("rewind to v36: %v", err)
	}

	// Migrate v36 → v38. The v37 block must create all 6 tables + 10
	// indexes verbatim from thrum-agents; the v38 block adds the
	// timestamp index.
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate v36→v38: %v", err)
	}

	// All 6 memory tables present.
	expectedTables := []string{
		"memory_record", "memory_tag", "memory_edge",
		"memory_fts", "memory_embeddings", "memory_embed_queue",
	}
	for _, tbl := range expectedTables {
		var n int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','virtual table') AND name = ?`,
			tbl,
		).Scan(&n); err != nil {
			t.Fatalf("query for table %q: %v", tbl, err)
		}
		if n != 1 {
			t.Errorf("table %q missing after v37 back-port (got %d rows in sqlite_master, want 1)", tbl, n)
		}
	}

	// All 10 memory indexes present.
	expectedIndexes := []string{
		"idx_memory_kind", "idx_memory_agent", "idx_memory_created",
		"idx_memory_updated", "idx_memory_status", "idx_memory_scope",
		"idx_memory_tag_tag", "idx_memory_edge_to", "idx_memory_edge_kind",
		"idx_memory_embed_status",
	}
	for _, idx := range expectedIndexes {
		if !indexExists(t, db, idx) {
			t.Errorf("index %q missing after v37 back-port", idx)
		}
	}

	// v38 index also present (post v37 → v38 migration).
	if !indexExists(t, db, "idx_events_timestamp") {
		t.Error("idx_events_timestamp missing after v37 → v38 path")
	}

	// Schema stamped at v38.
	if got := schemaVersion(t, db); got != schema.CurrentVersion {
		t.Errorf("post-migrate schema_version = %d, want CurrentVersion", got)
	}
}

// TestSchema_V38_Idempotent — thrum-7ojv. CREATE INDEX IF NOT EXISTS
// must be idempotent across repeated migration calls. Mirrors the
// Multi-Binary Worktree Footgun scenario where a co-resident
// thrum-agents binary adds its own v38 = idx_events_timestamp
// migration in the future; both binaries running their v38 block
// against the same DB must not error.
func TestSchema_V38_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v38-idempotent.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	// Sanity: fresh install already at v38 with the index in place.
	if got := schemaVersion(t, db); got != schema.CurrentVersion {
		t.Fatalf("fresh schema_version = %d, want CurrentVersion", got)
	}
	if !indexExists(t, db, "idx_events_timestamp") {
		t.Fatal("fresh install missing idx_events_timestamp (createIndexes path)")
	}

	// Rewind to v37 + re-run migrate so the v38 block fires AGAIN against
	// an index that already exists. CREATE INDEX IF NOT EXISTS should
	// no-op, not error.
	if _, err := db.Exec(`UPDATE schema_version SET version = 37`); err != nil {
		t.Fatalf("rewind to v37: %v", err)
	}
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate v37→v38 with index already present: %v (CREATE INDEX IF NOT EXISTS must be idempotent)", err)
	}
	if got := schemaVersion(t, db); got != schema.CurrentVersion {
		t.Errorf("post-migrate schema_version = %d, want CurrentVersion", got)
	}
	if !indexExists(t, db, "idx_events_timestamp") {
		t.Error("idx_events_timestamp missing after idempotent re-run")
	}
}

// TestSchema_V38_to_V39_AddsMonitorSchedule — thrum-puhr.9. Asserts the v39
// migration adds the schedule column to an existing v38 monitors table.
// Rewinds a fresh DB to v38, drops the schedule column (via table rebuild
// since SQLite ALTER TABLE DROP COLUMN exists but we keep the test minimal
// by starting from an explicit v38-shape table), then runs Migrate and
// verifies the column landed.
func TestSchema_V38_to_V39_AddsMonitorSchedule(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "v38-to-v39.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	// Rebuild monitors as the v38-shape table (without schedule), then
	// stamp the version back to v38 so Migrate runs only the v39 step.
	rebuild := []string{
		`DROP TABLE monitors`,
		`CREATE TABLE monitors (
			id                TEXT PRIMARY KEY,
			name              TEXT NOT NULL UNIQUE,
			argv              TEXT NOT NULL,
			match_pattern     TEXT NOT NULL,
			target            TEXT NOT NULL,
			cwd               TEXT NOT NULL,
			env               TEXT NOT NULL,
			debounce_seconds  INTEGER NOT NULL,
			created_at        TEXT NOT NULL,
			updated_at        TEXT NOT NULL,
			status            TEXT NOT NULL,
			last_exit_code    INTEGER,
			last_exit_at      TEXT,
			pid               INTEGER
		)`,
		`UPDATE schema_version SET version = 38`,
	}
	for _, s := range rebuild {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("rebuild v38 monitors: %v: %s", err, s)
		}
	}

	if got := schemaVersion(t, db); got != 38 {
		t.Fatalf("pre-migrate schema_version = %d, want 38", got)
	}

	// Sanity: column doesn't exist yet.
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('monitors') WHERE name='schedule'`,
	).Scan(&n); err != nil {
		t.Fatalf("pre-migrate column check: %v", err)
	}
	if n != 0 {
		t.Fatalf("pre-migrate schedule column already present (n=%d)", n)
	}

	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate v38→v39: %v", err)
	}

	if got := schemaVersion(t, db); got != schema.CurrentVersion {
		t.Errorf("post-migrate schema_version = %d, want CurrentVersion", got)
	}

	// Schedule column must now exist.
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('monitors') WHERE name='schedule'`,
	).Scan(&n); err != nil {
		t.Fatalf("post-migrate column check: %v", err)
	}
	if n != 1 {
		t.Errorf("post-migrate schedule column missing (n=%d, want 1)", n)
	}

	// Re-running Migrate must be idempotent — column-already-present path.
	if err := schema.Migrate(db); err != nil {
		t.Errorf("re-run Migrate at v39 must be a no-op: %v", err)
	}
}
