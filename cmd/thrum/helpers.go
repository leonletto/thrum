package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/identity/guard"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:774-776
// Destination: helpers.go:25-27
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// isInteractive returns true if stdin is a terminal (not piped/redirected).
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) // #nosec G115 -- file descriptors are small non-negative integers; uintptr->int conversion cannot overflow
}

// ORIGIN[thrum-8kxh]: moved from main.go:5878-5908
// Destination: helpers.go:64-94
// Tests: cmd/thrum/cross_worktree_response_test.go (indirect via classifyRefreshError); cmd/thrum/job_test.go; cmd/thrum/hints_integration_test.go
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// getClient returns a configured RPC client.
// Respects THRUM_SOCKET env var if set, otherwise uses DefaultSocketPath.
// GetClient opens a daemon connection and refreshes the local identity
// file + daemon's agent record from live process/tmux/git state. Use for
// every command except daemon lifecycle, init, and quickstart — those
// should call getClientNoRefresh().
//
// Refresh failures are non-fatal by default: they log to stderr and the
// underlying command proceeds normally. See RefreshLocalIdentity doc for
// details.
//
// thrum-7b84.6 (Enhanced Policy 2): strict-mode cross_worktree guard
// fires get per-class treatment based on the calling leaf's
// crossWorktreeResponseKey annotation:
//   - abort (default): close the client and propagate refreshErr; the
//     command exits non-zero with the 4-line guard error on stderr and
//     an empty stdout. Wrong-identity writes are permanent and
//     ~undetectable.
//   - diagnostic_banner: emit a one-line stderr banner BEFORE any
//     stdout write, then let the command proceed. For team / daemon * /
//     agent list / version where output is useful from the wrong cwd.
//   - whoami: emit the banner on BOTH stderr and stdout (prepended to
//     the identity block in non-JSON mode). Whoami's stdout asserts
//     identity, so the cross-worktree context belongs inline. In --json
//     mode the stdout banner is suppressed (would corrupt the JSON
//     contract); the slog bridge surfaces the warn via the hints array.
//
// Other guard reasons (dead_pid_auto_reclaim etc.) keep the original
// log-and-proceed contract regardless of class.
func getClient() (*cli.Client, error) {
	client, err := getClientNoRefresh()
	if err != nil {
		return nil, err
	}

	repoPath := flagRepo
	if repoPath == "" {
		repoPath = "."
	}
	if _, refreshErr := cli.RefreshLocalIdentity(client, repoPath); refreshErr != nil {
		fatalErr, absorbed := classifyRefreshError(currentCobraCmd, refreshErr)
		if fatalErr != nil {
			// Abort path: print the raw refresh error so the user
			// sees the structured 4-line guard error block on stderr
			// before the command exits. The banner branches below
			// produce their own user-friendly stderr write and don't
			// need the raw dump preceding them.
			fmt.Fprintf(os.Stderr, "thrum: identity refresh failed: %v\n", refreshErr)
			_ = client.Close()
			return nil, fatalErr
		}
		if !absorbed {
			// Not a cross_worktree fire — keep the original
			// log-and-proceed contract: log to stderr and continue.
			fmt.Fprintf(os.Stderr, "thrum: identity refresh failed: %v\n", refreshErr)
		}
	}

	return client, nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:6017-6023
// Destination: helpers.go:107-113
// Tests: cmd/thrum/job_test.go (indirect via daemon RPC bind)
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// getClientNoRefresh opens a daemon connection without running the identity
// refresh. Use for:
//   - daemon lifecycle commands (start/stop/restart/status/logs)
//   - init and quickstart (before/during initial registration)
//   - any test or diagnostic tool that must not side-effect the identity
func getClientNoRefresh() (*cli.Client, error) {
	socketPath := os.Getenv("THRUM_SOCKET")
	if socketPath == "" {
		socketPath = cli.DefaultSocketPath(flagRepo)
	}
	return cli.NewClient(socketPath)
}

// ORIGIN[thrum-8kxh]: moved from main.go:6043-6059
// Destination: helpers.go:139-155
// Tests: cmd/thrum/email_test.go; cmd/thrum/job_test.go; cmd/thrum/hints_integration_test.go
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// resolveLocalAgentID resolves the agent ID from the local worktree's identity file.
// This is used to pass caller identity to the daemon, which may be running in a
// different worktree (via .thrum/redirect). Returns empty string if resolution fails.
//
// Priority order (rc.6 — thrum-qofl):
//  1. Cwd-anchored config (`config.LoadWithPath(flagRepo, ...)`) when it
//     resolves to a valid identity. Cwd has authority when it has thrum state.
//  2. `THRUM_AGENT_ID` env var as a fallback when cwd-based config resolution
//     fails (e.g., caller is outside any worktree, or worktree has no
//     matching identity).
//
// This inverts the rc.5-and-earlier order (env wins). Old behavior produced
// cross-worktree misidentification when stale `THRUM_AGENT_ID` was inherited
// at fork time from a parent shell — e.g., a Claude process forked from a
// shell anchored to `falcon_llm_client` but then operating in `falcon-agent`
// would claim the wrong agent_id on every RPC. Cwd-first resolution closes
// that footgun while keeping env as a legitimate fallback for callers outside
// any worktree.
func resolveLocalAgentID() (string, error) {
	cfg, err := config.LoadWithPath(flagRepo, flagRole, flagModule)
	if err == nil && cfg.Agent.Name != "" {
		// For named agents, GenerateAgentID returns the name directly.
		// For unnamed agents, it generates a deterministic hash-based ID.
		return identity.GenerateAgentID(cfg.RepoID, cfg.Agent.Role, cfg.Agent.Module, cfg.Agent.Name), nil
	}

	if agentID := strings.TrimSpace(os.Getenv("THRUM_AGENT_ID")); agentID != "" {
		return agentID, nil
	}

	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("no agent name in config and THRUM_AGENT_ID env var not set")
}

// ORIGIN[thrum-8kxh]: moved from main.go:6063-6069
// Destination: helpers.go:165-171
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// resolveLocalMentionRole resolves the agent's role from the local worktree's identity file.
// Used for the --mentions filter so the daemon filters by the correct role.
func resolveLocalMentionRole() (string, error) {
	cfg, err := config.LoadWithPath(flagRepo, flagRole, flagModule)
	if err != nil {
		return "", err
	}
	return cfg.Agent.Role, nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:5932-5954
// Destination: helpers.go:201-223
// Tests: cmd/thrum/cross_worktree_response_test.go (TestClassifyRefreshError, TestRepoFlag_AbsorbsCrossWorktree)
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// classifyRefreshError decides what to do when RefreshLocalIdentity
// returned an error. Returns:
//   - fatalErr non-nil: getClient must close the client and propagate
//     (Class A abort path).
//   - absorbed=true: a Class B / Class C banner was emitted, or the
//     user explicitly passed --repo as an operator override; the
//     caller should NOT print the raw refresh error.
//   - fatalErr=nil + absorbed=false: not a cross_worktree fire; caller
//     keeps the legacy log-and-proceed contract for other guard
//     reasons (dead_pid_auto_reclaim etc.).
//
// --repo escape hatch: when the user explicitly passes --repo on the
// command line, they are asserting "I'm intentionally operating on
// this other repo" — the same operator-override intent the
// cross_worktree remediation message advertises. Suppress the guard
// fire in that case regardless of response class. Other guard
// reasons (dead_pid_auto_reclaim) still pass through to legacy
// log-and-proceed since they're unrelated to the cross-worktree
// scenario --repo overrides.
//
// Factored out of getClient for unit testability — the policy
// decision is exercised independently of the daemon connection.
func classifyRefreshError(cmd *cobra.Command, refreshErr error) (fatalErr error, absorbed bool) {
	var ge *guard.Error
	if !errors.As(refreshErr, &ge) || ge.Guard != "cross_worktree" {
		return nil, false
	}
	if explicitRepoFlag(cmd) {
		// Operator override: --repo is the documented escape
		// hatch for cross-worktree calls. Absorb the guard fire
		// silently and let the command proceed against the
		// user-specified repo.
		return nil, true
	}
	switch crossWorktreeResponseFor(cmd) {
	case CrossWorktreeResponseDiagnosticBanner:
		emitCrossWorktreeBanner(ge, false)
		return nil, true
	case CrossWorktreeResponseWhoami:
		emitCrossWorktreeBanner(ge, true)
		return nil, true
	default: // CrossWorktreeResponseAbort (and any unknown value)
		return refreshErr, false
	}
}

// ORIGIN[thrum-8kxh]: moved from main.go:5959-5968
// Destination: helpers.go:234-243
// Tests: cmd/thrum/cross_worktree_response_test.go (TestExplicitRepoFlag)
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// explicitRepoFlag reports whether the caller explicitly passed
// --repo on the command line (vs. the default "."). Inherits the
// persistent flag from root; safe on nil cmd.
func explicitRepoFlag(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	f := cmd.Flags().Lookup("repo")
	if f == nil {
		return false
	}
	return f.Changed
}

// ORIGIN[thrum-8kxh]: moved from main.go:5974-5982
// Destination: helpers.go:255-263
// Tests: cmd/thrum/cross_worktree_response_test.go (TestCrossWorktreeResponseFor)
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// crossWorktreeResponseFor returns the leaf's annotated response class
// or CrossWorktreeResponseAbort as the safe default when the cmd is
// nil (e.g., called outside the normal cobra flow) or the annotation
// is missing.
func crossWorktreeResponseFor(cmd *cobra.Command) string {
	if cmd == nil {
		return CrossWorktreeResponseAbort
	}
	if resp, ok := cmd.Annotations[crossWorktreeResponseKey]; ok && resp != "" {
		return resp
	}
	return CrossWorktreeResponseAbort
}

// ORIGIN[thrum-8kxh]: moved from main.go:5992-6010
// Destination: helpers.go:279-297
// Tests: cmd/thrum/cross_worktree_response_test.go (TestEmitBanner_*)
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// emitCrossWorktreeBanner writes the Enhanced Policy 2 cross-worktree
// banner. Stderr is always written (and flushed first per the
// pipe/tee ordering contract). When stdoutToo is true (Class C —
// whoami) AND the caller is not in --json mode, the same banner is
// also written to stdout above the upcoming identity block. --json
// mode suppresses the stdout write to preserve the single-document
// JSON contract; the slog bridge surfaces equivalent context via the
// hints array.
func emitCrossWorktreeBanner(ge *guard.Error, stdoutToo bool) {
	expected := ge.ExpectedAgent
	if expected == "" {
		expected = "another agent"
	}
	banner := fmt.Sprintf(
		"⚠ Cross-worktree: you are running this from %s's worktree. "+
			"cd to your own worktree or run 'thrum prime' to re-claim before further commands.",
		expected,
	)
	// Stderr write first, with an explicit Sync so the banner reaches
	// the terminal before any subsequent stdout from the RunE body.
	_, _ = fmt.Fprintln(os.Stderr, banner)
	_ = os.Stderr.Sync()
	if stdoutToo && !flagJSON {
		_, _ = fmt.Fprintln(os.Stdout, banner)
		_ = os.Stdout.Sync()
	}
}
