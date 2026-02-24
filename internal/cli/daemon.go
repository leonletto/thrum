package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/paths"
)

// DaemonStatusResult contains daemon status information.
type DaemonStatusResult struct {
	Running       bool   `json:"running"`
	Status        string `json:"status"`
	PID           int    `json:"pid,omitempty"`
	RepoPath      string `json:"repo_path,omitempty"`
	Uptime        string `json:"uptime,omitempty"`
	Version       string `json:"version,omitempty"`
	SyncState     string `json:"sync_state,omitempty"`
	WebSocketPort int    `json:"ws_port,omitempty"`
}

// DaemonStart starts the daemon in the background.
// When localOnly is true, the --local flag is passed to the daemon subprocess.
func DaemonStart(repoPath string, localOnly bool) error {
	// Convert to absolute path so the daemon knows where to run
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("failed to resolve repo path: %w", err)
	}
	repoPath = absPath

	thrumDir, err := paths.ResolveThrumDir(repoPath)
	if err != nil {
		thrumDir = filepath.Join(repoPath, ".thrum")
	}
	pidPath := filepath.Join(thrumDir, "var", "thrum.pid")
	socketPath := filepath.Join(thrumDir, "var", "thrum.sock")

	// Check if daemon is already running
	running, pidInfo, err := daemon.CheckPIDFileJSON(pidPath)
	if err != nil {
		return fmt.Errorf("failed to check daemon status: %w", err)
	}

	if running {
		// Check if the running daemon serves this repo
		if daemon.ValidatePIDRepo(pidInfo, repoPath) {
			return fmt.Errorf("daemon is already running (PID %d) for repo %s", pidInfo.PID, repoPath)
		}
		// Different repo - warn and proceed
		fmt.Fprintf(os.Stderr, "WARNING: Daemon PID %d is running for different repo %s, proceeding\n",
			pidInfo.PID, pidInfo.RepoPath)
	}

	// Get the path to the current executable
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Build command to start daemon
	args := []string{"daemon", "run", "--repo", repoPath}
	if localOnly {
		args = append(args, "--local")
	}
	cmd := exec.Command(executable, args...) //nolint:gosec // executable from os.Executable(), repoPath validated above

	// Detach from current process - daemon runs independently
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session (detach from terminal)
	}

	// Start daemon process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon process: %w", err)
	}

	// Release the child process so it gets adopted by init/launchd.
	// Do NOT call cmd.Wait() — the parent is about to exit and a goroutine
	// calling Wait() will be killed mid-syscall, leaving the child in an
	// uninterruptible state (UE) on macOS that can't be force-killed.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("failed to release daemon process: %w", err)
	}

	// Wait for socket and ws.port to become available (indicates daemon is ready)
	wsPortPath := filepath.Join(thrumDir, "var", "ws.port")
	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	socketReady := false
	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for daemon to start")
		case <-ticker.C:
			if !socketReady {
				if _, err := os.Stat(socketPath); err == nil {
					socketReady = true
				}
			}
			if socketReady {
				// Also wait for ws.port file so the URL is available
				if _, err := os.Stat(wsPortPath); err == nil {
					return nil
				}
			}
		}
	}
}

// DaemonStop stops the daemon gracefully.
func DaemonStop(repoPath string) error {
	thrumDir, err := paths.ResolveThrumDir(repoPath)
	if err != nil {
		thrumDir = filepath.Join(repoPath, ".thrum")
	}
	pidPath := filepath.Join(thrumDir, "var", "thrum.pid")

	// Check if daemon is running
	running, pidInfo, err := daemon.CheckPIDFileJSON(pidPath)
	if err != nil {
		return fmt.Errorf("failed to check daemon status: %w", err)
	}

	if !running {
		return fmt.Errorf("daemon is not running")
	}

	// Send SIGTERM for graceful shutdown
	process, err := os.FindProcess(pidInfo.PID)
	if err != nil {
		return fmt.Errorf("failed to find process %d: %w", pidInfo.PID, err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM to process %d: %w", pidInfo.PID, err)
	}

	// Wait for daemon to stop (with timeout)
	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			// Timeout - daemon didn't stop gracefully
			return fmt.Errorf("timeout waiting for daemon to stop (PID %d still running)", pidInfo.PID)
		case <-ticker.C:
			// Check if process is still running
			running, _, _ := daemon.CheckPIDFileJSON(pidPath)
			if !running {
				// Daemon stopped successfully
				return nil
			}
		}
	}
}

