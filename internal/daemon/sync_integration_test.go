package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/checkpoint"
	"github.com/leonletto/thrum/internal/daemon/eventlog"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
)

// testDaemon is a lightweight daemon instance for integration tests.
type testDaemon struct {
	state    *state.State
	listener net.Listener
	cancel   context.CancelFunc
}

// newTestDaemon creates a test daemon with a sync server on a random TCP port.
func newTestDaemon(t *testing.T, name string) *testDaemon {
	t.Helper()

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("create thrum dir for %s: %v", name, err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "r_"+name)
	if err != nil {
		t.Fatalf("create state for %s: %v", name, err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Create sync registry with sync.pull and sync.peer_info handlers
	reg := NewSyncRegistry()
	pullHandler := rpc.NewSyncPullHandler(st)
	peerInfoHandler := rpc.NewPeerInfoHandler(st.DaemonID(), name)

	_ = reg.Register("sync.pull", pullHandler.Handle)
	_ = reg.Register("sync.peer_info", peerInfoHandler.Handle)

	// Start TCP listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for %s: %v", name, err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go reg.ServeSyncRPC(ctx, conn, "test-peer")
		}
	}()

	return &testDaemon{
		state:    st,
		listener: ln,
		cancel:   cancel,
	}
}

// addr returns the TCP address of the daemon's sync listener.
func (d *testDaemon) addr() string {
	return d.listener.Addr().String()
}

// writeEvent writes a test event to the daemon's state.
func (d *testDaemon) writeEvent(t *testing.T, agentID string, idx int) {
	t.Helper()
	evt := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		EventID:   fmt.Sprintf("evt_%s_%04d", agentID, idx),
		AgentID:   agentID,
		Kind:      "agent",
		Role:      "tester",
		Module:    "test",
	}
	if err := d.state.WriteEvent(evt); err != nil {
		t.Fatalf("write event %d: %v", idx, err)
	}
}

// eventCount returns the number of events in the daemon's database.
func (d *testDaemon) eventCount(t *testing.T) int {
	t.Helper()
	var count int
	err := d.state.DB().QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	return count
}

func TestPullSyncBasic(t *testing.T) {
	daemonA := newTestDaemon(t, "alice")
	daemonB := newTestDaemon(t, "bob")

	// Create 10 events on daemon A
	for i := 1; i <= 10; i++ {
		daemonA.writeEvent(t, "agent_a", i)
	}

	if daemonA.eventCount(t) != 10 {
		t.Fatalf("daemon A should have 10 events, got %d", daemonA.eventCount(t))
	}

	// Pull from A to B using DaemonSyncManager
	applier := NewSyncApplier(daemonB.state)
	client := NewSyncClient()

	resp, err := client.PullEvents(daemonA.addr(), 0, "")
	if err != nil {
		t.Fatalf("PullEvents: %v", err)
	}

	if len(resp.Events) != 10 {
		t.Errorf("got %d events, want 10", len(resp.Events))
	}

	applied, skipped, err := applier.ApplyRemoteEvents(resp.Events)
	if err != nil {
		t.Fatalf("ApplyRemoteEvents: %v", err)
	}

	if applied != 10 {
		t.Errorf("applied = %d, want 10", applied)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d, want 0", skipped)
	}

	// Verify events appear in B's database
	if daemonB.eventCount(t) != 10 {
		t.Errorf("daemon B should have 10 events, got %d", daemonB.eventCount(t))
	}

	// Verify event IDs match
	rows, err := daemonB.state.DB().Query("SELECT event_id FROM events ORDER BY sequence")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var eventIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		eventIDs = append(eventIDs, id)
	}

	for i := 1; i <= 10; i++ {
		expected := fmt.Sprintf("evt_agent_a_%04d", i)
		if !slices.Contains(eventIDs, expected) {
			t.Errorf("event %s not found in daemon B", expected)
		}
	}
}

