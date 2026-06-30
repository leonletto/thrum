package schema_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/schema"
)

// thrum-399av: dead-end DDL forward-port v41→v51. These tests are the
// acceptance coordinator_main named — a v0.10.6 (release-line) binary must
// open + do basic ops on a v51 (0.11-schema) DB without the one-way-migration
// brick. Three angles: a fresh DB inits at v51 with the full surface; a v40 DB
// migrates cleanly to v51 (new columns/tables appear, existing rows survive
// with safe defaults, the v47 index swap lands); and a v51 DB round-trips
// basic message/agent CRUD.

func hasColumn(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == col {
			return true
		}
	}
	return false
}

func hasTable(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&got)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("query table %s: %v", name, err)
	}
	return true
}

func hasIndex(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name=?", name).Scan(&got)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("query index %s: %v", name, err)
	}
	return true
}

// v51Surface is the full set of new columns/tables a v51 DB must carry, used by
// both the fresh-init and post-migration assertions.
var (
	v51NewColumns = map[string][]string{
		"messages":           {"visibility_class", "retarget_fill_order", "priority"},
		"agents":             {"agent_pid_start_time", "phase"},
		"permission_nudges":  {"prompt_fingerprint"},
		"message_deliveries": {"addressed_via"},
	}
	v51NewTables = []string{
		"alert_deliveries", "telegram_outbound_queue",
		"node", "node_label", "edge", "node_comment", "graph_blocked",
		"memory_satellite",
	}
	// Every index a v41–v51 migration creates must also appear on a fresh DB —
	// asserting presence here is what makes fresh-init/migration drift on ANY
	// index actually fail the test.
	v51NewIndexes = []string{
		"idx_messages_time_id",         // v47 (replaces idx_messages_time)
		"idx_messages_visibility",      // v47
		"idx_alert_deliveries_expires", // v45
		"idx_deliveries_recipient_via", // v48
		"idx_tg_queue_next",            // v49
		"idx_node_ready",               // v50
		"idx_node_kind",                // v50
		"idx_edge_to",                  // v50
		"idx_node_label_label",         // v50
		"idx_node_comment_node",        // v50
	}
)

func assertV51Surface(t *testing.T, db *sql.DB) {
	t.Helper()
	for table, cols := range v51NewColumns {
		for _, c := range cols {
			if !hasColumn(t, db, table, c) {
				t.Errorf("missing column %s.%s on v51 schema", table, c)
			}
		}
	}
	for _, tbl := range v51NewTables {
		if !hasTable(t, db, tbl) {
			t.Errorf("missing table %s on v51 schema", tbl)
		}
	}
	// Every new index is present (covers the v47 keyset index plus the v45/v48/
	// v49/v50 additions — drift on any one fails here).
	for _, idx := range v51NewIndexes {
		if !hasIndex(t, db, idx) {
			t.Errorf("missing index %s on v51 schema", idx)
		}
	}
	// v47 swap: the old single-column index is gone.
	if hasIndex(t, db, "idx_messages_time") {
		t.Error("idx_messages_time should be dropped on v51 schema")
	}
}

func TestForwardPortV51_FreshInit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh_v51.db")
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
	if v != 55 {
		t.Errorf("fresh DB version = %d, want 55 (current floor; v51 surface is a subset)", v)
	}
	assertV51Surface(t, db)
}

// bootstrapV40 hand-builds a minimal v40-shape DB: schema_version=40 plus the
// four core tables a v0.10.6 binary would have created, WITHOUT any v41–v51
// column/table, and the old idx_messages_time index. Mirrors the existing
// TestMigrationV19 bootstrap pattern.
func bootstrapV40(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO schema_version (version) VALUES (40)`,
		`CREATE TABLE messages (
			message_id TEXT PRIMARY KEY, thread_id TEXT, agent_id TEXT NOT NULL,
			session_id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT,
			body_format TEXT NOT NULL, body_content TEXT NOT NULL, body_structured TEXT,
			deleted INTEGER DEFAULT 0, deleted_at TEXT, delete_reason TEXT,
			authored_by TEXT, disclosed INTEGER DEFAULT 0,
			pending_route_resolution INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX idx_messages_time ON messages(created_at)`,
		`CREATE TABLE agents (
			agent_id TEXT PRIMARY KEY, kind TEXT NOT NULL, role TEXT NOT NULL,
			module TEXT NOT NULL, display TEXT NOT NULL DEFAULT '', hostname TEXT NOT NULL DEFAULT '',
			agent_pid INTEGER NOT NULL DEFAULT 0, registered_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL DEFAULT '', origin_daemon TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT 'persistent', identity TEXT NOT NULL DEFAULT 'long_lived',
			auto_respawn_enabled INTEGER NOT NULL DEFAULT 0, auto_respawn_disabled_at INTEGER,
			state_md_parse_failed_at INTEGER, last_pane_alive_at INTEGER
		)`,
		`CREATE TABLE permission_nudges (
			message_id TEXT PRIMARY KEY, session TEXT NOT NULL, tmux_target TEXT NOT NULL,
			agent_name TEXT NOT NULL, pattern_key TEXT NOT NULL, approve_key TEXT NOT NULL,
			deny_key TEXT, first_detected TIMESTAMP NOT NULL, last_nudge_at TIMESTAMP NOT NULL,
			nudge_count INTEGER NOT NULL, last_pane_hash BLOB NOT NULL, expires_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE message_deliveries (
			message_id TEXT NOT NULL, recipient_agent_id TEXT NOT NULL,
			delivered_at TEXT NOT NULL, seen_at TEXT, read_at TEXT,
			PRIMARY KEY (message_id, recipient_agent_id)
		)`,
		// Seed pre-existing rows to prove NOT NULL defaults apply on ALTER and
		// data survives the migration.
		`INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
			VALUES ('m_pre', 'a_pre', 's_pre', '2026-06-16T00:00:00Z', 'markdown', 'hello')`,
		`INSERT INTO agents (agent_id, kind, role, module, registered_at)
			VALUES ('a_pre', 'agent', 'implementer', 'test', '2026-06-16T00:00:00Z')`,
		`INSERT INTO message_deliveries (message_id, recipient_agent_id, delivered_at)
			VALUES ('m_pre', 'a_pre', '2026-06-16T00:00:00Z')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("bootstrap v40 stmt failed: %v\nSQL: %s", err, s)
		}
	}
}

