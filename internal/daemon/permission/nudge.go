package permission

import "time"

// NudgeRow is an in-memory representation of a permission_nudges row.
// Exported fields map 1:1 to the SQLite columns in the v21 schema.
type NudgeRow struct {
	// MessageID is the msg_id of the first nudge message we sent for
	// this (session, prompt) pair. Primary key.
	MessageID string

	// Session is the tmux session name, e.g. "cursor-test". Indexed so
	// scheduler lookups by session are fast.
	Session string

	// TmuxTarget is the pre-resolved full session:window.pane target
	// captured from the agent's IdentityFile.TmuxSession at detection
	// time. Used directly in ttmux.SendKeys — matches how every other
	// caller in the codebase addresses panes.
	TmuxTarget string

	// AgentName is the registered agent name, surfaced in nudge bodies.
	AgentName string

	// PatternKey is "runtime.pattern_name", e.g. "cursor.not_in_allowlist".
	// Stored for display; the actual keystrokes are cached in
	// ApproveKey/DenyKey.
	PatternKey string

	// ApproveKey and DenyKey are the single-invocation keystrokes
	// copied from the matched Pattern at insert time. Stored explicitly
	// so a pattern library edit between first-nudge and reply doesn't
	// race with in-flight nudges.
	ApproveKey string
	DenyKey    string

	// FirstDetected anchors the reminder schedule.
	FirstDetected time.Time

	// LastNudgeAt is the timestamp of the most recent nudge sent.
	LastNudgeAt time.Time

	// NudgeCount is how many nudges have fired (1..6). Cap at 6 = give up.
	NudgeCount int

	// LastPaneHash is sha256 of the pane tail at the last nudge. If the
	// hash still matches on the next silence fire, the scheduler
	// advances to the next reminder. If it differs, it's treated as a
	// new prompt (reset counter, fresh first-nudge).
	LastPaneHash [32]byte

	// ExpiresAt is when this row should be evicted if still unresolved.
	// Set at insert time to FirstDetected + 8h.
	ExpiresAt time.Time
}
