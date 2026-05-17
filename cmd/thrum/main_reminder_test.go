package main

import (
	"strings"
	"testing"
	"time"
)

// TestParseFutureDuration_ValidShapes exercises the formats listed in
// reminder set's --in help: Go duration strings + "<N>d" days.
func TestParseFutureDuration_ValidShapes(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"1h", time.Hour},
		{"30m", 30 * time.Minute},
		{"2h15m", 2*time.Hour + 15*time.Minute},
		{"45s", 45 * time.Second},
		{"1d", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
	}
	for _, c := range cases {
		got, err := parseFutureDuration(c.in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseFutureDuration_RejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"empty":             "",
		"leading dash":      "-1h",
		"zero":              "0s",
		"negative day":      "-1d",
		"zero day":          "0d",
		"non-numeric day":   "abcd",
		"garbage":           "notaduration",
		"alpha after digit": "1x",
	}
	for name, in := range cases {
		if _, err := parseFutureDuration(in); err == nil {
			t.Errorf("%s (%q): expected error", name, in)
		}
	}
}

// TestReminderSetCmd_RejectsBothAtAndIn — XOR validation kicks in
// before any daemon call, so no fake RPC needed.
func TestReminderSetCmd_RejectsBothAtAndIn(t *testing.T) {
	cmd := reminderSetCmd()
	cmd.SetArgs([]string{"--at", "2099-01-01T00:00:00Z", "--in", "1h", "--body", "x"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when both --at and --in supplied")
	}
	if !strings.Contains(err.Error(), "exactly one of") {
		t.Errorf("error should mention 'exactly one of'; got %v", err)
	}
}

func TestReminderSetCmd_RejectsNeitherAtNorIn(t *testing.T) {
	cmd := reminderSetCmd()
	cmd.SetArgs([]string{"--body", "x"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when neither --at nor --in supplied")
	}
}

func TestReminderSetCmd_RejectsPastAt(t *testing.T) {
	cmd := reminderSetCmd()
	cmd.SetArgs([]string{"--at", "2020-01-01T00:00:00Z", "--body", "x"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected past-time error")
	}
	if !strings.Contains(err.Error(), "past") {
		t.Errorf("error should mention 'past'; got %v", err)
	}
}

func TestReminderSetCmd_RejectsMalformedAt(t *testing.T) {
	cmd := reminderSetCmd()
	cmd.SetArgs([]string{"--at", "not-a-time", "--body", "x"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected RFC3339 parse error")
	}
	if !strings.Contains(err.Error(), "RFC3339") {
		t.Errorf("error should mention RFC3339; got %v", err)
	}
}

func TestReminderSetCmd_RejectsBadIn(t *testing.T) {
	cmd := reminderSetCmd()
	cmd.SetArgs([]string{"--in", "-1h", "--body", "x"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected negative-duration error")
	}
	if !strings.Contains(err.Error(), "--in invalid") {
		t.Errorf("error should mention '--in invalid'; got %v", err)
	}
}

// MarkFlagRequired enforcement: --body absence is caught by cobra.
func TestReminderSetCmd_RequiresBody(t *testing.T) {
	cmd := reminderSetCmd()
	cmd.SetArgs([]string{"--in", "1h"})
	// cobra prints to stderr by default; silence it for the test.
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected required-flag error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "body") {
		t.Errorf("error should mention body; got %v", err)
	}
}
