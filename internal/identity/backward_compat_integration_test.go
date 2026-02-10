package identity_test

import (
	"testing"

	"github.com/leonletto/thrum/internal/identity"
)

// TestBackwardCompatibilityIntegration tests the complete backward compatibility story:
// Legacy events with "agent:role:hash" format should work seamlessly alongside
// new unnamed "role_hash" and named "name" formats.
func TestBackwardCompatibilityIntegration(t *testing.T) {
	testCases := []struct {
		name             string
		agentID          string
		expectedFilename string // What filename should be used for messages
		expectedDisplay  string // What should be shown to users
		expectedRole     string // What role should be extracted (if any)
		expectedHash     string // What hash should be extracted (if any)
		description      string
	}{
		{
			name:             "Legacy coordinator from actual events",
			agentID:          "agent:coordinator:1B9K33T6RK",
			expectedFilename: "coordinator_1B9K33T6RK",
			expectedDisplay:  "@coordinator",
			expectedRole:     "coordinator",
			expectedHash:     "1B9K33T6RK",
			description:      "Real legacy format from existing .thrum/messages.jsonl",
		},
		{
			name:             "Legacy owner from actual events",
			agentID:          "agent:owner:6J91YTRZZN",
			expectedFilename: "owner_6J91YTRZZN",
			expectedDisplay:  "@owner",
			expectedRole:     "owner",
			expectedHash:     "6J91YTRZZN",
			description:      "Real legacy format from existing .thrum/messages.jsonl",
		},
		{
			name:             "Legacy planner from actual events",
			agentID:          "agent:planner:9TCG9YTRGS",
			expectedFilename: "planner_9TCG9YTRGS",
			expectedDisplay:  "@planner",
			expectedRole:     "planner",
			expectedHash:     "9TCG9YTRGS",
			description:      "Real legacy format from existing .thrum/messages.jsonl",
		},
		{
			name:             "Current unnamed format",
			agentID:          "implementer_35HV62T9B9",
			expectedFilename: "implementer_35HV62T9B9",
			expectedDisplay:  "@implementer",
			expectedRole:     "implementer",
			expectedHash:     "35HV62T9B9",
			description:      "Current format for unnamed agents",
		},
		{
			name:             "Current named format",
			agentID:          "furiosa",
			expectedFilename: "furiosa",
			expectedDisplay:  "@furiosa",
			expectedRole:     "",
			expectedHash:     "",
			description:      "Current format for named agents",
		},
		{
			name:             "Named agent with underscores",
			agentID:          "max_rockatansky",
			expectedFilename: "max_rockatansky",
			expectedDisplay:  "@max_rockatansky",
			expectedRole:     "",
			expectedHash:     "",
			description:      "Named agents can have underscores",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test filename conversion
			filename := identity.AgentIDToName(tc.agentID)
			if filename != tc.expectedFilename {
				t.Errorf("AgentIDToName(%q) = %q, want %q\nDescription: %s",
					tc.agentID, filename, tc.expectedFilename, tc.description)
			}

			// Test display name extraction
			display := identity.ExtractDisplayName(tc.agentID)
			if display != tc.expectedDisplay {
				t.Errorf("ExtractDisplayName(%q) = %q, want %q\nDescription: %s",
					tc.agentID, display, tc.expectedDisplay, tc.description)
			}

			// Test role/hash parsing
			role, hash := identity.ParseAgentID(tc.agentID)
			if role != tc.expectedRole {
				t.Errorf("ParseAgentID(%q) role = %q, want %q\nDescription: %s",
					tc.agentID, role, tc.expectedRole, tc.description)
			}
			if hash != tc.expectedHash {
				t.Errorf("ParseAgentID(%q) hash = %q, want %q\nDescription: %s",
					tc.agentID, hash, tc.expectedHash, tc.description)
			}
		})
	}
}

// TestLegacyToCurrentConversion verifies that legacy IDs convert to the same
// filename as equivalent current unnamed IDs.
func TestLegacyToCurrentConversion(t *testing.T) {
	testCases := []struct {
		legacyID  string
		currentID string
	}{
		{
			legacyID:  "agent:coordinator:1B9K33T6RK",
			currentID: "coordinator_1B9K33T6RK",
		},
		{
			legacyID:  "agent:implementer:ABC123",
			currentID: "implementer_ABC123",
		},
		{
			legacyID:  "agent:reviewer:XYZ789",
			currentID: "reviewer_XYZ789",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.legacyID, func(t *testing.T) {
			legacyFilename := identity.AgentIDToName(tc.legacyID)
			currentFilename := identity.AgentIDToName(tc.currentID)

			if legacyFilename != currentFilename {
				t.Errorf("Legacy and current IDs should map to same filename:\n"+
					"  Legacy  %q -> %q\n"+
					"  Current %q -> %q\n"+
					"  Want: both -> %q",
					tc.legacyID, legacyFilename,
					tc.currentID, currentFilename,
					tc.currentID)
			}

			// Both should produce the current format
			if legacyFilename != tc.currentID {
				t.Errorf("AgentIDToName(%q) = %q, want %q",
					tc.legacyID, legacyFilename, tc.currentID)
			}
		})
	}
}

// TestNoRegressionOnExistingFormats ensures we didn't break current formats.
func TestNoRegressionOnExistingFormats(t *testing.T) {
	// Test unnamed format (already in use)
	unnamedID := "coordinator_1B9K33T6RK"
	if identity.AgentIDToName(unnamedID) != unnamedID {
		t.Errorf("Unnamed format should pass through unchanged")
	}

	// Test named format (new feature)
	namedID := "furiosa"
	if identity.AgentIDToName(namedID) != namedID {
		t.Errorf("Named format should pass through unchanged")
	}

	// Named agents should extract to their name, not a role
	displayName := identity.ExtractDisplayName(namedID)
	if displayName != "@furiosa" {
		t.Errorf("Named agent display should be @name, got %s", displayName)
	}
}