func TestForwardPortV51_MigrateFromV40(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v40_to_v51.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	bootstrapV40(t, db)

	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate v40→v55: %v", err)
	}

	v, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion: %v", err)
	}
	if v != 55 {
		t.Fatalf("post-migration version = %d, want 55 (current floor; v51 surface is a subset)", v)
	}

	// All new columns/tables present, index swap landed.
	assertV51Surface(t, db)

	// Pre-existing data survived, with the new columns carrying their safe
	// defaults (proves the ALTERs applied NOT NULL DEFAULTs to existing rows).
	var vis, prio, addressedVia, phase, pidStart string
	if err := db.QueryRow(
		`SELECT visibility_class, priority FROM messages WHERE message_id='m_pre'`,
	).Scan(&vis, &prio); err != nil {
		t.Fatalf("read migrated message: %v", err)
	}
	if vis != "targeted" || prio != "" {
		t.Errorf("migrated message defaults: visibility_class=%q priority=%q, want targeted/empty", vis, prio)
	}
	if err := db.QueryRow(
		`SELECT phase, agent_pid_start_time FROM agents WHERE agent_id='a_pre'`,
	).Scan(&phase, &pidStart); err != nil {
		t.Fatalf("read migrated agent: %v", err)
	}
	if phase != "active" || pidStart != "" {
		t.Errorf("migrated agent defaults: phase=%q agent_pid_start_time=%q, want active/empty", phase, pidStart)
	}
	if err := db.QueryRow(
		`SELECT addressed_via FROM message_deliveries WHERE message_id='m_pre'`,
	).Scan(&addressedVia); err != nil {
		t.Fatalf("read migrated delivery: %v", err)
	}
	if addressedVia != "unattributed" {
		t.Errorf("migrated delivery addressed_via=%q, want unattributed", addressedVia)
	}

	// Migrate is idempotent / no-op on an already-current DB.
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("second Migrate (no-op) failed: %v", err)
	}
}

func TestForwardPortV51_CRUDRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v51_crud.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	// Basic agent + message CRUD on the v51 schema, exercising a v47/v48 column
	// on the write so the new surface is actually used, not just present.
	if _, err := db.Exec(
		`INSERT INTO agents (agent_id, kind, role, module, registered_at, phase)
			VALUES ('a1', 'agent', 'implementer', 'test', '2026-06-16T00:00:00Z', 'active')`,
	); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content, visibility_class)
			VALUES ('m1', 'a1', 's1', '2026-06-16T00:01:00Z', 'markdown', 'hi', 'targeted')`,
	); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO message_deliveries (message_id, recipient_agent_id, delivered_at, addressed_via)
			VALUES ('m1', 'a1', '2026-06-16T00:01:00Z', 'identity')`,
	); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}

	var role, content, via string
	if err := db.QueryRow(
		`SELECT a.role, m.body_content, d.addressed_via
		   FROM messages m
		   JOIN agents a ON a.agent_id = m.agent_id
		   JOIN message_deliveries d ON d.message_id = m.message_id
		  WHERE m.message_id = 'm1'`,
	).Scan(&role, &content, &via); err != nil {
		t.Fatalf("read back joined CRUD row: %v", err)
	}
	if role != "implementer" || content != "hi" || via != "identity" {
		t.Errorf("CRUD round-trip mismatch: role=%q content=%q via=%q", role, content, via)
	}

	// Update + delete round-trip.
	if _, err := db.Exec(`UPDATE messages SET body_content = 'edited' WHERE message_id = 'm1'`); err != nil {
		t.Fatalf("update message: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM message_deliveries WHERE message_id = 'm1'`); err != nil {
		t.Fatalf("delete delivery: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM message_deliveries WHERE message_id='m1'`).Scan(&n); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if n != 0 {
		t.Errorf("delete round-trip: %d deliveries remain, want 0", n)
	}
}
