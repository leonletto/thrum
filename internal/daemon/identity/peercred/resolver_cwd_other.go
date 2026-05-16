//go:build unix && !darwin

package peercred

import (
	"fmt"

	goproc "github.com/shirou/gopsutil/v3/process"
)

// processCWD returns the current working directory of the process with the
// given PID via gopsutil. Works on Linux (and other unix that gopsutil
// supports natively). Darwin has its own implementation in
// resolver_cwd_darwin.go because gopsutil.Cwd() is "not implemented yet" on
// macOS.
func processCWD(pid int) (string, error) {
	p, err := goproc.NewProcess(int32(pid)) //nolint:gosec // pid comes from kernel peer creds, always valid int
	if err != nil {
		return "", fmt.Errorf("gopsutil NewProcess(%d): %w", pid, err)
	}
	cwd, err := p.Cwd()
	if err != nil {
		return "", fmt.Errorf("gopsutil Cwd(%d): %w", pid, err)
	}
	return cwd, nil
}
