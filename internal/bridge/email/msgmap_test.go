package email_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/bridge/email"
)

// D-B1.7 — msgmap (.thrum/state/email-msgmap.jsonl sidecar).
// In-process map + append-on-insert + compact-on-startup. NO new SQLite
// table per canonical-ref §3.12 (msgmap is the explicit sidecar exception).

func mapPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "email-msgmap.jsonl")
}

func TestMsgmap_InsertLookup(t *testing.T) {
	m, err := email.NewMsgMap(mapPath(t))
	if err != nil {
		t.Fatalf("NewMsgMap: %v", err)
	}
	defer func() { _ = m.Close() }()

	if err := m.Insert("<msg-1@host>", "msg_01KRHX1"); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, ok := m.Lookup("<msg-1@host>")
	if !ok {
		t.Fatal("Lookup returned ok=false for inserted key")
	}
	if got != "msg_01KRHX1" {
		t.Errorf("Lookup returned %q, want msg_01KRHX1", got)
	}
}

func TestMsgmap_InsertIdempotent(t *testing.T) {
	m, err := email.NewMsgMap(mapPath(t))
	if err != nil {
		t.Fatalf("NewMsgMap: %v", err)
	}
	defer func() { _ = m.Close() }()

	if err := m.Insert("<msg-1@host>", "msg_01KRHX1"); err != nil {
		t.Fatalf("Insert (1st): %v", err)
	}
	if err := m.Insert("<msg-1@host>", "msg_01KRHX1"); err != nil {
		t.Fatalf("Insert (2nd identical): %v", err)
	}
	if l := m.Len(); l != 1 {
		t.Errorf("Len after duplicate Insert = %d, want 1", l)
	}
}

func TestMsgmap_LookupMissReturnsFalse(t *testing.T) {
	m, err := email.NewMsgMap(mapPath(t))
	if err != nil {
		t.Fatalf("NewMsgMap: %v", err)
	}
	defer func() { _ = m.Close() }()

	if _, ok := m.Lookup("<never-inserted@host>"); ok {
		t.Errorf("Lookup of missing key returned ok=true")
	}
}

func TestMsgmap_SweepDropsOlderThan90d(t *testing.T) {
	path := mapPath(t)
	m, err := email.NewMsgMap(path)
	if err != nil {
		t.Fatalf("NewMsgMap: %v", err)
	}

	// Insert a fresh entry + an artificially-aged entry via the helper
	// InsertAt (exported as test-only hook in the impl) so the test
	// can pin the timestamp without sleeping 90+ days.
	if err := m.Insert("<fresh@host>", "msg_fresh"); err != nil {
		t.Fatalf("Insert fresh: %v", err)
	}
	old := time.Now().Add(-100 * 24 * time.Hour)
	if err := m.InsertAt("<old@host>", "msg_old", old); err != nil {
		t.Fatalf("InsertAt old: %v", err)
	}

	cutoff := time.Now().Add(-90 * 24 * time.Hour)
	if err := m.Sweep(cutoff); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if _, ok := m.Lookup("<old@host>"); ok {
		t.Error("Sweep failed to drop old entry")
	}
	if _, ok := m.Lookup("<fresh@host>"); !ok {
		t.Error("Sweep dropped fresh entry")
	}
	_ = m.Close()
}

func TestMsgmap_FlushPersistsToDisk(t *testing.T) {
	path := mapPath(t)

	m1, err := email.NewMsgMap(path)
	if err != nil {
		t.Fatalf("NewMsgMap (1st): %v", err)
	}
	for k, v := range map[string]string{
		"<a@host>": "msg_a",
		"<b@host>": "msg_b",
		"<c@host>": "msg_c",
	} {
		if err := m1.Insert(k, v); err != nil {
			t.Fatalf("Insert %s: %v", k, err)
		}
	}
	if err := m1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Sidecar file must exist on disk.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sidecar not on disk: %v", err)
	}

	// New instance reads the sidecar and restores all entries.
	m2, err := email.NewMsgMap(path)
	if err != nil {
		t.Fatalf("NewMsgMap (2nd, post-restart): %v", err)
	}
	defer func() { _ = m2.Close() }()

	for k, want := range map[string]string{
		"<a@host>": "msg_a",
		"<b@host>": "msg_b",
		"<c@host>": "msg_c",
	} {
		got, ok := m2.Lookup(k)
		if !ok {
			t.Errorf("post-restart Lookup(%s) returned ok=false", k)
			continue
		}
		if got != want {
			t.Errorf("post-restart Lookup(%s) = %q, want %q", k, got, want)
		}
	}
}

func TestMsgmap_CompactOnStartupDedupsAppendOnlyHistory(t *testing.T) {
	// The sidecar accumulates an append-only history on Insert. A
	// constructor that re-loads MUST collapse duplicate keys (keeping
	// the latest value), so historical churn doesn't shadow the
	// current state. Without compaction, an entry that was overwritten
	// would silently revert after restart.
	path := mapPath(t)

	m1, err := email.NewMsgMap(path)
	if err != nil {
		t.Fatalf("NewMsgMap: %v", err)
	}
	_ = m1.Insert("<dup@host>", "msg_first")
	_ = m1.Insert("<dup@host>", "msg_second") // overwrite
	_ = m1.Close()

	m2, err := email.NewMsgMap(path)
	if err != nil {
		t.Fatalf("NewMsgMap (re-open): %v", err)
	}
	defer func() { _ = m2.Close() }()

	got, _ := m2.Lookup("<dup@host>")
	if got != "msg_second" {
		t.Errorf("after compact-on-startup, Lookup = %q, want msg_second", got)
	}
	if l := m2.Len(); l != 1 {
		t.Errorf("Len after compact = %d, want 1", l)
	}
}
