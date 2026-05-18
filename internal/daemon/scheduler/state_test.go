package scheduler

import (
	"context"
	"sync"
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

// TestStateStore_TickLoopPredicate exercises the reactor's canonical
// tick-loop query shape from spec §8.1.3:
//
//	WHERE next_scheduled_at IS NOT NULL AND next_scheduled_at <= ?
//
// Mixed NULL/non-NULL rows: NULL rows (one-shot terminal) must be
// excluded EVEN if the cutoff would otherwise match; past-due
// non-NULL rows match; future non-NULL rows don't. The idx_scheduler
// state_next index covers this query (canonical §3.2).
func TestStateStore_TickLoopPredicate(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()
	now := time.Unix(1747353600, 0)
	past := now.Add(-10 * time.Minute)
	future := now.Add(10 * time.Minute)

	rows := []*StateRow{
		// Past-due non-NULL: SHOULD match.
		{JobID: "due-1", Generation: 1, CurrentState: StateScheduled, NextScheduledAt: &past, CreatedAt: past, UpdatedAt: past},
		{JobID: "due-2", Generation: 1, CurrentState: StateScheduled, NextScheduledAt: &past, CreatedAt: past, UpdatedAt: past},
		// Future non-NULL: SHOULD NOT match.
		{JobID: "future-1", Generation: 1, CurrentState: StateScheduled, NextScheduledAt: &future, CreatedAt: now, UpdatedAt: now},
		// NULL (one-shot terminal): MUST be excluded regardless of cutoff.
		{JobID: "oneshot-done", Generation: 1, CurrentState: StateCompleted, NextScheduledAt: nil, TotalRuns: 1, LastCompletedAt: &past, CreatedAt: past, UpdatedAt: past},
		{JobID: "oneshot-failed", Generation: 1, CurrentState: StateFailed, NextScheduledAt: nil, TotalRuns: 1, LastCompletedAt: &past, CreatedAt: past, UpdatedAt: past},
	}
	for _, r := range rows {
		if err := store.UpsertState(ctx, r); err != nil {
			t.Fatalf("seed %s: %v", r.JobID, err)
		}
	}

	// Exercise the canonical predicate directly via the underlying
	// safedb handle (mirrors the shape the reactor would use).
	queryRows, err := store.DB().QueryContext(ctx,
		`SELECT job_id FROM scheduler_job_state
		 WHERE next_scheduled_at IS NOT NULL AND next_scheduled_at <= ?`,
		now.Unix())
	if err != nil {
		t.Fatalf("tick-loop predicate query: %v", err)
	}
	defer func() { _ = queryRows.Close() }()

	got := map[string]bool{}
	for queryRows.Next() {
		var id string
		if err := queryRows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[id] = true
	}
	if err := queryRows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}

	for _, want := range []string{"due-1", "due-2"} {
		if !got[want] {
			t.Errorf("missing due row %q from predicate match", want)
		}
	}
	for _, exclude := range []string{"future-1", "oneshot-done", "oneshot-failed"} {
		if got[exclude] {
			t.Errorf("predicate matched %q; should be excluded", exclude)
		}
	}
}

// TestStateStore_NonTerminalAtBoot verifies the reconciliation walker
// returns only scheduled / dispatched / running rows; terminal states
// (completed, failed) are excluded.
func TestStateStore_NonTerminalAtBoot(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()

	now := time.Unix(1747353600, 0)
	rows := []*StateRow{
		{JobID: "running-1", Generation: 1, CurrentState: StateRunning, NextScheduledAt: timePtr(now), CreatedAt: now, UpdatedAt: now},
		{JobID: "scheduled-1", Generation: 1, CurrentState: StateScheduled, NextScheduledAt: timePtr(now.Add(time.Minute)), CreatedAt: now, UpdatedAt: now},
		{JobID: "dispatched-1", Generation: 1, CurrentState: StateDispatched, NextScheduledAt: timePtr(now), CreatedAt: now, UpdatedAt: now},
		{JobID: "completed-1", Generation: 1, CurrentState: StateCompleted, NextScheduledAt: nil, CreatedAt: now, UpdatedAt: now},
		{JobID: "failed-1", Generation: 1, CurrentState: StateFailed, NextScheduledAt: timePtr(now.Add(time.Minute)), CreatedAt: now, UpdatedAt: now},
	}
	for _, r := range rows {
		if err := store.UpsertState(ctx, r); err != nil {
			t.Fatalf("upsert %s: %v", r.JobID, err)
		}
	}

	nonTerminal, err := store.NonTerminalAtBoot(ctx)
	if err != nil {
		t.Fatalf("non-terminal: %v", err)
	}
	got := make(map[string]bool)
	for _, r := range nonTerminal {
		got[r.JobID] = true
	}
	for _, want := range []string{"running-1", "scheduled-1", "dispatched-1"} {
		if !got[want] {
			t.Errorf("missing %q from non-terminal set", want)
		}
	}
	for _, dont := range []string{"completed-1", "failed-1"} {
		if got[dont] {
			t.Errorf("terminal job %q should NOT be in non-terminal set", dont)
		}
	}
	if len(nonTerminal) != 3 {
		t.Errorf("non-terminal count = %d, want 3", len(nonTerminal))
	}
}

