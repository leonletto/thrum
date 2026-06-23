package state

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// TestIngestSyncedEvent_SkipsAlreadyPulledEvent is the thrum-lv9x Path-A
// guard. The a-sync git ingest (IngestSyncedEvent) had NO dedup at all: an
// event already ingested via the RPC-pull path (Path B, which records it in
// the events table) was re-applied to the projector on every git merge — for
// message.create that aborted with the messages.message_id UNIQUE error and
// permanently stalled the git-sync loop, and each re-apply re-fired the
// onEventWrite notify hook (storm fuel). IngestSyncedEvent must now pre-check
// the events table and skip silently: no projector apply, no hook fire.
func TestIngestSyncedEvent_SkipsAlreadyPulledEvent(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	st, err := NewState(thrumDir, thrumDir, "r_LV9XINGEST", "d_local_test")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	var hookFires atomic.Int64
	st.SetOnEventWrite(func(daemonID string, seq int64, event []byte) {
		hookFires.Add(1)
	})

	// Ingest via the Path-B shape: WriteEvent with an explicit event_id puts
	// the event in the events table (what SyncApplier.applyEvent does).
	eventMap := map[string]any{
		"type":          "message.create",
		"event_id":      "evt_LV9X_PATHA",
		"timestamp":     "2026-06-10T08:00:00Z",
		"origin_daemon": "d_remote",
		"message_id":    "msg_LV9X_PATHA",
		"agent_id":      "remote_author",
		"session_id":    "ses_r",
		"body":          map[string]any{"format": "markdown", "content": "via path B"},
		"v":             1,
	}
	if _, err := st.WriteEvent(ctx, eventMap); err != nil {
		t.Fatalf("WriteEvent (path B ingest): %v", err)
	}
	firesAfterPathB := hookFires.Load()

	// Now the SAME event arrives via the a-sync git merge (Path A).
	raw, _ := json.Marshal(eventMap)
	if err := st.IngestSyncedEvent(ctx, raw); err != nil {
		t.Fatalf("IngestSyncedEvent must skip an already-pulled event, not error: %v", err)
	}

	// The skip must be silent: no hook re-fire for the duplicate.
	if got := hookFires.Load(); got != firesAfterPathB {
		t.Errorf("onEventWrite fired %d times after the dup ingest (was %d) — dup re-ingest must not re-notify (storm fuel)", got, firesAfterPathB)
	}

	// Exactly one message row.
	var n int
	if err := st.RawDB().QueryRow(`SELECT COUNT(*) FROM messages WHERE message_id = 'msg_LV9X_PATHA'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("messages rows = %d, want 1", n)
	}
}

// TestIngestSyncedEvent_PathAOnlyReplay_NoError covers the Path-A-only replay:
// an event that NEVER went through the events table (pure git-merge ingest)
// gets re-merged on a later sync round. The HasEvent pre-check cannot catch it
// (Path A does not record into the events table); the projector's idempotent
// message INSERT (lv9x B1) must absorb it without error.
func TestIngestSyncedEvent_PathAOnlyReplay_NoError(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	st, err := NewState(thrumDir, thrumDir, "r_LV9XREPLAY", "d_local_test")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	raw, _ := json.Marshal(map[string]any{
		"type":          "message.create",
		"event_id":      "evt_LV9X_REPLAY",
		"timestamp":     "2026-06-10T08:00:00Z",
		"origin_daemon": "d_remote",
		"message_id":    "msg_LV9X_REPLAY",
		"agent_id":      "remote_author",
		"session_id":    "ses_r",
		"body":          map[string]any{"format": "markdown", "content": "merged"},
		"v":             1,
	})
	if err := st.IngestSyncedEvent(ctx, raw); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	// Re-merge replay (pre-fix: UNIQUE abort → permanent git-sync stall).
	if err := st.IngestSyncedEvent(ctx, raw); err != nil {
		t.Fatalf("Path-A replay must be tolerated by the idempotent projector, not stall: %v", err)
	}
}
