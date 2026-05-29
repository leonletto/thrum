package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
)

// registerDeadAgentFixture installs an active agent + session whose
// AgentPID is dead (999999), mirroring the team_test.go fixture pattern.
// Returns the generated agent_id + session_id.
func registerDeadAgentFixture(t *testing.T, st *state.State) (agentID, sessionID string) {
	t.Helper()
	ctx := context.Background()
	const deadPID = 999999

	// Use a fixed agent_id so the identity-file path is deterministic.
	agentID = "deadagent_fixture_001"
	sessionID = "ses_deadagent_001"

	registerEvent := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		EventID:   "evt_DEADAGENT_REG",
		AgentID:   agentID,
		Kind:      "agent",
		Role:      "implementer",
		Module:    "sweeper",
		AgentPID:  deadPID,
	}
	if _, err := st.WriteEvent(ctx, registerEvent); err != nil {
		t.Fatalf("register dead agent: %v", err)
	}

	startEvent := types.AgentSessionStartEvent{
		Type:      "agent.session.start",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		EventID:   "evt_DEADAGENT_SES",
		SessionID: sessionID,
		AgentID:   agentID,
	}
	if _, err := st.WriteEvent(ctx, startEvent); err != nil {
		t.Fatalf("start dead-agent session: %v", err)
	}

	return agentID, sessionID
}

func TestDeadAgentSweeper_Sweep_EmitsSessionEnd(t *testing.T) {
	st := createTestStateForSync(t)
	_, sessionID := registerDeadAgentFixture(t, st)

	sw := NewDeadAgentSweeper(st, "")

	var endBefore int
	if err := st.RawDB().QueryRow(
		"SELECT COUNT(*) FROM events WHERE type = 'agent.session.end' AND event_json LIKE ?",
		"%"+sessionID+"%",
	).Scan(&endBefore); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if endBefore != 0 {
		t.Fatalf("expected 0 session.end events before sweep, got %d", endBefore)
	}

	sw.Sweep(context.Background())

	var endAfter int
	if err := st.RawDB().QueryRow(
		"SELECT COUNT(*) FROM events WHERE type = 'agent.session.end' AND event_json LIKE ?",
		"%"+sessionID+"%",
	).Scan(&endAfter); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if endAfter != 1 {
		t.Errorf("session.end events for dead session: got %d, want 1", endAfter)
	}

	// A second sweep MUST be a no-op: the projection now shows the
	// session as ended (ended_at != NULL) so the JOIN in
	// collectDeadAgents excludes it.
	sw.Sweep(context.Background())

	var endAfterSecond int
	if err := st.RawDB().QueryRow(
		"SELECT COUNT(*) FROM events WHERE type = 'agent.session.end' AND event_json LIKE ?",
		"%"+sessionID+"%",
	).Scan(&endAfterSecond); err != nil {
		t.Fatalf("count after second sweep: %v", err)
	}
	if endAfterSecond != 1 {
		t.Errorf("second sweep wrote a duplicate session.end: got %d total, want 1 (sweeper must self-converge)", endAfterSecond)
	}
}

// TestDeadAgentSweeper_Sweep_ClearsDeadAgentPID is the thrum-5oui
// defense-in-depth guard: when the sweeper ends a dead local agent's session,
// it must also reset agents.agent_pid to 0 (the restartable sentinel), so the
// dead PID doesn't linger in the projection until the next boot reconcile.
func TestDeadAgentSweeper_Sweep_ClearsDeadAgentPID(t *testing.T) {
	st := createTestStateForSync(t)
	agentID, _ := registerDeadAgentFixture(t, st)

	var pidBefore int
	if err := st.RawDB().QueryRow(
		"SELECT agent_pid FROM agents WHERE agent_id = ?", agentID).Scan(&pidBefore); err != nil {
		t.Fatalf("read agent_pid before: %v", err)
	}
	if pidBefore <= 0 {
		t.Fatalf("fixture should seed a dead non-zero agent_pid, got %d", pidBefore)
	}

	NewDeadAgentSweeper(st, "").Sweep(context.Background())

	var pidAfter int
	if err := st.RawDB().QueryRow(
		"SELECT agent_pid FROM agents WHERE agent_id = ?", agentID).Scan(&pidAfter); err != nil {
		t.Fatalf("read agent_pid after: %v", err)
	}
	if pidAfter != 0 {
		t.Errorf("agents.agent_pid = %d after sweep, want 0 (thrum-5oui: sweeper must zero a dead agent's PID so its worktree stays restartable, not leave %d to linger)", pidAfter, pidBefore)
	}
}

