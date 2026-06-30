package schema_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/schema"
)

// thrum-2q0wt: dead-end DDL forward-port v52→v55 (precedent thrum-ej6qn for
// v52, thrum-399av for v51). The frozen private ladder lands three steps:
//   - v53 (fleet-purge E1): a NEW purge_tombstones table + its phase index.
//   - v54 (klezv): value-convention only (agents.phase 'parked'→'sleeping') —
//     NO DDL; public is phase-agnostic, so this is a pure dead-end version bump.
//   - v55: ALTER TABLE agents ADD COLUMN hidden_from_gui — an inert column the
//     public binary never reads.
// These tests are the coordinator's acceptance: a public 0.10.x binary must
// open + do basic ops on a v55 (0.11-schema) DB without the one-way-migration
// brick. Helpers hasColumn/hasTable/hasIndex + assertV52Surface live in the
// sibling v51/v52 forwardport tests (same package).

// v55NewColumns is the set of new columns a v55 DB must carry beyond v52.
var v55NewColumns = map[string][]string{
	"agents": {"hidden_from_gui"},
}

func assertV55Surface(t *testing.T, db *sql.DB) {
	t.Helper()
	// v55 is a superset of v52 — the prior surface must still hold.
	assertV52Surface(t, db)
	for table, cols := range v55NewColumns {
		for _, c := range cols {
			if !hasColumn(t, db, table, c) {
				t.Errorf("missing column %s.%s on v55 schema", table, c)
			}
		}
	}
	// v53 fleet-purge table + its phase index.
	if !hasTable(t, db, "purge_tombstones") {
		t.Errorf("missing table purge_tombstones on v55 schema")
	}
	if !hasIndex(t, db, "idx_purge_tombstones_phase") {
		t.Errorf("missing index idx_purge_tombstones_phase on v55 schema")
	}
}

func TestForwardPortV55_FreshInit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh_v55.db")
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
		t.Errorf("fresh DB version = %d, want 55", v)
	}
	assertV55Surface(t, db)
}

// bootstrapV52 hand-builds a minimal v52-shape DB: schema_version=52 plus the
// agents table at the v52 surface (includes the v48 phase + v52 agent_status
// columns) but WITHOUT the v55 hidden_from_gui column and WITHOUT the v53
// purge_tombstones table. Pinned at v52 so Migrate exercises exactly the
// v52→v55 ladder.
func bootstrapV52(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO schema_version (version) VALUES (52)`,
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
			phase                    TEXT NOT NULL DEFAULT 'active',
			agent_status             TEXT NOT NULL DEFAULT '',
			agent_status_updated_at  TEXT NOT NULL DEFAULT ''
		)`,
		// Pre-existing row to prove the v55 ALTER applies its default to existing
		// rows and data survives the migration.
		`INSERT INTO agents (agent_id, kind, role, module, registered_at)
			VALUES ('a_pre', 'agent', 'implementer', 'test', '2026-06-29T00:00:00Z')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("bootstrap v52 stmt failed: %v\nSQL: %s", err, s)
		}
	}
}

func TestForwardPortV55_MigrateFromV52(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v52_to_v55.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	bootstrapV52(t, db)

	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate v52→v55: %v", err)
	}

	v, err := schema.GetSchemaVersion(db)
	if err != nil {
		t.Fatalf("GetSchemaVersion: %v", err)
	}
	if v != 55 {
		t.Fatalf("post-migration version = %d, want 55", v)
	}

	// v55 column + v53 table/index present after migration.
	if !hasColumn(t, db, "agents", "hidden_from_gui") {
		t.Errorf("missing column agents.hidden_from_gui after v52→v55 migration")
	}
	if !hasTable(t, db, "purge_tombstones") {
		t.Errorf("missing table purge_tombstones after v52→v55 migration")
	}
	if !hasIndex(t, db, "idx_purge_tombstones_phase") {
		t.Errorf("missing index idx_purge_tombstones_phase after v52→v55 migration")
	}

	// Pre-existing row survived with hidden_from_gui carrying its 0 default
	// (proves the ALTER applied NOT NULL DEFAULT 0 to existing rows).
	var hidden int
	if err := db.QueryRow(`SELECT hidden_from_gui FROM agents WHERE agent_id='a_pre'`).Scan(&hidden); err != nil {
		t.Fatalf("read migrated agent: %v", err)
	}
	if hidden != 0 {
		t.Errorf("migrated agent hidden_from_gui=%d, want 0", hidden)
	}

	// Migrate is idempotent / no-op on an already-current DB.
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("second Migrate (no-op) failed: %v", err)
	}
}

// TestForwardPortV55_Tolerance: a row carrying the frozen 0.11 conventions
// (agents.phase='sleeping' from klezv + hidden_from_gui=1) round-trips, and a
// purge_tombstones row inserts + reads back — proving the public binary
// tolerates a v55 DB written by a 0.11 peer.
func TestForwardPortV55_Tolerance(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "v55_tolerance.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO agents (agent_id, kind, role, module, registered_at, phase, hidden_from_gui)
			VALUES ('a1', 'agent', 'implementer', 'test', '2026-06-29T00:00:00Z', 'sleeping', 1)`,
	); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	var phase string
	var hidden int
	if err := db.QueryRow(
		`SELECT phase, hidden_from_gui FROM agents WHERE agent_id='a1'`,
	).Scan(&phase, &hidden); err != nil {
		t.Fatalf("read back agent: %v", err)
	}
	if phase != "sleeping" || hidden != 1 {
		t.Errorf("agent round-trip: phase=%q hidden_from_gui=%d, want sleeping/1", phase, hidden)
	}

	// purge_tombstones round-trip exercising the full column surface + defaults.
	if _, err := db.Exec(
		`INSERT INTO purge_tombstones
			(tombstone_id, target_agent_id, initiator, created_at, grace_until, grace_days, origin_daemon_id)
			VALUES ('t1', 'a1', 'coord', '2026-06-29T00:00:00Z', '2026-07-06T00:00:00Z', 7, 'd_local')`,
	); err != nil {
		t.Fatalf("insert purge_tombstone: %v", err)
	}
	var author, phaseT string
	var recoverable int
	if err := db.QueryRow(
		`SELECT author, phase, recoverable FROM purge_tombstones WHERE tombstone_id='t1'`,
	).Scan(&author, &phaseT, &recoverable); err != nil {
		t.Fatalf("read back purge_tombstone: %v", err)
	}
	if author != "user" || phaseT != "pending-purge" || recoverable != 1 {
		t.Errorf("purge_tombstone defaults: author=%q phase=%q recoverable=%d, want user/pending-purge/1", author, phaseT, recoverable)
	}
}
