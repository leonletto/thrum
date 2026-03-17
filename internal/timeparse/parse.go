// Package timeparse provides helpers for parsing user-friendly time
// specifications into absolute UTC timestamps.  It is used by the
// thrum-purge command to interpret the --before flag.
package timeparse

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseBefore converts a time specification into an absolute UTC time.
//
// Supported formats (tried in order):
//  1. Relative days  – "2d", "7d"              → N*24h subtracted from now
//  2. Go duration    – "24h", "2h30m"           → duration subtracted from now
//  3. Date-only      – "2026-03-15"             → midnight UTC on that date
//  4. Full RFC 3339  – "2026-03-15T14:30:00Z"   → exact timestamp
//
// Errors are returned for empty input, unrecognized formats, negative or zero
// day counts, and negative or zero durations.
func ParseBefore(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("time specification must not be empty")
	}

	// 1. Relative days: "<N>d"
	if strings.HasSuffix(s, "d") {
		nStr := s[:len(s)-1]
		n, err := strconv.Atoi(nStr)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid day count in %q: %w", s, err)
		}
		if n <= 0 {
			return time.Time{}, fmt.Errorf("day count must be positive, got %d", n)
		}
		return time.Now().UTC().Add(-time.Duration(n) * 24 * time.Hour), nil
	}

	// 2. Go duration (e.g. "24h", "2h30m", "90m")
	// time.ParseDuration accepts a leading '-', which we want to reject
	// explicitly, so check the sign first.
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("duration must be positive, got %v", d)
		}
		return time.Now().UTC().Add(-d), nil
	}

	// 3. Date-only: "YYYY-MM-DD"
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}

	// 4. Full RFC 3339
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}

	return time.Time{}, fmt.Errorf("unrecognized time specification %q; "+
		"accepted formats: Nd (e.g. 7d), Go duration (e.g. 24h), "+
		"date (2006-01-02), or RFC 3339 (2006-01-02T15:04:05Z)", s)
}
