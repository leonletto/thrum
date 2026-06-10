package rpc

// Cross-class and cross-version interop guards for the thrum-b6qw read-state
// unification (port of the tcqw interop suite, adapted to the release line).
//
// RELEASE-LINE ADAPTATION (SKIP-T1, coordinator-confirmed): there is no
// DrainUnreadDeliveries server-side drain here — `thrum message read --all` is
// CLIENT-side enumerated (the CLI lists unread and passes explicit IDs). The
// round-trip therefore exercises the per-ID HandleMarkRead path, which is the
// production path. Authored-self messages are NOT inbox-visible on this line
// (no Part-5 in the inline predicate — deliberate, see the b6qw Part-5 fork
// decision), so they never count as unread; the T3 self-delivery row is
// asserted directly instead.

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/schema"
)

// mustExecRaw runs a seed INSERT/UPDATE against a raw *sql.DB.
func mustExecRaw(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("mustExecRaw %q: %v", q, err)
	}
}

// rawInboxVisibleUnreadCount runs the badge-style inbox-visible-unread count —
// the SAME inline predicate HandleList/the backstop use (buildForAgentValues +
// buildForAgentClause, same package) minus pagination — directly against a raw
// *sql.DB. This is the acceptance oracle for "does the stuck class count as
// unread".
func rawInboxVisibleUnreadCount(t *testing.T, db *sql.DB, agentID string) int {
	t.Helper()
	var role string
	_ = db.QueryRow(`SELECT COALESCE(role,'') FROM agents WHERE agent_id = ?`, agentID).Scan(&role)
	values := buildForAgentValues(agentID, role)
	clause, args := buildForAgentClause(values, agentID, role)
	q := `SELECT COUNT(*) FROM messages m WHERE m.deleted = 0` + clause +
		` AND m.message_id NOT IN (SELECT md.message_id FROM message_deliveries md
		   WHERE md.recipient_agent_id = ? AND md.read_at IS NOT NULL)`
	args = append(args, agentID)
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("rawInboxVisibleUnreadCount: %v", err)
	}
	return n
}

