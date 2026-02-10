package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultWSPortFile is the default filename for the WebSocket port file.
const DefaultWSPortFile = "ws.port"

// WSPortFilePath returns the standard path for the WebSocket port file.
// The port file is stored in .thrum/var/ws.port.
func WSPortFilePath(repoPath string) string {
	return filepath.Join(repoPath, ".thrum", "var", DefaultWSPortFile)
}

// DefaultPortRangeMin is the default minimum port for WebSocket server.
const DefaultPortRangeMin = 9000

// DefaultPortRangeMax is the default maximum port for WebSocket server.
const DefaultPortRangeMax = 9999

// FindAvailablePort finds an available TCP port in the specified range.
// It tries ports starting from minPort until maxPort, returning the first available port.
// Returns an error if no port is available in the range.
func FindAvailablePort(minPort, maxPort int) (int, error) {
	if minPort > maxPort {
		return 0, fmt.Errorf("invalid port range: min (%d) > max (%d)", minPort, maxPort)
	}
	if minPort < 1 || maxPort > 65535 {
		return 0, fmt.Errorf("port range must be between 1 and 65535")
	}

	for port := minPort; port <= maxPort; port++ {
		if isPortAvailable(port) {
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available port in range %d-%d", minPort, maxPort)
}

// isPortAvailable checks if a TCP port is available for listening.
func isPortAvailable(port int) bool {
	addr := fmt.Sprintf("localhost:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// WritePortFile writes the port number to the specified file.
// It creates the parent directory if it doesn't exist.
// The file is written atomically (write to temp file, then rename).
func WritePortFile(path string, port int) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create port file directory: %w", err)
	}

	// Write to temporary file first for atomic write
	tempPath := path + ".tmp"
	content := fmt.Sprintf("%d\n", port)
	if err := os.WriteFile(tempPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write port file: %w", err)
	}

	// Rename temp file to final path (atomic on Unix)
	if err := os.Rename(tempPath, path); err != nil {
		// Clean up temp file on failure
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to finalize port file: %w", err)
	}

	return nil
}

// ReadPortFile reads the port number from the specified file.
// Returns an error if the file doesn't exist or contains invalid data.
func ReadPortFile(path string) (int, error) {
	content, err := os.ReadFile(path) //nolint:gosec // G304 - path from internal var directory
	if err != nil {
		// Return error without wrapping to preserve os.IsNotExist check
		return 0, err
	}

	portStr := strings.TrimSpace(string(content))
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, fmt.Errorf("invalid port in file: %s", portStr)
	}

	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port out of valid range: %d", port)
	}

	return port, nil
}

// RemovePortFile removes the port file.
// Returns nil if the file doesn't exist.
func RemovePortFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove port file: %w", err)
	}
	return nil
}
