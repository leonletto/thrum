package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/agentdispatch"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/schema"
)

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
// the operator-facing failure mode: when a placeholder receives a
// dispatch (e.g. an operator-configured scheduled_agent job ticks
// before E6.5 Task 42b lands), the error chain is
// ErrHandlerWiringPending rather than a nil-deref panic. Cleanly
// failed dispatches surface in `thrum cron history` with a
// meaningful reason.
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
