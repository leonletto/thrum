package telegram

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/schema"
)

// testDB creates a fresh SQLite database with the thrum-48kt.2
// telegram_msg_map table (via the full schema migration chain).
// Returns a closable *sql.DB.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "msgmap_test.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("schema.OpenDB: %v", err)
	}
	if err := schema.Migrate(db); err != nil {
		_ = db.Close()
		t.Fatalf("schema.Migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestMessageMap_InMemoryBackwardCompat verifies that the pre-48kt.2
// constructor (no DB) behaves exactly as before: Store/ThrumID/TeleID
// round-trip through the cache, no DB required.
func TestMessageMap_InMemoryBackwardCompat(t *testing.T) {
	mm := NewMessageMap(10)
	mm.Store(100, 42, "msg_alpha")

	id, ok := mm.ThrumID(100, 42)
	if !ok || id != "msg_alpha" {
		t.Errorf("ThrumID(100,42) = (%q, %v); want (msg_alpha, true)", id, ok)
	}

	chatID, teleID, ok := mm.TeleID("msg_alpha")
	if !ok || chatID != 100 || teleID != 42 {
		t.Errorf("TeleID(msg_alpha) = (%d, %d, %v); want (100, 42, true)", chatID, teleID, ok)
	}

	if got := mm.Len(); got != 1 {
		t.Errorf("Len = %d; want 1", got)
	}
}

// TestMessageMap_SQLiteWriteThrough verifies Store writes to both the
// cache and the telegram_msg_map table when a DB is wired.
func TestMessageMap_SQLiteWriteThrough(t *testing.T) {
	db := testDB(t)
	mm := NewMessageMapWithDB(10, db)

	mm.Store(200, 99, "msg_beta")

	// Cache has it.
	if id, ok := mm.ThrumID(200, 99); !ok || id != "msg_beta" {
		t.Errorf("cache ThrumID = (%q, %v); want (msg_beta, true)", id, ok)
	}

	// DB has it.
	var dbID string
	err := db.QueryRow(
		`SELECT thrum_msg_id FROM telegram_msg_map WHERE external_key = ?`,
		"200:99",
	).Scan(&dbID)
	if err != nil {
		t.Fatalf("DB read: %v", err)
	}
	if dbID != "msg_beta" {
		t.Errorf("DB thrum_msg_id = %q; want msg_beta", dbID)
	}
}

// TestMessageMap_SQLiteReadFallback verifies that when a mapping is NOT
// in the cache but IS in the DB, ThrumID falls back and returns it. This
// is the pre-48kt.2 bug's inverse: proves post-restart mappings resolve.
func TestMessageMap_SQLiteReadFallback(t *testing.T) {
	db := testDB(t)

	// Seed the DB directly without touching a MessageMap cache.
	_, err := db.Exec(
		`INSERT INTO telegram_msg_map (external_key, thrum_msg_id, created_at)
		 VALUES (?, ?, ?)`,
		"300:101", "msg_gamma", 1714000000,
	)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Fresh MessageMap — cache is empty.
	mm := NewMessageMapWithDB(10, db)
	if got := mm.Len(); got != 0 {
		t.Errorf("fresh cache Len = %d; want 0", got)
	}

	id, ok := mm.ThrumID(300, 101)
	if !ok || id != "msg_gamma" {
		t.Errorf("DB-fallback ThrumID = (%q, %v); want (msg_gamma, true)", id, ok)
	}

	// After the fallback hit, the cache should be warmed.
	if got := mm.Len(); got != 1 {
		t.Errorf("post-fallback Len = %d; want 1 (cache warmed)", got)
	}

	// Reverse lookup also works via DB fallback.
	chatID, teleID, ok := mm.TeleID("msg_gamma")
	if !ok || chatID != 300 || teleID != 101 {
		t.Errorf("DB-fallback TeleID = (%d, %d, %v); want (300, 101, true)", chatID, teleID, ok)
	}
}

// TestMessageMap_RestartScenario is the exact bug this bead fixes:
// daemon writes a mapping, then the daemon "restarts" (construct a
// new MessageMap with the same DB), and the old mapping resolves.
func TestMessageMap_RestartScenario(t *testing.T) {
	db := testDB(t)

	// Pre-restart: daemon writes nudge mapping.
	mm1 := NewMessageMapWithDB(10, db)
	mm1.Store(400, 7, "msg_nudge_pending")

	// Daemon "restarts": new MessageMap instance, same DB.
	mm2 := NewMessageMapWithDB(10, db)

	if got := mm2.Len(); got != 0 {
		t.Errorf("post-restart Len = %d; want 0 (fresh cache)", got)
	}

	// The critical post-restart reply from supervisor.
	id, ok := mm2.ThrumID(400, 7)
	if !ok {
		t.Fatal("post-restart ThrumID = !ok; the 48kt.2 bug — mapping should resolve")
	}
	if id != "msg_nudge_pending" {
		t.Errorf("post-restart ThrumID = %q; want msg_nudge_pending", id)
	}

	// Reverse lookup survives restart too.
	chatID, teleID, ok := mm2.TeleID("msg_nudge_pending")
	if !ok || chatID != 400 || teleID != 7 {
		t.Errorf("post-restart TeleID = (%d, %d, %v); want (400, 7, true)", chatID, teleID, ok)
	}
}

// TestMessageMap_LRUEvictionDoesntLoseFromDB verifies that overflowing
// the cache does NOT lose mappings from the DB. The cache evicts
// oldest-first; DB retains all entries.
func TestMessageMap_LRUEvictionDoesntLoseFromDB(t *testing.T) {
	db := testDB(t)
	mm := NewMessageMapWithDB(3, db) // tiny cache to force eviction

	// Store 5 mappings; cache holds 3, DB holds all 5.
	for i := 1; i <= 5; i++ {
		mm.Store(500, i, keyFor(i))
	}

	// Cache: should hold the 3 most recent (i=3,4,5). Oldest (1, 2) evicted.
	if got := mm.Len(); got != 3 {
		t.Errorf("post-overflow cache Len = %d; want 3", got)
	}

	// Evicted entries MUST still resolve via DB fallback.
	id, ok := mm.ThrumID(500, 1)
	if !ok || id != keyFor(1) {
		t.Errorf("evicted (500,1) ThrumID = (%q, %v); want (%s, true)", id, ok, keyFor(1))
	}

	id, ok = mm.ThrumID(500, 2)
	if !ok || id != keyFor(2) {
		t.Errorf("evicted (500,2) ThrumID = (%q, %v); want (%s, true)", id, ok, keyFor(2))
	}

	// Still-cached entries resolve normally.
	id, ok = mm.ThrumID(500, 5)
	if !ok || id != keyFor(5) {
		t.Errorf("cached (500,5) ThrumID = (%q, %v); want (%s, true)", id, ok, keyFor(5))
	}
}

// TestMessageMap_StoreOverwrite verifies the INSERT OR REPLACE semantics:
// storing the same external key twice updates the thrum_msg_id to the
// latest value in both cache and DB (matches in-memory Store behavior).
func TestMessageMap_StoreOverwrite(t *testing.T) {
	db := testDB(t)
	mm := NewMessageMapWithDB(10, db)

	mm.Store(600, 1, "msg_first")
	mm.Store(600, 1, "msg_second") // same external key, different thrum id

	id, ok := mm.ThrumID(600, 1)
	if !ok || id != "msg_second" {
		t.Errorf("overwrite cache = (%q, %v); want (msg_second, true)", id, ok)
	}

	var dbID string
	if err := db.QueryRow(
		`SELECT thrum_msg_id FROM telegram_msg_map WHERE external_key = ?`,
		"600:1",
	).Scan(&dbID); err != nil {
		t.Fatalf("DB read: %v", err)
	}
	if dbID != "msg_second" {
		t.Errorf("overwrite DB = %q; want msg_second", dbID)
	}
}

func keyFor(i int) string {
	return "msg_id_" + string(rune('A'+i-1))
}
