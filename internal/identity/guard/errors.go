package guard

import (
	"fmt"
	"strings"
)

// Error is the structured failure returned by every guard running in
// ModeStrict. The fields are intentionally flat — callers usually log
// the whole thing as a single slog.Attr bag, and operators read
// Error() as a human-friendly summary. Zero-valued optional fields are
// omitted from the rendered message so G1a (non_git_bootstrap) isn't
// cluttered with irrelevant PID placeholders.
type Error struct {
	// Guard is the config key of the guard that triggered (e.g.
	// "cross_worktree", "non_git_bootstrap").
	Guard string

	// Reason is a short snake_case code describing the specific
	// failure mode within the guard (e.g. "pid_mismatch",
	// "name_collision", "non_git", "dead_pid"). Separate from Guard
	// so one guard can emit multiple reason codes.
	Reason string

	// CallerPID is the PID of the process that attempted the
	// guarded action. Zero means "not applicable" for guards that
	// fire before a caller can be identified.
	CallerPID int

	// CallerCWD is the working directory the caller invoked from.
	CallerCWD string

	// ExpectedAgent is the agent name whose identity file is being
	// protected (the rightful owner of the worktree, the active
	// daemon writer, etc.).
	ExpectedAgent string

	// DetectedAgent is the agent name inferred from the caller's
	// environment (CWD + runtime + TMUX). It may differ from
	// ExpectedAgent when a second agent cds into someone else's
	// worktree — that delta is the whole reason cross_worktree
	// fires. Blank when the caller environment does not resolve to
	// a registered agent.
	DetectedAgent string

	// ExpectedPID is the PID currently recorded in the identity
	// file. Zero omits the field from the rendered message.
	ExpectedPID int

	// Remediation is an operator-actionable hint — usually a
	// concrete command to run ("cd /a/foo and retry").
	Remediation string
}

// Error renders the guard violation as a multi-line string suitable
// for logging or returning to a CLI user.
func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "identity guard %q fired: %s", e.Guard, e.Reason)
	if e.ExpectedAgent != "" {
		fmt.Fprintf(&b, "\n  expected agent: %s", e.ExpectedAgent)
	}
	if e.DetectedAgent != "" {
		fmt.Fprintf(&b, "\n  detected agent: %s", e.DetectedAgent)
	}
	if e.ExpectedPID != 0 {
		fmt.Fprintf(&b, "\n  expected pid: %d", e.ExpectedPID)
	}
	if e.CallerPID != 0 {
		fmt.Fprintf(&b, "\n  caller pid: %d", e.CallerPID)
	}
	if e.CallerCWD != "" {
		fmt.Fprintf(&b, "\n  caller cwd: %s", e.CallerCWD)
	}
	if e.Remediation != "" {
		fmt.Fprintf(&b, "\n  remediation: %s", e.Remediation)
	}
	return b.String()
}
