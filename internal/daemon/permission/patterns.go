// Package permission implements detection of stuck permission prompts
// in tmux-managed agent sessions and delivery of actionable nudges to
// configured supervisors. See dev-docs/specs/2026-04-14-permission-
// prompt-detection-design.md for the full design.
package permission

import "regexp"

// Pattern describes a single recognizable permission-prompt shape for
// one runtime, along with the keystrokes that answer it.
type Pattern struct {
	// Name is a short stable identifier, used in nudge bodies and logs.
	// Convention: lowercase, underscores, no whitespace.
	Name string

	// Regex matches against the captured pane tail (last ~20 lines).
	// Patterns should be anchored and precise — a false positive here
	// sends a spurious keystroke into a user's pane.
	Regex *regexp.Regexp

	// ApproveKey is the keystroke that grants single-invocation approval.
	// MUST NOT map to a "don't ask again" / "add to allowlist" /
	// "auto-run everything" option. Enforced by
	// TestApproveKeyNeverForeverAllow.
	ApproveKey string

	// DenyKey is the keystroke that refuses the operation. Empty if the
	// runtime has no explicit deny (only interrupt/skip). The nudge body
	// shows a "press Ctrl+C to interrupt" fallback when empty.
	//
	// NOTE: some runtimes (notably claude) ship multiple prompt variants
	// with the same anchor but different option lists. The dispatch
	// layer may need to inspect the captured pane tail to decide which
	// key to actually send — see the Claude Code section of
	// dev-docs/plans/2026-04-14-permission-prompt-samples.md for the
	// Variant A (3-option) vs Variant B (2-option) rule.
	DenyKey string

	// Comment is a short human-readable description of the prompt shape,
	// surfaced in nudge bodies and debug output.
	Comment string
}

// patterns holds the compiled-in per-runtime pattern library. Keys are
// canonical runtime names matching `identity.Runtime` (claude, codex,
// cursor, opencode, kiro-cli, auggie). Populated at package-init time;
// edits require a thrum rebuild.
var patterns = map[string][]Pattern{
	// Populated in Task 2.2 and Task 2.3.
}

// Match finds the first pattern for the given runtime that matches the
// captured pane content. Returns nil if no pattern matches or the
// runtime is unknown.
func Match(runtime, pane string) *Pattern {
	for i := range patterns[runtime] {
		if patterns[runtime][i].Regex.MatchString(pane) {
			return &patterns[runtime][i]
		}
	}
	return nil
}
