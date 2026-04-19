package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/cli"
)

func TestPrintAgentSummaryField(t *testing.T) {
	s := &cli.AgentSummary{
		AgentID:     "bob",
		Role:        "impl",
		TmuxAlive:   true,
		PID:         9001,
		TmuxSession: "bob:0.0",
		Host:        "laptop.local",
	}
	cases := []struct {
		field, want string
	}{
		{"agent_id", "bob\n"},
		{"role", "impl\n"},
		{"tmux_alive", "true\n"},
		{"pid", "9001\n"},
		{"tmux_session", "bob:0.0\n"},
		{"host", "laptop.local\n"},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		if err := printAgentSummaryField(&buf, s, tc.field); err != nil {
			t.Fatalf("field %q: %v", tc.field, err)
		}
		if buf.String() != tc.want {
			t.Fatalf("field %q: got %q, want %q", tc.field, buf.String(), tc.want)
		}
	}

	var buf bytes.Buffer
	err := printAgentSummaryField(&buf, s, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("error message should mention 'unknown field': got %q", err.Error())
	}
}
