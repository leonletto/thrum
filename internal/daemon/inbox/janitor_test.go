// internal/daemon/inbox/janitor_test.go
package inbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeReadState is a test stub implementing MessageReadStateFunc.
type fakeReadState struct {
	read    map[string]bool
	missing map[string]bool
}

func (f *fakeReadState) State(msgID, agentID string) ReadState {
	if f.missing[msgID] {
		return StateMissing
	}
	if f.read[msgID] {
		return StateRead
	}
	return StateUnread
}

func TestReconcile_DeletesReadAndOrphan_KeepsUnread(t *testing.T) {
	dir := t.TempDir()
	agentID := "bob"
	writeEnv := func(id string) {
		env := Envelope{MsgID: id, From: "@x", ReceivedAt: time.Now()}
		if err := WriteSpool(dir, agentID, env); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	writeEnv("msg_read")
	writeEnv("msg_unread")
	writeEnv("msg_orphan")

	fake := &fakeReadState{
		read:    map[string]bool{"msg_read": true},
		missing: map[string]bool{"msg_orphan": true},
	}
	j := NewSpoolJanitor(dir, func() []string { return []string{agentID} }, fake.State)
	j.Reconcile()

	spoolDir := filepath.Join(dir, "spool", agentID)
	for _, name := range []string{"msg_read.json", "msg_orphan.json"} {
		if _, err := os.Stat(filepath.Join(spoolDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should have been deleted, stat err: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(spoolDir, "msg_unread.json")); err != nil {
		t.Errorf("msg_unread should still exist: %v", err)
	}
}

func TestReconcile_MissingSpoolDirIsNoOp(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeReadState{}
	j := NewSpoolJanitor(dir, func() []string { return []string{"nobody"} }, fake.State)
	j.Reconcile() // should not panic or error
}

// TestReconcile_PreservesBackstopEnvelopes pins the thrum-7b84.3 E3
// invariant: the daemon-side backstop dispatcher writes synthetic
// "backstop-<min>.json" envelopes that intentionally have no
// corresponding messages row. Without an explicit skip the janitor
// would resolve them to StateMissing and reap them, breaking the
// dead-pane backstop path. Lock the skip in.
func TestReconcile_PreservesBackstopEnvelopes(t *testing.T) {
	dir := t.TempDir()
	agentID := "carol"

	// A RECENT backstop envelope (within retention) must survive — and the
	// janitor must NOT consult readState for it (the thrum-7b84.3 E3 invariant:
	// readState reports StateMissing for backstop ids and would reap it). Stamp
	// = now, so it is the live reminder.
	stamp := time.Now().UTC().Format(backstopTimeLayout)
	msgID := "backstop-" + stamp
	if err := WriteSpool(dir, agentID, Envelope{MsgID: msgID, From: "thrum-backstop", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("seed backstop envelope: %v", err)
	}

	// If the janitor consults readState for this entry, the test fails — the
	// skip must happen before readState is called.
	fake := &fakeReadState{missing: map[string]bool{msgID: true}}
	j := NewSpoolJanitor(dir, func() []string { return []string{agentID} }, fake.State)
	j.Reconcile()

	spoolDir := filepath.Join(dir, "spool", agentID)
	if _, err := os.Stat(filepath.Join(spoolDir, msgID+".json")); err != nil {
		t.Fatalf("recent backstop envelope must survive reconcile: %v", err)
	}
}

// TestReconcile_PrunesStaleBackstopEnvelopes is the thrum-ist8 fix: backstop
// envelopes older than the retention window are pruned (they accumulated
// unbounded — the dispatcher writes a fresh one each tick and the old
// unconditional skip never reaped superseded ones), while the recent/live
// reminder is kept.
func TestReconcile_PrunesStaleBackstopEnvelopes(t *testing.T) {
	dir := t.TempDir()
	agentID := "dave"
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	stale := "backstop-" + now.Add(-90*time.Minute).Format(backstopTimeLayout) // > 1h old
	recent := "backstop-" + now.Add(-5*time.Minute).Format(backstopTimeLayout) // within 1h
	for _, id := range []string{stale, recent} {
		if err := WriteSpool(dir, agentID, Envelope{MsgID: id, From: "thrum-backstop", ReceivedAt: now}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	j := NewSpoolJanitor(dir, func() []string { return []string{agentID} }, (&fakeReadState{}).State)
	j.SetNow(func() time.Time { return now })
	j.Reconcile()

	spoolDir := filepath.Join(dir, "spool", agentID)
	if _, err := os.Stat(filepath.Join(spoolDir, stale+".json")); !os.IsNotExist(err) {
		t.Errorf("stale backstop envelope should be pruned, stat err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(spoolDir, recent+".json")); err != nil {
		t.Errorf("recent backstop envelope must survive: %v", err)
	}
}

// TestReconcile_PrunesMalformedStaleBackstop_ViaModTime pins the leak-proof
// fallback: a backstop envelope whose embedded stamp can't be parsed (legacy/
// malformed name) is aged by ModTime instead, so it can't leak forever.
func TestReconcile_PrunesMalformedStaleBackstop_ViaModTime(t *testing.T) {
	dir := t.TempDir()
	agentID := "erin"
	now := time.Now()

	bad := "backstop-not-a-timestamp"
	if err := WriteSpool(dir, agentID, Envelope{MsgID: bad, From: "thrum-backstop", ReceivedAt: now}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	spoolDir := filepath.Join(dir, "spool", agentID)
	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(spoolDir, bad+".json"), old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	j := NewSpoolJanitor(dir, func() []string { return []string{agentID} }, (&fakeReadState{}).State)
	j.Reconcile()

	if _, err := os.Stat(filepath.Join(spoolDir, bad+".json")); !os.IsNotExist(err) {
		t.Errorf("malformed stale backstop (old ModTime) should be pruned, stat err: %v", err)
	}
}
