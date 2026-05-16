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

// parseAt handles "@at <iso8601-with-tz>". Returns oneShotAt with the
// parsed instant. Canonical §4.1.1 requires an explicit TZ — naive
// timestamps without offset or Z are rejected so the operator cannot
// accidentally schedule a fire at the daemon's local clock when they meant
// UTC (and vice-versa).
func parseAt(s string) (*oneShotAt, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("schedule.Parse: '@at' requires an ISO 8601 timestamp")
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("schedule.Parse '@at %s': must be RFC3339 with explicit TZ (e.g. '2026-05-15T09:00:00Z' or '...+00:00'): %w", s, err)
		}
	}
	return &oneShotAt{when: t}, nil
}

// oneShotAt fires once at `when`, then never again.
//
// The `fired` field is plain bool — single-reactor design (E1.1 Task 11:
// one goroutine owns the heap + per-job Schedule lookups) means only one
// caller invokes Next() for a given job. A future multi-reactor / RPC-driven
// preview refactor must wrap this in sync/atomic.Bool or a Mutex.
type oneShotAt struct {
	when  time.Time
	fired bool
}

func (o *oneShotAt) Next(_ time.Time) time.Time {
	if o.fired {
		return time.Time{}
	}
	o.fired = true
	return o.when
}

// oneShotOnce fires at the reactor's "after" time (now) once, then never
// again. Reactor adds jitter at registration, not here. Same single-caller
// concurrency contract as oneShotAt.
type oneShotOnce struct {
	fired bool
}

func (o *oneShotOnce) Next(after time.Time) time.Time {
	if o.fired {
		return time.Time{}
	}
	o.fired = true
	return after
}
