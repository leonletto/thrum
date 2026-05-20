package state_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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

// TestAgentLifecycleStore_ListByAgents_BulkLookup pins the I1 fold-in
// from E6.8 batch-1 review: the bulk ListByAgents method runs one
// SQL round-trip across N agents (rather than N per-agent
// ListByAgent calls inside a team.list loop). Asserts:
//   - Results keyed by agent_name.
//   - Per-agent ordering most-recent first.
//   - Per-agent limit applied via the windowed ROW_NUMBER() partition.
//   - Agents without events are absent from the map.
//   - Agents not in the request list are absent from the map.
//   - Empty input → empty map + nil error (no SQL fires).
func TestAgentLifecycleStore_ListByAgents_BulkLookup(t *testing.T) {
	s := state.NewAgentLifecycleStore(newLifecycleStoreDB(t))
	ctx := context.Background()
	now := time.Now()

	// Seed: docs_bot has 3 events; ops_bot has 1; unknown_bot has 0.
	// no_match_bot has events but won't be in the request.
	for i, kind := range []state.AgentLifecycleEventKind{
		state.EventCrashDetected, state.EventRespawnFired, state.EventRespawnSkippedLoopguard,
	} {
		if _, err := s.Append(ctx, state.AgentLifecycleEvent{
			AgentName: "docs_bot",
			EventKind: kind,
			EventTime: now.Add(time.Duration(-i) * time.Minute),
			Reason:    fmt.Sprintf("docs_bot event #%d", i),
		}); err != nil {
			t.Fatalf("seed docs_bot: %v", err)
		}
	}
	if _, err := s.Append(ctx, state.AgentLifecycleEvent{
		AgentName: "ops_bot", EventKind: state.EventCrashDetected,
		EventTime: now, Reason: "ops crash",
	}); err != nil {
		t.Fatalf("seed ops_bot: %v", err)
	}
	if _, err := s.Append(ctx, state.AgentLifecycleEvent{
		AgentName: "no_match_bot", EventKind: state.EventCrashDetected,
		EventTime: now, Reason: "out-of-scope",
	}); err != nil {
		t.Fatalf("seed no_match_bot: %v", err)
	}

	out, err := s.ListByAgents(ctx, []string{"docs_bot", "ops_bot", "unknown_bot"}, 2)
	if err != nil {
		t.Fatalf("ListByAgents: %v", err)
	}

	// docs_bot has 3 events; limit=2 windows to top-2 per partition.
	if got := len(out["docs_bot"]); got != 2 {
		t.Errorf("docs_bot count = %d; want 2 (limit applied per partition)", got)
	}
	// Most-recent first: docs_bot event #0 (now) should precede event #1 (now-1min).
	if len(out["docs_bot"]) >= 2 {
		if !out["docs_bot"][0].EventTime.After(out["docs_bot"][1].EventTime) &&
			!out["docs_bot"][0].EventTime.Equal(out["docs_bot"][1].EventTime) {
			t.Errorf("docs_bot ordering not DESC: [0]=%v [1]=%v",
				out["docs_bot"][0].EventTime, out["docs_bot"][1].EventTime)
		}
	}
	if got := len(out["ops_bot"]); got != 1 {
		t.Errorf("ops_bot count = %d; want 1", got)
	}
	if _, present := out["unknown_bot"]; present {
		t.Errorf("unknown_bot (zero events) should be ABSENT from the map; map = %v",
			mapKeys(out))
	}
	if _, present := out["no_match_bot"]; present {
		t.Errorf("no_match_bot (not requested) leaked into result; map = %v",
			mapKeys(out))
	}
}

