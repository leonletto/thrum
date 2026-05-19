package main

import (
	"fmt"
	goruntime "runtime"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:1331-1355
// Destination: version_cmd.go:17-41
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 8bca6129d7
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func versionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show thrum version",
		Long:  `Display version information including version number, build hash, repository URL, and documentation URL.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagJSON {
				return cli.EmitJSON(map[string]string{
					"version":     Version,
					"build":       Build,
					"go_version":  goruntime.Version(),
					"repo_url":    "https://github.com/leonletto/thrum",
					"website_url": "https://thrum.team",
				})
			}
			// Human-readable output with OSC 8 hyperlinks
			// Format: ESC ] 8 ; ; URL ESC \ TEXT ESC ] 8 ; ; ESC \
			fmt.Printf("thrum v%s (build: %s, %s)\n", Version, Build, goruntime.Version())
			fmt.Printf("\x1b]8;;https://github.com/leonletto/thrum\x07https://github.com/leonletto/thrum\x1b]8;;\x07\n")
			fmt.Printf("\x1b]8;;https://thrum.team\x07https://thrum.team\x1b]8;;\x07\n")
			return nil
		},
	}
	return cmd
}
