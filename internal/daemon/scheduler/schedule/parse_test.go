package schedule

import (
	"testing"
	"time"
)

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

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

func TestParse_AtIso8601_AcceptsTZ(t *testing.T) {
	s, err := Parse("@at 2026-05-15T09:00:00Z", ParseOpts{Location: time.UTC})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	now := time.Date(2026, 5, 15, 8, 0, 0, 0, time.UTC)
	next := s.Next(now)
	want := time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("first Next = %v, want %v", next, want)
	}
	// One-shot semantics: second call returns zero time.
	next2 := s.Next(want.Add(time.Second))
	if !next2.IsZero() {
		t.Errorf("second Next should be zero; got %v", next2)
	}
}

// TestParse_AtIso8601_RequiresTZ pins canonical §4.1.1: naive timestamps
// (no offset, no Z) are rejected so the operator can't accidentally
// schedule a fire at the daemon's local clock when they meant UTC.
func TestParse_AtIso8601_RequiresTZ(t *testing.T) {
	bad := []string{
		"@at 2026-05-15T09:00:00", // no TZ
		"@at 2026-05-15 09:00:00", // no TZ + space-separated
		"@at not-a-timestamp",
		"@at ", // empty timestamp
	}
	for _, s := range bad {
		if _, err := Parse(s, ParseOpts{Location: time.UTC}); err == nil {
			t.Errorf("Parse(%q): expected error", s)
		}
	}
}

// TestParse_AtIso8601_AcceptsOffset verifies non-Z TZ offsets (e.g.
// "+02:00") are accepted; the parsed instant is timezone-normalized.
func TestParse_AtIso8601_AcceptsOffset(t *testing.T) {
	s, err := Parse("@at 2026-05-15T11:00:00+02:00", ParseOpts{Location: time.UTC})
	if err != nil {
		t.Fatalf("parse with offset: %v", err)
	}
	now := time.Date(2026, 5, 15, 8, 0, 0, 0, time.UTC)
	next := s.Next(now)
	want := time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("Next = %v, want %v (instant equivalence regardless of offset)", next, want)
	}
}

func TestParse_Once_FiresOnceThenZero(t *testing.T) {
	s, err := Parse("@once", ParseOpts{Location: time.UTC})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	now := time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)
	next := s.Next(now)
	// @once fires at the reactor's "after" time; jitter applied at
	// registration, not in Next().
	if !next.Equal(now) {
		t.Errorf("first Next = %v, want now = %v", next, now)
	}
	next2 := s.Next(now.Add(time.Second))
	if !next2.IsZero() {
		t.Errorf("second Next should be zero; got %v", next2)
	}
}

// TestParse_DST_SpringForward_LA: 2026-03-08 02:00 PST → 03:00 PDT in
// America/Los_Angeles. A `0 9 * * *` schedule must still fire at 09:00
// local on the DST day; robfig handles the wall-clock advancement.
func TestParse_DST_SpringForward_LA(t *testing.T) {
	loc := mustLoadLocation("America/Los_Angeles")
	s, err := Parse("0 9 * * *", ParseOpts{Location: loc})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	now := time.Date(2026, 3, 7, 23, 0, 0, 0, loc)
	next := s.Next(now)
	want := time.Date(2026, 3, 8, 9, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Errorf("DST forward: next = %v, want %v", next, want)
	}
}

// TestParse_DST_FallBack_LA: 2026-11-01 02:00 PDT → 01:00 PST. A
// `0 1 * * *` schedule must still resolve to a single fire on the DST day.
func TestParse_DST_FallBack_LA(t *testing.T) {
	loc := mustLoadLocation("America/Los_Angeles")
	s, err := Parse("0 1 * * *", ParseOpts{Location: loc})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	now := time.Date(2026, 11, 1, 0, 30, 0, 0, loc)
	next := s.Next(now)
	if next.Year() != 2026 || next.Month() != 11 || next.Day() != 1 {
		t.Errorf("DST back: unexpected next %v", next)
	}
}

// TestParse_StartupDiagnosticHelper covers the spec §4.2 requirement that
// the parser expose a Diagnostic with the resolved location name + the next
// 3 fire times.
func TestParse_StartupDiagnosticHelper(t *testing.T) {
	loc := mustLoadLocation("UTC")
	_, info, err := ParseWithDiagnostic("0 9 * * *", ParseOpts{Location: loc})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.LocationName != "UTC" {
		t.Errorf("location = %q, want UTC", info.LocationName)
	}
	if len(info.NextThree) != 3 {
		t.Errorf("expected 3 next-fire times, got %d", len(info.NextThree))
	}
}

// TestParse_DiagnosticPreservesReactorSchedule: one-shot @once is
// state-bearing; if the diagnostic computation consumed the reactor's
// Schedule, the reactor would never see a fire-time. Verify both copies
// remain usable.
func TestParse_DiagnosticPreservesReactorSchedule(t *testing.T) {
	sched, info, err := ParseWithDiagnostic("@once", ParseOpts{Location: time.UTC})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Diagnostic should have one entry (one-shot fires once, then break).
	if len(info.NextThree) != 1 {
		t.Errorf("expected 1 fire time for @once diagnostic, got %d", len(info.NextThree))
	}
	// The returned reactor Schedule should still fire on its first Next() call.
	now := time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)
	if got := sched.Next(now); got.IsZero() {
		t.Error("reactor Schedule already consumed by diagnostic — should still fire once")
	}
}
