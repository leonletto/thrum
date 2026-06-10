package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newBodyInputTestCmd builds a bare cobra command with the shell-safe body
// input flags registered, so resolveMessageBody can be exercised in isolation
// (no daemon, no identity). Mirrors how addBodyInputFlags wires send/reply/edit.
func newBodyInputTestCmd(stdin string) *cobra.Command {
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	addBodyInputFlags(cmd)
	cmd.SetIn(strings.NewReader(stdin))
	return cmd
}

// TestResolveMessageBody_PositionalVerbatim pins thrum-d3fp: a positional
// MESSAGE is returned byte-for-byte, with no trailing-newline trimming (only
// stdin/file bodies get the heredoc-artifact trim).
func TestResolveMessageBody_PositionalVerbatim(t *testing.T) {
	cmd := newBodyInputTestCmd("")
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := resolveMessageBody(cmd, "hello `world`", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello `world`" {
		t.Errorf("positional body should pass verbatim; got %q", got)
	}
}

// TestResolveMessageBody_Stdin pins reading from stdin via --stdin, including
// the single-trailing-newline strip a heredoc appends.
func TestResolveMessageBody_Stdin(t *testing.T) {
	cmd := newBodyInputTestCmd("body with `backticks` and $(cmd)\n")
	if err := cmd.ParseFlags([]string{"--stdin"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := resolveMessageBody(cmd, "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "body with `backticks` and $(cmd)" {
		t.Errorf("stdin body wrong; got %q", got)
	}
}

// TestResolveMessageBody_BacktickAndDollarBraceVerbatim is the load-bearing
// case for thrum-d3fp: a body containing BOTH backticks AND ${...} (the exact
// shell metacharacters that get command-substituted / expanded before thrum
// runs) must survive byte-for-byte through stdin and --body-file. This is the
// whole point of the feature — if either source mangles them, the fix is moot.
func TestResolveMessageBody_BacktickAndDollarBraceVerbatim(t *testing.T) {
	const body = "Run `make build`, then check ${PLUGIN_ROOT}/bin and $(git rev-parse HEAD)."

	t.Run("stdin", func(t *testing.T) {
		cmd := newBodyInputTestCmd(body + "\n")
		if err := cmd.ParseFlags([]string{"--stdin"}); err != nil {
			t.Fatalf("parse: %v", err)
		}
		got, err := resolveMessageBody(cmd, "", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != body {
			t.Errorf("stdin body not verbatim:\n got  %q\n want %q", got, body)
		}
	})

	t.Run("body-file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "body.md")
		if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		cmd := newBodyInputTestCmd("")
		if err := cmd.ParseFlags([]string{"--body-file", path}); err != nil {
			t.Fatalf("parse: %v", err)
		}
		got, err := resolveMessageBody(cmd, "", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != body {
			t.Errorf("body-file not verbatim:\n got  %q\n want %q", got, body)
		}
	})
}

// TestResolveMessageBody_DashAliasStdin pins MESSAGE=='-' as a stdin alias.
func TestResolveMessageBody_DashAliasStdin(t *testing.T) {
	cmd := newBodyInputTestCmd("piped body\n")
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := resolveMessageBody(cmd, "-", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "piped body" {
		t.Errorf("MESSAGE='-' should read stdin; got %q", got)
	}
}

// TestResolveMessageBody_BodyFile pins reading from --body-file, including the
// single-trailing-newline strip.
func TestResolveMessageBody_BodyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.txt")
	content := "file body with `backticks`\nsecond line\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := newBodyInputTestCmd("")
	if err := cmd.ParseFlags([]string{"--body-file", path}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := resolveMessageBody(cmd, "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "file body with `backticks`\nsecond line" {
		t.Errorf("body-file content wrong; got %q", got)
	}
}

// TestResolveMessageBody_NoSource errors when no body source is provided.
func TestResolveMessageBody_NoSource(t *testing.T) {
	cmd := newBodyInputTestCmd("")
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err := resolveMessageBody(cmd, "", false)
	if err == nil {
		t.Fatalf("expected error when no body source provided")
	}
	if !strings.Contains(err.Error(), "no message body") {
		t.Errorf("error should explain missing body; got %q", err.Error())
	}
}

// TestResolveMessageBody_AmbiguousSources errors when more than one body source
// is supplied. Covers positional+stdin, positional+file, and dash-alias+file.
func TestResolveMessageBody_AmbiguousSources(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cases := []struct {
		name       string
		flags      []string
		positional string
		hasPos     bool
	}{
		{"positional+stdin", []string{"--stdin"}, "hi", true},
		{"positional+bodyfile", []string{"--body-file", path}, "hi", true},
		{"dash+bodyfile", []string{"--body-file", path}, "-", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newBodyInputTestCmd("stdin-data")
			if err := cmd.ParseFlags(tc.flags); err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err := resolveMessageBody(cmd, tc.positional, tc.hasPos)
			if err == nil {
				t.Fatalf("expected ambiguity error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), "ambiguous") {
				t.Errorf("error should mention ambiguity; got %q", err.Error())
			}
		})
	}
}

// TestResolveMessageBody_StdinAndBodyFileMutex verifies cobra rejects --stdin
// and --body-file together at parse time (mutually exclusive flags).
func TestResolveMessageBody_StdinAndBodyFileMutex(t *testing.T) {
	cmd := newBodyInputTestCmd("")
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--stdin", "--body-file", "/tmp/x"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected mutex error for --stdin + --body-file")
	}
	if !strings.Contains(err.Error(), "stdin") || !strings.Contains(err.Error(), "body-file") {
		t.Errorf("mutex error should name both flags; got %q", err.Error())
	}
}

// TestResolveMessageBody_TrailingNewlineStrip pins the trim semantics: exactly
// one trailing newline removed (CRLF handled); additional blank lines preserved.
func TestResolveMessageBody_TrailingNewlineStrip(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a\n", "a"},
		{"a\r\n", "a"},
		{"a\n\n", "a\n"},
		{"a", "a"},
		{"a\nb\n", "a\nb"},
	}
	for _, tc := range cases {
		cmd := newBodyInputTestCmd(tc.in)
		if err := cmd.ParseFlags([]string{"--stdin"}); err != nil {
			t.Fatalf("parse: %v", err)
		}
		got, err := resolveMessageBody(cmd, "", false)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("trim(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// TestResolveMessageBody_BodyFileMissing surfaces a clear read error.
func TestResolveMessageBody_BodyFileMissing(t *testing.T) {
	cmd := newBodyInputTestCmd("")
	if err := cmd.ParseFlags([]string{"--body-file", "/no/such/path/xyzzy"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err := resolveMessageBody(cmd, "", false)
	if err == nil {
		t.Fatalf("expected error for missing body-file")
	}
	if !strings.Contains(err.Error(), "/no/such/path/xyzzy") {
		t.Errorf("error should name the path; got %q", err.Error())
	}
}

// TestSendCmd_NoBodyNoStdin pins the relaxed Args: `thrum send --to @x` with no
// positional and no stdin/file source must fail with the body-source error
// (not a cobra arg-count error), and must short-circuit before any daemon RPC
// (stdout empty).
func TestSendCmd_NoBodyNoStdin(t *testing.T) {
	cmd := sendCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--to", "@coordinator_main"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when no body source provided; stdout=%q", stdout.String())
	}
	if !strings.Contains(err.Error(), "no message body") {
		t.Errorf("expected body-source error; got %q", err.Error())
	}
	if stdout.String() != "" {
		t.Errorf("expected empty stdout (must short-circuit before RPC); got %q", stdout.String())
	}
}

// TestReplyCmd_NoBodyNoStdin pins reply's relaxed Args: MSG_ID present but no
// body and no stdin/file must fail with the body-source error before any RPC.
func TestReplyCmd_NoBodyNoStdin(t *testing.T) {
	cmd := replyCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"msg_01TEST"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when no body source provided; stdout=%q", stdout.String())
	}
	if !strings.Contains(err.Error(), "no message body") {
		t.Errorf("expected body-source error; got %q", err.Error())
	}
	if stdout.String() != "" {
		t.Errorf("expected empty stdout (must short-circuit before RPC); got %q", stdout.String())
	}
}

// TestReplyCmd_MissingMsgID pins that MSG_ID stays required even with the
// relaxed body args (RangeArgs lower bound is 1).
func TestReplyCmd_MissingMsgID(t *testing.T) {
	cmd := replyCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--stdin"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when MSG_ID omitted")
	}
	// Pin the cobra arg-count rejection so a regression to ExactArgs(2) or a
	// raised lower bound is caught (RangeArgs(1,2) lower bound must stay 1).
	if !strings.Contains(err.Error(), "arg") {
		t.Errorf("expected cobra arg-count error; got %q", err.Error())
	}
}

// TestEditCmd_NoBodyNoStdin pins `message edit`'s relaxed Args + body wiring:
// MSG_ID present but no body and no stdin/file must fail with the body-source
// error, short-circuiting before identity resolution or any RPC (stdout empty).
// Exercising the nested edit subcommand also guards against accidental removal
// of addBodyInputFlags(editCmd).
func TestEditCmd_NoBodyNoStdin(t *testing.T) {
	cmd := messageCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"edit", "msg_01TEST"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when no body source provided; stdout=%q", stdout.String())
	}
	if !strings.Contains(err.Error(), "no message body") {
		t.Errorf("expected body-source error; got %q", err.Error())
	}
	if stdout.String() != "" {
		t.Errorf("expected empty stdout (must short-circuit before RPC); got %q", stdout.String())
	}
}

// TestEditCmd_StdinFlagRegistered guards that --stdin is wired on the edit
// subcommand (addBodyInputFlags(editCmd)); a parse failure here would mean the
// flag registration regressed.
func TestEditCmd_StdinFlagRegistered(t *testing.T) {
	cmd := messageCmd()
	edit, _, err := cmd.Find([]string{"edit"})
	if err != nil {
		t.Fatalf("find edit subcommand: %v", err)
	}
	if edit.Flags().Lookup("stdin") == nil {
		t.Errorf("message edit missing --stdin flag (addBodyInputFlags not wired)")
	}
	if edit.Flags().Lookup("body-file") == nil {
		t.Errorf("message edit missing --body-file flag (addBodyInputFlags not wired)")
	}
}
