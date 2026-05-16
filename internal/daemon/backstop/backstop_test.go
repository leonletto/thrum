package backstop

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/schema"
	_ "modernc.org/sqlite"
)

// fakeDispatcher records calls so tests can assert which agents got nudged.
type fakeDispatcher struct {
	mu    sync.Mutex
	calls []dispatchCall
}

type dispatchCall struct {
	agentID string
	count   int
}

func (f *fakeDispatcher) Dispatch(ctx context.Context, agentID string, count int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, dispatchCall{agentID: agentID, count: count})
	return nil
}

func (f *fakeDispatcher) Calls() []dispatchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]dispatchCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// newTestDB opens an in-memory SQLite with the project schema and wraps
// it in safedb.New per the established daemon-test pattern (see
// internal/daemon/safedb/safedb_test.go:23, internal/groups/resolver_test.go).
func newTestDB(t *testing.T) (*safedb.DB, func()) {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := schema.InitDB(raw); err != nil {
		_ = raw.Close()
		t.Fatalf("init schema: %v", err)
	}
	return safedb.New(raw), func() { _ = raw.Close() }
}

// seedAgent inserts an agents row. alive=true sets last_seen_at to "now",
// alive=false leaves it 2 hours in the past so the Backstop's AliveWindow
// filter rejects the row.
func seedAgent(t *testing.T, db *safedb.DB, agentID string, alive bool) {
	t.Helper()
	lastSeen := time.Now().UTC().Format(time.RFC3339Nano)
	if !alive {
		lastSeen = time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	}
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO agents (agent_id, kind, role, module, registered_at, last_seen_at)
		 VALUES (?, 'named', 'tester', 'test', ?, ?)`,
		agentID, lastSeen, lastSeen)
	if err != nil {
		t.Fatalf("seed agent %s: %v", agentID, err)
	}
}

// seedDelivery inserts a message_deliveries row. delivered_at is required;
// seen_at and read_at may be empty strings to leave them NULL-equivalent
// (SQLite stores them as empty TEXT, but the query uses `read_at IS NULL`).
// To get NULL we omit those columns from the INSERT when the caller passes
// empty strings.
func seedDelivery(t *testing.T, db *safedb.DB, msgID, agentID, deliveredAt, seenAt, readAt string) {
	t.Helper()
	// First ensure the message row exists; queryStaleUnread joins on
	// recipient_agent_id only, but the foreign-key-like assumption is
	// that the message row exists in real data. The test schema doesn't
	// strictly enforce FK, but we insert a stub for realism.
	_, err := db.ExecContext(context.Background(),
		`INSERT OR IGNORE INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		 VALUES (?, 'sender', 'ses_x', ?, 'markdown', 'stub')`,
		msgID, deliveredAt)
	if err != nil {
		t.Fatalf("seed message %s: %v", msgID, err)
	}

	switch {
	case seenAt == "" && readAt == "":
		_, err = db.ExecContext(context.Background(),
			`INSERT INTO message_deliveries (message_id, recipient_agent_id, delivered_at)
			 VALUES (?, ?, ?)`, msgID, agentID, deliveredAt)
	case readAt == "":
		_, err = db.ExecContext(context.Background(),
			`INSERT INTO message_deliveries (message_id, recipient_agent_id, delivered_at, seen_at)
			 VALUES (?, ?, ?, ?)`, msgID, agentID, deliveredAt, seenAt)
	default:
		_, err = db.ExecContext(context.Background(),
			`INSERT INTO message_deliveries (message_id, recipient_agent_id, delivered_at, seen_at, read_at)
			 VALUES (?, ?, ?, ?, ?)`, msgID, agentID, deliveredAt, seenAt, readAt)
	}
	if err != nil {
		t.Fatalf("seed delivery for %s/%s: %v", msgID, agentID, err)
	}
}

func TestTick_NudgesAliveAgentsWithStaleUnread(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	// alice: 1 message, delivered 16 min ago, unread, alive → should nudge
	// bob:   1 message, delivered 16 min ago, unread, OFFLINE → no nudge
	// carol: 1 message, delivered 5 min ago, unread, alive → no nudge (too fresh)
	// dave:  1 message, delivered 16 min ago, READ → no nudge
	now := time.Now().UTC()
	stale := now.Add(-16 * time.Minute).Format(time.RFC3339Nano)
	fresh := now.Add(-5 * time.Minute).Format(time.RFC3339Nano)

	seedAgent(t, db, "alice", true)
	seedAgent(t, db, "bob", false)
	seedAgent(t, db, "carol", true)
	seedAgent(t, db, "dave", true)
	seedDelivery(t, db, "msg_a", "alice", stale, "", "")
	seedDelivery(t, db, "msg_b", "bob", stale, "", "")
	seedDelivery(t, db, "msg_c", "carol", fresh, "", "")
	seedDelivery(t, db, "msg_d", "dave", stale, "", stale)

	disp := &fakeDispatcher{}
	bs := &Backstop{
		DB:        db,
		Dispatch:  disp,
		AgeCutoff: 15 * time.Minute,
		Now:       func() time.Time { return now },
	}
	if err := bs.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	calls := disp.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected single nudge, got %+v", calls)
	}
	if calls[0].agentID != "alice" {
		t.Fatalf("expected nudge to alice, got %+v", calls[0])
	}
	if calls[0].count != 1 {
		t.Fatalf("expected count=1, got %d", calls[0].count)
	}
}

func TestTick_EmptyWhenNoBacklog(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	disp := &fakeDispatcher{}
	bs := &Backstop{
		DB:        db,
		Dispatch:  disp,
		AgeCutoff: 15 * time.Minute,
		Now:       time.Now,
	}
	if err := bs.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if calls := disp.Calls(); len(calls) != 0 {
		t.Fatalf("expected no dispatches, got %+v", calls)
	}
}

// TestTick_AggregatesPerAgent verifies that multiple stale messages for
// the same recipient produce a single dispatch with the aggregated count.
func TestTick_AggregatesPerAgent(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	now := time.Now().UTC()
	stale := now.Add(-20 * time.Minute).Format(time.RFC3339Nano)

	seedAgent(t, db, "alice", true)
	seedDelivery(t, db, "msg_1", "alice", stale, "", "")
	seedDelivery(t, db, "msg_2", "alice", stale, "", "")
	seedDelivery(t, db, "msg_3", "alice", stale, "", "")

	disp := &fakeDispatcher{}
	bs := &Backstop{
		DB:        db,
		Dispatch:  disp,
		AgeCutoff: 15 * time.Minute,
		Now:       func() time.Time { return now },
	}
	if err := bs.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	calls := disp.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected single aggregated nudge, got %+v", calls)
	}
	if calls[0].count != 3 {
		t.Fatalf("expected count=3, got %d", calls[0].count)
	}
}
