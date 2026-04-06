package peer_test

import (
	"testing"

	"github.com/leonletto/thrum/internal/bridge/peer"
)

func TestValidateAddressChange(t *testing.T) {
	tests := []struct {
		name      string
		transport string
		oldAddr   string
		newAddr   string
		wantErr   bool
	}{
		// local transport
		{
			name:      "local same loopback",
			transport: "local",
			oldAddr:   "127.0.0.1:8080",
			newAddr:   "127.0.0.1:8081",
			wantErr:   false,
		},
		{
			name:      "local changed to non-loopback",
			transport: "local",
			oldAddr:   "127.0.0.1:8080",
			newAddr:   "192.168.1.5:8080",
			wantErr:   true,
		},

		// tailscale transport
		{
			name:      "tailscale port change only",
			transport: "tailscale",
			oldAddr:   "100.64.0.1:8080",
			newAddr:   "100.64.0.1:9090",
			wantErr:   false,
		},
		{
			name:      "tailscale IP change within CGNAT range",
			transport: "tailscale",
			oldAddr:   "100.64.0.1:8080",
			newAddr:   "100.100.0.5:8080",
			wantErr:   false,
		},
		{
			name:      "tailscale IP out of CGNAT range",
			transport: "tailscale",
			oldAddr:   "100.64.0.1:8080",
			newAddr:   "192.168.1.5:8080",
			wantErr:   true,
		},

		// network transport
		{
			name:      "network same /24 subnet",
			transport: "network",
			oldAddr:   "192.168.1.10:8080",
			newAddr:   "192.168.1.20:8080",
			wantErr:   false,
		},
		{
			name:      "network different /24 subnet",
			transport: "network",
			oldAddr:   "192.168.1.10:8080",
			newAddr:   "192.168.2.10:8080",
			wantErr:   true,
		},

		// bad inputs
		{
			name:      "invalid old address",
			transport: "local",
			oldAddr:   "notanaddress",
			newAddr:   "127.0.0.1:8080",
			wantErr:   true,
		},
		{
			name:      "invalid new address",
			transport: "local",
			oldAddr:   "127.0.0.1:8080",
			newAddr:   "notanaddress",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := peer.ValidateAddressChange(tt.transport, tt.oldAddr, tt.newAddr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAddressChange(%q, %q, %q) = %v, wantErr %v",
					tt.transport, tt.oldAddr, tt.newAddr, err, tt.wantErr)
			}
		})
	}
}
