package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// ThrumConfig represents the top-level .thrum/config.json file.
type ThrumConfig struct {
	Daemon DaemonConfig `json:"daemon"`
}

// DaemonConfig holds daemon-specific settings.
type DaemonConfig struct {
	LocalOnly bool `json:"local_only"`
}

// LoadThrumConfig reads .thrum/config.json from the given thrum directory.
// Returns a zero-value ThrumConfig (all defaults) if the file doesn't exist.
func LoadThrumConfig(thrumDir string) (*ThrumConfig, error) {
	configPath := filepath.Join(thrumDir, "config.json")

	data, err := os.ReadFile(configPath) //nolint:gosec // G304 - path from internal thrum directory
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &ThrumConfig{}, nil
		}
		return nil, err
	}

	var cfg ThrumConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// SaveThrumConfig writes .thrum/config.json, merging with any existing content.
// Reads the file first so future top-level keys are preserved.
func SaveThrumConfig(thrumDir string, cfg *ThrumConfig) error {
	configPath := filepath.Join(thrumDir, "config.json")

	// Read existing file to preserve unknown keys
	existing := make(map[string]any)
	if data, err := os.ReadFile(configPath); err == nil { //nolint:gosec // G304 - path from internal thrum directory
		_ = json.Unmarshal(data, &existing) // best-effort; overwrite if corrupt
	}

	// Marshal the daemon section and merge it in
	daemonBytes, err := json.Marshal(cfg.Daemon)
	if err != nil {
		return err
	}
	var daemonMap any
	_ = json.Unmarshal(daemonBytes, &daemonMap)
	existing["daemon"] = daemonMap

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return os.WriteFile(configPath, data, 0600)
}
