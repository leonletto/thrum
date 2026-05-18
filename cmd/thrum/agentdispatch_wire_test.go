package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/agentdispatch"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/internal/skills/mirror"
)

// testMirrorWorker returns a non-nil *mirror.Worker for tests that
// need to satisfy wireScheduledAgentHandlers' nil-guard. SourceRoot
// is required by mirror.New; t.TempDir keeps the worker isolated.
func testMirrorWorker(t *testing.T) *mirror.Worker {
	t.Helper()
	return mirror.New(mirror.WorkerOpts{SourceRoot: t.TempDir()})
}

// newProbeServer returns a daemon.Server with no handlers registered.
// Use registerProbeMethod below to add agent.listFiles before
// calling wireAgentDispatch.
func newProbeServer(t *testing.T) *daemon.Server {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "probe.sock")
	return daemon.NewServer(socketPath)
}

// registerProbeMethod registers a no-op handler at the agent.listFiles
// method so wireAgentDispatch's probe sees the RPC as present.
func registerProbeMethod(s *daemon.Server) {
	s.RegisterHandler("agent.listFiles", func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, nil
	})
}

// TestWireAgentDispatch_RPCAbsent_SetsSkipDrain pins the v0.11
// default state: agent.listFiles isn't registered → tracker is
// flipped into skip-drain mode → drain returns immediately.
func TestWireAgentDispatch_RPCAbsent_SetsSkipDrain(t *testing.T) {
	server := newProbeServer(t)
	// No handlers registered; agent.listFiles is absent.

	drainer, tracker := wireAgentDispatch(server)

	if drainer == nil {
		t.Fatal("expected non-nil Drainer from wireAgentDispatch")
	}
	if tracker == nil {
		t.Fatal("expected non-nil tracker from wireAgentDispatch")
	}

	// Even after Begin (simulating an RPC that somehow registered
	// without going through the wired adapter), the tracker reports
	// 0 in-flight because skip-drain is set.
	tracker.Begin("docs_bot", "agent.listFiles")
	if c := tracker.Count("docs_bot", []string{"agent.listFiles"}); c != 0 {
		t.Errorf("skip-drain mode Count = %d; want 0 (RPC absent → short-circuit)", c)
	}

	// Drainer returns nil error immediately (no blocking on grace).
	start := time.Now()
	if err := drainer.DrainListFiles(context.Background(), "docs_bot", 2*time.Second); err != nil {
		t.Errorf("DrainListFiles in skip-drain mode = %v; want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("skip-drain DrainListFiles elapsed = %v; expected near-zero", elapsed)
	}
}

// TestWireAgentDispatch_RPCPresent_LeavesSkipDrainOff pins the
// post-MB-1.S2 path: when agent.listFiles is registered, the
// tracker stays in normal mode so Begin/End counts surface and
// drain actually waits.
func TestWireAgentDispatch_RPCPresent_LeavesSkipDrainOff(t *testing.T) {
	server := newProbeServer(t)
	registerProbeMethod(server)

	_, tracker := wireAgentDispatch(server)

	tracker.Begin("docs_bot", "agent.listFiles")
	if c := tracker.Count("docs_bot", []string{"agent.listFiles"}); c != 1 {
		t.Errorf("RPC-present Count = %d; want 1 (tracker fully operational)", c)
	}
}

// TestWireAgentDispatch_NilServerPanics is a defensive check on the
// wiring-bug guard. Production callers always pass the real server,
// but a nil here is a programming error worth crashing on rather
// than silently degrading.
func TestWireAgentDispatch_NilServerPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil server")
		}
	}()
	_, _ = wireAgentDispatch(nil)
}

// --- registerPlaceholderHandlers (E6.5 Task 42a) ---

// newSchedulerForRegistrationTest spins up a real scheduler over an
// in-memory test database. Mirrors the pattern from
// internal/daemon/scheduler/scheduler_test.go but lives here so the
// cmd/thrum/ integration-style test can verify the registration
// helper against an actual scheduler instance.
func newSchedulerForRegistrationTest(t *testing.T) *scheduler.Scheduler {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "registration.db")
	raw, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if err := schema.InitDB(raw); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	db := safedb.New(raw)
	s := scheduler.New(scheduler.Config{DB: db, DaemonID: "test-daemon"})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s
}

