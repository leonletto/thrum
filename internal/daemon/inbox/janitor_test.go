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

func (f *fakeReadState) State(msgID string) ReadState {
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
