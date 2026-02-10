package cli

import (
	"testing"
)

func TestGetTerminalWidth(t *testing.T) {
	// Test that GetTerminalWidth returns a positive value
	width := GetTerminalWidth()
	if width <= 0 {
		t.Errorf("GetTerminalWidth() returned %d, expected positive number", width)
	}

	// Width should be at least the default
	if width < 80 {
		t.Errorf("GetTerminalWidth() returned %d, expected at least 80", width)
	}
}

func TestGetWidthFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected int
	}{
		{"valid width", "120", 120},
		{"small width", "40", 40},
		{"invalid value", "abc", 0},
		{"empty value", "", 0},
		{"zero", "0", 0},
		{"negative", "-1", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set COLUMNS env var
			if tt.envValue != "" {
				t.Setenv("COLUMNS", tt.envValue)
			}

			got := getWidthFromEnv()
			if got != tt.expected {
				t.Errorf("getWidthFromEnv() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestGetWidthFromIOCTL(t *testing.T) {
	// Test that IOCTL returns either a valid width or 0
	// We can't guarantee it succeeds (might not be a TTY in test environment)
	width := getWidthFromIOCTL()
	if width < 0 {
		t.Errorf("getWidthFromIOCTL() returned %d, expected >= 0", width)
	}
}
