package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/schema"
)

// newSchedulerForEmailWireTest spins up an A-B1 scheduler against a fresh
// on-disk SQLite. Mirrors newSchedulerForSweepTest minus the reminders
// dependency.
func newSchedulerForEmailWireTest(t *testing.T) *scheduler.Scheduler {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "email_wire_test.db")
	raw, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(raw); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	db := safedb.New(raw)
	sched := scheduler.New(scheduler.Config{DB: db, DaemonID: "test-daemon"})
	t.Cleanup(func() { _ = sched.Stop(context.Background()) })
	return sched
}

func newTestEmailBridge() *email.Bridge {
	return email.New(config.EmailConfig{DaemonHandle: "wiretest"}, nil, "8080")
}

// TestWireEmailInternal_RegistersThreeJobs: the three substrate jobs
// land with the canonical IDs, types, and skip-storm CatchUp policy.
// Schedule strings reflect the configured cadence.
func TestWireEmailInternal_RegistersThreeJobs(t *testing.T) {
	sched := newSchedulerForEmailWireTest(t)
	cfg := config.EmailConfig{
		DaemonHandle:        "wiretest",
		PollIntervalSeconds: 90,
		Queue:               config.EmailQueue{PollIntervalSeconds: 7},
	}

	wireEmailInternal(sched, newTestEmailBridge(), cfg)

	cases := []struct {
		id       string
		schedule string
	}{
		{"internal.email_poll", "@every 90s"},
		{"internal.email_dedup_cleanup", "@daily"},
		{"internal.email_queue_drain", "@every 7s"},
	}
	for _, c := range cases {
		spec, ok := sched.JobSpec(c.id)
		if !ok {
			t.Errorf("%s not registered", c.id)
			continue
		}
		if spec.Type != "internal" {
			t.Errorf("%s spec.Type = %q, want internal", c.id, spec.Type)
		}
		if spec.Schedule != c.schedule {
			t.Errorf("%s spec.Schedule = %q, want %q", c.id, spec.Schedule, c.schedule)
		}
		if spec.CatchUp != "skip" {
			t.Errorf("%s spec.CatchUp = %q, want 'skip' (missed-tick storm prevention)", c.id, spec.CatchUp)
		}
		if spec.RunAtStart {
			t.Errorf("%s RunAtStart = true; want false (let the configured cadence cover the first tick)", c.id)
		}
	}
}

// TestWireEmailInternal_DefaultsWhenUnset: zero cadence configs fall
// back to documented defaults (60s poll, 5s queue drain).
func TestWireEmailInternal_DefaultsWhenUnset(t *testing.T) {
	sched := newSchedulerForEmailWireTest(t)
	cfg := config.EmailConfig{DaemonHandle: "wiretest"} // both cadences zero

	wireEmailInternal(sched, newTestEmailBridge(), cfg)

	poll, _ := sched.JobSpec("internal.email_poll")
	if poll.Schedule != "@every 60s" {
		t.Errorf("internal.email_poll schedule = %q, want '@every 60s' (default)", poll.Schedule)
	}
	queue, _ := sched.JobSpec("internal.email_queue_drain")
	if queue.Schedule != "@every 5s" {
		t.Errorf("internal.email_queue_drain schedule = %q, want '@every 5s' (default)", queue.Schedule)
	}
	dedup, _ := sched.JobSpec("internal.email_dedup_cleanup")
	if dedup.Schedule != "@daily" {
		t.Errorf("internal.email_dedup_cleanup schedule = %q, want '@daily'", dedup.Schedule)
	}
}

// TestWireEmailInternal_HandlerShapeMatches: the registered handlers
// satisfy scheduler.Handler — RegisterInternal panics if a handler is
// missing a method, but the test makes the contract explicit.
func TestWireEmailInternal_HandlerShapeMatches(t *testing.T) {
	var _ scheduler.Handler = (*email.PollHandler)(nil)
	var _ scheduler.Handler = (*email.DedupCleanupHandler)(nil)
	var _ scheduler.Handler = (*email.QueueDrainHandler)(nil)
}
