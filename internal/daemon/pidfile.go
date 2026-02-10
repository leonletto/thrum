package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// PIDInfo contains daemon process metadata stored in the PID file.
type PIDInfo struct {
	PID        int       `json:"pid"`
	RepoPath   string    `json:"repo_path,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	SocketPath string    `json:"socket_path,omitempty"`
}

// WritePIDFileJSON writes process information to the PID file in JSON format.
func WritePIDFileJSON(path string, info PIDInfo) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create PID file directory: %w", err)
	}

	// Marshal PID info to JSON with indentation for readability
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal PID info: %w", err)
	}

	// Write JSON to file
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	return nil
}

// ReadPIDFileJSON reads process information from the PID file.
// Supports both JSON format (new) and plain integer format (old) for backward compatibility.
func ReadPIDFileJSON(path string) (PIDInfo, error) {
	// Read file content
	data, err := os.ReadFile(path) //nolint:gosec // G304 - path from internal var directory
	if err != nil {
		// Return error without wrapping to preserve os.IsNotExist check
		return PIDInfo{}, err
	}

	// Try JSON format first
	var info PIDInfo
	if err := json.Unmarshal(data, &info); err == nil {
		return info, nil
	}

	// Fall back to plain integer format for backward compatibility
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return PIDInfo{}, fmt.Errorf("invalid PID file format: %w", err)
	}

	// Return PIDInfo with just the PID field populated
	return PIDInfo{PID: pid}, nil
}

// CheckPIDFileJSON checks if the PID file exists and if the process is running.
// Returns: (running bool, PIDInfo, error)
// - running: true if process is running, false if stale or doesn't exist
// - PIDInfo: process metadata from the file (PID=0 if file doesn't exist)
// - error: any error reading the file (nil if file doesn't exist).
func CheckPIDFileJSON(path string) (bool, PIDInfo, error) {
	// Read PID info from file
	info, err := ReadPIDFileJSON(path)
	if err != nil {
		// If file doesn't exist, return false with no error
		// This is a normal case (daemon not running)
		if os.IsNotExist(err) {
			return false, PIDInfo{}, nil
		}
		return false, PIDInfo{}, err
	}

	// Check if process is running
	running := isProcessRunning(info.PID)

	return running, info, nil
}

// ValidatePIDRepo checks if the PID file's repo path matches the expected repo path.
// Empty repo paths (legacy PID files) return false — the flock is the arbiter for
// running process detection when repo affinity cannot be confirmed.
func ValidatePIDRepo(info PIDInfo, expectedRepoPath string) bool {
	if info.RepoPath == "" {
		return false
	}
	return filepath.Clean(info.RepoPath) == filepath.Clean(expectedRepoPath)
}

// WritePIDFile writes the current process ID to the specified file.
func WritePIDFile(path string) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create PID file directory: %w", err)
	}

	// Get current PID
	pid := os.Getpid()

	// Write PID to file
	content := fmt.Sprintf("%d\n", pid)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	return nil
}

// ReadPIDFile reads the process ID from the specified file.
func ReadPIDFile(path string) (int, error) {
	// Read file content
	content, err := os.ReadFile(path) //nolint:gosec // G304 - path from internal var directory
	if err != nil {
		// Return error without wrapping to preserve os.IsNotExist check
		return 0, err
	}

	// Parse PID
	pidStr := strings.TrimSpace(string(content))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("invalid PID in file: %s", pidStr)
	}

	return pid, nil
}

// CheckPIDFile checks if the PID file exists and if the process is running
// Returns: (running, pid, error)
// - running: true if process is running, false if stale or doesn't exist
// - pid: the PID from the file (0 if file doesn't exist)
// - error: any error reading the file (nil if file doesn't exist).
func CheckPIDFile(path string) (bool, int, error) {
	// Read PID from file
	pid, err := ReadPIDFile(path)
	if err != nil {
		// If file doesn't exist, return false with no error
		// This is a normal case (daemon not running)
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, err
	}

	// Check if process is running
	running := isProcessRunning(pid)

	return running, pid, nil
}

// RemovePIDFile removes the PID file.
func RemovePIDFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove PID file: %w", err)
	}
	return nil
}

// isProcessRunning checks if a process with the given PID is running.
func isProcessRunning(pid int) bool {
	// Send signal 0 to check if process exists
	// This doesn't actually send a signal, just checks permissions and existence
	process, err := os.FindProcess(pid)
	if err != nil {
		// On Unix, FindProcess always succeeds
		// On Windows, it may fail if process doesn't exist
		return false
	}

	// Try to send signal 0 (null signal) to check if process exists
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		// Process exists and we have permission to signal it
		return true
	}

	// Check error type
	if err == syscall.ESRCH {
		// No such process
		return false
	}

	if err == syscall.EPERM {
		// Process exists but we don't have permission
		// This means process is running
		return true
	}

	// Other error (process finished, etc.)
	// Assume process is not running
	return false
}
