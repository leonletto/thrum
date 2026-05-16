package scheduler

import (
	"strings"
	"testing"
	"time"
)

// TestValidator_RejectsInternalPrefixInUserConfig: rule 1.
func TestValidator_RejectsInternalPrefixInUserConfig(t *testing.T) {
	s := New(Config{Location: time.UTC})
	// Simulate a daemon-registered internal job.
	s.handlers["internal.backup"] = &noopHandler{}
	s.specs["internal.backup"] = JobSpec{
		ID: "internal.backup", Type: "internal", Schedule: "@every 1h", Enabled: true,
	}

	userJobs := map[string]JobSpec{
		"internal.evil": {
			ID: "internal.evil", Type: "command", Schedule: "@every 5m", Enabled: true,
			Command: &CommandSpec{Exec: "/bin/echo"},
		},
	}
	errs := s.ValidateWholeConfig(userJobs)
	if len(errs) == 0 {
		t.Fatal("expected at least one error")
	}
	if !containsErrSubstr(errs, "reserved for daemon-registered") {
		t.Errorf("expected internal.* rejection; got %v", errs)
	}
}

// TestValidator_RejectsKebabCaseShape: rule 2.
func TestValidator_RejectsKebabCaseShape(t *testing.T) {
	s := New(Config{Location: time.UTC})
	userJobs := map[string]JobSpec{
		"InvalidID": {
			ID: "InvalidID", Type: "command", Schedule: "@every 5m", Enabled: true,
			Command: &CommandSpec{Exec: "/bin/echo"},
		},
		"starts-OK-then-bad": {
			ID: "starts-OK-then-bad", Type: "command", Schedule: "@every 5m", Enabled: true,
			Command: &CommandSpec{Exec: "/bin/echo"},
		},
	}
	errs := s.ValidateWholeConfig(userJobs)
	if len(errs) < 2 {
		t.Errorf("expected >= 2 errors (both ids invalid); got %d: %v", len(errs), errs)
	}
	for _, e := range errs {
		if !strings.Contains(e.Error(), "must match") {
			continue
		}
	}
}

// TestValidator_RejectsScheduledAgentFieldsOnNudge: rule 4.
func TestValidator_RejectsScheduledAgentFieldsOnNudge(t *testing.T) {
	s := New(Config{Location: time.UTC})
	userJobs := map[string]JobSpec{
		"bad-nudge": {
			ID: "bad-nudge", Type: "nudge", Schedule: "@every 5m", Enabled: true,
			Nudge:          &NudgeSpec{Target: "@agent", Message: "ping"},
			ScheduledAgent: &ScheduledAgentSpec{Target: "agent"},
		},
	}
	errs := s.ValidateWholeConfig(userJobs)
	if !containsErrSubstr(errs, "not permitted on type 'nudge'") {
		t.Errorf("expected scheduled-agent-on-nudge rejection; got %v", errs)
	}
}

// TestValidator_RejectsStageTimeoutsOnNudge: rule 4 second clause.
func TestValidator_RejectsStageTimeoutsOnNudge(t *testing.T) {
	s := New(Config{Location: time.UTC})
	userJobs := map[string]JobSpec{
		"bad-nudge-stages": {
			ID: "bad-nudge-stages", Type: "nudge", Schedule: "@every 5m", Enabled: true,
			Nudge:         &NudgeSpec{Target: "@agent", Message: "ping"},
			StageTimeouts: map[string]time.Duration{"x": time.Minute},
		},
	}
	errs := s.ValidateWholeConfig(userJobs)
	if !containsErrSubstr(errs, "stage_timeouts: not permitted on type 'nudge'") {
		t.Errorf("expected stage_timeouts rejection; got %v", errs)
	}
}

// TestValidator_RejectsMalformedSchedule: rule 5.
func TestValidator_RejectsMalformedSchedule(t *testing.T) {
	s := New(Config{Location: time.UTC})
	userJobs := map[string]JobSpec{
		"bad-schedule": {
			ID: "bad-schedule", Type: "command", Schedule: "not a cron", Enabled: true,
			Command: &CommandSpec{Exec: "/bin/echo"},
		},
	}
	errs := s.ValidateWholeConfig(userJobs)
	if len(errs) == 0 {
		t.Error("expected schedule parse error")
	}
}

// TestValidator_RequiresSchedule: rule 5 missing case.
func TestValidator_RequiresSchedule(t *testing.T) {
	s := New(Config{Location: time.UTC})
	userJobs := map[string]JobSpec{
		"no-schedule": {
			ID: "no-schedule", Type: "command", Schedule: "", Enabled: true,
			Command: &CommandSpec{Exec: "/bin/echo"},
		},
	}
	errs := s.ValidateWholeConfig(userJobs)
	if !containsErrSubstr(errs, "schedule: required") {
		t.Errorf("expected schedule-required error; got %v", errs)
	}
}

