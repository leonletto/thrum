package cli

import (
	"strings"
	"testing"
)

func TestFormatOverview(t *testing.T) {
	tests := []struct {
		name     string
		result   OverviewResult
		contains []string
	}{
		{
			name: "full overview",
			result: OverviewResult{
				Health: HealthResult{
					Status:    "ok",
					UptimeMs:  3600000,
					Version:   "0.1.0",
					SyncState: "synced",
				},
				Agent: &WhoamiResult{
					AgentID:      "agent:implementer:auth",
					Role:         "implementer",
					Module:       "auth",
					SessionID:    "ses_01HXE...",
					SessionStart: "2026-02-03T10:00:00Z",
				},
				WorkContext: &AgentWorkContext{
					Intent:      "Implementing login flow",
					CurrentTask: "beads:thrum-xyz",
					Branch:      "feature/auth",
					UnmergedCommits: []CommitSummary{
						{SHA: "abc1234", Message: "test"},
					},
					ChangedFiles: []string{"auth.go", "handler.go"},
				},
				Team: []AgentWorkContext{
					{
						AgentID:      "agent:reviewer:auth",
						Branch:       "feature/auth",
						Intent:       "Reviewing PR #42",
						GitUpdatedAt: "2026-02-03T11:55:00Z",
					},
				},
				Inbox: &struct {
					Total  int `json:"total"`
					Unread int `json:"unread"`
				}{
					Total:  12,
					Unread: 3,
				},
			},
			contains: []string{
				"@implementer",
				"active",
				"Implementing login flow",
				"beads:thrum-xyz",
				"feature/auth",
				"Team:",
				"@reviewer",
				"3 unread",
				"synced",
			},
		},
		{
			name: "no session",
			result: OverviewResult{
				Health: HealthResult{
					Status:    "ok",
					SyncState: "synced",
				},
				Agent: &WhoamiResult{
					AgentID: "agent:tester:core",
					Role:    "tester",
				},
			},
			contains: []string{"@tester", "none"},
		},
		{
			name: "not registered",
			result: OverviewResult{
				Health: HealthResult{
					Status:    "ok",
					SyncState: "synced",
				},
			},
			contains: []string{"Not registered"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatOverview(&tt.result)
			for _, substr := range tt.contains {
				if !strings.Contains(output, substr) {
					t.Errorf("Output should contain '%s', got:\n%s", substr, output)
				}
			}
		})
	}
}
