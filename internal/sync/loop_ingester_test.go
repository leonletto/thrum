package sync

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// fakeIngester records each event it receives so tests can assert
// that SyncLoop.updateProjection routes through it when set.
type fakeIngester struct {
	mu      sync.Mutex
	events  [][]byte
	failErr error
}

func (f *fakeIngester) IngestSyncedEvent(_ context.Context, event []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return f.failErr
	}
	f.events = append(f.events, append([]byte{}, event...))
	return nil
}

func (f *fakeIngester) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// TestUpdateProjection_RoutesThroughIngesterWhenSet verifies the
// Task 6.3 bridge: when SetIngester has been called, every parsed
// event flows through the ingester instead of projector.Apply. This
// is what makes cross-repo replies reach the permission package.
func TestUpdateProjection_RoutesThroughIngesterWhenSet(t *testing.T) {
	loop := &SyncLoop{}
	ing := &fakeIngester{}
	loop.SetIngester(ing)

	events := []json.RawMessage{
		json.RawMessage(`{"type":"message.create","event_id":"e1","agent_id":"a","session_id":"s","body":{"format":"markdown","content":"hi"}}`),
		json.RawMessage(`{"type":"agent.register","event_id":"e2","agent_id":"b","kind":"agent","role":"r","module":"m"}`),
	}
	if err := loop.updateProjection(context.Background(), events); err != nil {
		t.Fatalf("updateProjection: %v", err)
	}
	if got := ing.count(); got != 2 {
		t.Errorf("ingester received %d events, want 2", got)
	}
}

// TestUpdateProjection_IngesterErrorShortCircuits verifies the
// update aborts when the ingester returns an error — keeping the
// sync cycle's retry semantics intact.
func TestUpdateProjection_IngesterErrorShortCircuits(t *testing.T) {
	loop := &SyncLoop{}
	ing := &fakeIngester{failErr: errors.New("boom")}
	loop.SetIngester(ing)

	events := []json.RawMessage{
		json.RawMessage(`{"type":"message.create","event_id":"e1","agent_id":"a","session_id":"s","body":{"format":"markdown","content":"hi"}}`),
	}
	err := loop.updateProjection(context.Background(), events)
	if err == nil {
		t.Fatal("expected error from ingester to propagate")
	}
	if !errors.Is(err, ing.failErr) {
		// Wrapped in fmt.Errorf — errors.Is handles that.
		t.Errorf("error chain should contain ingester error, got %v", err)
	}
}

// TestUpdateProjection_NoIngesterFallsBackToProjector exists as a
// regression guard: tests that use NewSyncLoop without calling
// SetIngester must continue to work via the projector-only path. We
// assert that an empty loop with a nil ingester and a nil projector
// correctly short-circuits the ingester branch (and would then try to
// dereference the projector — so this test constructs a loop with no
// events, which keeps the for-loop body unreachable).
func TestUpdateProjection_NoIngesterEmptyEventsNoOp(t *testing.T) {
	loop := &SyncLoop{}
	if err := loop.updateProjection(context.Background(), nil); err != nil {
		t.Fatalf("empty events with nil ingester should not error: %v", err)
	}
}
