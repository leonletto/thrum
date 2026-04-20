package telegram

import (
	"context"
	"testing"
	"time"
)

// TestSweepStale_DeletesOldOrphans covers the common-case acceptance:
// rows older than the TTL that are NOT referenced by a live
// permission_nudges row are deleted by the sweep.
func TestSweepStale_DeletesOldOrphans(t *testing.T) {
	db := testDB(t)

	// Two rows old enough to be stale.
	oldEpoch := time.Now().Add(-40 * 24 * time.Hour).Unix()
	_, err := db.Exec(
		`INSERT INTO telegram_msg_map (external_key, thrum_msg_id, created_at)
		 VALUES (?, ?, ?), (?, ?, ?)`,
		"100:1", "msg_orphan_a", oldEpoch,
		"100:2", "msg_orphan_b", oldEpoch,
	)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	deleted, err := SweepStale(context.Background(), db, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM telegram_msg_map`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Errorf("remaining rows = %d, want 0", remaining)
	}
}

// TestSweepStale_KeepsPendingNudgeRows is the critical safety invariant:
// even if a Telegram mapping has aged past the TTL, we MUST NOT delete
// it while the referenced permission_nudges row is still live —
// otherwise a supervisor reply arriving after the sweep would silently
// drop reply_to threading (the exact bug 48kt.2 exists to prevent).
func TestSweepStale_KeepsPendingNudgeRows(t *testing.T) {
	db := testDB(t)

	// Aged mapping whose thrum_msg_id IS a live pending nudge.
	oldEpoch := time.Now().Add(-45 * 24 * time.Hour).Unix()
	_, err := db.Exec(
		`INSERT INTO telegram_msg_map (external_key, thrum_msg_id, created_at)
		 VALUES (?, ?, ?)`,
		"200:5", "msg_still_pending", oldEpoch,
	)
	if err != nil {
		t.Fatalf("seed msgmap: %v", err)
	}

	// Seed the referenced permission_nudges row. The cross-check joins
	// telegram_msg_map.thrum_msg_id = permission_nudges.message_id.
	_, err = db.Exec(`
		INSERT INTO permission_nudges (
			message_id, session, tmux_target, agent_name, pattern_key,
			approve_key, deny_key, first_detected, last_nudge_at,
			nudge_count, last_pane_hash, expires_at
		)
		VALUES (?, 'sess', 'tmux:0', 'agent', 'pat', 'y', 'n', ?, ?, 1, X'00', ?)`,
		"msg_still_pending",
		time.Now().UTC().Format(time.RFC3339Nano),
		time.Now().UTC().Format(time.RFC3339Nano),
		time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("seed nudge: %v", err)
	}

	deleted, err := SweepStale(context.Background(), db, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 (pending nudge mapping must survive)", deleted)
	}

	// Verify the row is still present.
	var id string
	if err := db.QueryRow(
		`SELECT thrum_msg_id FROM telegram_msg_map WHERE external_key = ?`, "200:5",
	).Scan(&id); err != nil {
		t.Fatalf("mapping missing after sweep: %v", err)
	}
	if id != "msg_still_pending" {
		t.Errorf("mapping = %q, want msg_still_pending", id)
	}
}

// TestSweepStale_KeepsYoungRows verifies the age predicate: mappings
// newer than the TTL are untouched regardless of whether a pending
// nudge exists. Without this the sweep would collapse to "delete
// everything not pinned", which would break active threading.
func TestSweepStale_KeepsYoungRows(t *testing.T) {
	db := testDB(t)

	recentEpoch := time.Now().Add(-1 * time.Hour).Unix()
	_, err := db.Exec(
		`INSERT INTO telegram_msg_map (external_key, thrum_msg_id, created_at)
		 VALUES (?, ?, ?)`,
		"300:9", "msg_fresh", recentEpoch,
	)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	deleted, err := SweepStale(context.Background(), db, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM telegram_msg_map`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("remaining = %d, want 1", count)
	}
}