// TestStateStore_ConcurrentUpdates_RaceClean: 10 goroutines × 100 iterations
// each performing UpsertState + GetState against the same row. SQLite under
// WAL serializes writes (busy_timeout=5s prevents SQLITE_BUSY cascades);
// this test pins that the Go layer doesn't add unsafe shared state on top.
// Must pass with -race.
func TestStateStore_ConcurrentUpdates_RaceClean(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()
	now := time.Unix(1747353600, 0)

	if err := store.UpsertState(ctx, &StateRow{
		JobID:           "race-job",
		Generation:      1,
		CurrentState:    StateScheduled,
		NextScheduledAt: timePtr(now),
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const goroutines = 10
	const iterations = 100

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				err := store.UpsertState(ctx, &StateRow{
					JobID:           "race-job",
					Generation:      1,
					CurrentState:    StateRunning,
					NextScheduledAt: timePtr(now),
					TotalRuns:       i*iterations + j,
					CreatedAt:       now,
					UpdatedAt:       now.Add(time.Duration(i*iterations+j) * time.Millisecond),
				})
				if err != nil {
					t.Errorf("upsert g=%d j=%d: %v", i, j, err)
					return
				}
				if _, err := store.GetState(ctx, "race-job"); err != nil {
					t.Errorf("get g=%d j=%d: %v", i, j, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	got, err := store.GetState(ctx, "race-job")
	if err != nil {
		t.Fatalf("final get: %v", err)
	}
	if got.CurrentState != StateRunning {
		t.Errorf("final state = %q, want %q", got.CurrentState, StateRunning)
	}
}

// TestEventsForRun_AscByEventTime verifies events for one run are returned
// oldest-first, scoped strictly to the requested run_id. E6.9's boot
// reconciler depends on ASC order to extract the final non-rollback
// worktree_path / branch_name / tmux_session_name from the journal.
func TestEventsForRun_AscByEventTime(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()

	t0 := time.Unix(1747353600, 0)
	mustAppend := func(e *Event) {
		t.Helper()
		if err := store.AppendEvent(ctx, e); err != nil {
			t.Fatalf("append %s: %v", e.Reason, err)
		}
	}
	mustAppend(&Event{JobID: "j1", RunID: "r1", EventTime: t0, ToState: StateRunning, Reason: "stage 1 dispatch"})
	mustAppend(&Event{JobID: "j1", RunID: "r1", EventTime: t0.Add(time.Second), FromState: StateRunning, ToState: StateRunning, Reason: "stage 3 complete",
		Details: map[string]any{"worktree_path": "/wt1", "branch_name": "agent/x/job-r1"}})
	mustAppend(&Event{JobID: "j1", RunID: "r1", EventTime: t0.Add(2 * time.Second), FromState: StateRunning, ToState: StateRunning, Reason: "stage 4 complete",
		Details: map[string]any{"tmux_session_name": "sess1"}})
	// Different run for the same job — must be excluded.
	mustAppend(&Event{JobID: "j1", RunID: "r2", EventTime: t0.Add(3 * time.Second), ToState: StateRunning, Reason: "other run"})

	got, err := store.EventsForRun(ctx, "r1")
	if err != nil {
		t.Fatalf("EventsForRun: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3 (r2 must be excluded)", len(got))
	}
	if got[0].Reason != "stage 1 dispatch" {
		t.Errorf("got[0].Reason = %q, want stage 1 dispatch (oldest first)", got[0].Reason)
	}
	if got[2].Reason != "stage 4 complete" {
		t.Errorf("got[2].Reason = %q, want stage 4 complete (newest last)", got[2].Reason)
	}
	if wp, ok := got[1].Details["worktree_path"].(string); !ok || wp != "/wt1" {
		t.Errorf("got[1].Details[worktree_path] = %v, want /wt1", got[1].Details["worktree_path"])
	}
	if bn, ok := got[1].Details["branch_name"].(string); !ok || bn != "agent/x/job-r1" {
		t.Errorf("got[1].Details[branch_name] = %v, want agent/x/job-r1", got[1].Details["branch_name"])
	}
	if ts, ok := got[2].Details["tmux_session_name"].(string); !ok || ts != "sess1" {
		t.Errorf("got[2].Details[tmux_session_name] = %v, want sess1", got[2].Details["tmux_session_name"])
	}
}

// TestEventsForRun_UnknownRun returns empty slice + nil error.
func TestEventsForRun_UnknownRun(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	got, err := store.EventsForRun(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("EventsForRun: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d events, want 0 for unknown run", len(got))
	}
}

// TestNonTerminalWorktrees_OnlyLiveRuns verifies the orphan-sweep
// cross-reference set: includes worktree_path values journaled under
// non-terminal rows' last_run_id; excludes terminal rows + rows with no
// worktree journaled + rows whose worktree_path was recorded under an
// earlier run_id.
func TestNonTerminalWorktrees_OnlyLiveRuns(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	ctx := context.Background()

	t0 := time.Unix(1747353600, 0)
	mustUpsert := func(r *StateRow) {
		t.Helper()
		if err := store.UpsertState(ctx, r); err != nil {
			t.Fatalf("upsert %s: %v", r.JobID, err)
		}
	}
	mustAppend := func(e *Event) {
		t.Helper()
		if err := store.AppendEvent(ctx, e); err != nil {
			t.Fatalf("append %s/%s: %v", e.JobID, e.RunID, err)
		}
	}

	// j1: running, worktree journaled — INCLUDED.
	mustUpsert(&StateRow{JobID: "j1", Generation: 1, CurrentState: StateRunning, LastRunID: "r1", CreatedAt: t0, UpdatedAt: t0})
	mustAppend(&Event{JobID: "j1", RunID: "r1", EventTime: t0, ToState: StateRunning, Reason: "stage 3",
		Details: map[string]any{"worktree_path": "/wt1"}})

	// j2: dispatched, worktree journaled — INCLUDED.
	mustUpsert(&StateRow{JobID: "j2", Generation: 1, CurrentState: StateDispatched, LastRunID: "r2", CreatedAt: t0, UpdatedAt: t0})
	mustAppend(&Event{JobID: "j2", RunID: "r2", EventTime: t0, ToState: StateRunning, Reason: "stage 3",
		Details: map[string]any{"worktree_path": "/wt2"}})

	// j3: scheduled, never ran — EXCLUDED (no events match last_run_id="").
	mustUpsert(&StateRow{JobID: "j3", Generation: 1, CurrentState: StateScheduled, LastRunID: "", CreatedAt: t0, UpdatedAt: t0})

	// j4: completed (terminal), has a worktree event — EXCLUDED.
	mustUpsert(&StateRow{JobID: "j4", Generation: 1, CurrentState: StateCompleted, LastRunID: "r4", CreatedAt: t0, UpdatedAt: t0})
	mustAppend(&Event{JobID: "j4", RunID: "r4", EventTime: t0, ToState: StateRunning, Reason: "stage 3",
		Details: map[string]any{"worktree_path": "/wt4"}})

	// j5: running, an earlier run had a worktree but last_run_id points at r5b
	// (r5b has no worktree journaled yet) — EXCLUDED for /wt5a, but /wt5b absent.
	mustUpsert(&StateRow{JobID: "j5", Generation: 2, CurrentState: StateRunning, LastRunID: "r5b", CreatedAt: t0, UpdatedAt: t0})
	mustAppend(&Event{JobID: "j5", RunID: "r5a", EventTime: t0.Add(-time.Hour), ToState: StateRunning, Reason: "old stage 3",
		Details: map[string]any{"worktree_path": "/wt5a"}})

	got, err := store.NonTerminalWorktrees(ctx)
	if err != nil {
		t.Fatalf("NonTerminalWorktrees: %v", err)
	}
	want := map[string]bool{"/wt1": true, "/wt2": true}
	if len(got) != len(want) {
		t.Errorf("got %d paths, want %d (%v vs %v)", len(got), len(want), got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing expected path %q", k)
		}
	}
	if got["/wt4"] {
		t.Errorf("/wt4 leaked despite j4 being terminal")
	}
	if got["/wt5a"] {
		t.Errorf("/wt5a leaked despite belonging to earlier run r5a")
	}
}

// TestNonTerminalWorktrees_Empty returns empty map + nil error.
func TestNonTerminalWorktrees_Empty(t *testing.T) {
	store := NewStateStore(setupStateTestDB(t))
	got, err := store.NonTerminalWorktrees(context.Background())
	if err != nil {
		t.Fatalf("NonTerminalWorktrees: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d paths, want 0 (empty DB)", len(got))
	}
}
