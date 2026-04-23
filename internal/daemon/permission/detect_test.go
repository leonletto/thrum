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
	// 20 post-approval lines > paneBottomMatchLines (15). The minimum
	// for this test to exercise the upper-scrollback case is `N - (prompt lines)
	// + 1`, currently 15 - 5 + 1 = 11. Oversized on purpose so the
	// assertion still holds if the prompt regex grows a line or if
	// paneBottomMatchLines is tuned slightly upward.
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

// TestBottomLines_TrailingNewlinePreservesWindow covers the IMPORTANT
// review finding from the k4wf first-pass: a trailing "\n" on the
// input used to consume one slot of the N-line window via the phantom
// empty element produced by strings.Split. The helper now strips the
// trailing newline before counting and re-appends it after slicing,
// so the effective content window stays at exactly N lines.
func TestBottomLines_TrailingNewlinePreservesWindow(t *testing.T) {
	// 20 distinct lines, each of the form "line-NN".
	lines := make([]string, 0, 20)
	for i := 1; i <= 20; i++ {
		lines = append(lines, "line-"+itoa(i))
	}
	// Trailing newline on the input (matches what some tmux / capture
	// wrappers emit).
	content := strings.Join(lines, "\n") + "\n"

	got := bottomLines(content, 15)
	gotLines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(gotLines) != 15 {
		t.Fatalf("bottomLines returned %d content lines, want 15", len(gotLines))
	}
	// Expect last 15 lines of original (line-6 .. line-20).
	if gotLines[0] != "line-6" || gotLines[14] != "line-20" {
		t.Errorf("bottomLines returned wrong slice: first=%q last=%q, want first=%q last=%q",
			gotLines[0], gotLines[14], "line-6", "line-20")
	}
	if !strings.HasSuffix(got, "\n") {
		t.Error("bottomLines dropped the trailing newline from input that had one")
	}
}

// TestBottomLines_CRLFNormalization verifies the defense against
// CRLF-terminated pane captures (rare but possible via remote ssh
// transports). Without the normalization, "\r" would become part of
// line content and inflate the effective line count to 2x.
func TestBottomLines_CRLFNormalization(t *testing.T) {
	// 3 lines joined with \r\n. Without CRLF normalization, bottomLines
	// would treat this as one big line containing embedded "\r\n"
	// (since strings.Split on "\n" leaves "\r" trailing on each line,
	// not relevant for count) — but more importantly, downstream
	// multi-line regex anchors work on LF-separated content.
	content := "a\r\nb\r\nc"
	got := bottomLines(content, 15)
	if strings.Contains(got, "\r") {
		t.Errorf("bottomLines did not strip \\r: %q", got)
	}
}

// itoa is a tiny helper to keep the test free of a fmt import for one
// int conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
