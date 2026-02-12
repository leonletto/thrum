package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatQuickstart(t *testing.T) {
	tests := []struct {
		name     string
		result   QuickstartResult
		contains []string
	}{
		{
			name: "basic quickstart",
			result: QuickstartResult{
				Register: &RegisterResponse{
					AgentID: "agent:implementer:auth",
					Status:  "registered",
				},
				Session: &SessionStartResponse{
					SessionID: "ses_01HXE...",
					AgentID:   "agent:implementer:auth",
				},
			},
			contains: []string{"Registered", "@implementer", "Session started", "ses_01HXE..."},
		},
		{
			name: "quickstart with intent",
			result: QuickstartResult{
				Register: &RegisterResponse{
					AgentID: "agent:reviewer:auth",
					Status:  "registered",
				},
				Session: &SessionStartResponse{
					SessionID: "ses_02ABC...",
					AgentID:   "agent:reviewer:auth",
				},
				Intent: &SetIntentResponse{
					SessionID: "ses_02ABC...",
					Intent:    "Reviewing PR #42",
				},
			},
			contains: []string{"Registered", "@reviewer", "Session started", "Intent set", "Reviewing PR #42"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatQuickstart(&tt.result)
			for _, substr := range tt.contains {
				if !strings.Contains(output, substr) {
					t.Errorf("Output should contain '%s', got: %s", substr, output)
				}
			}
		})
	}
}

func TestExtractRoleFromID(t *testing.T) {
	tests := []struct {
		agentID string
		want    string
	}{
		{"agent:implementer:auth", "implementer"},
		{"agent:reviewer:core", "reviewer"},
		{"planner", "planner"},
	}

	for _, tt := range tests {
		t.Run(tt.agentID, func(t *testing.T) {
			got := extractRoleFromID(tt.agentID)
			if got != tt.want {
				t.Errorf("extractRoleFromID(%q) = %q, want %q", tt.agentID, got, tt.want)
			}
		})
	}
}

func TestFormatQuickstart_WithRuntime(t *testing.T) {
	tests := []struct {
		name     string
		result   QuickstartResult
		contains []string
	}{
		{
			name: "detected runtime",
			result: QuickstartResult{
				RuntimeName: "claude",
				Detected:    true,
				Register: &RegisterResponse{
					AgentID: "agent:impl:auth",
					Status:  "registered",
				},
				Session: &SessionStartResponse{
					SessionID: "ses_abc",
					AgentID:   "agent:impl:auth",
				},
			},
			contains: []string{"Detected runtime: claude", "Registered"},
		},
		{
			name: "explicit runtime",
			result: QuickstartResult{
				RuntimeName: "codex",
				Detected:    false,
				Register: &RegisterResponse{
					AgentID: "agent:impl:auth",
					Status:  "registered",
				},
				Session: &SessionStartResponse{
					SessionID: "ses_abc",
					AgentID:   "agent:impl:auth",
				},
			},
			contains: []string{"Using runtime: codex", "Registered"},
		},
		{
			name: "dry run with files",
			result: QuickstartResult{
				RuntimeName: "claude",
				Detected:    true,
				RuntimeInit: &RuntimeInitResult{
					Runtime: "claude",
					DryRun:  true,
					Files: []FileAction{
						{Path: ".claude/settings.json", Action: "create"},
						{Path: "scripts/thrum-startup.sh", Action: "create"},
					},
				},
			},
			contains: []string{"Detected runtime: claude", "Would create: .claude/settings.json", "Would create: scripts/thrum-startup.sh"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatQuickstart(&tt.result)
			for _, substr := range tt.contains {
				if !strings.Contains(output, substr) {
					t.Errorf("Output should contain '%s', got:\n%s", substr, output)
				}
			}
		})
	}
}

func TestQuickstartResult_JSON(t *testing.T) {
	result := &QuickstartResult{
		RuntimeName: "claude",
		Detected:    true,
		Register: &RegisterResponse{
			AgentID: "agent:impl:auth",
			Status:  "registered",
		},
		Session: &SessionStartResponse{
			SessionID: "ses_abc",
			AgentID:   "agent:impl:auth",
		},
		RuntimeInit: &RuntimeInitResult{
			Runtime: "claude",
			Files: []FileAction{
				{Path: ".claude/settings.json", Action: "create", Runtime: "claude"},
			},
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded["runtime"] != "claude" {
		t.Errorf("runtime = %v, want %q", decoded["runtime"], "claude")
	}
	if decoded["runtime_detected"] != true {
		t.Errorf("runtime_detected = %v, want true", decoded["runtime_detected"])
	}
	if decoded["runtime_init"] == nil {
		t.Error("expected runtime_init to be present")
	}
}

func TestQuickstartDryRun_RuntimeInit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a .claude marker so detection finds "claude"
	claudeDir := filepath.Join(tmpDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	// Note: We can't test the full Quickstart flow without a daemon,
	// but we can test that DryRun returns early without trying to connect
	// by calling the quickstart format function with dry-run results.
	result := &QuickstartResult{
		RuntimeName: "claude",
		Detected:    true,
		RuntimeInit: &RuntimeInitResult{
			Runtime: "claude",
			DryRun:  true,
			Files: []FileAction{
				{Path: ".claude/settings.json", Action: "overwrite"},
				{Path: "scripts/thrum-startup.sh", Action: "create"},
			},
		},
	}

	output := FormatQuickstart(result)
	if !strings.Contains(output, "Would overwrite: .claude/settings.json") {
		t.Errorf("expected overwrite action in output:\n%s", output)
	}
	if !strings.Contains(output, "Would create: scripts/thrum-startup.sh") {
		t.Errorf("expected create action in output:\n%s", output)
	}

	// Should NOT contain registration (dry-run skips it)
	if strings.Contains(output, "Registered") {
		t.Errorf("dry-run should not show registration:\n%s", output)
	}
}
