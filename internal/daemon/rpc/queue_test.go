package rpc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// setupTmuxHandlerTest creates a TmuxHandler backed by an in-memory state instance.
// Returns the handler and a cleanup function. Mirrors setupPurgeTest in purge_test.go.
func setupTmuxHandlerTest(t *testing.T) (*TmuxHandler, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create .thrum dir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "test_repo_queue", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	handler := NewTmuxHandler(thrumDir, st)
	cleanup := func() { _ = st.Close() }
	return handler, cleanup
}

func TestSessionQueueFIFO(t *testing.T) {
	q := NewSessionQueue("test-session")

	q.Enqueue(&QueuedCommand{ID: "cmd_1", Text: "first"})
	q.Enqueue(&QueuedCommand{ID: "cmd_2", Text: "second"})
	q.Enqueue(&QueuedCommand{ID: "cmd_3", Text: "third"})

	if got := q.Len(); got != 3 {
		t.Errorf("Len=%d, want 3", got)
	}

	head := q.Peek()
	if head == nil || head.ID != "cmd_1" {
		t.Errorf("Peek=%v, want cmd_1", head)
	}

	popped := q.Pop()
	if popped == nil || popped.ID != "cmd_1" {
		t.Errorf("Pop=%v, want cmd_1", popped)
	}

	if got := q.Len(); got != 2 {
		t.Errorf("Len after pop=%d, want 2", got)
	}
}
