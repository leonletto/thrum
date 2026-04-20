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
//
// Safety invariant: every ApproveKey must pass
// TestApproveKeyNeverForeverAllow. A supervisor's approval must NEVER
// land on a "don't ask again" / "add to allowlist" / "auto-run
// everything" option.
var patterns = map[string][]Pattern{
	"cursor": {
		{
			Name:       "not_in_allowlist",
			Regex:      regexp.MustCompile(`(?m)^\s*Not in allowlist:`),
			ApproveKey: "y",      // Run (once)
			DenyKey:    "Escape", // Skip (esc or n)
			Comment:    "Cursor allowlist approval prompt",
		},
	},
	"claude": {
		{
			// NOTE: claude ships TWO prompt variants sharing this anchor.
			// Variant A (3-option Write/Exec): 1=Yes, 2=forever-allow, 3=No
			// Variant B (2-option Read): 1=Yes, 2=path-scoped forever-allow
			//
			// The pattern library holds ONE row; the dispatch/nudge layer
			// inspects paneTail before sending the deny key — if the pane
			// contains regex `(?m)^\s*3\.\s+No,\s+and tell Claude` it's
			// Variant A (deny=3); otherwise Variant B (deny=Escape). See
			// dev-docs/plans/2026-04-14-permission-prompt-samples.md for
			// the raw captures and full disambiguation rationale.
			//
			// approve_key='1' and forbidden={'2','Tab'} apply uniformly.
			Name: "tool_confirmation",
			// Anchor requires BOTH the question and a structural marker
			// unique to the real Claude UI dialog. Pre-thrum-48kt.7 the
			// pattern only anchored on "Do you want to proceed?" at
			// start-of-line, so coordinator-agent plan summaries ending
			// with that conversational phrase were matched as real
			// tool-confirmation prompts and routed to the supervisor
			// (false nudge observed 2026-04-20 04:42 UTC).
			//
			// Accepted structural markers (any one of three, within 500
			// chars after the question so panes containing the phrase in
			// prose with an unrelated permission dialog far below don't
			// transitively match):
			//
			//  - "❯ <digit>." — the arrow selector on a numbered option
			//    line (Variant B / Read prompt).
			//  - "Esc to cancel · Tab to amend" — Variant B footer. The
			//    middle dot (U+00B7) is unique to the Claude UI and
			//    essentially never appears in plain prose.
			//  - "No, and tell Claude what to do differently" — Variant
			//    A option 3 text. Long and distinctive enough to be
			//    safe as a sole anchor.
			//
			// See dev-docs/plans/2026-04-14-permission-prompt-samples.md
			// for the raw captures.
			Regex: regexp.MustCompile(`(?ms)^\s*(?:⎿\s+)?Do you want to proceed\?.{0,500}(?:^\s*❯\s+\d+\.\s|Esc to cancel · Tab to amend|No,\s+and tell Claude what to do differently)`),
			ApproveKey: "1", // Yes (once) — NEVER "2" (don't ask again)
			DenyKey:    "3", // Variant A default; dispatch overrides to Escape for Variant B
			Comment:    "Claude Code tool-use confirmation (two variants — dispatch disambiguates)",
		},
	},
	"codex": {
		{
			Name:       "tool_confirmation",
			Regex:      regexp.MustCompile(`(?m)^\s*Would you like to run the following command\?`),
			ApproveKey: "1", // Yes, proceed — NEVER "2" (forever-allow for prefix)
			DenyKey:    "3", // No, and tell Codex what to do differently
			Comment:    "OpenAI Codex CLI tool-use confirmation",
		},
	},
	"opencode": {
		{
			// OpenCode does NOT gate shell commands; it gates reads/writes
			// to paths outside the worktree. Default selection on a fresh
			// prompt is "Allow once" (leftmost), so Option A (bare Enter)
			// approves-once. If default drift is ever observed, extend
			// sendKeystroke to support comma-separated sequences and
			// switch to Option B ("Home,Enter").
			//
			// The leading character class allows either plain whitespace
			// or OpenCode's left-border box-drawing characters (U+2503
			// "┃" heavy vertical, U+2502 "│" light vertical). Without
			// this, live tmux captures of OpenCode's bordered UI never
			// match — the raw samples doc had unbordered text so the
			// original anchor passed its unit test but failed in
			// production. Verified live during Epic E smoke tests.
			Name:       "permission_required",
			Regex:      regexp.MustCompile(`(?m)^[\s│┃]*△\s*Permission required`),
			ApproveKey: "Enter",     // Default selection "Allow once"
			DenyKey:    "End,Enter", // Navigate to rightmost "Reject" then confirm
			Comment:    "OpenCode external directory access prompt (default-selection approve-once)",
		},
	},
	"kiro-cli": {
		{
			// Default-selection on a fresh prompt is "Yes, single permission"
			// (verified live 2026-04-14). The risky option "Trust, always
			// allow in this session" is reachable only via Down+Enter,
			// which is why we forbid that sequence in the invariant test.
			Name:       "shell_approval",
			Regex:      regexp.MustCompile(`(?m)^\s*shell requires approval`),
			ApproveKey: "Enter",  // Default "Yes, single permission"
			DenyKey:    "Escape", // Closes the prompt
			Comment:    "Kiro CLI shell-command approval (default-selection is single-permission)",
		},
	},
	"auggie": {
		{
			// CRITICAL: the default-highlighted option on this prompt is
			// [1] "Always index this workspace" — a forever-allow trap
			// that persists to ~/.augment/settings.json. A bare Enter
			// silently activates it. approve_key MUST be "3" (session-
			// only) and TestApproveKeyNeverForeverAllow forbids both "1"
			// and "Enter" for this pattern.
			Name: "indexing_consent",
			// Anchor at start-of-line (with optional leading chrome like
			// '→ ' and bracketed option numbers) so a shell command or
			// doc string containing the phrase in its output cannot
			// trigger a spurious match.
			Regex:      regexp.MustCompile(`(?m)^\s*(?:[→>]\s+)?(?:\[\d+\]\s+)?Always index this workspace`),
			ApproveKey: "3",      // Index this workspace for this session — NEVER "1" or "Enter"
			DenyKey:    "Escape", // Documented "Esc to skip"
			Comment:    "Auggie workspace indexing consent (approve session-only; default-highlighted is forever-allow)",
		},
		{
			// auggie's per-tool approval (save-file, launch-process, etc.)
			// is the cleanest surface in the set: two options, single-
			// letter hotkeys, no forever-allow in the prompt itself
			// (forever-allow lives in ~/.augment/settings.json only).
			//
			// The phrase "Tool Approval Required" appears inside a boxed
			// header: `│  Tool Approval Required   │`. Anchoring to the
			// box chrome eliminates false-positives from status-bar /
			// log output containing the bare phrase. The regex tolerates
			// variable interior spacing produced by auggie's box layout.
			Name:       "tool_approval",
			Regex:      regexp.MustCompile(`(?m)^\s*│\s+Tool Approval Required\s+│`),
			ApproveKey: "A", // [A]llow — verified live
			DenyKey:    "D", // [D]eny
			Comment:    "Auggie per-tool approval (save-file, launch-process, etc.)",
		},
	},
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

// LookupPattern returns the Pattern with the given Name under the
// given runtime, or nil if no such pattern exists. Used by
// HandleCheckPane to reverse-resolve a reason string of the form
// "permission:<runtime>.<name>" back into the Pattern struct — we
// cannot round-trip through Match because the original pane
// content is what drove the reason in the first place, and
// re-running Match would both duplicate work and couple the RPC
// handler to the full pattern library.
func LookupPattern(runtime, name string) *Pattern {
	for i := range patterns[runtime] {
		if patterns[runtime][i].Name == name {
			return &patterns[runtime][i]
		}
	}
	return nil
}
