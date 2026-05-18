package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
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
