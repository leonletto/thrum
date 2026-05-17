package reminders

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/schema"
)

// newTestStore returns an SQLStore backed by an empty, freshly migrated
// SQLite DB at a per-test tmpDir. The DB closes automatically via
// t.Cleanup.
func newTestStore(t *testing.T) *SQLStore {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "reminders_test.db")
	raw, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(raw); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return NewSQLStore(safedb.New(raw))
}

// mintOpenTime mints a valid agent/time reminder in state=open and
// returns its id. Default trigger_at is one hour from now so DueOpen(now)
// won't pick it up by default — tests that want it due should pass
// `pastTrigger=true`.
func mintOpenTime(t *testing.T, s *SQLStore) string {
	t.Helper()
	r := &Reminder{
		Source:      SourceAgent,
		TriggerKind: TriggerTime,
		SourceAgent: "test_agent",
		TriggerAt:   timePtrFuture(1 * time.Hour),
		TargetAgent: "test_agent",
		Body:        "test body",
	}
	if err := s.Mint(context.Background(), r); err != nil {
		t.Fatalf("Mint: %v", err)
	}
	return r.ID
}

// mintOpenCondition mints a valid daemon/condition_pane_quiet reminder.
// next_reminder_at is set explicitly (in the past so DueOpen finds it)
// since condition rows are normally set by the sweep.
func mintOpenCondition(t *testing.T, s *SQLStore) string {
	t.Helper()
	past := time.Now().Add(-1 * time.Minute).UTC()
	r := &Reminder{
		Source:         SourceDaemon,
		TriggerKind:    TriggerConditionPaneQuiet,
		TriggerMeta:    json.RawMessage(`{"agent":"docs_bot","quiet_since":1700000000}`),
		TargetChain:    []string{"@coordinator_main"},
		PaneSnapshot:   "captured pane content",
		NextReminderAt: &past,
	}
	if err := s.Mint(context.Background(), r); err != nil {
		t.Fatalf("Mint condition: %v", err)
	}
	return r.ID
}

// forceState bypasses the Store API to put a row in a terminal state for
// test setup. Used by the negative-transition matrix where we need to
// observe what happens when a mutation hits an already-terminal row.
func forceState(t *testing.T, s *SQLStore, id string, state State) {
	t.Helper()
	now := time.Now().UTC().Unix()
	// next_reminder_at goes NULL for terminal states; the relevant
	// terminal-timestamp column gets `now` so the row looks realistic.
	var tsCol string
	switch state {
	case StateFired:
		tsCol = "last_fired_at"
	case StateCleared:
		tsCol = "cleared_at"
	case StateCancelled:
		tsCol = "cancelled_at"
	default:
		t.Fatalf("forceState: %q is not a terminal state", state)
	}
	q := `UPDATE reminders SET state = ?, ` + tsCol + ` = ?, next_reminder_at = NULL, updated_at = ? WHERE id = ?`
	if _, err := s.db.ExecContext(context.Background(), q, string(state), now, now, id); err != nil {
		t.Fatalf("forceState %s: %v", state, err)
	}
}

// timePtrFuture returns a pointer to a time `d` from now (UTC).
func timePtrFuture(d time.Duration) *time.Time {
	t := time.Now().Add(d).UTC()
	return &t
}
