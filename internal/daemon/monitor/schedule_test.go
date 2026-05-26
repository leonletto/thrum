package monitor

import (
	"testing"
	"time"
)

func TestParseSchedule_Valid(t *testing.T) {
	cases := []string{
		"* * * * *",
		"0 * * * *",
		"7,27,47 * * * *",
		"*/5 * * * *",
		"0 9-17 * * *",
		"0 0 1 * *",
		"0 0 * * 0",
		"15,45 8-18/2 * * 1-5",
		"30 2 1,15 * *",
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			s, err := ParseSchedule(expr)
			if err != nil {
				t.Fatalf("ParseSchedule(%q) returned error: %v", expr, err)
			}
			if s == nil {
				t.Fatalf("ParseSchedule(%q) returned nil schedule", expr)
			}
		})
	}
}

func TestParseSchedule_Invalid(t *testing.T) {
	cases := []string{
		"",
		"* * * *",                 // 4 fields
		"* * * * * *",             // 6 fields
		"60 * * * *",              // minute > 59
		"* 24 * * *",              // hour > 23
		"* * 0 * *",               // day < 1
		"* * 32 * *",              // day > 31
		"* * * 0 *",               // month < 1
		"* * * 13 *",              // month > 12
		"* * * * 8",               // dow > 7
		"abc * * * *",             // non-numeric
		"5-2 * * * *",             // inverted range
		"*/0 * * * *",             // zero step
		"5/3 * * * *",             // step without lhs (we require range or *)
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			if _, err := ParseSchedule(expr); err == nil {
				t.Fatalf("ParseSchedule(%q) unexpectedly succeeded", expr)
			}
		})
	}
}

func TestSchedule_Next_EveryMinute(t *testing.T) {
	s, err := ParseSchedule("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 1, 12, 30, 15, 0, time.UTC)
	next := s.Next(from)
	want := time.Date(2026, 1, 1, 12, 31, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v", from, next, want)
	}
}

func TestSchedule_Next_MinuteList(t *testing.T) {
	s, err := ParseSchedule("7,27,47 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 1, 12, 30, 15, 0, time.UTC)
	next := s.Next(from)
	want := time.Date(2026, 1, 1, 12, 47, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Next at 12:30 = %v, want %v", next, want)
	}

	from = time.Date(2026, 1, 1, 12, 47, 0, 0, time.UTC)
	next = s.Next(from)
	want = time.Date(2026, 1, 1, 13, 7, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Next at 12:47 = %v, want %v", next, want)
	}
}

func TestSchedule_Next_HourRange(t *testing.T) {
	s, err := ParseSchedule("0 9-17 * * *")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 1, 8, 30, 0, 0, time.UTC)
	next := s.Next(from)
	want := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Next 8:30 = %v, want %v", next, want)
	}

	from = time.Date(2026, 1, 1, 17, 30, 0, 0, time.UTC)
	next = s.Next(from)
	want = time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Next 17:30 = %v, want %v", next, want)
	}
}

func TestSchedule_Next_Step(t *testing.T) {
	s, err := ParseSchedule("*/5 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 1, 12, 7, 0, 0, time.UTC)
	next := s.Next(from)
	want := time.Date(2026, 1, 1, 12, 10, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Next at 12:07 = %v, want %v", next, want)
	}
}

func TestSchedule_Next_DayOfWeek(t *testing.T) {
	// Mondays at 09:00. 2026-01-05 is a Monday.
	s, err := ParseSchedule("0 9 * * 1")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC) // Saturday
	next := s.Next(from)
	want := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Next Sat→Mon 09:00 = %v, want %v", next, want)
	}
}

func TestSchedule_Next_SundayBoth0And7(t *testing.T) {
	// 0 and 7 both mean Sunday.
	s0, err := ParseSchedule("0 9 * * 0")
	if err != nil {
		t.Fatal(err)
	}
	s7, err := ParseSchedule("0 9 * * 7")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC) // Saturday
	if !s0.Next(from).Equal(s7.Next(from)) {
		t.Fatalf("dow=0 and dow=7 should be equivalent Sundays")
	}
	// 2026-01-04 is Sunday.
	want := time.Date(2026, 1, 4, 9, 0, 0, 0, time.UTC)
	if got := s0.Next(from); !got.Equal(want) {
		t.Fatalf("dow=0 next = %v, want %v", got, want)
	}
}

func TestSchedule_Next_RangeWithStep(t *testing.T) {
	s, err := ParseSchedule("0 8-18/2 * * *")
	if err != nil {
		t.Fatal(err)
	}
	// 8,10,12,14,16,18
	from := time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC)
	next := s.Next(from)
	want := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Next 9:30 in 8-18/2 = %v, want %v", next, want)
	}
}
