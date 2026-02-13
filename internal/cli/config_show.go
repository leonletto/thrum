package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/runtime"
)

// ConfigShowResult contains the resolved effective configuration.
type ConfigShowResult struct {
	ConfigFile string `json:"config_file"`

	Runtime  ConfigRuntimeInfo  `json:"runtime"`
	Daemon   ConfigDaemonInfo   `json:"daemon"`
	Identity ConfigIdentityInfo `json:"identity,omitempty"`

	// Overrides lists active environment variable overrides.
	Overrides []ConfigOverride `json:"overrides,omitempty"`
}

// ConfigRuntimeInfo contains runtime configuration details.
type ConfigRuntimeInfo struct {
	Primary  string   `json:"primary"`
	Source   string   `json:"source"`
	Detected []string `json:"detected,omitempty"`
}

// ConfigDaemonInfo contains daemon configuration details.
type ConfigDaemonInfo struct {
	LocalOnly    ConfigValue `json:"local_only"`
	SyncInterval ConfigValue `json:"sync_interval"`
	WSPort       ConfigValue `json:"ws_port"`
	Status       string      `json:"status"`
	PID          int         `json:"pid,omitempty"`
	Socket       string      `json:"socket,omitempty"`
}

// ConfigIdentityInfo contains current agent identity.
type ConfigIdentityInfo struct {
	Agent  string `json:"agent,omitempty"`
	Role   string `json:"role,omitempty"`
	Module string `json:"module,omitempty"`
	File   string `json:"file,omitempty"`
}

// ConfigValue pairs a value with its source.
type ConfigValue struct {
	Value  string `json:"value"`
	Source string `json:"source"` // "config.json", "env", "default", "auto"
}

// ConfigOverride describes an active environment variable override.
type ConfigOverride struct {
	EnvVar string `json:"env_var"`
	Value  string `json:"value"`
}

// ConfigShow resolves the effective configuration from all sources.
func ConfigShow(repoPath string) (*ConfigShowResult, error) {
	thrumDir := filepath.Join(repoPath, ".thrum")
	configPath := filepath.Join(thrumDir, "config.json")

	result := &ConfigShowResult{
		ConfigFile: configPath,
	}

	// Load config.json
	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// --- Runtime section ---
	result.Runtime.Primary = cfg.Runtime.Primary
	if cfg.Runtime.Primary != "" {
		result.Runtime.Source = "config.json"
	} else {
		detected := runtime.DetectRuntime(repoPath)
		result.Runtime.Primary = detected
		if detected != "cli-only" {
			result.Runtime.Source = "auto-detected"
		} else {
			result.Runtime.Source = "default"
		}
	}

	// Detect all runtimes for informational display
	allDetected := runtime.DetectAllRuntimes(repoPath)
	for _, d := range allDetected {
		result.Runtime.Detected = append(result.Runtime.Detected, d.Name)
	}

	// --- Daemon section ---

	// Local-only: env > config > default
	result.Daemon.LocalOnly = ConfigValue{Value: "false", Source: "default"}
	if cfg.Daemon.LocalOnly {
		result.Daemon.LocalOnly = ConfigValue{Value: "true", Source: "config.json"}
	}
	if env := os.Getenv("THRUM_LOCAL"); env == "1" || env == "true" {
		result.Daemon.LocalOnly = ConfigValue{Value: "true", Source: "env"}
	}

	// Sync interval: env > config > default
	result.Daemon.SyncInterval = ConfigValue{
		Value:  strconv.Itoa(cfg.Daemon.SyncInterval) + "s",
		Source: "default",
	}
	if cfg.Daemon.SyncInterval != config.DefaultSyncInterval {
		result.Daemon.SyncInterval.Source = "config.json"
	}
	if envInterval := os.Getenv("THRUM_SYNC_INTERVAL"); envInterval != "" {
		result.Daemon.SyncInterval = ConfigValue{Value: envInterval + "s", Source: "env"}
	}

	// WS port: env > config > default
	result.Daemon.WSPort = ConfigValue{
		Value:  cfg.Daemon.WSPort,
		Source: "default",
	}
	if cfg.Daemon.WSPort != config.DefaultWSPort {
		result.Daemon.WSPort.Source = "config.json"
	}
	if envPort := os.Getenv("THRUM_WS_PORT"); envPort != "" {
		result.Daemon.WSPort = ConfigValue{Value: envPort, Source: "env"}
	}
	// If daemon is running, show actual port
	if wsPort := ReadWebSocketPort(repoPath); wsPort > 0 {
		result.Daemon.WSPort.Value = strconv.Itoa(wsPort) + " (active)"
	}

	// Daemon status
	pidPath := filepath.Join(thrumDir, "var", "thrum.pid")
	running, pidInfo, _ := daemon.CheckPIDFileJSON(pidPath)
	if running {
		result.Daemon.Status = fmt.Sprintf("running (PID %d)", pidInfo.PID)
		result.Daemon.PID = pidInfo.PID
		result.Daemon.Socket = pidInfo.SocketPath
	} else {
		result.Daemon.Status = "not running"
	}

	// --- Identity section ---
	identityFile, identityPath, identErr := config.LoadIdentityWithPath(repoPath)
	if identErr == nil {
		result.Identity = ConfigIdentityInfo{
			Agent:  identityFile.Agent.Name,
			Role:   identityFile.Agent.Role,
			Module: identityFile.Agent.Module,
			File:   identityPath,
		}
	}

	// --- Overrides section ---
	envOverrides := []struct {
		name string
	}{
		{"THRUM_NAME"},
		{"THRUM_ROLE"},
		{"THRUM_MODULE"},
		{"THRUM_LOCAL"},
		{"THRUM_SYNC_INTERVAL"},
		{"THRUM_WS_PORT"},
	}
	for _, e := range envOverrides {
		if v := os.Getenv(e.name); v != "" {
			result.Overrides = append(result.Overrides, ConfigOverride{
				EnvVar: e.name,
				Value:  v,
			})
		}
	}

	return result, nil
}

