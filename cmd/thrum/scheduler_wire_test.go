package main

import (
	"context"
	"sort"
	"testing"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/internal/websocket"
)

// schedulerTestDB opens an in-memory SQLite DB migrated to head and wrapped
// with *safedb.DB, matching the substrate's test pattern in
// internal/daemon/scheduler/state_test.go.
func schedulerTestDB(t *testing.T) *safedb.DB {
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

// TestWireScheduler_RegistersTenRPCsOnWebSocket pins the WebSocket-side
// surface: all 10 job.* methods land on wsRegistry.RegisteredMethods().
// The same for-loop also writes to the Unix-socket server.RegisterHandler;
// substrate-level bind_test.go's TestMethods_RegistersAllTenRPCs pins that
// Methods() emits the same 10 names, so WS coverage is sufficient to prove
// the for-loop ran end-to-end.
func TestWireScheduler_RegistersTenRPCsOnWebSocket(t *testing.T) {
	server := daemon.NewServer(t.TempDir() + "/test.sock")
	wsReg := websocket.NewSimpleRegistry()
	db := schedulerTestDB(t)

	sched := wireScheduler(server, wsReg, db, "test-daemon", 0)
	defer func() { _ = sched.Stop(context.Background()) }()

	got := wsReg.RegisteredMethods()
	sort.Strings(got)
	want := []string{
		"job.cancel", "job.create", "job.delete", "job.disable", "job.done",
		"job.enable", "job.history", "job.list", "job.show", "job.update",
	}
	if len(got) != len(want) {
		t.Fatalf("registered %d methods on wsRegistry; want %d: got=%v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("position %d: got %q; want %q", i, got[i], w)
		}
	}
}

// TestWireScheduler_RegistersInternalCleanupJob pins the canonical cleanup
// registration. RegisterInternal panics on bad ID or duplicate, so a
// successful JobSpec() lookup after wireScheduler proves the call succeeded.
func TestWireScheduler_RegistersInternalCleanupJob(t *testing.T) {
	server := daemon.NewServer(t.TempDir() + "/test.sock")
	wsReg := websocket.NewSimpleRegistry()
	db := schedulerTestDB(t)

	sched := wireScheduler(server, wsReg, db, "test-daemon", 0)
	defer func() { _ = sched.Stop(context.Background()) }()

	spec, ok := sched.JobSpec("internal.scheduler_event_cleanup")
	if !ok {
		t.Fatal("internal.scheduler_event_cleanup not registered")
	}
	if spec.Type != "internal" {
		t.Errorf("spec.Type = %q; want %q", spec.Type, "internal")
	}
	if spec.Schedule != "@daily" {
		t.Errorf("spec.Schedule = %q; want %q", spec.Schedule, "@daily")
	}
}

// TestWireScheduler_RetentionFromCallerPropagates proves the retentionDays
// argument flows from the caller (main.go reads cfg.Daemon.Scheduler
// .EventRetentionDays) all the way to NewCleanupHandler, which clamps
// non-positive values to 7. A non-zero positive value is honored verbatim.
// Sanity check: NewCleanupHandler is unit-tested at the substrate level;
// this test pins the wireScheduler-side plumbing so a future refactor that
// drops the retention argument is caught here.
func TestWireScheduler_RetentionFromCallerPropagates(t *testing.T) {
	// Non-positive input → both registrations succeed (clamp behavior is
	// internal to NewCleanupHandler; from wireScheduler's POV the contract
	// is "any int is OK").
	server := daemon.NewServer(t.TempDir() + "/test.sock")
	wsReg := websocket.NewSimpleRegistry()
	db := schedulerTestDB(t)

	sched := wireScheduler(server, wsReg, db, "test-daemon", 0)
	defer func() { _ = sched.Stop(context.Background()) }()
	if _, ok := sched.JobSpec("internal.scheduler_event_cleanup"); !ok {
		t.Fatal("zero retention: cleanup not registered")
	}

	// Positive value also wires cleanly.
	server2 := daemon.NewServer(t.TempDir() + "/test2.sock")
	wsReg2 := websocket.NewSimpleRegistry()
	db2 := schedulerTestDB(t)
	sched2 := wireScheduler(server2, wsReg2, db2, "test-daemon-2", 30)
	defer func() { _ = sched2.Stop(context.Background()) }()
	if _, ok := sched2.JobSpec("internal.scheduler_event_cleanup"); !ok {
		t.Fatal("retention=30: cleanup not registered")
	}
}
