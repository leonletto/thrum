package rpc

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSyncNotifyHandler_Basic(t *testing.T) {
	var triggered atomic.Int32
	var triggeredIDs sync.Map

	handler := NewSyncNotifyHandler(func(daemonID string) {
		triggered.Add(1)
		triggeredIDs.Store(daemonID, true)
		time.Sleep(10 * time.Millisecond) // simulate sync work
	})

	params, _ := json.Marshal(SyncNotifyRequest{
		DaemonID:   "peer-1",
		LatestSeq:  100,
		EventCount: 5,
	})

	result, err := handler.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	resp, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("unexpected result type: %T", result)
	}
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want ok", resp["status"])
	}

	// Wait for async sync to complete
	time.Sleep(50 * time.Millisecond)

	if triggered.Load() != 1 {
		t.Errorf("triggered %d times, want 1", triggered.Load())
	}
	if _, ok := triggeredIDs.Load("peer-1"); !ok {
		t.Error("peer-1 was not triggered")
	}
}

func TestSyncNotifyHandler_MissingParams(t *testing.T) {
	handler := NewSyncNotifyHandler(func(string) {})

	_, err := handler.Handle(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil params")
	}
}

func TestSyncNotifyHandler_MissingDaemonID(t *testing.T) {
	handler := NewSyncNotifyHandler(func(string) {})

	params, _ := json.Marshal(SyncNotifyRequest{
		LatestSeq:  100,
		EventCount: 5,
	})

	_, err := handler.Handle(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing daemon_id")
	}
}

func TestSyncNotifyHandler_Debounce(t *testing.T) {
	var syncCount atomic.Int32
	syncStarted := make(chan struct{}, 1)

	handler := NewSyncNotifyHandler(func(daemonID string) {
		syncCount.Add(1)
		select {
		case syncStarted <- struct{}{}:
		default:
		}
		time.Sleep(50 * time.Millisecond) // simulate slow sync
	})
	handler.debounce = 10 * time.Millisecond // fast debounce for test

	params, _ := json.Marshal(SyncNotifyRequest{
		DaemonID:   "peer-1",
		LatestSeq:  100,
		EventCount: 1,
	})

	// First notification triggers sync
	result, err := handler.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	if result.(map[string]string)["status"] != "ok" {
		t.Errorf("first status = %q, want ok", result.(map[string]string)["status"])
	}

	// Wait for sync to actually start
	<-syncStarted

	// Second notification while syncing should be queued
	params2, _ := json.Marshal(SyncNotifyRequest{
		DaemonID:   "peer-1",
		LatestSeq:  200,
		EventCount: 1,
	})
	result2, err := handler.Handle(context.Background(), params2)
	if err != nil {
		t.Fatalf("second Handle: %v", err)
	}
	if result2.(map[string]string)["status"] != "queued" {
		t.Errorf("second status = %q, want queued", result2.(map[string]string)["status"])
	}

	// Wait for both syncs to complete
	time.Sleep(200 * time.Millisecond)

	count := syncCount.Load()
	if count < 2 {
		t.Errorf("sync triggered %d times, want >= 2 (original + queued)", count)
	}

	// Should no longer be syncing
	if handler.IsSyncing("peer-1") {
		t.Error("still syncing after completion")
	}
}

func TestSyncNotifyHandler_MultiplePeers(t *testing.T) {
	var triggered sync.Map

	handler := NewSyncNotifyHandler(func(daemonID string) {
		triggered.Store(daemonID, true)
		time.Sleep(10 * time.Millisecond)
	})

	// Send notifications from two different peers
	for _, id := range []string{"peer-1", "peer-2"} {
		params, _ := json.Marshal(SyncNotifyRequest{
			DaemonID:   id,
			LatestSeq:  100,
			EventCount: 1,
		})
		result, err := handler.Handle(context.Background(), params)
		if err != nil {
			t.Fatalf("Handle(%s): %v", id, err)
		}
		if result.(map[string]string)["status"] != "ok" {
			t.Errorf("%s status = %q, want ok", id, result.(map[string]string)["status"])
		}
	}

	time.Sleep(50 * time.Millisecond)

	for _, id := range []string{"peer-1", "peer-2"} {
		if _, ok := triggered.Load(id); !ok {
			t.Errorf("%s was not triggered", id)
		}
	}
}
