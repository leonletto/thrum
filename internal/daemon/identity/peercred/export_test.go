package peercred

// FindGitRootForTest exposes findGitRoot for external test packages.
// Only compiled during testing.
func FindGitRootForTest(dir string) string {
	return findGitRoot(dir)
}

// MatchWorktreeForTest exposes matchWorktree for external test packages.
// Only compiled during testing. thrum-g1ux uses this to assert the
// IsNotExist→Debug downgrade without standing up a full unix-socket
// round-trip.
func MatchWorktreeForTest(candidate string, agents []AgentWorktree) (*AgentWorktree, error) {
	return matchWorktree(candidate, agents)
}