// TestAgentLifecycleStore_ListByAgents_EmptyInput_NoQuery pins the
// fast-path: an empty agentNames slice short-circuits without
// firing SQL (would otherwise hit a syntax error on an empty IN
// clause).
func TestAgentLifecycleStore_ListByAgents_EmptyInput_NoQuery(t *testing.T) {
	s := state.NewAgentLifecycleStore(newLifecycleStoreDB(t))
	out, err := s.ListByAgents(context.Background(), nil, 5)
	if err != nil {
		t.Fatalf("empty input ListByAgents: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("empty input should return empty map; got %v", out)
	}
}

// mapKeys returns sorted keys for stable error output.
func mapKeys(m map[string][]state.AgentLifecycleEvent) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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

// TestAgentLifecycleStore_AppendRejectsInvalidDetectionMethod pins
// brainstormer-third B1: Append() validates detection_method at the
// Go layer (defense-in-depth on top of the SQL CHECK constraint).
// The SQL CHECK is the durable guard, but a Go-layer rejection gives
// callers a clean error message and avoids burning a DB round-trip.
func TestAgentLifecycleStore_AppendRejectsInvalidDetectionMethod(t *testing.T) {
	s := state.NewAgentLifecycleStore(newLifecycleStoreDB(t))
	ctx := context.Background()

	_, err := s.Append(ctx, state.AgentLifecycleEvent{
		AgentName:       "victim",
		EventKind:       state.EventCrashDetected,
		EventTime:       time.Now(),
		DetectionMethod: "totally_made_up",
	})
	if err == nil {
		t.Fatal("expected error for invalid detection_method, got nil")
	}
	// Error must name the offending field so operators can self-correct
	// without grepping the codebase.
	if !strings.Contains(err.Error(), "detection_method") {
		t.Errorf("err = %q; want substring 'detection_method'", err.Error())
	}

	// Verify no row was persisted (Go-layer rejection short-circuits
	// before the INSERT, so the failed attempt leaves no audit trail).
	events, err := s.ListByAgent(ctx, "victim", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("invalid append leaked a row: %d events", len(events))
	}

	// Canonical values + empty (→ NULL) must still pass.
	for _, dm := range []state.DetectionMethod{
		"",
		state.DetectionHealthCheckTick,
		state.DetectionRestartReconciliation,
		state.DetectionRPCObservation,
	} {
		_, err := s.Append(ctx, state.AgentLifecycleEvent{
			AgentName:       "victim",
			EventKind:       state.EventCrashDetected,
			EventTime:       time.Now(),
			DetectionMethod: dm,
		})
		if err != nil {
			t.Errorf("canonical detection_method %q rejected: %v", dm, err)
		}
	}
}

// TestAgentLifecycleStore_ConcurrentAppendWriters pins brainstormer-
// third B2 + bd AC 9.1.7: the Store is safe to call from multiple
// goroutines simultaneously. Spawns 10 writers each appending 5 events
// against the same agent_name; verifies the run is race-detector clean
// and all 50 rows land without loss.
//
// SQLite serializes writes per connection; safedb provides the
// connection. This test pins both the SQLite-level safety and the
// absence of Go-level data races on shared fields.
func TestAgentLifecycleStore_ConcurrentAppendWriters(t *testing.T) {
	s := state.NewAgentLifecycleStore(newLifecycleStoreDB(t))
	ctx := context.Background()
	now := time.Now()

	const writers = 10
	const perWriter = 5

	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := s.Append(ctx, state.AgentLifecycleEvent{
					AgentName: "shared_agent",
					EventKind: state.EventRespawnFired,
					EventTime: now.Add(time.Duration(writer*perWriter+i) * time.Second),
					Reason:    fmt.Sprintf("writer=%d i=%d", writer, i),
				}); err != nil {
					t.Errorf("writer %d append %d: %v", writer, i, err)
				}
			}
		}(w)
	}
	wg.Wait()

	events, err := s.ListByAgent(ctx, "shared_agent", 1000)
	if err != nil {
		t.Fatalf("list after concurrent writes: %v", err)
	}
	if got := len(events); got != writers*perWriter {
		t.Errorf("concurrent writers persisted %d events; want %d", got, writers*perWriter)
	}
}

