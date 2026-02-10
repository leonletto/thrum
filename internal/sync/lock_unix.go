//go:build unix || darwin || linux

package sync

import (
	"errors"
	"os"
	"syscall"
)

var errLocked = errors.New("lock is held by another process")

// flockExclusive acquires an exclusive file lock using flock.
func flockExclusive(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == syscall.EWOULDBLOCK {
		return errLocked
	}
	return err
}

// flockUnlock releases a file lock.
func flockUnlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
