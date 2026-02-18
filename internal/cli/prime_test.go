package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPrimeContext_JSONStructure(t *testing.T) {
	ctx := &PrimeContext{
		Identity: &WhoamiResult{
			AgentID: "test:impl:auth",
			Role:    "impl",
			Module:  "auth",
			Source:  "identity_file",
		},
		Session: &SessionInfo{
			SessionID: "sess_abc123",
			StartedAt: "2026-02-11T10:00:00Z",
			Intent:    "Testing context prime",
		},
		Agents: &AgentsInfo{
			Total:  3,
			Active: 2,
			List: []AgentInfo{
				{AgentID: "test:impl:auth", Role: "impl", Module: "auth"},
				{AgentID: "test:reviewer:ui", Role: "reviewer", Module: "ui"},
			},
		},
		Messages: &MessagesInfo{
			Unread: 1,
			Total:  5,
			Recent: []Message{
				{
					MessageID: "msg_1",
					AgentID:   "test:coordinator:main",
					Body: struct {
						Format     string `json:"format"`
						Content    string `json:"content"`
						Structured string `json:"structured,omitempty"`
					}{
						Content: "Please review PR #42",
					},
				},
			},
		},
		WorkContext: &WorkContextInfo{
			Branch:           "feature/test",
			UncommittedFiles: []string{"src/main.go"},
			UnmergedCommits:  3,
		},
		SyncState: &PrimeSyncInfo{
			DaemonStatus: "ok",
			UptimeMs:     360000,
			SyncState:    "idle",
			Version:      "0.4.0",
		},
	}

	// Verify JSON marshaling
	data, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	// Check all sections present
	for _, section := range []string{"identity", "session", "agents", "messages", "work_context", "sync_state"} {
		if _, ok := decoded[section]; !ok {
			t.Errorf("missing section %q in JSON output", section)
		}
	}

	// Verify identity fields
	identity := decoded["identity"].(map[string]any)
	if identity["agent_id"] != "test:impl:auth" {
		t.Errorf("identity.agent_id = %v, want %q", identity["agent_id"], "test:impl:auth")
	}

	// Verify session
	session := decoded["session"].(map[string]any)
	if session["session_id"] != "sess_abc123" {
		t.Errorf("session.session_id = %v, want %q", session["session_id"], "sess_abc123")
	}

	// Verify agents
	agents := decoded["agents"].(map[string]any)
	if int(agents["total"].(float64)) != 3 {
		t.Errorf("agents.total = %v, want 3", agents["total"])
	}

	// Verify messages
	messages := decoded["messages"].(map[string]any)
	if int(messages["unread"].(float64)) != 1 {
		t.Errorf("messages.unread = %v, want 1", messages["unread"])
	}

	// Verify work_context
	wc := decoded["work_context"].(map[string]any)
	if wc["branch"] != "feature/test" {
		t.Errorf("work_context.branch = %v, want %q", wc["branch"], "feature/test")
	}

	// Verify sync_state
	ss := decoded["sync_state"].(map[string]any)
	if ss["daemon_status"] != "ok" {
		t.Errorf("sync_state.daemon_status = %v, want %q", ss["daemon_status"], "ok")
	}
	if ss["version"] != "0.4.0" {
		t.Errorf("sync_state.version = %v, want %q", ss["version"], "0.4.0")
	}
}

func TestPrimeContext_NoSession(t *testing.T) {
	ctx := &PrimeContext{
		Identity: &WhoamiResult{
			AgentID: "test:impl:auth",
			Role:    "impl",
			Module:  "auth",
			Source:  "identity_file",
		},
		// Session is nil
	}

	data, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded["session"] != nil {
		t.Error("expected session to be nil/absent")
	}
}

func TestFormatPrimeContext_HumanReadable(t *testing.T) {
	ctx := &PrimeContext{
		Identity: &WhoamiResult{
			AgentID: "test:impl:auth",
			Role:    "impl",
			Module:  "auth",
		},
		Session: &SessionInfo{
			SessionID: "sess_abc",
			Intent:    "Testing",
		},
		Agents: &AgentsInfo{
			Total:  2,
			Active: 1,
			List: []AgentInfo{
				{AgentID: "test:impl:auth", Role: "impl", Module: "auth"},
			},
		},
		Messages: &MessagesInfo{
			Unread: 3,
			Total:  10,
		},
		WorkContext: &WorkContextInfo{
			Branch:          "feature/test",
			UnmergedCommits: 5,
		},
	}

	output := FormatPrimeContext(ctx)

	checks := []string{
		"@impl",
		"sess_abc",
		"Testing",
		"2 agents",
		"1 active",
		"3 unread",
		"feature/test",
		"5",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output missing %q:\n%s", check, output)
		}
	}
}

