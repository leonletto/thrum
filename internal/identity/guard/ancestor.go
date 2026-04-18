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

// ChainContains reports whether pid appears anywhere in chain.
func ChainContains(chain []int, pid int) bool {
	return slices.Contains(chain, pid)
}
