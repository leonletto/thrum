package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/runtime"
)

func TestRuntimeList_HumanReadable(t *testing.T) {
	// Isolate from user config
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	result := RuntimeList()
	output := FormatRuntimeList(result)

	// Should contain all built-in runtimes
	for _, name := range []string{"claude", "codex", "cursor", "gemini", "auggie", "amp"} {
		if !strings.Contains(output, name) {
			t.Errorf("output missing runtime %q:\n%s", name, output)
		}
	}

	// Should have "Built-in Runtimes:" header
	if !strings.Contains(output, "Built-in Runtimes:") {
		t.Errorf("output missing 'Built-in Runtimes:' header:\n%s", output)
	}
}

func TestRuntimeList_JSON(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	result := RuntimeList()

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded RuntimeListResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(decoded.Presets) < 6 {
		t.Errorf("expected at least 6 presets, got %d", len(decoded.Presets))
	}
}

func TestRuntimeShow_HumanReadable(t *testing.T) {
	preset, err := RuntimeShow("claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := FormatRuntimeShow(preset)

	checks := []string{
		"Name:",
		"claude",
		"Display Name:",
		"Claude Code",
		"MCP Supported:",
		"true",
		"Hooks Supported:",
		"true",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output missing %q:\n%s", check, output)
		}
	}
}

func TestRuntimeShow_JSON(t *testing.T) {
	preset, err := RuntimeShow("claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := json.Marshal(preset)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded runtime.RuntimePreset
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Name != "claude" {
		t.Errorf("Name = %q, want %q", decoded.Name, "claude")
	}
	if decoded.MCPSupported != true {
		t.Errorf("MCPSupported = %v, want true", decoded.MCPSupported)
	}
}

func TestRuntimeShow_NotFound(t *testing.T) {
	_, err := RuntimeShow("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent runtime, got nil")
	}
}

func TestRuntimeSetDefault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	if err := RuntimeSetDefault("claude"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := RuntimeList()
	if result.DefaultRuntime != "claude" {
		t.Errorf("DefaultRuntime = %q, want %q", result.DefaultRuntime, "claude")
	}
}

func TestRuntimeSetDefault_Invalid(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	err := RuntimeSetDefault("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent runtime, got nil")
	}
}

func TestFormatFeatures(t *testing.T) {
	tests := []struct {
		name     string
		preset   runtime.RuntimePreset
		expected string
	}{
		{
			name:     "MCP and hooks",
			preset:   runtime.RuntimePreset{MCPSupported: true, HooksSupported: true},
			expected: "MCP ✓, Hooks ✓",
		},
		{
			name:     "MCP only",
			preset:   runtime.RuntimePreset{MCPSupported: true},
			expected: "MCP ✓",
		},
		{
			name:     "CLI-only",
			preset:   runtime.RuntimePreset{},
			expected: "CLI-only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatFeatures(tt.preset)
			if got != tt.expected {
				t.Errorf("formatFeatures() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestRuntimeList_WithCustomPresets(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	configDir := filepath.Join(tmpDir, "thrum")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := map[string]any{
		"custom_runtimes": map[string]any{
			"my-agent": map[string]any{
				"name":         "my-agent",
				"display_name": "My Custom Agent",
				"command":      "my-agent-cli",
			},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(configDir, "runtimes.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	result := RuntimeList()
	output := FormatRuntimeList(result)

	if !strings.Contains(output, "Custom Runtimes:") {
		t.Errorf("output missing 'Custom Runtimes:' header:\n%s", output)
	}
	if !strings.Contains(output, "my-agent") {
		t.Errorf("output missing custom runtime 'my-agent':\n%s", output)
	}
}
