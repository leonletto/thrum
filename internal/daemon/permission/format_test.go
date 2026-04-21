package permission

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFormatNudge_FirstDetect(t *testing.T) {
	row := &NudgeRow{
		Session:       "cursor-test",
		AgentName:     "researcher_cursor",
		PatternKey:    "cursor.not_in_allowlist",
		ApproveKey:    "y",
		DenyKey:       "Escape",
		FirstDetected: time.Date(2026, 4, 14, 10, 33, 12, 0, time.UTC),
		NudgeCount:    1,
	}
	paneTail := "Run this command?\nNot in allowlist: curl https://example.com\n → Run (once) (y)"

	body := FormatNudge(row, paneTail, "cursor", "thrum", time.Date(2026, 4, 14, 10, 35, 12, 0, time.UTC))

	assertContains := func(substr string) {
		t.Helper()
		if !strings.Contains(body, substr) {
			t.Errorf("body missing %q:\n%s", substr, body)
		}
	}
	// Header: agent · session (runtime)
	assertContains("⚠ @researcher_cursor · cursor-test (cursor)")
	// Pane content retained (indented by 2 spaces)
	assertContains("  Run this command?")
	assertContains("  Not in allowlist: curl https://example.com")
	// One-line reply hint with both keys
	assertContains(`Reply: y (approve) · n (deny) · or thrum tmux send cursor-test "y"|"Escape"`)
	// Footer collapses reminder / repo / pattern / first-seen
	assertContains("(reminder 1/6 · thrum · cursor.not_in_allowlist · 2m ago)")
}

func TestFormatNudge_Reminder(t *testing.T) {
	row := &NudgeRow{
		Session:       "cursor-test",
		AgentName:     "researcher_cursor",
		PatternKey:    "cursor.not_in_allowlist",
		ApproveKey:    "y",
		DenyKey:       "Escape",
		FirstDetected: time.Date(2026, 4, 14, 10, 33, 12, 0, time.UTC),
		NudgeCount:    3,
	}
	body := FormatNudge(row, "pane", "cursor", "thrum", time.Date(2026, 4, 14, 10, 48, 12, 0, time.UTC))
	if !strings.Contains(body, "reminder 3/6") {
		t.Errorf("body should show 'reminder 3/6':\n%s", body)
	}
}

func TestFormatNudge_NoDenyKey(t *testing.T) {
	row := &NudgeRow{
		Session:    "opencode-test",
		AgentName:  "researcher_opencode",
		PatternKey: "opencode.some_prompt",
		ApproveKey: "y",
		DenyKey:    "", // no explicit deny
		NudgeCount: 1,
	}
	body := FormatNudge(row, "pane", "opencode", "thrum", time.Now())
	if !strings.Contains(body, "Ctrl+C in pane to interrupt") {
		t.Errorf("body should show Ctrl+C fallback when DenyKey is empty:\n%s", body)
	}
	// Ensure we did NOT render a tmux send command for an empty deny key.
	if strings.Contains(body, `thrum tmux send opencode-test ""`) {
		t.Errorf("body must not render an empty deny keystroke:\n%s", body)
	}
}

func TestFormatNudge_LineBudget(t *testing.T) {
	// Regression for thrum-7khf acceptance: typical nudges stay ≤10
	// lines. Fixture switched in thrum-uy1n to a real claude UI shape
	// (5-line prompt body) — the prior 9-line synthetic fixture
	// inflated the count beyond what the actual claude UI produces.
	row := &NudgeRow{
		Session:       "plugin-skills-slate",
		AgentName:     "impl_skills",
		PatternKey:    "claude.tool_confirmation",
		ApproveKey:    "1",
		DenyKey:       "3",
		FirstDetected: time.Date(2026, 4, 20, 4, 1, 35, 0, time.UTC),
		NudgeCount:    1,
	}
	paneTail := `⏺ Bash(rm -rf /Users/leon/.workspaces/thrum/plugin-skills-slate/dev-docs/toy-philosophy)
  ⎿  Do you want to proceed?
     1. Yes
     2. Yes, and don't ask again for Bash(rm -rf:*)
     3. No, and tell Claude what to do differently (Esc)`
	body := FormatNudge(row, paneTail, "claude", "permission-prompts",
		time.Date(2026, 4, 20, 4, 1, 35, 0, time.UTC))
	lineCount := strings.Count(body, "\n")
	if lineCount > 10 {
		t.Errorf("nudge body exceeds 10-line budget: %d lines\n%s", lineCount, body)
	}
	// Acceptance: the prompt body must be in the snippet so the
	// recipient sees what is being approved.
	if !strings.Contains(body, "rm -rf") {
		t.Errorf("nudge body missing the approved command:\n%s", body)
	}
}

func TestFormatNudge_PaneTailTruncated(t *testing.T) {
	hugeTail := strings.Repeat("x", 10_000)
	row := &NudgeRow{
		Session:    "x",
		AgentName:  "x",
		PatternKey: "x.x",
		ApproveKey: "y",
		NudgeCount: 1,
	}
	body := FormatNudge(row, hugeTail, "cursor", "thrum", time.Now())
	if len(body) > 4_000 {
		t.Errorf("nudge body exceeds 4KB Telegram cap: %d bytes", len(body))
	}
}