// TestValidator_RejectsRunAtStartWithOneShot: rule 6.
func TestValidator_RejectsRunAtStartWithOneShot(t *testing.T) {
	s := New(Config{Location: time.UTC})
	cases := map[string]string{
		"once":  "@once",
		"at":    "@at 2026-05-15T09:00:00Z",
	}
	for name, sched := range cases {
		userJobs := map[string]JobSpec{
			"redundant-ras-" + name: {
				ID: "redundant-ras-" + name, Type: "command",
				Schedule: sched, RunAtStart: true, Enabled: true,
				Command: &CommandSpec{Exec: "/bin/echo"},
			},
		}
		errs := s.ValidateWholeConfig(userJobs)
		if !containsErrSubstr(errs, "incompatible with one-shot") {
			t.Errorf("%s: expected one-shot+run_at_start rejection; got %v", name, errs)
		}
	}
}

// TestValidator_RequiredPerTypeFields: rule 7.
func TestValidator_RequiredPerTypeFields(t *testing.T) {
	s := New(Config{Location: time.UTC})
	userJobs := map[string]JobSpec{
		"no-command": {
			ID: "no-command", Type: "command", Schedule: "@every 5m", Enabled: true,
			Command: nil,
		},
		"no-thrum-args": {
			ID: "no-thrum-args", Type: "thrum_command", Schedule: "@every 5m", Enabled: true,
			ThrumCommand: &ThrumCommandSpec{Args: nil},
		},
		"no-target": {
			ID: "no-target", Type: "scheduled_agent", Schedule: "@every 5m", Enabled: true,
			ScheduledAgent: &ScheduledAgentSpec{Primer: "p"},
		},
		"no-nudge-target": {
			ID: "no-nudge-target", Type: "nudge", Schedule: "@every 5m", Enabled: true,
			Nudge: &NudgeSpec{Message: "m"},
		},
		"empty-type": {
			ID: "empty-type", Type: "", Schedule: "@every 5m", Enabled: true,
		},
		"unknown-type": {
			ID: "unknown-type", Type: "what", Schedule: "@every 5m", Enabled: true,
		},
	}
	errs := s.ValidateWholeConfig(userJobs)
	wantSubstrings := []string{
		"command.exec: required",
		"thrum_command.args: required",
		"scheduled_agent.target: required",
		"nudge.target: required",
		"type: required",
		"unknown type",
	}
	for _, sub := range wantSubstrings {
		if !containsErrSubstr(errs, sub) {
			t.Errorf("missing expected error %q in %v", sub, errs)
		}
	}
}

// TestValidator_WholeConfigReportsAllErrors: rule 1+2+5+7 stacked in one
// pass — whole-config does NOT bail at the first error.
func TestValidator_WholeConfigReportsAllErrors(t *testing.T) {
	s := New(Config{Location: time.UTC})
	s.handlers["internal.backup"] = &noopHandler{}
	s.specs["internal.backup"] = JobSpec{ID: "internal.backup", Type: "internal"}

	userJobs := map[string]JobSpec{
		"internal.evil": {
			ID: "internal.evil", Type: "command", Schedule: "@every 5m", Enabled: true,
			Command: &CommandSpec{Exec: "/bin/echo"},
		},
		"InvalidID": {
			ID: "InvalidID", Type: "command", Schedule: "not a cron", Enabled: true,
			Command: &CommandSpec{Exec: "/bin/echo"},
		},
		"missing-command": {
			ID: "missing-command", Type: "command", Schedule: "@every 5m", Enabled: true,
		},
	}
	errs := s.ValidateWholeConfig(userJobs)
	if len(errs) < 4 {
		t.Errorf("expected >= 4 errors (internal prefix + bad ID + bad schedule + missing command); got %d: %v", len(errs), errs)
	}
}

// TestValidator_EmptyConfig: zero user jobs is valid.
func TestValidator_EmptyConfig(t *testing.T) {
	s := New(Config{Location: time.UTC})
	if errs := s.ValidateWholeConfig(nil); len(errs) != 0 {
		t.Errorf("nil userJobs: got %d errors; want 0", len(errs))
	}
	if errs := s.ValidateWholeConfig(map[string]JobSpec{}); len(errs) != 0 {
		t.Errorf("empty userJobs: got %d errors; want 0", len(errs))
	}
}

// TestValidator_HappyPath: a clean spec yields no errors.
func TestValidator_HappyPath(t *testing.T) {
	s := New(Config{Location: time.UTC})
	userJobs := map[string]JobSpec{
		"backup-mind": {
			ID: "backup-mind", Type: "command", Schedule: "@every 1h", Enabled: true,
			Command: &CommandSpec{Exec: "/usr/bin/true"},
		},
		"docs-poll": {
			ID: "docs-poll", Type: "thrum_command", Schedule: "0 9 * * *", Enabled: true,
			ThrumCommand: &ThrumCommandSpec{Args: []string{"status"}},
		},
	}
	if errs := s.ValidateWholeConfig(userJobs); len(errs) != 0 {
		t.Errorf("happy-path config rejected: %v", errs)
	}
}

func containsErrSubstr(errs []error, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e.Error(), sub) {
			return true
		}
	}
	return false
}
