package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestPersistCommand(t *testing.T) {
	h, cleanup := setupTmuxHandlerTest(t)
	defer cleanup()

	ctx := context.Background()
	cmd := &QueuedCommand{
		ID:             "cmd_persist",
		Text:           "echo test",
		RequesterAgent: "test_coord",
		Timeout:        120 * time.Second,
		State:          StateQueued,
		SubmittedAt:    time.Now().UTC(),
	}

	if err := persistCommand(ctx, h.state.DB(), "test-session", cmd, 0); err != nil {
		t.Fatalf("persistCommand: %v", err)
	}

	loaded, err := loadCommand(ctx, h.state.DB(), "cmd_persist")
	if err != nil {
		t.Fatalf("loadCommand: %v", err)
	}
	if loaded.ID != cmd.ID {
		t.Errorf("ID=%s, want %s", loaded.ID, cmd.ID)
	}
	if loaded.State != StateQueued {
		t.Errorf("State=%s, want %s", loaded.State, StateQueued)
	}
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
