package reminders

import (
	"strings"
	"testing"
	"time"
)

func TestFire_AgentBody_ConditionTriggered(t *testing.T) {
	r := &Reminder{
		ID:          "reminder-docs_bot-100-0001",
		TriggerKind: TriggerConditionPaneQuiet,
		TargetAgent: "docs_bot",
	}
	got := FormatAgentBody(r)
	want := "Idle Agent Detected with idle-id: reminder-docs_bot-100-0001 — run `thrum agent reminder reminder-docs_bot-100-0001`"
	if got != want {
		t.Errorf("FormatAgentBody:\n got: %q\n want: %q", got, want)
	}
}

func TestFire_AgentBody_TimeTriggered(t *testing.T) {
	r := &Reminder{
		ID:          "reminder-docs_bot-100-0001",
		TriggerKind: TriggerTime,
	}
	got := FormatAgentBody(r)
	if !strings.HasPrefix(got, "Reminder fired") {
		t.Errorf("time-triggered body should start with 'Reminder fired'; got %q", got)
	}
	if !strings.Contains(got, "reminder-docs_bot-100-0001") {
		t.Errorf("body should embed id; got %q", got)
	}
	if !strings.Contains(got, "`thrum agent reminder reminder-docs_bot-100-0001`") {
		t.Errorf("body should embed lookup command; got %q", got)
	}
}

func TestFire_EmailBody_IncludesActivitySinceRaised(t *testing.T) {
	now := time.Now().UTC()
	r := &Reminder{
		ID:          "reminder-docs_bot-100-0001",
		TriggerKind: TriggerConditionPaneQuiet,
		TargetAgent: "docs_bot",
		RaisedAt:    now.Add(-2 * time.Hour),
	}
	subject, body := FormatEmail(r, now)
	if !strings.Contains(subject, "agent docs_bot idle") {
		t.Errorf("subject = %q", subject)
	}
	if !strings.Contains(body, "2h") && !strings.Contains(body, "120m") {
		t.Errorf("duration missing from body: %q", body)
	}
	if !strings.Contains(body, "thrum agent reminder reminder-docs_bot-100-0001") {
		t.Error("lookup command missing from body")
	}
}

// Extra coverage beyond the three golden tests — the duration formatter
// is the most error-prone piece (boundary at minute/hour/day) and the
// FormatEmail body advertises the defer/clear/cancel flags which CLI
// consumers test against.

func TestFire_EmailBody_AdvertisesDeferClearCancel(t *testing.T) {
	now := time.Now().UTC()
	r := &Reminder{
		ID:          "reminder-x-100-0001",
		TriggerKind: TriggerConditionPaneQuiet,
		TargetAgent: "x",
		RaisedAt:    now.Add(-time.Hour),
	}
	_, body := FormatEmail(r, now)
	for _, want := range []string{"--defer", "--clear", "--cancel"} {
		if !strings.Contains(body, want) {
			t.Errorf("email body missing flag %s; body=%q", want, body)
		}
	}
}

func TestFormatElapsed_Boundaries(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "less than a minute"},
		{time.Minute, "1m"},
		{2 * time.Minute, "2m"},
		{59 * time.Minute, "59m"},
		{time.Hour, "1h"},
		{2 * time.Hour, "2h"},
		{23 * time.Hour, "23h"},
		{24 * time.Hour, "1d"},
		{48 * time.Hour, "2d"},
		// negative durations format as their absolute value (clock skew /
		// test fixtures with raised_at in the future).
		{-2 * time.Hour, "2h"},
	}
	for _, c := range cases {
		got := formatElapsed(c.d)
		if got != c.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestFire_EmailBody_IncludesRFC3339RaisedAt(t *testing.T) {
	now := time.Now().UTC()
	raised := now.Add(-3 * time.Hour)
	r := &Reminder{
		ID:          "reminder-x-1-1",
		TriggerKind: TriggerConditionPaneQuiet,
		TargetAgent: "x",
		RaisedAt:    raised,
	}
	_, body := FormatEmail(r, now)
	if !strings.Contains(body, raised.Format(time.RFC3339)) {
		t.Errorf("body should embed RFC3339 raised_at %q; body=%q", raised.Format(time.RFC3339), body)
	}
}
