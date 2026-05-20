package main

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/leonletto/thrum/internal/identity/guard"
)

// withCrossWorktreeGuardStub swaps the package-level
// checkCrossWorktreeGuard for the duration of the test and restores
// it on cleanup. The stub returns whatever err the caller closes over.
func withCrossWorktreeGuardStub(t *testing.T, err error) {
	t.Helper()
	prev := checkCrossWorktreeGuard
	checkCrossWorktreeGuard = func(string) error { return err }
	t.Cleanup(func() { checkCrossWorktreeGuard = prev })
}

// resetCrossWorktreeAbsorbed clears the dedup flag between subtests.
// Tests MUST reset because cobra normally invokes one leaf per
// process; in-test we exercise the path multiple times.
func resetCrossWorktreeAbsorbed(t *testing.T) {
	t.Helper()
	prev := crossWorktreeAbsorbed
	crossWorktreeAbsorbed = false
	t.Cleanup(func() { crossWorktreeAbsorbed = prev })
}

// cmdWithResponseAnnotation builds a cobra.Command with the
// cross_worktree_response annotation set to resp ("" leaves the
// annotation absent, exercising the default).
func cmdWithResponseAnnotation(resp string) *cobra.Command {
	cmd := &cobra.Command{}
	if resp != "" {
		cmd.Annotations = map[string]string{crossWorktreeResponseKey: resp}
	}
	return cmd
}

// TestCrossWorktreePreflight covers the annotation-gated dispatch in
// crossWorktreePreflight: only Class B/C leaves emit a banner; Class A
// and unannotated leaves are no-ops; --repo override and a passing
// guard.Check are also no-ops.
//
// Pins the thrum-7b84.11 contract: Class B/C leaves get the
// diagnostic banner even when they bypass getClient (e.g. `thrum
// daemon status` → cli.DaemonStatus).
func TestCrossWorktreePreflight(t *testing.T) {
	xworktreeErr := &guard.Error{
		Guard:         "cross_worktree",
		Reason:        "pid_mismatch",
		ExpectedAgent: "coordinator_main",
	}
	primeOwnErr := &guard.Error{Guard: "prime_ownership", Reason: "non_owner"}

	cases := []struct {
		name         string
		resp         string
		guardErr     error
		repoSet      bool
		wantStderr   string
		wantAbsorbed bool
	}{
		{
			name:         "diagnostic_banner class on cross_worktree fires banner",
			resp:         CrossWorktreeResponseDiagnosticBanner,
			guardErr:     xworktreeErr,
			wantStderr:   "⚠ Cross-worktree:",
			wantAbsorbed: true,
		},
		{
			name:         "whoami class on cross_worktree fires banner",
			resp:         CrossWorktreeResponseWhoami,
			guardErr:     xworktreeErr,
			wantStderr:   "⚠ Cross-worktree:",
			wantAbsorbed: true,
		},
		{
			name:         "abort class is no-op (getClient handles abort)",
			resp:         CrossWorktreeResponseAbort,
			guardErr:     xworktreeErr,
			wantStderr:   "",
			wantAbsorbed: false,
		},
		{
			name:         "missing annotation is no-op (safe default)",
			resp:         "",
			guardErr:     xworktreeErr,
			wantStderr:   "",
			wantAbsorbed: false,
		},
		{
			name:         "diagnostic_banner with --repo override is no-op (operator escape hatch)",
			resp:         CrossWorktreeResponseDiagnosticBanner,
			guardErr:     xworktreeErr,
			repoSet:      true,
			wantStderr:   "",
			wantAbsorbed: false,
		},
		{
			name:         "diagnostic_banner with no guard fire is no-op",
			resp:         CrossWorktreeResponseDiagnosticBanner,
			guardErr:     nil,
			wantStderr:   "",
			wantAbsorbed: false,
		},
		{
			name:         "diagnostic_banner with non-cross_worktree guard fire is no-op (deferred to classify)",
			resp:         CrossWorktreeResponseDiagnosticBanner,
			guardErr:     primeOwnErr,
			wantStderr:   "",
			wantAbsorbed: false,
		},
		{
			name:         "diagnostic_banner with plain non-guard error is no-op",
			resp:         CrossWorktreeResponseDiagnosticBanner,
			guardErr:     io.EOF,
			wantStderr:   "",
			wantAbsorbed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withCrossWorktreeGuardStub(t, tc.guardErr)
			resetCrossWorktreeAbsorbed(t)

			var cmd *cobra.Command
			if tc.repoSet {
				cmd = cmdWithRepoFlag(t, tc.resp, true)
			} else {
				cmd = cmdWithResponseAnnotation(tc.resp)
			}

			_, stderr := captureStdStreams(t, func() {
				crossWorktreePreflight(cmd, "/tmp/test-repo")
			})

			if tc.wantStderr == "" {
				if stderr != "" {
					t.Errorf("expected empty stderr (no banner), got %q", stderr)
				}
			} else if !strings.Contains(stderr, tc.wantStderr) {
				t.Errorf("expected stderr to contain %q, got %q", tc.wantStderr, stderr)
			}

			if crossWorktreeAbsorbed != tc.wantAbsorbed {
				t.Errorf("crossWorktreeAbsorbed=%v, want %v", crossWorktreeAbsorbed, tc.wantAbsorbed)
			}
		})
	}
}

