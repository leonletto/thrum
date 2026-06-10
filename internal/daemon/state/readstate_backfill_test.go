package state

import (
	"context"
	"database/sql"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/schema"
)

// newStateTestDB opens an in-memory SQLite DB migrated to head (v40) and returns
// the raw *sql.DB. Read-state tests seed with mustExec on the raw handle and
// wrap in safedb.New only where BackfillReadState requires a *safedb.DB.
func newStateTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("mustExec: %v", err)
	}
}

func assertReadStamped(t *testing.T, db *sql.DB, msgID, agent string) {
	t.Helper()
	var readAt sql.NullString
	err := db.QueryRow(`SELECT read_at FROM message_deliveries WHERE message_id=? AND recipient_agent_id=?`, msgID, agent).Scan(&readAt)
	if err != nil {
		t.Fatalf("assertReadStamped query %s/%s: %v", msgID, agent, err)
	}
	if !readAt.Valid {
		t.Fatalf("expected %s/%s read-stamped, got NULL read_at", msgID, agent)
	}
}

func assertUnread(t *testing.T, db *sql.DB, msgID, agent string) {
	t.Helper()
	var readAt sql.NullString
	err := db.QueryRow(`SELECT read_at FROM message_deliveries WHERE message_id=? AND recipient_agent_id=?`, msgID, agent).Scan(&readAt)
	if err != nil {
		t.Fatalf("assertUnread query %s/%s: %v", msgID, agent, err)
	}
	if readAt.Valid {
		t.Fatalf("expected %s/%s unread (read_at NULL), got %q", msgID, agent, readAt.String)
	}
}

// TestBackfillReadState_LocalOnly proves the v40 backfill clears local stuck
// unread (both the no-delivery-row class and existing unread rows) while leaving
// synced peer-agent rows untouched (the edhn leak-guard, made structural).
func TestBackfillReadState_LocalOnly(t *testing.T) {
	db := newStateTestDB(t)
	const localDaemon = "daemon-A"

	// LOCAL agent (origin_daemon = this daemon).
	mustExec(t, db, `INSERT OR IGNORE INTO agents(agent_id, kind, role, module, registered_at, origin_daemon) VALUES('user:leon-letto','claude','','test',datetime('now'), ?)`, localDaemon)
	// SYNCED PEER agent (origin_daemon = some other daemon) — must NOT be touched.
	mustExec(t, db, `INSERT OR IGNORE INTO agents(agent_id, kind, role, module, registered_at, origin_daemon) VALUES('peer_coord','claude','coordinator','test',datetime('now'),'daemon-B')`)

	// Class A: no-delivery-row legacy broadcast (no refs/scopes), local agent.
	mustExec(t, db, `INSERT INTO messages(message_id, agent_id, session_id, body_format, body_content, created_at, deleted) VALUES('m-legacy','bob','ses_test','markdown','x','2026-05-22T00:00:00Z',0)`)
	// Class C: existing unread delivery row, local agent.
	mustExec(t, db, `INSERT INTO messages(message_id, agent_id, session_id, body_format, body_content, created_at, deleted) VALUES('m-deliv','bob','ses_test','markdown','x','2026-05-22T00:00:00Z',0)`)
	mustExec(t, db, `INSERT INTO message_deliveries(message_id, recipient_agent_id, delivered_at, read_at) VALUES('m-deliv','user:leon-letto','t',NULL)`)
	// Peer's unread delivery row — must stay unread.
	mustExec(t, db, `INSERT INTO messages(message_id, agent_id, session_id, body_format, body_content, created_at, deleted) VALUES('m-peer','bob','ses_test','markdown','x','2026-05-22T00:00:00Z',0)`)
	mustExec(t, db, `INSERT INTO message_deliveries(message_id, recipient_agent_id, delivered_at, read_at) VALUES('m-peer','peer_coord','t',NULL)`)

	if err := BackfillReadState(context.Background(), safedb.New(db), localDaemon); err != nil {
		t.Fatalf("BackfillReadState: %v", err)
	}
	assertReadStamped(t, db, "m-legacy", "user:leon-letto") // Class A created+read
	assertReadStamped(t, db, "m-deliv", "user:leon-letto")  // Class C stamped read
	assertUnread(t, db, "m-peer", "peer_coord")             // leak-guard: peer untouched
}

