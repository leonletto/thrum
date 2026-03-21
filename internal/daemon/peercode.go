package daemon

import (
	"fmt"
	"strconv"
	"strings"
)

// FormatConnectionString creates a peercode string: name:ip:port:code.
func FormatConnectionString(name, ip string, port int, code string) string {
	return fmt.Sprintf("%s:%s:%d:%s", name, ip, port, code)
}

// FormatPeercode creates a peercode from name, address (ip:port), and code.
func FormatPeercode(name, address, code string) string {
	return fmt.Sprintf("%s:%s:%s", name, address, code)
}

// ParseConnectionString parses a peercode string into its 4 components.
// Format: name:ip:port:code (colon-separated, exactly 4 fields).
func ParseConnectionString(s string) (name, ip string, port int, code string, err error) {
	parts := strings.SplitN(s, ":", 4)
	if len(parts) != 4 {
		return "", "", 0, "", fmt.Errorf("invalid peercode: expected name:ip:port:code, got %q", s)
	}
	// Validate IP field doesn't contain colons (IPv6 would break the format)
	if strings.Contains(parts[1], ":") {
		return "", "", 0, "", fmt.Errorf("invalid peercode: IP field must not contain colons (IPv6 not supported), got %q", parts[1])
	}
	p, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", "", 0, "", fmt.Errorf("invalid port in peercode: %w", err)
	}
	return parts[0], parts[1], p, parts[3], nil
}
