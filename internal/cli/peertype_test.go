package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePeerType(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    PeerType
		wantErr error
	}{
		{"tailscale", "tailscale", PeerTypeTailscale, nil},
		{"local", "local", PeerTypeLocal, nil},
		{"network", "network", PeerTypeNetwork, nil},
		{"repair", "repair", PeerTypeRepair, nil},
		{"upper-case tailscale", "TAILSCALE", PeerTypeTailscale, nil},
		{"mixed-case Local", "Local", PeerTypeLocal, nil},
		{"surrounding whitespace", "  network  ", PeerTypeNetwork, nil},
		{"empty", "", "", ErrPeerTypeMissing},
		{"whitespace only", "   ", "", ErrPeerTypeMissing},
		{"unknown 'a-sync' (removed in xir.27 scope correction)", "a-sync", "", ErrPeerTypeUnknown},
		{"unknown 'remote'", "remote", "", ErrPeerTypeUnknown},
		{"unknown 'sibling'", "sibling", "", ErrPeerTypeUnknown},
		{"unknown 'subnet' (renamed to network)", "subnet", "", ErrPeerTypeUnknown},
		{"unknown 'tcp'", "tcp", "", ErrPeerTypeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePeerType(tt.raw)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ParsePeerType(%q) err = %v, want %v", tt.raw, err, tt.wantErr)
				}
				if got != "" {
					t.Fatalf("ParsePeerType(%q) returned non-empty value %q on error path", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePeerType(%q) unexpected err = %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParsePeerType(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestIsValidPeerType(t *testing.T) {
	if !IsValidPeerType("local") {
		t.Fatal("expected 'local' to be valid")
	}
	if IsValidPeerType("") {
		t.Fatal("expected empty to be invalid")
	}
	if IsValidPeerType("nonsense") {
		t.Fatal("expected 'nonsense' to be invalid")
	}
}

func TestMissingTypeMessage_ListsAllFourValues(t *testing.T) {
	// Sanity: the user-facing missing-type message must mention every
	// PeerType value. If a new PeerType is added, this test forces the
	// message to be updated alongside.
	values := []PeerType{
		PeerTypeTailscale,
		PeerTypeLocal,
		PeerTypeNetwork,
		PeerTypeRepair,
	}
	for _, v := range values {
		t.Run(string(v), func(t *testing.T) {
			needle := "--type " + string(v)
			if !strings.Contains(MissingTypeMessage, needle) {
				t.Fatalf("MissingTypeMessage missing %q — update the message", needle)
			}
		})
	}
}
