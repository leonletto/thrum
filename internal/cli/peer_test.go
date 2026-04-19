package cli

import "testing"

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
