package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/schema"
)

// setupStateTestDB opens an in-memory SQLite DB, runs schema migrations to
// head, and wraps it with *safedb.DB per the project-wide convention. All
// SQL access in daemon code must go through safedb (philosophy doc
// Anti-Pattern #1).
func setupStateTestDB(t *testing.T) *safedb.DB {
	t.Helper()
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return safedb.New(db)
}

func timePtr(t time.Time) *time.Time { return &t }

func TestStateRow_UpsertCreates(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()

	now := time.Unix(1747353600, 0)
	err := store.UpsertState(ctx, &StateRow{
		JobID:           "docs-bot",
		Generation:      1,
		CurrentState:    StateScheduled,
		NextScheduledAt: timePtr(now.Add(5 * time.Minute)),
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.GetState(ctx, "docs-bot")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if got.CurrentState != StateScheduled {
		t.Errorf("CurrentState = %q, want %q", got.CurrentState, StateScheduled)
	}
	if got.NextScheduledAt == nil || !got.NextScheduledAt.Equal(now.Add(5*time.Minute)) {
		t.Errorf("NextScheduledAt = %v, want %v", got.NextScheduledAt, now.Add(5*time.Minute))
	}
	if got.Generation != 1 {
		t.Errorf("Generation = %d, want 1", got.Generation)
	}
	if got.TotalRuns != 0 {
		t.Errorf("TotalRuns = %d, want 0", got.TotalRuns)
	}
}

func TestStateRow_UpsertUpdates(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()

	now := time.Unix(1747353600, 0)
	base := &StateRow{
		JobID:           "docs-bot",
		Generation:      1,
		CurrentState:    StateScheduled,
		NextScheduledAt: timePtr(now.Add(5 * time.Minute)),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := store.UpsertState(ctx, base); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	base.CurrentState = StateRunning
	base.LastFiredAt = timePtr(now.Add(time.Minute))
	base.UpdatedAt = now.Add(time.Minute)
	if err := store.UpsertState(ctx, base); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := store.GetState(ctx, "docs-bot")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if got.CurrentState != StateRunning {
		t.Errorf("CurrentState = %q, want %q", got.CurrentState, StateRunning)
	}
	if !got.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, now.Add(time.Minute))
	}
	if got.LastFiredAt == nil || !got.LastFiredAt.Equal(now.Add(time.Minute)) {
		t.Errorf("LastFiredAt = %v, want %v", got.LastFiredAt, now.Add(time.Minute))
	}
}

func TestStateRow_GetState_NotFound(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	_, err := store.GetState(context.Background(), "nonexistent")
	if err != ErrJobNotFound {
		t.Errorf("err = %v, want ErrJobNotFound", err)
	}
}

// TestStateRow_OneShotTerminal_NullableNextScheduled pins canonical-ref §4.1.1
// one-shot semantics: post-completion the row carries
// current_state='completed' AND next_scheduled_at=NULL. The reactor's
// tick-loop predicate (next_scheduled_at IS NOT NULL) then excludes the
// row from subsequent fires.
func TestStateRow_OneShotTerminal_NullableNextScheduled(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()

	now := time.Unix(1747353600, 0)
	err := store.UpsertState(ctx, &StateRow{
		JobID:           "docs-bot-once",
		Generation:      1,
		CurrentState:    StateCompleted,
		NextScheduledAt: nil,
		TotalRuns:       1,
		LastCompletedAt: timePtr(now),
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.GetState(ctx, "docs-bot-once")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if got.NextScheduledAt != nil {
		t.Errorf("NextScheduledAt should be nil for one-shot terminal; got %v", got.NextScheduledAt)
	}
	if got.CurrentState != StateCompleted {
		t.Errorf("CurrentState = %q, want %q", got.CurrentState, StateCompleted)
	}
}

// TestEventLog_AppendAndRead verifies append-only event INSERT and that
// RecentEvents returns rows DESC by event_time.
func TestEventLog_AppendAndRead(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()

	now := time.Unix(1747353600, 0)
	e1 := Event{
		JobID:     "docs-bot",
		RunID:     "docs-bot-g1-1747353600",
		EventTime: now,
		FromState: "",
		ToState:   StateDispatched,
		Reason:    "tick fired",
	}
	if err := store.AppendEvent(ctx, &e1); err != nil {
		t.Fatalf("append e1: %v", err)
	}

	e2 := Event{
		JobID:     "docs-bot",
		RunID:     "docs-bot-g1-1747353600",
		EventTime: now.Add(time.Second),
		FromState: StateDispatched,
		ToState:   StateRunning,
		Reason:    "handler invoked",
	}
	if err := store.AppendEvent(ctx, &e2); err != nil {
		t.Fatalf("append e2: %v", err)
	}

	events, err := store.RecentEvents(ctx, "docs-bot", 10)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	// RecentEvents returns DESC by event_time; newest first.
	if events[0].ToState != StateRunning {
		t.Errorf("events[0].ToState = %q, want %q", events[0].ToState, StateRunning)
	}
	if events[1].ToState != StateDispatched {
		t.Errorf("events[1].ToState = %q, want %q", events[1].ToState, StateDispatched)
	}
	// First event has empty FromState (NULL column); second has Dispatched.
	if events[1].FromState != "" {
		t.Errorf("events[1].FromState = %q, want empty", events[1].FromState)
	}
	if events[0].FromState != StateDispatched {
		t.Errorf("events[0].FromState = %q, want %q", events[0].FromState, StateDispatched)
	}
}

// TestEventLog_DetailsRoundTrip verifies the Details map marshals and
// unmarshals correctly through SQLite. The plan picks the structured payload
// from the B-B1 idle-nudge-exhaustion escalation as a realistic example.
func TestEventLog_DetailsRoundTrip(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()

	e := Event{
		JobID:     "docs-bot",
		RunID:     "docs-bot-g1-1747353600",
		EventTime: time.Unix(1747353600, 0),
		FromState: StateRunning,
		ToState:   StateFailed,
		Reason:    "idle nudge exhausted",
		Details: map[string]any{
			"escalation_emitted_by": "b-b1.idle_nudge",
			"max_idle_nudges":       5,
		},
	}
	if err := store.AppendEvent(ctx, &e); err != nil {
		t.Fatalf("append: %v", err)
	}

	events, err := store.RecentEvents(ctx, "docs-bot", 1)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Details["escalation_emitted_by"] != "b-b1.idle_nudge" {
		t.Errorf("details round-trip failed: %v", events[0].Details)
	}
	// JSON unmarshal turns integers into float64; assert via that type.
	if got, ok := events[0].Details["max_idle_nudges"].(float64); !ok || got != 5 {
		t.Errorf("max_idle_nudges = %v (type %T), want 5", events[0].Details["max_idle_nudges"], events[0].Details["max_idle_nudges"])
	}
}