func TestFormatNudge_PaneTailTruncatedMidRune(t *testing.T) {
	// Regression for M1 (Epic B review): a single >2KB line containing
	// multi-byte runes must not emit invalid UTF-8 when the byte cap
	// lands mid-rune. The rescue walks past any UTF-8 continuation
	// bytes before returning.
	//
	// "→" is 3 bytes (0xE2 0x86 0x92). We build a ~3KB line of arrows
	// so no newline exists in the captured segment (newline rescue
	// cannot fire), then assert the emitted body is still valid UTF-8.
	hugeArrowLine := strings.Repeat("→", 1_000) // ~3KB, no newlines
	row := &NudgeRow{
		Session:    "x",
		AgentName:  "x",
		PatternKey: "x.x",
		ApproveKey: "y",
		NudgeCount: 1,
	}
	body := FormatNudge(row, hugeArrowLine, "cursor", "thrum", time.Now())
	if !utf8.ValidString(body) {
		t.Error("nudge body contains invalid UTF-8 after mid-rune truncation")
	}
	// The rendered body also must contain at least one arrow — i.e.
	// we actually kept some pane content after the walk-past.
	if !strings.Contains(body, "→") {
		t.Error("expected at least one arrow in truncated body")
	}
}

func TestFormatNudge_PaneTailLineCap(t *testing.T) {
	// 30 distinct lines — we expect only the LAST maxPaneTailLines (6)
	// in the body.
	//
	// thrum-uy1n reverts the 7khf head-bias: the actual claude UI puts
	// the prompt body (Bash command + dialog + selector) at the BOTTOM
	// of the pane scrollback, with banner / status / command history
	// above. A 30-line tmux capture-pane therefore needs tail-bias so
	// the snippet contains the prompt the supervisor must approve, not
	// the unrelated history chrome.
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("ROW%02d", i))
	}
	tail := strings.Join(lines, "\n")
	row := &NudgeRow{
		Session:    "s",
		AgentName:  "a",
		PatternKey: "x.x",
		ApproveKey: "y",
		NudgeCount: 1,
	}
	body := FormatNudge(row, tail, "cursor", "thrum", time.Now())
	// Last maxPaneTailLines kept; everything earlier dropped.
	keepFrom := 30 - maxPaneTailLines
	for i := keepFrom; i < 30; i++ {
		kept := fmt.Sprintf("ROW%02d", i)
		if !strings.Contains(body, kept) {
			t.Errorf("%s should be present:\n%s", kept, body)
		}
	}
	for i := 0; i < keepFrom; i++ {
		dropped := fmt.Sprintf("ROW%02d", i)
		if strings.Contains(body, dropped) {
			t.Errorf("%s should have been truncated:\n%s", dropped, body)
		}
	}
}

// TestFormatNudge_RealClaudePrompt_TailBias — thrum-uy1n acceptance.
// Real-world tmux capture-pane returns ~30 lines: banner + history
// chrome at the top, the actual permission prompt at the bottom. The
// snippet MUST contain the prompt body (the command being approved)
// so a Telegram recipient can decide y/n without `tmux capture-pane`
// access. Pre-fix (head-bias) the snippet showed only the banner,
// burying the prompt and forcing the recipient to approve blindly.
func TestFormatNudge_RealClaudePrompt_TailBias(t *testing.T) {
	// Mimics what the daemon sees: 30-line scrollback with banner,
	// history, and the dialog at the bottom. Indentation matches a
	// real claude tool-confirmation capture.
	pane := `▝▜█████▛▘ Opus 4.7 (1M context) with high effort · Claude Max
▘▘ ▝▝   ~/.workspaces/thrum/perm-test
Tip: run /help for a full command list
❯ /thrum:prime
⎿ Loading prime context...
⎿ Done.

[user typed: thrum inbox --unread]

⏺ Bash(thrum inbox --unread)
  ⎿  Reading from inbox...

(spacer)
(spacer)
(spacer)
(spacer)
(spacer)
(spacer)
(spacer)
(spacer)
(spacer)
(spacer)
(spacer)
(spacer)
(spacer)
(spacer)
⏺ Bash(rm -rf /tmp/foo)
  ⎿  Do you want to proceed?
     1. Yes
     2. Yes, and don't ask again for Bash(rm -rf:*)
     3. No, and tell Claude what to do differently (Esc)`

	row := &NudgeRow{
		Session:       "perm-test",
		AgentName:     "impl_perm_test",
		PatternKey:    "claude.tool_confirmation",
		ApproveKey:    "1",
		DenyKey:       "3",
		FirstDetected: time.Date(2026, 4, 20, 14, 30, 0, 0, time.UTC),
		NudgeCount:    1,
	}
	body := FormatNudge(row, pane, "claude", "thrum",
		time.Date(2026, 4, 20, 14, 30, 5, 0, time.UTC))

	// Prompt body must be in the snippet — the supervisor cannot
	// decide y/n otherwise. These are the lines tail-5 should keep
	// from the dialog at the bottom of the capture.
	mustContain := []string{
		"Do you want to proceed?",
		"1. Yes",
		"3. No, and tell Claude what to do differently",
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("nudge body missing prompt-body line %q:\n%s", want, body)
		}
	}

	// Banner / history chrome from the top must be DROPPED, otherwise
	// recipients see the same garbage that motivated the bug report.
	mustNotContain := []string{
		"Opus 4.7",         // banner
		"~/.workspaces",    // status line
		"/thrum:prime",     // earlier command
		"Loading prime",    // earlier output
	}
	for _, dont := range mustNotContain {
		if strings.Contains(body, dont) {
			t.Errorf("nudge body must drop pre-prompt chrome %q:\n%s", dont, body)
		}
	}
}
