package runtime

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
)

// RuntimePreset describes the configuration and capabilities of a supported
// AI coding runtime. Built-in presets cover all known runtimes; users can
// add custom presets via ~/.config/thrum/runtimes.json.
type RuntimePreset struct {
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Command          string `json:"command"`
	MCPSupported     bool   `json:"mcp_supported"`
	HooksSupported   bool   `json:"hooks_supported"`
	InstructionsFile string `json:"instructions_file"`
	MCPConfigPath    string `json:"mcp_config_path"`
	SetupNotes       string `json:"setup_notes"`
}

// BuiltinPresets contains the default presets for all known runtimes.
var BuiltinPresets = map[string]RuntimePreset{
	"claude": {
		Name:             "claude",
		DisplayName:      "Claude Code",
		Command:          "claude",
		MCPSupported:     true,
		HooksSupported:   true,
		InstructionsFile: "CLAUDE.md",
		MCPConfigPath:    ".claude/settings.json",
		SetupNotes:       "Add thrum MCP server to .claude/settings.json",
	},
	"codex": {
		Name:             "codex",
		DisplayName:      "OpenAI Codex",
		Command:          "codex",
		MCPSupported:     true,
		HooksSupported:   false,
		InstructionsFile: "AGENTS.md",
		MCPConfigPath:    "Run: codex mcp add thrum 'thrum mcp serve'",
		SetupNotes:       "Use .codex/hooks/session-start for startup",
	},
	"cursor": {
		Name:             "cursor",
		DisplayName:      "Cursor",
		Command:          "cursor-agent",
		MCPSupported:     true,
		HooksSupported:   false,
		InstructionsFile: ".cursorrules",
		MCPConfigPath:    "Settings > Tools & MCP",
		SetupNotes:       "Add MCP server via UI, use startup script",
	},
	"gemini": {
		Name:             "gemini",
		DisplayName:      "Google Gemini Code Assist",
		Command:          "gemini",
		MCPSupported:     true,
		HooksSupported:   false,
		InstructionsFile: "~/.gemini/instructions.md",
		MCPConfigPath:    "~/.gemini/settings.json",
		SetupNotes:       "Global instructions, use profile.sh for startup",
	},
	"auggie": {
		Name:             "auggie",
		DisplayName:      "Augment (Auggie)",
		Command:          "auggie",
		MCPSupported:     false,
		HooksSupported:   false,
		InstructionsFile: "CLAUDE.md",
		MCPConfigPath:    "",
		SetupNotes:       "CLI-only integration (MCP support unknown)",
	},
	"amp": {
		Name:             "amp",
		DisplayName:      "Sourcegraph Amp",
		Command:          "amp",
		MCPSupported:     false,
		HooksSupported:   false,
		InstructionsFile: "CLAUDE.md",
		MCPConfigPath:    "",
		SetupNotes:       "CLI-only integration (MCP support unknown)",
	},
}

// userPresetsConfig is the JSON schema for ~/.config/thrum/runtimes.json.
type userPresetsConfig struct {
	DefaultRuntime string                   `json:"default_runtime,omitempty"`
	CustomRuntimes map[string]RuntimePreset `json:"custom_runtimes,omitempty"`
}

// GetPreset returns the preset for the given runtime name.
// It checks user presets first (allowing overrides), then built-in presets.
func GetPreset(name string) (RuntimePreset, error) {
	// Check user presets first (user overrides built-in)
	userPresets, _ := loadUserPresets()
	if preset, ok := userPresets[name]; ok {
		return preset, nil
	}

	if preset, ok := BuiltinPresets[name]; ok {
		return preset, nil
	}

	return RuntimePreset{}, fmt.Errorf("runtime preset %q not found", name)
}

// ListPresets returns all presets (built-in + user-defined), sorted by name.
// User presets override built-in presets with the same name.
func ListPresets() []RuntimePreset {
	merged := make(map[string]RuntimePreset, len(BuiltinPresets))
	maps.Copy(merged, BuiltinPresets)

	userPresets, _ := loadUserPresets()
	maps.Copy(merged, userPresets)

	presets := make([]RuntimePreset, 0, len(merged))
	for _, preset := range merged {
		presets = append(presets, preset)
	}

	sort.Slice(presets, func(i, j int) bool {
		return presets[i].Name < presets[j].Name
	})

	return presets
}

// GetDefaultRuntime returns the user-configured default runtime, or empty string.
func GetDefaultRuntime() string {
	cfg, err := loadUserConfig()
	if err != nil {
		return ""
	}
	return cfg.DefaultRuntime
}

// SetDefaultRuntime sets the default runtime in user config.
func SetDefaultRuntime(name string) error {
	// Validate runtime exists
	if _, err := GetPreset(name); err != nil {
		return err
	}

	cfg, _ := loadUserConfig()
	cfg.DefaultRuntime = name
	return saveUserConfig(cfg)
}

// userConfigPath returns the path to the user config file.
// Checks XDG_CONFIG_HOME first (standard on Linux, testable on macOS),
// then falls back to os.UserConfigDir().
func userConfigPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "thrum", "runtimes.json"), nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine config dir: %w", err)
	}
	return filepath.Join(configDir, "thrum", "runtimes.json"), nil
}

// loadUserPresets loads custom runtime presets from user config.
// Returns an empty map (not an error) if the file doesn't exist.
func loadUserPresets() (map[string]RuntimePreset, error) {
	cfg, err := loadUserConfig()
	if err != nil {
		return nil, err
	}
	if cfg.CustomRuntimes == nil {
		return map[string]RuntimePreset{}, nil
	}
	return cfg.CustomRuntimes, nil
}

// loadUserConfig loads the user config from disk.
func loadUserConfig() (*userPresetsConfig, error) {
	path, err := userConfigPath()
	if err != nil {
		return &userPresetsConfig{}, err
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if os.IsNotExist(err) {
			return &userPresetsConfig{}, nil
		}
		return &userPresetsConfig{}, err
	}

	var cfg userPresetsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &userPresetsConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}

	return &cfg, nil
}

// saveUserConfig writes the user config to disk.
func saveUserConfig(cfg *userPresetsConfig) error {
	path, err := userConfigPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	return os.WriteFile(path, data, 0600)
}
