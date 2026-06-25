package schema_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/schema"
)

// thrum-ej6qn: dead-end DDL forward-port v51→v52 (precedent thrum-399av for
// v51). The v52 delta vs v51 is purely additive: two NEW columns on the
// existing 'agents' table (mirror of private 0.11's thrum-v8uyw agent_status
// columns), nothing else. These tests are the acceptance the coordinator named
// — a public 0.10.x binary must open + do basic ops on a v52 (0.11-schema) DB
// without the one-way-migration brick. Three angles: a fresh DB inits at v52
// with both columns; a v51 DB migrates cleanly to v52 (the two columns appear,
// existing rows survive with the '' default); and a v52 DB round-trips basic
// agent CRUD writing the new column. Helpers hasColumn/hasTable/hasIndex live in
// schema_v51_forwardport_test.go (same package).

// v52NewColumns is the full set of new columns a v52 DB must carry on the
// 'agents' table, used by both the fresh-init and post-migration assertions.
var v52NewColumns = map[string][]string{
	"agents": {"agent_status", "agent_status_updated_at"},
}

func assertV52Surface(t *testing.T, db *sql.DB) {
	t.Helper()
	// v52 is a superset of v51 — the prior surface must still hold.
	assertV51Surface(t, db)
	for table, cols := range v52NewColumns {
		for _, c := range cols {
			if !hasColumn(t, db, table, c) {
				t.Errorf("missing column %s.%s on v52 schema", table, c)
			}
		}
	}
}

func TestForwardPortV52_FreshInit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh_v52.db")
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
	if v != 52 {
		t.Errorf("fresh DB version = %d, want 52", v)
	}
	assertV52Surface(t, db)
}

// bootstrapV51 hand-builds a minimal v51-shape DB: schema_version=51 plus the
// core tables a v0.10.6 (v51) binary would have created, WITHOUT the two v52
// agents columns. Mirrors bootstrapV40 in schema_v51_forwardport_test.go but
// pinned at v51 so Migrate exercises exactly the v51→v52 step.
func bootstrapV51(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO schema_version (version) VALUES (51)`,
		// agents table at the v51 surface: includes the v41/v48 forward-port
		// columns (agent_pid_start_time, phase) but NOT the v52 columns.
		`CREATE TABLE agents (
			agent_id                 TEXT PRIMARY KEY,
			kind                     TEXT NOT NULL,
			role                     TEXT NOT NULL,
			module                   TEXT NOT NULL,
			display                  TEXT NOT NULL DEFAULT '',
			hostname                 TEXT NOT NULL DEFAULT '',
			agent_pid                INTEGER NOT NULL DEFAULT 0,
			registered_at            TEXT NOT NULL,
			last_seen_at             TEXT NOT NULL DEFAULT '',
			origin_daemon            TEXT NOT NULL DEFAULT '',
			mode                     TEXT NOT NULL DEFAULT 'persistent',
			identity                 TEXT NOT NULL DEFAULT 'long_lived',
			auto_respawn_enabled     INTEGER NOT NULL DEFAULT 0,
			auto_respawn_disabled_at INTEGER,
			state_md_parse_failed_at INTEGER,
			last_pane_alive_at       INTEGER,
			agent_pid_start_time     TEXT NOT NULL DEFAULT '',
			phase                    TEXT NOT NULL DEFAULT 'active'
		)`,
		// Seed a pre-existing row to prove the v52 ALTERs apply the '' default to
		// existing rows and data survives the migration.
		`INSERT INTO agents (agent_id, kind, role, module, registered_at)
			VALUES ('a_pre', 'agent', 'implementer', 'test', '2026-06-25T00:00:00Z')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("bootstrap v51 stmt failed: %v\nSQL: %s", err, s)
		}
	}
}

func TestForwardPortV52_MigrateFromV51(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v51_to_v52.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	bootstrapV51(t, db)

	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate v51→v52: %v", err)
	}

	v, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion: %v", err)
	}
	if v != 52 {
		t.Fatalf("post-migration version = %d, want 52", v)
	}

	// Both new columns present on the agents table.
	for _, c := range v52NewColumns["agents"] {
		if !hasColumn(t, db, "agents", c) {
			t.Errorf("missing column agents.%s after v51→v52 migration", c)
		}
	}

	// Pre-existing row survived, with the new columns carrying their '' default
	// (proves the ALTERs applied NOT NULL DEFAULT '' to existing rows).
	var status, statusUpdated string
	if err := db.QueryRow(
		`SELECT agent_status, agent_status_updated_at FROM agents WHERE agent_id='a_pre'`,
	).Scan(&status, &statusUpdated); err != nil {
		t.Fatalf("read migrated agent: %v", err)
	}
	if status != "" || statusUpdated != "" {
		t.Errorf("migrated agent defaults: agent_status=%q agent_status_updated_at=%q, want empty/empty", status, statusUpdated)
	}

	// Migrate is idempotent / no-op on an already-current DB (re-run of the
	// column-set-guarded ALTERs must not error).
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("second Migrate (no-op) failed: %v", err)
	}
}

func TestForwardPortV52_CRUDRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v52_crud.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	// Insert exercising the new v52 columns so the surface is actually used, not
	// just present.
	if _, err := db.Exec(
		`INSERT INTO agents (agent_id, kind, role, module, registered_at, agent_status, agent_status_updated_at)
			VALUES ('a1', 'agent', 'implementer', 'test', '2026-06-25T00:00:00Z', 'working', '2026-06-25T00:01:00Z')`,
	); err != nil {
		t.Fatalf("insert agent: %v", err)
	}

	var role, status, statusUpdated string
	if err := db.QueryRow(
		`SELECT role, agent_status, agent_status_updated_at FROM agents WHERE agent_id='a1'`,
	).Scan(&role, &status, &statusUpdated); err != nil {
		t.Fatalf("read back agent: %v", err)
	}
	if role != "implementer" || status != "working" || statusUpdated != "2026-06-25T00:01:00Z" {
		t.Errorf("CRUD round-trip mismatch: role=%q agent_status=%q agent_status_updated_at=%q", role, status, statusUpdated)
	}

	// Update round-trip on the new column.
	if _, err := db.Exec(`UPDATE agents SET agent_status = 'idle' WHERE agent_id = 'a1'`); err != nil {
		t.Fatalf("update agent_status: %v", err)
	}
	if err := db.QueryRow(`SELECT agent_status FROM agents WHERE agent_id='a1'`).Scan(&status); err != nil {
		t.Fatalf("read after update: %v", err)
	}
	if status != "idle" {
		t.Errorf("update round-trip: agent_status=%q, want idle", status)
	}
}
