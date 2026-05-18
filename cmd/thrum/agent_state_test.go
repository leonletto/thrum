package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/agentstate"
)

// withTempRepoRoot creates a fresh repo root with .thrum/ directory
// + identity file for the named agent, then chdirs into it so the
// cwd-anchored paths.FindThrumRoot resolves to our fixture. Cleanup
// restores the original cwd. Returns the absolute repoRoot path.
func withTempRepoRoot(t *testing.T, agentID string) string {
	t.Helper()
	repoRoot := t.TempDir()
	thrumRoot := filepath.Join(repoRoot, ".thrum")
	if err := os.MkdirAll(thrumRoot, 0o700); err != nil {
		t.Fatalf("mkdir thrumRoot: %v", err)
	}
	// Identity file so currentAgentID would have something to find
	// IF the daemon were running. The agent_state CLI defaults to
	// the --agent-id flag when daemon isn't reachable, so tests
	// pass --agent-id explicitly.
	idDir := filepath.Join(thrumRoot, "identities")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}
	body := `{"version":5,"repo_id":"test","agent":{"name":"` + agentID + `","role":"implementer","module":"test"},"worktree":"` + repoRoot + `","updated_at":"2026-05-18T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(idDir, agentID+".json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return repoRoot
}

func TestAgentStateUpdate_FirstSession_CreatesFile(t *testing.T) {
	repoRoot := withTempRepoRoot(t, "alpha")

	cmd := agentStateCmd()
	cmd.SetArgs([]string{
		"update",
		"--agent-id", "alpha",
		"--session-id", "ses_001",
		"--summary", "First session: created the test fixture.",
	})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	statePath := filepath.Join(repoRoot, ".thrum", "agents", "alpha", "state.md")
	data, err := os.ReadFile(statePath) // #nosec G304 -- test fixture path
	if err != nil {
		t.Fatalf("read state.md: %v", err)
	}
	state, err := agentstate.Parse(string(data))
	if err != nil {
		t.Fatalf("parse just-written state.md: %v", err)
	}

	if state.AgentName != "alpha" {
		t.Errorf("AgentName: got %q, want alpha", state.AgentName)
	}
	if len(state.Verbatim) != 1 {
		t.Fatalf("Verbatim count: got %d, want 1", len(state.Verbatim))
	}
	if state.Verbatim[0].SessionID != "ses_001" {
		t.Errorf("SessionID: got %q, want ses_001", state.Verbatim[0].SessionID)
	}
	if state.Verbatim[0].Summary != "First session: created the test fixture." {
		t.Errorf("Summary: got %q", state.Verbatim[0].Summary)
	}
	if state.LastUpdated.IsZero() {
		t.Error("LastUpdated should be populated")
	}
}

func TestAgentStateUpdate_SubsequentSessions_PromoteAndDrop(t *testing.T) {
	withTempRepoRoot(t, "beta")

	// Land 5 sessions in sequence. Sessions 1-4 fill the verbatim
	// queue; session 5 causes ses_001 to graduate into block A.
	for i := 1; i <= 5; i++ {
		cmd := agentStateCmd()
		cmd.SetArgs([]string{
			"update",
			"--agent-id", "beta",
			"--session-id", "ses_00" + string(rune('0'+i)),
			"--summary", "Synthetic session " + string(rune('0'+i)),
		})
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute session %d: %v", i, err)
		}
	}

	// Read the final state.
	statePath := filepath.Join(".thrum", "agents", "beta", "state.md")
	data, err := os.ReadFile(statePath) // #nosec G304 -- test fixture path
	if err != nil {
		t.Fatalf("read state.md: %v", err)
	}
	state, err := agentstate.Parse(string(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(state.Verbatim) != 4 {
		t.Errorf("Verbatim count: got %d, want 4", len(state.Verbatim))
	}
	if state.Verbatim[0].SessionID != "ses_005" {
		t.Errorf("slot #1 should be ses_005, got %q", state.Verbatim[0].SessionID)
	}
	if len(state.SummaryBlocks) != 1 {
		t.Fatalf("SummaryBlocks count: got %d, want 1", len(state.SummaryBlocks))
	}
	if state.SummaryBlocks[0].StartSession != "ses_001" {
		t.Errorf("first block should contain graduated ses_001: %+v", state.SummaryBlocks[0])
	}
}

func TestAgentStateUpdate_MissingSessionID_Fails(t *testing.T) {
	withTempRepoRoot(t, "gamma")

	cmd := agentStateCmd()
	cmd.SetArgs([]string{
		"update",
		"--agent-id", "gamma",
		"--summary", "missing session id should fail",
	})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --session-id")
	}
	if !strings.Contains(err.Error(), "session-id is required") {
		t.Errorf("error should mention required flag: %v", err)
	}
}

func TestAgentStateUpdate_MissingSummary_Fails(t *testing.T) {
	withTempRepoRoot(t, "delta")

	cmd := agentStateCmd()
	cmd.SetArgs([]string{
		"update",
		"--agent-id", "delta",
		"--session-id", "ses_001",
	})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing --summary")
	}
	if !strings.Contains(err.Error(), "summary is required") {
		t.Errorf("error should mention required flag: %v", err)
	}
}

// TestAgentStateUpdate_RefuseToOverwriteCorruptStateMD verifies the
// spec §6.5 invariant: if the existing state.md fails to parse,
// the update command must NOT overwrite it with a fresh state.
// It must return an error so the operator routes through
// /thrum:recover-agent-state.
func TestAgentStateUpdate_RefuseToOverwriteCorruptStateMD(t *testing.T) {
	repoRoot := withTempRepoRoot(t, "epsilon")

	// Pre-create a malformed state.md.
	statePath := filepath.Join(repoRoot, ".thrum", "agents", "epsilon", "state.md")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("not a valid state.md\nno header\n"), 0o600); err != nil {
		t.Fatalf("write malformed: %v", err)
	}

	cmd := agentStateCmd()
	cmd.SetArgs([]string{
		"update",
		"--agent-id", "epsilon",
		"--session-id", "ses_001",
		"--summary", "should fail before writing",
	})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for malformed state.md")
	}
	if !strings.Contains(err.Error(), "recovery required") {
		t.Errorf("error should mention recovery path: %v", err)
	}

	// File must be unchanged.
	data, _ := os.ReadFile(statePath) // #nosec G304 -- test fixture
	if !strings.Contains(string(data), "not a valid state.md") {
		t.Error("malformed state.md was overwritten — §6.5 invariant violated")
	}
}

// === `thrum agent state recover` tests ===

func TestAgentStateRecover_NoStateMD_NoOp(t *testing.T) {
	withTempRepoRoot(t, "alpha")

	cmd := agentStateCmd()
	cmd.SetArgs([]string{"recover", "--agent-id", "alpha"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(stdout.String(), "does not exist") {
		t.Errorf("expected 'does not exist' nothing-to-recover message: %q", stdout.String())
	}
}

func TestAgentStateRecover_CleanStateMD_ReportsOK(t *testing.T) {
	repoRoot := withTempRepoRoot(t, "beta")

	// Land a clean state.md via the update command.
	updateCmd := agentStateCmd()
	updateCmd.SetArgs([]string{
		"update",
		"--agent-id", "beta",
		"--session-id", "ses_001",
		"--summary", "clean session",
	})
	var buf bytes.Buffer
	updateCmd.SetOut(&buf)
	updateCmd.SetErr(&buf)
	if err := updateCmd.Execute(); err != nil {
		t.Fatalf("update: %v", err)
	}

	// Recover should report OK.
	recoverCmd := agentStateCmd()
	recoverCmd.SetArgs([]string{"recover", "--agent-id", "beta"})
	var stdout bytes.Buffer
	recoverCmd.SetOut(&stdout)
	recoverCmd.SetErr(&stdout)
	if err := recoverCmd.Execute(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !strings.Contains(stdout.String(), "OK") {
		t.Errorf("expected 'OK' status: %q", stdout.String())
	}

	// File must still exist (recover didn't move it).
	if _, err := os.Stat(filepath.Join(repoRoot, ".thrum", "agents", "beta", "state.md")); err != nil {
		t.Errorf("state.md should still exist after clean recover: %v", err)
	}
}

// TestAgentStateRecover_MalformedStateMD_PreservesBroken covers the
// spec §6.5 invariant on the SKILL/CLI side: a malformed state.md
// gets moved to .broken; the RPC is invoked (which sets the
// corruption flag + emits the escalation). Since this is a CLI
// test without a real daemon, the daemon-side fails to connect —
// but the .broken move happens FIRST (per CLI code) so the
// preservation invariant holds regardless of RPC reachability.
func TestAgentStateRecover_MalformedStateMD_PreservesBroken(t *testing.T) {
	repoRoot := withTempRepoRoot(t, "gamma")

	// Pre-create a malformed state.md.
	statePath := filepath.Join(repoRoot, ".thrum", "agents", "gamma", "state.md")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("not a valid state.md\nno header\n"), 0o600); err != nil {
		t.Fatalf("write malformed: %v", err)
	}

	cmd := agentStateCmd()
	cmd.SetArgs([]string{"recover", "--agent-id", "gamma"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	// The recover command will error because no daemon is running,
	// but the .broken file move happens BEFORE the RPC call.
	_ = cmd.Execute()

	// Invariant: original path is gone (moved to .broken).
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state.md should have been moved away; still at %s", statePath)
	}
	// Invariant: .broken file exists with the original content.
	brokenPath := statePath + ".broken"
	data, err := os.ReadFile(brokenPath) // #nosec G304 -- test fixture
	if err != nil {
		t.Fatalf("read .broken: %v", err)
	}
	if !strings.Contains(string(data), "not a valid state.md") {
		t.Error("corrupt content not preserved in .broken file")
	}
}

// TestAgentStateUpdate_OptionalFlags covers --last-worked-on /
// --planning-next / --run-id / --run-state field overrides.
func TestAgentStateUpdate_OptionalFlags(t *testing.T) {
	withTempRepoRoot(t, "zeta")

	cmd := agentStateCmd()
	cmd.SetArgs([]string{
		"update",
		"--agent-id", "zeta",
		"--session-id", "ses_001",
		"--summary", "first session",
		"--last-worked-on", "I last did the test fixture. Open thread: more tests.",
		"--planning-next", "Next wake should add more flag tests.",
		"--run-id", "test-run-g3",
		"--run-state", "success",
	})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	statePath := filepath.Join(".thrum", "agents", "zeta", "state.md")
	data, err := os.ReadFile(statePath) // #nosec G304 -- test fixture
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	state, err := agentstate.Parse(string(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !strings.Contains(state.LastWorkedOn, "test fixture") {
		t.Errorf("LastWorkedOn: got %q", state.LastWorkedOn)
	}
	if !strings.Contains(state.PlanningNext, "more flag tests") {
		t.Errorf("PlanningNext: got %q", state.PlanningNext)
	}
	if state.LastRunID != "test-run-g3" {
		t.Errorf("LastRunID: got %q", state.LastRunID)
	}
	if state.LastRunState != "success" {
		t.Errorf("LastRunState: got %q", state.LastRunState)
	}
}
