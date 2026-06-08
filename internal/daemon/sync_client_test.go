package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/eventlog"
	"github.com/leonletto/thrum/internal/websocket"
)

// newTestWSServer starts an httptest.Server that serves WebSocket RPC using the
// provided SyncRegistry. Returns the server and a cleanup function.
// The returned addr is in "host:port" format (no scheme) suitable for SyncClient.
func newTestWSServer(t *testing.T, reg *SyncRegistry) (addr string, cleanup func()) {
	t.Helper()
	ts := httptest.NewServer(websocket.NewServer("", reg, nil).HTTPHandler())
	t.Cleanup(ts.Close)
	// Strip "http://" prefix — SyncClient builds the ws:// URL itself.
	addr = ts.Listener.Addr().String()
	return addr, ts.Close
}

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

	addr, _ := newTestWSServer(t, reg)

	// Test PullEvents
	client := NewSyncClient()
	resp, err := client.PullEvents(addr, 0, "")
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
	resp, err = client.PullEvents(addr, 2, "")
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

	addr, _ := newTestWSServer(t, reg)

	client := NewSyncClient()
	info, err := client.QueryPeerInfo(addr, "")
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

// TestPullResponse_FilteredFlag verifies the client can read the additive
// `filtered` flag a 0.11 hub sets on a directed/filtered sync.pull response
// (D10/I-1). The key is omitempty: absent in a normal peer's body → false.
func TestPullResponse_FilteredFlag(t *testing.T) {
	var withFlag PullResponse
	if err := json.Unmarshal([]byte(`{"events":[],"next_sequence":5,"more_available":true,"filtered":true}`), &withFlag); err != nil {
		t.Fatalf("unmarshal with filtered:true: %v", err)
	}
	if !withFlag.Filtered {
		t.Fatal("Filtered must be true when body carries filtered:true")
	}

	var without PullResponse
	if err := json.Unmarshal([]byte(`{"events":[],"next_sequence":5,"more_available":true}`), &without); err != nil {
		t.Fatalf("unmarshal without filtered key: %v", err)
	}
	if without.Filtered {
		t.Fatal("Filtered must default to false when the key is absent")
	}
}

// TestPullAllEvents_FilteredMultiWindow_DoesNotStopOnEmpty (D13 part 2): an
// empty-but-filtered window (a window where the directed filter dropped every
// event) is NORMAL, not end-of-stream. PullAllEvents must keep pulling while
// more_available, traversing all windows to the final scan-watermark instead of
// halting at the empty middle window.
func TestPullAllEvents_FilteredMultiWindow_DoesNotStopOnEmpty(t *testing.T) {
	reg := NewSyncRegistry()

	type window struct {
		events []eventlog.Event
		next   int64
		more   bool
		filt   bool
	}
	windows := []window{
		{events: []eventlog.Event{{EventID: "m2", Sequence: 2, Type: "message.create", Timestamp: "2026-02-11T10:00:00Z", OriginDaemon: "d_test", EventJSON: json.RawMessage(`{"type":"message.create"}`)}}, next: 2, more: true, filt: true},
		{events: nil, next: 80, more: true, filt: true},
		{events: []eventlog.Event{{EventID: "m90", Sequence: 90, Type: "message.create", Timestamp: "2026-02-11T10:01:00Z", OriginDaemon: "d_test", EventJSON: json.RawMessage(`{"type":"message.create"}`)}}, next: 90, more: false, filt: true},
	}
	var calls int64
	_ = reg.Register("sync.pull", func(_ context.Context, _ json.RawMessage) (any, error) {
		i := int(atomic.AddInt64(&calls, 1) - 1)
		if i >= len(windows) {
			return map[string]any{"events": nil, "next_sequence": int64(0), "more_available": false}, nil
		}
		w := windows[i]
		return map[string]any{
			"events":         w.events,
			"next_sequence":  w.next,
			"more_available": w.more,
			"filtered":       w.filt,
		}, nil
	})

	addr, _ := newTestWSServer(t, reg)

	c := NewSyncClient()
	var lastSeq int64
	err := c.PullAllEvents(addr, 0, "", func(_ []eventlog.Event, nextSeq int64, _ bool) error {
		lastSeq = nextSeq // stand-in for ApplyAndCheckpoint
		return nil
	})
	if err != nil {
		t.Fatalf("PullAllEvents: %v", err)
	}
	if lastSeq != 90 {
		t.Fatalf("pull must traverse all 3 windows to seq 90, stopped at %d", lastSeq)
	}
	if got := atomic.LoadInt64(&calls); got != 3 {
		t.Fatalf("must not stop at the empty w2; served %d windows", got)
	}
}