// TestAgentLifecycleStore_PruneOlderThan_BoundaryEventSurvives pins
// brainstormer-third I3 strict-less-than semantics: PruneOlderThan
// deletes rows with event_time < cutoff. An event at event_time ==
// cutoff is OUTSIDE the deletion range and MUST survive. Critical for
// the cleanup-handler contract: a daily prune that runs at exactly
// 7 days * 24 hours after an event must keep that event, not delete it.
func TestAgentLifecycleStore_PruneOlderThan_BoundaryEventSurvives(t *testing.T) {
	s := state.NewAgentLifecycleStore(newLifecycleStoreDB(t))
	ctx := context.Background()

	// Pick a fixed cutoff (truncated to seconds since SQLite stores
	// unix-seconds) and seed exactly one event AT the boundary.
	cutoff := time.Now().Add(-7 * 24 * time.Hour).Truncate(time.Second)
	if _, err := s.Append(ctx, state.AgentLifecycleEvent{
		AgentName: "boundary",
		EventKind: state.EventRespawnFired,
		EventTime: cutoff, // exactly at the cutoff
	}); err != nil {
		t.Fatalf("append boundary event: %v", err)
	}
	// Plus a clearly-older event (one second BEFORE cutoff) that
	// must be pruned, to prove the predicate actually fires.
	if _, err := s.Append(ctx, state.AgentLifecycleEvent{
		AgentName: "boundary",
		EventKind: state.EventRespawnFired,
		EventTime: cutoff.Add(-1 * time.Second),
	}); err != nil {
		t.Fatalf("append older event: %v", err)
	}

	rows, err := s.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if rows != 1 {
		t.Errorf("expected to prune 1 row (the older one); pruned %d", rows)
	}
	remaining, err := s.ListByAgent(ctx, "boundary", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("boundary event was deleted; remaining=%d", len(remaining))
	}
	if !remaining[0].EventTime.Equal(cutoff.UTC()) {
		t.Errorf("surviving event time = %v; want %v", remaining[0].EventTime, cutoff.UTC())
	}
}

// TestAgentLifecycleStore_AppendRejectsInvalidEventKind pins thrum-6qmf.4.91:
// Append() validates event_kind at the Go layer (defense-in-depth). The
// SQL CHECK constraint guards only detection_method, so the Go-layer
// allowlist is the only enforcement point for event_kind. Without this,
// paraphrasing drift would silently land bad kinds in the DB and
// render as unknown rows in `thrum team --journal`.
//
// Mirrors TestAgentLifecycleStore_AppendRejectsInvalidDetectionMethod
// in shape: reject + verify no row leaks + canonical-kinds-loop passes.
func TestAgentLifecycleStore_AppendRejectsInvalidEventKind(t *testing.T) {
	s := state.NewAgentLifecycleStore(newLifecycleStoreDB(t))
	ctx := context.Background()

	_, err := s.Append(ctx, state.AgentLifecycleEvent{
		AgentName: "victim",
		EventKind: "totally_made_up",
		EventTime: time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for invalid event_kind, got nil")
	}
	if !strings.Contains(err.Error(), "event_kind") {
		t.Errorf("err = %q; want substring 'event_kind'", err.Error())
	}

	// Verify no row was persisted (Go-layer rejection short-circuits
	// before the INSERT, so the failed attempt leaves no audit trail).
	events, err := s.ListByAgent(ctx, "victim", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("invalid append leaked a row: %d events", len(events))
	}

	// All canonical event kinds must pass. detection_method left empty
	// so detection_method validation doesn't shadow event_kind validation.
	for _, ek := range []state.AgentLifecycleEventKind{
		state.EventRespawnFired,
		state.EventRespawnSkippedLoopguard,
		state.EventCrashDetected,
		state.EventStateMdParseFailed,
		state.EventStateMdAckCleared,
		state.EventRespawnAckCleared,
		state.EventReconcileWorktreeDiscrepancy,
	} {
		_, err := s.Append(ctx, state.AgentLifecycleEvent{
			AgentName: "victim",
			EventKind: ek,
			EventTime: time.Now(),
		})
		if err != nil {
			t.Errorf("canonical event_kind %q rejected: %v", ek, err)
		}
	}
}
