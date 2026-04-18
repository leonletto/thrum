package main

import (
	"sort"
	"testing"

	"github.com/spf13/cobra"
)

// TestCommandInventory dumps the full leaf-path / guard-category
// mapping under -v. Useful for updating dev-docs/plans/command-inventory.md
// during the audit and for sanity-checking new commands at review time.
// Always passes — this is a catalog, not an assertion.
func TestCommandInventory(t *testing.T) {
	if !testing.Verbose() {
		t.Skip("set -v to dump command inventory")
	}
	root := buildRootCmd()
	type entry struct{ path, cat string }
	var leaves []entry
	walkLeaves(root, func(leaf *cobra.Command) {
		leaves = append(leaves, entry{
			path: leaf.CommandPath(),
			cat:  leaf.Annotations[guardCategoryKey],
		})
	})
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].path < leaves[j].path })
	t.Logf("thrum command inventory (%d leaves):", len(leaves))
	for _, l := range leaves {
		t.Logf("  %-60s %s", l.path, l.cat)
	}
}
