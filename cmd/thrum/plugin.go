package main

import (
	"fmt"

	"github.com/leonletto/thrum/internal/backup"
	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:9350-9396
// Destination: plugin.go:19-65
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 184cf28e7f
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func pluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage backup plugins",
	}

	// plugin add
	var addName, addCommand, addPreset string
	var addIncludes []string
	addCmd := &cobra.Command{
		Use:   "add",
		Short: "Add a backup plugin",
		Long:  "Add a plugin by name/command/include or use --preset for built-in plugins.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginAdd(addName, addCommand, addIncludes, addPreset)
		},
	}
	addCmd.Flags().StringVar(&addName, "name", "", "Plugin name")
	addCmd.Flags().StringVar(&addCommand, "command", "", "Command to run before collecting files")
	addCmd.Flags().StringSliceVar(&addIncludes, "include", nil, "File patterns to collect (glob)")
	addCmd.Flags().StringVar(&addPreset, "preset", "", "Use built-in preset (beads, beads-rust)")
	cmd.AddCommand(addCmd)

	// plugin list
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginList()
		},
	})

	// plugin remove
	var removeName string
	removeCmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove a backup plugin",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginRemove(removeName)
		},
	}
	removeCmd.Flags().StringVar(&removeName, "name", "", "Plugin name to remove")
	_ = removeCmd.MarkFlagRequired("name")
	cmd.AddCommand(removeCmd)

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:9398-9435
// Destination: plugin.go:73-110
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 184cf28e7f
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func runPluginAdd(name, command string, includes []string, preset string) error {
	if preset != "" {
		p, ok := backup.PluginPresets[preset]
		if !ok {
			return fmt.Errorf("unknown preset %q (available: beads, beads-rust)", preset)
		}
		name = p.Name
		command = p.Command
		includes = p.Include
	}

	if name == "" {
		return fmt.Errorf("--name or --preset is required")
	}

	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	cfg.AddPlugin(config.PluginConfig{
		Name:    name,
		Command: command,
		Include: includes,
	})

	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("Plugin %q added.\n", name)
	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:9437-9466
// Destination: plugin.go:118-147
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 184cf28e7f
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func runPluginList() error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if len(cfg.Backup.Plugins) == 0 {
		fmt.Println("No plugins configured.")
		return nil
	}

	if flagJSON {
		return cli.EmitJSON(cfg.Backup.Plugins)
	}
	for _, p := range cfg.Backup.Plugins {
		fmt.Printf("  %s\n", p.Name)
		if p.Command != "" {
			fmt.Printf("    command: %s\n", p.Command)
		}
		if len(p.Include) > 0 {
			fmt.Printf("    include: %v\n", p.Include)
		}
	}
	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:9468-9489
// Destination: plugin.go:155-176
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 184cf28e7f
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func runPluginRemove(name string) error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if !cfg.RemovePlugin(name) {
		return fmt.Errorf("plugin %q not found", name)
	}

	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("Plugin %q removed.\n", name)
	return nil
}
