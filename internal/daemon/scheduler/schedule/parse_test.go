package schedule

import (
	"testing"
	"time"
)

func TestParse_5FieldCron(t *testing.T) {
	s, err := Parse("0 9 * * *", ParseOpts{Location: time.UTC})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	now := time.Date(2026, 5, 15, 8, 0, 0, 0, time.UTC)
	next := s.Next(now)
	want := time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestParse_6FieldCronWithSeconds(t *testing.T) {
	s, err := Parse("*/15 * * * * *", ParseOpts{Location: time.UTC})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	now := time.Date(2026, 5, 15, 9, 0, 5, 0, time.UTC)
	next := s.Next(now)
	want := time.Date(2026, 5, 15, 9, 0, 15, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestParse_EveryDuration(t *testing.T) {
	s, err := Parse("@every 10m", ParseOpts{Location: time.UTC})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	now := time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)
	next := s.Next(now)
	want := now.Add(10 * time.Minute)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestParse_RobfigMacros(t *testing.T) {
	cases := map[string]struct {
		now, want time.Time
	}{
		"@daily":  {time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC), time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)},
		"@hourly": {time.Date(2026, 5, 15, 9, 30, 0, 0, time.UTC), time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)},
	}
	for spec, tc := range cases {
		s, err := Parse(spec, ParseOpts{Location: time.UTC})
		if err != nil {
			t.Fatalf("parse %q: %v", spec, err)
		}
		next := s.Next(tc.now)
		if !next.Equal(tc.want) {
			t.Errorf("%q.Next(%v) = %v, want %v", spec, tc.now, next, tc.want)
		}
	}
}

func TestParse_Malformed(t *testing.T) {
	bad := []string{
		"",
		"not a cron",
		"@every notaduration",
		"9 25 * * *", // hour out of range
	}
	for _, s := range bad {
		if _, err := Parse(s, ParseOpts{Location: time.UTC}); err == nil {
			t.Errorf("Parse(%q): expected error", s)
		}
	}
}

// TestParse_RequiresLocation pins that opts.Location is required — a parser
// without a TZ cannot disambiguate cron expressions correctly. This is
// canonical §4.1.1 contract.
func TestParse_RequiresLocation(t *testing.T) {
	if _, err := Parse("0 9 * * *", ParseOpts{}); err == nil {
		t.Error("Parse with nil Location: expected error")
	}
}