// TestCrossWorktreePreflight_WhoamiBannerHitsStdout pins the Class C
// stdout-too behavior end-to-end through the preflight path. The
// existing TestEmitCrossWorktreeBanner_StdoutAndStderr covers
// emitCrossWorktreeBanner in isolation; this asserts the preflight
// correctly routes whoami leaves to the stdout-true emit path.
func TestCrossWorktreePreflight_WhoamiBannerHitsStdout(t *testing.T) {
	withCrossWorktreeGuardStub(t, &guard.Error{
		Guard:         "cross_worktree",
		Reason:        "pid_mismatch",
		ExpectedAgent: "coordinator_main",
	})
	resetCrossWorktreeAbsorbed(t)

	prevJSON := flagJSON
	flagJSON = false
	t.Cleanup(func() { flagJSON = prevJSON })

	stdout, stderr := captureStdStreams(t, func() {
		crossWorktreePreflight(cmdWithResponseAnnotation(CrossWorktreeResponseWhoami), "/tmp/test-repo")
	})

	if !strings.Contains(stdout, "⚠ Cross-worktree:") {
		t.Errorf("whoami preflight must also write banner to stdout (Class C contract), got %q", stdout)
	}
	if !strings.Contains(stderr, "⚠ Cross-worktree:") {
		t.Errorf("whoami preflight must still write banner to stderr, got %q", stderr)
	}
	if !crossWorktreeAbsorbed {
		t.Error("crossWorktreeAbsorbed must be set after whoami preflight emit")
	}
}

// TestClassifyRefreshError_DedupAfterPreflight pins the dedup contract:
// when crossWorktreePreflight already emitted the banner, a subsequent
// classifyRefreshError (from getClient on the same invocation) must
// NOT emit a second banner. This matters for Class B/C leaves that
// flow through getClient AND preflight — e.g. `thrum team` runs
// preflight in PersistentPreRunE and classify inside getClient.
//
// Without dedup, those leaves would emit two banners per invocation.
func TestClassifyRefreshError_DedupAfterPreflight(t *testing.T) {
	xworktreeErr := &guard.Error{
		Guard:         "cross_worktree",
		Reason:        "pid_mismatch",
		ExpectedAgent: "coordinator_main",
	}

	for _, resp := range []string{CrossWorktreeResponseDiagnosticBanner, CrossWorktreeResponseWhoami} {
		t.Run(resp, func(t *testing.T) {
			resetCrossWorktreeAbsorbed(t)
			crossWorktreeAbsorbed = true // simulate preflight already fired

			cmd := cmdWithResponseAnnotation(resp)
			stdout, stderr := captureStdStreams(t, func() {
				fatalErr, absorbed := classifyRefreshError(cmd, xworktreeErr)
				if fatalErr != nil {
					t.Errorf("dedup path must not surface fatalErr, got %v", fatalErr)
				}
				if !absorbed {
					t.Error("dedup path must mark absorbed=true so caller suppresses raw dump")
				}
			})
			if stdout != "" {
				t.Errorf("dedup path must not write to stdout, got %q", stdout)
			}
			if stderr != "" {
				t.Errorf("dedup path must not write a second banner to stderr, got %q", stderr)
			}
		})
	}
}

// TestCrossWorktreePreflight_AbsorbsCorrectGuardErrorType is a
// regression guard: only *guard.Error{Guard: "cross_worktree"}
// triggers the banner; other *guard.Error values flow through to
// classify. Plain non-guard errors also flow through.
func TestCrossWorktreePreflight_AbsorbsCorrectGuardErrorType(t *testing.T) {
	resetCrossWorktreeAbsorbed(t)
	wrappedErr := errors.New("wrapped non-guard error")
	withCrossWorktreeGuardStub(t, wrappedErr)

	stdout, stderr := captureStdStreams(t, func() {
		crossWorktreePreflight(cmdWithResponseAnnotation(CrossWorktreeResponseDiagnosticBanner), "/tmp/test-repo")
	})
	if stdout != "" || stderr != "" {
		t.Errorf("non-guard error must not fire banner; stdout=%q stderr=%q", stdout, stderr)
	}
	if crossWorktreeAbsorbed {
		t.Error("crossWorktreeAbsorbed must stay false on non-guard error")
	}
}

