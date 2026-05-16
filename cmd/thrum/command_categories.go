package main

import "github.com/spf13/cobra"

// Guard category vocabulary. Every leaf cobra.Command under rootCmd
// must carry one of these values under Annotations["guard_category"].
// The test in command_categories_test.go walks the tree in-test and
// fails if a leaf is missing an assignment — so adding a new cobra
// command without classifying it is a CI failure, not a silent regression.
const (
	// GuardCategoryBypass marks commands that do NOT touch identity
	// state or require a daemon connection: help, version, shell
	// completion generation. Safe to run before quickstart; safe to
	// run in a non-git directory; safe when the daemon is not running.
	GuardCategoryBypass = "bypass"

	// GuardCategoryPrime marks commands that CREATE or REFRESH
	// identity: init, prime, quickstart. These are the only callers
	// allowed to write AgentPID into an identity file (via
	// guard.WritePID), and they're the bootstrap paths where G2/G1a/
	// G1b/G5 fire.
	GuardCategoryPrime = "prime-path"

	// GuardCategoryGuarded marks every other command — the standard
	// RPC verbs that call getClient() and thus transitively flow
	// through guard.Check (Rule #4‴ + companion guards). The vast
	// majority of commands fall here.
	GuardCategoryGuarded = "guarded"

	// The Annotations map key used by the audit across the codebase.
	// Kept as an unexported constant so typos fail at compile time.
	guardCategoryKey = "guard_category"
)

// Cross-worktree response classes (thrum-7b84.6 Enhanced Policy 2).
//
// Orthogonal axis to GuardCategory*: the old axis governs WHO calls
// guard.Check (Bypass/Prime/Guarded — plumbing); this axis governs
// WHAT to do when guard.Check fires under strict-mode cross_worktree.
// Only consulted by getClient() after RefreshLocalIdentity returns a
// *guard.Error for the cross_worktree guard, so the annotation is
// inert for Bypass/Prime leaves (kept on them for audit consistency).
const (
	// CrossWorktreeResponseAbort fails closed: getClient returns
	// (nil, refreshErr) so the command exits non-zero with the
	// existing 4-line guard error block on stderr and an empty
	// stdout. The safe default for mutating + identity-filtered
	// verbs where a wrong-identity write is permanent and
	// ~undetectable.
	CrossWorktreeResponseAbort = "abort"

	// CrossWorktreeResponseDiagnosticBanner allows the command to
	// proceed but emits a one-line stderr banner BEFORE any stdout
	// write. For identity-agnostic diagnostic verbs (team, daemon
	// *, agent list, version) where the operator wants the output
	// even though they're in the wrong worktree.
	CrossWorktreeResponseDiagnosticBanner = "diagnostic_banner"

	// CrossWorktreeResponseWhoami allows the command to proceed
	// and emits the banner on BOTH stdout (prepended to the
	// identity block) AND stderr. Specific to whoami — the
	// caller's stdout consumer is reading "who am I" and needs to
	// see the cross-worktree context inline, not just in stderr.
	CrossWorktreeResponseWhoami = "whoami"

	// crossWorktreeResponseKey is the Annotations map key for the
	// cross-worktree response class. Separate from guardCategoryKey
	// so the two axes evolve independently.
	crossWorktreeResponseKey = "cross_worktree_response"
)

// diagnosticBannerLeaves enumerates leaves that get
// CrossWorktreeResponseDiagnosticBanner: identity-agnostic diagnostic
// verbs whose output is useful even under a cross_worktree fire. Class
// B per the Enhanced Policy 2 spec (thrum-7b84.6).
//
// The status-verb siblings (peer/sync/backup/telegram/tmux status)
// were ratified by @researcher_inbox_race in third-pass review: the
// dispatch's "status" was ambiguous; all identity-agnostic
// daemon/system status reporters belong in Class B since none filter
// by caller identity.
var diagnosticBannerLeaves = map[string]bool{
	"thrum team":            true,
	"thrum agent list":      true,
	"thrum version":         true,
	"thrum daemon logs":     true,
	"thrum daemon restart":  true,
	"thrum daemon run":      true,
	"thrum daemon start":    true,
	"thrum daemon status":   true,
	"thrum daemon stop":     true,
	"thrum peer status":     true,
	"thrum sync status":     true,
	"thrum backup status":   true,
	"thrum telegram status": true,
	"thrum tmux status":     true,
}