// TestRegisterPlaceholderHandlers_BothTypesAppear pins the E6.5
// Task 42a integration AC: registering placeholders at daemon
// boot makes "scheduled_agent" and "nudge" visible via
// RegisteredTypeHandlers. This is the canonical post-registration
// state operators and downstream consumers (validator + reactor)
// can rely on.
func TestRegisterPlaceholderHandlers_BothTypesAppear(t *testing.T) {
	s := newSchedulerForRegistrationTest(t)

	if err := registerPlaceholderHandlers(s); err != nil {
		t.Fatalf("registerPlaceholderHandlers: %v", err)
	}

	registered := s.RegisteredTypeHandlers()
	have := make(map[string]bool, len(registered))
	for _, jt := range registered {
		have[jt] = true
	}
	for _, want := range []string{"scheduled_agent", "nudge"} {
		if !have[want] {
			t.Errorf("type %q missing from RegisteredTypeHandlers; got %v", want, registered)
		}
	}
}

// TestRegisterPlaceholderHandlers_DispatchReturnsWiringPending pins
// the operator-facing failure mode of the scheduled_agent / nudge
// PLACEHOLDER dispatcher (the type-handler registered by
// registerPlaceholderHandlers — distinct from the Restarter slot
// which thrum-6qmf.4.88 already wired to the real adapter).
//
// The placeholder type-dispatcher is retained for fixture/test
// utility (see registerPlaceholderHandlers docstring); if a
// scheduled_agent job ticks through it before the real adapter is
// wired in production, the error chain is ErrHandlerWiringPending
// rather than a nil-deref panic. Cleanly failed dispatches
// surface in `thrum cron history` with a meaningful reason.
func TestRegisterPlaceholderHandlers_DispatchReturnsWiringPending(t *testing.T) {
	// Construct a placeholder directly and exercise Dispatch — the
	// scheduler-side end-to-end fire would require A-B1's reactor +
	// state DB plumbing, which is out of scope for the 42a
	// integration assertion. The integration value here is verifying
	// that the placeholder we register at boot is the same shape
	// that produces a clean error on dispatch.
	h := agentdispatch.NewPlaceholderHandler("scheduled_agent")
	err := h.Dispatch(context.Background(), scheduler.JobSpec{ID: "test"}, "run-1", nil, nil)
	if !errors.Is(err, agentdispatch.ErrHandlerWiringPending) {
		t.Errorf("placeholder Dispatch error = %v; want errors.Is ErrHandlerWiringPending", err)
	}
}

// TestRegisterPlaceholderHandlers_NilSchedulerRejects pins the
// defensive guard on the helper itself — a nil scheduler is a
// programming error and should fail loudly before any registration
// is attempted.
func TestRegisterPlaceholderHandlers_NilSchedulerRejects(t *testing.T) {
	if err := registerPlaceholderHandlers(nil); err == nil {
		t.Error("expected error on nil scheduler")
	}
}

// TestRegisterPlaceholderHandlers_RejectsDuplicateRegistration
// pins the fact that re-calling the helper against the same
// scheduler fails on the first duplicate type — operators or 42b
// will need to swap the registration mechanism rather than
// double-register.
func TestRegisterPlaceholderHandlers_RejectsDuplicateRegistration(t *testing.T) {
	s := newSchedulerForRegistrationTest(t)
	if err := registerPlaceholderHandlers(s); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := registerPlaceholderHandlers(s); err == nil {
		t.Error("expected error on duplicate registration")
	}
}

// --- wireScheduledAgentHandlers (E6.5 Task 42b) ---

// TestWireScheduledAgentHandlers_RejectsNilScheduler pins the
// defensive guard on the helper itself — a nil scheduler is a
// programming error.
func TestWireScheduledAgentHandlers_RejectsNilScheduler(t *testing.T) {
	if _, err := wireScheduledAgentHandlers(nil, scheduledAgentDeps{}); err == nil {
		t.Error("expected error on nil scheduler")
	}
}