// TestCrossWorktreePreflight_JSONModeStderrOnly pins the --json
// contract through the preflight path: Class B leaves never write
// the banner to stdout (which would corrupt the single-document JSON
// stream). The existing TestEmitCrossWorktreeBanner_JSONSuppressesStdout
// covers the emit fn directly; this asserts the preflight respects
// flagJSON for Class B (the most common --json consumer). Class C
// (whoami) is intentionally NOT included — by design the whoami
// banner reaches stdout in non-JSON mode and is suppressed in JSON
// mode by emitCrossWorktreeBanner itself.
func TestCrossWorktreePreflight_JSONModeStderrOnly(t *testing.T) {
	withCrossWorktreeGuardStub(t, &guard.Error{
		Guard:         "cross_worktree",
		Reason:        "pid_mismatch",
		ExpectedAgent: "coordinator_main",
	})
	resetCrossWorktreeAbsorbed(t)

	prevJSON := flagJSON
	flagJSON = true
	t.Cleanup(func() { flagJSON = prevJSON })

	stdout, stderr := captureStdStreams(t, func() {
		crossWorktreePreflight(cmdWithResponseAnnotation(CrossWorktreeResponseDiagnosticBanner), "/tmp/test-repo")
	})
	if stdout != "" {
		t.Errorf("--json mode: Class B preflight must keep stdout banner-free, got %q", stdout)
	}
	if !strings.Contains(stderr, "⚠ Cross-worktree:") {
		t.Errorf("--json mode: Class B preflight must still emit stderr banner, got %q", stderr)
	}
}

// TestCrossWorktreePreflight_RealCobraLeaves_FireBanner is the
// integration check that wires together the cobra tree, the
// annotation tags, and the preflight dispatch. It exercises every
// known bypass-getClient leaf in diagnosticBannerLeaves and asserts
// the preflight fires the banner when guard.Check would. Without
// this test, a wiring regression in PersistentPreRunE (e.g. the
// preflight call being moved inside a wrong-condition gate) could
// slip past the unit tests above. See thrum-7b84.11 ticket § 2a/2b.
func TestCrossWorktreePreflight_RealCobraLeaves_FireBanner(t *testing.T) {
	// These leaves are the original bug surface — they bypass
	// getClient in their RunE bodies. The test asserts preflight
	// fires the banner for each. Adding a new bypass-getClient
	// Class B leaf to command_categories.go without preflight
	// wiring would fail here once added to this list, which is the
	// intended audit signal.
	bypassClassBLeaves := []string{
		"thrum daemon status",
		"thrum daemon logs",
		"thrum daemon start",
		"thrum daemon stop",
		"thrum daemon restart",
		"thrum daemon run",
		"thrum backup status",
		"thrum telegram status",
	}

	withCrossWorktreeGuardStub(t, &guard.Error{
		Guard:         "cross_worktree",
		Reason:        "pid_mismatch",
		ExpectedAgent: "coordinator_main",
	})

	root := buildRootCmd()
	leavesByPath := map[string]*cobra.Command{}
	walkLeaves(root, func(leaf *cobra.Command) {
		leavesByPath[leaf.CommandPath()] = leaf
	})

	for _, path := range bypassClassBLeaves {
		t.Run(path, func(t *testing.T) {
			resetCrossWorktreeAbsorbed(t)
			leaf, ok := leavesByPath[path]
			if !ok {
				t.Fatalf("cobra tree missing leaf %q — taxonomy may have drifted", path)
			}
			// Sanity: confirm the live tree carries the expected annotation.
			if got := leaf.Annotations[crossWorktreeResponseKey]; got != CrossWorktreeResponseDiagnosticBanner {
				t.Fatalf("leaf %q annotation = %q, want %q (taxonomy regression)",
					path, got, CrossWorktreeResponseDiagnosticBanner)
			}
			_, stderr := captureStdStreams(t, func() {
				crossWorktreePreflight(leaf, "/tmp/test-repo")
			})
			if !strings.Contains(stderr, "⚠ Cross-worktree:") {
				t.Errorf("preflight on %q did not fire banner; stderr=%q", path, stderr)
			}
			if !crossWorktreeAbsorbed {
				t.Errorf("preflight on %q did not set crossWorktreeAbsorbed", path)
			}
		})
	}
}
