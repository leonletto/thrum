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

func TestIngestSyncedEvent_FiresHook(t *testing.T) {
	st := newHookTestState(t)

	var (
		mu       sync.Mutex
		captured []byte
		gotSeq   int64 = -1
	)
	st.SetOnEventWrite(func(_ string, sequence int64, event []byte) {
		mu.Lock()
		defer mu.Unlock()
		captured = append([]byte{}, event...)
		gotSeq = sequence
	})

	// Simulate an event that arrived via sync merge from a peer. The
	// event already has its own sequence number (42) from the peer's
	// daemon; our IngestSyncedEvent must NOT advance the local
	// sequence counter for it.
	syncedEvent := []byte(`{
		"type": "message.create",
		"timestamp": "2026-04-14T00:00:00Z",
		"event_id": "evt_synced_01",
		"v": 1,
		"sequence": 42,
		"message_id": "msg_from_peer",
		"agent_id": "supervisor_falcon",
		"session_id": "supervisor",
		"body": {"format": "markdown", "content": "test"},
		"origin_daemon": "peer-daemon"
	}`)

	if err := st.IngestSyncedEvent(context.Background(), syncedEvent); err != nil {
		t.Fatalf("IngestSyncedEvent: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(captured) == 0 {
		t.Fatal("hook did not receive synced event payload")
	}
	if !strings.Contains(string(captured), "msg_from_peer") {
		t.Errorf("captured event doesn't contain expected message_id: %s", captured)
	}
	// Sequence of 0 is the sentinel for "not locally written" — the
	// daemon-local counter is not incremented for synced events.
	if gotSeq != 0 {
		t.Errorf("gotSeq = %d, want 0 (sentinel for synced)", gotSeq)
	}
}

func TestIngestSyncedEvent_AppliesProjection(t *testing.T) {
	// Synced events must still land in the SQLite projection so
	// downstream queries (thrum team, ListActiveAgentsByRole) see
	// them — the hook is a layered concern, not a replacement for
	// the projector.
	st := newHookTestState(t)

	syncedEvent := []byte(`{
		"type": "agent.register",
		"timestamp": "2026-04-14T00:00:00Z",
		"event_id": "evt_agent_01",
		"v": 1,
		"agent_id": "synced_agent",
		"kind": "agent",
		"role": "researcher",
		"module": "test"
	}`)

	if err := st.IngestSyncedEvent(context.Background(), syncedEvent); err != nil {
		t.Fatalf("IngestSyncedEvent: %v", err)
	}

	var count int
	err := st.RawDB().QueryRow(
		`SELECT COUNT(*) FROM agents WHERE agent_id = ?`, "synced_agent",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query agents: %v", err)
	}
	if count != 1 {
		t.Errorf("synced agent not in projection (count=%d)", count)
	}
}

func TestIngestSyncedEvent_NilHookIsANoOp(t *testing.T) {
	// With no hook registered, IngestSyncedEvent should still apply
	// to the projection without panicking.
	st := newHookTestState(t)
	syncedEvent := []byte(`{
		"type": "agent.register",
		"timestamp": "2026-04-14T00:00:00Z",
		"event_id": "evt_agent_02",
		"v": 1,
		"agent_id": "synced_nohook",
		"kind": "agent",
		"role": "researcher",
		"module": "test"
	}`)
	if err := st.IngestSyncedEvent(context.Background(), syncedEvent); err != nil {
		t.Fatalf("IngestSyncedEvent with nil hook: %v", err)
	}
}
