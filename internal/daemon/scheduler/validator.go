package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/daemon/scheduler/schedule"
)

// ValidateWholeConfig runs all 8 validation rules from spec §4.1 across an
// entire user-jobs map. Whole-config behavior: reports ALL errors per call
// rather than bailing on the first — operators get one round-trip with
// every problem surfaced, not eight reload cycles each surfacing the next.
//
// Validation rules:
//
//  1. `internal.*` prefix is reserved for daemon-registered jobs (panics
//     in RegisterInternal; rejected here for user config).
//  2. Kebab-case ID shape per canonical §3.11: ^[a-z][a-z0-9-]{0,63}$.
//  3. ID collision with internal-registry (defensive — rule 1 catches
//     most cases since internal jobs require the prefix).
//  4. scheduled_agent-only fields (ScheduledAgent, StageTimeouts) on a
//     nudge job.
//  5. Malformed schedule string (rejected by schedule.Parse).
//  6. run_at_start + one-shot schedule combination — `@once` /
//     `@at <iso8601>` fire exactly once at start anyway, so RunAtStart
//     is redundant and confusing.
//  7. Required per-type fields: command.exec, thrum_command.args,
//     scheduled_agent.target+primer, nudge.target+message.
//  8. Unknown top-level keys (enforced at the JSON parse layer via
//     json.Decoder.DisallowUnknownFields by ReloadConfig — Task 32).
//     ValidateWholeConfig sees only typed JobSpec values, so unknown
//     keys have already been rejected before reaching this function.
//
// Empty userJobs map returns nil — empty config is valid.
func (s *Scheduler) ValidateWholeConfig(userJobs map[string]JobSpec) []error {
	if len(userJobs) == 0 {
		return nil
	}
	var errs []error
	for id, spec := range userJobs {
		errs = append(errs, s.validateOneJob(id, spec)...)
	}
	return errs
}

// validateOneJob returns the rules 1-7 violations for a single user job.
// Pulled out so the per-job validator can be reused by E1.4's job.create /
// job.update RPC handlers (single-job validation against the same rules).
func (s *Scheduler) validateOneJob(id string, spec JobSpec) []error {
	var errs []error

	// Rule 1: internal.* prefix reserved for daemon-registered jobs.
	if strings.HasPrefix(id, InternalPrefix) {
		errs = append(errs, fmt.Errorf("jobs.%s.id: %q prefix is reserved for daemon-registered jobs", id, InternalPrefix))
	} else {
		// Rule 2: kebab-case shape (only check non-internal ids; rule 1
		// already rejected internal-prefixed ones with a clearer error).
		if !idRE.MatchString(id) {
			errs = append(errs, fmt.Errorf("jobs.%s.id: must match %s", id, idRE.String()))
		}
	}

	// Rule 3: collision with daemon-registered handler. Rule 1 already
	// rejects user IDs with the internal.* prefix; this rule covers the
	// orthogonal case where a user supplies an ID that happens to match
	// a registered handler (currently only possible if rule 1 is bypassed
	// or relaxed, but kept as defense-in-depth so the validator's
	// "collision" diagnostic always surfaces when the ID is double-bound).
	s.mu.RLock()
	_, internalCollision := s.handlers[id]
	s.mu.RUnlock()
	if internalCollision {
		errs = append(errs, fmt.Errorf("jobs.%s.id: collides with daemon-registered internal job", id))
	}

	// Rule 4: scheduled_agent-only fields on nudge.
	if spec.Type == "nudge" {
		if spec.ScheduledAgent != nil {
			errs = append(errs, fmt.Errorf("jobs.%s.scheduled_agent: not permitted on type 'nudge'", id))
		}
		if len(spec.StageTimeouts) > 0 {
			errs = append(errs, fmt.Errorf("jobs.%s.stage_timeouts: not permitted on type 'nudge'", id))
		}
	}

	// Rule 5: schedule required + parseable.
	if spec.Schedule == "" {
		errs = append(errs, fmt.Errorf("jobs.%s.schedule: required", id))
	} else {
		loc := s.cfg.Location
		if loc == nil {
			// Defensive: ReloadConfig (Task 32) sets Config.Location before
			// any validation call lands here. Falling back to UTC keeps the
			// validator usable from unit tests that hand-construct a
			// Scheduler with an empty Config.
			loc = time.UTC
		}
		if _, err := schedule.Parse(spec.Schedule, schedule.ParseOpts{Location: loc}); err != nil {
			errs = append(errs, fmt.Errorf("jobs.%s.schedule: %w", id, err))
		}
	}

	// Rule 6: run_at_start + one-shot is redundant; reject so operators
	// don't mistakenly assume the one-shot fires twice.
	if spec.RunAtStart && isOneShotSchedule(spec.Schedule) {
		errs = append(errs, fmt.Errorf("jobs.%s.run_at_start: incompatible with one-shot schedule '%s'", id, spec.Schedule))
	}

	// Rule 7: required per-type sub-tree fields.
	errs = append(errs, validateTypeFields(id, spec)...)

	return errs
}

// validateTypeFields enforces the per-type required-fields rules.
func validateTypeFields(id string, spec JobSpec) []error {
	var errs []error
	switch spec.Type {
	case "":
		errs = append(errs, fmt.Errorf("jobs.%s.type: required", id))
	case "command":
		if spec.Command == nil || spec.Command.Exec == "" {
			errs = append(errs, fmt.Errorf("jobs.%s.command.exec: required", id))
		}
	case "thrum_command":
		if spec.ThrumCommand == nil || len(spec.ThrumCommand.Args) == 0 {
			errs = append(errs, fmt.Errorf("jobs.%s.thrum_command.args: required (non-empty argv slice)", id))
		}
	case "scheduled_agent":
		if spec.ScheduledAgent == nil {
			errs = append(errs, fmt.Errorf("jobs.%s.scheduled_agent: required", id))
		} else {
			if spec.ScheduledAgent.Target == "" {
				errs = append(errs, fmt.Errorf("jobs.%s.scheduled_agent.target: required", id))
			}
			if spec.ScheduledAgent.Primer == "" {
				errs = append(errs, fmt.Errorf("jobs.%s.scheduled_agent.primer: required", id))
			}
		}
	case "nudge":
		if spec.Nudge == nil {
			errs = append(errs, fmt.Errorf("jobs.%s.nudge: required", id))
		} else {
			if spec.Nudge.Target == "" {
				errs = append(errs, fmt.Errorf("jobs.%s.nudge.target: required", id))
			}
			if spec.Nudge.Message == "" {
				errs = append(errs, fmt.Errorf("jobs.%s.nudge.message: required", id))
			}
		}
	default:
		errs = append(errs, fmt.Errorf("jobs.%s.type: unknown type %q", id, spec.Type))
	}
	return errs
}

// isOneShotSchedule reports whether `s` is a one-shot canonical §4.1.1
// schedule (`@once` or `@at <iso8601>`).
func isOneShotSchedule(s string) bool {
	return s == "@once" || strings.HasPrefix(s, "@at ")
}
