package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/checkpoint"
	"github.com/leonletto/thrum/internal/daemon/eventlog"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
)

func createTestStateForSync(t *testing.T) *state.State {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_SYNCTEST123")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSyncApplier_ApplyRemoteEvents(t *testing.T) {
	st := createTestStateForSync(t)
	applier := NewSyncApplier(st)

	events := []eventlog.Event{
		{
			EventID:      "evt_REMOTE001",
			Sequence:     100,
			Type:         "agent.register",
			Timestamp:    "2026-02-11T10:00:00Z",
			OriginDaemon: "d_remote",
			EventJSON:    json.RawMessage(`{"type":"agent.register","timestamp":"2026-02-11T10:00:00Z","event_id":"evt_REMOTE001","origin_daemon":"d_remote","agent_id":"remote_agent","kind":"agent","role":"tester","module":"test","v":1}`),
		},
		{
			EventID:      "evt_REMOTE002",
			Sequence:     101,
			Type:         "agent.register",
			Timestamp:    "2026-02-11T10:01:00Z",
			OriginDaemon: "d_remote",
			EventJSON:    json.RawMessage(`{"type":"agent.register","timestamp":"2026-02-11T10:01:00Z","event_id":"evt_REMOTE002","origin_daemon":"d_remote","agent_id":"remote_agent2","kind":"agent","role":"tester","module":"test","v":1}`),
		},
	}

	applied, skipped, err := applier.ApplyRemoteEvents(context.Background(), events)
	if err != nil {
		t.Fatalf("ApplyRemoteEvents: %v", err)
	}
	if applied != 2 {
		t.Errorf("applied = %d, want 2", applied)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d, want 0", skipped)
	}

	// Verify events are in the events table
	var count int
	err = st.RawDB().QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("events in DB = %d, want 2", count)
	}
}

func TestSyncApplier_Deduplication(t *testing.T) {
	st := createTestStateForSync(t)
	applier := NewSyncApplier(st)

	// Write a local event first
	localEvent := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-02-11T09:00:00Z",
		EventID:   "evt_LOCAL001",
		AgentID:   "local_agent",
		Kind:      "agent",
		Role:      "tester",
		Module:    "test",
	}
	if err := st.WriteEvent(context.Background(), localEvent); err != nil {
		t.Fatalf("write local event: %v", err)
	}

	// Try to apply the same event from remote (same event_id)
	events := []eventlog.Event{
		{
			EventID:      "evt_LOCAL001",
			Sequence:     50,
			Type:         "agent.register",
			Timestamp:    "2026-02-11T09:00:00Z",
			OriginDaemon: "d_remote",
			EventJSON:    json.RawMessage(`{"type":"agent.register","timestamp":"2026-02-11T09:00:00Z","event_id":"evt_LOCAL001","agent_id":"local_agent","kind":"agent","role":"tester","module":"test","v":1}`),
		},
		{
			EventID:      "evt_REMOTE003",
			Sequence:     51,
			Type:         "agent.register",
			Timestamp:    "2026-02-11T10:00:00Z",
			OriginDaemon: "d_remote",
			EventJSON:    json.RawMessage(`{"type":"agent.register","timestamp":"2026-02-11T10:00:00Z","event_id":"evt_REMOTE003","origin_daemon":"d_remote","agent_id":"new_agent","kind":"agent","role":"tester","module":"test","v":1}`),
		},
	}

	applied, skipped, err := applier.ApplyRemoteEvents(context.Background(), events)
	if err != nil {
		t.Fatalf("ApplyRemoteEvents: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1", applied)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}

	// Total events should be 2 (1 local + 1 new remote)
	var count int
	err = st.RawDB().QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("events in DB = %d, want 2", count)
	}
}

func TestSyncApplier_ApplyAndCheckpoint(t *testing.T) {
	st := createTestStateForSync(t)
	applier := NewSyncApplier(st)

	events := []eventlog.Event{
		{
			EventID:      "evt_CP001",
			Sequence:     200,
			Type:         "agent.register",
			Timestamp:    "2026-02-11T10:00:00Z",
			OriginDaemon: "d_peer",
			EventJSON:    json.RawMessage(`{"type":"agent.register","timestamp":"2026-02-11T10:00:00Z","event_id":"evt_CP001","origin_daemon":"d_peer","agent_id":"cp_agent","kind":"agent","role":"tester","module":"test","v":1}`),
		},
	}

	applied, _, err := applier.ApplyAndCheckpoint(context.Background(), "d_peer", events, 200)
	if err != nil {
		t.Fatalf("ApplyAndCheckpoint: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1", applied)
	}

	// Verify checkpoint
	cp, err := checkpoint.GetCheckpoint(st.RawDB(), "d_peer")
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if cp == nil {
		t.Fatal("checkpoint should exist")
	}
	if cp.LastSyncedSeq != 200 {
		t.Errorf("LastSyncedSeq = %d, want 200", cp.LastSyncedSeq)
	}
}

func TestSyncApplier_GetCheckpoint(t *testing.T) {
	st := createTestStateForSync(t)
	applier := NewSyncApplier(st)

	// No checkpoint yet
	seq, err := applier.GetCheckpoint("d_unknown")
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if seq != 0 {
		t.Errorf("expected 0 for unknown peer, got %d", seq)
	}
}
