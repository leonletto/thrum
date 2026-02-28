package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// ThrumConfig represents the top-level .thrum/config.json file.
type ThrumConfig struct {
	Runtime RuntimeConfig `json:"runtime"`
	Daemon  DaemonConfig  `json:"daemon"`
	Backup  BackupConfig  `json:"backup"`
}

// RuntimeConfig holds runtime selection preferences.
type RuntimeConfig struct {
	Primary string `json:"primary,omitempty"` // "claude", "auggie", "cursor", etc.
}

// DaemonConfig holds daemon-specific settings.
type DaemonConfig struct {
	LocalOnly    bool   `json:"local_only,omitempty"`
	SyncInterval int    `json:"sync_interval,omitempty"` // seconds; 0 means use default (60)
	WSPort       string `json:"ws_port,omitempty"`       // "auto" or specific port number
}

// BackupConfig holds backup-related settings.
type BackupConfig struct {
	Dir        string          `json:"dir,omitempty"`
	Retention  RetentionConfig `json:"retention"`
	Plugins    []PluginConfig  `json:"plugins,omitempty"`
	PostBackup string          `json:"post_backup,omitempty"`
}

// RetentionConfig controls GFS (Grandfather-Father-Son) archive rotation.
type RetentionConfig struct {
	Daily   int `json:"daily"`   // default 5
	Weekly  int `json:"weekly"`  // default 4
	Monthly int `json:"monthly"` // default -1 (keep forever)
}

// PluginConfig defines a third-party backup plugin.
type PluginConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Include []string `json:"include"`
}

// Default retention values.
const (
	DefaultRetentionDaily   = 5
	DefaultRetentionWeekly  = 4
	DefaultRetentionMonthly = -1
)

// DefaultSyncInterval is the default git sync interval in seconds.
const DefaultSyncInterval = 60

// DefaultWSPort is the default WebSocket port strategy.
const DefaultWSPort = "auto"

// LoadThrumConfig reads .thrum/config.json from the given thrum directory.
// Returns a zero-value ThrumConfig (all defaults) if the file doesn't exist.
func LoadThrumConfig(thrumDir string) (*ThrumConfig, error) {
	configPath := filepath.Join(thrumDir, "config.json")

	data, err := os.ReadFile(configPath) //nolint:gosec // G304 - path from internal thrum directory
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg := &ThrumConfig{}
			applyDefaults(cfg)
			return cfg, nil
		}
		return nil, err
	}

	var cfg ThrumConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// applyDefaults fills in sensible defaults for zero-value fields.
// Note: LocalOnly defaults to true (local-first). Users must explicitly
// set local_only=false in config.json to enable remote git sync.
func applyDefaults(cfg *ThrumConfig) {
	if cfg.Daemon.SyncInterval == 0 {
		cfg.Daemon.SyncInterval = DefaultSyncInterval
	}
	if cfg.Daemon.WSPort == "" {
		cfg.Daemon.WSPort = DefaultWSPort
	}
	if cfg.Backup.Retention.Daily == 0 {
		cfg.Backup.Retention.Daily = DefaultRetentionDaily
	}
	if cfg.Backup.Retention.Weekly == 0 {
		cfg.Backup.Retention.Weekly = DefaultRetentionWeekly
	}
	if cfg.Backup.Retention.Monthly == 0 {
		cfg.Backup.Retention.Monthly = DefaultRetentionMonthly
	}
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

	// Marshal and merge the runtime section (only if non-empty)
	if cfg.Runtime.Primary != "" {
		runtimeBytes, err := json.Marshal(cfg.Runtime)
		if err != nil {
			return err
		}
		var runtimeMap any
		_ = json.Unmarshal(runtimeBytes, &runtimeMap)
		existing["runtime"] = runtimeMap
	}

	// Marshal and merge the daemon section
	daemonBytes, err := json.Marshal(cfg.Daemon)
	if err != nil {
		return err
	}
	var daemonMap any
	_ = json.Unmarshal(daemonBytes, &daemonMap)
	existing["daemon"] = daemonMap

	// Marshal and merge the backup section (only if user has configured something)
	isDefaultRetention := (cfg.Backup.Retention.Daily == 0 || cfg.Backup.Retention.Daily == DefaultRetentionDaily) &&
		(cfg.Backup.Retention.Weekly == 0 || cfg.Backup.Retention.Weekly == DefaultRetentionWeekly) &&
		(cfg.Backup.Retention.Monthly == 0 || cfg.Backup.Retention.Monthly == DefaultRetentionMonthly)
	if cfg.Backup.Dir != "" || len(cfg.Backup.Plugins) > 0 || cfg.Backup.PostBackup != "" || !isDefaultRetention {
		backupBytes, err := json.Marshal(cfg.Backup)
		if err != nil {
			return err
		}
		var backupMap any
		_ = json.Unmarshal(backupBytes, &backupMap)
		existing["backup"] = backupMap
	}

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return os.WriteFile(configPath, data, 0600)
}
