package main

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

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

// --- lookup view tests ---

func TestFormatReminderLookup_AgentTime(t *testing.T) {
	fire := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	raised := time.Date(2026, 5, 20, 8, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 20, 8, 30, 0, 0, time.UTC)
	r := cli.ReminderRow{
		ID:             "reminder-docs_bot-100-0001",
		Source:         "agent",
		SourceAgent:    "docs_bot",
		TriggerKind:    "time",
		TargetAgent:    "docs_bot",
		Body:           "finish release notes",
		RaisedAt:       raised,
		NextReminderAt: &fire,
		State:          "open",
	}
	got := formatReminderLookup(r, now)
	for _, want := range []string{
		"reminder-docs_bot-100-0001",
		"source:       agent",
		"trigger_kind: time",
		"state:        open",
		"fires at:     2026-05-20T09:00:00Z",
		"target:       @docs_bot",
		"raised:       2026-05-20T08:00:00Z",
		"finish release notes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// time-triggered rows should NOT carry the activity banner.
	if strings.Contains(got, "active for") {
		t.Errorf("time-triggered row should not have activity-since-raised banner:\n%s", got)
	}
}

func TestFormatReminderLookup_ConditionWithActivityBanner(t *testing.T) {
	raised := time.Date(2026, 5, 20, 6, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 20, 8, 0, 0, 0, time.UTC) // 2h later
	next := time.Date(2026, 5, 20, 8, 15, 0, 0, time.UTC)
	r := cli.ReminderRow{
		ID:             "reminder-docs_bot-200-1111",
		Source:         "daemon",
		TriggerKind:    "condition_pane_quiet",
		TargetAgent:    "docs_bot",
		TargetChain:    []string{"@coordinator_main", "leon@example.com"},
		PaneSnapshot:   "line1\nline2\nline3\nline4\nline5",
		RaisedAt:       raised,
		NextReminderAt: &next,
		State:          "open",
	}
	got := formatReminderLookup(r, now)

	for _, want := range []string{
		"condition:    pane_quiet",
		"chain:        @coordinator_main, leon@example.com",
		"agent has been active for 2 hours since this alert was raised",
		"pane snapshot (",
		"line1",
		"line5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// TestFormatReminderLookup_RendersDeferHistory verifies plan §Task 14
// AC ("defer_history (each (deferred_by, defer_to, when) triple)") —
// brainstormer review found this dropped between daemon and CLI.
// Now wired end-to-end: ReminderRow.DeferHistory populated from
// daemon's JSON response via wireToRow + this render block.
func TestFormatReminderLookup_RendersDeferHistory(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	r := cli.ReminderRow{
		ID:    "reminder-docs_bot-100-0001",
		State: "open",
		DeferHistory: []cli.DeferEntry{
			{
				DeferredBy: "leon",
				DeferTo:    time.Date(2026, 5, 20, 14, 0, 0, 0, time.UTC),
				When:       time.Date(2026, 5, 20, 11, 0, 0, 0, time.UTC),
			},
			{
				DeferredBy: "coordinator_main",
				DeferTo:    time.Date(2026, 5, 20, 16, 0, 0, 0, time.UTC),
				When:       time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC),
			},
		},
	}
	got := formatReminderLookup(r, now)
	for _, want := range []string{
		"defer history (2 entries)",
		"by:        leon",
		"by:        coordinator_main",
		"until:     2026-05-20T14:00:00Z",
		"until:     2026-05-20T16:00:00Z",
		"deferred:  2026-05-20T11:00:00Z",
		"deferred:  2026-05-20T13:00:00Z",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("defer-history render missing %q:\n%s", want, got)
		}
	}
}

func TestFormatReminderLookup_HidesDeferHistoryWhenEmpty(t *testing.T) {
	r := cli.ReminderRow{
		ID:           "reminder-x-1-1",
		State:        "open",
		DeferHistory: nil,
	}
	got := formatReminderLookup(r, time.Now().UTC())
	if strings.Contains(got, "defer history") {
		t.Errorf("empty defer_history should hide the block; got:\n%s", got)
	}
}

func TestFormatReminderLookup_TerminalStatesIncludeTimestamps(t *testing.T) {
	clearedAt := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	r := cli.ReminderRow{
		ID:        "reminder-x-1-2",
		State:     "cleared",
		ClearedAt: &clearedAt,
	}
	got := formatReminderLookup(r, time.Now().UTC())
	if !strings.Contains(got, "cleared at:   2026-05-20T10:00:00Z") {
		t.Errorf("missing cleared_at: %s", got)
	}
}

func TestFormatLookupElapsed_Boundaries(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "less than a minute"},
		{time.Minute, "1 minute"},
		{2 * time.Minute, "2 minutes"},
		{time.Hour, "1 hour"},
		{2 * time.Hour, "2 hours"},
		{24 * time.Hour, "1 day"},
		{72 * time.Hour, "3 days"},
	}
	for _, c := range cases {
		got := formatLookupElapsed(c.d)
		if got != c.want {
			t.Errorf("formatLookupElapsed(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestLastNLines_FewerThanN(t *testing.T) {
	got := lastNLines("a\nb\nc", 10)
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("got %q, want [a b c]", got)
	}
}

func TestLastNLines_TruncatesToLastN(t *testing.T) {
	in := "a\nb\nc\nd\ne"
	got := lastNLines(in, 3)
	if len(got) != 3 || got[0] != "c" || got[2] != "e" {
		t.Errorf("got %q, want [c d e]", got)
	}
}

func TestLastNLines_TrimsTrailingNewline(t *testing.T) {
	got := lastNLines("a\nb\n", 10)
	if len(got) != 2 || got[1] != "b" {
		t.Errorf("got %q, want [a b]", got)
	}
}

func TestReminderCmd_NoArgsPrintsHelp(t *testing.T) {
	cmd := reminderCmd()
	cmd.SetArgs([]string{})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(new(strings.Builder)) // suppress help-text from test log
	// Help output goes to stdout by default. The test only verifies
	// that no error is returned and the help path was taken — actual
	// help-text golden tests live in thrum-6qmf.3.22.
	if err := cmd.Execute(); err != nil {
		t.Errorf("no-args should print help and return nil; got %v", err)
	}
}

// --- mutual exclusion of --defer / --clear / --cancel ---

func TestReminderCmd_MutuallyExclusiveActionFlags(t *testing.T) {
	combos := [][]string{
		{"reminder-x-1-2", "--defer", "1h", "--clear"},
		{"reminder-x-1-2", "--defer", "1h", "--cancel"},
		{"reminder-x-1-2", "--clear", "--cancel"},
		{"reminder-x-1-2", "--defer", "1h", "--clear", "--cancel"},
	}
	for _, combo := range combos {
		t.Run(strings.Join(combo[1:], "_"), func(t *testing.T) {
			cmd := reminderCmd()
			cmd.SetArgs(combo)
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetOut(new(strings.Builder))
			cmd.SetErr(new(strings.Builder))
			err := cmd.Execute()
			if err == nil {
				t.Errorf("expected mutual-exclusivity error for %v", combo)
			}
		})
	}
}

// TestReminderHelp_Distinguishes_ClearVsCancel asserts that both the
// reminder parent help and `reminder set --help` carry the
// --clear/--cancel distinction per brainstorm cycle-2 finding #13.
// Golden assertion: presence-of-snippet rather than byte-equality so
// minor wording tweaks don't break the test.
func TestReminderHelp_Distinguishes_ClearVsCancel(t *testing.T) {
	cases := map[string]*cobra.Command{
		"reminder parent": reminderCmd(),
		"reminder set":    reminderSetCmd(),
	}
	for name, cmd := range cases {
		t.Run(name, func(t *testing.T) {
			help := cmd.Long
			for _, want := range []string{"--clear", "--cancel"} {
				if !strings.Contains(help, want) {
					t.Errorf("%s help missing %q:\n%s", name, want, help)
				}
			}
			// Both Long strings must mention the storage-level
			// distinction so users learn that --list --state filters
			// will distinguish them later.
			if !strings.Contains(help, "cleared_at") || !strings.Contains(help, "cancelled_at") {
				t.Errorf("%s help should mention cleared_at + cancelled_at distinction:\n%s", name, help)
			}
		})
	}
}

func TestReminderCmd_DeferInvalidDuration(t *testing.T) {
	cmd := reminderCmd()
	cmd.SetArgs([]string{"reminder-x-1-2", "--defer", "-1h"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(new(strings.Builder))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for negative defer duration")
	}
	if !strings.Contains(err.Error(), "--defer invalid") {
		t.Errorf("error should mention '--defer invalid'; got %v", err)
	}
}