func TestPullSyncDeduplication(t *testing.T) {
	daemonA := newTestDaemon(t, "alice")
	daemonB := newTestDaemon(t, "bob")

	// Create events on daemon A
	for i := 1; i <= 5; i++ {
		daemonA.writeEvent(t, "agent_dedup", i)
	}

	applier := NewSyncApplier(daemonB.state)
	client := NewSyncClient()

	// First sync
	resp, err := client.PullEvents(daemonA.addr(), 0, "")
	if err != nil {
		t.Fatalf("PullEvents (first): %v", err)
	}

	applied1, skipped1, err := applier.ApplyRemoteEvents(resp.Events)
	if err != nil {
		t.Fatalf("ApplyRemoteEvents (first): %v", err)
	}

	if applied1 != 5 {
		t.Errorf("first sync: applied = %d, want 5", applied1)
	}
	if skipped1 != 0 {
		t.Errorf("first sync: skipped = %d, want 0", skipped1)
	}

	// Second sync (same events)
	resp2, err := client.PullEvents(daemonA.addr(), 0, "")
	if err != nil {
		t.Fatalf("PullEvents (second): %v", err)
	}

	applied2, skipped2, err := applier.ApplyRemoteEvents(resp2.Events)
	if err != nil {
		t.Fatalf("ApplyRemoteEvents (second): %v", err)
	}

	if applied2 != 0 {
		t.Errorf("second sync: applied = %d, want 0", applied2)
	}
	if skipped2 != 5 {
		t.Errorf("second sync: skipped = %d, want 5", skipped2)
	}

	// Verify events appear only once
	if daemonB.eventCount(t) != 5 {
		t.Errorf("daemon B should have 5 events, got %d", daemonB.eventCount(t))
	}
}

func TestPullSyncCheckpointing(t *testing.T) {
	daemonA := newTestDaemon(t, "alice")
	daemonB := newTestDaemon(t, "bob")

	// Create 5 events on A
	for i := 1; i <= 5; i++ {
		daemonA.writeEvent(t, "agent_cp", i)
	}

	applier := NewSyncApplier(daemonB.state)
	client := NewSyncClient()
	peerID := daemonA.state.DaemonID()

	// First sync with checkpoint
	var totalApplied int
	err := client.PullAllEvents(daemonA.addr(), 0, "", func(events []eventlog.Event, nextSeq int64) error {
		a, _, applyErr := applier.ApplyAndCheckpoint(peerID, events, nextSeq)
		totalApplied += a
		return applyErr
	})
	if err != nil {
		t.Fatalf("PullAllEvents (first): %v", err)
	}
	if totalApplied != 5 {
		t.Errorf("first sync: applied = %d, want 5", totalApplied)
	}

	// Verify checkpoint
	cp, err := checkpoint.GetCheckpoint(daemonB.state.DB(), peerID)
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if cp == nil {
		t.Fatal("checkpoint should exist")
	}
	firstCheckpointSeq := cp.LastSyncedSeq

	// Create 5 more events on A
	for i := 6; i <= 10; i++ {
		daemonA.writeEvent(t, "agent_cp", i)
	}

	// Second sync using checkpoint
	totalApplied = 0
	err = client.PullAllEvents(daemonA.addr(), firstCheckpointSeq, "", func(events []eventlog.Event, nextSeq int64) error {
		a, _, applyErr := applier.ApplyAndCheckpoint(peerID, events, nextSeq)
		totalApplied += a
		return applyErr
	})
	if err != nil {
		t.Fatalf("PullAllEvents (second): %v", err)
	}

	if totalApplied != 5 {
		t.Errorf("second sync: applied = %d, want 5 (only new events)", totalApplied)
	}

	// Verify total events in B
	if daemonB.eventCount(t) != 10 {
		t.Errorf("daemon B should have 10 events, got %d", daemonB.eventCount(t))
	}

	// Verify checkpoint updated
	cp2, err := checkpoint.GetCheckpoint(daemonB.state.DB(), peerID)
	if err != nil {
		t.Fatalf("GetCheckpoint (second): %v", err)
	}
	if cp2.LastSyncedSeq <= firstCheckpointSeq {
		t.Errorf("checkpoint should have advanced: %d <= %d", cp2.LastSyncedSeq, firstCheckpointSeq)
	}
}

