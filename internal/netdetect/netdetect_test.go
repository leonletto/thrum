package netdetect

import (
	"errors"
	"net"
	"testing"
)

func TestIsFilteredAddress(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"ipv4 loopback", "127.0.0.1", true},
		{"ipv4 loopback alt", "127.0.0.5", true},
		{"ipv4 tailscale cgnat low", "100.64.0.1", true},
		{"ipv4 tailscale cgnat high", "100.127.255.254", true},
		{"ipv4 tailscale cgnat user-host", "100.118.69.3", true},
		{"ipv4 link-local", "169.254.10.20", true},
		{"ipv4 valid LAN 192.168.x", "192.168.1.5", false},
		{"ipv4 valid LAN 10.x", "10.0.0.5", false},
		{"ipv4 valid LAN 172.16.x", "172.16.5.10", false},
		{"ipv4 public", "8.8.8.8", false},
		{"ipv6 loopback ::1", "::1", true},
		{"ipv6 link-local fe80::", "fe80::1", true},
		{"ipv6 ula fc00::", "fc00::1", true},
		{"ipv6 ula fd00::", "fd00::abcd", true},
		{"ipv6 GUA", "2001:db8::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isFilteredAddress(net.ParseIP(tt.ip))
			if got != tt.want {
				t.Fatalf("isFilteredAddress(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestIsFilteredInterfaceName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"en0", false},
		{"en1", false},
		{"eth0", false},
		{"wlan0", false},
		{"utun0", true},
		{"utun3", true},
		{"tun0", true},
		{"tap0", true},
		{"ppp0", true},
		{"gif0", true},
		{"stf0", true},
		{"awdl0", true},
		{"llw0", true},
		{"docker0", true},
		{"br-12345abcde", true},
		{"veth1234", true},
		{"virbr0", true},
		{"vmnet1", true},
		{"vboxnet0", true},
		{"En0", false},     // case-sensitive lowering: still allowed
		{"UTUN0", true},    // upper-case still matched
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isFilteredInterfaceName(tt.name)
			if got != tt.want {
				t.Fatalf("isFilteredInterfaceName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestSameSubnet(t *testing.T) {
	_, twentyfour, _ := net.ParseCIDR("192.168.1.0/24")
	tests := []struct {
		name string
		a, b string
		cidr *net.IPNet
		want bool
	}{
		{"same /24", "192.168.1.5", "192.168.1.99", twentyfour, true},
		{"different /24", "192.168.1.5", "192.168.2.99", twentyfour, false},
		{"a outside cidr", "10.0.0.5", "192.168.1.99", twentyfour, false},
		{"b outside cidr", "192.168.1.5", "10.0.0.99", twentyfour, false},
		{"both outside", "10.0.0.5", "10.0.0.99", twentyfour, false},
		{"nil a", "", "192.168.1.5", twentyfour, false},
		{"nil b", "192.168.1.5", "", twentyfour, false},
		{"nil cidr", "192.168.1.5", "192.168.1.99", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SameSubnet(net.ParseIP(tt.a), net.ParseIP(tt.b), tt.cidr)
			if got != tt.want {
				t.Fatalf("SameSubnet(%s, %s, %v) = %v, want %v", tt.a, tt.b, tt.cidr, got, tt.want)
			}
		})
	}
}

func TestSubnetForLocalAddress_FilteredClasses(t *testing.T) {
	// These IP classes are filtered by class alone — independent of NIC.
	classes := []struct {
		name string
		ip   string
	}{
		{"loopback", "127.0.0.1"},
		{"tailscale cgnat", "100.118.69.3"},
		{"link-local", "169.254.10.20"},
		{"ipv6 loopback", "::1"},
		{"ipv6 link-local", "fe80::1"},
		{"ipv6 ula", "fc00::1"},
	}
	for _, c := range classes {
		t.Run(c.name, func(t *testing.T) {
			_, err := SubnetForLocalAddress(net.ParseIP(c.ip))
			if !errors.Is(err, ErrIneligible) {
				t.Fatalf("SubnetForLocalAddress(%s) err = %v, want ErrIneligible", c.ip, err)
			}
		})
	}
}

func TestSubnetForLocalAddress_NoMatch(t *testing.T) {
	// 240.0.0.0/4 is reserved for future use, no host should have it as
	// a local address, so this is a stable "not on any NIC" test value.
	// (Also passes the address-class filter.)
	_, err := SubnetForLocalAddress(net.ParseIP("240.0.0.1"))
	if err == nil {
		t.Fatal("expected error for unassigned IP, got nil")
	}
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
}

func TestSubnetForLocalAddress_Nil(t *testing.T) {
	_, err := SubnetForLocalAddress(nil)
	if err == nil {
		t.Fatal("expected error for nil ip, got nil")
	}
}
