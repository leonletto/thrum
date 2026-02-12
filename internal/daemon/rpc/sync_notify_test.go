package rpc

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

func TestSyncNotifyHandler_Basic(t *testing.T) {
	var triggered atomic.Int32

	handler := NewSyncNotifyHandler(func(daemonID string) {
		triggered.Add(1)
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

func TestSyncNotifyHandler_MultiplePeers(t *testing.T) {
	var triggered atomic.Int32

	handler := NewSyncNotifyHandler(func(daemonID string) {
		triggered.Add(1)
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

	if triggered.Load() != 2 {
		t.Errorf("triggered %d times, want 2", triggered.Load())
	}
}

func TestSyncNotifyHandler_TokenField(t *testing.T) {
	var triggered atomic.Int32

	handler := NewSyncNotifyHandler(func(daemonID string) {
		triggered.Add(1)
	})

	// Token field should be accepted in params (validated by sync_server, not handler)
	params, _ := json.Marshal(SyncNotifyRequest{
		Token:      "test-token-abc",
		DaemonID:   "peer-1",
		LatestSeq:  100,
		EventCount: 5,
	})

	result, err := handler.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	if result.(map[string]string)["status"] != "ok" {
		t.Errorf("status = %q, want ok", result.(map[string]string)["status"])
	}

	time.Sleep(50 * time.Millisecond)

	if triggered.Load() != 1 {
		t.Errorf("triggered %d times, want 1", triggered.Load())
	}
}
