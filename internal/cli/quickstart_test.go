package cli

import (
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