func TestFormatPrimeContext_NotRegistered(t *testing.T) {
	ctx := &PrimeContext{}
	output := FormatPrimeContext(ctx)

	if !strings.Contains(output, "not registered") {
		t.Errorf("expected 'not registered' in output:\n%s", output)
	}
}

func TestFormatPrimeContext_GitError(t *testing.T) {
	ctx := &PrimeContext{
		WorkContext: &WorkContextInfo{
			Error: "not a git repository",
		},
	}
	output := FormatPrimeContext(ctx)

	if !strings.Contains(output, "not a git repository") {
		t.Errorf("expected 'not a git repository' in output:\n%s", output)
	}
}

func TestFormatPrimeContext_SyncState(t *testing.T) {
	ctx := &PrimeContext{
		SyncState: &PrimeSyncInfo{
			DaemonStatus: "ok",
			UptimeMs:     7200000, // 2 hours
			SyncState:    "idle",
			Version:      "0.4.0",
		},
	}
	output := FormatPrimeContext(ctx)

	checks := []string{
		"Daemon: ok",
		"v0.4.0",
		"Sync: idle",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output missing %q:\n%s", check, output)
		}
	}
}

func TestFormatPrimeContext_CommandReference(t *testing.T) {
	ctx := &PrimeContext{}
	output := FormatPrimeContext(ctx)

	// Quick command reference should always appear
	checks := []string{
		"Commands:",
		"thrum send",
		"thrum inbox",
		"thrum reply",
		"thrum status",
		"thrum wait",
		"thrum <cmd> --help",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output missing %q:\n%s", check, output)
		}
	}
}

func TestFormatPrimeContext_NoSyncState(t *testing.T) {
	ctx := &PrimeContext{}
	output := FormatPrimeContext(ctx)

	// Should not contain "Daemon:" when SyncState is nil
	if strings.Contains(output, "Daemon:") {
		t.Errorf("unexpected 'Daemon:' in output when SyncState is nil:\n%s", output)
	}
}

func TestFormatPrimeContext_ListenerInstruction_ClaudeRuntime(t *testing.T) {
	// Create temp dir with identity file to simulate registered agent
	tmpDir := t.TempDir()
	identDir := tmpDir + "/.thrum/identities"
	if err := os.MkdirAll(identDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identDir+"/test_agent.json", []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}

	ctx := &PrimeContext{
		Identity: &WhoamiResult{
			AgentID: "test_agent",
			Role:    "impl",
			Module:  "auth",
		},
		RepoPath: tmpDir,
		Runtime:  "claude",
	}

	output := FormatPrimeContext(ctx)

	checks := []string{
		"ACTION REQUIRED",
		"Start background message listener",
		"message-listener",
		"--timeout 15m",
		tmpDir,
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output missing %q:\n%s", check, output)
		}
	}
}

func TestFormatPrimeContext_NoListenerInstruction_NonClaudeRuntime(t *testing.T) {
	ctx := &PrimeContext{
		Identity: &WhoamiResult{
			AgentID: "test_agent",
			Role:    "impl",
			Module:  "auth",
		},
		RepoPath: "/tmp/test",
		Runtime:  "cursor",
	}

	output := FormatPrimeContext(ctx)

	if strings.Contains(output, "Listener:") {
		t.Errorf("should not contain listener instruction for non-claude runtime:\n%s", output)
	}
}

func TestFormatPrimeContext_NoListenerInstruction_NoIdentity(t *testing.T) {
	ctx := &PrimeContext{
		Runtime: "claude",
	}

	output := FormatPrimeContext(ctx)

	if strings.Contains(output, "Listener:") {
		t.Errorf("should not contain listener instruction without identity:\n%s", output)
	}
}

func TestGetGitWorkContext(t *testing.T) {
	// This test runs in the actual repo, so it should find git context
	wc := getGitWorkContext()

	if wc.Branch == "" && wc.Error == "" {
		t.Error("expected either branch or error to be set")
	}

	if wc.Error == "" && wc.Branch == "" {
		t.Error("expected branch to be set when no error")
	}
}
