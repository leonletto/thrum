package peercred

// FindGitRootForTest exposes findGitRoot for external test packages.
// Only compiled during testing.
func FindGitRootForTest(dir string) string {
	return findGitRoot(dir)
}
