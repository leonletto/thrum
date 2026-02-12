package config

import (
	"fmt"
	"os"
	"strconv"
)

// DefaultTailscalePort is the default port for the tsnet sync listener.
const DefaultTailscalePort = 9100

// TailscaleConfig holds configuration for the Tailscale tsnet sync listener.
type TailscaleConfig struct {
	Enabled    bool   // Whether Tailscale sync is enabled
	Hostname   string // tsnet hostname (e.g., "thrum-daemon-alice")
	Port       int    // Sync listener port (default 9100)
	StateDir   string // Directory for tsnet state persistence
	AuthKey    string // Tailscale auth key (loaded from env)
	ControlURL string // Control plane URL (empty = Tailscale SaaS; set for Headscale)
}

// LoadTailscaleConfig loads Tailscale configuration from environment variables.
//
// Environment variables:
//   - THRUM_TS_ENABLED: "true"/"1" to enable (default: false)
//   - THRUM_TS_HOSTNAME: tsnet hostname (required when enabled)
//   - THRUM_TS_PORT: listener port (default: 9100)
//   - THRUM_TS_AUTHKEY: Tailscale auth key (required when enabled)
//   - THRUM_TS_STATE_DIR: state directory (default: .thrum/var/tsnet)
//   - THRUM_TAILSCALE_CONTROL_URL: control plane URL (optional, for Headscale)
func LoadTailscaleConfig(thrumDir string) TailscaleConfig {
	cfg := TailscaleConfig{
		Port:     DefaultTailscalePort,
		StateDir: fmt.Sprintf("%s/var/tsnet", thrumDir),
	}

	// Enabled
	if envBool("THRUM_TS_ENABLED") {
		cfg.Enabled = true
	}

	// Hostname
	if h := os.Getenv("THRUM_TS_HOSTNAME"); h != "" {
		cfg.Hostname = h
	}

	// Port
	if p := os.Getenv("THRUM_TS_PORT"); p != "" {
		if port, err := strconv.Atoi(p); err == nil {
			cfg.Port = port
		}
	}

	// Auth key
	cfg.AuthKey = os.Getenv("THRUM_TS_AUTHKEY")

	// State dir override
	if d := os.Getenv("THRUM_TS_STATE_DIR"); d != "" {
		cfg.StateDir = d
	}

	// Control URL (for Headscale / self-hosted)
	cfg.ControlURL = os.Getenv("THRUM_TAILSCALE_CONTROL_URL")

	return cfg
}

// Validate checks that the configuration is valid when enabled.
// Returns nil if disabled or valid.
func (c *TailscaleConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	if c.Hostname == "" {
		return fmt.Errorf("THRUM_TS_HOSTNAME is required when Tailscale sync is enabled")
	}

	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("THRUM_TS_PORT must be between 1 and 65535, got %d", c.Port)
	}

	if c.AuthKey == "" {
		return fmt.Errorf("THRUM_TS_AUTHKEY is required when Tailscale sync is enabled")
	}

	return nil
}

// envBool returns true if the env var is set to a truthy value ("true", "1", "yes").
func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "true" || v == "1" || v == "yes"
}