// TestWireScheduledAgentHandlers_RejectsNilHandlers pins the
// guards that catch wiring bugs at boot: nil TmuxHandler / nil
// MessageHandler / empty CallerAgentID / nil MirrorWorker all
// return a clear error rather than silently constructing a handler
// that would nil-deref at first dispatch.
func TestWireScheduledAgentHandlers_RejectsNilHandlers(t *testing.T) {
	s := newSchedulerForRegistrationTest(t)
	cases := []struct {
		name string
		deps scheduledAgentDeps
		want string
	}{
		{"nil-tmux", scheduledAgentDeps{}, "nil TmuxHandler"},
		{"nil-message", scheduledAgentDeps{TmuxHandler: &rpc.TmuxHandler{}}, "nil MessageHandler"},
		{"empty-caller", scheduledAgentDeps{TmuxHandler: &rpc.TmuxHandler{}, MessageHandler: &rpc.MessageHandler{}}, "empty CallerAgentID"},
		{"nil-mirror", scheduledAgentDeps{
			TmuxHandler:    &rpc.TmuxHandler{},
			MessageHandler: &rpc.MessageHandler{},
			CallerAgentID:  "supervisor_test",
		}, "nil MirrorWorker"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := wireScheduledAgentHandlers(s, tc.deps)
			if err == nil {
				t.Fatalf("expected error mentioning %q; got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v; want substring %q", err, tc.want)
			}
		})
	}
}

// TestWireScheduledAgentHandlers_RejectsNilDaemonState pins the
// E6.9 B3 deps guard: DaemonState is required to construct the
// production BootReconciler (StateStore + LifecycleStore both
// thread its safedb handle).
func TestWireScheduledAgentHandlers_RejectsNilDaemonState(t *testing.T) {
	s := newSchedulerForRegistrationTest(t)
	_, err := wireScheduledAgentHandlers(s, scheduledAgentDeps{
		TmuxHandler:    &rpc.TmuxHandler{},
		MessageHandler: &rpc.MessageHandler{},
		CallerAgentID:  "supervisor_test",
		MirrorWorker:   testMirrorWorker(t),
		// DaemonState intentionally omitted.
	})
	if err == nil {
		t.Fatalf("expected nil-DaemonState error, got nil")
	}
	if !strings.Contains(err.Error(), "nil DaemonState") {
		t.Errorf("error = %v; want substring %q", err, "nil DaemonState")
	}
}

// TestWireScheduledAgentHandlers_RegistersBothTypes verifies that
// when the deps are wired correctly, both "scheduled_agent" and
// "nudge" types are registered with real (non-placeholder)
// handlers. Closes §9.6.1 against the real-handler path (it was
// already closed for the placeholder path by 42a's
// TestRegisterPlaceholderHandlers_BothTypesAppear).
//
// The test uses zero-valued nested deps (e.g., TmuxHandler with
// no thrumDir / state) — sufficient for registration; dispatch
// itself isn't exercised here. The 9-stage flow's correctness is
// covered by scheduled_agent_test.go's existing fixtures.
func TestWireScheduledAgentHandlers_RegistersBothTypes(t *testing.T) {
	s := newSchedulerForRegistrationTest(t)
	if _, err := wireScheduledAgentHandlers(s, scheduledAgentDeps{
		RepoPath:       "/tmp/repo",
		TmuxHandler:    &rpc.TmuxHandler{},
		MessageHandler: &rpc.MessageHandler{},
		CallerAgentID:  "supervisor_test",
		MirrorWorker:   testMirrorWorker(t),
		DaemonState:    newStateForWireTest(t),
	}); err != nil {
		t.Fatalf("wireScheduledAgentHandlers: %v", err)
	}
	registered := s.RegisteredTypeHandlers()
	have := make(map[string]bool, len(registered))
	for _, jt := range registered {
		have[jt] = true
	}
	for _, want := range []string{"scheduled_agent", "nudge"} {
		if !have[want] {
			t.Errorf("type %q missing from RegisteredTypeHandlers after 42b wire; got %v", want, registered)
		}
	}
}

