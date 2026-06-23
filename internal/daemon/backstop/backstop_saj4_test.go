package backstop

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestTick_VisibilityFilter is the thrum-saj4 regression: the raw
// message_deliveries scan counts unread rows the recipient cannot SEE in any
// inbox view (a delivery row for a message hidden by the for-agent visibility
// filter — the storm-relay feeder). The backstop must nudge only when the
// recipient has VISIBLE unread, using the injected VisibleUnread count.
func TestTick_VisibilityFilter(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	fake := &fakeDispatcher{}

	seedAgent(t, db, "sees_mail", true)
	seedAgent(t, db, "phantom_only", true)
	old := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339Nano)
	seedDelivery(t, db, "msg_visible", "sees_mail", old, "", "")
	seedDelivery(t, db, "msg_hidden", "phantom_only", old, "", "")

	bs := &Backstop{
		DB:        db,
		Dispatch:  fake,
		AgeCutoff: 15 * time.Minute,
		// Both agents have a raw unread delivery row, but only sees_mail's is
		// inbox-visible; phantom_only's is filter-hidden (visible count 0).
		VisibleUnread: func(_ context.Context, agentID string) (int, error) {
			if agentID == "sees_mail" {
				return 1, nil
			}
			return 0, nil
		},
	}
	if err := bs.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("dispatch calls = %d, want 1 (visible only); got %+v", len(calls), calls)
	}
	if calls[0].agentID != "sees_mail" {
		t.Errorf("dispatched to %q, want sees_mail (phantom_only's hidden unread must not nudge)", calls[0].agentID)
	}
}

// TestTick_VisibilityFilter_DispatchesVisibleCount pins that the nudge carries
// the VISIBLE count, not the raw delivery-row count — so the reminder reflects
// what the agent will actually find in their inbox.
func TestTick_VisibilityFilter_DispatchesVisibleCount(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	fake := &fakeDispatcher{}

	seedAgent(t, db, "agent_a", true)
	old := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339Nano)
	// 3 raw unread delivery rows, but only 2 are inbox-visible.
	seedDelivery(t, db, "m1", "agent_a", old, "", "")
	seedDelivery(t, db, "m2", "agent_a", old, "", "")
	seedDelivery(t, db, "m3", "agent_a", old, "", "")

	bs := &Backstop{
		DB: db, Dispatch: fake, AgeCutoff: 15 * time.Minute,
		VisibleUnread: func(_ context.Context, _ string) (int, error) { return 2, nil },
	}
	if err := bs.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].count != 2 {
		t.Fatalf("want one dispatch carrying the visible count 2, got %+v", calls)
	}
}

// TestTick_VisibilityFilter_FailsClosed pins that a VisibleUnread error skips
// the nudge (fail closed) — a count failure must not resurrect the phantom.
func TestTick_VisibilityFilter_FailsClosed(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	fake := &fakeDispatcher{}

	seedAgent(t, db, "agent_a", true)
	old := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339Nano)
	seedDelivery(t, db, "m1", "agent_a", old, "", "")

	bs := &Backstop{
		DB: db, Dispatch: fake, AgeCutoff: 15 * time.Minute,
		VisibleUnread: func(_ context.Context, _ string) (int, error) { return 0, errors.New("db boom") },
	}
	if err := bs.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Fatalf("VisibleUnread error must skip the nudge (fail closed); got %+v", calls)
	}
}

// TestTick_NilVisibleUnread_LegacyAllowAll pins the nil-predicate behavior:
// constructors that don't set VisibleUnread keep the raw-scan semantics.
func TestTick_NilVisibleUnread_LegacyAllowAll(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	fake := &fakeDispatcher{}

	seedAgent(t, db, "agent_a", true)
	old := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339Nano)
	seedDelivery(t, db, "m1", "agent_a", old, "", "")

	bs := &Backstop{DB: db, Dispatch: fake, AgeCutoff: 15 * time.Minute}
	if err := bs.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if calls := fake.Calls(); len(calls) != 1 || calls[0].agentID != "agent_a" {
		t.Fatalf("nil VisibleUnread must keep legacy raw-scan nudge; got %+v", calls)
	}
}
