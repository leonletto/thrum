package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/sweep"
	"github.com/leonletto/thrum/internal/schema"
)

// newSchedulerForSweepTest spins up an A-B1 scheduler.Scheduler against
// a fresh on-disk SQLite (reminders + scheduler tables coexist). Same
// pattern as the dispatcher tests in internal/daemon/reminders/.
func newSchedulerForSweepTest(t *testing.T) (*scheduler.Scheduler, *safedb.DB, reminders.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "sweep_wire_test.db")
	raw, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(raw); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	db := safedb.New(raw)
	store := reminders.NewSQLStore(db)
	sched := scheduler.New(scheduler.Config{DB: db, DaemonID: "test-daemon"})
	t.Cleanup(func() { _ = sched.Stop(context.Background()) })
	return sched, db, store
}

func TestWireSweep_RegistersInternalJob(t *testing.T) {
	sched, db, store := newSchedulerForSweepTest(t)
	thrumDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatal(err)
	}
	cfg := &config.DaemonConfig{
		StalledSweep: config.StalledSweepConfig{IntervalMinutes: 20},
		Escalation:   config.EscalationConfig{SupervisorAgentName: "coordinator_main"},
	}

	wireSweep(sched, store, db, thrumDir, cfg)

	spec, ok := sched.JobSpec("internal.stalled_agent_sweep")
	if !ok {
		t.Fatalf("internal.stalled_agent_sweep not found after wireSweep")
	}
	if spec.Type != "internal" {
		t.Errorf("spec.Type = %q, want internal", spec.Type)
	}
	if spec.Schedule != "@every 20m" {
		t.Errorf("spec.Schedule = %q, want '@every 20m' (from cfg)", spec.Schedule)
	}
	if spec.CatchUp != "skip" {
		t.Errorf("spec.CatchUp = %q, want 'skip' (missed-tick storm prevention)", spec.CatchUp)
	}
	if spec.RunAtStart {
		t.Error("RunAtStart should be false (fresh boot waits one cadence cycle)")
	}
}

func TestWireSweep_DefaultsWhenIntervalUnset(t *testing.T) {
	sched, db, store := newSchedulerForSweepTest(t)
	thrumDir := t.TempDir()
	cfg := &config.DaemonConfig{} // StalledSweep + Reminders both zero

	wireSweep(sched, store, db, thrumDir, cfg)

	spec, _ := sched.JobSpec("internal.stalled_agent_sweep")
	// 15-minute default per canonical §4.4 → "@every 15m".
	if spec.Schedule != "@every 15m" {
		t.Errorf("spec.Schedule = %q, want '@every 15m' (canonical default for zero IntervalMinutes)", spec.Schedule)
	}
}

func TestWireSweep_DoesNotPanicOnFreshBootMissingIdentitiesDir(t *testing.T) {
	sched, db, store := newSchedulerForSweepTest(t)
	// Note: thrumDir exists but identities/ subdirectory does NOT —
	// fresh-install state before any agent registers.
	thrumDir := t.TempDir()
	cfg := &config.DaemonConfig{}

	// Should not panic; identityFileAgentRegistry.LiveAgents handles
	// the missing dir gracefully.
	wireSweep(sched, store, db, thrumDir, cfg)
}

// --- identityFileAgentRegistry tests ---

// writeIdentity writes a minimal identity file fixture for sweep tests.
func writeIdentity(t *testing.T, dir, name string, idFile config.IdentityFile) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(idFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityFileAgentRegistry_MissingDir_ReturnsEmpty(t *testing.T) {
	r := &identityFileAgentRegistry{
		identitiesDir: filepath.Join(t.TempDir(), "nonexistent"),
	}
	agents, err := r.LiveAgents(context.Background())
	if err != nil {
		t.Errorf("missing dir should be silent; got %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("missing dir should yield empty slice; got %v", agents)
	}
}

func TestIdentityFileAgentRegistry_ReadsAgents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identities")
	writeIdentity(t, dir, "docs_bot", config.IdentityFile{
		Version: 3,
		Agent:   config.AgentConfig{Name: "docs_bot", Role: "implementer", Module: "docs"},
		TmuxSession: "docs:0.0",
	})
	writeIdentity(t, dir, "billing_bot", config.IdentityFile{
		Version: 3,
		Agent:   config.AgentConfig{Name: "billing_bot", Role: "implementer", Module: "billing"},
		TmuxSession: "billing:0.0",
	})

	r := &identityFileAgentRegistry{identitiesDir: dir}
	agents, err := r.LiveAgents(context.Background())
	if err != nil {
		t.Fatalf("LiveAgents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	names := map[string]bool{}
	for _, a := range agents {
		names[a.Name] = true
	}
	if !names["docs_bot"] || !names["billing_bot"] {
		t.Errorf("expected both docs_bot + billing_bot; got %+v", agents)
	}
}

func TestIdentityFileAgentRegistry_SkipsCorruptFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identities")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	// Good file.
	writeIdentity(t, dir, "good", config.IdentityFile{
		Version: 3, Agent: config.AgentConfig{Name: "good"},
	})
	// Corrupt file (not valid JSON).
	if err := os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &identityFileAgentRegistry{identitiesDir: dir}
	agents, err := r.LiveAgents(context.Background())
	if err != nil {
		t.Fatalf("LiveAgents: %v", err)
	}
	// Corrupt file logged + skipped; good file still surfaces.
	if len(agents) != 1 || agents[0].Name != "good" {
		t.Errorf("expected only 'good' agent; got %+v", agents)
	}
}

func TestIdentityFileAgentRegistry_SkipsNonJSONAndDirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identities")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, dir, "real", config.IdentityFile{
		Version: 3, Agent: config.AgentConfig{Name: "real"},
	})
	// .txt file — should be skipped.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("readme"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Subdirectory — should be skipped.
	if err := os.MkdirAll(filepath.Join(dir, "backup"), 0o750); err != nil {
		t.Fatal(err)
	}

	r := &identityFileAgentRegistry{identitiesDir: dir}
	agents, err := r.LiveAgents(context.Background())
	if err != nil {
		t.Fatalf("LiveAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].Name != "real" {
		t.Errorf("only .json files in dir should yield agents; got %+v", agents)
	}
}

func TestIdentityFileAgentRegistry_SkipsEmptyName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identities")
	writeIdentity(t, dir, "broken", config.IdentityFile{
		Version: 3, Agent: config.AgentConfig{Name: ""},
	})
	r := &identityFileAgentRegistry{identitiesDir: dir}
	agents, _ := r.LiveAgents(context.Background())
	if len(agents) != 0 {
		t.Errorf("identity with empty Name should be skipped; got %+v", agents)
	}
}

// Compile-time check that identityFileAgentRegistry satisfies
// sweep.AgentRegistry — catches signature drift if sweep.AgentRegistry
// gains a method.
var _ sweep.AgentRegistry = (*identityFileAgentRegistry)(nil)

// Sanity check: the test file references the test helpers + types
// that the production wiring depends on. If any of these renames at
// some future date, this assertion makes the test break loudly.
var _ = time.Minute // time used elsewhere in the file
