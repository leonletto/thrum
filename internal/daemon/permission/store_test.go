package permission

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/schema"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func fixtureRow() *NudgeRow {
	now := time.Now().UTC().Truncate(time.Second)
	return &NudgeRow{
		MessageID:     "msg_test_01",
		Session:       "cursor-test",
		TmuxTarget:    "cursor-test:0.0",
		AgentName:     "researcher_cursor",
		PatternKey:    "cursor.not_in_allowlist",
		ApproveKey:    "y",
		DenyKey:       "Escape",
		FirstDetected: now,
		LastNudgeAt:   now,
		NudgeCount:    1,
		LastPaneHash:  [32]byte{1, 2, 3, 4},
		ExpiresAt:     now.Add(8 * time.Hour),
	}
}

func TestStore_InsertAndLookupByMessageID(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	row := fixtureRow()
	if err := s.InsertPendingNudge(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := s.LookupPendingNudgeByMessageID(ctx, row.MessageID)
	if err != nil {
		t.Fatalf("LookupByMessageID: %v", err)
	}
	if got == nil {
		t.Fatal("expected row, got nil")
	}
	if got.Session != row.Session {
		t.Errorf("Session = %q, want %q", got.Session, row.Session)
	}
	if got.TmuxTarget != row.TmuxTarget {
		t.Errorf("TmuxTarget = %q, want %q", got.TmuxTarget, row.TmuxTarget)
	}
	if got.AgentName != row.AgentName {
		t.Errorf("AgentName = %q, want %q", got.AgentName, row.AgentName)
	}
	if got.PatternKey != row.PatternKey {
		t.Errorf("PatternKey = %q, want %q", got.PatternKey, row.PatternKey)
	}
	if got.ApproveKey != row.ApproveKey {
		t.Errorf("ApproveKey = %q, want %q", got.ApproveKey, row.ApproveKey)
	}
	if got.DenyKey != row.DenyKey {
		t.Errorf("DenyKey = %q, want %q", got.DenyKey, row.DenyKey)
	}
	if got.NudgeCount != row.NudgeCount {
		t.Errorf("NudgeCount = %d, want %d", got.NudgeCount, row.NudgeCount)
	}
	if got.LastPaneHash != row.LastPaneHash {
		t.Errorf("LastPaneHash = %x, want %x", got.LastPaneHash, row.LastPaneHash)
	}
	if !got.FirstDetected.Equal(row.FirstDetected) {
		t.Errorf("FirstDetected = %v, want %v", got.FirstDetected, row.FirstDetected)
	}
	if !got.ExpiresAt.Equal(row.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, row.ExpiresAt)
	}
}

func TestStore_LookupByMessageID_NotFound(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	got, err := s.LookupPendingNudgeByMessageID(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil row for missing id, got %+v", got)
	}
}

func TestStore_InsertDuplicateMessageID(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	row := fixtureRow()
	if err := s.InsertPendingNudge(ctx, row); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := s.InsertPendingNudge(ctx, row); err == nil {
		t.Error("expected error on duplicate primary key insert, got nil")
	}
}

func TestStore_LookupBySession(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	row := fixtureRow()
	if err := s.InsertPendingNudge(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := s.LookupPendingNudgeBySession(ctx, row.Session)
	if err != nil || got == nil {
		t.Fatalf("LookupBySession: %v, row=%v", err, got)
	}
	if got.MessageID != row.MessageID {
		t.Errorf("MessageID mismatch: got %q, want %q", got.MessageID, row.MessageID)
	}
}

func TestStore_LookupBySession_NotFound(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	got, err := s.LookupPendingNudgeBySession(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil row for missing session, got %+v", got)
	}
}

func TestStore_Update(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	row := fixtureRow()
	if err := s.InsertPendingNudge(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	row.NudgeCount = 3
	row.LastNudgeAt = row.LastNudgeAt.Add(10 * time.Minute)
	row.LastPaneHash = [32]byte{99, 88, 77}
	if err := s.UpdatePendingNudge(ctx, row); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.LookupPendingNudgeByMessageID(ctx, row.MessageID)
	if err != nil || got == nil {
		t.Fatalf("Lookup after update: %v", err)
	}
	if got.NudgeCount != 3 {
		t.Errorf("NudgeCount = %d, want 3", got.NudgeCount)
	}
	if got.LastPaneHash != row.LastPaneHash {
		t.Errorf("LastPaneHash not updated")
	}
}

func TestStore_Delete(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	row := fixtureRow()
	if err := s.InsertPendingNudge(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := s.DeletePendingNudge(ctx, row.MessageID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := s.LookupPendingNudgeByMessageID(ctx, row.MessageID)
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestStore_Delete_Idempotent(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	// Deleting a missing row should not be an error.
	if err := s.DeletePendingNudge(context.Background(), "nonexistent"); err != nil {
		t.Errorf("delete of missing row returned error: %v", err)
	}
}

func TestStore_ReloadOnBoot_ExcludesExpired(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	valid := fixtureRow()
	expired := fixtureRow()
	expired.MessageID = "msg_expired"
	expired.Session = "other-session"
	expired.ExpiresAt = time.Now().UTC().Add(-1 * time.Hour)
	if err := s.InsertPendingNudge(ctx, valid); err != nil {
		t.Fatalf("insert valid: %v", err)
	}
	if err := s.InsertPendingNudge(ctx, expired); err != nil {
		t.Fatalf("insert expired: %v", err)
	}

	rows, err := s.ReloadOnBoot(ctx)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("Reload returned %d rows, want 1 (expired excluded)", len(rows))
	}
	if len(rows) == 1 && rows[0].MessageID != valid.MessageID {
		t.Errorf("Reload returned wrong row: %q", rows[0].MessageID)
	}
}

func TestStore_ReloadOnBoot_Empty(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	rows, err := s.ReloadOnBoot(context.Background())
	if err != nil {
		t.Fatalf("Reload on empty db: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows on empty db, got %d", len(rows))
	}
}

func TestStore_SweepExpired(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	expired := fixtureRow()
	expired.ExpiresAt = time.Now().UTC().Add(-1 * time.Hour)
	if err := s.InsertPendingNudge(ctx, expired); err != nil {
		t.Fatalf("Insert expired: %v", err)
	}

	alive := fixtureRow()
	alive.MessageID = "msg_alive"
	alive.Session = "another-session"
	if err := s.InsertPendingNudge(ctx, alive); err != nil {
		t.Fatalf("Insert alive: %v", err)
	}

	deleted, err := s.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	// The alive row must still be present.
	got, err := s.LookupPendingNudgeByMessageID(ctx, alive.MessageID)
	if err != nil || got == nil {
		t.Errorf("alive row should survive sweep, got err=%v row=%v", err, got)
	}
}

func TestStore_EmptyDenyKey(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	ctx := context.Background()

	row := fixtureRow()
	row.DenyKey = "" // Some runtimes have no explicit deny key.
	if err := s.InsertPendingNudge(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, _ := s.LookupPendingNudgeByMessageID(ctx, row.MessageID)
	if got == nil {
		t.Fatal("expected row, got nil")
	}
	if got.DenyKey != "" {
		t.Errorf("DenyKey = %q, want empty", got.DenyKey)
	}
}
