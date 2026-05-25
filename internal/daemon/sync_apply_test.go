package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/checkpoint"
	"github.com/leonletto/thrum/internal/daemon/eventlog"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
)

func createTestPeerRegistry(t *testing.T) *PeerRegistry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peers.json")
	reg, err := NewPeerRegistry(path)
	if err != nil {
		t.Fatalf("create peer registry: %v", err)
	}
	return reg
}

func createTestStateForSync(t *testing.T) *state.State {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_SYNCTEST123", "")
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
	if _, err := st.WriteEvent(context.Background(), localEvent); err != nil {
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
	cp, err := checkpoint.GetCheckpoint(context.Background(), st.DB(), "d_peer")
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

func TestApplyRemoteEvents_SkipsBeforePurgeCutoff(t *testing.T) {
	st := createTestStateForSync(t)
	defer func() { _ = st.Close() }()

	cutoff := "2026-03-20T00:00:00Z"
	_, err := st.RawDB().Exec(`INSERT INTO purge_metadata (key, value) VALUES ('purge_cutoff', ?)`, cutoff)
	if err != nil {
		t.Fatalf("insert purge_metadata: %v", err)
	}

	applier := NewSyncApplier(st)

	// Event before cutoff — should be skipped
	oldEvent := eventlog.Event{
		EventID:      "evt_old_001",
		Type:         "agent.register",
		Timestamp:    "2026-03-19T12:00:00Z",
		OriginDaemon: "peer_1",
		Sequence:     1,
		EventJSON:    []byte(`{"type":"agent.register","timestamp":"2026-03-19T12:00:00Z","event_id":"evt_old_001","agent_id":"old_agent","role":"test","module":"test","v":1,"origin_daemon":"peer_1"}`),
	}

	// Event after cutoff — should be applied
	newEvent := eventlog.Event{
		EventID:      "evt_new_001",
		Type:         "agent.register",
		Timestamp:    "2026-03-21T12:00:00Z",
		OriginDaemon: "peer_1",
		Sequence:     2,
		EventJSON:    []byte(`{"type":"agent.register","timestamp":"2026-03-21T12:00:00Z","event_id":"evt_new_001","agent_id":"new_agent","role":"test","module":"test","v":1,"origin_daemon":"peer_1"}`),
	}

	applied, skipped, err := applier.ApplyRemoteEvents(context.Background(), []eventlog.Event{oldEvent, newEvent})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied != 1 {
		t.Errorf("expected 1 applied, got %d", applied)
	}
	if skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", skipped)
	}

	// Verify old agent was NOT created
	var count int
	_ = st.RawDB().QueryRow(`SELECT COUNT(*) FROM agents WHERE agent_id = 'old_agent'`).Scan(&count)
	if count != 0 {
		t.Error("old_agent should not exist (event was before purge cutoff)")
	}

	// Verify new agent WAS created
	_ = st.RawDB().QueryRow(`SELECT COUNT(*) FROM agents WHERE agent_id = 'new_agent'`).Scan(&count)
	if count != 1 {
		t.Error("new_agent should exist (event was after purge cutoff)")
	}
}

func TestSyncApply_PurgeExecutedPropagation(t *testing.T) {
	st := createTestStateForSync(t)
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	applier := NewSyncApplier(st)

	// Simulate: peer sends a batch that includes old events + a purge.executed event.
	// The old event arrives before purge.executed in the batch, so it gets applied first
	// (no cutoff set yet). But the purge.executed projector then cleans up the stale data.
	oldRegister := eventlog.Event{
		EventID:      "evt_stale_reg",
		Type:         "agent.register",
		Timestamp:    "2026-03-18T00:00:00Z",
		OriginDaemon: "peer_1",
		Sequence:     1,
		EventJSON:    []byte(`{"type":"agent.register","timestamp":"2026-03-18T00:00:00Z","event_id":"evt_stale_reg","agent_id":"stale_agent","kind":"agent","role":"test","module":"test","v":1,"origin_daemon":"peer_1"}`),
	}

	purgeEvt := eventlog.Event{
		EventID:      "evt_purge_001",
		Type:         "purge.executed",
		Timestamp:    "2026-03-25T00:00:00Z",
		OriginDaemon: "peer_1",
		Sequence:     2,
		EventJSON:    []byte(`{"type":"purge.executed","timestamp":"2026-03-25T00:00:00Z","event_id":"evt_purge_001","cutoff":"2026-03-20T00:00:00Z","v":1,"origin_daemon":"peer_1"}`),
	}

	// Both events should be applied: old one passes (no cutoff yet), purge.executed then cleans up
	applied, _, err := applier.ApplyRemoteEvents(ctx, []eventlog.Event{oldRegister, purgeEvt})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied != 2 {
		t.Errorf("expected 2 applied in first batch, got %d", applied)
	}

	// After purge.executed projection, the stale agent.register event should be gone from
	// the events table (purge.executed deletes events with timestamp < cutoff).
	var evtCount int
	_ = st.RawDB().QueryRow(`SELECT COUNT(*) FROM events WHERE event_id = 'evt_stale_reg'`).Scan(&evtCount)
	if evtCount != 0 {
		t.Error("evt_stale_reg should have been removed from events table by purge.executed projection")
	}

	// Verify purge cutoff is now stored
	var cutoff string
	err = st.RawDB().QueryRow(`SELECT value FROM purge_metadata WHERE key = 'purge_cutoff'`).Scan(&cutoff)
	if err != nil {
		t.Fatalf("query purge_metadata: %v", err)
	}
	if cutoff != "2026-03-20T00:00:00Z" {
		t.Errorf("expected cutoff 2026-03-20T00:00:00Z, got %s", cutoff)
	}

	// Now try to apply another old event — should be skipped by cutoff filter
	anotherOld := eventlog.Event{
		EventID:      "evt_stale_msg",
		Type:         "agent.register",
		Timestamp:    "2026-03-19T00:00:00Z",
		OriginDaemon: "peer_1",
		Sequence:     3,
		EventJSON:    []byte(`{"type":"agent.register","timestamp":"2026-03-19T00:00:00Z","event_id":"evt_stale_msg","agent_id":"another_stale","kind":"agent","role":"test","module":"test","v":1,"origin_daemon":"peer_1"}`),
	}

	applied2, skipped2, err := applier.ApplyRemoteEvents(ctx, []eventlog.Event{anotherOld})
	if err != nil {
		t.Fatalf("apply second batch: %v", err)
	}
	if applied2 != 0 || skipped2 != 1 {
		t.Errorf("expected 0 applied / 1 skipped, got %d / %d", applied2, skipped2)
	}
}

// TestSyncApplier_ApplyRemoteEvents_CoalescesPostCommit — thrum-1nkt.2:
// the per-event sync trigger now fires ONCE per ApplyRemoteEvents batch
// instead of once per event in the batch. The walker is incremental so
// the per-event fires were near-noop in their useful work, but each
// still paid the walker.mu acquire + compactor cost; bpq5 measured this
// at ~40ms per event under burst. Coalescing collapses N walks into 1.
func TestSyncApplier_ApplyRemoteEvents_CoalescesPostCommit(t *testing.T) {
	st := createTestStateForSync(t)
	applier := NewSyncApplier(st)

	var triggerCount atomic.Int32
	st.SetSyncTrigger(func(ctx context.Context) {
		triggerCount.Add(1)
	})

	const n = 5
	events := make([]eventlog.Event, 0, n)
	for i := range n {
		eventID := fmt.Sprintf("evt_COALESCE_%03d", i)
		agentID := fmt.Sprintf("coalesce_agent_%d", i)
		events = append(events, eventlog.Event{
			EventID:      eventID,
			Sequence:     int64(200 + i),
			Type:         "agent.register",
			Timestamp:    fmt.Sprintf("2026-05-25T12:00:%02dZ", i),
			OriginDaemon: "d_coalesce",
			EventJSON: json.RawMessage(fmt.Sprintf(
				`{"type":"agent.register","timestamp":"2026-05-25T12:00:%02dZ","event_id":%q,"origin_daemon":"d_coalesce","agent_id":%q,"kind":"agent","role":"tester","module":"coalesce","v":1}`,
				i, eventID, agentID,
			)),
		})
	}

	applied, skipped, err := applier.ApplyRemoteEvents(context.Background(), events)
	if err != nil {
		t.Fatalf("ApplyRemoteEvents: %v", err)
	}
	if applied != n {
		t.Fatalf("applied = %d, want %d", applied, n)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d, want 0", skipped)
	}

	if got := int(triggerCount.Load()); got != 1 {
		t.Errorf("sync trigger fired %d times for a %d-event structural batch, want 1 (coalesce broken — every event still fires its own walker)",
			got, n)
	}
}

// TestSyncApplier_ApplyRemoteEvents_NoTriggerForEmptyOrNonStructural —
// thrum-1nkt.2 corollary: the coalesced fire must not run when the
// batch is empty OR when no event in the batch is structural.
//
// Depends on agent.session.start NOT being in
// state.isStructuralEvent's whitelist. If a future change adds it (or
// any of the event types this test sends) to the structural whitelist,
// WriteEvent will start returning a non-nil postCommit and this test
// will fire the trigger — pick a different non-structural event here.
func TestSyncApplier_ApplyRemoteEvents_NoTriggerForEmptyOrNonStructural(t *testing.T) {
	st := createTestStateForSync(t)
	applier := NewSyncApplier(st)

	var triggerCount atomic.Int32
	st.SetSyncTrigger(func(ctx context.Context) {
		triggerCount.Add(1)
	})

	// Empty batch: zero fires.
	if _, _, err := applier.ApplyRemoteEvents(context.Background(), nil); err != nil {
		t.Fatalf("empty batch ApplyRemoteEvents: %v", err)
	}
	if got := int(triggerCount.Load()); got != 0 {
		t.Errorf("empty batch fired trigger %d times, want 0", got)
	}

	// Non-structural batch: still zero fires (every postCommit is nil).
	events := []eventlog.Event{
		{
			EventID:      "evt_NONSTRUCT_001",
			Sequence:     300,
			Type:         "agent.session.start",
			Timestamp:    "2026-05-25T13:00:00Z",
			OriginDaemon: "d_nonstruct",
			EventJSON:    json.RawMessage(`{"type":"agent.session.start","timestamp":"2026-05-25T13:00:00Z","event_id":"evt_NONSTRUCT_001","origin_daemon":"d_nonstruct","agent_id":"nonstruct_agent","session_id":"ses_nonstruct_001","v":1}`),
		},
	}
	if _, _, err := applier.ApplyRemoteEvents(context.Background(), events); err != nil {
		t.Fatalf("non-structural batch ApplyRemoteEvents: %v", err)
	}
	if got := int(triggerCount.Load()); got != 0 {
		t.Errorf("non-structural batch fired trigger %d times, want 0", got)
	}
}
