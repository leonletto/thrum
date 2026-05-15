package runtime

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// RuntimePreset describes the configuration and capabilities of a supported
// AI coding runtime. Built-in presets cover all known runtimes; users can
// add custom presets via ~/.thrum/runtimes.json.
type RuntimePreset struct {
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Command          string `json:"command"`
	MCPSupported     bool   `json:"mcp_supported"`
	HooksSupported   bool   `json:"hooks_supported"`
	InstructionsFile string `json:"instructions_file"`
	MCPConfigPath    string `json:"mcp_config_path"`
	SetupNotes       string `json:"setup_notes"`

	// HasSessionStartHook is true for runtimes that ship the
	// inject-prime-context.sh SessionStart hook (claude-plugin /
	// cursor-plugin). When true, the daemon's tmux launch/restart flow
	// skips the post-launch /thrum:prime send because the hook already
	// auto-injects the briefing. Single source of truth — do NOT
	// hard-code runtime names in cmd/thrum or daemon code; route
	// through GetPreset and read this field. thrum-6hqy.
	HasSessionStartHook bool `json:"has_session_start_hook,omitempty"`

	// BottomAnchorRegex matches the horizontal-rule line that separates
	// the conversation transcript from the runtime's input chrome (input
	// box, footer, key hints). The post-launch silence watchdog uses this
	// to locate the bottom of the agent-output region so it can decide
	// whether to nudge. Nil means the runtime has no TUI chrome separator;
	// the watchdog treats a missing anchor as "engaged" (don't nudge) —
	// conservative default. thrum-84xc.
	BottomAnchorRegex *regexp.Regexp `json:"-"`

	// SpinnerRegex matches the animated thinking-indicator line that the
	// runtime renders in the agent-output region while a turn is in
	// flight. Matched lines are ignored by the silence watchdog (they are
	// chrome, not real agent output). Nil means no spinner pattern —
	// any non-blank line is treated as real output. thrum-84xc.
	SpinnerRegex *regexp.Regexp `json:"-"`
}

// claudeBottomAnchorRegex matches the horizontal rule (U+2500 × 20+) that
// Claude Code renders between the conversation transcript and the input chrome.
// Used by the silence watchdog (thrum-84xc) to bound the agent-output region.
var claudeBottomAnchorRegex = regexp.MustCompile(`^─{20,}$`)

// claudeSpinnerRegex matches Claude Code's animated thinking indicator.
// Two observed durations:
//   - Short form (<1m):  "✻ <verb> for <N>s"       — e.g. "✻ Churned for 17s"
//   - Long form  (≥1m):  "✻ <verb> for <Nm Ns>"    — e.g. "✻ Baked for 1m 45s"
//
// ✻ = U+273B. Present in the agent-output region while a turn is in flight;
// the watchdog ignores these lines as chrome. Uses \S+ instead of \w+ because
// some verbs contain non-ASCII characters (e.g. "Sautéed"). The long-form
// branch is non-capturing so the regex stays anchored at line end. thrum-8dl3.
var claudeSpinnerRegex = regexp.MustCompile(`^✻ \S+ for \d+(?:m \d+)?s$`)

// BuiltinPresets contains the default presets for all known runtimes.
var BuiltinPresets = map[string]RuntimePreset{
	"claude": {
		Name:                "claude",
		DisplayName:         "Claude Code",
		Command:             "claude",
		MCPSupported:        true,
		HooksSupported:      true,
		HasSessionStartHook: true,
		InstructionsFile:    "CLAUDE.md",
		MCPConfigPath:       ".claude/settings.json",
		SetupNotes:          "Add thrum MCP server to .claude/settings.json",
		BottomAnchorRegex:   claudeBottomAnchorRegex,
		SpinnerRegex:        claudeSpinnerRegex,
	},
	"codex": {
		Name:                "codex",
		DisplayName:         "OpenAI Codex",
		Command:             "codex",
		MCPSupported:        true,
		HooksSupported:      false,
		HasSessionStartHook: true, // pm7n.4: codex plugin ships codex-plugin/plugins/thrum/scripts/inject-prime-context.sh as a SessionStart hook
		InstructionsFile:    "AGENTS.md",
		MCPConfigPath:       "Run: codex mcp add thrum 'thrum mcp serve'",
		SetupNotes:          "Use .codex/hooks/session-start for startup",
	},
	"opencode": {
		Name:             "opencode",
		DisplayName:      "Open Code",
		Command:          "opencode",
		MCPSupported:     true,
		HooksSupported:   true,
		InstructionsFile: "AGENTS.md",
		MCPConfigPath:    "opencode.json",
		SetupNotes:       "Install plugin: opencode plugin opencode-thrum",
	},
	"cursor": {
		Name:                "cursor",
		DisplayName:         "Cursor",
		Command:             "agent",
		MCPSupported:        true,
		HooksSupported:      false,
		HasSessionStartHook: true, // ships cursor-plugin/scripts/inject-prime-context.sh
		InstructionsFile:    ".cursorrules",
		MCPConfigPath:       "Settings > Tools & MCP",
		SetupNotes:          "Add MCP server via UI, use startup script",
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
	"kiro-cli": {
		Name:             "kiro-cli",
		DisplayName:      "Amazon Kiro CLI",
		Command:          "kiro-cli chat",
		MCPSupported:     false,
		HooksSupported:   false,
		InstructionsFile: "AGENTS.md",
		MCPConfigPath:    "",
		SetupNotes:       "CLI-only; launched via 'kiro-cli chat' interactive mode",
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
	"shell": {
		Name:             "shell",
		DisplayName:      "Shell (bash)",
		Command:          "bash",
		MCPSupported:     false,
		HooksSupported:   false,
		InstructionsFile: "",
		MCPConfigPath:    "",
		SetupNotes:       "Plain bash shell — useful for testing and manual operations without an AI runtime",
	},
}

// userPresetsConfig is the JSON schema for ~/.thrum/runtimes.json.
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

// userConfigPath returns the path to the user-level runtime preset config.
// The location is $HOME/.thrum/runtimes.json, identical on macOS and Linux
// so there are no platform-specific differences. On Unix-like systems
// os.UserHomeDir() honors the $HOME env var, which tests set via t.Setenv
// for sandboxing.
func userConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home dir: %w", err)
	}
	return filepath.Join(home, ".thrum", "runtimes.json"), nil
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