// TestReadStateRoundTrip_AllClasses exercises unread -> mark -> read for all
// three read-state classes so none can regress silently:
//   - legacy-broadcast no-delivery-row (created+stamped via the qb62 gate)
//   - delivery-backed unread (stamped via the receipt update)
//   - authored-self (pre-read at send via the T3 self-delivery row; not
//     inbox-visible on this line, asserted directly)
func TestReadStateRoundTrip_AllClasses(t *testing.T) {
	handler, agentID, cleanup := setupFilterTest(t)
	defer cleanup()
	ctx := context.Background()
	db := handler.state.RawDB()
	opsID := identity.GenerateAgentID("r_FILTER_TEST", "ops", "core", "")

	// Class 1: legacy-broadcast no-delivery-row (raw insert, no targeting).
	mustExecRaw(t, db, `INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES ('c1', 'bob', 'ses_x', '2030-01-01T00:00:00Z', 'markdown', 'legacy')`)
	// Class 2: authored-self — the test agent sends to @ops through the full
	// path, so the T3 read-stamped author self-row fires.
	sendParams, _ := json.Marshal(SendRequest{Content: "from me", To: "@" + opsID, CallerAgentID: agentID})
	resp, err := handler.HandleSend(ctx, sendParams)
	if err != nil {
		t.Fatalf("send authored-self: %v", err)
	}
	c2 := resp.(*SendResponse).MessageID
	// Class 3: delivery-backed unread — ops sends to @reviewer.
	sendParams, _ = json.Marshal(SendRequest{Content: "to reviewer", Mentions: []string{"@reviewer"}, CallerAgentID: opsID})
	resp, err = handler.HandleSend(ctx, sendParams)
	if err != nil {
		t.Fatalf("send delivery-backed: %v", err)
	}
	c3 := resp.(*SendResponse).MessageID

	// Authored-self must be pre-read at send (T3) — asserted directly since it
	// is not inbox-visible on this line.
	var selfReadAt sql.NullString
	if err := db.QueryRow(`SELECT read_at FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ?`, c2, agentID).Scan(&selfReadAt); err != nil {
		t.Fatalf("query authored-self row: %v", err)
	}
	if !selfReadAt.Valid {
		t.Fatalf("authored-self delivery row must be pre-read at send (T3)")
	}

	// Pre: unread = {c1 legacy, c3 mention}.
	if u := rawInboxVisibleUnreadCount(t, db, agentID); u != 2 {
		t.Fatalf("pre unread=%d want 2 (c1 legacy + c3 mention)", u)
	}

	// Per-ID mark-read (the release-line `read --all` shape: CLI enumerates,
	// daemon marks the explicit IDs).
	markParams, _ := json.Marshal(MarkReadRequest{MessageIDs: []string{"c1", c3}, CallerAgentID: agentID})
	if _, err := handler.HandleMarkRead(ctx, markParams); err != nil {
		t.Fatalf("markRead: %v", err)
	}
	if u := rawInboxVisibleUnreadCount(t, db, agentID); u != 0 {
		t.Fatalf("post unread=%d want 0 — a class did not clear", u)
	}

	// Idempotency: re-marking is a clean no-op (no dupes, still 0 unread).
	if _, err := handler.HandleMarkRead(ctx, markParams); err != nil {
		t.Fatalf("second markRead: %v", err)
	}
	if u := rawInboxVisibleUnreadCount(t, db, agentID); u != 0 {
		t.Fatalf("post second mark unread=%d want 0", u)
	}
	var c1Rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM message_deliveries WHERE message_id='c1' AND recipient_agent_id=?`, agentID).Scan(&c1Rows); err != nil {
		t.Fatalf("count c1 rows: %v", err)
	}
	if c1Rows != 1 {
		t.Fatalf("c1 delivery rows=%d want exactly 1 after double mark", c1Rows)
	}
}

// TestMarkRead_PerID_ClearsNoDeliveryRow proves the explicit per-ID markRead
// path clears a no-delivery-row legacy broadcast via the qb62 gate — the exact
// shape `thrum message read <id>` and the CLI-enumerated `read --all` use.
func TestMarkRead_PerID_ClearsNoDeliveryRow(t *testing.T) {
	handler, agentID, cleanup := setupFilterTest(t)
	defer cleanup()
	ctx := context.Background()
	db := handler.state.RawDB()

	mustExecRaw(t, db, `INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES ('p1', 'bob', 'ses_x', '2030-01-01T00:00:00Z', 'markdown', 'legacy noise')`)
	if u := rawInboxVisibleUnreadCount(t, db, agentID); u != 1 {
		t.Fatalf("pre unread=%d want 1", u)
	}
	markParams, _ := json.Marshal(MarkReadRequest{MessageIDs: []string{"p1"}, CallerAgentID: agentID})
	if _, err := handler.HandleMarkRead(ctx, markParams); err != nil {
		t.Fatalf("per-id markRead: %v", err)
	}
	if u := rawInboxVisibleUnreadCount(t, db, agentID); u != 0 {
		t.Fatalf("post unread=%d want 0 (per-id path must clear no-delivery-row)", u)
	}
}

// TestReadState_CrossVersion_PreV40ToV40 is the "both directions" guard: a v40
// binary reading a pre-v40 (v39) DB sees the no-delivery-row unaddressed
// message as unread BEFORE the crossing, and as 0 unread AFTER the v39→v40
// migration's one-time backfill runs.
func TestReadState_CrossVersion_PreV40ToV40(t *testing.T) {
	const localDaemon = "daemon-A"
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	if err := os.MkdirAll(filepath.Join(thrumDir, "var"), 0o750); err != nil {
		t.Fatalf("mkdir var: %v", err)
	}

	// Seed a pre-v40 DB with a stuck local no-delivery-row legacy broadcast.
	dbPath := filepath.Join(thrumDir, "var", "messages.db")
	seed, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if err := schema.InitDB(seed); err != nil {
		t.Fatalf("init seed db: %v", err)
	}
	mustExecRaw(t, seed, `UPDATE schema_version SET version = 39`)
	mustExecRaw(t, seed, `INSERT OR IGNORE INTO agents(agent_id, kind, role, module, registered_at, origin_daemon) VALUES('user:leon-letto','claude','','test',datetime('now'), ?)`, localDaemon)
	mustExecRaw(t, seed, `INSERT INTO messages(message_id, agent_id, session_id, body_format, body_content, created_at, deleted) VALUES('old-broadcast','bob','ses_test','markdown','x','2026-05-22T00:00:00Z',0)`)

	// Direction 1: pre-migration data reports the stuck message unread.
	if got := rawInboxVisibleUnreadCount(t, seed, "user:leon-letto"); got != 1 {
		t.Fatalf("pre-migration unread=%d want 1", got)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	// Cross to v40 (state.NewState runs the one-time backfill).
	st, err := state.NewState(thrumDir, syncDir, "r_xver", localDaemon)
	if err != nil {
		t.Fatalf("NewState (v39->v40 crossing): %v", err)
	}
	defer func() { _ = st.Close() }()

	// Direction 2: post-migration, the same agent's inbox-visible unread is 0.
	if got := rawInboxVisibleUnreadCount(t, st.RawDB(), "user:leon-letto"); got != 0 {
		t.Fatalf("post-migration unread=%d want 0", got)
	}
}
