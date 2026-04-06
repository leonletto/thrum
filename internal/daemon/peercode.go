package daemon

import (
	"fmt"
	"net"
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

// DetectTransport infers the transport type from a peer address.
// Returns "local" for loopback, "tailscale" for Tailscale CGNAT (100.64.0.0/10), or "network".
func DetectTransport(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "network"
	}
	if ip.IsLoopback() {
		return "local"
	}
	// Tailscale CGNAT range: 100.64.0.0/10
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	if cgnat.Contains(ip) {
		return "tailscale"
	}
	return "network"
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
