//go:build unix

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// AcquireLock tries to get an exclusive non-blocking lock on the lock file.
// Returns error if lock is held by another process.
// The lock is automatically released by the OS when the process dies (even SIGKILL).
func AcquireLock(path string) (*FileLock, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create lock file directory: %w", err)
	}

	// Open (or create) the lock file
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600) // #nosec G304 -- path is an internal .thrum/var lock file path
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file: %w", err)
	}

	// Try to acquire exclusive lock (non-blocking)
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { // #nosec G115 -- file descriptors are small non-negative integers; uintptr->int conversion cannot overflow
		_ = f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, fmt.Errorf("daemon lock held by another process")
		}
		return nil, fmt.Errorf("failed to acquire lock: %w", err)
	}

	return &FileLock{path: path, file: f}, nil
}

// Release releases the lock and removes the lock file.
// Safe to call multiple times — subsequent calls are no-ops.
func (l *FileLock) Release() error {
	if l.file == nil {
		return nil
	}
	// Capture and nil before operations to prevent double-release on reused fd
	f := l.file
	l.file = nil
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) // #nosec G115 -- file descriptors are small non-negative integers; uintptr->int conversion cannot overflow
	err := f.Close()
	_ = os.Remove(l.path)
	return err
}

// IsLocked checks if the lock file is currently held by another process.
func IsLocked(path string) bool {
	f, err := os.OpenFile(path, os.O_RDONLY, 0) // #nosec G304 -- path is an internal .thrum/var lock file path
	if err != nil {
		// File doesn't exist or can't be opened - not locked
		return false
	}
	defer func() { _ = f.Close() }()

	// Try to acquire lock (non-blocking)
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) // #nosec G115 -- file descriptors are small non-negative integers; uintptr->int conversion cannot overflow
	if err != nil {
		// Lock is held by another process
		return true
	}

	// We got the lock - release it and return false
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) // #nosec G115 -- file descriptors are small non-negative integers; uintptr->int conversion cannot overflow
	return false
}
