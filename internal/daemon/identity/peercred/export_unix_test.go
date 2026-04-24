//go:build unix

package peercred

// SetProcessCWDFnForTest replaces the processCWD function used by Resolve
// with fn, returning a restore closure. Tests use this to force gopsutil
// failures and verify the resolver's error-classification contract
// (see thrum-ndtw).
func SetProcessCWDFnForTest(fn func(int) (string, error)) func() {
	prev := processCWDFn
	processCWDFn = fn
	return func() { processCWDFn = prev }
}
