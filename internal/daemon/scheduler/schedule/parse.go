// Package schedule parses scheduler `schedule:` fields into a Schedule
// interface that the reactor consults for next-fire times.
//
// Format authority: dev-docs/thrum-agents/substrate-canonical-reference.md §4.1.1.
// The six canonical formats land across two tasks in E1.6:
//   - 5-field cron      (Task 6, this file)
//   - 6-field cron      (Task 6, this file)
//   - @every <duration> (Task 6, this file)
//   - robfig macros     (Task 6, this file)
//   - @at <iso8601>     (Task 7, parseAt below)
//   - @once             (Task 7, oneShotOnce below)
package schedule

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Schedule produces the next fire time given the current time. A return
// value of time.Time{} (zero value) means "no further fire scheduled" —
// the reactor treats this as one-shot-done and writes NULL into
// scheduler_job_state.next_scheduled_at.
type Schedule interface {
	Next(after time.Time) time.Time
}

// ParseOpts carries context the parser needs but the schedule string
// cannot express: timezone resolution and the deterministic-jitter seed.
type ParseOpts struct {
	// Location resolves cron expressions (operator-local, UTC fallback, or
	// a per-job schedule_tz override). REQUIRED.
	Location *time.Location

	// JitterSeed is a deterministic hash for jitter computation; opaque to
	// Parse. Reactor passes (job_id + daemon_id) hashed. Empty seed
	// disables jitter (used by one-shot @at / @once per canonical §4.1.1).
	JitterSeed string
}

// Parse interprets `s` per canonical §4.1.1 and returns a Schedule.
func Parse(s string, opts ParseOpts) (Schedule, error) {
	if opts.Location == nil {
		return nil, errors.New("schedule.Parse: opts.Location is required")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("schedule.Parse: empty schedule string")
	}
	// One-shot forms come first; cheap to detect.
	if s == "@once" {
		return &oneShotOnce{}, nil
	}
	if rest, ok := strings.CutPrefix(s, "@at "); ok {
		return parseAt(rest)
	}
	// Cron + @every + robfig macros all route through robfig/cron's parser.
	// 6-field forms need cron.Second; 5-field don't. Detect by token count
	// — robfig macros (@every, @daily, etc.) pass through Descriptor.
	parserOpts := cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor
	if !strings.HasPrefix(s, "@") && len(strings.Fields(s)) == 6 {
		parserOpts |= cron.Second
	}
	p := cron.NewParser(parserOpts)
	sched, err := p.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("schedule.Parse(%q): %w", s, err)
	}
	return &cronSchedule{sched: sched, loc: opts.Location}, nil
}

// cronSchedule wraps robfig/cron's Schedule and pins next-fire computation
// to the per-job timezone.
type cronSchedule struct {
	sched cron.Schedule
	loc   *time.Location
}

func (c *cronSchedule) Next(after time.Time) time.Time {
	// robfig's Next() is timezone-aware via the parsed expression's interpretation
	// of field values; cast `after` into our location so DST-sensitive expressions
	// (e.g., "0 2 * * *") resolve relative to the operator's clock.
	return c.sched.Next(after.In(c.loc))
}

// parseAt is the @at <iso8601> handler. Body lands in Task 7 (thrum-6qmf.6.6);
// stub returns an error so Task 6 ships routing without claiming functionality
// it does not have.
func parseAt(_ string) (Schedule, error) {
	return nil, errors.New("schedule.Parse: @at <iso8601> not yet implemented (Task 7)")
}

// oneShotOnce is the @once handler. Body lands in Task 7; stub's Next()
// returns the zero time so callers see "no further fire" — safe sentinel
// while the real impl is pending.
type oneShotOnce struct{}

func (*oneShotOnce) Next(_ time.Time) time.Time { return time.Time{} }