// whoamiLeaves enumerates leaves that get CrossWorktreeResponseWhoami:
// commands whose stdout asserts identity and therefore needs the
// banner prepended inline. Class C per the Enhanced Policy 2 spec
// (thrum-7b84.6). Both the top-level `thrum whoami` and the
// `thrum agent whoami` alias qualify.
var whoamiLeaves = map[string]bool{
	"thrum whoami":       true,
	"thrum agent whoami": true,
}

// primePathLeaves is the exhaustive list of leaf-command paths (the
// string returned by cmd.CommandPath()) that create or refresh
// identity files. Any leaf path appearing here gets
// guard_category=prime-path; anything not in this or the bypass list
// defaults to guarded. Spec §Command Inventory Audit defines the
// taxonomy.
var primePathLeaves = map[string]bool{
	"thrum init":       true,
	"thrum quickstart": true,
	"thrum prime":      true,
}

// bypassLeaves is the exhaustive list of leaf-command paths that do
// not touch identity state or require a daemon connection. Must stay
// small; see the godoc on GuardCategoryBypass for the rationale.
// Note: cobra adds "help" and "completion" subcommands automatically;
// their presence in the tree is handled in tagGuardCategories by
// name-match rather than a full path key so upstream cobra renames
// do not break the taxonomy.
var bypassLeaves = map[string]bool{
	"thrum cron install-inbox-poll": true, // print-only, no daemon I/O
	"thrum version":                 true,
}

// tagGuardCategories walks the entire tree rooted at root and
// assigns guard_category annotations to every leaf command. Parent
// commands (those with subcommands) are not tagged — the test only
// enforces the taxonomy on leaves because invoking a parent without
// a subcommand prints usage and never reaches guard.Check.
//
// Categorization rules:
//  1. cobra's auto-generated `help` and `completion` commands
//     (and their subtrees) are bypass — they print docs, never
//     connect to the daemon.
//  2. leaves with a path in primePathLeaves → prime-path.
//  3. leaves with a path in bypassLeaves → bypass.
//  4. everything else → guarded.
//
// The map lookups make new commands default to the safe choice
// (guarded); bypass and prime-path require an explicit entry, which
// is the footgun closure the audit wants.
func tagGuardCategories(root *cobra.Command) {
	walkLeaves(root, func(leaf *cobra.Command) {
		path := leaf.CommandPath()
		cat := GuardCategoryGuarded
		switch {
		case isHelpOrCompletion(leaf):
			cat = GuardCategoryBypass
		case primePathLeaves[path]:
			cat = GuardCategoryPrime
		case bypassLeaves[path]:
			cat = GuardCategoryBypass
		}
		if leaf.Annotations == nil {
			leaf.Annotations = make(map[string]string, 2)
		}
		leaf.Annotations[guardCategoryKey] = cat

		// thrum-7b84.6: cross-worktree response axis. Defaults to
		// abort (safe — fail closed on guard fire); diagnostic and
		// whoami leaves are pulled from explicit maps. Help and
		// completion don't connect to the daemon at all, so the
		// classification is moot but kept consistent with abort.
		resp := CrossWorktreeResponseAbort
		switch {
		case whoamiLeaves[path]:
			resp = CrossWorktreeResponseWhoami
		case diagnosticBannerLeaves[path]:
			resp = CrossWorktreeResponseDiagnosticBanner
		}
		leaf.Annotations[crossWorktreeResponseKey] = resp
	})
}

// walkLeaves visits every leaf command in the subtree rooted at cmd.
// A leaf is a command with no subcommands. Cobra's auto-added `help`
// and `completion` commands count as leaves once the tree is built.
func walkLeaves(cmd *cobra.Command, fn func(*cobra.Command)) {
	subs := cmd.Commands()
	if len(subs) == 0 {
		fn(cmd)
		return
	}
	for _, sub := range subs {
		walkLeaves(sub, fn)
	}
}

// isHelpOrCompletion identifies the commands cobra injects for help
// and shell completion. Both are identity-agnostic by nature and
// must not regress the audit if cobra adds a new auto-subcommand
// under either parent.
func isHelpOrCompletion(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "help", "completion":
			return true
		}
	}
	return false
}
