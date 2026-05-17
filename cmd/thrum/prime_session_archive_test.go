package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/cli"
	configpkg "github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// === Unit tests: stub primeRPCCaller ===

// stubCaller implements primeRPCCaller for fast, isolated tests of
// the wiring assignment logic — no daemon needed.
type stubCaller struct {
	respond func(method string, params any, result any) error
}

func (s *stubCaller) Call(method string, params any, result any) error {
	return s.respond(method, params, result)
}

func TestWireSessionArchiveResponse_NilResult_NoOp(t *testing.T) {
	called := false
	c := &stubCaller{respond: func(_ string, _, _ any) error {
		called = true
		return nil
	}}
	// nil result should short-circuit without invoking the client.
	wireSessionArchiveResponse(c, nil)
	if called {
		t.Error("client.Call should not be invoked when result is nil")
	}
}

func TestWireSessionArchiveResponse_NilIdentity_NoOp(t *testing.T) {
	called := false
	c := &stubCaller{respond: func(_ string, _, _ any) error {
		called = true
		return nil
	}}
	wireSessionArchiveResponse(c, &cli.PrimeContext{Identity: nil})
	if called {
		t.Error("client.Call should not be invoked when result.Identity is nil")
	}
}

func TestWireSessionArchiveResponse_ClientError_NoMutation(t *testing.T) {
	c := &stubCaller{respond: func(_ string, _, _ any) error {
		return errors.New("daemon unreachable")
	}}
	result := &cli.PrimeContext{
		Identity:             &cli.WhoamiResult{AgentID: "alpha"},
		RestartSnapshot:      "preserved",
		SessionDiscoveryHint: "preserved",
	}
	wireSessionArchiveResponse(c, result)
	if result.RestartSnapshot != "preserved" {
		t.Errorf("RestartSnapshot mutated on RPC error: got %q", result.RestartSnapshot)
	}
	if result.SessionDiscoveryHint != "preserved" {
		t.Errorf("SessionDiscoveryHint mutated on RPC error: got %q", result.SessionDiscoveryHint)
	}
}

// TestWireSessionArchiveResponse_PopulatesBothFields is the
// brainstormer-third I1 unit-level case: a successful RPC response
// with Content + DiscoveryHint must flow into the PrimeContext
// fields correctly. The companion E2E test below exercises the same
// behavior over a real daemon socket; this stub-level test catches
// the JSON-tag / pointer-deref class of regression cheaply.
func TestWireSessionArchiveResponse_PopulatesBothFields(t *testing.T) {
	content := "snapshot body content"
	hint := "Past sessions: 3 saved (most recent 2026-05-17)\nLast big picture: lab demo."
	c := &stubCaller{respond: func(method string, params any, result any) error {
		if method != "session.archive" {
			t.Errorf("expected method 'session.archive', got %q", method)
		}
		// Marshal/unmarshal to mimic the real client.Call JSON
		// roundtrip — catches JSON tag mismatches.
		respJSON := fmt.Sprintf(`{"archived_path":"/some/path","big_picture":"BP","content":%q,"discovery_hint":%q}`,
			content, hint)
		return json.Unmarshal([]byte(respJSON), result)
	}}
	result := &cli.PrimeContext{
		Identity: &cli.WhoamiResult{AgentID: "alpha"},
	}
	wireSessionArchiveResponse(c, result)

	if result.RestartSnapshot != content {
		t.Errorf("RestartSnapshot: got %q, want %q", result.RestartSnapshot, content)
	}
	if result.SessionDiscoveryHint != hint {
		t.Errorf("SessionDiscoveryHint: got %q, want %q", result.SessionDiscoveryHint, hint)
	}
}

func TestWireSessionArchiveResponse_NilContentPreservesPrior(t *testing.T) {
	c := &stubCaller{respond: func(_ string, _, _ any) error {
		// All fields nil → no snapshot existed.
		return nil
	}}
	result := &cli.PrimeContext{
		Identity:        &cli.WhoamiResult{AgentID: "alpha"},
		RestartSnapshot: "should-stay",
	}
	wireSessionArchiveResponse(c, result)
	if result.RestartSnapshot != "should-stay" {
		t.Errorf("RestartSnapshot should not be cleared by nil Content response: got %q",
			result.RestartSnapshot)
	}
}

// === E2E: in-process daemon ===

