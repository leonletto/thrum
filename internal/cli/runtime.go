package cli

import (
	"fmt"
	"strings"

	"github.com/leonletto/thrum/internal/runtime"
)

// RuntimeListResult contains the result of listing runtime presets.
type RuntimeListResult struct {
	Presets        []runtime.RuntimePreset `json:"presets"`
	DefaultRuntime string                  `json:"default_runtime,omitempty"`
}

// RuntimeList returns all runtime presets.
func RuntimeList() *RuntimeListResult {
	return &RuntimeListResult{
		Presets:        runtime.ListPresets(),
		DefaultRuntime: runtime.GetDefaultRuntime(),
	}
}

// RuntimeShow returns details for a specific runtime preset.
func RuntimeShow(name string) (*runtime.RuntimePreset, error) {
	preset, err := runtime.GetPreset(name)
	if err != nil {
		return nil, err
	}
	return &preset, nil
}

// RuntimeSetDefault sets the default runtime.
func RuntimeSetDefault(name string) error {
	return runtime.SetDefaultRuntime(name)
}

// FormatRuntimeList formats the runtime list for human-readable display.
func FormatRuntimeList(result *RuntimeListResult) string {
	var out strings.Builder

	// Separate built-in from custom
	var builtins, custom []runtime.RuntimePreset
	builtinNames := map[string]bool{
		"claude": true, "codex": true, "cursor": true,
		"gemini": true, "auggie": true, "amp": true,
	}
	for _, p := range result.Presets {
		if builtinNames[p.Name] {
			builtins = append(builtins, p)
		} else {
			custom = append(custom, p)
		}
	}

	out.WriteString("Built-in Runtimes:\n")
	for _, p := range builtins {
		features := formatFeatures(p)
		fmt.Fprintf(&out, "  %-10s %s (%s)\n", p.Name, p.DisplayName, features)
	}

	if len(custom) > 0 {
		out.WriteString("\nCustom Runtimes:\n")
		for _, p := range custom {
			features := formatFeatures(p)
			fmt.Fprintf(&out, "  %-10s %s (%s)\n", p.Name, p.DisplayName, features)
		}
	}

	if result.DefaultRuntime != "" {
		fmt.Fprintf(&out, "\nDefault: %s\n", result.DefaultRuntime)
	}

	return out.String()
}

// formatFeatures returns a compact feature string like "MCP ✓, Hooks ✓".
func formatFeatures(p runtime.RuntimePreset) string {
	var parts []string
	if p.MCPSupported {
		parts = append(parts, "MCP ✓")
	}
	if p.HooksSupported {
		parts = append(parts, "Hooks ✓")
	}
	if len(parts) == 0 {
		return "CLI-only"
	}
	return strings.Join(parts, ", ")
}

// FormatRuntimeShow formats a single runtime preset for human-readable display.
func FormatRuntimeShow(p *runtime.RuntimePreset) string {
	var out strings.Builder

	fmt.Fprintf(&out, "Name:             %s\n", p.Name)
	fmt.Fprintf(&out, "Display Name:     %s\n", p.DisplayName)
	fmt.Fprintf(&out, "Command:          %s\n", p.Command)
	fmt.Fprintf(&out, "MCP Supported:    %v\n", p.MCPSupported)
	fmt.Fprintf(&out, "Hooks Supported:  %v\n", p.HooksSupported)

	if p.InstructionsFile != "" {
		fmt.Fprintf(&out, "Instructions:     %s\n", p.InstructionsFile)
	}
	if p.MCPConfigPath != "" {
		fmt.Fprintf(&out, "MCP Config:       %s\n", p.MCPConfigPath)
	}
	if p.SetupNotes != "" {
		fmt.Fprintf(&out, "Setup Notes:      %s\n", p.SetupNotes)
	}

	return out.String()
}
