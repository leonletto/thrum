package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/leonletto/thrum/internal/identity/guard"
)

// TestCrossWorktreeResponseFor covers the safe-default path:
// nil cmd, missing annotation, all three valid annotations.
func TestCrossWorktreeResponseFor(t *testing.T) {
	cases := []struct {
		name string
		cmd  *cobra.Command
		want string
	}{
		{"nil cmd defaults to abort", nil, CrossWorktreeResponseAbort},
		{"missing annotation defaults to abort", &cobra.Command{}, CrossWorktreeResponseAbort},
		{
			"empty annotation value defaults to abort",
			&cobra.Command{Annotations: map[string]string{crossWorktreeResponseKey: ""}},
			CrossWorktreeResponseAbort,
		},
		{
			"abort annotation",
			&cobra.Command{Annotations: map[string]string{crossWorktreeResponseKey: CrossWorktreeResponseAbort}},
			CrossWorktreeResponseAbort,
		},
		{
			"diagnostic_banner annotation",
			&cobra.Command{Annotations: map[string]string{crossWorktreeResponseKey: CrossWorktreeResponseDiagnosticBanner}},
			CrossWorktreeResponseDiagnosticBanner,
		},
		{
			"whoami annotation",
			&cobra.Command{Annotations: map[string]string{crossWorktreeResponseKey: CrossWorktreeResponseWhoami}},
			CrossWorktreeResponseWhoami,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := crossWorktreeResponseFor(tc.cmd); got != tc.want {
				t.Errorf("crossWorktreeResponseFor = %q, want %q", got, tc.want)
			}
		})
	}
}

// captureStdStreams swaps os.Stdout and os.Stderr for the duration of
// fn and returns whatever was written to each. Used to assert the
// Enhanced Policy 2 banner content + ordering.
func captureStdStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	origStdout, origStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutW, stderrW

	done := make(chan struct{}, 2)
	var outBuf, errBuf bytes.Buffer
	go func() { _, _ = io.Copy(&outBuf, stdoutR); done <- struct{}{} }()
	go func() { _, _ = io.Copy(&errBuf, stderrR); done <- struct{}{} }()

	fn()

	_ = stdoutW.Close()
	_ = stderrW.Close()
	<-done
	<-done

	os.Stdout, os.Stderr = origStdout, origStderr
	return outBuf.String(), errBuf.String()
}

