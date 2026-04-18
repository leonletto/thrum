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
	"thrum version": true,
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
			leaf.Annotations = make(map[string]string, 1)
		}
		leaf.Annotations[guardCategoryKey] = cat
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
