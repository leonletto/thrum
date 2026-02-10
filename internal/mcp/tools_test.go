package mcp

import (
	"testing"
	"time"
)

func TestParseMentionRole(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"@ops", "ops"},
		{"@reviewer", "reviewer"},
		{"ops", "ops"},
		{"reviewer", "reviewer"},
		{"agent:ops:abc123", "ops"},
		{"agent:reviewer:xyz", "reviewer"},
		{"agent:", ""},
		{"@", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseMentionRole(tt.input)
			if got != tt.expected {
				t.Errorf("parseMentionRole(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestIsValidPriority(t *testing.T) {
	valid := []string{"critical", "high", "normal", "low"}
	for _, p := range valid {
		if !isValidPriority(p) {
			t.Errorf("expected %q to be valid", p)
		}
	}

	invalid := []string{"", "urgent", "NORMAL", "medium", "1"}
	for _, p := range invalid {
		if isValidPriority(p) {
			t.Errorf("expected %q to be invalid", p)
		}
	}
}

func TestDeriveAgentStatusThreshold(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		elapsed  time.Duration
		expected string
	}{
		{"1 min ago (active)", 1 * time.Minute, "active"},
		{"119 seconds ago (active)", 119 * time.Second, "active"},
		{"3 min ago (offline)", 3 * time.Minute, "offline"},
		{"10 min ago (offline)", 10 * time.Minute, "offline"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastSeen := now.Add(-tt.elapsed).Format(time.RFC3339Nano)
			got := deriveAgentStatus(lastSeen, now)
			if got != tt.expected {
				t.Errorf("deriveAgentStatus(-%v) = %q, want %q", tt.elapsed, got, tt.expected)
			}
		})
	}
}
