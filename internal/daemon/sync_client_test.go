package daemon

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/eventlog"
)

func TestSyncClient_PullEvents(t *testing.T) {
	// Set up a sync server with test data
	reg := NewSyncRegistry()

	testEvents := []eventlog.Event{
		{EventID: "evt_001", Sequence: 1, Type: "message.create", Timestamp: "2026-02-11T10:00:00Z", OriginDaemon: "d_test", EventJSON: json.RawMessage(`{"type":"message.create"}`)},
		{EventID: "evt_002", Sequence: 2, Type: "message.create", Timestamp: "2026-02-11T10:01:00Z", OriginDaemon: "d_test", EventJSON: json.RawMessage(`{"type":"message.create"}`)},
		{EventID: "evt_003", Sequence: 3, Type: "agent.register", Timestamp: "2026-02-11T10:02:00Z", OriginDaemon: "d_test", EventJSON: json.RawMessage(`{"type":"agent.register"}`)},
	}

	_ = reg.Register("sync.pull", func(_ context.Context, params json.RawMessage) (any, error) {
		var req struct {
			AfterSequence int64 `json:"after_sequence"`
			MaxBatch      int   `json:"max_batch"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}

		var result []eventlog.Event
		for _, e := range testEvents {
			if e.Sequence > req.AfterSequence {
				result = append(result, e)
			}
		}

		more := false
		if len(result) > req.MaxBatch {
			result = result[:req.MaxBatch]
			more = true
		}

		nextSeq := int64(0)
		if len(result) > 0 {
			nextSeq = result[len(result)-1].Sequence
		}

		return map[string]any{
			"events":         result,
			"next_sequence":  nextSeq,
			"more_available": more,
		}, nil
	})

	_ = reg.Register("sync.peer_info", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{
			"daemon_id": "d_test",
			"name":      "test-host",
		}, nil
	})

	// Start a TCP listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go reg.ServeSyncRPC(ctx, conn, "test-peer")
		}
	}()

	// Test PullEvents
	client := NewSyncClient()
	resp, err := client.PullEvents(ln.Addr().String(), 0, "")
	if err != nil {
		t.Fatalf("PullEvents: %v", err)
	}

	if len(resp.Events) != 3 {
		t.Errorf("got %d events, want 3", len(resp.Events))
	}
	if resp.NextSequence != 3 {
		t.Errorf("NextSequence = %d, want 3", resp.NextSequence)
	}

	// Pull after sequence 2 — should get only 1 event
	resp, err = client.PullEvents(ln.Addr().String(), 2, "")
	if err != nil {
		t.Fatalf("PullEvents(afterSeq=2): %v", err)
	}
	if len(resp.Events) != 1 {
		t.Errorf("got %d events, want 1", len(resp.Events))
	}
}

func TestSyncClient_QueryPeerInfo(t *testing.T) {
	reg := NewSyncRegistry()
	_ = reg.Register("sync.peer_info", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{
			"daemon_id": "d_alice",
			"name":      "alice-laptop",
		}, nil
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go reg.ServeSyncRPC(ctx, conn, "test-peer")
		}
	}()

	client := NewSyncClient()
	info, err := client.QueryPeerInfo(ln.Addr().String(), "")
	if err != nil {
		t.Fatalf("QueryPeerInfo: %v", err)
	}

	if info.DaemonID != "d_alice" {
		t.Errorf("DaemonID = %q, want %q", info.DaemonID, "d_alice")
	}
	if info.Name != "alice-laptop" {
		t.Errorf("Name = %q, want %q", info.Name, "alice-laptop")
	}
}

func TestSyncClient_ConnectionRefused(t *testing.T) {
	client := NewSyncClient()
	_, err := client.PullEvents("127.0.0.1:1", 0, "")
	if err == nil {
		t.Error("expected error for connection refused")
	}
}
