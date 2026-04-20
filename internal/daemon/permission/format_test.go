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
	// Pane content retained (indented)
	assertContains("  Run this command?")
	assertContains("Not in allowlist: curl https://example.com")
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
	// Regression for thrum-7khf acceptance: typical nudges stay ≤10 lines.
	row := &NudgeRow{
		Session:       "plugin-skills-slate",
		AgentName:     "impl_skills",
		PatternKey:    "claude.tool_confirmation",
		ApproveKey:    "1",
		DenyKey:       "3",
		FirstDetected: time.Date(2026, 4, 20, 4, 1, 35, 0, time.UTC),
		NudgeCount:    1,
	}
	paneTail := `Bash command
rm -rf /Users/leon/.workspaces/thrum/plugin-skills-slate/dev-docs/toy-philosophy
Clean toy artifacts
Permission rule Bash(rm -rf *) requires confirmation for this command.
/permissions to update rules
Do you want to proceed?
❯ 1. Yes
2. No
Esc to cancel · Tab to amend · ctrl+e to explain`
	body := FormatNudge(row, paneTail, "claude", "permission-prompts",
		time.Date(2026, 4, 20, 4, 1, 35, 0, time.UTC))
	lineCount := strings.Count(body, "\n")
	if lineCount > 10 {
		t.Errorf("nudge body exceeds 10-line budget: %d lines\n%s", lineCount, body)
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
	// 30 distinct lines — we expect only the first 5 in the body
	// (thrum-7khf: cap tightened from 15 → 5 AND head-biased to keep
	// the command + reason at the top of a prompt capture, dropping
	// selector/shortcut chrome from the bottom).
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
	// ROW00..ROW04 kept, ROW05..ROW29 dropped.
	for i := 0; i < 5; i++ {
		kept := fmt.Sprintf("ROW%02d", i)
		if !strings.Contains(body, kept) {
			t.Errorf("%s should be present:\n%s", kept, body)
		}
	}
	for i := 5; i < 30; i++ {
		dropped := fmt.Sprintf("ROW%02d", i)
		if strings.Contains(body, dropped) {
			t.Errorf("%s should have been truncated:\n%s", dropped, body)
		}
	}
}
