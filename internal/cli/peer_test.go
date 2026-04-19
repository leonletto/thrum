package cli

import (
	"strings"
	"testing"
)

// xir.29: FormatPeerList renders a drift hint under any peer whose
// ReconcileStatus is "drift_reconcile_failed". The hint must name
// `--type repair` so a user reading `thrum peer list` knows the next
// step without consulting external docs.
func TestFormatPeerList_DriftStatusRendered(t *testing.T) {
	peers := []PeerListEntry{
		{
			Name:            "alpha",
			Address:         "1.2.3.4:7731",
			LastSync:        "1s ago",
			ReconcileStatus: "drift_reconcile_failed",
		},
		{
			Name:    "bravo",
			Address: "5.6.7.8:7731",
			// No drift status — healthy.
		},
	}
	out := FormatPeerList(peers)

	// Alpha must surface both the drift keyword and the --type repair hint.
	if !strings.Contains(out, "drift") {
		t.Errorf("output missing 'drift' marker:\n%s", out)
	}
	if !strings.Contains(out, "--type repair alpha") {
		t.Errorf("output missing targeted repair hint for alpha:\n%s", out)
	}

	// Bravo (healthy) must NOT show a drift marker.
	bravoTail := out[strings.Index(out, "bravo"):]
	if strings.Contains(bravoTail, "drift") {
		t.Errorf("bravo wrongly shows drift marker:\n%s", out)
	}
}

func TestFormatPeerList_Healthy_NoDriftSection(t *testing.T) {
	out := FormatPeerList([]PeerListEntry{
		{Name: "alpha", Address: "1.2.3.4:7731"},
	})
	if strings.Contains(out, "drift") || strings.Contains(out, "--type repair") {
		t.Errorf("healthy peer list should not contain drift/repair text:\n%s", out)
	}
}

func TestIsTsnetActive(t *testing.T) {
	tests := []struct {
		name   string
		health *HealthResult
		want   bool
	}{
		{
			name:   "nil health",
			health: nil,
			want:   false,
		},
		{
			name:   "no tailscale info",
			health: &HealthResult{},
			want:   false,
		},
		{
			name:   "tailscale present but disabled",
			health: &HealthResult{Tailscale: &TailscaleSyncInfo{Enabled: false}},
			want:   false,
		},
		{
			name:   "tailscale enabled",
			health: &HealthResult{Tailscale: &TailscaleSyncInfo{Enabled: true}},
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTsnetActive(tt.health); got != tt.want {
				t.Fatalf("IsTsnetActive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthKeyPromptNeeded(t *testing.T) {
	healthy := &HealthResult{Tailscale: &TailscaleSyncInfo{Enabled: true}}
	unhealthy := &HealthResult{}

	tests := []struct {
		name   string
		env    string
		health *HealthResult
		want   bool
	}{
		{
			name:   "env auth key present, healthy tsnet",
			env:    "tskey-auth-foo",
			health: healthy,
			want:   false,
		},
		{
			name:   "env auth key present, unhealthy tsnet",
			env:    "tskey-auth-foo",
			health: unhealthy,
			want:   false,
		},
		{
			name:   "no env auth key, healthy tsnet — skip prompt",
			env:    "",
			health: healthy,
			want:   false,
		},
		{
			name:   "no env auth key, no tsnet info — prompt",
			env:    "",
			health: unhealthy,
			want:   true,
		},
		{
			name:   "no env auth key, nil health (daemon unreachable) — prompt",
			env:    "",
			health: nil,
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AuthKeyPromptNeeded(tt.env, tt.health); got != tt.want {
				t.Fatalf("AuthKeyPromptNeeded(%q, %+v) = %v, want %v", tt.env, tt.health, got, tt.want)
			}
		})
	}
}