// DaemonStatus checks the daemon status.
func DaemonStatus(repoPath string) (*DaemonStatusResult, error) {
	thrumDir, err := paths.ResolveThrumDir(repoPath)
	if err != nil {
		thrumDir = filepath.Join(repoPath, ".thrum")
	}
	pidPath := filepath.Join(thrumDir, "var", "thrum.pid")
	socketPath := filepath.Join(thrumDir, "var", "thrum.sock")

	// Check if daemon is running
	running, pidInfo, err := daemon.CheckPIDFileJSON(pidPath)
	if err != nil {
		return nil, fmt.Errorf("failed to check daemon status: %w", err)
	}

	status := "stopped"
	if running {
		status = "running"
	}

	result := &DaemonStatusResult{
		Running:  running,
		Status:   status,
		PID:      pidInfo.PID,
		RepoPath: pidInfo.RepoPath,
	}

	// If daemon is running, try to get additional info via RPC
	if running {
		// Read WebSocket port
		result.WebSocketPort = ReadWebSocketPort(repoPath)

		// Check if socket exists
		if _, err := os.Stat(socketPath); err == nil {
			// Try to connect and get health info
			client, err := NewClient(socketPath)
			if err == nil {
				defer func() { _ = client.Close() }()

				var health HealthResult
				if err := client.Call("health", map[string]any{}, &health); err == nil {
					// Format uptime
					uptime := time.Duration(health.UptimeMs) * time.Millisecond
					result.Uptime = formatDuration(uptime)
					result.Version = health.Version
					result.SyncState = health.SyncState
				}
			}
		}
	}

	return result, nil
}

// DaemonRestart restarts the daemon (stop + start).
// When localOnly is true, the restarted daemon runs in local-only mode.
func DaemonRestart(repoPath string, localOnly bool) error {
	// Read the previous WebSocket port before stopping (DaemonStop deletes ws.port)
	prevPort := ReadWebSocketPort(repoPath)

	// Try to stop daemon (ignore error if not running)
	_ = DaemonStop(repoPath)

	// Wait a bit for cleanup
	time.Sleep(500 * time.Millisecond)

	// Preserve the previous WebSocket port so the UI reconnects to the same URL
	if prevPort > 0 {
		os.Setenv("THRUM_WS_PORT", fmt.Sprintf("%d", prevPort)) //nolint:errcheck
		defer os.Unsetenv("THRUM_WS_PORT")                      //nolint:errcheck
	}

	// Start daemon
	return DaemonStart(repoPath, localOnly)
}

// FormatDaemonStatus formats the daemon status for display.
func FormatDaemonStatus(result *DaemonStatusResult) string {
	if !result.Running {
		return "Daemon:   not running\n"
	}

	status := fmt.Sprintf("Daemon:   running (PID %d)\n", result.PID)
	if result.Uptime != "" {
		status += fmt.Sprintf("Uptime:   %s\n", result.Uptime)
	}
	if result.Version != "" {
		status += fmt.Sprintf("Version:  %s\n", result.Version)
	}
	if result.SyncState != "" {
		if result.SyncState == "synced" {
			status += "Sync:     ✓ synced\n"
		} else {
			status += fmt.Sprintf("Sync:     %s\n", result.SyncState)
		}
	}
	if result.WebSocketPort > 0 {
		status += fmt.Sprintf("UI:       http://localhost:%d\n", result.WebSocketPort)
	}

	return status
}
