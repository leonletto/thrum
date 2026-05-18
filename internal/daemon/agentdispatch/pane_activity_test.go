package agentdispatch

import (
	"strconv"
	"testing"
	"time"
)

// TestParsePaneActivity_ValidUnixTimestamp pins the canonical
// happy-path conversion: a Unix-epoch integer with a trailing
// newline (tmux's display-message format) parses to the matching
// time.Time. Drift here would silently break the idle-nudge
// loop's silence calculation.
func TestParsePaneActivity_ValidUnixTimestamp(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{"plain integer", "1747353600", 1747353600},
		{"trailing newline", "1747353600\n", 1747353600},
		{"trailing crlf", "1747353600\r\n", 1747353600},
		{"surrounding whitespace", "  1747353600  ", 1747353600},
		{"zero (epoch)", "0", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parsePaneActivity([]byte(c.raw))
			if err != nil {
				t.Fatalf("err = %v; want nil for %q", err, c.raw)
			}
			if got.Unix() != c.want {
				t.Errorf("Unix() = %d; want %d", got.Unix(), c.want)
			}
		})
	}
}

// TestParsePaneActivity_EmptyOutput pins the distinct empty-output
// failure class: tmux returning nothing (session torn down
// mid-poll) gets its own error so callers can distinguish
// "session missing" (benign teardown noise) from "session present
// but produced garbage" (upstream tmux change worth investigating).
func TestParsePaneActivity_EmptyOutput(t *testing.T) {
	cases := []string{"", "\n", "   \t\n  "}
	for _, raw := range cases {
		_, err := parsePaneActivity([]byte(raw))
		if err == nil {
			t.Errorf("err = nil; want non-nil for empty input %q", raw)
		}
	}
}

// TestParsePaneActivity_NonNumeric pins the parse-failure case:
// tmux output that isn't a Unix-epoch integer (upstream tmux
// change, broken format string) surfaces as a wrapped strconv
// error so log lines include the raw output for diagnostics.
func TestParsePaneActivity_NonNumeric(t *testing.T) {
	cases := []string{"not a number", "0x1234", "1747353600.5", "1747353600 stuff"}
	for _, raw := range cases {
		_, err := parsePaneActivity([]byte(raw))
		if err == nil {
			t.Errorf("err = nil; want non-nil for non-numeric input %q", raw)
		}
	}
}

// TestParsePaneActivity_NegativeTimestamp pins the defensive
// negative-timestamp rejection. A negative Unix epoch is
// time.Time before 1970 — almost certainly an upstream bug,
// not a real activity timestamp. Better to fail loud than
// silently produce a time.Time that breaks idle calculations.
func TestParsePaneActivity_NegativeTimestamp(t *testing.T) {
	_, err := parsePaneActivity([]byte("-1\n"))
	if err == nil {
		t.Errorf("err = nil; want non-nil for negative timestamp")
	}
}

// TestParsePaneActivity_RoundTripPreservesUnixSeconds pins the
// round-trip equivalence: parsing the textual form of a known
// Unix epoch produces a time.Time that re-renders to the same
// integer. Catches a future regression that loses seconds
// precision (e.g., switching to time.UnixMilli mid-stack).
func TestParsePaneActivity_RoundTripPreservesUnixSeconds(t *testing.T) {
	now := time.Now().Unix()
	got, err := parsePaneActivity([]byte(strconv.FormatInt(now, 10)))
	if err != nil {
		t.Fatalf("round-trip err = %v", err)
	}
	if got.Unix() != now {
		t.Errorf("round-trip Unix() = %d; want %d", got.Unix(), now)
	}
}
