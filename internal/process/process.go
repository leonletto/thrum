//go:build unix

package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
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

// runPS runs /bin/ps with the given args and a 2-second timeout. All ps
// invocations in this package must go through here so a stuck ps cannot
// hang a CLI command indefinitely.
//
// This wrapper intentionally does NOT import internal/daemon/safecmd:
// internal/process is a low-level utility imported by internal/cli,
// internal/config, and cmd/thrum — none of which depend on
// internal/daemon. Importing the daemon's safecmd would invert the
// dependency direction.
func runPS(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "ps", args...).Output() // #nosec G204
}

// matchRuntimeName compares a ps "comm" string against a known runtime
// binary name after stripping any leading directory components.
//
// On macOS, `ps -o comm=` returns the contents of the kernel's `p_comm`
// field, which is set at exec time. Some binaries (e.g. claude via its
// npm wrapper) record a plain basename; others (e.g. codex via a
// node-spawned native binary) record the full executable path. The
// literal string comparison only matches the first shape, so codex PIDs
// were being classified as "not a runtime" across the codebase.
//
// Filepath.Base is a no-op on already-basename inputs, so this helper
// is safe for every known runtime without a per-runtime branch.
func matchRuntimeName(psComm, runtime string) bool {
	return filepath.Base(psComm) == runtime
}

// IsClaudeProcess checks if the given PID belongs to a Claude process.
func IsClaudeProcess(ctx context.Context, pid int) bool {
	return matchRuntimeName(processName(ctx, pid), "claude")
}

// IsRuntimeProcess checks if the given PID belongs to a process matching
// the specified runtime binary name. If runtime is empty, checks all known runtimes.
func IsRuntimeProcess(ctx context.Context, pid int, runtime string) bool {
	if pid <= 0 {
		return false
	}
	name := processName(ctx, pid)
	if name == "" {
		return false
	}
	if runtime == "" {
		for _, rt := range knownRuntimes {
			if matchRuntimeName(name, rt) {
				return true
			}
		}
		return false
	}
	// Check specific runtime — handle cursor's dual binary names
	if runtime == "cursor" {
		return matchRuntimeName(name, "cursor-agent") || matchRuntimeName(name, "agent")
	}
	return matchRuntimeName(name, runtime)
}

// knownRuntimes lists binary names of known AI coding runtimes.
var knownRuntimes = []string{
	"claude", "opencode", "aider", "codex",
	"cursor-agent", "agent", // agent = Cursor
	"gemini", "auggie", "amp",
}

// runtimeDisplayName maps ambiguous binary names to canonical runtime names.
var runtimeDisplayName = map[string]string{
	"cursor-agent": "cursor",
	"agent":        "cursor",
}

// FindClaudeAncestor walks the process tree from the current process
// up to PID 1, looking for an ancestor matching a known AI coding runtime.
// Returns the PID and runtime name, or (0, "") if not found.
func FindClaudeAncestor(ctx context.Context) (int, string) {
	pid := os.Getppid()
	for pid > 1 {
		name := processName(ctx, pid)
		for _, rt := range knownRuntimes {
			if matchRuntimeName(name, rt) {
				displayName := rt
				if mapped, ok := runtimeDisplayName[rt]; ok {
					displayName = mapped
				}
				return pid, displayName
			}
		}
		pid = parentPID(ctx, pid)
	}
	return 0, ""
}

// processName returns the command name of a process via ps.
func processName(ctx context.Context, pid int) string {
	out, err := runPS(ctx, "-p", fmt.Sprintf("%d", pid), "-o", "comm=")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// parentPID returns the parent PID of a process via ps.
func parentPID(ctx context.Context, pid int) int {
	out, err := runPS(ctx, "-p", fmt.Sprintf("%d", pid), "-o", "ppid=")
	if err != nil {
		return 0
	}
	ppid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return ppid
}
