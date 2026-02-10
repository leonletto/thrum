//go:build windows

package sync

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var errLocked = errors.New("lock is held by another process")

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx = kernel32.NewProc("LockFileEx")
	procUnlockFile = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock   = 0x2
	lockfileFailImmediately = 0x1
)

// flockExclusive acquires an exclusive file lock using Windows LockFileEx.
func flockExclusive(f *os.File) error {
	var overlapped syscall.Overlapped

	// Try to lock (non-blocking)
	r1, _, err := procLockFileEx.Call(
		uintptr(f.Fd()),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		uintptr(0),
		uintptr(1),
		uintptr(0),
		uintptr(unsafe.Pointer(&overlapped)),
	)

	if r1 == 0 {
		if err == syscall.ERROR_LOCK_VIOLATION {
			return errLocked
		}
		return err
	}

	return nil
}

// flockUnlock releases a file lock using Windows UnlockFileEx.
func flockUnlock(f *os.File) error {
	var overlapped syscall.Overlapped

	r1, _, err := procUnlockFile.Call(
		uintptr(f.Fd()),
		uintptr(0),
		uintptr(1),
		uintptr(0),
		uintptr(unsafe.Pointer(&overlapped)),
	)

	if r1 == 0 {
		return err
	}

	return nil
}
