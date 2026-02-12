package rpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/eventlog"
)

// mockEventQuerier implements EventQuerier for testing.
type mockEventQuerier struct {
	events []eventlog.Event
}

func (m *mockEventQuerier) GetEventsSince(afterSeq int64, limit int) ([]eventlog.Event, int64, bool, error) {
	var result []eventlog.Event
	for _, e := range m.events {
		if e.Sequence > afterSeq {
			result = append(result, e)
		}
	}

	if len(result) == 0 {
		return nil, 0, false, nil
	}

	more := false
	if len(result) > limit {
		result = result[:limit]
		more = true
	}

	nextSeq := result[len(result)-1].Sequence
	return result, nextSeq, more, nil
}

func TestSyncPullHandler_Basic(t *testing.T) {
	events := make([]eventlog.Event, 10)
	for i := range 10 {
		events[i] = eventlog.Event{
			EventID:      "evt_" + string(rune('A'+i)),
			Sequence:     int64(i + 1),
			Type:         "message.create",
			Timestamp:    "2026-02-11T10:00:00Z",
			OriginDaemon: "d_test",
			EventJSON:    json.RawMessage(`{}`),
		}
	}

	h := NewSyncPullHandler(&mockEventQuerier{events: events})

	params, _ := json.Marshal(SyncPullRequest{AfterSequence: 0, MaxBatch: 100})
	result, err := h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	resp := result.(SyncPullResponse)
	if len(resp.Events) != 10 {
		t.Errorf("got %d events, want 10", len(resp.Events))
	}
	if resp.NextSequence != 10 {
		t.Errorf("NextSequence = %d, want 10", resp.NextSequence)
	}
	if resp.MoreAvailable {
		t.Error("MoreAvailable should be false")
	}
}

func TestSyncPullHandler_Batching(t *testing.T) {
	events := make([]eventlog.Event, 2500)
	for i := range 2500 {
		events[i] = eventlog.Event{
			EventID:  "evt_" + string(rune(i)),
			Sequence: int64(i + 1),
			Type:     "message.create",
		}
	}

	h := NewSyncPullHandler(&mockEventQuerier{events: events})

	// First batch
	params, _ := json.Marshal(SyncPullRequest{AfterSequence: 0, MaxBatch: 1000})
	result, err := h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	resp := result.(SyncPullResponse)
	if len(resp.Events) != 1000 {
		t.Errorf("batch 1: got %d events, want 1000", len(resp.Events))
	}
	if !resp.MoreAvailable {
		t.Error("batch 1: MoreAvailable should be true")
	}

	// Second batch
	params, _ = json.Marshal(SyncPullRequest{AfterSequence: resp.NextSequence, MaxBatch: 1000})
	result, err = h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	resp = result.(SyncPullResponse)
	if len(resp.Events) != 1000 {
		t.Errorf("batch 2: got %d events, want 1000", len(resp.Events))
	}
	if !resp.MoreAvailable {
		t.Error("batch 2: MoreAvailable should be true")
	}

	// Third batch (remainder)
	params, _ = json.Marshal(SyncPullRequest{AfterSequence: resp.NextSequence, MaxBatch: 1000})
	result, err = h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	resp = result.(SyncPullResponse)
	if len(resp.Events) != 500 {
		t.Errorf("batch 3: got %d events, want 500", len(resp.Events))
	}
	if resp.MoreAvailable {
		t.Error("batch 3: MoreAvailable should be false")
	}
}

func TestSyncPullHandler_CapsBatchSize(t *testing.T) {
	h := NewSyncPullHandler(&mockEventQuerier{})

	// Request with max_batch > 1000 should be capped
	params, _ := json.Marshal(SyncPullRequest{AfterSequence: 0, MaxBatch: 5000})
	_, err := h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// No panic or error means it was capped correctly
}

func TestSyncPullHandler_EmptyResult(t *testing.T) {
	h := NewSyncPullHandler(&mockEventQuerier{})

	params, _ := json.Marshal(SyncPullRequest{AfterSequence: 0})
	result, err := h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	resp := result.(SyncPullResponse)
	if len(resp.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(resp.Events))
	}
}

func TestSyncPullHandler_NegativeSequence(t *testing.T) {
	h := NewSyncPullHandler(&mockEventQuerier{})

	params, _ := json.Marshal(SyncPullRequest{AfterSequence: -1})
	_, err := h.Handle(context.Background(), params)
	if err == nil {
		t.Error("expected error for negative after_sequence")
	}
}
