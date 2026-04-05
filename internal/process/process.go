//go:build unix

package process

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// IsRunning checks if a process with the given PID is alive.
// Uses kill -0 (null signal) to probe without affecting the process.
func IsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if err == syscall.EPERM {
		return true // exists but no permission
	}
	return false // ESRCH or other error = not running
}

// IsClaudeProcess checks if the given PID belongs to a Claude process.
func IsClaudeProcess(pid int) bool {
	return processName(pid) == "claude"
}

// FindClaudeAncestor walks the process tree from the current process
// up to PID 1, looking for an ancestor named "claude". Returns the
// PID of the Claude process, or 0 if not running inside Claude.
func FindClaudeAncestor() int {
	pid := os.Getppid()
	for pid > 1 {
		if processName(pid) == "claude" {
			return pid
		}
		pid = parentPID(pid)
	}
	return 0
}

// processName returns the command name of a process via ps.
func processName(pid int) string {
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "comm=").Output() // #nosec G204
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// parentPID returns the parent PID of a process via ps.
func parentPID(pid int) int {
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "ppid=").Output() // #nosec G204
	if err != nil {
		return 0
	}
	ppid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return ppid
}