// TestEmitCrossWorktreeBanner_StderrOnly pins Class B behavior:
// banner goes to stderr only, stdout stays clean.
func TestEmitCrossWorktreeBanner_StderrOnly(t *testing.T) {
	ge := &guard.Error{
		Guard:         "cross_worktree",
		Reason:        "pid_mismatch",
		ExpectedAgent: "coordinator_main",
	}

	stdout, stderr := captureStdStreams(t, func() {
		emitCrossWorktreeBanner(ge, false)
	})

	if stdout != "" {
		t.Errorf("expected empty stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "⚠ Cross-worktree:") {
		t.Errorf("stderr missing banner prefix:\n%s", stderr)
	}
	if !strings.Contains(stderr, "coordinator_main's worktree") {
		t.Errorf("stderr missing expected-agent token:\n%s", stderr)
	}
	if !strings.Contains(stderr, "cd to your own worktree") {
		t.Errorf("stderr missing remediation hint:\n%s", stderr)
	}
}

// TestEmitCrossWorktreeBanner_StdoutAndStderr pins Class C behavior:
// banner goes to BOTH stderr (always) and stdout (when stdoutToo=true
// AND not --json). Used by whoami where the stdout consumer needs
// the cross-worktree context inline.
func TestEmitCrossWorktreeBanner_StdoutAndStderr(t *testing.T) {
	ge := &guard.Error{
		Guard:         "cross_worktree",
		Reason:        "pid_mismatch",
		ExpectedAgent: "impl_data_tier",
	}

	prevJSON := flagJSON
	flagJSON = false
	t.Cleanup(func() { flagJSON = prevJSON })

	stdout, stderr := captureStdStreams(t, func() {
		emitCrossWorktreeBanner(ge, true)
	})

	if !strings.Contains(stdout, "⚠ Cross-worktree:") {
		t.Errorf("stdout missing banner:\n%s", stdout)
	}
	if !strings.Contains(stdout, "impl_data_tier's worktree") {
		t.Errorf("stdout missing expected-agent token:\n%s", stdout)
	}
	if !strings.Contains(stderr, "⚠ Cross-worktree:") {
		t.Errorf("stderr missing banner:\n%s", stderr)
	}
}

// TestEmitCrossWorktreeBanner_JSONSuppressesStdout pins the --json
// contract: even for Class C (whoami) the stdout write is suppressed
// when --json is on, so the single-document JSON stream stays valid.
// The slog bridge surfaces equivalent context via the hints array.
func TestEmitCrossWorktreeBanner_JSONSuppressesStdout(t *testing.T) {
	ge := &guard.Error{
		Guard:         "cross_worktree",
		Reason:        "pid_mismatch",
		ExpectedAgent: "coordinator_main",
	}

	prevJSON := flagJSON
	flagJSON = true
	t.Cleanup(func() { flagJSON = prevJSON })

	stdout, stderr := captureStdStreams(t, func() {
		emitCrossWorktreeBanner(ge, true)
	})

	if stdout != "" {
		t.Errorf("--json mode must keep stdout banner-free for the JSON contract, got %q", stdout)
	}
	if !strings.Contains(stderr, "⚠ Cross-worktree:") {
		t.Errorf("stderr banner must fire even in --json mode:\n%s", stderr)
	}
}

// TestEmitCrossWorktreeBanner_NoExpectedAgent verifies the
// fallback wording when guard.Error doesn't carry an ExpectedAgent
// (defensive — the guard machinery always populates this in the
// pid_mismatch path, but the banner shouldn't render an empty
// possessive ("'s worktree") if the field is ever blank).
func TestEmitCrossWorktreeBanner_NoExpectedAgent(t *testing.T) {
	ge := &guard.Error{
		Guard:  "cross_worktree",
		Reason: "pid_mismatch",
	}

	_, stderr := captureStdStreams(t, func() {
		emitCrossWorktreeBanner(ge, false)
	})

	if strings.Contains(stderr, "'s worktree") && !strings.Contains(stderr, "another agent's worktree") {
		t.Errorf("expected 'another agent's worktree' fallback, got:\n%s", stderr)
	}
}

// TestClassifyRefreshError covers the decision matrix in
// classifyRefreshError: which error/class combinations propagate
// (fail closed) vs absorb (banner emitted, continue) vs neither
// (pass through to legacy log-and-proceed). The negative-evidence
// fingerprint from the dispatch (wrong-cwd send → exit 1, no
// `✓ Message sent`, no entry in `thrum sent`) follows from the
// fatalErr arm here — getClient closes the client and returns the
// error, callers exit 1, no RPC ever fires.
func TestClassifyRefreshError(t *testing.T) {
	xworktreeErr := &guard.Error{Guard: "cross_worktree", Reason: "pid_mismatch", ExpectedAgent: "alice"}
	otherGuardErr := &guard.Error{Guard: "dead_pid_auto_reclaim", Reason: "dead_owner_reclaimed"}

	cases := []struct {
		name         string
		cmdResp      string
		refreshErr   error
		wantFatal    bool
		wantAbsorbed bool
	}{
		{
			name:         "abort class on cross_worktree fails closed",
			cmdResp:      CrossWorktreeResponseAbort,
			refreshErr:   xworktreeErr,
			wantFatal:    true,
			wantAbsorbed: false,
		},
		{
			name:         "missing annotation defaults to abort",
			cmdResp:      "",
			refreshErr:   xworktreeErr,
			wantFatal:    true,
			wantAbsorbed: false,
		},
		{
			name:         "diagnostic_banner class absorbs cross_worktree",
			cmdResp:      CrossWorktreeResponseDiagnosticBanner,
			refreshErr:   xworktreeErr,
			wantFatal:    false,
			wantAbsorbed: true,
		},
		{
			name:         "whoami class absorbs cross_worktree",
			cmdResp:      CrossWorktreeResponseWhoami,
			refreshErr:   xworktreeErr,
			wantFatal:    false,
			wantAbsorbed: true,
		},
		{
			name:         "non-cross_worktree guard error passes through (legacy log-and-proceed)",
			cmdResp:      CrossWorktreeResponseAbort,
			refreshErr:   otherGuardErr,
			wantFatal:    false,
			wantAbsorbed: false,
		},
		{
			name:         "plain non-guard error passes through",
			cmdResp:      CrossWorktreeResponseAbort,
			refreshErr:   io.EOF, // any non-*guard.Error
			wantFatal:    false,
			wantAbsorbed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			if tc.cmdResp != "" {
				cmd.Annotations = map[string]string{crossWorktreeResponseKey: tc.cmdResp}
			}
			// Drain banner output so test logs stay clean.
			_, _ = captureStdStreams(t, func() {
				gotFatal, gotAbsorbed := classifyRefreshError(cmd, tc.refreshErr)
				if (gotFatal != nil) != tc.wantFatal {
					t.Errorf("classifyRefreshError fatal=%v (err=%v), want fatal=%v",
						gotFatal != nil, gotFatal, tc.wantFatal)
				}
				if gotAbsorbed != tc.wantAbsorbed {
					t.Errorf("classifyRefreshError absorbed=%v, want absorbed=%v", gotAbsorbed, tc.wantAbsorbed)
				}
			})
		})
	}
}

