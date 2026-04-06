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

func TestFormatPrimeContext_AllReadSuppressesRecent(t *testing.T) {
	ctx := &PrimeContext{
		Messages: &MessagesInfo{
			Unread: 0,
			Total:  15,
			Recent: []Message{
				{
					MessageID: "msg_1",
					AgentID:   "test:coordinator:main",
					Body: struct {
						Format     string `json:"format"`
						Content    string `json:"content"`
						Structured string `json:"structured,omitempty"`
					}{
						Content: "This should not appear in output",
					},
				},
			},
		},
	}

	output := FormatPrimeContext(ctx)

	if !strings.Contains(output, "15 messages (all read)") {
		t.Errorf("expected '15 messages (all read)' in output:\n%s", output)
	}
	if strings.Contains(output, "This should not appear") {
		t.Errorf("recent messages should be suppressed when all read:\n%s", output)
	}
	if strings.Contains(output, "@coordinator") {
		t.Errorf("message sender should not appear when all read:\n%s", output)
	}
}

func TestFormatPrimeContext_UnreadShowsRecent(t *testing.T) {
	ctx := &PrimeContext{
		Messages: &MessagesInfo{
			Unread: 2,
			Total:  10,
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
	}

	output := FormatPrimeContext(ctx)

	if !strings.Contains(output, "2 unread") {
		t.Errorf("expected '2 unread' in output:\n%s", output)
	}
	if !strings.Contains(output, "Please review PR #42") {
		t.Errorf("expected recent message content when unread > 0:\n%s", output)
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
	// Commands now only appear in multi-agent section 5
	tmpDir := t.TempDir()
	identDir := tmpDir + "/.thrum/identities"
	os.MkdirAll(identDir, 0750)
	os.WriteFile(identDir+"/test_agent.json", []byte(`{"version":1}`), 0600)

	ctx := &PrimeContext{
		Identity: &WhoamiResult{AgentID: "test_agent", Role: "impl"},
		RepoPath: tmpDir,
		Runtime:  "claude",
	}
	output := FormatPrimeContext(ctx)

	checks := []string{
		"thrum send",
		"thrum inbox",
		"thrum reply",
		"thrum status",
		"thrum team",
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

	// Also create a context file to test session context inlining
	contextDir := tmpDir + "/.thrum/context"
	if err := os.MkdirAll(contextDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contextDir+"/test_agent.md", []byte("# Context"), 0600); err != nil {
		t.Fatal(err)
	}

	ctx := &PrimeContext{
		Identity: &WhoamiResult{
			AgentID: "test_agent",
			Role:    "impl",
			Module:  "auth",
		},
		RepoPath:            tmpDir,
		Runtime:             "claude",
		SavedSessionContext: "# Context",
	}

	output := FormatPrimeContext(ctx)

	checks := []string{
		"Multi-Agent Messaging Protocol",
		"Start Background Message Listener",
		"message-listener",
		"--timeout 8m",
		tmpDir,
		"Session Context",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output missing %q:\n%s", check, output)
		}
	}
}

func TestFormatPrimeContext_NoListenerInSingleAgentMode(t *testing.T) {
	// Create temp dir with identity but single-agent mode enabled
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
		RepoPath:        tmpDir,
		Runtime:         "claude",
		SingleAgentMode: true,
	}

	output := FormatPrimeContext(ctx)

	// Should NOT have multi-agent protocol section (sections 5-6)
	if strings.Contains(output, "Multi-Agent Messaging Protocol") {
		t.Errorf("output should not contain messaging protocol in single-agent mode:\n%s", output)
	}
	// The "Start Background Message Listener" heading is section 6 — should be absent
	if strings.Contains(output, "Start Background Message Listener") {
		t.Errorf("output should not contain listener spawn section in single-agent mode:\n%s", output)
	}
	// Note: DefaultPreamble still contains "message-listener" until Task 9 strips it.
	// That's fine — the preamble is section 2, not sections 5-6.
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

// setupPrimeTestFiles creates the .thrum directory structure needed for prime tests.
func setupPrimeTestFiles(t *testing.T, repoPath string) {
	t.Helper()
	thrumDir := repoPath + "/.thrum"
	os.MkdirAll(thrumDir+"/context", 0750)
	os.MkdirAll(thrumDir+"/identities", 0750)

	// Create a preamble file
	os.WriteFile(thrumDir+"/context/test_agent_preamble.md",
		[]byte("## Test Preamble\nTest instructions."), 0644)

	// Create project_state.md
	os.WriteFile(thrumDir+"/context/project_state.md",
		[]byte("# Project State — test\n\n**Version:** v1.0.0\n"), 0644)

	// Create an identity file (so listener section triggers in multi-agent)
	os.WriteFile(thrumDir+"/identities/test_agent.json",
		[]byte(`{"agent_id":"test_agent"}`), 0644)
}

func TestPrimeOutputMultiAgent(t *testing.T) {
	repoPath := t.TempDir()
	setupPrimeTestFiles(t, repoPath)
	ctx := &PrimeContext{
		Identity:        &WhoamiResult{AgentID: "test_agent", Role: "coordinator"},
		Runtime:         "claude",
		RepoPath:        repoPath,
		SingleAgentMode: false,
	}

	output := FormatPrimeContext(ctx)
	if !strings.Contains(output, "Agent Instructions") {
		t.Error("missing Agent Instructions section")
	}
	if !strings.Contains(output, "Project State") {
		t.Error("missing Project State section")
	}
	if !strings.Contains(output, "Multi-Agent Messaging Protocol") {
		t.Error("missing Multi-Agent Messaging Protocol section")
	}
	if !strings.Contains(output, "Listener Rules") {
		t.Error("missing Listener Rules section")
	}
	if !strings.Contains(output, "message-listener") {
		t.Error("missing message-listener spawn instruction")
	}
}

func TestPrimeOutputSingleAgent(t *testing.T) {
	repoPath := t.TempDir()
	setupPrimeTestFiles(t, repoPath)
	ctx := &PrimeContext{
		Identity:        &WhoamiResult{AgentID: "test_agent", Role: "coordinator"},
		Runtime:         "claude",
		RepoPath:        repoPath,
		SingleAgentMode: true,
	}

	output := FormatPrimeContext(ctx)
	if !strings.Contains(output, "Agent Instructions") {
		t.Error("missing Agent Instructions section")
	}
	if !strings.Contains(output, "Project State") {
		t.Error("missing Project State section")
	}
	if strings.Contains(output, "Multi-Agent Messaging Protocol") {
		t.Error("should not contain Multi-Agent Messaging Protocol in single-agent mode")
	}
	if strings.Contains(output, "Listener Rules") {
		t.Error("should not contain Listener Rules in single-agent mode")
	}
	if strings.Contains(output, "Start Background Message Listener") {
		t.Error("should not contain listener spawn in single-agent mode")
	}
}

func TestPrimeOutputInlinesSessionContext(t *testing.T) {
	repoPath := t.TempDir()
	setupPrimeTestFiles(t, repoPath)
	ctx := &PrimeContext{
		Identity:            &WhoamiResult{AgentID: "test_agent", Role: "coordinator"},
		Runtime:             "claude",
		RepoPath:            repoPath,
		SingleAgentMode:     true,
		SavedSessionContext: "## Working on feature X\nNext: finish tests",
	}

	output := FormatPrimeContext(ctx)
	if !strings.Contains(output, "Session Context") {
		t.Error("missing Session Context section")
	}
	if !strings.Contains(output, "Working on feature X") {
		t.Error("session context content not inlined")
	}
}

func TestPrimeOutputInlinesPreambleFromFile(t *testing.T) {
	repoPath := t.TempDir()
	setupPrimeTestFiles(t, repoPath)
	ctx := &PrimeContext{
		Identity: &WhoamiResult{AgentID: "test_agent", Role: "coordinator"},
		Runtime:  "claude",
		RepoPath: repoPath,
	}

	output := FormatPrimeContext(ctx)
	// Should contain the preamble from the file
	if !strings.Contains(output, "Test Preamble") {
		t.Error("preamble file content not inlined in prime output")
	}
}

func TestPrimeOutputInlinesProjectState(t *testing.T) {
	repoPath := t.TempDir()
	setupPrimeTestFiles(t, repoPath)
	ctx := &PrimeContext{
		Identity: &WhoamiResult{AgentID: "test_agent", Role: "coordinator"},
		Runtime:  "claude",
		RepoPath: repoPath,
	}

	output := FormatPrimeContext(ctx)
	if !strings.Contains(output, "Project State — test") {
		t.Error("project_state.md content not inlined in prime output")
	}
	if !strings.Contains(output, "**Version:** v1.0.0") {
		t.Error("project state version not present")
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
