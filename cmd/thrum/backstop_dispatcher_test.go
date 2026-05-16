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

// TestBackstopDispatcher_WritesSpoolEnvelope verifies the production
// dispatcher writes a synthetic backstop envelope to the agent's spool
// dir, so a dead-pane agent picks the reminder up via its check-inbox
// hook on next SessionStart. tmux dispatch is fire-and-forget and a
// no-op without a registered identity file, so this test exercises the
// always-fire spool path.
func TestBackstopDispatcher_WritesSpoolEnvelope(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir thrumDir: %v", err)
	}

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
