package sessionarchive_test

import (
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/sessionarchive"
)

func TestFormatTimestamp_ColonsAndDotsStripped(t *testing.T) {
	in := time.Date(2026, 5, 17, 15, 32, 18, 421_000_000, time.UTC)
	got := sessionarchive.FormatTimestamp(in)
	want := "20260517T153218421Z"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatTimestamp_NormalizesToUTC(t *testing.T) {
	// 15:32:18.421 in a +05:00 zone is 10:32:18.421 in UTC.
	tz := time.FixedZone("test+05", 5*60*60)
	in := time.Date(2026, 5, 17, 15, 32, 18, 421_000_000, tz)
	got := sessionarchive.FormatTimestamp(in)
	want := "20260517T103218421Z"
	if got != want {
		t.Errorf("got %q, want %q (UTC normalization)", got, want)
	}
}

func TestFormatTimestamp_NoFractionalSecond_StillMillisField(t *testing.T) {
	// time.Date with 0 nanos still must produce a 3-digit ms section
	// stripped to 000 — filename grammar requires the ms field to
	// always be present so the lexical sort over directory entries
	// equals the chronological sort.
	in := time.Date(2026, 5, 17, 15, 32, 18, 0, time.UTC)
	got := sessionarchive.FormatTimestamp(in)
	want := "20260517T153218000Z"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Defensive: shape must be exactly YYYYMMDDTHHMMSSmmmZ.
	// Length breakdown: 8 (date) + 1 (T) + 6 (HHMMSS) + 3 (mmm) + 1 (Z) = 19.
	if len(got) != 19 || !strings.HasSuffix(got, "Z") {
		t.Errorf("shape mismatch: len=%d suffix=Z? want 19-char ...mmmZ", len(got))
	}
}
