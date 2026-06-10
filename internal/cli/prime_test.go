package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

// TestFormatPrimeContext_Preamble_NilIdentityFallsBackToOnDisk is the thrum-t6qx
// 3rd-site guard (backport via thrum-4ye2). The role preamble (agent
// instructions / role discipline) is LOCAL state consumed during prime, but
// FormatPrimeContext gated it on the daemon-resolved ctx.Identity. After a raw
// self-restart relaunch the daemon has not re-bound the new PID (whoami nil →
// Identity nil), so the agent would lose its entire role discipline despite the
// identity + preamble being on disk. The consume must fall back to the on-disk
// identity.
func TestFormatPrimeContext_Preamble_NilIdentityFallsBackToOnDisk(t *testing.T) {
	t.Setenv("THRUM_NAME", "alpha")
	repo := t.TempDir()
	thrumDir := filepath.Join(repo, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "context"), 0o700); err != nil {
		t.Fatalf("mkdir context: %v", err)
	}
	if err := config.SaveIdentityFile(thrumDir, &config.IdentityFile{
		Version: 5, RepoID: "r",
		Agent:    config.AgentConfig{Name: "alpha", Role: "implementer", Module: "test"},
		Worktree: thrumDir, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save identity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "context", "alpha_preamble.md"),
		[]byte("ROLE DISCIPLINE BODY"), 0o600); err != nil {
		t.Fatalf("write preamble: %v", err)
	}

	// Daemon-race: nil Identity, but the on-disk identity + preamble are present.
	out := FormatPrimeContext(&PrimeContext{Identity: nil, RepoPath: repo})
	if !strings.Contains(out, "# Agent Instructions") || !strings.Contains(out, "ROLE DISCIPLINE BODY") {
		t.Errorf("preamble must load via on-disk identity fallback when Identity is nil; got:\n%s", out)
	}
}

