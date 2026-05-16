package reminders

import "fmt"

// Validate enforces the polymorphism rules from canonical-ref §3.5. Each
// (Source, TriggerKind) combination requires a specific column set; unknown
// combinations are rejected. Called at every INSERT and at any UPDATE that
// touches a column appearing in any of the required sets.
//
// Returns nil if the combination is recognized and all required columns are
// populated; returns a descriptive error otherwise.
//
// Per §3.5 implementation-standards rule (plan v2.1 Implementation Standards
// #1): polymorphism is enforced at mint time, NOT at fire time. Fire-time
// guards are defense-in-depth against a bug that shouldn't exist by then.
func Validate(r *Reminder) error {
	if r == nil {
		return fmt.Errorf("nil reminder")
	}
	switch {
	case r.Source == SourceDaemon && r.TriggerKind == TriggerConditionPaneQuiet:
		if len(r.TriggerMeta) == 0 {
			return fmt.Errorf("daemon/condition_pane_quiet: trigger_meta required")
		}
		if len(r.TargetChain) == 0 {
			return fmt.Errorf("daemon/condition_pane_quiet: target_chain required (non-empty)")
		}
		if r.PaneSnapshot == "" {
			return fmt.Errorf("daemon/condition_pane_quiet: pane_snapshot required")
		}
	case r.Source == SourceAgent && r.TriggerKind == TriggerTime:
		if r.SourceAgent == "" {
			return fmt.Errorf("agent/time: source_agent required")
		}
		if r.TriggerAt == nil {
			return fmt.Errorf("agent/time: trigger_at required")
		}
		if r.TargetAgent == "" {
			return fmt.Errorf("agent/time: target_agent required")
		}
		if r.Body == "" {
			return fmt.Errorf("agent/time: body required")
		}
	case r.Source == SourceUser && r.TriggerKind == TriggerTime:
		if r.TriggerAt == nil {
			return fmt.Errorf("user/time: trigger_at required")
		}
		if r.TargetAgent == "" {
			return fmt.Errorf("user/time: target_agent required")
		}
		if r.Body == "" {
			return fmt.Errorf("user/time: body required")
		}
	case r.Source == SourceDaemon && r.TriggerKind == TriggerTime:
		if r.TriggerAt == nil {
			return fmt.Errorf("daemon/time: trigger_at required")
		}
		if len(r.TargetChain) == 0 {
			return fmt.Errorf("daemon/time: target_chain required (non-empty)")
		}
		if r.Body == "" {
			return fmt.Errorf("daemon/time: body required")
		}
	default:
		return fmt.Errorf("unknown (source=%q, trigger_kind=%q) combination", r.Source, r.TriggerKind)
	}
	return nil
}
