package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestEveryLeafHasGuardCategory is the audit-enforcement test: it
// builds the full cobra tree via buildRootCmd, walks every leaf, and
// fails if any leaf is missing an Annotations["guard_category"]
// entry or carries one outside the allowed vocabulary (bypass /
// prime-path / guarded). Adding a new cobra command without
// classifying it — either by extending primePathLeaves /
// bypassLeaves in command_categories.go, or by leaving it to the
// default-guarded fall-through — is caught here.
//
// The failure message points directly at the command path + tells
// the maintainer how to fix, so future readers don't have to
// reverse-engineer the audit machinery to unblock a build.
func TestEveryLeafHasGuardCategory(t *testing.T) {
	valid := map[string]bool{
		GuardCategoryBypass:  true,
		GuardCategoryPrime:   true,
		GuardCategoryGuarded: true,
	}

	root := buildRootCmd()
	var missing, wrong []string

	walkLeaves(root, func(leaf *cobra.Command) {
		path := leaf.CommandPath()
		cat, ok := leaf.Annotations[guardCategoryKey]
		if !ok || cat == "" {
			missing = append(missing, path)
			return
		}
		if !valid[cat] {
			wrong = append(wrong, path+" (got "+cat+")")
		}
	})

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("missing guard_category annotation on %d leaf command(s):\n  %s\n\n"+
			"Fix: add the command path to primePathLeaves or bypassLeaves in "+
			"cmd/thrum/command_categories.go, or accept the default-guarded "+
			"classification by ensuring tagGuardCategories is called on the "+
			"full tree in buildRootCmd.",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(wrong) > 0 {
		sort.Strings(wrong)
		t.Errorf("leaves carry unknown guard_category (must be bypass / prime-path / guarded):\n  %s",
			strings.Join(wrong, "\n  "))
	}
}

// TestPrimePathLeavesAreCovered guards against the prime-path list
// drifting out of sync with the actual commands — a typo in
// primePathLeaves would silently classify 'thrum init' as guarded
// (incorrect taxonomy, but the audit test above would still pass
// because guarded IS a valid category). Explicit existence check on
// each prime-path entry catches this class of drift.
func TestPrimePathLeavesAreCovered(t *testing.T) {
	root := buildRootCmd()
	seen := map[string]bool{}
	walkLeaves(root, func(leaf *cobra.Command) {
		seen[leaf.CommandPath()] = true
	})
	for path := range primePathLeaves {
		if !seen[path] {
			t.Errorf("primePathLeaves references %q but that path is not a leaf in the cobra tree "+
				"(typo, rename, or stale entry — remove or fix in command_categories.go)", path)
		}
	}
	for path := range bypassLeaves {
		if !seen[path] {
			t.Errorf("bypassLeaves references %q but that path is not a leaf in the cobra tree "+
				"(typo, rename, or stale entry — remove or fix in command_categories.go)", path)
		}
	}
}

// TestEveryLeafHasCrossWorktreeResponse pins the thrum-7b84.6 Enhanced
// Policy 2 invariant: every cobra leaf must carry a
// cross_worktree_response annotation drawn from the abort /
// diagnostic_banner / whoami vocabulary. The default-fallthrough
// classification is abort (safe — fail closed on guard fire), so any
// new leaf added without explicit classification still produces a
// defensible runtime response. This test guards against the
// classification being silently dropped (e.g., if tagGuardCategories
// is refactored and a branch forgets the new annotation).
func TestEveryLeafHasCrossWorktreeResponse(t *testing.T) {
	valid := map[string]bool{
		CrossWorktreeResponseAbort:            true,
		CrossWorktreeResponseDiagnosticBanner: true,
		CrossWorktreeResponseWhoami:           true,
	}

	root := buildRootCmd()
	var missing, wrong []string

	walkLeaves(root, func(leaf *cobra.Command) {
		path := leaf.CommandPath()
		resp, ok := leaf.Annotations[crossWorktreeResponseKey]
		if !ok || resp == "" {
			missing = append(missing, path)
			return
		}
		if !valid[resp] {
			wrong = append(wrong, path+" (got "+resp+")")
		}
	})

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("missing cross_worktree_response annotation on %d leaf command(s):\n  %s\n\n"+
			"Fix: ensure tagGuardCategories in cmd/thrum/command_categories.go sets "+
			"the annotation on every leaf. The default is "+CrossWorktreeResponseAbort+
			"; add to diagnosticBannerLeaves or whoamiLeaves to override.",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(wrong) > 0 {
		sort.Strings(wrong)
		t.Errorf("leaves carry unknown cross_worktree_response (must be %s / %s / %s):\n  %s",
			CrossWorktreeResponseAbort, CrossWorktreeResponseDiagnosticBanner, CrossWorktreeResponseWhoami,
			strings.Join(wrong, "\n  "))
	}
}

// TestCrossWorktreeBannerLeavesAreCovered mirrors TestPrimePathLeaves
// Are Covered: a typo in diagnosticBannerLeaves / whoamiLeaves would
// silently classify the leaf as abort (a valid response but the wrong
// taxonomy). Explicit existence check on each banner-class entry.
func TestCrossWorktreeBannerLeavesAreCovered(t *testing.T) {
	root := buildRootCmd()
	seen := map[string]bool{}
	walkLeaves(root, func(leaf *cobra.Command) {
		seen[leaf.CommandPath()] = true
	})
	for path := range diagnosticBannerLeaves {
		if !seen[path] {
			t.Errorf("diagnosticBannerLeaves references %q but that path is not a leaf in the cobra tree "+
				"(typo, rename, or stale entry — remove or fix in command_categories.go)", path)
		}
	}
	for path := range whoamiLeaves {
		if !seen[path] {
			t.Errorf("whoamiLeaves references %q but that path is not a leaf in the cobra tree "+
				"(typo, rename, or stale entry — remove or fix in command_categories.go)", path)
		}
	}
}
