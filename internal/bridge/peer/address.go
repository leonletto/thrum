package peer

import (
	"fmt"
	"net"
)

// ValidateAddressChange checks whether a proposed address change is valid for the given transport type.
// OldAddr and newAddr must be host:port strings.
func ValidateAddressChange(transport, oldAddr, newAddr string) error {
	_, _, err := net.SplitHostPort(oldAddr)
	if err != nil {
		return fmt.Errorf("parse old address: %w", err)
	}
	newHost, _, err := net.SplitHostPort(newAddr)
	if err != nil {
		return fmt.Errorf("parse new address: %w", err)
	}
	newIP := net.ParseIP(newHost)
	if newIP == nil {
		return fmt.Errorf("invalid new IP: %s", newHost)
	}

	switch transport {
	case "local":
		if !newIP.IsLoopback() {
			return fmt.Errorf("local peer address must be loopback, got %s", newHost)
		}
	case "tailscale":
		_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
		if !cgnat.Contains(newIP) {
			return fmt.Errorf("tailscale peer must be in 100.64.0.0/10, got %s", newHost)
		}
	case "network":
		oldHost, _, _ := net.SplitHostPort(oldAddr)
		oldIP := net.ParseIP(oldHost)
		oldSubnet := oldIP.Mask(net.CIDRMask(24, 32))
		newSubnet := newIP.Mask(net.CIDRMask(24, 32))
		if !oldSubnet.Equal(newSubnet) {
			return fmt.Errorf("network peer must stay on same /24 subnet, was %s now %s", oldHost, newHost)
		}
	}
	return nil
}