func TestPullSyncBatching(t *testing.T) {
	daemonA := newTestDaemon(t, "alice")
	daemonB := newTestDaemon(t, "bob")

	// Create 2500 events on A (will require multiple batches at 1000/batch)
	for i := 1; i <= 2500; i++ {
		daemonA.writeEvent(t, "agent_batch", i)
	}

	if daemonA.eventCount(t) != 2500 {
		t.Fatalf("daemon A should have 2500 events, got %d", daemonA.eventCount(t))
	}

	applier := NewSyncApplier(daemonB.state)
	client := NewSyncClient()

	batchCount := 0
	totalApplied := 0
	err := client.PullAllEvents(daemonA.addr(), 0, "", func(events []eventlog.Event, nextSeq int64) error {
		batchCount++
		a, _, applyErr := applier.ApplyRemoteEvents(events)
		totalApplied += a
		return applyErr
	})
	if err != nil {
		t.Fatalf("PullAllEvents: %v", err)
	}

	if batchCount != 3 {
		t.Errorf("batch count = %d, want 3 (1000+1000+500)", batchCount)
	}
	if totalApplied != 2500 {
		t.Errorf("total applied = %d, want 2500", totalApplied)
	}

	// Verify all events in B
	if daemonB.eventCount(t) != 2500 {
		t.Errorf("daemon B should have 2500 events, got %d", daemonB.eventCount(t))
	}
}

func TestPullSyncEndToEnd_SyncManager(t *testing.T) {
	// Tests the full DaemonSyncManager.SyncFromPeer flow
	daemonA := newTestDaemon(t, "alice")

	// Create temp dir for daemon B's sync manager
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	varDir := filepath.Join(thrumDir, "var")
	if err := os.MkdirAll(varDir, 0750); err != nil {
		t.Fatalf("create var dir: %v", err)
	}

	stB, err := state.NewState(thrumDir, thrumDir, "r_bob")
	if err != nil {
		t.Fatalf("create state B: %v", err)
	}
	t.Cleanup(func() { _ = stB.Close() })

	syncMgr, err := NewDaemonSyncManager(stB, varDir)
	if err != nil {
		t.Fatalf("create sync manager: %v", err)
	}

	// Create events on A
	for i := 1; i <= 15; i++ {
		daemonA.writeEvent(t, "agent_e2e", i)
	}

	// Sync from A
	peerID := daemonA.state.DaemonID()
	applied, skipped, err := syncMgr.SyncFromPeer(daemonA.addr(), peerID)
	if err != nil {
		t.Fatalf("SyncFromPeer: %v", err)
	}
	if applied != 15 {
		t.Errorf("applied = %d, want 15", applied)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d, want 0", skipped)
	}

	// Verify events in B
	var count int
	err = stB.DB().QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 15 {
		t.Errorf("events in DB = %d, want 15", count)
	}

	// Verify checkpoint was set
	cp, err := checkpoint.GetCheckpoint(stB.DB(), peerID)
	if err != nil {
		t.Fatalf("GetCheckpoint: %v", err)
	}
	if cp == nil || cp.LastSyncedSeq == 0 {
		t.Error("checkpoint should be set after sync")
	}

	// Sync again — should skip all
	applied2, skipped2, err := syncMgr.SyncFromPeer(daemonA.addr(), peerID)
	if err != nil {
		t.Fatalf("SyncFromPeer (second): %v", err)
	}
	if applied2 != 0 {
		t.Errorf("second sync: applied = %d, want 0", applied2)
	}
	if skipped2 != 0 {
		// After checkpointing, the second sync should not even fetch old events
		// since it uses the checkpoint as after_sequence
		t.Logf("second sync: skipped = %d (expected 0 with checkpoint)", skipped2)
	}
}

func TestPullSyncPeerInfo(t *testing.T) {
	daemonA := newTestDaemon(t, "alice")
	client := NewSyncClient()

	info, err := client.QueryPeerInfo(daemonA.addr(), "")
	if err != nil {
		t.Fatalf("QueryPeerInfo: %v", err)
	}

	if info.DaemonID != daemonA.state.DaemonID() {
		t.Errorf("DaemonID = %q, want %q", info.DaemonID, daemonA.state.DaemonID())
	}
	if info.Name != "alice" {
		t.Errorf("Name = %q, want %q", info.Name, "alice")
	}
}

func TestPullSyncSecurityBoundary(t *testing.T) {
	// Verify that application RPCs are rejected on the sync endpoint
	daemonA := newTestDaemon(t, "alice")

	conn, err := net.Dial("tcp", daemonA.addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Try to call an application RPC
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "message.send",
		"params":  map[string]any{"body": "test"},
	}
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var resp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error for application RPC on sync endpoint")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code = %d, want -32601 (method not found)", resp.Error.Code)
	}
}
