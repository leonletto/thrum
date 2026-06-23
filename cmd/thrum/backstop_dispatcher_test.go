package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/inbox"
)

// seedIdentityFile makes agentID resident on this daemon for the thrum-wo2z
// residency guard: nudge.HasLocalIdentity is satisfied by the existence of
// <thrumDir>/identities/<agent>.json.
func seedIdentityFile(t *testing.T, thrumDir, agentID string) {
	t.Helper()
	dir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentID+".json"), []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}
}

// TestBackstopDispatcher_WritesSpoolEnvelope verifies the production
// dispatcher writes a synthetic backstop envelope to the agent's spool
// dir, so a dead-pane agent picks the reminder up via its check-inbox
// hook on next SessionStart. The agent is RESIDENT (identity file seeded
// — required since the thrum-wo2z residency guard) but has no live tmux
// pane, so this exercises the dead-pane spool path.
func TestBackstopDispatcher_WritesSpoolEnvelope(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir thrumDir: %v", err)
	}
	seedIdentityFile(t, thrumDir, "alice")

	d := newBackstopDispatcher(thrumDir)
	if err := d.Dispatch(context.Background(), "alice", 3); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	spoolDir := filepath.Join(thrumDir, "spool", "alice")
	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		t.Fatalf("read spool dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 spool entry, got %d", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, "backstop-") || !strings.HasSuffix(name, ".json") {
		t.Fatalf("unexpected spool entry name: %q", name)
	}

	data, err := os.ReadFile(filepath.Join(spoolDir, name)) // #nosec G304 -- test reads its own temp dir
	if err != nil {
		t.Fatalf("read spool file: %v", err)
	}
	var env inbox.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !strings.HasPrefix(env.MsgID, "backstop-") {
		t.Fatalf("expected backstop-prefixed msg_id, got %q", env.MsgID)
	}
	if env.From != "thrum-backstop" {
		t.Fatalf("expected From=thrum-backstop, got %q", env.From)
	}
}

// TestBackstopDispatcher_DedupesWithinWindow verifies multiple Dispatch
// calls for the same agent within the same minute collapse to a single
// spool entry (deterministic msg_id keyed on minute granularity).
func TestBackstopDispatcher_DedupesWithinWindow(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir thrumDir: %v", err)
	}
	seedIdentityFile(t, thrumDir, "bob")

	d := newBackstopDispatcher(thrumDir)
	for i := 0; i < 3; i++ {
		if err := d.Dispatch(context.Background(), "bob", 5); err != nil {
			t.Fatalf("Dispatch iteration %d: %v", i, err)
		}
	}

	spoolDir := filepath.Join(thrumDir, "spool", "bob")
	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		t.Fatalf("read spool dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected dedupe to a single entry within the minute, got %d: %+v", len(entries), entries)
	}
}

// TestBackstopDispatcher_NonResident_NoNudgeNoSpool is the thrum-wo2z
// coverage pin (coordinator requirement): a NON-resident recipient (no
// identity file on this daemon — e.g. a synced-in remote agent) gets
// NEITHER the tmux nudge NOR a spool envelope. Without the spool half,
// backstop- envelopes for remote agents accumulate forever — the janitor
// preserves the backstop- prefix and a remote agent never reads a local
// spool (the quiet cousin of the 15-minute wake-up bug).
func TestBackstopDispatcher_NonResident_NoNudgeNoSpool(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir thrumDir: %v", err)
	}
	// No identity file for coord_remote — the production shape: its
	// registration row synced into the local DB, but it lives elsewhere.

	d := newBackstopDispatcher(thrumDir)
	if err := d.Dispatch(context.Background(), "coord_remote", 2); err != nil {
		t.Fatalf("Dispatch must skip silently, not error: %v", err)
	}

	// No spool envelope accumulation: the agent's spool dir must not exist.
	spoolDir := filepath.Join(thrumDir, "spool", "coord_remote")
	if _, err := os.Stat(spoolDir); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(spoolDir)
		t.Fatalf("non-resident recipient must get NO spool envelope; dir exists with %d entries", len(entries))
	}
}

// TestBackstopDispatcher_IdentityDeletedMidLife is the destroyed-agent edge
// (coordinator requirement; the impl_async_exposure cleanup shape): an agent
// that WAS resident — identity file existed, DB rows remain — then was
// destroyed. The residency check stats the filesystem live, so the next
// Dispatch after deletion must skip: no new spool envelope.
func TestBackstopDispatcher_IdentityDeletedMidLife(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir thrumDir: %v", err)
	}
	seedIdentityFile(t, thrumDir, "doomed")

	d := newBackstopDispatcher(thrumDir)
	if err := d.Dispatch(context.Background(), "doomed", 1); err != nil {
		t.Fatalf("Dispatch while resident: %v", err)
	}
	spoolDir := filepath.Join(thrumDir, "spool", "doomed")
	before, err := os.ReadDir(spoolDir)
	if err != nil || len(before) != 1 {
		t.Fatalf("expected 1 spool entry while resident, got %d (err %v)", len(before), err)
	}

	// The agent is destroyed: identity file removed, spool drained.
	if err := os.Remove(filepath.Join(thrumDir, "identities", "doomed.json")); err != nil {
		t.Fatalf("remove identity: %v", err)
	}
	if err := os.Remove(filepath.Join(spoolDir, before[0].Name())); err != nil {
		t.Fatalf("drain spool: %v", err)
	}

	if err := d.Dispatch(context.Background(), "doomed", 1); err != nil {
		t.Fatalf("Dispatch after destruction must skip silently: %v", err)
	}
	after, _ := os.ReadDir(spoolDir)
	if len(after) != 0 {
		t.Fatalf("destroyed agent must get no new spool envelope, got %d", len(after))
	}
}