// TestSweepStale_Idempotent — running sweep twice in a row should have
// no additional effect on the second call. Protects against accidental
// double-delete semantics or non-deterministic predicates.
func TestSweepStale_Idempotent(t *testing.T) {
	db := testDB(t)

	oldEpoch := time.Now().Add(-60 * 24 * time.Hour).Unix()
	_, err := db.Exec(
		`INSERT INTO telegram_msg_map (external_key, thrum_msg_id, created_at)
		 VALUES (?, ?, ?)`,
		"400:1", "msg_very_old", oldEpoch,
	)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	first, err := SweepStale(context.Background(), db, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("first SweepStale: %v", err)
	}
	if first != 1 {
		t.Errorf("first deleted = %d, want 1", first)
	}

	second, err := SweepStale(context.Background(), db, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("second SweepStale: %v", err)
	}
	if second != 0 {
		t.Errorf("second deleted = %d, want 0 (idempotent)", second)
	}
}

// TestSweepStale_EmptyTable — sweep against an empty table must not
// error and must return 0. Guards against a NULL/NOT-NULL predicate
// trip or a zero-row panic inside the scanner.
func TestSweepStale_EmptyTable(t *testing.T) {
	db := testDB(t)
	deleted, err := SweepStale(context.Background(), db, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("SweepStale on empty: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
}

// TestSweepStale_MixedOutcome — all three cases in a single DB. Proves
// the predicate composes: old+orphan deletes, old+pending keeps,
// young+orphan keeps. This is the closest proxy to production steady
// state and is the test that would catch a predicate typo in practice.
func TestSweepStale_MixedOutcome(t *testing.T) {
	db := testDB(t)

	oldEpoch := time.Now().Add(-40 * 24 * time.Hour).Unix()
	recentEpoch := time.Now().Add(-1 * time.Hour).Unix()
	_, err := db.Exec(`
		INSERT INTO telegram_msg_map (external_key, thrum_msg_id, created_at)
		VALUES
			('500:1', 'msg_old_orphan',  ?),
			('500:2', 'msg_old_pending', ?),
			('500:3', 'msg_young_orphan', ?)`,
		oldEpoch, oldEpoch, recentEpoch,
	)
	if err != nil {
		t.Fatalf("seed msgmap: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO permission_nudges (
			message_id, session, tmux_target, agent_name, pattern_key,
			approve_key, deny_key, first_detected, last_nudge_at,
			nudge_count, last_pane_hash, expires_at
		)
		VALUES (?, 'sess', 'tmux:0', 'agent', 'pat', 'y', 'n', ?, ?, 1, X'00', ?)`,
		"msg_old_pending",
		time.Now().UTC().Format(time.RFC3339Nano),
		time.Now().UTC().Format(time.RFC3339Nano),
		time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("seed nudge: %v", err)
	}

	deleted, err := SweepStale(context.Background(), db, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("SweepStale: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (only the old orphan)", deleted)
	}

	// The old+pending and young+orphan rows must remain.
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM telegram_msg_map`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 2 {
		t.Errorf("remaining = %d, want 2", remaining)
	}

	// Spot-check the identities of the survivors.
	survivors := map[string]bool{}
	rows, err := db.Query(`SELECT thrum_msg_id FROM telegram_msg_map`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		survivors[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration: %v", err)
	}
	if !survivors["msg_old_pending"] {
		t.Error("msg_old_pending was deleted; pending-nudge cross-check broken")
	}
	if !survivors["msg_young_orphan"] {
		t.Error("msg_young_orphan was deleted; age predicate broken")
	}
	if survivors["msg_old_orphan"] {
		t.Error("msg_old_orphan survived; age predicate broken")
	}
}

// TestSweepStale_NilDB — nil db handle must be a no-op, not a panic.
// Matches the defensive pattern across the rest of this package
// (SetDB, ThrumID, TeleID all nil-check before touching *sql.DB).
func TestSweepStale_NilDB(t *testing.T) {
	deleted, err := SweepStale(context.Background(), nil, 30*24*time.Hour)
	if err != nil {
		t.Errorf("SweepStale(nil) err = %v, want nil", err)
	}
	if deleted != 0 {
		t.Errorf("SweepStale(nil) deleted = %d, want 0", deleted)
	}
}
