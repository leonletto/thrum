package runtime

import "regexp"

// This file is the canonical home of the per-runtime pane-region anchor
// regexes consumed by the post-launch silence watchdog (see
// internal/daemon/rpc/tmux.go: paneAgentEngaged). One Anchor pair per
// supported runtime: BottomAnchorRegex separates the conversation
// transcript from the runtime's input chrome; SpinnerRegex matches the
// animated thinking indicator that should be ignored as chrome (not real
// agent output).
//
// Per spec §9.11.1 (B-B1 E6.10 Task 71) — extracted from presets.go so
// downstream smoke tests (E6.10 Task 72/73) and the runtime preset
// registry share a single import path for the patterns. Vars stay
// unexported; consumers route through RuntimePreset.BottomAnchorRegex /
// SpinnerRegex via runtime.GetPreset(name). This file is structurally
// the "consolidation target" the plan + spec named; behavior is
// unchanged from the prior in-presets.go definitions.

// claudeBottomAnchorRegex matches the horizontal rule (U+2500 × 20+) that
// Claude Code renders between the conversation transcript and the input chrome.
// Used by the silence watchdog (thrum-84xc) to bound the agent-output region.
var claudeBottomAnchorRegex = regexp.MustCompile(`^─{20,}$`)

// claudeSpinnerRegex matches Claude Code's animated thinking indicator.
// Three observed forms:
//   - Short form (<1m):  "✻ <verb> for <N>s"       — e.g. "✻ Churned for 17s"
//   - Long form  (≥1m):  "✻ <verb> for <Nm Ns>"    — e.g. "✻ Baked for 1m 45s"
//   - Dot form  (2.1.141+): "· <verb>…"             — e.g. "· Twisting…"
//
// ✻ = U+273B, · = U+00B7, … = U+2026. Present in the agent-output region
// while a turn is in flight; the watchdog ignores these lines as chrome.
// Uses \S+ instead of \w+ because some verbs contain non-ASCII characters
// (e.g. "Sautéed"). The long-form branch is non-capturing so the regex stays
// anchored at line end. thrum-8dl3, thrum-fyza.
var claudeSpinnerRegex = regexp.MustCompile(`^(?:✻ \S+ for \d+(?:m \d+)?s|· \S+…)$`)
