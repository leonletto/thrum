package guard

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/process"
)

// ClearPIDIfDead checks the stored agent_pid in an identity file for
// liveness and clears it (via WritePID(path, 0)) when the process is not
// running. Returns cleared=true when a clear happened, cleared=false when
// the stored PID is already 0 or still alive.
//
// Defensive self-heal for identity files whose last-written PID belongs
// to a process that has since exited — the canonical case is
// `thrum tmux create`'s inline quickstart subshell whose PID is persisted
// and then immediately exits when the subshell returns. Without this
// clear, HandleLaunch's writeTmuxToIdentity Pass 0 trips the G4 strict
// writer-liveness check on the dead PID and the tmux_session field never
// gets written.
//
// On cleared=true the function emits a slog.Warn that surfaces as a hint
// code via the slog-hint bridge.
func ClearPIDIfDead(path string) (cleared bool, err error) {
	b, err := os.ReadFile(path) // #nosec G304 -- path is .thrum/identities/<name>.json, an internal identity file
	if err != nil {
		return false, fmt.Errorf("read identity %s: %w", path, err)
	}
	var id config.IdentityFile
	if err := json.Unmarshal(b, &id); err != nil {
		return false, fmt.Errorf("unmarshal identity %s: %w", path, err)
	}
	if id.AgentPID == 0 {
		return false, nil
	}
	if process.IsRunning(id.AgentPID) {
		return false, nil
	}
	if err := WritePID(path, 0); err != nil {
		return false, fmt.Errorf("write cleared pid to %s: %w", path, err)
	}
	slog.Warn("tmux.launch.stale-pid-cleared", slog.String("path", path))
	return true, nil
}
