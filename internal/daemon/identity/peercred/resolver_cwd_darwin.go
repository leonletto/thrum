//go:build darwin

package peercred

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// processCWD returns the current working directory of the process with the
// given PID on macOS.
//
// Implementation: shells out to `/usr/sbin/lsof -p PID -Fn -d cwd` and parses
// the structured `-F` output for the `n` field. Slow path (~20-40ms per call)
// but reliable: lsof is a system tool always present on macOS and the -F
// output format is stable. The hot pre-rc.5 macOS path was effectively broken
// because gopsutil's Cwd() is documented as "not implemented yet" on Darwin —
// every call returned an error, the daemon fell through to legacy
// client-asserted identity, and stale `THRUM_AGENT_ID` env vars silently
// overrode cwd-based identity (long-standing footgun; see thrum-2t7d for the
// full history). A correctly-working lsof path is strictly better than a
// silent no-op even at 30ms.
//
// v0.10.4 candidate: replace with native libproc `proc_pidinfo`
// (PROC_PIDVNODEPATHINFO) via either (a) pure-Go syscall.Syscall6 once the
// proc_vnodepathinfo struct layout is locked down, or (b) cgo with goreleaser
// switched to per-OS CGO_ENABLED settings + matrix-built darwin runners.
//
// Linux and other unix use gopsutil directly in resolver_cwd_other.go;
// gopsutil works natively there so no subprocess is needed.
func processCWD(pid int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// -F n      structured output, field "n" carries the path
	// -d cwd    only the cwd file descriptor entry
	// -a        intersect filters (PID AND cwd-fd)
	// -p PID    target process
	// /usr/sbin/lsof is the system path; not subject to PATH manipulation.
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-Fn", "-d", "cwd", "-a", "-p", fmt.Sprintf("%d", pid)) //nolint:gosec // pid comes from kernel peer creds
	cmd.Env = []string{}                                                                                      // no env inheritance — lsof doesn't need any

	out, err := cmd.Output()
	if err != nil {
		// Non-zero exit may mean the PID doesn't exist or we lack permission
		// (cross-user, sandboxed, etc.). Either way, we can't read cwd.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("lsof pid=%d exit=%d: %w", pid, exitErr.ExitCode(), err)
		}
		return "", fmt.Errorf("lsof pid=%d: %w", pid, err)
	}

	// lsof -F output is line-oriented; each line starts with a 1-char field
	// identifier. We want lines starting with 'n' (the name/path).
	// Example output (for our purposes):
	//   p70003
	//   fcwd
	//   n/Users/leon/dev/falcondev/falcon-agent
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 2 || line[0] != 'n' {
			continue
		}
		path := line[1:]
		if path == "" {
			continue
		}
		return path, nil
	}

	return "", fmt.Errorf("lsof pid=%d: no cwd field in output", pid)
}
