package cli

import (
	"testing"
)

// Additional tests to improve coverage

func TestFormatSyncForce_AllStates(t *testing.T) {
	tests := []struct {
		name     string
		response SyncForceResponse
		contains string
	}{
		{
			name: "synced_state",
			response: SyncForceResponse{
				Triggered:  true,
				SyncState:  "synced",
				LastSyncAt: "2026-02-03T12:30:00Z",
			},
			contains: "✓ synced",
		},
		{
			name: "idle_state",
			response: SyncForceResponse{
				Triggered: true,
				SyncState: "idle",
			},
			contains: "idle",
		},
		{
			name: "error_state",
			response: SyncForceResponse{
				Triggered: true,
				SyncState: "error",
			},
			contains: "✗ error",
		},
		{
			name: "stopped_state",
			response: SyncForceResponse{
				Triggered: true,
				SyncState: "stopped",
			},
			contains: "stopped",
		},
		{
			name: "unknown_state",
			response: SyncForceResponse{
				Triggered: true,
				SyncState: "unknown",
			},
			contains: "unknown",
		},
		{
			name: "no_last_sync",
			response: SyncForceResponse{
				Triggered:  true,
				SyncState:  "idle",
				LastSyncAt: "",
			},
			contains: "triggered",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatSyncForce(&tt.response)
			if !contains(output, tt.contains) {
				t.Errorf("Output should contain '%s', got:\n%s", tt.contains, output)
			}
		})
	}
}

func TestFormatSyncStatus_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		response SyncStatusResponse
		contains []string
	}{
		{
			name: "not_running",
			response: SyncStatusResponse{
				Running:   false,
				SyncState: "stopped",
			},
			contains: []string{"✗ stopped", "never"},
		},
		{
			name: "with_error_no_last_sync",
			response: SyncStatusResponse{
				Running:   true,
				SyncState: "error",
				LastError: "connection timeout",
			},
			contains: []string{"error", "connection timeout", "never"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatSyncStatus(&tt.response)
			for _, substr := range tt.contains {
				if !contains(output, substr) {
					t.Errorf("Output should contain '%s'", substr)
				}
			}
		})
	}
}

func TestFormatSubscriptionsList_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		response ListSubscriptionsResponse
		contains []string
	}{
		{
			name: "all_subscription",
			response: ListSubscriptionsResponse{
				Subscriptions: []SubscriptionInfo{
					{
						ID:        1,
						All:       true,
						CreatedAt: "2026-02-03T10:00:00Z",
					},
				},
			},
			contains: []string{"All messages", "firehose"},
		},
		{
			name: "multiple_types",
			response: ListSubscriptionsResponse{
				Subscriptions: []SubscriptionInfo{
					{
						ID:         1,
						ScopeType:  "module",
						ScopeValue: "auth",
						CreatedAt:  "2026-02-03T10:00:00Z",
					},
					{
						ID:          2,
						MentionRole: "reviewer",
						CreatedAt:   "2026-02-03T10:05:00Z",
					},
					{
						ID:        3,
						All:       true,
						CreatedAt: "2026-02-03T10:10:00Z",
					},
				},
			},
			contains: []string{"module:auth", "@reviewer", "firehose"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatSubscriptionsList(&tt.response)
			for _, substr := range tt.contains {
				if !contains(output, substr) {
					t.Errorf("Output should contain '%s'", substr)
				}
			}
		})
	}
}

func TestFormatSessionStart_InvalidTime(t *testing.T) {
	result := SessionStartResponse{
		SessionID: "ses_123",
		AgentID:   "agent:test:123",
		StartedAt: "invalid-time",
	}

	output := FormatSessionStart(&result)

	// Should still contain the raw time string even if parsing fails
	if !contains(output, "invalid-time") {
		t.Error("Output should contain raw time string when parsing fails")
	}
}

func TestFormatSessionEnd_InvalidTime(t *testing.T) {
	result := SessionEndResponse{
		SessionID: "ses_123",
		EndedAt:   "invalid-time",
		Duration:  60000,
	}

	output := FormatSessionEnd(&result)

	// Should still contain the raw time string even if parsing fails
	if !contains(output, "invalid-time") {
		t.Error("Output should contain raw time string when parsing fails")
	}
}

func TestFormatAgentList_InvalidTimes(t *testing.T) {
	result := ListAgentsResponse{
		Agents: []AgentInfo{
			{
				AgentID:      "agent:test:123",
				Role:         "tester",
				Module:       "test",
				RegisteredAt: "invalid-time",
				LastSeenAt:   "also-invalid",
			},
		},
	}

	output := FormatAgentList(&result)

	// Should still show the agent info even with invalid times
	if !contains(output, "tester") {
		t.Error("Output should contain agent info even with invalid times")
	}
}

func TestFormatRegisterResponse_ConflictWithoutDetails(t *testing.T) {
	result := RegisterResponse{
		Status:   "conflict",
		Conflict: nil,
	}

	output := FormatRegisterResponse(&result)

	// Should show conflict message even without details
	if !contains(output, "conflict") {
		t.Error("Output should indicate conflict")
	}
}

func TestFormatRegisterResponse_Updated(t *testing.T) {
	result := RegisterResponse{
		AgentID: "agent:test:123",
		Status:  "updated",
	}

	output := FormatRegisterResponse(&result)

	// Should show re-registered message
	if !contains(output, "re-registered") {
		t.Error("Output should indicate re-registration")
	}
}

func TestFormatDaemonStatus_FullInfo(t *testing.T) {
	result := DaemonStatusResult{
		Running:   true,
		PID:       12345,
		Uptime:    "2h",
		Version:   "1.0.0",
		SyncState: "error",
	}

	output := FormatDaemonStatus(&result)

	expectedFields := []string{"running", "12345", "2h", "1.0.0", "error"}
	for _, field := range expectedFields {
		if !contains(output, field) {
			t.Errorf("Output should contain '%s'", field)
		}
	}
}
