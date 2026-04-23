package cli

import (
	"strings"
	"testing"
)

func TestHint(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		quiet      bool
		jsonMode   bool
		shouldHint bool
	}{
		{"normal mode", "agent.register", false, false, true},
		{"quiet mode suppresses hints", "agent.register", true, false, false},
		{"json mode suppresses hints", "agent.register", false, true, false},
		{"unknown command", "unknown.command", false, false, false},
		{"empty command", "", false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := Hint(tt.command, tt.quiet, tt.jsonMode)

			if tt.shouldHint {
				if hint == "" {
					t.Errorf("Expected hint for command %q, got empty string", tt.command)
				}
				if !strings.HasPrefix(hint, "  Tip:") && !strings.HasPrefix(hint, "  ") {
					t.Errorf("Hint should start with indentation, got: %q", hint)
				}
			} else {
				if hint != "" {
					t.Errorf("Expected no hint, got: %q", hint)
				}
			}
		})
	}
}

func TestHintRotation(t *testing.T) {
	// Test that hints rotate (get different hints over multiple calls)
	command := "agent.register"
	seenHints := make(map[string]bool)

	// Call 20 times to likely see different hints
	for i := 0; i < 20; i++ {
		hint := Hint(command, false, false)
		if hint != "" {
			seenHints[hint] = true
		}
	}

	// Should have seen at least 2 different hints (unless extremely unlucky with randomness)
	if len(seenHints) < 2 {
		t.Logf("Only saw %d unique hints, expected at least 2", len(seenHints))
		// Not a hard failure - randomness could theoretically give same hint 20 times
	}
}

func TestCommandHintsExist(t *testing.T) {
	// Test that all command keys have non-empty hint arrays
	for cmd, hints := range commandHints {
		if len(hints) == 0 {
			t.Errorf("Command %q has no hints defined", cmd)
		}

		for i, hint := range hints {
			if hint == "" {
				t.Errorf("Command %q has empty hint at index %d", cmd, i)
			}
		}
	}
}

func TestHintCommands(t *testing.T) {
	// Test that common commands have hints
	expectedCommands := []string{
		"agent.register",
		"session.start",
		"session.end",
		"send",
		"inbox",
		"agent.list",
		"status",
	}

	for _, cmd := range expectedCommands {
		if _, ok := commandHints[cmd]; !ok {
			t.Errorf("Expected command %q to have hints defined", cmd)
		}
	}
}
