package main

import (
	"fmt"
	"os"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/timeparse"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:1615-1720
// Destination: daemon_cmd.go:18-123
// Tests: cmd/thrum/main_test.go (indirect via Execute()); cmd/thrum/daemon_bootstrap_test.go (sibling, unaffected)
// Commit: 69a0f569a9
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func daemonCmd() *cobra.Command {
	var flagLocal bool
	var flagForce bool

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the Thrum daemon",
	}

	cmd.PersistentFlags().BoolVar(&flagLocal, "local", false,
		"Local-only mode: skip git push/fetch in sync loop")
	cmd.PersistentFlags().BoolVar(&flagForce, "force", false,
		"Proceed even when the repo directory is not git-anchored (G2 override)")

	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the daemon in the background",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.DaemonStart(flagRepo, flagLocal, flagForce); err != nil {
				return err
			}

			if !flagQuiet {
				if wsPort := cli.ReadWebSocketPort(flagRepo); wsPort > 0 {
					fmt.Printf("✓ Daemon started — http://localhost:%d\n", wsPort)
				} else {
					fmt.Println("✓ Daemon started successfully")
				}
			}

			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon gracefully",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.DaemonStop(flagRepo); err != nil {
				return err
			}

			if !flagQuiet {
				fmt.Println("✓ Daemon stopped successfully")
				fmt.Println("  All messaging commands will fail until the daemon is restarted:")
				fmt.Println("    thrum daemon start")
			}

			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := cli.DaemonStatus(flagRepo)
			if err != nil {
				return err
			}

			if flagJSON {
				if err := cli.EmitJSON(result); err != nil {
					return err
				}
			} else {
				// Human-readable formatted output
				fmt.Print(cli.FormatDaemonStatus(result))
			}

			// Exit code 1 when daemon is not running (like systemctl status).
			// In JSON mode, always exit 0 — the running status is in the JSON body.
			if !result.Running && !flagJSON {
				os.Exit(1)
			}

			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cli.DaemonRestart(flagRepo, flagLocal, flagForce); err != nil {
				return err
			}

			if !flagQuiet {
				if wsPort := cli.ReadWebSocketPort(flagRepo); wsPort > 0 {
					fmt.Printf("✓ Daemon restarted — http://localhost:%d\n", wsPort)
				} else {
					fmt.Println("✓ Daemon restarted successfully")
				}
			}

			return nil
		},
	})

	cmd.AddCommand(daemonRunCmd(&flagLocal, &flagForce))
	cmd.AddCommand(daemonLogsCmd())
	// Old tsync/peers commands removed — replaced by top-level "thrum peer" commands

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:1722-1758
// Destination: daemon_cmd.go:131-167
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 69a0f569a9
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func daemonLogsCmd() *cobra.Command {
	var (
		follow bool
		lines  int
		since  string
	)

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show daemon log output",
		Long: `Read the daemon log file at .thrum/var/daemon.log.

By default prints the last 50 lines. Use --follow/-f to stream new lines as
they are written. Use --since to filter by timestamp (e.g. "1h", "7d",
"2026-04-09", or RFC3339).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := cli.DaemonLogsOptions{
				Lines:  lines,
				Follow: follow,
			}
			if since != "" {
				t, err := timeparse.ParseBefore(since)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				opts.Since = &t
			}
			return cli.DaemonLogs(cmd.Context(), flagRepo, opts, os.Stdout)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Stream new log lines as they are written")
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "Number of lines to show (0 = all)")
	cmd.Flags().StringVar(&since, "since", "", "Only show lines at or after this time (e.g. 1h, 7d, 2026-04-09)")

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:1760-1769
// Destination: daemon_cmd.go:175-184
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: 69a0f569a9
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func daemonRunCmd(flagLocal *bool, flagForce *bool) *cobra.Command {
	return &cobra.Command{
		Use:    "run",
		Short:  "Run the daemon in the foreground (internal use)",
		Hidden: true, // Hidden from help - used internally by daemon start
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemon(flagRepo, *flagLocal, *flagForce)
		},
	}
}