// TestPrimeContext_LocalAgentName covers the thrum-t6qx resolution ladder:
// daemon identity wins, on-disk identity is the fallback, "" when neither.
func TestPrimeContext_LocalAgentName(t *testing.T) {
	t.Run("daemon identity wins", func(t *testing.T) {
		got := (&PrimeContext{Identity: &WhoamiResult{AgentID: "from_daemon"}, RepoPath: t.TempDir()}).LocalAgentName()
		if got != "from_daemon" {
			t.Errorf("got %q, want from_daemon", got)
		}
	})
	t.Run("on-disk fallback when Identity nil", func(t *testing.T) {
		t.Setenv("THRUM_NAME", "from_disk")
		repo := t.TempDir()
		thrumDir := filepath.Join(repo, ".thrum")
		if err := os.MkdirAll(thrumDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := config.SaveIdentityFile(thrumDir, &config.IdentityFile{
			Version: 5, RepoID: "r",
			Agent:    config.AgentConfig{Name: "from_disk", Role: "implementer", Module: "test"},
			Worktree: thrumDir, UpdatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("save identity: %v", err)
		}
		if got := (&PrimeContext{Identity: nil, RepoPath: repo}).LocalAgentName(); got != "from_disk" {
			t.Errorf("got %q, want from_disk", got)
		}
	})
	t.Run("empty when neither available", func(t *testing.T) {
		if got := (&PrimeContext{Identity: nil, RepoPath: t.TempDir()}).LocalAgentName(); got != "" {
			t.Errorf("got %q, want empty", got)
		}
		var nilCtx *PrimeContext
		if nilCtx.LocalAgentName() != "" {
			t.Error("nil receiver must resolve to empty")
		}
	})
}

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
		[]byte("## Test Preamble\nTest instructions."), 0o600)

	// Create project_state.md
	os.WriteFile(thrumDir+"/context/project_state.md",
		[]byte("# Project State — test\n\n**Version:** v1.0.0\n"), 0o600)

	// Create an identity file (so listener section triggers in multi-agent)
	os.WriteFile(thrumDir+"/identities/test_agent.json",
		[]byte(`{"agent_id":"test_agent"}`), 0o600)
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

func TestFormatPrimeContext_TmuxMode(t *testing.T) {
	repoPath := t.TempDir()
	setupPrimeTestFiles(t, repoPath)

	ctx := &PrimeContext{
		Identity: &WhoamiResult{AgentID: "test_agent"},
		Runtime:  "claude",
		TmuxMode: true,
		RepoPath: repoPath,
	}

	output := FormatPrimeContext(ctx)

	// Should NOT contain listener spawn instructions
	if strings.Contains(output, "Background Message Listener") {
		t.Error("tmux-mode should not include listener spawn instructions")
	}
	if strings.Contains(output, "message-listener") {
		t.Error("tmux-mode should not reference message-listener subagent")
	}

	// Should contain tmux-mode instructions
	if !strings.Contains(output, "tmux-managed session") {
		t.Error("tmux-mode should include tmux-managed session notice")
	}
	if !strings.Contains(output, "do NOT spawn a background listener") {
		t.Error("tmux-mode should explicitly say not to spawn listener")
	}
}

func TestFormatPrimeContext_LegacyMode(t *testing.T) {
	repoPath := t.TempDir()
	setupPrimeTestFiles(t, repoPath)

	ctx := &PrimeContext{
		Identity:        &WhoamiResult{AgentID: "test_agent"},
		Runtime:         "claude",
		SingleAgentMode: false,
		TmuxMode:        false,
		RepoPath:        repoPath,
	}

	output := FormatPrimeContext(ctx)

	// Legacy mode should still include listener instructions
	if !strings.Contains(output, "Background Message Listener") {
		t.Error("legacy mode should include listener spawn instructions")
	}
}

func TestFormatPrimeContext_RestartSnapshot(t *testing.T) {
	ctx := &PrimeContext{
		RestartSnapshot: "# Restart Snapshot — test\n\n=== USER ===\nHello\n\n=== ASSISTANT ===\nHi",
	}
	output := FormatPrimeContext(ctx)
	if !strings.Contains(output, "# Previous Session Context") {
		t.Error("missing Previous Session Context section (heading is load-bearing for hook grep)")
	}
	if !strings.Contains(output, "ACTION REQUIRED") {
		t.Error("missing loud action-required framing on restart snapshot")
	}
	if !strings.Contains(output, "Resume Plan") {
		t.Error("missing Resume Plan pointer in framing")
	}
	if !strings.Contains(output, "=== USER ===") {
		t.Error("restart snapshot content not included")
	}
	if !strings.Contains(output, "Hello") {
		t.Error("restart snapshot conversation not included")
	}
}

func TestFormatPrimeContext_NoRestartSnapshot(t *testing.T) {
	ctx := &PrimeContext{}
	output := FormatPrimeContext(ctx)
	if strings.Contains(output, "Previous Session Context") {
		t.Error("should not include restart section when no snapshot")
	}
}

// TestContextPrime_RuntimePrefersProcessTree asserts that when the injected
// detector returns a non-empty runtime, ContextPrime uses it over the
// repo-based detection.
func TestContextPrime_RuntimePrefersProcessTree(t *testing.T) {
	// Swap detector to return "codex".
	orig := detectAncestor
	detectAncestor = func(_ context.Context) (int, string) { return 12345, "codex" }
	t.Cleanup(func() { detectAncestor = orig })

	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, ".thrum"), 0750); err != nil {
		t.Fatal(err)
	}

	// ContextPrime uses os.Getwd internally; chdir to the tmp dir for
	// isolation. Pin THRUM_HOME so paths.EffectiveRepoPath doesn't redirect.
	t.Setenv("THRUM_HOME", tmpDir)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	ctx := ContextPrime(nil)
	if ctx.Runtime != "codex" {
		t.Errorf("ctx.Runtime = %q, want codex", ctx.Runtime)
	}
}

