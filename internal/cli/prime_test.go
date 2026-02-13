package cli

import (
	"encoding/json"
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
	for _, section := range []string{"identity", "session", "agents", "messages", "work_context"} {
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
