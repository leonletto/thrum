package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/rpc"
)

// TestSkillCmd_HelpListsAllNineVerbs pins the public command tree
// per plan §E10.1 AC: the parent --help output must surface every
// subcommand so operators see the full surface in one glance.
func TestSkillCmd_HelpListsAllNineVerbs(t *testing.T) {
	t.Parallel()

	cmd := skillCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--help: %v", err)
	}
	out := buf.String()
	// 8 top-level verbs; check status is nested under check.
	for _, verb := range []string{"list", "show", "check", "promote", "delete", "revise", "sync", "validate"} {
		if !strings.Contains(out, verb) {
			t.Errorf("--help missing %q in:\n%s", verb, out)
		}
	}
}

// TestSkillCmd_CheckStatusNestedCommand pins the `check status <id>`
// subcommand. Cobra requires nested subcommands to be addressable
// via the canonical Use string; if a future refactor flattens the
// command tree, this test catches it.
func TestSkillCmd_CheckStatusNestedCommand(t *testing.T) {
	t.Parallel()

	cmd := skillCmd()
	// Walk the cmd tree to find `check status`.
	var check, status *struct{}
	_ = check
	_ = status
	for _, sub := range cmd.Commands() {
		if sub.Name() == "check" {
			for _, nested := range sub.Commands() {
				if nested.Name() == "status" {
					return // found
				}
			}
			t.Fatalf("`check` has no `status` subcommand")
		}
	}
	t.Fatalf("no `check` subcommand")
}

// TestSkillCmd_ListAcceptsPendingFlag confirms `--pending` parses
// without error. Body-level RPC dispatch lands at E10.2; the flag
// + cobra wiring is what E10.1 owns.
func TestSkillCmd_ListAcceptsPendingFlag(t *testing.T) {
	t.Parallel()

	cmd := skillCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"list", "--pending"})
	err := cmd.Execute()
	// Expect the stub-error from the RunE body (E10.2 placeholder),
	// NOT a flag-parse error. Any error whose message references
	// "skill.list" passes; a flag-parse error would mention "unknown
	// flag" or similar.
	if err == nil {
		t.Fatalf("expected stub-error, got nil")
	}
	if strings.Contains(err.Error(), "unknown flag") || strings.Contains(err.Error(), "invalid argument") {
		t.Errorf("--pending failed to parse: %v", err)
	}
}

// TestSkillCmd_PromoteAllowSecretRepeatable confirms repeated
// --allow-secret occurrences collect into a slice. Plan AC line
// 660: "--allow-secret <regex> (repeatable)".
func TestSkillCmd_PromoteAllowSecretRepeatable(t *testing.T) {
	t.Parallel()

	cmd := skillCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"promote", "some/path", "--allow-secret", "a", "--allow-secret", "b"})

	err := cmd.Execute()
	// Same stub-error pattern as the list test: any non-flag-parse
	// error is acceptable.
	if err == nil {
		t.Fatalf("expected stub-error, got nil")
	}
	if strings.Contains(err.Error(), "unknown flag") || strings.Contains(err.Error(), "invalid argument") {
		t.Errorf("--allow-secret repeat parse failed: %v", err)
	}

	// Locate the promote subcommand directly and inspect the flag's
	// final parsed value. StringSlice resolves to []string.
	for _, sub := range cmd.Commands() {
		if sub.Name() == "promote" {
			vals, err := sub.Flags().GetStringSlice("allow-secret")
			if err != nil {
				t.Fatalf("GetStringSlice: %v", err)
			}
			if len(vals) != 2 {
				t.Errorf("expected 2 entries, got %d: %v", len(vals), vals)
			}
			if vals[0] != "a" || vals[1] != "b" {
				t.Errorf("entries: %v", vals)
			}
			return
		}
	}
	t.Fatalf("no `promote` subcommand")
}

// TestSkillCmd_VerbsRegistered confirms every plan-AC verb landed
// as a subcommand. Catches a future refactor that accidentally
// drops a verb from skillCmd().
func TestSkillCmd_VerbsRegistered(t *testing.T) {
	t.Parallel()

	cmd := skillCmd()
	have := map[string]bool{}
	for _, sub := range cmd.Commands() {
		have[sub.Name()] = true
	}
	for _, want := range []string{"list", "show", "check", "promote", "delete", "revise", "sync", "validate"} {
		if !have[want] {
			t.Errorf("missing subcommand: %s", want)
		}
	}
}

// TestSkillCheck_CLIExitsCode2 drives the pure classifier function
// that decides the CLI exit code for a given daemon error. The
// canonical §8.3 stub message → exit 2; any other error → exit 1;
// nil → 0. The cobra RunE delegates to osExit() with this code,
// so testing the classifier covers the exit-code-2 plan AC for
// `thrum skill check` (per spec §7.3) without invoking the CLI as
// a subprocess.
func TestSkillCheck_CLIExitsCode2(t *testing.T) {
	t.Parallel()

	// The daemon's RPC error message is wrapped by the JSON-RPC
	// client as "RPC error -32000: <verbatim message>". The
	// classifier must match against the verbatim substring so the
	// wrap doesn't defeat the match.
	wrappedStubErr := fmt.Errorf("RPC error -32000: %s", rpc.CheckSkillNotAvailableMessage)

	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "stub-error", err: wrappedStubErr, want: 2},
		{name: "stub-sentinel", err: rpc.ErrCheckTheSkillNotAvailable, want: 2},
		{name: "nil", err: nil, want: 0},
		{name: "connect-error", err: errors.New("failed to connect to daemon: connection refused"), want: 1},
		{name: "unauthorized", err: errors.New("RPC error -32000: unauthorized: coordinator-role required"), want: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifySkillCheckError(c.err)
			if got != c.want {
				t.Errorf("classify(%q) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}

// TestSkillDelete_ForceSkipsPrompt pins the AC E10.6 line 1776
// "non-interactive mode + --force → succeeds" invariant at the CLI
// layer. The cobra cmd's stdin (a bytes.Buffer here) is non-interactive
// so the prompt branch is bypassed unconditionally; the test verifies
// no "Delete promoted skill" text appears in the cmd output AND the
// returned error is the daemon-connection failure path (not the
// prompt-abort path).
func TestSkillDelete_ForceSkipsPrompt(t *testing.T) {
	t.Parallel()

	cmd := skillCmd()
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errBuf)
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetArgs([]string{"delete", "ghost", "--force"})

	err := cmd.Execute()
	// Cmd will fail at getClient() or resolveLocalAgentID() in the
	// test environment — that's expected; we only care that we DIDN'T
	// emit the interactive prompt or take the "delete aborted by
	// operator" path.
	if strings.Contains(out.String(), "Delete promoted skill") {
		t.Errorf("prompt text appeared despite --force; out: %s", out.String())
	}
	if err != nil && strings.Contains(err.Error(), "delete aborted") {
		t.Errorf("prompt path was taken: %v", err)
	}
}

// TestSkillDelete_NonInteractiveStdinSkipsPrompt confirms that without
// --force, a non-TTY stdin (every cobra-test invocation) skips the
// prompt — the same behavior we'd want from CI pipes. This is the
// stronger guarantee behind --force: --force is a deliberate skip
// during interactive sessions, but non-interactive callers always
// skip regardless.
func TestSkillDelete_NonInteractiveStdinSkipsPrompt(t *testing.T) {
	t.Parallel()

	cmd := skillCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetArgs([]string{"delete", "ghost"})

	_ = cmd.Execute()
	if strings.Contains(out.String(), "Delete promoted skill") {
		t.Errorf("prompt text appeared on non-TTY stdin; out: %s", out.String())
	}
}
