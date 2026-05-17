package state_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/schema"
)

// newLifecycleStoreDB opens an in-memory SQLite DB, runs schema migrations
// to head, and wraps it with *safedb.DB — the canonical test pattern
// established by internal/daemon/scheduler/state_test.go's
// setupStateTestDB.
func newLifecycleStoreDB(t *testing.T) *safedb.DB {
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

func TestAgentLifecycleStore_AppendAndList(t *testing.T) {
	s := state.NewAgentLifecycleStore(newLifecycleStoreDB(t))
	ctx := context.Background()
	now := time.Now()

	id, err := s.Append(ctx, state.AgentLifecycleEvent{
		AgentName: "docs_bot",
		EventKind: state.EventRespawnFired,
		EventTime: now,
		Reason:    "test fire",
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive AUTOINCREMENT id, got %d", id)
	}

	events, err := s.ListByAgent(ctx, "docs_bot", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventKind != state.EventRespawnFired {
		t.Errorf("event_kind = %q, want %q", events[0].EventKind, state.EventRespawnFired)
	}
	if events[0].Reason != "test fire" {
		t.Errorf("reason = %q, want %q", events[0].Reason, "test fire")
	}
	if events[0].DetectionMethod != "" {
		t.Errorf("detection_method = %q, want empty", events[0].DetectionMethod)
	}
}

// TestAgentLifecycleStore_LoopGuardCount pins the 3-in-window predicate
// canonical-ref §3.4 + spec §B-B1-Q10 use to decide whether to refuse
// the next auto-respawn. The query window is the half-open interval
// (now-windowSeconds, now] — events at the boundaries are precisely
// inside/outside per the >/<= operators.
func TestAgentLifecycleStore_LoopGuardCount(t *testing.T) {
	s := state.NewAgentLifecycleStore(newLifecycleStoreDB(t))
	ctx := context.Background()
	now := time.Now()

	// 4 respawn_fired events: 2 inside the 600s window, 2 outside.
	for _, dt := range []time.Duration{
		-100 * time.Second,
		-200 * time.Second,
		-700 * time.Second,
		-800 * time.Second,
	} {
		_, err := s.Append(ctx, state.AgentLifecycleEvent{
			AgentName: "flaky_agent",
			EventKind: state.EventRespawnFired,
			EventTime: now.Add(dt),
		})
		if err != nil {
			t.Fatalf("append at dt=%v: %v", dt, err)
		}
	}

	count, err := s.LoopGuardCount(ctx, "flaky_agent", state.EventRespawnFired, 600)
	if err != nil {
		t.Fatalf("loop guard: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 in window, got %d", count)
	}

	// Different kind in the same window: zero hits.
	zero, err := s.LoopGuardCount(ctx, "flaky_agent", state.EventCrashDetected, 600)
	if err != nil {
		t.Fatalf("loop guard for crash: %v", err)
	}
	if zero != 0 {
		t.Errorf("expected 0 crash_detected in window, got %d", zero)
	}
}

// TestAgentLifecycleStore_AppendIsNotIdempotent pins the AUTOINCREMENT
// contract: writes are intentionally NOT semantically deduped at the
// store layer — every successful Append produces a fresh id. Callers
// who need dedup enforce it at the event-source layer.
func TestAgentLifecycleStore_AppendIsNotIdempotent(t *testing.T) {
	s := state.NewAgentLifecycleStore(newLifecycleStoreDB(t))
	ctx := context.Background()

	e := state.AgentLifecycleEvent{
		AgentName:       "x",
		EventKind:       state.EventCrashDetected,
		EventTime:       time.Now(),
		DetectionMethod: state.DetectionHealthCheckTick,
	}
	id1, err := s.Append(ctx, e)
	if err != nil {
		t.Fatalf("append 1: %v", err)
	}
	id2, err := s.Append(ctx, e)
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if id1 == id2 {
		t.Errorf("expected distinct AUTOINCREMENT ids, got duplicate %d", id1)
	}
}

// TestAgentLifecycleStore_RoundTripsAllFields pins per-column persistence:
// every non-zero field set on AgentLifecycleEvent must survive a write
// and a read. Catches issues like Details being stored but not decoded,
// or DetectionMethod losing its empty-string→NULL mapping.
func TestAgentLifecycleStore_RoundTripsAllFields(t *testing.T) {
	s := state.NewAgentLifecycleStore(newLifecycleStoreDB(t))
	ctx := context.Background()
	now := time.Now().Truncate(time.Second) // SQLite stores seconds

	in := state.AgentLifecycleEvent{
		AgentName:       "round_trip_agent",
		EventKind:       state.EventCrashDetected,
		EventTime:       now,
		DetectionMethod: state.DetectionRestartReconciliation,
		Reason:          "pane gone on boot reconcile",
		Details:         json.RawMessage(`{"tmux_session":"impl_x","exit_code":137}`),
	}
	_, err := s.Append(ctx, in)
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	events, err := s.ListByAgent(ctx, "round_trip_agent", 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	out := events[0]
	if out.AgentName != in.AgentName {
		t.Errorf("AgentName: got %q, want %q", out.AgentName, in.AgentName)
	}
	if out.EventKind != in.EventKind {
		t.Errorf("EventKind: got %q, want %q", out.EventKind, in.EventKind)
	}
	if !out.EventTime.Equal(now.UTC()) {
		t.Errorf("EventTime: got %v, want %v", out.EventTime, now.UTC())
	}
	if out.DetectionMethod != in.DetectionMethod {
		t.Errorf("DetectionMethod: got %q, want %q", out.DetectionMethod, in.DetectionMethod)
	}
	if out.Reason != in.Reason {
		t.Errorf("Reason: got %q, want %q", out.Reason, in.Reason)
	}
	// Compare via decoded JSON to avoid whitespace/key-order sensitivity.
	var inMap, outMap map[string]any
	if err := json.Unmarshal(in.Details, &inMap); err != nil {
		t.Fatalf("unmarshal in.Details: %v", err)
	}
	if err := json.Unmarshal(out.Details, &outMap); err != nil {
		t.Fatalf("unmarshal out.Details: %v", err)
	}
	if inMap["tmux_session"] != outMap["tmux_session"] {
		t.Errorf("Details.tmux_session: got %v, want %v", outMap["tmux_session"], inMap["tmux_session"])
	}
}

// TestAgentLifecycleStore_PruneOlderThan pins the cleanup-handler
// contract: events with event_time < cutoff are deleted, events at or
// after cutoff survive. Used by internal.agent_lifecycle_cleanup
// (B-B1 Task 4) for daily retention housekeeping.
func TestAgentLifecycleStore_PruneOlderThan(t *testing.T) {
	s := state.NewAgentLifecycleStore(newLifecycleStoreDB(t))
	ctx := context.Background()
	now := time.Now()

	// 3 old (10d ago) + 2 fresh (1h ago).
	for i := 0; i < 3; i++ {
		_, err := s.Append(ctx, state.AgentLifecycleEvent{
			AgentName: "victim",
			EventKind: state.EventCrashDetected,
			EventTime: now.Add(-10 * 24 * time.Hour),
		})
		if err != nil {
			t.Fatalf("append old #%d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		_, err := s.Append(ctx, state.AgentLifecycleEvent{
			AgentName: "victim",
			EventKind: state.EventRespawnFired,
			EventTime: now.Add(-1 * time.Hour),
		})
		if err != nil {
			t.Fatalf("append fresh #%d: %v", i, err)
		}
	}

	rows, err := s.PruneOlderThan(ctx, now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if rows != 3 {
		t.Errorf("expected to prune 3 rows; pruned %d", rows)
	}

	remaining, err := s.ListByAgent(ctx, "victim", 100)
	if err != nil {
		t.Fatalf("list after prune: %v", err)
	}
	if len(remaining) != 2 {
		t.Errorf("expected 2 surviving rows; got %d", len(remaining))
	}
	for _, ev := range remaining {
		if ev.EventKind == state.EventCrashDetected {
			t.Errorf("old crash event survived prune: %v", ev)
		}
	}
}