// TestWireSessionArchiveResponse_E2E_RealDaemon is the
// brainstormer-third I1 end-to-end case. Spins up a real daemon
// in-process, registers an agent, writes a snapshot to
// .thrum/restart/<id>.md, calls wireSessionArchiveResponse with a
// real *cli.Client over the daemon's Unix socket, and asserts the
// snapshot content lands in result.RestartSnapshot plus the
// discovery hint reflects N=1.
//
// This is the test that would have caught the Q-Spec-1 wiring
// regression brainstormer-third flagged — if the JSON tag on
// `Content` ever changes, or the response struct shape drifts, or
// the assignment `result.RestartSnapshot = *archiveResp.Content`
// regresses, this test fails. Pure unit tests against the stub
// caller above can't catch JSON-tag drift; this can.
func TestWireSessionArchiveResponse_E2E_RealDaemon(t *testing.T) {
	thrumDir := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(thrumDir, 0o700); err != nil {
		t.Fatalf("mkdir thrumDir: %v", err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "test_repo_e2e", "")
	if err != nil {
		t.Fatalf("new state: %v", err)
	}

	// Start an in-process daemon on a short Unix socket path
	// (avoiding the 104-char macOS limit by using /tmp).
	sockDir, err := os.MkdirTemp("", "tw-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	socketPath := filepath.Join(sockDir, "d.sock")

	server := daemon.NewServer(socketPath)

	// Register the handlers wireSessionArchiveResponse needs:
	// session.archive (the load-bearing one) plus agent.register
	// (needed to populate the agents table so HandleSessionArchive's
	// registry.Lookup succeeds).
	agentHandler := rpc.NewAgentHandler(st)
	server.RegisterHandler("agent.register", agentHandler.HandleRegister)
	sessionArchiveHandler := rpc.NewSessionArchiveHandler(st, thrumDir)
	server.RegisterHandler("session.archive", sessionArchiveHandler.HandleArchive)

	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("server start: %v", err)
	}

	// Cleanup ordering matters (t.Cleanup runs LIFO): server.Stop
	// must run BEFORE st.Close so any in-flight handler can finish
	// touching state. If we register st.Close first, the LIFO order
	// would invert. Register server.Stop FIRST so it runs LAST is
	// wrong too — we want server.Stop first (runs last) then
	// st.Close (registered after, runs first under LIFO). Inverted:
	// we want server.Stop to run before st.Close in cleanup order.
	// Since LIFO means last-registered-first-fired, register st.Close
	// FIRST then server.Stop SECOND so server.Stop fires FIRST and
	// st.Close fires SECOND. Brainstormer-third Low #1 catch — the
	// prior ordering was inverted and would race if Stop ever
	// became async.
	t.Cleanup(func() { _ = st.Close() }) // runs second (after server)
	t.Cleanup(func() { server.Stop() })  // runs first (LIFO last-registered)

	// Wait for the socket to accept connections.
	waitForSocket(t, socketPath, 2*time.Second)

	// Register an agent (inserts agents-table row).
	client, err := cli.NewClient(socketPath)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	registerReq := map[string]any{
		"role":     "implementer",
		"module":   "test",
		"mode":     "persistent",
		"identity": "long_lived",
	}
	var registerResp struct {
		AgentID string `json:"agent_id"`
	}
	if err := client.Call("agent.register", registerReq, &registerResp); err != nil {
		t.Fatalf("agent.register: %v", err)
	}
	if registerResp.AgentID == "" {
		t.Fatal("agent.register returned empty AgentID")
	}
	agentID := registerResp.AgentID

	// Write the identity file — HandleRegister inserts the DB row
	// but production identity-file write happens in the CLI/quickstart
	// flow. session_archive.resolveWorktreeThrumDir scans identity
	// files, so we must provide one for the test.
	idFile := &configpkg.IdentityFile{
		Version:   5,
		RepoID:    "test_repo_e2e",
		Agent:     configpkg.AgentConfig{Name: agentID, Role: "implementer", Module: "test"},
		Worktree:  thrumDir,
		UpdatedAt: time.Now().UTC(),
	}
	if err := configpkg.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity file: %v", err)
	}

	// Write a fixture snapshot at .thrum/restart/<id>.md.
	snapshotBody := "Locked the brainstormer-third I1 fix. Wired E2E test through real daemon socket."
	snapshotContent := "---\n" +
		"agent: " + agentID + "\n" +
		"session_id: ses_e2e\n" +
		"saved_at: 2026-05-17T20:00:00.000Z\n" +
		"reason: manual\n" +
		"machine_id: test-host\n" +
		"---\n\n" +
		"## 1. Big picture — what shipped this session\n\n" +
		snapshotBody + "\n"
	restartDir := filepath.Join(thrumDir, "restart")
	if err := os.MkdirAll(restartDir, 0o700); err != nil {
		t.Fatalf("mkdir restart: %v", err)
	}
	if err := os.WriteFile(filepath.Join(restartDir, agentID+".md"), []byte(snapshotContent), 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	// Now exercise the load-bearing wire-up: build a PrimeContext
	// the way `thrum prime` does, then call our extracted helper
	// against the real client.
	result := &cli.PrimeContext{
		Identity: &cli.WhoamiResult{AgentID: agentID},
	}
	wireSessionArchiveResponse(client, result)

	// Snapshot content must reach the PrimeContext.
	if result.RestartSnapshot == "" {
		t.Fatal("RestartSnapshot empty — Q-Spec-1 CLI wire regression: snapshot did not flow from session.archive RPC to PrimeContext")
	}
	if !strings.Contains(result.RestartSnapshot, snapshotBody) {
		t.Errorf("RestartSnapshot missing expected body %q; got: %q",
			snapshotBody, result.RestartSnapshot)
	}

	// Discovery hint must reflect the just-archived session (N=1)
	// and include the §1 body.
	if result.SessionDiscoveryHint == "" {
		t.Error("SessionDiscoveryHint empty — discovery-hint wire regression")
	}
	if !strings.Contains(result.SessionDiscoveryHint, "Past sessions: 1 saved") {
		t.Errorf("SessionDiscoveryHint should reflect N=1: %q", result.SessionDiscoveryHint)
	}
	if !strings.Contains(result.SessionDiscoveryHint, snapshotBody) {
		t.Errorf("SessionDiscoveryHint should include §1 body %q; got: %q",
			snapshotBody, result.SessionDiscoveryHint)
	}

	// Snapshot file must have moved out of restart/ into sessions/.
	if _, err := os.Stat(filepath.Join(restartDir, agentID+".md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("snapshot still in restart/ — archive did not fire: %v", err)
	}
	sessionsDir := filepath.Join(thrumDir, "agents", agentID, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatalf("read sessions dir: %v", err)
	}
	var archivedCount int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-restart.md") {
			archivedCount++
		}
	}
	if archivedCount != 1 {
		t.Errorf("expected 1 archived snapshot in %s, got %d", sessionsDir, archivedCount)
	}
}

// waitForSocket polls until the Unix socket is accepting connections
// or the deadline expires.
func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s did not accept connections within %s", path, timeout)
}
