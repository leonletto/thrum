package config

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// loadEnvFile reads one or more .env files (checked in order) and sets any
// THRUM_ or TAILSCALE_ prefixed variables that are not already present in the
// environment. The first file that exists wins for each individual variable;
// later paths cannot override values set by earlier paths or by the process
// environment.
//
// File format: KEY=VALUE lines. Lines starting with '#' and blank lines are
// ignored. No shell variable expansion is performed.
//
// Note: the auth-key variable name in the code is THRUM_TS_AUTHKEY (no
// underscore between AUTH and KEY). Some guides may show THRUM_TS_AUTH_KEY —
// use the form without the extra underscore in .env files.
func loadEnvFile(paths ...string) {
	for _, path := range paths {
		f, err := os.Open(path) // #nosec G304 -- paths are .thrum/.env and repo root .env, not user-controlled
		if err != nil {
			// File does not exist or is not readable — skip silently.
			continue
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			// Skip blank lines and comments.
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			idx := strings.IndexByte(line, '=')
			if idx < 1 {
				// No '=' or key is empty — skip malformed lines.
				continue
			}
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])

			// Only load thrum-related variables.
			if !strings.HasPrefix(key, "THRUM_") && !strings.HasPrefix(key, "TAILSCALE_") {
				continue
			}

			// Env vars already set in the process take precedence.
			if os.Getenv(key) == "" {
				_ = os.Setenv(key, value)
			}
		}
		_ = f.Close()
	}
}

// LoadTailscaleConfig loads Tailscale configuration from environment variables.
// Before reading env vars it auto-detects .env files in .thrum/ and the repo
// root, giving the .thrum/.env file higher priority.
//
// Environment variables (can also be set via .env files):
//   - THRUM_TS_AUTHKEY: Tailscale auth key (prompted on first peer add)
//   - THRUM_TS_HOSTNAME: tsnet hostname (auto-derived if not set)
//   - THRUM_TS_PORT: listener port (auto-randomized if not set)
//   - THRUM_TS_STATE_DIR: state directory (default: .thrum/var/tsnet)
//   - THRUM_TAILSCALE_CONTROL_URL: control plane URL (optional, for Headscale)
//   - THRUM_TS_ENABLED: deprecated — Tailscale starts automatically when peers exist
func LoadTailscaleConfig(thrumDir string) TailscaleConfig {
	// Auto-detect .env files. repoRoot is the parent of thrumDir (.thrum).
	repoRoot := filepath.Dir(thrumDir)
	loadEnvFile(
		filepath.Join(thrumDir, ".env"), // .thrum/.env (highest priority)
		filepath.Join(repoRoot, ".env"), // repo root .env
	)

	cfg := TailscaleConfig{
		Port:     DefaultTailscalePort,
		StateDir: fmt.Sprintf("%s/var/tsnet", thrumDir),
	}

	// THRUM_TS_ENABLED is deprecated — Tailscale is now enabled implicitly
	// when peers.json has local.port or peer add is run.
	if envBool("THRUM_TS_ENABLED") {
		cfg.Enabled = true
		log.Println("Note: THRUM_TS_ENABLED is deprecated and can be removed from .env. Tailscale starts automatically when peers are configured.")
	}

	// Hostname — auto-derive if not explicitly set
	if h := os.Getenv("THRUM_TS_HOSTNAME"); h != "" {
		cfg.Hostname = h
	} else {
		if h, err := os.Hostname(); err == nil {
			cfg.Hostname = strings.ToLower(h) + "-thrum"
		}
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
// Returns nil if disabled or valid. Port 0 is valid (means not yet configured;
// will be assigned on first peer add). Auth key is required only when Enabled.
func (c *TailscaleConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	if c.Hostname == "" {
		return fmt.Errorf("THRUM_TS_HOSTNAME is required when Tailscale sync is enabled")
	}

	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("THRUM_TS_PORT must be between 0 and 65535, got %d", c.Port)
	}

	if c.AuthKey == "" {
		return fmt.Errorf("THRUM_TS_AUTHKEY is required when Tailscale sync is enabled")
	}

	return nil
}

// SaveAuthKeyToEnvFile saves the Tailscale auth key to .thrum/.env.
// If the file exists and already contains THRUM_TS_AUTHKEY, it is updated in place.
// Otherwise, the key is appended. Uses atomic write.
func SaveAuthKeyToEnvFile(thrumDir, authKey string) error {
	envPath := filepath.Join(thrumDir, ".env")

	var lines []string
	found := false

	// Read existing content if file exists
	if data, err := os.ReadFile(envPath); err == nil { // #nosec G304 -- envPath is derived from repo root, not user input
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "THRUM_TS_AUTHKEY=") {
				lines = append(lines, "THRUM_TS_AUTHKEY="+authKey)
				found = true
			} else {
				lines = append(lines, line)
			}
		}
	}

	if !found {
		lines = append(lines, "THRUM_TS_AUTHKEY="+authKey)
	}

	content := strings.Join(lines, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	// Atomic write
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		return fmt.Errorf("create thrum dir: %w", err)
	}
	tmpPath := envPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0600); err != nil { // #nosec G703 -- path derived from repo root
		return fmt.Errorf("write env file: %w", err)
	}
	return os.Rename(tmpPath, envPath)
}

// envBool returns true if the env var is set to a truthy value ("true", "1", "yes").
func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "true" || v == "1" || v == "yes"
}
