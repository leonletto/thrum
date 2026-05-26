package monitor

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed 5-field standard cron expression
// (minute hour day-of-month month day-of-week).
//
// Field syntax supported per element:
//   - "*"               every value
//   - "N"               single value
//   - "N,M,P"           comma-separated list of values
//   - "N-M"             inclusive range
//   - "*/K" or "N-M/K"  range with step K (K>=1)
//
// Day-of-week accepts 0 or 7 for Sunday (matching standard cron).
// Names (jan, mon, etc.) are NOT supported in this in-tree implementation.
// If a project later needs names or 6-field seconds-resolution cron, swap in
// github.com/robfig/cron/v3.
type Schedule struct {
	expr    string
	minute  []bool // 0..59
	hour    []bool // 0..23
	dom     []bool // 1..31
	month   []bool // 1..12
	dow     []bool // 0..6 (Sunday == 0)
	domStar bool   // true when day-of-month was "*"
	dowStar bool   // true when day-of-week was "*"
}

// String returns the original expression as supplied to ParseSchedule.
func (s *Schedule) String() string {
	if s == nil {
		return ""
	}
	return s.expr
}

// ParseSchedule parses a 5-field cron expression. Returns a non-nil
// *Schedule on success.
func ParseSchedule(expr string) (*Schedule, error) {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return nil, fmt.Errorf("empty schedule expression")
	}
	fields := strings.Fields(trimmed)
	if len(fields) != 5 {
		return nil, fmt.Errorf("schedule must have 5 fields (minute hour dom month dow), got %d", len(fields))
	}

	minute, _, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	hour, _, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	dom, domStar, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("day-of-month: %w", err)
	}
	month, _, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	dow, dowStar, err := parseDOW(fields[4])
	if err != nil {
		return nil, fmt.Errorf("day-of-week: %w", err)
	}

	return &Schedule{
		expr:    trimmed,
		minute:  minute,
		hour:    hour,
		dom:     dom,
		month:   month,
		dow:     dow,
		domStar: domStar,
		dowStar: dowStar,
	}, nil
}

