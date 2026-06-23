package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/schema"
)

// seedPreV40DB creates a messages.db at <thrumDir>/var, fully migrated then
// pinned back to v39, with a stuck local no-delivery-row legacy broadcast and a
// peer agent's unread delivery row. Closing it leaves the file on disk for
// NewState to reopen.
func seedPreV40DB(t *testing.T, thrumDir, localDaemon string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(thrumDir, "var"), 0o750); err != nil {
		t.Fatalf("mkdir var: %v", err)
	}
	dbPath := filepath.Join(thrumDir, "var", "messages.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("init seed db: %v", err)
	}
	// Pin back to v39 so NewState detects a real v39->v40 crossing.
	mustExec(t, db, `UPDATE schema_version SET version = 39`)
	// Local + peer agents.
	mustExec(t, db, `INSERT OR IGNORE INTO agents(agent_id, kind, role, module, registered_at, origin_daemon) VALUES('user:leon-letto','claude','','test',datetime('now'), ?)`, localDaemon)
	mustExec(t, db, `INSERT OR IGNORE INTO agents(agent_id, kind, role, module, registered_at, origin_daemon) VALUES('peer_coord','claude','coordinator','test',datetime('now'),'daemon-B')`)
	// Stuck local no-delivery-row legacy broadcast.
	mustExec(t, db, `INSERT INTO messages(message_id, agent_id, session_id, body_format, body_content, created_at, deleted) VALUES('m-legacy','bob','ses_test','markdown','x','2026-05-22T00:00:00Z',0)`)
	// Peer unread delivery row — must stay unread across the crossing.
	mustExec(t, db, `INSERT INTO messages(message_id, agent_id, session_id, body_format, body_content, created_at, deleted) VALUES('m-peer','bob','ses_test','markdown','x','2026-05-22T00:00:00Z',0)`)
	mustExec(t, db, `INSERT INTO message_deliveries(message_id, recipient_agent_id, delivered_at, read_at) VALUES('m-peer','peer_coord','t',NULL)`)
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
}

// TestStateInit_v39_to_v40_BackfillsOnce drives the REAL NewState wiring: a v39
// DB crosses to v40, the gated backfill runs once, local stuck unread clears,
// the peer row is untouched (leak-guard), the schema advances to 40, and a
// second construction (now oldVersion=40) is a clean no-op (idempotent gate).
func TestStateInit_v39_to_v40_BackfillsOnce(t *testing.T) {
	const localDaemon = "daemon-A"
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")

	seedPreV40DB(t, thrumDir, localDaemon)

	st, err := NewState(thrumDir, syncDir, "r_v40test", localDaemon)
	if err != nil {
		t.Fatalf("NewState (v39->v40 crossing): %v", err)
	}
	defer func() { _ = st.Close() }()

	// Migrate always advances to CurrentVersion (51 after the thrum-399av
	// forward-port); the read-state backfill still crosses at v40 (the gate is
	// oldVersion < SchemaVersionReadState), which is what this test exercises.
	if v, err := schema.GetSchemaVersion(st.RawDB()); err != nil || v != schema.CurrentVersion {
		t.Fatalf("schema version after crossing = %d (err %v), want %d", v, err, schema.CurrentVersion)
	}
	assertReadStamped(t, st.RawDB(), "m-legacy", "user:leon-letto") // backfill ran
	assertUnread(t, st.RawDB(), "m-peer", "peer_coord")             // leak-guard held
	_ = st.Close()

	// Re-construct: oldVersion is now CurrentVersion (>= SchemaVersionReadState),
	// so the gate is false — a clean no-op.
	st2, err := NewState(thrumDir, syncDir, "r_v40test", localDaemon)
	if err != nil {
		t.Fatalf("NewState (idempotent re-open): %v", err)
	}
	defer func() { _ = st2.Close() }()
	assertReadStamped(t, st2.RawDB(), "m-legacy", "user:leon-letto") // still read
	assertUnread(t, st2.RawDB(), "m-peer", "peer_coord")             // still untouched
}
