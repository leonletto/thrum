package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGetPreset_Builtin(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		command     string
		mcp         bool
		hooks       bool
	}{
		{"claude", "Claude Code", "claude", true, true},
		{"codex", "OpenAI Codex", "codex", true, false},
		{"cursor", "Cursor", "cursor-agent", true, false},
		{"gemini", "Google Gemini Code Assist", "gemini", true, false},
		{"auggie", "Augment (Auggie)", "auggie", false, false},
		{"amp", "Sourcegraph Amp", "amp", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preset, err := GetPreset(tt.name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if preset.Name != tt.name {
				t.Errorf("Name = %q, want %q", preset.Name, tt.name)
			}
			if preset.DisplayName != tt.displayName {
				t.Errorf("DisplayName = %q, want %q", preset.DisplayName, tt.displayName)
			}
			if preset.Command != tt.command {
				t.Errorf("Command = %q, want %q", preset.Command, tt.command)
			}
			if preset.MCPSupported != tt.mcp {
				t.Errorf("MCPSupported = %v, want %v", preset.MCPSupported, tt.mcp)
			}
			if preset.HooksSupported != tt.hooks {
				t.Errorf("HooksSupported = %v, want %v", preset.HooksSupported, tt.hooks)
			}
		})
	}
}

func TestGetPreset_NotFound(t *testing.T) {
	_, err := GetPreset("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent preset, got nil")
	}
}

func TestGetPreset_Custom(t *testing.T) {
	// Create a temporary config dir
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Write custom preset config
	configDir := filepath.Join(tmpDir, "thrum")
	if err := os.MkdirAll(configDir, 0750); err != nil {
		t.Fatal(err)
	}

	cfg := userPresetsConfig{
		CustomRuntimes: map[string]RuntimePreset{
			"custom-agent": {
				Name:        "custom-agent",
				DisplayName: "My Custom Agent",
				Command:     "my-agent",
			},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "runtimes.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	preset, err := GetPreset("custom-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preset.DisplayName != "My Custom Agent" {
		t.Errorf("DisplayName = %q, want %q", preset.DisplayName, "My Custom Agent")
	}
	if preset.Command != "my-agent" {
		t.Errorf("Command = %q, want %q", preset.Command, "my-agent")
	}
}

func TestGetPreset_CustomOverridesBuiltin(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	configDir := filepath.Join(tmpDir, "thrum")
	if err := os.MkdirAll(configDir, 0750); err != nil {
		t.Fatal(err)
	}

	// Override the claude preset
	cfg := userPresetsConfig{
		CustomRuntimes: map[string]RuntimePreset{
			"claude": {
				Name:        "claude",
				DisplayName: "Claude Custom",
				Command:     "claude-custom",
			},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(configDir, "runtimes.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	preset, err := GetPreset("claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preset.DisplayName != "Claude Custom" {
		t.Errorf("DisplayName = %q, want %q", preset.DisplayName, "Claude Custom")
	}
}

func TestListPresets_BuiltinOnly(t *testing.T) {
	// Point to a non-existent config to isolate built-ins
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	presets := ListPresets()
	if len(presets) < 6 {
		t.Errorf("expected at least 6 built-in presets, got %d", len(presets))
	}

	required := map[string]bool{
		"claude": false, "codex": false, "cursor": false,
		"gemini": false, "auggie": false, "amp": false,
	}
	for _, p := range presets {
		if _, ok := required[p.Name]; ok {
			required[p.Name] = true
		}
	}
	for name, found := range required {
		if !found {
			t.Errorf("missing required preset: %s", name)
		}
	}
}

func TestListPresets_Sorted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	presets := ListPresets()
	for i := 1; i < len(presets); i++ {
		if presets[i].Name < presets[i-1].Name {
			t.Errorf("presets not sorted: %q appears after %q", presets[i].Name, presets[i-1].Name)
		}
	}
}

func TestListPresets_Merged(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	configDir := filepath.Join(tmpDir, "thrum")
	if err := os.MkdirAll(configDir, 0750); err != nil {
		t.Fatal(err)
	}

	cfg := userPresetsConfig{
		CustomRuntimes: map[string]RuntimePreset{
			"my-agent": {
				Name:        "my-agent",
				DisplayName: "My Agent",
				Command:     "my-agent-cli",
			},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(configDir, "runtimes.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	presets := ListPresets()
	if len(presets) < 7 {
		t.Errorf("expected at least 7 presets (6 built-in + 1 custom), got %d", len(presets))
	}

	found := false
	for _, p := range presets {
		if p.Name == "my-agent" {
			found = true
			if p.DisplayName != "My Agent" {
				t.Errorf("custom preset DisplayName = %q, want %q", p.DisplayName, "My Agent")
			}
		}
	}
	if !found {
		t.Error("custom preset 'my-agent' not found in merged list")
	}
}

func TestSetDefaultRuntime(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	if err := SetDefaultRuntime("claude"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := GetDefaultRuntime()
	if got != "claude" {
		t.Errorf("GetDefaultRuntime() = %q, want %q", got, "claude")
	}
}

func TestSetDefaultRuntime_Invalid(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	err := SetDefaultRuntime("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent runtime, got nil")
	}
}

func TestRuntimePreset_JSON(t *testing.T) {
	preset := BuiltinPresets["claude"]

	data, err := json.Marshal(preset)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded RuntimePreset
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Name != preset.Name {
		t.Errorf("round-trip Name = %q, want %q", decoded.Name, preset.Name)
	}
	if decoded.MCPSupported != preset.MCPSupported {
		t.Errorf("round-trip MCPSupported = %v, want %v", decoded.MCPSupported, preset.MCPSupported)
	}
}
