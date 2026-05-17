package state_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// D-B1.13 — WAL recovery log for mesh config mutations.
// Three-step protocol per design-spec §5: APPEND intent → atomic-write
// config → APPEND committed-marker. On boot, replay any intent without
// matching committed. Compaction at 1MB drops committed entries older
// than the newest uncommitted one (preserves all uncommitted).

func walPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "pending-mesh-updates.jsonl")
}

func TestWal_AppendIntentBeforeCommitted(t *testing.T) {
	w, err := state.NewPendingMeshUpdatesLog(walPath(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = w.Close() }()

	if err := w.AppendIntent("update-1", "peer.announce", map[string]string{"handle": "x"}); err != nil {
		t.Fatalf("AppendIntent: %v", err)
	}
	if err := w.AppendCommitted("update-1"); err != nil {
		t.Fatalf("AppendCommitted: %v", err)
	}

	// File should have exactly two lines: intent then committed, timestamps ascending.
	data, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (intent + committed), got %d", len(lines))
	}

	var first, second map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("parse line 1: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("parse line 2: %v", err)
	}
	if first["stage"] != "intent" {
		t.Errorf("line 1 stage=%v, want intent", first["stage"])
	}
	if second["stage"] != "committed" {
		t.Errorf("line 2 stage=%v, want committed", second["stage"])
	}
	if first["update_id"] != "update-1" || second["update_id"] != "update-1" {
		t.Errorf("update_id mismatch: %v / %v", first["update_id"], second["update_id"])
	}
}

func TestWal_PendingFindsIntentWithoutCommitted(t *testing.T) {
	path := walPath(t)
	w, err := state.NewPendingMeshUpdatesLog(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Two updates: u1 fully committed; u2 intent-only (uncommitted).
	_ = w.AppendIntent("u1", "peer.announce", nil)
	_ = w.AppendCommitted("u1")
	_ = w.AppendIntent("u2", "peer.welcome", nil)
	_ = w.Close()

	// Re-open and query Pending.
	w2, err := state.NewPendingMeshUpdatesLog(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = w2.Close() }()

	pending, err := w2.Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d: %+v", len(pending), pending)
	}
	if pending[0].UpdateID != "u2" {
		t.Errorf("Pending[0]=%s, want u2", pending[0].UpdateID)
	}
	if pending[0].Verb != "peer.welcome" {
		t.Errorf("Pending[0].Verb=%s, want peer.welcome", pending[0].Verb)
	}
}

func TestWal_ReplayOnBootMissingCommitted(t *testing.T) {
	// Simulates: crash AFTER intent + atomic-write but BEFORE committed.
	// On boot, the daemon must see the pending update so it can re-apply
	// (idempotently — the config already has the write; the replay is
	// just to land the committed marker + re-emit the audit log).
	path := walPath(t)
	w, err := state.NewPendingMeshUpdatesLog(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = w.AppendIntent("orphan", "peer.rebind", map[string]string{"handle": "laptop-x"})
	_ = w.Close() // committed marker never written → simulates crash

	w2, err := state.NewPendingMeshUpdatesLog(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = w2.Close() }()

	pending, _ := w2.Pending()
	if len(pending) != 1 || pending[0].UpdateID != "orphan" {
		t.Errorf("expected 'orphan' pending; got %+v", pending)
	}
}

func TestWal_CompactionAt1MBPreservesUncommitted(t *testing.T) {
	// Seed: many committed updates + one uncommitted at the end.
	// Compact() should drop the committed history but keep the uncommitted.
	path := walPath(t)
	w, err := state.NewPendingMeshUpdatesLog(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Seed enough rows to exceed the 1MB compaction threshold (each row
	// is ~2KB with the padded payload; 700 × 2 lines × 2KB = ~2.8MB).
	for i := range 700 {
		id := "committed-" + itoaWal(i)
		_ = w.AppendIntent(id, "peer.announce", strings.Repeat("x", 2000))
		_ = w.AppendCommitted(id)
	}
	_ = w.AppendIntent("survivor", "peer.welcome", nil)
	_ = w.Close()

	w2, err := state.NewPendingMeshUpdatesLog(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = w2.Close() }()

	if err := w2.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	pending, _ := w2.Pending()
	if len(pending) != 1 || pending[0].UpdateID != "survivor" {
		t.Errorf("after Compact, expected 'survivor' as sole pending; got %+v", pending)
	}

	// Compaction must NOT lose the uncommitted entry.
	// File should be much smaller post-compaction.
	info, _ := os.Stat(path)
	if info.Size() > 10*1024 {
		t.Errorf("post-compaction file size %d > 10KB; compaction did not drop committed", info.Size())
	}
}

func TestWal_CompactionThresholdGate(t *testing.T) {
	// Compact() is a no-op when the file is small (< 1MB). Verifies we
	// don't churn rewrites on every Append.
	path := walPath(t)
	w, err := state.NewPendingMeshUpdatesLog(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = w.AppendIntent("tiny", "peer.announce", nil)
	_ = w.AppendCommitted("tiny")

	before, _ := os.Stat(path)
	if err := w.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	after, _ := os.Stat(path)

	if before.Size() != after.Size() {
		t.Errorf("Compact rewrote small file (%d → %d); should be no-op below 1MB threshold",
			before.Size(), after.Size())
	}
	_ = w.Close()
}

// itoaWal — tiny ASCII int-to-string helper to keep the WAL test file
// self-contained without pulling strconv into the import group.
func itoaWal(i int) string {
	if i == 0 {
		return "0"
	}
	var b [4]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = "0123456789"[i%10]
		i /= 10
	}
	return string(b[pos:])
}
