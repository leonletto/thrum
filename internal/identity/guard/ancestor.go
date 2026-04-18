package guard

import (
	"context"
	"fmt"
	"slices"

	"github.com/leonletto/thrum/internal/process"
)

// maxAncestorDepth caps the ancestor walk to defend against corrupted
// process tables or (hypothetical) ppid cycles. A real depth past a few
// dozen would indicate a pathological environment, not legitimate state.
const maxAncestorDepth = 64

// WalkAncestors returns the PID chain starting at startPID and walking
// up via ParentPID until pid 1 (init) is reached, lookup fails, or the
// safety cap is hit. The returned slice is [self, parent, grandparent,
// …, 1] (or truncated at the failure point). A non-nil error indicates
// the walk terminated early; the partial chain collected so far is still
// returned for diagnostic logging.
func WalkAncestors(ctx context.Context, startPID int) ([]int, error) {
	chain := []int{startPID}
	cur := startPID
	for range maxAncestorDepth {
		if cur <= 1 {
			break
		}
		parent, err := process.ParentPID(ctx, cur)
		if err != nil {
			return chain, fmt.Errorf("parent of %d: %w", cur, err)
		}
		if parent == 0 || parent == cur {
			break
		}
		chain = append(chain, parent)
		cur = parent
	}
	return chain, nil
}

// isRuntimeProcessFn and runtimeNameFn are test seams. Production code
// delegates to internal/process; tests may swap them to simulate an
// arbitrary process tree without actually spawning runtimes.
//
// WARNING: these are package-level mutable vars. Tests that swap them
// MUST NOT call t.Parallel() anywhere in the guard package — a
// concurrent test could observe a stubbed function intended for a
// different case. Use t.Cleanup() to restore originals.
var (
	isRuntimeProcessFn = process.IsRuntimeProcess
	runtimeNameFn      = process.RuntimeName
)

// ClosestRuntimeAncestor walks ancestors from startPID and returns the
// first PID that is a recognized AI-coding runtime (claude, codex,
// cursor, opencode, …) along with the canonical runtime name. Returns
// (0, "", nil) if no runtime ancestor is found — "no runtime" is a
// legitimate environmental state, not an error.
//
// If WalkAncestors fails before collecting any PIDs, the walk error is
// returned. A partial chain is still searched, so an intermittent ps
// failure deep in the tree does not mask a runtime match closer to the
// caller.
func ClosestRuntimeAncestor(ctx context.Context, startPID int) (int, string, error) {
	chain, err := WalkAncestors(ctx, startPID)
	if err != nil && len(chain) == 0 {
		return 0, "", err
	}
	for _, pid := range chain {
		if isRuntimeProcessFn(ctx, pid, "") {
			return pid, runtimeNameFn(ctx, pid), nil
		}
	}
	return 0, "", nil
}

// ChainContains reports whether pid appears anywhere in chain.
func ChainContains(chain []int, pid int) bool {
	return slices.Contains(chain, pid)
}
