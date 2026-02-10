package sync

import (
	"fmt"
	"os"
	"path/filepath"
)

// Lock represents a file lock.
type Lock struct {
	file *os.File
	path string
}

// acquireLock acquires a file lock.
// This creates the lock file if it doesn't exist and uses OS-level file locking.
func acquireLock(lockPath string) (*Lock, error) {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(lockPath), 0750); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}

	// Open or create lock file
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // G304 - path from internal var directory
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	// Try to acquire exclusive lock
	if err := flockExclusive(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquire lock: %w", err)
	}

	return &Lock{
		file: f,
		path: lockPath,
	}, nil
}

// releaseLock releases a file lock.
func releaseLock(lock *Lock) error {
	if lock == nil || lock.file == nil {
		return nil
	}

	// Unlock is automatic when file is closed on Unix systems
	// but we'll call it explicitly for clarity
	if err := flockUnlock(lock.file); err != nil {
		_ = lock.file.Close()
		return fmt.Errorf("unlock: %w", err)
	}

	if err := lock.file.Close(); err != nil {
		return fmt.Errorf("close lock file: %w", err)
	}

	return nil
}
