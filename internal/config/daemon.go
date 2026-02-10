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
