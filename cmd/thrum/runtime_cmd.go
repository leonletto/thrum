package main

import (
	"fmt"
	"path/filepath"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:4939-5010
// Destination: runtime_cmd.go:18-89
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: ccad4a6acc
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func runtimeGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Manage runtime presets",
		Long: `Manage AI coding runtime presets.

Thrum supports multiple AI coding runtimes (Claude, Codex, Cursor,
Gemini, Auggie, Amp). Each runtime has a preset with configuration
defaults. Use these commands to list, inspect, and configure runtimes.`,
	}

	// thrum runtime list
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all runtime presets",
		RunE: func(cmd *cobra.Command, args []string) error {
			result := cli.RuntimeList()

			if flagJSON {
				return cli.EmitJSON(result)
			}
			fmt.Print(cli.FormatRuntimeList(result))
			return nil
		},
	}

	// thrum runtime show <name>
	showCmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show details for a runtime preset",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			preset, err := cli.RuntimeShow(args[0])
			if err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(preset)
			}
			fmt.Print(cli.FormatRuntimeShow(preset))
			return nil
		},
	}

	// thrum runtime set-default <name>
	setDefaultCmd := &cobra.Command{
		Use:   "set-default <name>",
		Short: "Set the default runtime preset",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.RuntimeSetDefault(args[0]); err != nil {
				return err
			}
			// Also update the repo-level config so 'config show' reflects the change.
			thrumDir := filepath.Join(flagRepo, ".thrum")
			cfg, err := config.LoadThrumConfig(thrumDir)
			if err == nil {
				cfg.Runtime.Primary = args[0]
				_ = config.SaveThrumConfig(thrumDir, cfg)
			}
			fmt.Printf("✓ Default runtime set to: %s\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(listCmd)
	cmd.AddCommand(showCmd)
	cmd.AddCommand(setDefaultCmd)

	return cmd
}