// TestFormatPrimeContext_ProjectStateFollowsRedirect pins thrum-92mj:
// feature-worktree agents must see `.thrum/context/project_state.md`
// content in their prime output. Pre-fix, prime.go joined ctx.RepoPath
// directly with `.thrum/context/project_state.md`; in a worktree the
// file doesn't exist there (it lives in the main repo), os.ReadFile
// returned "not exist", and the whole Project State section was
// silently skipped — agents started sessions blind to repo structure
// and decisions. Fix: paths.ResolveThrumDir follows `.thrum/redirect`.
func TestFormatPrimeContext_ProjectStateFollowsRedirect(t *testing.T) {
	// Stand up a realistic main-repo + worktree layout:
	//   <tmp>/main/.thrum/context/project_state.md   (the shared file)
	//   <tmp>/worktree/.thrum/redirect               (→ <tmp>/main/.thrum)
	// The test runs ContextPrime() from inside the worktree and
	// asserts the project-state content surfaces in FormatPrimeContext
	// output.
	tmpDir := t.TempDir()
	mainRepo := filepath.Join(tmpDir, "main")
	mainThrum := filepath.Join(mainRepo, ".thrum")
	mainContextDir := filepath.Join(mainThrum, "context")
	if err := os.MkdirAll(mainContextDir, 0o750); err != nil {
		t.Fatalf("mkdir main context: %v", err)
	}
	const projectStateContent = "# Project State Summary\n\nShared across all worktrees (thrum-92mj marker).\n"
	projectStatePath := filepath.Join(mainContextDir, "project_state.md")
	if err := os.WriteFile(projectStatePath, []byte(projectStateContent), 0o600); err != nil {
		t.Fatalf("write project_state.md: %v", err)
	}

	worktree := filepath.Join(tmpDir, "worktree")
	worktreeThrum := filepath.Join(worktree, ".thrum")
	if err := os.MkdirAll(worktreeThrum, 0o750); err != nil {
		t.Fatalf("mkdir worktree .thrum: %v", err)
	}
	redirectPath := filepath.Join(worktreeThrum, "redirect")
	if err := os.WriteFile(redirectPath, []byte(mainThrum+"\n"), 0o600); err != nil {
		t.Fatalf("write redirect: %v", err)
	}

	// Pin THRUM_HOME to the synthetic worktree so paths.EffectiveRepoPath
	// doesn't override ctx.RepoPath with the real repo in CI/dev shells
	// (same pattern as TestContextPrime_RuntimePrefersProcessTree).
	t.Setenv("THRUM_HOME", worktree)

	// Sanity-check: before the fix this same setup would have the
	// worktree's .thrum/context/project_state.md missing — prove it by
	// confirming no such file exists. If this assertion ever fails,
	// the test's mental model is off.
	if _, err := os.Stat(filepath.Join(worktreeThrum, "context", "project_state.md")); !os.IsNotExist(err) {
		t.Fatalf("worktree-local project_state.md unexpectedly present (err=%v); test setup invalid", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(worktree); err != nil {
		t.Fatalf("chdir worktree: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	ctx := ContextPrime(nil)
	output := FormatPrimeContext(ctx)

	if !strings.Contains(output, "# Project State") {
		t.Errorf("missing '# Project State' header — section skipped?\n%s", output)
	}
	if !strings.Contains(output, "thrum-92mj marker") {
		t.Errorf("missing project_state.md content from the MAIN repo in output — redirect not followed:\n%s", output)
	}
}

// TestFormatPrimeContext_ProjectStateLocalMainRepo verifies the
// non-redirected case: an agent running in the main repo itself
// (no .thrum/redirect) still finds the same file via paths.ResolveThrumDir
// returning the local .thrum/ directory. Guards against the fix
// accidentally breaking the main-repo path.
func TestFormatPrimeContext_ProjectStateLocalMainRepo(t *testing.T) {
	tmpDir := t.TempDir()
	contextDir := filepath.Join(tmpDir, ".thrum", "context")
	if err := os.MkdirAll(contextDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const body = "# Project State\n\nmain-repo direct read (thrum-92mj marker).\n"
	if err := os.WriteFile(filepath.Join(contextDir, "project_state.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Pin THRUM_HOME so paths.EffectiveRepoPath doesn't override with a
	// real repo path from the ambient environment.
	t.Setenv("THRUM_HOME", tmpDir)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	ctx := ContextPrime(nil)
	output := FormatPrimeContext(ctx)
	if !strings.Contains(output, "thrum-92mj marker") {
		t.Errorf("main-repo project_state.md content missing:\n%s", output)
	}
}
