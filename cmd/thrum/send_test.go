package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestSendCmd_MissingRecipient_ExitsWithError pins thrum-t698: invoking
// `thrum send 'msg'` without either `--to` or `--broadcast` must hard-error
// (exit 1) with a conversational stderr message offering the two valid
// paths. Previously this default silently broadcast to every team agent — a
// 94-recipient footgun coord live-demonstrated during Session 75. The fix
// aligns the CLI with the long-established CLAUDE.md convention of
// "always use --to" by making the implicit broadcast a hard error.
//
// Test asserts:
//   - cmd.Execute() returns a non-nil error (cobra exits 1)
//   - The error text contains the canonical conversational framing:
//     "missing recipient", "Did you intend to", and BOTH path options
//     (--to @agent_name + --broadcast).
//   - The error does NOT mention "@group_name" (groups are not user-facing
//     in v0.10.x; coord's correction in msg_01KS0RC0N6RZ removed the group
//     path from the original three-option draft).
//   - stdout is empty — no "✓ Message sent" line could ever appear,
//     pinning the no-I/O property: the validation MUST fire before any
//     daemon RPC, otherwise this test on a developer machine with a live
//     daemon would re-trigger the very broadcast footgun being fixed.
//     (The original implementer hit this footgun once during TDD red —
//     see thrum-t698 commit body for the learned-pattern note.)
func TestSendCmd_MissingRecipient_ExitsWithError(t *testing.T) {
	cmd := sendCmd()
	// rootCmd sets SilenceUsage + SilenceErrors at the top level (main.go),
	// and cobra inherits these from the parent at execution time. The
	// standalone-subcommand test wiring here has no rootCmd, so without
	// these the stdout-empty assertion below would catch cobra's default
	// usage dump and false-positive a regression. Mirror the production
	// wiring so the test is faithful.
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"hello team"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected hard-error when --to and --broadcast both omitted; got nil. stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("expected empty stdout (validation must short-circuit before any daemon RPC); got %q — this likely means the missing-recipient guard regressed and a live broadcast just fired", stdout.String())
	}
	msg := err.Error()
	for _, needle := range []string{
		"missing recipient",
		"Did you intend to",
		"--to @agent_name",
		"--broadcast",
	} {
		if !strings.Contains(msg, needle) {
			t.Errorf("error message missing canonical fragment %q: full message: %q", needle, msg)
		}
	}
	// Groups are not user-facing in v0.10.x — the error must NOT direct
	// users to a non-existent --to @group_name option.
	if strings.Contains(msg, "@group_name") {
		t.Errorf("error message should not mention @group_name (groups are not user-facing in v0.10.x): %q", msg)
	}
}

// TestSendCmd_ToAndBroadcastMutuallyExclusive pins thrum-t698's mutex guard:
// specifying both `--to` and `--broadcast` is ambiguous (does the user want
// a directed send or a fanout?) and must reject with a clear error rather
// than silently picking one. cobra's MarkFlagsMutuallyExclusive provides
// the error wiring; this test verifies the flags are correctly registered
// as mutex.
func TestSendCmd_ToAndBroadcastMutuallyExclusive(t *testing.T) {
	cmd := sendCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--to", "@coordinator_main", "--broadcast", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when both --to and --broadcast specified; got nil. stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	msg := err.Error()
	// cobra emits "if any flags in the group are set none of the others can be: [to broadcast]" or similar.
	// Pin "broadcast" exactly + the bracketed group notation; loose "to" substring
	// would match "intend to" / "intended" / many other tokens so it's not a tight check.
	if !strings.Contains(msg, "broadcast") {
		t.Errorf("mutex error should name the --broadcast flag; got: %q", msg)
	}
	if !strings.Contains(msg, "[to broadcast]") && !strings.Contains(msg, `"to"`) {
		t.Errorf("mutex error should reference --to in cobra group notation ([to broadcast]) or quoted form; got: %q", msg)
	}
}