// Next returns the next instant strictly after `from` that matches the
// schedule. The returned time is truncated to whole minutes.
//
// Day-of-month and day-of-week semantics follow POSIX cron: when BOTH fields
// are restricted (neither is "*"), a tick fires if EITHER matches (OR). When
// only one is restricted, that one alone gates the day. When both are "*"
// every day is allowed.
func (s *Schedule) Next(from time.Time) time.Time {
	// Start from the next minute boundary.
	t := from.Add(time.Minute).Truncate(time.Minute)

	// Bounded search. A 5-field cron can have a worst-case period of ~4 years
	// (Feb 29 + specific dow), so cap at 5 years to be safe and fail-soft.
	deadline := from.Add(5 * 366 * 24 * time.Hour)
	for t.Before(deadline) {
		if !s.month[t.Month()-1] {
			// Jump to the first day of the next allowed month.
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.dayMatches(t) {
			// Advance to the next day at 00:00.
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.hour[t.Hour()] {
			// Advance to the next hour.
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if !s.minute[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	// Should not happen with sane expressions; return zero time so callers
	// can detect and surface the anomaly.
	return time.Time{}
}

func (s *Schedule) dayMatches(t time.Time) bool {
	domOK := s.dom[t.Day()-1]
	// time.Weekday(): Sunday == 0, Saturday == 6.
	dowOK := s.dow[int(t.Weekday())]
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar && !s.dowStar:
		return dowOK
	case !s.domStar && s.dowStar:
		return domOK
	default:
		// Both restricted → OR (POSIX cron semantics).
		return domOK || dowOK
	}
}

// parseField returns a bitset (one bool per value in [min,max]) and a flag
// indicating whether the field was a bare "*".
func parseField(expr string, minV, maxV int) ([]bool, bool, error) {
	size := maxV - minV + 1
	set := make([]bool, size)
	if expr == "*" {
		for i := range set {
			set[i] = true
		}
		return set, true, nil
	}
	for _, term := range strings.Split(expr, ",") {
		term = strings.TrimSpace(term)
		if term == "" {
			return nil, false, fmt.Errorf("empty term in %q", expr)
		}
		if err := applyTerm(set, term, minV, maxV); err != nil {
			return nil, false, err
		}
	}
	return set, false, nil
}

func applyTerm(set []bool, term string, minV, maxV int) error {
	// Optional /step suffix.
	step := 1
	if i := strings.Index(term, "/"); i >= 0 {
		stepStr := term[i+1:]
		n, err := strconv.Atoi(stepStr)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid step %q", stepStr)
		}
		step = n
		term = term[:i]
		// Step only meaningful with "*" or "N-M". Bare "N/K" rejected.
		if term != "*" && !strings.Contains(term, "-") {
			return fmt.Errorf("step %q requires range or '*'", term)
		}
	}

	var lo, hi int
	switch {
	case term == "*":
		lo, hi = minV, maxV
	case strings.Contains(term, "-"):
		parts := strings.SplitN(term, "-", 2)
		var err error
		lo, err = strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("invalid range lower bound %q", parts[0])
		}
		hi, err = strconv.Atoi(parts[1])
		if err != nil {
			return fmt.Errorf("invalid range upper bound %q", parts[1])
		}
		if lo > hi {
			return fmt.Errorf("range %s-%s: lower > upper", parts[0], parts[1])
		}
	default:
		v, err := strconv.Atoi(term)
		if err != nil {
			return fmt.Errorf("invalid value %q", term)
		}
		lo, hi = v, v
	}

	if lo < minV || hi > maxV {
		return fmt.Errorf("value out of range [%d,%d]: %d-%d", minV, maxV, lo, hi)
	}
	for v := lo; v <= hi; v += step {
		set[v-minV] = true
	}
	return nil
}

// parseDOW parses the day-of-week field. The internal representation is
// 0..6 (Sunday == 0, Saturday == 6), but expressions may use 7 to mean
// Sunday (matching standard cron). 0 and 7 are equivalent.
func parseDOW(expr string) ([]bool, bool, error) {
	// Normalize: replace any standalone "7" with "0" so the standard
	// 0..6 parser accepts the input. We only handle simple cases here;
	// expressions like "1-7" are normalized to "1-6,0" implicitly via
	// expansion: lo=1 hi=7 then we set 0 too.
	if expr == "*" {
		set := make([]bool, 7)
		for i := range set {
			set[i] = true
		}
		return set, true, nil
	}

	set := make([]bool, 7)
	for _, term := range strings.Split(expr, ",") {
		term = strings.TrimSpace(term)
		if term == "" {
			return nil, false, fmt.Errorf("empty term in %q", expr)
		}
		if err := applyDOWTerm(set, term); err != nil {
			return nil, false, err
		}
	}
	return set, false, nil
}

func applyDOWTerm(set []bool, term string) error {
	step := 1
	if i := strings.Index(term, "/"); i >= 0 {
		stepStr := term[i+1:]
		n, err := strconv.Atoi(stepStr)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid step %q", stepStr)
		}
		step = n
		term = term[:i]
		if term != "*" && !strings.Contains(term, "-") {
			return fmt.Errorf("step %q requires range or '*'", term)
		}
	}

	var lo, hi int
	switch {
	case term == "*":
		lo, hi = 0, 6
	case strings.Contains(term, "-"):
		parts := strings.SplitN(term, "-", 2)
		var err error
		lo, err = strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("invalid range lower bound %q", parts[0])
		}
		hi, err = strconv.Atoi(parts[1])
		if err != nil {
			return fmt.Errorf("invalid range upper bound %q", parts[1])
		}
		if lo > hi {
			return fmt.Errorf("range %s-%s: lower > upper", parts[0], parts[1])
		}
		if lo < 0 || hi > 7 {
			return fmt.Errorf("dow range out of [0,7]: %d-%d", lo, hi)
		}
	default:
		v, err := strconv.Atoi(term)
		if err != nil {
			return fmt.Errorf("invalid value %q", term)
		}
		if v < 0 || v > 7 {
			return fmt.Errorf("dow value out of [0,7]: %d", v)
		}
		lo, hi = v, v
	}

	for v := lo; v <= hi; v += step {
		// Normalize 7 → 0 (both are Sunday).
		idx := v
		if idx == 7 {
			idx = 0
		}
		set[idx] = true
	}
	return nil
}
