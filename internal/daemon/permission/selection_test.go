package permission

import (
	"strings"
	"testing"
)

// AskUserQuestion-style dialog as Claude Code renders it: a question header
// with numbered options and the "❯" selection cursor on the active option.
const askUserQuestionPane = `
 Which library should we use for date formatting?

 ❯ 1. date-fns (Recommended)
   2. dayjs
   3. luxon

 Use arrow keys to select, Enter to confirm.
`

// A multi-question AskUserQuestion: the danger case the dispatch names — a
// stray Enter auto-answers the FIRST question.
const askUserQuestionMultiPane = `
 Question 1 of 3 — Auth method
 ❯ 1. OAuth
   2. API key
`

func TestIsSelectionPrompt_DetectsAskUserQuestion(t *testing.T) {
	if !IsSelectionPrompt(askUserQuestionPane) {
		t.Error("IsSelectionPrompt = false on an AskUserQuestion dialog; want true")
	}
	if !IsSelectionPrompt(askUserQuestionMultiPane) {
		t.Error("IsSelectionPrompt = false on a multi-question AskUserQuestion; want true")
	}
}

func TestIsSelectionPrompt_DetectsArrowWithParenOption(t *testing.T) {
	// Some menus render "❯ 1)" rather than "❯ 1.".
	if !IsSelectionPrompt("\n  ❯ 1) Yes\n    2) No\n") {
		t.Error("IsSelectionPrompt = false on '❯ 1)' menu; want true")
	}
}

func TestIsSelectionPrompt_DetectsBorderedMenu(t *testing.T) {
	// OpenCode-style left-border box chrome before the cursor.
	pane := "┃ ❯ 1. Allow once\n┃   2. Reject\n"
	if !IsSelectionPrompt(pane) {
		t.Error("IsSelectionPrompt = false on a bordered numbered menu; want true")
	}
}

func TestIsSelectionPrompt_EmptyIsFalse(t *testing.T) {
	if IsSelectionPrompt("") {
		t.Error("IsSelectionPrompt(\"\") = true; want false")
	}
}

func TestIsSelectionPrompt_ShellPromptIsNotASelection(t *testing.T) {
	// A pure/starship shell prompt uses "❯ " but has no numbered option
	// following the cursor — must NOT be mistaken for a menu (else nudges
	// to an agent sitting at a shell would defer forever).
	for _, pane := range []string{
		"some output\n❯ ",         // empty prompt
		"some output\n❯ ls -la\n", // prompt with a typed command
		"❯ git status\n",          // command, no number
	} {
		if IsSelectionPrompt(pane) {
			t.Errorf("IsSelectionPrompt(%q) = true; want false (shell prompt, not a menu)", pane)
		}
	}
}

func TestIsSelectionPrompt_PlainProseIsNotASelection(t *testing.T) {
	// Agent output that merely mentions numbered lists in prose must not match
	// — only the "❯" cursor on a numbered option counts.
	pane := "Here are the options:\n1. First\n2. Second\nWhich do you prefer?\n"
	if IsSelectionPrompt(pane) {
		t.Error("IsSelectionPrompt matched plain numbered prose; want false")
	}
}

func TestIsSelectionPrompt_ScrolledUpMenuIgnored(t *testing.T) {
	// A resolved menu that has scrolled above the active bottom region must
	// not keep matching (mirrors the bottomLines scoping the other detectors
	// use). Pad with >paneBottomMatchLines lines of post-answer output.
	pane := "❯ 1. old option\n   2. other\n" +
		strings.Repeat("post-answer output line\n", paneBottomMatchLines+3)
	if IsSelectionPrompt(pane) {
		t.Error("IsSelectionPrompt matched a scrolled-up menu; want false (bottomLines scoping)")
	}
}

func TestIsPaneSafeToType_SelectionPromptBlocks(t *testing.T) {
	if IsPaneSafeToType("claude", askUserQuestionPane) {
		t.Error("IsPaneSafeToType = true on an AskUserQuestion dialog; want false")
	}
	if IsPaneSafeToType("", askUserQuestionMultiPane) {
		t.Error("IsPaneSafeToType (empty runtime) = true on a selection dialog; want false")
	}
}

func TestIsPaneSafeToType_NormalOutputStaysSafe(t *testing.T) {
	// Regression guard: ordinary agent output (no prompt/gate/menu) must
	// remain safe-to-type so normal nudges still fire.
	pane := "Running tests...\nPASS\nok  github.com/x/y  0.4s\n"
	if !IsPaneSafeToType("claude", pane) {
		t.Error("IsPaneSafeToType = false on normal output; want true (would block all nudges)")
	}
}