// TestDeadAgentSweeper_Sweep_SkipsLiveFilePID ports the thrum-pxz.14
// Fix B contract to the .6 sweeper: when the DB reports a dead PID
// but the identity file reports a live PID for the same agent, the
// sweeper must NOT emit session.end (the agent is actually alive
// under a different PID; the DB is stale).
func TestDeadAgentSweeper_Sweep_SkipsLiveFilePID(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("create identities dir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_SWEEPER_FILE_PID", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	agentID, sessionID := registerDeadAgentFixture(t, st)

	// Identity file reports a live PID (os.Getpid()) different from the
	// DB's dead PID 999999 — exact bsn7-pxz.14 stale-DB scenario.
	idFile := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    agentID,
			Role:    "implementer",
			Module:  "sweeper",
			Display: "Sweeper File-PID Skip",
		},
		AgentPID: os.Getpid(),
		Runtime:  "claude",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity file: %v", err)
	}

	sw := NewDeadAgentSweeper(st, thrumDir)
	sw.Sweep(context.Background())

	var got int
	if err := st.RawDB().QueryRow(
		"SELECT COUNT(*) FROM events WHERE type = 'agent.session.end' AND event_json LIKE ?",
		"%"+sessionID+"%",
	).Scan(&got); err != nil {
		t.Fatalf("count: %v", err)
	}
	if got != 0 {
		t.Errorf("session.end emitted despite live file PID: got %d, want 0 (file-PID guard broken)", got)
	}
}

// TestDeadAgentSweeper_Sweep_SkipsRemoteOrigin verifies cross-daemon
// agents (those whose origin_daemon does NOT match this daemon's ID)
// are NOT swept. Their PID lives on a remote host; local IsRunning is
// meaningless. See thrum-pxz.14.
func TestDeadAgentSweeper_Sweep_SkipsRemoteOrigin(t *testing.T) {
	st := createTestStateForSync(t)
	sessionID := "ses_remote_001"
	agentID := "remote_agent_001"

	registerJSON, _ := json.Marshal(map[string]any{
		"type":          "agent.register",
		"timestamp":     time.Now().UTC().Format(time.RFC3339Nano),
		"event_id":      "evt_REMOTE_REG",
		"agent_id":      agentID,
		"kind":          "agent",
		"role":          "implementer",
		"module":        "remote",
		"agent_pid":     999999,
		"origin_daemon": "d_remote_other", // NOT this daemon
		"v":             1,
	})
	var registerMap map[string]any
	_ = json.Unmarshal(registerJSON, &registerMap)
	if _, err := st.WriteEvent(context.Background(), registerMap); err != nil {
		t.Fatalf("register remote agent: %v", err)
	}
	startEvent := types.AgentSessionStartEvent{
		Type:      "agent.session.start",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		EventID:   "evt_REMOTE_SES",
		SessionID: sessionID,
		AgentID:   agentID,
	}
	if _, err := st.WriteEvent(context.Background(), startEvent); err != nil {
		t.Fatalf("start remote session: %v", err)
	}

	sw := NewDeadAgentSweeper(st, "")
	sw.Sweep(context.Background())

	var got int
	if err := st.RawDB().QueryRow(
		"SELECT COUNT(*) FROM events WHERE type = 'agent.session.end' AND event_json LIKE ?",
		"%"+sessionID+"%",
	).Scan(&got); err != nil {
		t.Fatalf("count: %v", err)
	}
	if got != 0 {
		t.Errorf("session.end emitted for cross-daemon agent: got %d, want 0 (remote-origin guard broken)", got)
	}
}

// TestDeadAgentSweeper_Start_RunsInitialSweepAndTicks verifies the
// Start loop fires Sweep once immediately and then on the interval.
// Asserts the immediate sweep observed via a session.end event for a
// pre-registered dead agent, then cancels the ctx before a second
// tick fires.
func TestDeadAgentSweeper_Start_RunsInitialSweepAndTicks(t *testing.T) {
	st := createTestStateForSync(t)
	_, sessionID := registerDeadAgentFixture(t, st)

	sw := NewDeadAgentSweeper(st, "")
	sw.SetInterval(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sw.Start(ctx)
		close(done)
	}()

	// Wait long enough for the immediate Sweep to land + projection apply.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return within 2s after ctx cancel")
	}

	var got int
	if err := st.RawDB().QueryRow(
		"SELECT COUNT(*) FROM events WHERE type = 'agent.session.end' AND event_json LIKE ?",
		"%"+sessionID+"%",
	).Scan(&got); err != nil {
		t.Fatalf("count: %v", err)
	}
	if got < 1 {
		t.Errorf("session.end events emitted by Start: got %d, want ≥ 1 (initial sweep should have fired)", got)
	}
}