// TestRealCobraLeaves_HaveExpectedClasses cross-checks the live cobra
// tree against the Enhanced Policy 2 spec lists (thrum-7b84.6).
// Equivalent to TestEveryLeafHasCrossWorktreeResponse but asserts the
// SPECIFIC classification per the spec, not just that some valid
// classification exists.
func TestRealCobraLeaves_HaveExpectedClasses(t *testing.T) {
	root := buildRootCmd()
	expectations := map[string]string{
		// Class A — Abortable (sample; full list is tagged by default)
		"thrum send":           CrossWorktreeResponseAbort,
		"thrum reply":          CrossWorktreeResponseAbort,
		"thrum inbox":          CrossWorktreeResponseAbort,
		"thrum sent":           CrossWorktreeResponseAbort,
		"thrum wait":           CrossWorktreeResponseAbort,
		"thrum message read":   CrossWorktreeResponseAbort,
		"thrum message edit":   CrossWorktreeResponseAbort,
		"thrum quickstart":     CrossWorktreeResponseAbort,
		"thrum prime":          CrossWorktreeResponseAbort,
		"thrum tmux send":      CrossWorktreeResponseAbort,
		"thrum tmux create":    CrossWorktreeResponseAbort,
		"thrum context save":   CrossWorktreeResponseAbort,
		"thrum agent register": CrossWorktreeResponseAbort,
		"thrum session start":  CrossWorktreeResponseAbort,

		// Class B — DiagnosticBanner
		"thrum team":           CrossWorktreeResponseDiagnosticBanner,
		"thrum agent list":     CrossWorktreeResponseDiagnosticBanner,
		"thrum version":        CrossWorktreeResponseDiagnosticBanner,
		"thrum daemon logs":    CrossWorktreeResponseDiagnosticBanner,
		"thrum daemon restart": CrossWorktreeResponseDiagnosticBanner,
		"thrum daemon run":     CrossWorktreeResponseDiagnosticBanner,
		"thrum daemon start":   CrossWorktreeResponseDiagnosticBanner,
		"thrum daemon status":  CrossWorktreeResponseDiagnosticBanner,
		"thrum daemon stop":    CrossWorktreeResponseDiagnosticBanner,
		// Class B — status-verb siblings (ratified by
		// @researcher_inbox_race third-pass review): all identity-
		// agnostic daemon/system status reporters belong here.
		"thrum peer status":     CrossWorktreeResponseDiagnosticBanner,
		"thrum sync status":     CrossWorktreeResponseDiagnosticBanner,
		"thrum backup status":   CrossWorktreeResponseDiagnosticBanner,
		"thrum telegram status": CrossWorktreeResponseDiagnosticBanner,
		"thrum tmux status":     CrossWorktreeResponseDiagnosticBanner,

		// Class C — Whoami
		"thrum whoami":       CrossWorktreeResponseWhoami,
		"thrum agent whoami": CrossWorktreeResponseWhoami,
	}

	seen := map[string]string{}
	walkLeaves(root, func(leaf *cobra.Command) {
		if resp, ok := leaf.Annotations[crossWorktreeResponseKey]; ok {
			seen[leaf.CommandPath()] = resp
		}
	})

	for path, want := range expectations {
		got, ok := seen[path]
		if !ok {
			t.Errorf("leaf %q missing from cobra tree (rename?)", path)
			continue
		}
		if got != want {
			t.Errorf("leaf %q classified as %q, want %q (per Enhanced Policy 2 spec)", path, got, want)
		}
	}
}
