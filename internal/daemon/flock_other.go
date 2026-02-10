//go:build !unix

package daemon

// AcquireLock is a no-op on non-unix platforms.
// File locking for SIGKILL resilience is only supported on unix.
func AcquireLock(path string) (*FileLock, error) {
	return nil, nil
}

// Release is a no-op on non-unix platforms.
func (l *FileLock) Release() error {
	return nil
}

// IsLocked always returns false on non-unix platforms.
func IsLocked(path string) bool {
	return false
}
