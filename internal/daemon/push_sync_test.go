package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/checkpoint"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
)

// newTestDaemonWithNotify creates a test daemon that supports sync.notify, sync.pull, and sync.peer_info.
func newTestDaemonWithNotify(t *testing.T, name string, notifyHandler rpc.SyncTriggerFunc) *testDaemon {
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

	reg := NewSyncRegistry()
	pullHandler := rpc.NewSyncPullHandler(st)
	peerInfoHandler := rpc.NewPeerInfoHandler(st.DaemonID(), name)
	syncNotifyHandler := rpc.NewSyncNotifyHandler(notifyHandler)

	_ = reg.Register("sync.pull", pullHandler.Handle)
	_ = reg.Register("sync.peer_info", peerInfoHandler.Handle)
	_ = reg.Register("sync.notify", syncNotifyHandler.Handle)

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

func TestPushSync_NotifyTriggersSync(t *testing.T) {
	// Create daemon A (source of events)
	daemonA := newTestDaemon(t, "daemon-a")

	// Track sync triggers on daemon B
	var syncTriggered atomic.Int32
	daemonB := newTestDaemonWithNotify(t, "daemon-b", func(daemonID string) {
		syncTriggered.Add(1)
	})
	_ = daemonB // daemon B just needs to exist

	// Write events on daemon A
	writeTestEvent(t, daemonA.state, "message.create")

	// Send notification to daemon B
	client := NewSyncClient()
	err := client.SendNotify(daemonB.addr(), daemonA.state.DaemonID(), 1, 1, "")
	if err != nil {
		t.Fatalf("SendNotify: %v", err)
	}

	// Wait for async sync to trigger
	time.Sleep(100 * time.Millisecond)

	if syncTriggered.Load() == 0 {
		t.Error("sync was not triggered after notification")
	}
}

func TestPushSync_EndToEndEventSync(t *testing.T) {
	// Create two test daemons
	daemonA := newTestDaemon(t, "daemon-a")
	daemonB := newTestDaemon(t, "daemon-b")

	// Write an event on daemon A
	writeTestEvent(t, daemonA.state, "message.create")

	// Create sync manager for daemon B
	syncManager, err := NewDaemonSyncManager(daemonB.state, t.TempDir())
	if err != nil {
		t.Fatalf("create sync manager: %v", err)
	}

	// Add daemon A as a peer (use 127.0.0.1 since we're on localhost)
	_ = syncManager.PeerRegistry().AddPeer(&PeerInfo{
		DaemonID: daemonA.state.DaemonID(),
		Name:     "daemon-a",
		Address:  daemonA.addr(),
	})

	// Simulate what happens when daemon B receives a sync.notify from daemon A:
	// It calls SyncFromPeerByID
	syncManager.SyncFromPeerByID(daemonA.state.DaemonID())

	// Verify event was synced
	cp, err := checkpoint.GetCheckpoint(daemonB.state.DB(), daemonA.state.DaemonID())
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if cp == nil || cp.LastSyncedSeq == 0 {
		t.Error("checkpoint not updated after sync")
	}

	// Verify event exists in daemon B's database
	events, _, _, err := daemonB.state.GetEventsSince(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) == 0 {
		t.Error("no events synced to daemon B")
	}
}

func TestPushSync_BroadcastNotifyAllPeers(t *testing.T) {
	// Create daemon A and two peer daemons
	daemonA := newTestDaemon(t, "daemon-a")

	var peer1Notified, peer2Notified atomic.Int32
	peer1 := newTestDaemonWithNotify(t, "peer-1", func(daemonID string) {
		peer1Notified.Add(1)
	})
	peer2 := newTestDaemonWithNotify(t, "peer-2", func(daemonID string) {
		peer2Notified.Add(1)
	})

	// Create sync manager for daemon A
	syncManager, err := NewDaemonSyncManager(daemonA.state, t.TempDir())
	if err != nil {
		t.Fatalf("create sync manager: %v", err)
	}

	// Add both peers
	_ = syncManager.PeerRegistry().AddPeer(&PeerInfo{
		DaemonID: "d_peer-1",
		Name:     "peer-1",
		Address:  peer1.addr(),
	})
	_ = syncManager.PeerRegistry().AddPeer(&PeerInfo{
		DaemonID: "d_peer-2",
		Name:     "peer-2",
		Address:  peer2.addr(),
	})

	// Broadcast notification
	syncManager.BroadcastNotify(daemonA.state.DaemonID(), 100, 1)

	// Wait for notifications to arrive
	time.Sleep(200 * time.Millisecond)

	if peer1Notified.Load() == 0 {
		t.Error("peer-1 was not notified")
	}
	if peer2Notified.Load() == 0 {
		t.Error("peer-2 was not notified")
	}
}

func TestPushSync_EventWriteHookTriggersNotification(t *testing.T) {
	// Create daemon A
	daemonA := newTestDaemon(t, "daemon-a")

	// Create a peer daemon that tracks notifications
	var notified atomic.Int32
	peer := newTestDaemonWithNotify(t, "peer", func(daemonID string) {
		notified.Add(1)
	})

	// Create sync manager
	syncManager, err := NewDaemonSyncManager(daemonA.state, t.TempDir())
	if err != nil {
		t.Fatalf("create sync manager: %v", err)
	}

	// Add peer
	_ = syncManager.PeerRegistry().AddPeer(&PeerInfo{
		DaemonID: "d_peer",
		Name:     "peer",
		Address:  peer.addr(),
	})

	// Wire event write hook (same as daemon main.go does)
	daemonA.state.SetOnEventWrite(func(daemonID string, sequence int64, eventCount int) {
		go syncManager.BroadcastNotify(daemonID, sequence, eventCount)
	})

	// Write an event — this should trigger the hook → BroadcastNotify → peer receives sync.notify
	writeTestEvent(t, daemonA.state, "thread.create")

	// Wait for async notification
	time.Sleep(200 * time.Millisecond)

	if notified.Load() == 0 {
		t.Error("peer was not notified after event creation")
	}
}

func TestPushSync_NotifyFailureDoesNotBlockWrite(t *testing.T) {
	// Create daemon A with a hook that tries to notify an unreachable peer
	daemonA := newTestDaemon(t, "daemon-a")

	syncManager, err := NewDaemonSyncManager(daemonA.state, t.TempDir())
	if err != nil {
		t.Fatalf("create sync manager: %v", err)
	}

	// Add peer with unreachable address
	_ = syncManager.PeerRegistry().AddPeer(&PeerInfo{
		DaemonID: "d_offline",
		Name:     "offline",
		Address:  "127.0.0.1:1", // Almost certainly not listening
	})

	// Wire hook
	daemonA.state.SetOnEventWrite(func(daemonID string, sequence int64, eventCount int) {
		go syncManager.BroadcastNotify(daemonID, sequence, eventCount)
	})

	// Write should succeed even though notification will fail
	writeTestEvent(t, daemonA.state, "message.create")

	// Verify event was written
	events, _, _, err := daemonA.state.GetEventsSince(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) == 0 {
		t.Error("no events written")
	}
}

func TestPushSync_PeriodicSyncCatchesMissedNotifications(t *testing.T) {
	// Create two daemons
	daemonA := newTestDaemon(t, "daemon-a")
	daemonB := newTestDaemon(t, "daemon-b")

	// Write events on daemon A (WITHOUT notifying daemon B)
	writeTestEvent(t, daemonA.state, "message.create")
	writeTestEvent(t, daemonA.state, "thread.create")

	// Create sync manager for daemon B
	syncManager, err := NewDaemonSyncManager(daemonB.state, t.TempDir())
	if err != nil {
		t.Fatalf("create sync manager: %v", err)
	}

	// Add daemon A as peer
	_ = syncManager.PeerRegistry().AddPeer(&PeerInfo{
		DaemonID: daemonA.state.DaemonID(),
		Name:     "daemon-a",
		Address:  daemonA.addr(),
	})

	// Create scheduler and run one sync cycle
	scheduler := NewPeriodicSyncScheduler(syncManager, daemonB.state)
	scheduler.syncFromPeers()

	// Verify events were synced
	events, _, _, err := daemonB.state.GetEventsSince(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) < 2 {
		t.Errorf("expected at least 2 events synced, got %d", len(events))
	}
}

func TestPushSync_PeriodicSyncSkipsRecentlySynced(t *testing.T) {
	daemonA := newTestDaemon(t, "daemon-a")
	daemonB := newTestDaemon(t, "daemon-b")

	// Write event on daemon A
	writeTestEvent(t, daemonA.state, "message.create")

	// Create sync manager for daemon B
	syncManager, err := NewDaemonSyncManager(daemonB.state, t.TempDir())
	if err != nil {
		t.Fatalf("create sync manager: %v", err)
	}

	_ = syncManager.PeerRegistry().AddPeer(&PeerInfo{
		DaemonID: daemonA.state.DaemonID(),
		Name:     "daemon-a",
		Address:  daemonA.addr(),
	})

	// Sync once to set the checkpoint
	scheduler := NewPeriodicSyncScheduler(syncManager, daemonB.state)
	scheduler.syncFromPeers()

	// Write another event on daemon A
	writeTestEvent(t, daemonA.state, "thread.create")

	// Run another sync cycle — but with long threshold, it should skip
	scheduler.SetRecentThreshold(1 * time.Hour)
	scheduler.syncFromPeers()

	// writeTestEvent("message.create") writes 2 events (agent.register + message.create)
	// So after first sync, daemon B should have 2 events
	events, _, _, err := daemonB.state.GetEventsSince(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events (skipped recent peer), got %d", len(events))
	}

	// Now with 0 threshold, sync should catch the thread.create event
	scheduler.SetRecentThreshold(0)
	scheduler.syncFromPeers()

	events, _, _, err = daemonB.state.GetEventsSince(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("expected 3 events after zero threshold, got %d", len(events))
	}
}

func TestPushSync_SendNotifyClient(t *testing.T) {
	// Create a daemon with sync.notify handler
	var received atomic.Int32
	var receivedDaemonID atomic.Value

	daemon := newTestDaemonWithNotify(t, "test", func(daemonID string) {
		received.Add(1)
		receivedDaemonID.Store(daemonID)
	})

	// Send a notification
	client := NewSyncClient()
	err := client.SendNotify(daemon.addr(), "sender-daemon", 42, 5, "")
	if err != nil {
		t.Fatalf("SendNotify: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if received.Load() == 0 {
		t.Error("notification not received")
	}
	if got, _ := receivedDaemonID.Load().(string); got != "sender-daemon" {
		t.Errorf("daemon_id = %q, want sender-daemon", got)
	}
}

func TestPushSync_InvalidNotificationIgnored(t *testing.T) {
	var triggered atomic.Int32
	daemon := newTestDaemonWithNotify(t, "test", func(daemonID string) {
		triggered.Add(1)
	})

	// Send raw invalid JSON to sync.notify
	conn, err := net.DialTimeout("tcp", daemon.addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	// Send a sync.notify with missing daemon_id
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  "sync.notify",
		"params":  map[string]any{"latest_seq": 100},
		"id":      1,
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	_, _ = conn.Write(data)

	time.Sleep(100 * time.Millisecond)

	if triggered.Load() != 0 {
		t.Error("sync was triggered despite invalid notification")
	}
}

func TestPushSync_HealthIncludesTailscaleInfo(t *testing.T) {
	healthHandler := rpc.NewHealthHandler(time.Now(), "1.0.0", "r_test")

	// Set a provider that returns sync info
	healthHandler.SetTailscaleInfoProvider(func() *rpc.TailscaleSyncInfo {
		return &rpc.TailscaleSyncInfo{
			Enabled:        true,
			Hostname:       "thrum-test",
			ConnectedPeers: 2,
			Peers: []rpc.TailscalePeer{
				{DaemonID: "d_peer1", Name: "peer1", LastSync: "5s ago"},
				{DaemonID: "d_peer2", Name: "peer2", LastSync: "10s ago"},
			},
			SyncStatus: "idle",
		}
	})

	result, err := healthHandler.Handle(context.Background(), nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	resp, ok := result.(rpc.HealthResponse)
	if !ok {
		t.Fatalf("unexpected type: %T", result)
	}

	if resp.Tailscale == nil {
		t.Fatal("tailscale info is nil")
	}
	if !resp.Tailscale.Enabled {
		t.Error("tailscale not enabled")
	}
	if resp.Tailscale.Hostname != "thrum-test" {
		t.Errorf("hostname = %q, want thrum-test", resp.Tailscale.Hostname)
	}
	if resp.Tailscale.ConnectedPeers != 2 {
		t.Errorf("connected_peers = %d, want 2", resp.Tailscale.ConnectedPeers)
	}
	if len(resp.Tailscale.Peers) != 2 {
		t.Errorf("peers count = %d, want 2", len(resp.Tailscale.Peers))
	}
}

func TestPushSync_HealthWithoutTailscale(t *testing.T) {
	healthHandler := rpc.NewHealthHandler(time.Now(), "1.0.0", "r_test")

	result, err := healthHandler.Handle(context.Background(), nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	resp := result.(rpc.HealthResponse)
	if resp.Tailscale != nil {
		t.Error("tailscale info should be nil when no provider is set")
	}
}

// writeTestEvent writes a simple test event to the given state.
func writeTestEvent(t *testing.T, st *state.State, eventType string) {
	t.Helper()
	switch eventType {
	case "message.create":
		// Register an agent first
		_ = st.WriteEvent(context.Background(), types.AgentRegisterEvent{
			Type:      "agent.register",
			AgentID:   "test-agent",
			Role:      "tester",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		err := st.WriteEvent(context.Background(), types.MessageCreateEvent{
			Type:      "message.create",
			AgentID:   "test-agent",
			ThreadID:  "t_test",
			Body:      types.MessageBody{Format: "text/plain", Content: "test message"},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			t.Fatalf("write message event: %v", err)
		}
	case "thread.create":
		err := st.WriteEvent(context.Background(), types.ThreadCreateEvent{
			Type:      "thread.create",
			ThreadID:  "t_test_" + time.Now().Format("150405.000"),
			Title:     "Test Thread",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			t.Fatalf("write thread event: %v", err)
		}
	default:
		t.Fatalf("unsupported event type for test: %s", eventType)
	}
}