// FormatConfigShow formats the config show result for human-readable display.
func FormatConfigShow(result *ConfigShowResult) string {
	var b strings.Builder

	b.WriteString("Thrum Configuration\n")
	b.WriteString(fmt.Sprintf("  Config file: %s\n", result.ConfigFile))

	// Runtime section
	b.WriteString("\nRuntime\n")
	b.WriteString(fmt.Sprintf("  Primary:     %s (%s)\n", result.Runtime.Primary, result.Runtime.Source))
	if len(result.Runtime.Detected) > 0 {
		b.WriteString(fmt.Sprintf("  Detected:    %s\n", strings.Join(result.Runtime.Detected, ", ")))
	}

	// Daemon section
	b.WriteString("\nDaemon\n")
	b.WriteString(fmt.Sprintf("  Local-only:    %s (%s)\n", result.Daemon.LocalOnly.Value, result.Daemon.LocalOnly.Source))
	b.WriteString(fmt.Sprintf("  Sync interval: %s (%s)\n", result.Daemon.SyncInterval.Value, result.Daemon.SyncInterval.Source))
	b.WriteString(fmt.Sprintf("  WS port:       %s (%s)\n", result.Daemon.WSPort.Value, result.Daemon.WSPort.Source))
	b.WriteString(fmt.Sprintf("  Status:        %s\n", result.Daemon.Status))
	if result.Daemon.Socket != "" {
		b.WriteString(fmt.Sprintf("  Socket:        %s\n", result.Daemon.Socket))
	}

	// Identity section
	if result.Identity.Agent != "" || result.Identity.Role != "" {
		b.WriteString("\nIdentity\n")
		if result.Identity.Agent != "" {
			b.WriteString(fmt.Sprintf("  Agent:       %s\n", result.Identity.Agent))
		}
		if result.Identity.Role != "" {
			b.WriteString(fmt.Sprintf("  Role:        %s\n", result.Identity.Role))
		}
		if result.Identity.Module != "" {
			b.WriteString(fmt.Sprintf("  Module:      %s\n", result.Identity.Module))
		}
		if result.Identity.File != "" {
			b.WriteString(fmt.Sprintf("  File:        %s\n", result.Identity.File))
		}
	}

	// Overrides section
	if len(result.Overrides) > 0 {
		b.WriteString("\nOverrides (environment)\n")
		for _, o := range result.Overrides {
			b.WriteString(fmt.Sprintf("  %s=%s\n", o.EnvVar, o.Value))
		}
	}

	return b.String()
}
