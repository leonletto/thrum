package state

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/leonletto/thrum/internal/types"
)

// newHookTestState is a local helper for the widened-hook tests. It
// mirrors newTestState but lives here so the new test file is
// self-contained.
func newHookTestState(t *testing.T) *State {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}
	st, err := NewState(thrumDir, thrumDir, "r_HOOKTEST", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestEventWriteHook_ReceivesPayload(t *testing.T) {
	st := newHookTestState(t)

	var (
		mu       sync.Mutex
		captured []byte
	)
	st.SetOnEventWrite(func(daemonID string, sequence int64, event []byte) {
		mu.Lock()
		defer mu.Unlock()
		captured = append([]byte{}, event...)
	})

	evt := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-04-14T00:00:00Z",
		AgentID:   "test_agent",
		Kind:      "agent",
		Role:      "researcher",
		Module:    "test",
	}
	if err := st.WriteEvent(context.Background(), evt); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(captured) == 0 {
		t.Fatal("hook did not receive event payload")
	}
	if !strings.Contains(string(captured), "test_agent") {
		t.Errorf("captured event missing agent_id: %s", captured)
	}
	// And must parse as valid JSON — the hook should get the enriched
	// event map, not a raw Go struct reflection.
	var decoded map[string]any
	if err := json.Unmarshal(captured, &decoded); err != nil {
		t.Errorf("captured payload is not valid JSON: %v", err)
	}
}

func TestMessageCreateHook_FiresOnRPC(t *testing.T) {
	st := newHookTestState(t)

	var (
		mu      sync.Mutex
		gotType string
	)
	st.SetOnEventWrite(func(daemonID string, sequence int64, event []byte) {
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(event, &head)
		mu.Lock()
		defer mu.Unlock()
		gotType = head.Type
	})

	evt := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2026-04-14T00:00:00Z",
		MessageID: "msg_hook_test",
		AgentID:   "agent:researcher:TESTER",
		SessionID: "ses_test",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "hello",
		},
	}
	if err := st.WriteEvent(context.Background(), evt); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotType != "message.create" {
		t.Errorf("hook saw type %q, want message.create", gotType)
	}
}

func TestEventWriteHook_NilHookIsANoOp(t *testing.T) {
	// Confirm that not setting a hook is still safe — WriteEvent must
	// not panic when onEventWrite is nil.
	st := newHookTestState(t)
	evt := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-04-14T00:00:00Z",
		AgentID:   "x",
		Kind:      "agent",
		Role:      "r",
		Module:    "m",
	}
	if err := st.WriteEvent(context.Background(), evt); err != nil {
		t.Fatalf("WriteEvent with nil hook: %v", err)
	}
}
