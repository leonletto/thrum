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

// RuntimeName returns the canonical runtime name for a given PID
// (e.g. "claude", "codex", "cursor", "opencode"), or "" if the process
// is not a known runtime. Canonicalization applies runtimeDisplayName
// so both "cursor-agent" and "agent" collapse to "cursor", and
// ".opencode" collapses to "opencode".
func RuntimeName(ctx context.Context, pid int) string {
	if pid <= 0 {
		return ""
	}
	name := processName(ctx, pid)
	if name == "" {
		return ""
	}
	for _, rt := range knownRuntimes {
		if matchRuntimeName(name, rt) {
			if mapped, ok := runtimeDisplayName[rt]; ok {
				return mapped
			}
			return rt
		}
	}
	return ""
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
	// Check specific runtime — handle dual binary names where the npm shim
	// and the real binary differ (cursor, opencode).
	if runtime == "cursor" {
		return matchRuntimeName(name, "cursor-agent") || matchRuntimeName(name, "agent")
	}
	if runtime == "opencode" {
		return matchRuntimeName(name, "opencode") || matchRuntimeName(name, ".opencode")
	}
	return matchRuntimeName(name, runtime)
}

// knownRuntimes lists binary names of known AI coding runtimes.
//
// Some runtimes install both a user-facing command and a dot-prefixed shim
// binary that actually runs in the process tree (e.g. opencode's
// `.../opencode-ai/bin/.opencode`, cursor's `cursor-agent`/`agent`). Both
// names must appear here for ancestor detection to find them; see
// runtimeDisplayName for alias-to-canonical mapping.
var knownRuntimes = []string{
	"claude", "opencode", ".opencode", "aider", "codex",
	"cursor-agent", "agent", // agent = Cursor
	"gemini", "auggie", "amp",
	"kiro-cli", // Amazon Kiro CLI; canonical display name "kiro"
}

// runtimeDisplayName maps ambiguous binary names to canonical runtime names.
var runtimeDisplayName = map[string]string{
	"cursor-agent": "cursor",
	"agent":        "cursor",
	".opencode":    "opencode",
	"kiro-cli":     "kiro",
}

// FindClaudeAncestor walks the process tree from the current process
// up to PID 1, looking for an ancestor matching a known AI coding runtime.
// Returns the PID and runtime name, or (0, "") if not found.
//
// thrum-xir.40: returns the TOPMOST matching ancestor (not the first one
// encountered on the way up). A runtime like Claude may spawn transient
// sub-process helpers — e.g. the claude-sdk subprocess that runs
// SessionStart hooks for episodic-memory and similar plugins — and those
// helpers also identify as "claude" in `ps -o comm=`. Returning the first
// match means `quickstart` would bind agent_pid to a short-lived helper;
// later invocations from the long-lived runtime main then fail the
// ancestor walk-up (their walks never reach the dead helper PID) and the
// identity guard refuses every RPC with pid_mismatch. The long-lived
// session main is always the highest claude-named process in the chain,
// so the topmost match is the stable PID to bind to.
func FindClaudeAncestor(ctx context.Context) (int, string) {
	return findAncestorTopmost(ctx, os.Getppid(), processName, parentPID)
}

// findAncestorTopmost is the testable core of FindClaudeAncestor. It walks
// from startPID up to PID 1 via parentFn, naming each via nameFn, and
// returns the topmost process matching a known runtime. Returns (0, "")
// if no runtime ancestor exists on the chain.
func findAncestorTopmost(
	ctx context.Context,
	startPID int,
	nameFn func(context.Context, int) string,
	parentFn func(context.Context, int) int,
) (int, string) {
	pid := startPID
	topPID := 0
	topRuntime := ""
	for pid > 1 {
		name := nameFn(ctx, pid)
		for _, rt := range knownRuntimes {
			if matchRuntimeName(name, rt) {
				displayName := rt
				if mapped, ok := runtimeDisplayName[rt]; ok {
					displayName = mapped
				}
				topPID = pid
				topRuntime = displayName
				break
			}
		}
		pid = parentFn(ctx, pid)
	}
	return topPID, topRuntime
}

// processName returns the command name of a process via ps.
func processName(ctx context.Context, pid int) string {
	out, err := runPS(ctx, "-p", fmt.Sprintf("%d", pid), "-o", "comm=")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ParentPID returns the parent PID of the given process. Errors from ps
// (dead process, timeout, platform unsupported) are surfaced so callers
// walking an ancestor chain can distinguish "reached init (ppid=1)" from
// "lookup failed.".
func ParentPID(ctx context.Context, pid int) (int, error) {
	out, err := runPS(ctx, "-p", fmt.Sprintf("%d", pid), "-o", "ppid=")
	if err != nil {
		return 0, err
	}
	ppid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse ppid: %w", err)
	}
	return ppid, nil
}

// parentPID is the legacy, error-swallowing variant retained for
// callers that treat lookup failures as "give up and return 0.".
func parentPID(ctx context.Context, pid int) int {
	ppid, err := ParentPID(ctx, pid)
	if err != nil {
		return 0
	}
	return ppid
}
