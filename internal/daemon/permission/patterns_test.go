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
				if bad == "1" && (runtime != "auggie" || p.Name != "indexing_consent") {
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

// ----- Task 2.3: opencode + codex -----

func TestMatch_Opencode_PermissionRequired_Positive(t *testing.T) {
	// Raw capture from dev-docs/plans/2026-04-14-permission-prompt-samples.md
	// OpenCode 1.4.3, external directory access gate.
	pane := `△ Permission required
  ← Access external directory /opt/homebrew/lib/node_modules/opencode-ai

Patterns

- /opt/homebrew/lib/node_modules/opencode-ai/*

 Allow once   Allow always   Reject                        ctrl+f fullscreen  ⇆ select  enter confirm`
	m := Match("opencode", pane)
	if m == nil {
		t.Fatal("expected match for opencode permission prompt, got nil")
	}
	if m.Name != "permission_required" {
		t.Errorf("Name = %q", m.Name)
	}
	// Option A: default selection is "Allow once"; plain Enter approves.
	if m.ApproveKey != "Enter" {
		t.Errorf("ApproveKey = %q, want %q", m.ApproveKey, "Enter")
	}
}

func TestMatch_Opencode_Negative(t *testing.T) {
	if m := Match("opencode", "opencode> waiting for input"); m != nil {
		t.Errorf("expected nil on idle pane, got %+v", m)
	}
}

// TestMatch_Opencode_PermissionRequired_WithBorderChars verifies the
// regex tolerates OpenCode's box-drawing border characters at the
// start of each line. The original pattern anchored to `^\s*` which
// works for the unbordered samples.md capture but fails live because
// tmux capture-pane preserves the pane's UI chrome (the `┃` U+2503
// heavy vertical). Found during Epic E live verification.
func TestMatch_Opencode_PermissionRequired_WithBorderChars(t *testing.T) {
	// Exact shape of a live tmux capture-pane output when OpenCode
	// is parked on a /etc read-gate prompt. Each content line has a
	// leading `┃ ` (U+2503 + space).
	pane := "┃\n" +
		"┃  △ Permission required\n" +
		"┃    ← Access external directory /etc\n" +
		"┃\n" +
		"┃  Patterns\n" +
		"┃\n" +
		"┃  - /etc/*\n" +
		"┃\n" +
		"┃   Allow once   Allow always   Reject  ctrl+f fullscreen  ⇆ select  enter confirm\n" +
		"┃"
	m := Match("opencode", pane)
	if m == nil {
		t.Fatal("expected match on bordered opencode capture, got nil")
	}
	if m.Name != "permission_required" {
		t.Errorf("Name = %q, want permission_required", m.Name)
	}
	if m.ApproveKey != "Enter" {
		t.Errorf("ApproveKey = %q, want Enter", m.ApproveKey)
	}
}

func TestMatch_Codex_ToolConfirmation_Positive(t *testing.T) {
	// Raw capture from samples doc — OpenAI Codex CLI.
	pane := `• Starting the urgent curl experiment exactly as requested.

• Running curl https://example.com/thrum-test


  Would you like to run the following command?

  Reason: capture any permission prompt behavior

  $ curl https://example.com/thrum-test

› 1. Yes, proceed (y)
  2. Yes, and don't ask again for commands that start with ` + "`" + `curl https://example.com/thrum-test` + "`" + ` (p)
  3. No, and tell Codex what to do differently (esc)

  Press enter to confirm or esc to cancel`
	m := Match("codex", pane)
	if m == nil {
		t.Fatal("expected match for codex prompt, got nil")
	}
	if m.Name != "tool_confirmation" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.ApproveKey != "1" {
		t.Errorf("ApproveKey = %q, want %q", m.ApproveKey, "1")
	}
	if m.DenyKey != "3" {
		t.Errorf("DenyKey = %q, want %q", m.DenyKey, "3")
	}
}

// ----- Task 2.3b: kiro-cli -----

func TestMatch_KiroCli_ShellApproval_Positive(t *testing.T) {
	pane := `  Please run the following shell command: sudo ls /etc/sudoers.d

↓ Shell sudo ls /etc/sudoers.d

────────────────────────────────────────────────────────────────────────────────
 shell requires approval
 ❯ Yes, single permission
   Trust, always allow in this session
   No (Tab to edit)
────────────────────────────────────────────────────────────────────────────────
 ESC to close | Tab to edit`
	m := Match("kiro-cli", pane)
	if m == nil {
		t.Fatal("expected match for kiro-cli prompt, got nil")
	}
	if m.Name != "shell_approval" {
		t.Errorf("Name = %q", m.Name)
	}
	// kiro-cli: default selection is the safe "Yes, single permission",
	// so a bare Enter approves-once.
	if m.ApproveKey != "Enter" {
		t.Errorf("ApproveKey = %q, want %q", m.ApproveKey, "Enter")
	}
	if m.DenyKey != "Escape" {
		t.Errorf("DenyKey = %q, want %q", m.DenyKey, "Escape")
	}
}

// ----- Task 2.3c: auggie indexing consent -----

func TestMatch_Auggie_IndexingConsent_Positive(t *testing.T) {
	pane := ` Learn More:
 https://docs.augmentcode.com/setup-augment/workspace-indexing

   Workspace: ~/.workspaces/thrum/auggie-test

 Choose an option:

   → [1] Always index this workspace - Unlock full workspace understanding

     [2] Never index this workspace - Use basic assistance only

     [3] Index this workspace for this session

     [4] Skip indexing for this session


 [↑↓] Navigate • [Enter] Confirm

 Press 1/2/3/4 to select directly • Esc to skip`
	m := Match("auggie", pane)
	if m == nil {
		t.Fatal("expected match for auggie indexing prompt, got nil")
	}
	if m.Name != "indexing_consent" {
		t.Errorf("Name = %q", m.Name)
	}
	// CRITICAL: approve_key MUST be "3" (session-scoped), NEVER "1"
	// (forever-allow) or "Enter" (default-highlighted forever-allow).
	if m.ApproveKey != "3" {
		t.Errorf("ApproveKey = %q, want %q — auggie indexing default-highlighted is forever-allow trap", m.ApproveKey, "3")
	}
}

// ----- Task 2.3d: auggie tool approval -----

func TestMatch_Auggie_ToolApproval_SaveFile_Positive(t *testing.T) {
	pane := `● Save File - test-permissions.txt
╭─────────────────────────────────────────────────────────────────────╮
│                                                                     │
│  Tool Approval Required                                             │
│                                                                     │
│  Save File: test-permissions.txt                                    │
│                                                                     │
╰─────────────────────────────────────────────────────────────────────╯
 [A]llow [D]eny`
	m := Match("auggie", pane)
	if m == nil {
		t.Fatal("expected match for auggie tool approval, got nil")
	}
	if m.Name != "tool_approval" {
		t.Errorf("Name = %q, want %q", m.Name, "tool_approval")
	}
	if m.ApproveKey != "A" {
		t.Errorf("ApproveKey = %q, want %q", m.ApproveKey, "A")
	}
	if m.DenyKey != "D" {
		t.Errorf("DenyKey = %q, want %q", m.DenyKey, "D")
	}
}

func TestMatch_Auggie_ToolApproval_LaunchProcess_Positive(t *testing.T) {
	pane := `● Terminal - touch /tmp/auggie-shell-test && echo done
╭─────────────────────────────────────────────────────────────────────╮
│                                                                     │
│  Tool Approval Required                                             │
│                                                                     │
│  Terminal: touch /tmp/auggie-shell-test && echo done                │
│                                                                     │
╰─────────────────────────────────────────────────────────────────────╯
 [A]llow [D]eny`
	m := Match("auggie", pane)
	if m == nil {
		t.Fatal("expected match for auggie launch-process approval, got nil")
	}
	if m.Name != "tool_approval" {
		t.Errorf("Name = %q", m.Name)
	}
}

func TestMatch_Auggie_IndexingPrecedesToolApproval(t *testing.T) {
	// Pattern order matters: indexing_consent must come first for it
	// to match when both could theoretically appear. The indexing anchor
	// is stricter (includes "Always index this workspace") so cannot
	// false-match a tool approval pane.
	toolPane := `● Save File - x.txt
╭─╮
│ Tool Approval Required │
╰─╯
 [A]llow [D]eny`
	m := Match("auggie", toolPane)
	if m == nil {
		t.Fatal("expected match for tool approval, got nil")
	}
	if m.Name != "tool_approval" {
		t.Errorf("matched %q, want tool_approval", m.Name)
	}
}

// ----- Anchoring false-positive regression tests -----
//
// Post-review hardening (2026-04-15): the three regexes that previously
// matched anywhere in a line ("Do you want to proceed?", "Always index
// this workspace", "Tool Approval Required") now require start-of-line
// anchoring + for auggie tool_approval, the box chrome "│". These
// tests encode the intent so a future regex relaxation fails CI.

func TestMatch_Claude_NoFalsePositiveFromStdout(t *testing.T) {
	// A shell command's stdout (captured by something like `echo`) that
	// happens to include the phrase mid-line must NOT match.
	pane := `$ echo "Next step - Do you want to proceed? Then run make test"
Next step - Do you want to proceed? Then run make test
$ `
	if m := Match("claude", pane); m != nil {
		t.Errorf("claude regex matched stdout echo: %+v", m)
	}
}

// TestMatch_Claude_NoFalsePositiveFromConversationalProse regresses
// thrum-48kt.7. At 2026-04-20 04:42 UTC the coordinator's own Claude
// pane wrote a plan summary ending with the conversational phrase
// "Do you want to proceed?"; the pre-fix tool_confirmation regex only
// required start-of-line anchoring and matched the prose as a real UI
// prompt — the detector created a permission_nudge and routed it to
// the configured supervisor, interrupting the session. The tightened
// pattern requires a structural marker unique to the actual dialog
// (❯ selector line, the "Esc to cancel · Tab to amend" footer, or
// Variant A's distinctive option 3 text). Plain prose has none of
// these and must not match.
func TestMatch_Claude_NoFalsePositiveFromConversationalProse(t *testing.T) {
	// Representative of the 04:42 UTC capture: a multi-section plan
	// summary whose final line is the interrogative. No numbered
	// options, no ❯ selector, no footer line.
	pane := `Here's the full plan for the release cut, ready for your review.

## Phase 1 — merge outstanding fixes
Land thrum-rchj + thrum-92mj onto thrum-dev, run the full suite,
verify no sync regressions in the peer bridge.

## Phase 2 — cut the tag
Tag v0.9.0-rc2, push, watch CI for the release workflow.

## Phase 3 — smoke test
Run the E2E harness against the new tag, verify on both macminis.

Do you want to proceed?`
	if m := Match("claude", pane); m != nil {
		t.Errorf("claude regex matched conversational prose (thrum-48kt.7 regression): %+v", m)
	}
}

// TestMatch_Claude_NoFalsePositiveFromProseWithOneOption guards the
// bound on the "multiple markers required" check. A plan summary
// whose list items precede the interrogative (organic prose shape)
// must not match: the tightened regex requires a structural marker
// WITHIN 500 chars AFTER the question, so incidental numbering above
// the interrogative is correctly ignored. This documents that the
// ordering matters — the `.{0,500}` window only looks forward.
func TestMatch_Claude_NoFalsePositiveFromProseWithOneOption(t *testing.T) {
	pane := `Recap of what changed this session:

1. We merged the three P2 fixes into thrum-dev.
2. The tests all passed.

Do you want to proceed?`
	if m := Match("claude", pane); m != nil {
		t.Errorf("claude regex matched prose with incidental numbered list: %+v", m)
	}
}

func TestMatch_Auggie_ToolApproval_NoFalsePositiveFromBarePhrase(t *testing.T) {
	// A log line or status bar that merely contains "Tool Approval
	// Required" (no box chrome) must NOT match. The old unanchored
	// regex would have fired here.
	pane := `[2026-04-15 03:10] INFO  Tool Approval Required for save-file, deferring
[2026-04-15 03:11] INFO  pending user action
auggie>`
	if m := Match("auggie", pane); m != nil {
		t.Errorf("auggie tool_approval matched bare phrase: %+v", m)
	}
}

func TestMatch_Auggie_Indexing_NoFalsePositiveFromMidline(t *testing.T) {
	// A shell command whose stdout contains the phrase mid-line must
	// NOT match. Only a chrome-prefixed line (menu option) should match.
	pane := `$ grep -r "Always index this workspace" docs/
docs/faq.md: When prompted, choose "Always index this workspace" for persistent indexing.
$ `
	if m := Match("auggie", pane); m != nil {
		t.Errorf("auggie indexing_consent matched doc grep output: %+v", m)
	}
}
