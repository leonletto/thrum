//go:build unix

package peercred

// SetProcessCWDFnForTest replaces the processCWD function used by Resolve
// with fn, returning a restore closure. Tests use this to force gopsutil
// failures and verify the resolver's error-classification contract
// (see thrum-ndtw).
//
// thrum-xir.45: clears the per-PID CWD cache on both swap and restore.
// Changing the backing function semantically invalidates any cached
// results — otherwise a test that primes the cache with a valid CWD
// would shadow a subsequent test's forced-error injection.
func SetProcessCWDFnForTest(fn func(int) (string, error)) func() {
	prev := processCWDFn
	processCWDFn = fn
	clearCWDCacheForTest()
	return func() {
		processCWDFn = prev
		clearCWDCacheForTest()
	}
}
