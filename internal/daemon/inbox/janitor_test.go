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

	env := Envelope{MsgID: "backstop-20260516T1534", From: "thrum-backstop", ReceivedAt: time.Now()}
	if err := WriteSpool(dir, agentID, env); err != nil {
		t.Fatalf("seed backstop envelope: %v", err)
	}

	// Reader would report StateMissing for a backstop msg_id since
	// there's no underlying row in `messages`. If the janitor consults
	// readState for this entry, the test fails — the skip must happen
	// before readState is called.
	fake := &fakeReadState{
		missing: map[string]bool{"backstop-20260516T1534": true},
	}
	j := NewSpoolJanitor(dir, func() []string { return []string{agentID} }, fake.State)
	j.Reconcile()

	spoolDir := filepath.Join(dir, "spool", agentID)
	if _, err := os.Stat(filepath.Join(spoolDir, "backstop-20260516T1534.json")); err != nil {
		t.Fatalf("backstop envelope must survive reconcile: %v", err)
	}
}
