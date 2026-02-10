package identity_test

import (
	"testing"

	"github.com/leonletto/thrum/internal/identity"
)

// TestAgentIDFormats ensures backward compatibility with all agent ID formats:
// 1. Legacy format: "agent:role:hash" (e.g., "agent:coordinator:1B9K33T6RK")
// 2. Current unnamed format: "role_hash" (e.g., "coordinator_1B9K33T6RK")
// 3. Current named format: just the name (e.g., "furiosa").
func TestAgentIDFormats(t *testing.T) {
	tests := []struct {
		name             string
		agentID          string
		expectedIsLegacy bool
		expectedRole     string
		expectedHash     string
	}{
		{
			name:             "Legacy format with coordinator role",
			agentID:          "agent:coordinator:1B9K33T6RK",
			expectedIsLegacy: true,
			expectedRole:     "coordinator",
			expectedHash:     "1B9K33T6RK",
		},
		{
			name:             "Legacy format with owner role",
			agentID:          "agent:owner:6J91YTRZZN",
			expectedIsLegacy: true,
			expectedRole:     "owner",
			expectedHash:     "6J91YTRZZN",
		},
		{
			name:             "Legacy format with planner role",
			agentID:          "agent:planner:9TCG9YTRGS",
			expectedIsLegacy: true,
			expectedRole:     "planner",
			expectedHash:     "9TCG9YTRGS",
		},
		{
			name:             "Current unnamed format",
			agentID:          "implementer_35HV62T9B9",
			expectedIsLegacy: false,
			expectedRole:     "implementer",
			expectedHash:     "35HV62T9B9",
		},
		{
			name:             "Current named format",
			agentID:          "furiosa",
			expectedIsLegacy: false,
			expectedRole:     "", // Named agents don't have a role in their ID
			expectedHash:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, hash := identity.ParseAgentID(tt.agentID)

			if role != tt.expectedRole {
				t.Errorf("ParseAgentID() role = %q, want %q", role, tt.expectedRole)
			}

			if hash != tt.expectedHash {
				t.Errorf("ParseAgentID() hash = %q, want %q", hash, tt.expectedHash)
			}
		})
	}
}

// TestAgentIDToName tests conversion of agent IDs to filenames.
func TestAgentIDToName(t *testing.T) {
	tests := []struct {
		name     string
		agentID  string
		expected string
	}{
		{
			name:     "Legacy format converts to current unnamed format",
			agentID:  "agent:coordinator:1B9K33T6RK",
			expected: "coordinator_1B9K33T6RK",
		},
		{
			name:     "Current unnamed format unchanged",
			agentID:  "coordinator_1B9K33T6RK",
			expected: "coordinator_1B9K33T6RK",
		},
		{
			name:     "Named format unchanged",
			agentID:  "furiosa",
			expected: "furiosa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := identity.AgentIDToName(tt.agentID)

			if result != tt.expected {
				t.Errorf("AgentIDToName(%q) = %q, want %q", tt.agentID, result, tt.expected)
			}
		})
	}
}

// TestDisplayName tests extraction of display name from agent ID.
func TestDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		agentID  string
		expected string
	}{
		{
			name:     "Legacy format shows @role",
			agentID:  "agent:coordinator:1B9K33T6RK",
			expected: "@coordinator",
		},
		{
			name:     "Current unnamed format shows @role",
			agentID:  "implementer_35HV62T9B9",
			expected: "@implementer",
		},
		{
			name:     "Named format shows @name",
			agentID:  "furiosa",
			expected: "@furiosa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := identity.ExtractDisplayName(tt.agentID)

			if result != tt.expected {
				t.Errorf("ExtractDisplayName(%q) = %q, want %q", tt.agentID, result, tt.expected)
			}
		})
	}
}
