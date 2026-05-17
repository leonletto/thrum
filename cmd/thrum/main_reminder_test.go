package main

import (
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/cli"
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

// --- list subcommand tests ---

func TestBuildReminderListOpts_DefaultScopesToSelfOpen(t *testing.T) {
	opts := buildReminderListOpts(reminderListFlags{}, "docs_bot")
	if opts.TargetAgent != "docs_bot" {
		t.Errorf("TargetAgent = %q, want docs_bot", opts.TargetAgent)
	}
	if opts.State != "open" {
		t.Errorf("State = %q, want open", opts.State)
	}
}

func TestBuildReminderListOpts_StateFlagSuppressesDefault(t *testing.T) {
	opts := buildReminderListOpts(reminderListFlags{state: "cleared"}, "docs_bot")
	if opts.State != "cleared" {
		t.Errorf("State = %q, want cleared", opts.State)
	}
	// Self-default should NOT kick in once any filter is set.
	if opts.TargetAgent != "" {
		t.Errorf("TargetAgent = %q, want '' (state filter widens scope)", opts.TargetAgent)
	}
}

func TestBuildReminderListOpts_SourceFlagSuppressesDefault(t *testing.T) {
	opts := buildReminderListOpts(reminderListFlags{source: "daemon"}, "docs_bot")
	if opts.Source != "daemon" {
		t.Errorf("Source = %q", opts.Source)
	}
	if opts.TargetAgent != "" {
		t.Errorf("TargetAgent = %q, want '' under source filter", opts.TargetAgent)
	}
}

func TestBuildReminderListOpts_TargetFlagStripsAtPrefix(t *testing.T) {
	opts := buildReminderListOpts(reminderListFlags{target: "@other_agent"}, "docs_bot")
	if opts.TargetAgent != "other_agent" {
		t.Errorf("TargetAgent = %q, want other_agent (@ stripped)", opts.TargetAgent)
	}
}

func TestBuildReminderListOpts_LimitForwarded(t *testing.T) {
	opts := buildReminderListOpts(reminderListFlags{state: "open", limit: 10}, "x")
	if opts.Limit != 10 {
		t.Errorf("Limit = %d, want 10", opts.Limit)
	}
}

func TestReminderListCmd_RejectsNegativeLimit(t *testing.T) {
	cmd := reminderListCmd()
	cmd.SetArgs([]string{"--limit", "-5"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for negative limit")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error should mention limit; got %v", err)
	}
}

func TestTruncateBody_UnderMax_Unchanged(t *testing.T) {
	got := truncateBody("short", 60)
	if got != "short" {
		t.Errorf("got %q, want unchanged", got)
	}
}

func TestTruncateBody_OverMax_TruncatesWithEllipsis(t *testing.T) {
	body := strings.Repeat("x", 100)
	got := truncateBody(body, 60)
	if len(got) != 60 {
		t.Errorf("len = %d, want 60", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("missing ellipsis: %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("x", 57)) {
		t.Errorf("body prefix = %q", got)
	}
}

func TestTruncateBody_TinyMax_NoEllipsis(t *testing.T) {
	got := truncateBody("longer", 2)
	if got != "lo" {
		t.Errorf("got %q, want 'lo'", got)
	}
}

func TestFormatReminderListRow_OpenWithFireTime(t *testing.T) {
	fire := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	r := cli.ReminderRow{
		ID:             "reminder-docs_bot-123-4567",
		Source:         "agent",
		TargetAgent:    "docs_bot",
		Body:           "finish release notes",
		State:          "open",
		NextReminderAt: &fire,
	}
	got := formatReminderListRow(r)
	if !strings.Contains(got, "reminder-docs_bot-123-4567") {
		t.Errorf("missing id: %s", got)
	}
	if !strings.Contains(got, "fires 2026-05-20T09:00:00Z") {
		t.Errorf("missing 'fires <time>': %s", got)
	}
	if !strings.Contains(got, "target=docs_bot") {
		t.Errorf("missing target: %s", got)
	}
	if !strings.Contains(got, `"finish release notes"`) {
		t.Errorf("missing body: %s", got)
	}
}

func TestFormatReminderListRow_TerminalStates(t *testing.T) {
	for _, st := range []string{"fired", "cleared", "cancelled"} {
		r := cli.ReminderRow{
			ID:    "reminder-x-1-2",
			State: st,
		}
		got := formatReminderListRow(r)
		if !strings.Contains(got, st) {
			t.Errorf("state %q label missing: %s", st, got)
		}
		if !strings.Contains(got, "unscheduled") {
			t.Errorf("terminal row without next_reminder_at should render 'unscheduled': %s", got)
		}
	}
}

func TestFormatReminderListRow_LongBodyTruncated(t *testing.T) {
	r := cli.ReminderRow{
		ID:    "reminder-x-1-2",
		Body:  strings.Repeat("a", 200),
		State: "open",
	}
	got := formatReminderListRow(r)
	if !strings.Contains(got, "...") {
		t.Errorf("body > 60 chars should be truncated with ...; got %s", got)
	}
}
