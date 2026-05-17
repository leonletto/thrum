package email_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/schema"
)

// D-B1.8 — Dedup table is email_msg_seen (canonical-ref §3.7); the
// dedup layer is L4 in the inbound loop-protection set (design-spec §9
// step 3). Idempotent re-fetch + retry protection on the IMAP path.

func openDedupDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "dedup.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDedup_FirstInsertReturnsFalse(t *testing.T) {
	db := openDedupDB(t)
	d := email.NewDedup(db)

	alreadySeen, err := d.SeenOrInsert(t.Context(), "<msg-1@host>", "daemon-1", "", time.Now())
	if err != nil {
		t.Fatalf("SeenOrInsert: %v", err)
	}
	if alreadySeen {
		t.Error("first insert returned alreadySeen=true; want false")
	}

	// Row landed in DB.
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM email_msg_seen WHERE message_id=?`, "<msg-1@host>").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row after first insert, got %d", count)
	}
}

func TestDedup_SecondInsertReturnsTrue(t *testing.T) {
	db := openDedupDB(t)
	d := email.NewDedup(db)
	ctx := t.Context()

	if _, err := d.SeenOrInsert(ctx, "<msg-1@host>", "daemon-1", "", time.Now()); err != nil {
		t.Fatalf("SeenOrInsert 1st: %v", err)
	}
	alreadySeen, err := d.SeenOrInsert(ctx, "<msg-1@host>", "daemon-1", "", time.Now())
	if err != nil {
		t.Fatalf("SeenOrInsert 2nd: %v", err)
	}
	if !alreadySeen {
		t.Error("second insert returned alreadySeen=false; want true")
	}

	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM email_msg_seen WHERE message_id=?`, "<msg-1@host>").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row after duplicate insert, got %d (ON CONFLICT failed)", count)
	}
}

func TestDedup_NullableNonceOk(t *testing.T) {
	db := openDedupDB(t)
	d := email.NewDedup(db)

	if _, err := d.SeenOrInsert(t.Context(), "<msg-2@host>", "daemon-1", "", time.Now()); err != nil {
		t.Fatalf("SeenOrInsert with empty nonce: %v", err)
	}

	var nonceIsNull bool
	err := db.QueryRow(`SELECT nonce IS NULL FROM email_msg_seen WHERE message_id=?`, "<msg-2@host>").Scan(&nonceIsNull)
	if err != nil {
		t.Fatalf("query nonce: %v", err)
	}
	if !nonceIsNull {
		t.Error("empty nonce stored as non-NULL; expected NULL")
	}
}

func TestDedup_NullableFromDaemonOk(t *testing.T) {
	db := openDedupDB(t)
	d := email.NewDedup(db)

	if _, err := d.SeenOrInsert(t.Context(), "<msg-3@host>", "", "", time.Now()); err != nil {
		t.Fatalf("SeenOrInsert with empty fromDaemonID: %v", err)
	}

	var fromIsNull bool
	err := db.QueryRow(`SELECT from_daemon_id IS NULL FROM email_msg_seen WHERE message_id=?`, "<msg-3@host>").Scan(&fromIsNull)
	if err != nil {
		t.Fatalf("query from_daemon_id: %v", err)
	}
	if !fromIsNull {
		t.Error("empty fromDaemonID stored as non-NULL; expected NULL")
	}
}

func TestDedup_SweeperDrops30dOldEntries(t *testing.T) {
	db := openDedupDB(t)
	d := email.NewDedup(db)
	ctx := t.Context()

	old := time.Now().Add(-40 * 24 * time.Hour)
	if _, err := d.SeenOrInsert(ctx, "<old@host>", "daemon-1", "", old); err != nil {
		t.Fatalf("seed old: %v", err)
	}
	if _, err := d.SeenOrInsert(ctx, "<fresh@host>", "daemon-1", "", time.Now()); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}

	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	deleted, err := d.Sweep(ctx, cutoff)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if deleted != 1 {
		t.Errorf("Sweep deleted=%d, want 1", deleted)
	}

	var cnt int
	_ = db.QueryRow(`SELECT COUNT(*) FROM email_msg_seen WHERE message_id=?`, "<old@host>").Scan(&cnt)
	if cnt != 0 {
		t.Error("Sweep did not drop old row")
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM email_msg_seen WHERE message_id=?`, "<fresh@host>").Scan(&cnt)
	if cnt != 1 {
		t.Error("Sweep dropped fresh row")
	}
}

func TestDedup_SweeperPreservesRecent(t *testing.T) {
	db := openDedupDB(t)
	d := email.NewDedup(db)
	ctx := t.Context()

	for i := range 5 {
		id := "<recent-" + itoaDedup(i) + "@host>"
		if _, err := d.SeenOrInsert(ctx, id, "daemon-1", "", time.Now()); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	deleted, err := d.Sweep(ctx, cutoff)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if deleted != 0 {
		t.Errorf("Sweep deleted=%d recent rows; want 0", deleted)
	}
}

func TestDedup_ConcurrentInsertsNoRace(t *testing.T) {
	db := openDedupDB(t)
	d := email.NewDedup(db)
	ctx := t.Context()

	var wg sync.WaitGroup
	seenCount := make([]bool, 100)
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			alreadySeen, err := d.SeenOrInsert(ctx, "<concurrent@host>", "daemon-1", "", time.Now())
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			seenCount[i] = alreadySeen
		}(i)
	}
	wg.Wait()

	// Exactly one row in DB.
	var cnt int
	_ = db.QueryRow(`SELECT COUNT(*) FROM email_msg_seen WHERE message_id=?`, "<concurrent@host>").Scan(&cnt)
	if cnt != 1 {
		t.Errorf("expected exactly 1 row after concurrent insert, got %d", cnt)
	}

	// Exactly one goroutine saw alreadySeen=false.
	firstWins := 0
	for _, s := range seenCount {
		if !s {
			firstWins++
		}
	}
	if firstWins != 1 {
		t.Errorf("expected exactly 1 goroutine to see alreadySeen=false (the winner), got %d", firstWins)
	}
}

// Run uses context.Background here intentionally since t.Context() is the
// per-test ctx; we want the sweeper test to verify deletion via the
// production code path, not via a test-scoped helper.
var _ = context.Background

// itoaDedup is a tiny ASCII-only int-to-string helper to avoid importing
// strconv just for the loop-index in TestDedup_SweeperPreservesRecent.
func itoaDedup(i int) string {
	if i == 0 {
		return "0"
	}
	var b [3]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = "0123456789"[i%10]
		i /= 10
	}
	return string(b[pos:])
}
