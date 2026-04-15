package permission

import (
	"regexp"
	"strings"
	"testing"
)

func TestPattern_Fields(t *testing.T) {
	p := Pattern{
		Name:       "test",
		Regex:      regexp.MustCompile(`test`),
		ApproveKey: "y",
		DenyKey:    "n",
		Comment:    "test pattern",
	}
	if p.Name != "test" {
		t.Errorf("Name = %q", p.Name)
	}
	if p.ApproveKey != "y" {
		t.Errorf("ApproveKey = %q", p.ApproveKey)
	}
	if p.DenyKey != "n" {
		t.Errorf("DenyKey = %q", p.DenyKey)
	}
	if p.Comment != "test pattern" {
		t.Errorf("Comment = %q", p.Comment)
	}
	if p.Regex == nil || !p.Regex.MatchString("test") {
		t.Error("Regex should match 'test'")
	}
}

func TestMatch_UnknownRuntime(t *testing.T) {
	if m := Match("frobnicator", "any content"); m != nil {
		t.Errorf("expected nil for unknown runtime, got %+v", m)
	}
}

// TestApproveKeyNeverForeverAllow runs on every CI build. Any new
// pattern whose approve_key matches a forever-allow indicator fails
// the test, preventing a supervisor's approval from accidentally
// granting a persistent allowlist entry.
//
// Forbidden entries cover:
//   - cursor: Tab / Shift+Tab (add to allowlist / auto-run everything)
//   - claude: "2" (Yes, and don't ask again) — both Variant A and B
//   - codex: "2" (Yes, don't ask again for matching prefix) — Task 2.3
//   - opencode: "Right,Enter" (navigate to Allow always) — Task 2.3
//   - kiro-cli: "Down,Enter" (Trust, always allow in this session) — 2.3b
//   - auggie indexing: "1", "Enter" (Always index / default-highlighted) — 2.3c
//   - generic: strings containing "all", "always", "forever"
func TestApproveKeyNeverForeverAllow(t *testing.T) {
	forbidden := []string{
		// cursor "Add to allowlist" / "Auto-run everything"
		"Tab", "BTab", "shift+tab", "Shift+Tab",
		// claude "Yes, and don't ask again" (shared across Variant A and B)
		// codex "Yes, and don't ask again for commands that start with..."
		"2",
		// opencode — navigate to "Allow always" then confirm
		"Right,Enter",
		// kiro-cli — navigate to "Trust, always allow in this session"
		"Down,Enter",
		// auggie indexing — default-highlighted option 1 is forever-allow,
		// and a bare Enter lands on it. "1" is forbidden outright; "Enter"
		// is forbidden for the auggie indexing pattern specifically.
		"1",
		// generic permissive hints
		"all", "always", "forever",
	}
	for runtime, pats := range patterns {
		for _, p := range pats {
			// "1" is a valid approve_key for claude and codex (numeric
			// choice prompts where 1 = Yes) — only reject it for the
			// auggie indexing pattern where 1 is the forever-allow trap.
			for _, bad := range forbidden {
				if bad == "1" && !(runtime == "auggie" && p.Name == "indexing_consent") {
					continue
				}
				if strings.EqualFold(p.ApproveKey, bad) {
					t.Errorf("%s.%s approve_key=%q is forbidden — maps to forever-allow",
						runtime, p.Name, p.ApproveKey)
				}
			}
			// "Enter" is only forbidden for auggie indexing (the default-
			// highlighted option is forever-allow); kiro-cli uses Enter
			// on the safe default, so this is a pattern-specific rule.
			if runtime == "auggie" && p.Name == "indexing_consent" &&
				strings.EqualFold(p.ApproveKey, "Enter") {
				t.Errorf("%s.%s approve_key=Enter is forbidden — bare Enter activates forever-allow on indexing prompt",
					runtime, p.Name)
			}
		}
	}
}

func TestMatch_Cursor_NotInAllowlist_Positive(t *testing.T) {
	pane := `Run this command?
Not in allowlist: curl https://example.com
 → Run (once) (y)
   Add Shell(curl) to allowlist? (tab)
   Skip (esc or n)`
	m := Match("cursor", pane)
	if m == nil {
		t.Fatal("expected match, got nil")
	}
	if m.Name != "not_in_allowlist" {
		t.Errorf("Name = %q, want %q", m.Name, "not_in_allowlist")
	}
	if m.ApproveKey != "y" {
		t.Errorf("ApproveKey = %q, want %q", m.ApproveKey, "y")
	}
	if m.DenyKey != "Escape" {
		t.Errorf("DenyKey = %q, want %q", m.DenyKey, "Escape")
	}
}

func TestMatch_Cursor_Negative(t *testing.T) {
	pane := `agent> ready
waiting for input`
	if m := Match("cursor", pane); m != nil {
		t.Errorf("expected no match for idle pane, got %+v", m)
	}
}

func TestMatch_Claude_VariantA_ToolConfirmation_Positive(t *testing.T) {
	pane := `⏺ Bash(curl https://example.com)
  ⎿  Do you want to proceed?
     1. Yes
     2. Yes, and don't ask again for Bash(curl)
     3. No, and tell Claude what to do differently (Esc)`
	m := Match("claude", pane)
	if m == nil {
		t.Fatal("expected match, got nil")
	}
	if m.Name != "tool_confirmation" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.ApproveKey != "1" {
		t.Errorf("ApproveKey = %q, want %q", m.ApproveKey, "1")
	}
	// Pattern-library deny_key is the 3-option case. Dispatch layer
	// overrides to Escape when the pane shows only 2 options (Variant B).
	if m.DenyKey != "3" {
		t.Errorf("DenyKey = %q, want %q", m.DenyKey, "3")
	}
}

func TestMatch_Claude_VariantB_ReadPrompt_Positive(t *testing.T) {
	// 2-option Read variant — same anchor, different option list.
	// The dispatch layer inspects paneTail at fire time to decide
	// whether to use deny_key='3' (Variant A) or 'Escape' (Variant B).
	// This test just verifies the regex still matches; the dispatch
	// logic has its own tests in the nudge package.
	pane := ` Read file

 Search(pattern: "## Task 0.2", path: "~/plans/...")


 Do you want to proceed?
 ❯ 1. Yes
   2. Yes, allow reading from plans/ during this session

 Esc to cancel · Tab to amend`
	m := Match("claude", pane)
	if m == nil {
		t.Fatal("expected match for 2-option Read variant, got nil")
	}
	if m.ApproveKey != "1" {
		t.Errorf("ApproveKey = %q, want %q", m.ApproveKey, "1")
	}
}
