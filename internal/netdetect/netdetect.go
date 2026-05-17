// Package netdetect resolves locally-assigned IP addresses to the NIC and
// subnet that own them, with filtering for transports that should not be
// used for direct-TCP peer pairing (loopback, Tailscale CGNAT, common
// virtual / VPN interfaces).
//
// Used by `thrum peer add --type network` to validate the user-supplied
// --address against the local NIC list and infer the subnet for the
// peercode and the persisted PeerInfo.
//
// Per Leon (2026-04-19): subnet ALWAYS derives from a user-provided IP.
// There is NO auto-detect path; if the IP is not assigned to an
// eligible NIC, the call fails. Eligibility excludes loopback, link-local,
// tailscale CGNAT, and well-known virtual / VPN interface name patterns.
package netdetect

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// Pre-parsed CIDR ranges used by isFilteredAddress. Parsing these on every
// address-eligibility check showed up in review as wasted work; hoisting to
// package-level init ensures the parse happens once.
var (
	// CgnatCIDR is the Tailscale CGNAT range (100.64.0.0/10). Addresses in
	// this range belong to the tailscale transport, not "network".
	cgnatCIDR *net.IPNet
	// UlaCIDR is the IPv6 unique-local range (fc00::/7). Not a normal LAN
	// class for direct peer pairing.
	ulaCIDR *net.IPNet
)

func init() {
	_, cgnatCIDR, _ = net.ParseCIDR("100.64.0.0/10")
	_, ulaCIDR, _ = net.ParseCIDR("fc00::/7")
}

// Subnet describes an NIC subnet usable for direct-TCP peer transport.
type Subnet struct {
	// Interface is the NIC name (e.g., "en0").
	Interface string
	// Address is the local IPv4/IPv6 assigned to this NIC on this subnet.
	// For network-mode peer pairing this is the address that goes into
	// the peercode emitted to the joining side.
	Address net.IP
	// CIDR is the network mask for this subnet, used by auto-reconcile
	// (xir.29) to enforce the same-subnet drift guard.
	CIDR *net.IPNet
}

// String returns a human-readable summary suitable for CLI notes.
func (s Subnet) String() string {
	if s.CIDR == nil {
		return fmt.Sprintf("%s on %s", s.Address, s.Interface)
	}
	return fmt.Sprintf("%s on %s (subnet %s)", s.Address, s.Interface, s.CIDR)
}

// ErrNoMatch is returned when the supplied IP is not assigned to any
// local NIC.
var ErrNoMatch = errors.New("ip is not assigned to any local NIC")

// ErrIneligible is returned when the supplied IP is on a NIC that is
// filtered for peer-transport use (loopback, link-local, tailscale,
// virtual / VPN tun).
var ErrIneligible = errors.New("ip is on a NIC that is not eligible for peer transport")

// SubnetForLocalAddress finds the local NIC that owns the given IP and
// returns its Subnet. Returns ErrNoMatch if the IP is not assigned to
// any local interface; ErrIneligible if the matching interface is
// filtered (loopback, link-local, tailscale CGNAT, or a well-known
// virtual / VPN tun pattern).
//
// IPv4 and IPv6 addresses are both supported. Eligibility uses both
// the matching interface's name (utun*, tun*, tap*, etc.) and the IP's
// own classification (loopback, link-local, CGNAT) to filter.
func SubnetForLocalAddress(ip net.IP) (Subnet, error) {
	if ip == nil {
		return Subnet{}, fmt.Errorf("nil ip")
	}
	if isFilteredAddress(ip) {
		return Subnet{}, fmt.Errorf("%w: address class is filtered (loopback/link-local/tailscale/ula)", ErrIneligible)
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return Subnet{}, fmt.Errorf("enumerate interfaces: %w", err)
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if !ipNet.IP.Equal(ip) {
				continue
			}
			// IP is on this interface — now check eligibility.
			if iface.Flags&net.FlagLoopback != 0 {
				return Subnet{}, fmt.Errorf("%w: interface %q is loopback", ErrIneligible, iface.Name)
			}
			if iface.Flags&net.FlagUp == 0 {
				return Subnet{}, fmt.Errorf("%w: interface %q is down", ErrIneligible, iface.Name)
			}
			if isFilteredInterfaceName(iface.Name) {
				return Subnet{}, fmt.Errorf("%w: interface %q matches filtered pattern (vpn/virtual)", ErrIneligible, iface.Name)
			}
			cidr := &net.IPNet{IP: ipNet.IP.Mask(ipNet.Mask), Mask: ipNet.Mask}
			return Subnet{
				Interface: iface.Name,
				Address:   ipNet.IP,
				CIDR:      cidr,
			}, nil
		}
	}

	return Subnet{}, fmt.Errorf("%w: %s", ErrNoMatch, ip)
}

// SameSubnet reports whether two IPs sit in the same /CIDR according to
// the supplied CIDR mask. Used by auto-reconcile (xir.29) to enforce
// the same-subnet drift guard: an address change within the same
// subnet is safe to auto-fix; cross-subnet requires manual repair.
func SameSubnet(a, b net.IP, cidr *net.IPNet) bool {
	if a == nil || b == nil || cidr == nil {
		return false
	}
	return cidr.Contains(a) && cidr.Contains(b)
}

// isFilteredAddress returns true for IP classes that are never valid as
// a direct-TCP peer address: loopback, link-local, Tailscale CGNAT
// (100.64.0.0/10), IPv6 link-local (fe80::/10), IPv6 ULA (fc00::/7).
func isFilteredAddress(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	if ip.IsLinkLocalUnicast() {
		return true
	}
	// Tailscale CGNAT range — peer transport is "tailscale", not "network".
	if ip.To4() != nil {
		if cgnatCIDR != nil && cgnatCIDR.Contains(ip) {
			return true
		}
	}
	// IPv6 ULA — fc00::/7 is unique-local, treat as filtered (not a normal
	// LAN class for peer pairing). Standard IPv6 GUA addresses pass through.
	if ip.To4() == nil {
		if ulaCIDR != nil && ulaCIDR.Contains(ip) {
			return true
		}
	}
	return false
}

// isFilteredInterfaceName returns true for interface name prefixes that
// indicate a virtual / VPN tunnel and should not be used for LAN peer
// pairing. Conservative list — adds false negatives only (e.g., a custom
// LAN bridge named "vbr0" passes through; user can challenge in a
// future bug).
func isFilteredInterfaceName(name string) bool {
	lower := strings.ToLower(name)
	prefixes := []string{
		"utun",    // macOS user-mode tunnel (Tailscale, WireGuard userspace, etc.)
		"tun",     // Linux/BSD generic tun
		"tap",     // Linux/BSD generic tap (often used by VMs)
		"ppp",     // PPP / dial-up / mobile broadband
		"gif",     // BSD generic tunnel
		"stf",     // BSD 6to4 tunnel
		"awdl",    // macOS Apple Wireless Direct Link
		"llw",     // macOS low-latency wireless (peer-to-peer)
		"docker",  // docker bridge / host
		"br-",     // docker user-defined bridge
		"veth",    // Linux virtual ethernet (containers)
		"virbr",   // libvirt bridge
		"vmnet",   // VMware
		"vboxnet", // VirtualBox
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}