// TestWireScheduledAgentHandlers_RejectsDuplicateOnPlaceholderConflict
// pins the mutually-exclusive contract: 42b wiring cannot land on
// top of 42a registration (registerPlaceholderHandlers must not
// be called in the same daemon-boot path that calls
// wireScheduledAgentHandlers).
func TestWireScheduledAgentHandlers_RejectsDuplicateOnPlaceholderConflict(t *testing.T) {
	s := newSchedulerForRegistrationTest(t)
	if err := registerPlaceholderHandlers(s); err != nil {
		t.Fatalf("placeholder register: %v", err)
	}
	// Now try 42b on top — must fail because RegisterTypeHandler
	// rejects duplicates.
	_, err := wireScheduledAgentHandlers(s, scheduledAgentDeps{
		RepoPath:       "/tmp/repo",
		TmuxHandler:    &rpc.TmuxHandler{},
		MessageHandler: &rpc.MessageHandler{},
		CallerAgentID:  "supervisor_test",
		MirrorWorker:   testMirrorWorker(t),
		DaemonState:    newStateForWireTest(t),
	})
	if err == nil {
		t.Error("expected duplicate-registration error when placeholder + real handlers both registered")
	}
}

// --- wirePaneHealthCheck (thrum-fvhs / E6.7 9.8.4) ---

// TestWirePaneHealthCheck_RegistersInternalJob pins the
// thrum-fvhs / E6.7 9.8.4 wiring contract: wirePaneHealthCheck
// registers the canonical "internal.pane_health_check" job with
// the @every 30s cadence. Operators see it in `thrum cron list`
// + `thrum cron history` output keyed off this id; drift would
// break those audit-trail queries.
func TestWirePaneHealthCheck_RegistersInternalJob(t *testing.T) {
	s := newSchedulerForRegistrationTest(t)

	// Build minimal real deps. The registry + state are shared with
	// the production wiring path — same DB the scheduler sees.
	// TmuxHandler is zero-valued for registration-shape testing;
	// dispatch itself isn't exercised here (the agenthealth-side
	// tests cover the loop behavior; the rpc-side RestartSession
	// tests cover the production path).
	st := newStateForWireTest(t)
	registry := agent.NewSQLiteRegistry(st.DB())

	if err := wirePaneHealthCheck(s, registry, st, &rpc.TmuxHandler{}); err != nil {
		t.Fatalf("wirePaneHealthCheck: %v", err)
	}

	spec, ok := s.JobSpec(paneHealthCheckJobID)
	if !ok {
		t.Fatalf("job %q not registered", paneHealthCheckJobID)
	}
	if spec.Schedule != paneHealthCheckSchedule {
		t.Errorf("schedule = %q; want %q", spec.Schedule, paneHealthCheckSchedule)
	}
	if spec.Type != "internal" {
		t.Errorf("type = %q; want internal", spec.Type)
	}
}

// TestWirePaneHealthCheck_RejectsNilDeps pins the defensive nil
// guards in wirePaneHealthCheck. Operator-facing diagnostics
// matter here — a misconfigured daemon boot should fail loud
// rather than nil-deref during the first tick.
func TestWirePaneHealthCheck_RejectsNilDeps(t *testing.T) {
	s := newSchedulerForRegistrationTest(t)
	st := newStateForWireTest(t)
	registry := agent.NewSQLiteRegistry(st.DB())
	tmx := &rpc.TmuxHandler{}

	cases := []struct {
		name string
		sch  *scheduler.Scheduler
		reg  agent.AgentRegistry
		st   *state.State
		tmx  *rpc.TmuxHandler
	}{
		{"nil scheduler", nil, registry, st, tmx},
		{"nil registry", s, nil, st, tmx},
		{"nil state", s, registry, nil, tmx},
		{"nil tmux", s, registry, st, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := wirePaneHealthCheck(c.sch, c.reg, c.st, c.tmx); err == nil {
				t.Error("expected error; got nil")
			}
		})
	}
}

// newStateForWireTest builds a minimal *state.State backed by a
// fresh in-memory SQLite for wire-helper testing. The state's DB
// must already have run migrations to head so the agents +
// agent_lifecycle_events tables exist (the registry + lifecycle
// store both read against them).
func newStateForWireTest(t *testing.T) *state.State {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	if err := os.MkdirAll(syncDir, 0o750); err != nil {
		t.Fatalf("mkdir syncDir: %v", err)
	}
	st, err := state.NewState(thrumDir, syncDir, "test-repo", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
