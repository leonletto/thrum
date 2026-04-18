//go:build unix

package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// AtomicWrite writes data to path atomically and race-free across
// concurrent writers. It acquires an exclusive fcntl advisory lock on
// the parent directory for the duration of the write, then emits the
// new content via a tmpfile + rename so an interrupted write can never
// leave a partially populated identity file on disk.
//
// Callers must ensure the parent directory of path exists; AtomicWrite
// will not create it. The lock is advisory (fcntl), so only cooperating
// processes using this function or a compatible locking discipline are
// serialized — which is the design: inside thrum every identity-file
// writer routes through here.
func AtomicWrite(path string, data []byte) (retErr error) {
	dir := filepath.Dir(path)
	lock, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open dir for lock: %w", err)
	}
	defer func() {
		if cerr := lock.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("close lock dir: %w", cerr)
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock exclusive: %w", err)
	}
	defer func() {
		if uerr := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); uerr != nil && retErr == nil {
			retErr = fmt.Errorf("flock unlock: %w", uerr)
		}
	}()

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

