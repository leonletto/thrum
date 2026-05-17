package reminders

import (
	"encoding/json"
	"testing"
	"time"
)

func timePtr(t time.Time) *time.Time { return &t }

func TestValidate_NilReminder(t *testing.T) {
	if err := Validate(nil); err == nil {
		t.Error("expected error for nil reminder")
	}
}

func TestValidate_DaemonConditionPaneQuiet_AcceptsValid(t *testing.T) {
	r := &Reminder{
		Source:       SourceDaemon,
		TriggerKind:  TriggerConditionPaneQuiet,
		TriggerMeta:  json.RawMessage(`{"agent":"x","quiet_since":1700000000}`),
		TargetChain:  []string{"@coordinator_main", "leon@example.com"},
		PaneSnapshot: "non-empty",
		State:        StateOpen,
		RaisedAt:     time.Now(),
	}
	if err := Validate(r); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_DaemonConditionPaneQuiet_StripEachRequiredField(t *testing.T) {
	base := &Reminder{
		Source:       SourceDaemon,
		TriggerKind:  TriggerConditionPaneQuiet,
		TriggerMeta:  json.RawMessage(`{"agent":"x"}`),
		TargetChain:  []string{"@coord"},
		PaneSnapshot: "x",
	}
	if err := Validate(base); err != nil {
		t.Fatalf("base valid: got %v", err)
	}
	cases := map[string]func(*Reminder){
		"missing trigger_meta":  func(r *Reminder) { r.TriggerMeta = nil },
		"missing target_chain":  func(r *Reminder) { r.TargetChain = nil },
		"missing pane_snapshot": func(r *Reminder) { r.PaneSnapshot = "" },
	}
	for name, mut := range cases {
		r := *base
		// Deep-ish copy of TargetChain so the per-case mutation doesn't
		// leak across iterations.
		r.TargetChain = append([]string(nil), base.TargetChain...)
		mut(&r)
		if err := Validate(&r); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestValidate_AgentTime_AcceptsValid(t *testing.T) {
	r := &Reminder{
		Source:      SourceAgent,
		TriggerKind: TriggerTime,
		SourceAgent: "docs_bot",
		TriggerAt:   timePtr(time.Now().Add(time.Hour)),
		TargetAgent: "docs_bot",
		Body:        "finish release notes",
	}
	if err := Validate(r); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_AgentTime_StripEachRequiredField(t *testing.T) {
	base := &Reminder{
		Source:      SourceAgent,
		TriggerKind: TriggerTime,
		SourceAgent: "docs_bot",
		TriggerAt:   timePtr(time.Now().Add(time.Hour)),
		TargetAgent: "docs_bot",
		Body:        "finish release notes",
	}
	cases := map[string]func(*Reminder){
		"missing source_agent": func(r *Reminder) { r.SourceAgent = "" },
		"missing trigger_at":   func(r *Reminder) { r.TriggerAt = nil },
		"missing target_agent": func(r *Reminder) { r.TargetAgent = "" },
		"missing body":         func(r *Reminder) { r.Body = "" },
	}
	for name, mut := range cases {
		r := *base
		mut(&r)
		if err := Validate(&r); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestValidate_UserTime_AcceptsValid(t *testing.T) {
	r := &Reminder{
		Source:      SourceUser,
		TriggerKind: TriggerTime,
		TriggerAt:   timePtr(time.Now().Add(15 * time.Minute)),
		TargetAgent: "leon",
		Body:        "stand-up at 9am",
	}
	if err := Validate(r); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_UserTime_StripEachRequiredField(t *testing.T) {
	base := &Reminder{
		Source:      SourceUser,
		TriggerKind: TriggerTime,
		TriggerAt:   timePtr(time.Now().Add(15 * time.Minute)),
		TargetAgent: "leon",
		Body:        "stand-up",
	}
	cases := map[string]func(*Reminder){
		"missing trigger_at":   func(r *Reminder) { r.TriggerAt = nil },
		"missing target_agent": func(r *Reminder) { r.TargetAgent = "" },
		"missing body":         func(r *Reminder) { r.Body = "" },
	}
	for name, mut := range cases {
		r := *base
		mut(&r)
		if err := Validate(&r); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// daemon/time was added to canonical §3.5 row 4 on 2026-05-15 — used by
// C-B1 for skill-proposal staleness reminders (C-B1 spec §13.1) and
// future daemon-author time-triggered reminders (e.g. backup-due nudge).
func TestValidate_DaemonTime_AcceptsValid(t *testing.T) {
	r := &Reminder{
		Source:      SourceDaemon,
		TriggerKind: TriggerTime,
		TriggerAt:   timePtr(time.Now().Add(24 * time.Hour)),
		TargetChain: []string{"@coordinator_main"},
		Body:        "skill proposal awaiting review (C-B1)",
	}
	if err := Validate(r); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_DaemonTime_StripEachRequiredField(t *testing.T) {
	base := &Reminder{
		Source:      SourceDaemon,
		TriggerKind: TriggerTime,
		TriggerAt:   timePtr(time.Now().Add(24 * time.Hour)),
		TargetChain: []string{"@coord"},
		Body:        "skill proposal",
	}
	cases := map[string]func(*Reminder){
		"missing trigger_at":   func(r *Reminder) { r.TriggerAt = nil },
		"missing target_chain": func(r *Reminder) { r.TargetChain = nil },
		"missing body":         func(r *Reminder) { r.Body = "" },
	}
	for name, mut := range cases {
		r := *base
		r.TargetChain = append([]string(nil), base.TargetChain...)
		mut(&r)
		if err := Validate(&r); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestValidate_UnknownCombinations_Reject(t *testing.T) {
	cases := map[string]*Reminder{
		"unknown source": {
			Source:      "weird",
			TriggerKind: TriggerTime,
		},
		"unknown trigger_kind": {
			Source:      SourceAgent,
			TriggerKind: "condition_unsupported",
		},
		// agent/condition_pane_quiet is not in the §3.5 polymorphism table
		// — sweep is daemon-authored; agents don't mint condition rows.
		"agent + condition_pane_quiet": {
			Source:      SourceAgent,
			TriggerKind: TriggerConditionPaneQuiet,
		},
		// user/condition_pane_quiet is similarly outside the matrix.
		"user + condition_pane_quiet": {
			Source:      SourceUser,
			TriggerKind: TriggerConditionPaneQuiet,
		},
		// Empty source/trigger_kind.
		"empty source": {
			TriggerKind: TriggerTime,
		},
		"empty trigger_kind": {
			Source: SourceAgent,
		},
	}
	for name, r := range cases {
		if err := Validate(r); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
