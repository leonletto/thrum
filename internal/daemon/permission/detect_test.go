package permission

import (
	"strings"
	"testing"
)

func TestDetectPaneState_CursorMatch(t *testing.T) {
	pane := "Run this command?\nNot in allowlist: curl\n → Run (once) (y)\n"
	got := DetectPaneState("cursor", pane)
	want := "permission:cursor.not_in_allowlist"
	if got != want {
		t.Errorf("DetectPaneState = %q, want %q", got, want)
	}
}

func TestDetectPaneState_ClaudeMatch(t *testing.T) {
	// Full Variant A option list — the tightened tool_confirmation
	// regex (thrum-48kt.7) requires a structural marker alongside
	// "Do you want to proceed?" so conversational prose ending with
	// the same question doesn't match. Option 3's distinctive
	// "No, and tell Claude what to do differently" text is the
	// marker here.
	pane := "⏺ Bash(curl)\n  ⎿  Do you want to proceed?\n     1. Yes\n     2. Yes, and don't ask again for Bash(curl)\n     3. No, and tell Claude what to do differently (Esc)\n"
	got := DetectPaneState("claude", pane)
	want := "permission:claude.tool_confirmation"
	if got != want {
		t.Errorf("DetectPaneState = %q, want %q", got, want)
	}
}

func TestDetectPaneState_AuggieIndexing(t *testing.T) {
	pane := "Choose an option:\n\n   → [1] Always index this workspace - Unlock full workspace understanding"
	got := DetectPaneState("auggie", pane)
	want := "permission:auggie.indexing_consent"
	if got != want {
		t.Errorf("DetectPaneState = %q, want %q", got, want)
	}
}

func TestDetectPaneState_Idle(t *testing.T) {
	if got := DetectPaneState("cursor", "agent> ready"); got != "" {
		t.Errorf("DetectPaneState = %q, want empty", got)
	}
}

func TestDetectPaneState_UnknownRuntime(t *testing.T) {
	if got := DetectPaneState("frobnicator", "anything"); got != "" {
		t.Errorf("DetectPaneState = %q, want empty", got)
	}
}

func TestDetectPaneState_EmptyInput(t *testing.T) {
	if got := DetectPaneState("cursor", ""); got != "" {
		t.Errorf("DetectPaneState(empty pane) = %q, want empty", got)
	}
	if got := DetectPaneState("", "any"); got != "" {
		t.Errorf("DetectPaneState(empty runtime) = %q, want empty", got)
	}
}

// thrum-k4wf: permission-prompt text that lives in the UPPER portion of
// the captured pane tail but has been superseded by post-approval output
// at the bottom must NOT be detected as an active prompt. Otherwise
// OnDetection on the next poll fires a fresh firstDetect (because the
// pane hash differs from the row's stored hash, and the current
// hash-change-as-new-prompt logic treats it as a new prompt) —
// producing the supervisor-spam loop observed in k4wf.
//
// The fix scopes detection to the bottom `paneBottomMatchLines` lines
// of the captured content. A match in the upper scrollback is treated
// as resolved and returns "" (idle), routing HandleCheckPane to the
// OnRecovery branch which clears the pending_nudge row.
func TestDetectPaneState_ClaudePromptInUpperScrollback_DoesNotMatch(t *testing.T) {
	// Prompt sits in the top ~5 lines, then 20 lines of post-approval
	// tool output push it out of the bottom-N match window.
	var builder []string
	builder = append(builder,
		"⏺ Bash(curl https://example.com)",
		"  ⎿  Do you want to proceed?",
		"     1. Yes",
		"     2. Yes, and don't ask again for Bash(curl)",
		"     3. No, and tell Claude what to do differently (Esc)",
	)
	for range 20 {
		builder = append(builder, "⏺ [post-approval output line]")
	}
	// Bottom of the pane is clean idle-looking output.
	builder = append(builder,
		"✻ Cogitating…",
		"Model: Opus 4.7 (1M context) | Ctx: 12k | Block: 1hr",
	)
	pane := joinLines(builder)
	if got := DetectPaneState("claude", pane); got != "" {
		t.Errorf("DetectPaneState(prompt in upper scrollback) = %q, want empty — prompt should be ignored when outside bottom-match window", got)
	}
}

// TestDetectPaneState_ClaudePromptAtBottom_Matches verifies the fix
// doesn't regress detection when the prompt genuinely IS at the bottom
// of the pane (the common active-prompt case).
func TestDetectPaneState_ClaudePromptAtBottom_Matches(t *testing.T) {
	// Small amount of scrollback above, then the prompt at the bottom.
	var builder []string
	for range 15 {
		builder = append(builder, "⏺ [earlier agent output]")
	}
	builder = append(builder,
		"⏺ Bash(curl https://example.com)",
		"  ⎿  Do you want to proceed?",
		"     1. Yes",
		"     2. Yes, and don't ask again for Bash(curl)",
		"     3. No, and tell Claude what to do differently (Esc)",
	)
	pane := joinLines(builder)
	got := DetectPaneState("claude", pane)
	want := "permission:claude.tool_confirmation"
	if got != want {
		t.Errorf("DetectPaneState(prompt at bottom) = %q, want %q", got, want)
	}
}

// TestDetectPaneState_ClaudePromptShortPane_StillMatches verifies
// that short pane captures (fewer than paneBottomMatchLines lines) are
// not truncated — they're matched in full.
func TestDetectPaneState_ClaudePromptShortPane_StillMatches(t *testing.T) {
	pane := "⏺ Bash(curl)\n  ⎿  Do you want to proceed?\n     1. Yes\n     2. Yes, and don't ask again for Bash(curl)\n     3. No, and tell Claude what to do differently (Esc)\n"
	got := DetectPaneState("claude", pane)
	want := "permission:claude.tool_confirmation"
	if got != want {
		t.Errorf("DetectPaneState(short pane) = %q, want %q", got, want)
	}
}

// joinLines is a tiny helper: newline-separator, no trailing newline
// (tmux capture-pane does not emit one).
func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}
