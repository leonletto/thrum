package daemon

import "os"

// FileLock holds an exclusive file lock that auto-releases on process death.
// The OS releases the lock automatically when the process exits (even SIGKILL).
type FileLock struct {
	path string
	file *os.File
}

// LockPath returns the path to the lock file.
func (l *FileLock) LockPath() string {
	return l.path
}