// TestBackfillReadState_StaleLocalDaemonID is the corrective-rescope regression
// (thrum-tcqw, folded into the single v40 marker per thrum-b6qw): a LOCAL agent
// whose origin_daemon is a STALE prior-incarnation id (different from the
// current daemon id, but anchored to this host via a sibling agent's hostname)
// MUST be backfilled. thrum-agents' first (v42) scope keyed on the current id
// only and skipped exactly this agent (Leon's web user) — leaving 234 stuck. A
// genuine peer (foreign daemon id, even on a blank-hostname row) must still be
// left untouched (thrum-edhn leak-guard, mandatory per the b6qw dispatch).
func TestBackfillReadState_StaleLocalDaemonID(t *testing.T) {
	db := newStateTestDB(t)
	const (
		current = "d_current"
		stale   = "d_stale_prior"
		peer    = "d_peer_foreign"
		hostA   = "leonsmacm1pro"
	)
	// This daemon's recorded identity (hostname anchors the local id set).
	mustExec(t, db, `INSERT INTO daemon_identity(daemon_id, repo_name, hostname, repo_path, init_at, updated_at) VALUES(?, 'thrum', ?, '/x', 't', 't')`, current, hostA)
	// Sibling LOCAL agent on hostA under the STALE id — anchors d_stale as local.
	mustExec(t, db, `INSERT INTO agents(agent_id, kind, role, module, hostname, registered_at, origin_daemon) VALUES('impl_old','claude','implementer','m', ?, 't', ?)`, hostA, stale)
	// The stuck local web user: blank hostname, STALE origin id (the bug repro).
	mustExec(t, db, `INSERT INTO agents(agent_id, kind, role, module, hostname, registered_at, origin_daemon) VALUES('user:leon-letto','user','','ui', '', 't', ?)`, stale)
	// FOREIGN peer agent: blank-hostname synced user row (the leak trap).
	mustExec(t, db, `INSERT INTO agents(agent_id, kind, role, module, hostname, registered_at, origin_daemon) VALUES('user:peer-x','user','','ui', '', 't', ?)`, peer)

	// leon-letto's stuck no-delivery-row legacy broadcast + an existing unread row.
	mustExec(t, db, `INSERT INTO messages(message_id, agent_id, session_id, body_format, body_content, created_at, deleted) VALUES('m-legacy','bob','ses_test','markdown','x','2026-05-22T00:00:00Z',0)`)
	mustExec(t, db, `INSERT INTO messages(message_id, agent_id, session_id, body_format, body_content, created_at, deleted) VALUES('m-deliv','bob','ses_test','markdown','x','2026-05-22T00:00:00Z',0)`)
	mustExec(t, db, `INSERT INTO message_deliveries(message_id, recipient_agent_id, delivered_at, read_at) VALUES('m-deliv','user:leon-letto','t',NULL)`)
	// Peer's unread delivery row — must stay unread.
	mustExec(t, db, `INSERT INTO messages(message_id, agent_id, session_id, body_format, body_content, created_at, deleted) VALUES('m-peer','bob','ses_test','markdown','x','2026-05-22T00:00:00Z',0)`)
	mustExec(t, db, `INSERT INTO message_deliveries(message_id, recipient_agent_id, delivered_at, read_at) VALUES('m-peer','user:peer-x','t',NULL)`)

	if err := BackfillReadState(context.Background(), safedb.New(db), current); err != nil {
		t.Fatalf("BackfillReadState: %v", err)
	}
	assertReadStamped(t, db, "m-legacy", "user:leon-letto") // stale-local now backfilled (Pass 2)
	assertReadStamped(t, db, "m-deliv", "user:leon-letto")  // stale-local stamped (Pass 1)
	assertUnread(t, db, "m-peer", "user:peer-x")            // leak-guard: foreign peer untouched
}

// TestBackfillReadState_Idempotent re-runs the backfill and asserts a clean
// no-op: Pass 1 has nothing left to stamp, Pass 2's NOT EXISTS skips every
// already-created row (INSERT OR IGNORE + exactly one row per message/agent).
func TestBackfillReadState_Idempotent(t *testing.T) {
	db := newStateTestDB(t)
	const localDaemon = "daemon-A"
	mustExec(t, db, `INSERT OR IGNORE INTO agents(agent_id, kind, role, module, registered_at, origin_daemon) VALUES('user:leon-letto','claude','','test',datetime('now'), ?)`, localDaemon)
	mustExec(t, db, `INSERT INTO messages(message_id, agent_id, session_id, body_format, body_content, created_at, deleted) VALUES('m-legacy','bob','ses_test','markdown','x','2026-05-22T00:00:00Z',0)`)

	for i := 0; i < 2; i++ {
		if err := BackfillReadState(context.Background(), safedb.New(db), localDaemon); err != nil {
			t.Fatalf("BackfillReadState run %d: %v", i+1, err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM message_deliveries WHERE message_id='m-legacy' AND recipient_agent_id='user:leon-letto'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 delivery row after double backfill, got %d", count)
	}
	assertReadStamped(t, db, "m-legacy", "user:leon-letto")
}
