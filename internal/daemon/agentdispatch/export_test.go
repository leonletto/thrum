package agentdispatch

import "context"

// SetGitWorktreePruneForTest exposes the BootReconciler.gitWorktreePrune
// field to external test packages. Lets integration tests substitute
// a no-op or recording prune to avoid shelling out against repos
// that don't exist in the test environment.
func SetGitWorktreePruneForTest(r *BootReconciler, fn func(context.Context, string) error) {
	r.gitWorktreePrune = fn
}

// SetPathExistsForTest exposes the BootReconciler.pathExists field
// so integration tests can simulate "worktree exists on disk" without
// actually creating directories. ReconcileRun's row-2 vs row-3
// classification keys on this predicate.
func SetPathExistsForTest(r *BootReconciler, fn func(string) bool) {
	r.pathExists = fn
}
