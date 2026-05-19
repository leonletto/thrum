package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:2123-2329
// Destination: monitor.go:19-225
// Tests: cmd/thrum/main_test.go (indirect via Execute()); internal/daemon/rpc/monitor_trust_boundary_test.go (Phase 3 hazard — RPC handlers still in runDaemon, unaffected by this Phase 1 CLI-surface move)
// Commit: 4217e1fc89
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func monitorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Manage long-running monitor jobs",
		Long: `Monitor runs a command, filters its stdout/stderr through a regex, and
delivers matching lines as thrum messages to the specified target.

Examples:
  thrum monitor add --name errors --match "ERROR" --to @team -- tail -F /tmp/app.log
  thrum monitor list
  thrum monitor show <id>
  thrum monitor stop <id>
  thrum monitor restart <id>`,
	}

	// thrum monitor add -- COMMAND ARGS...
	var addName, addMatch, addTo, addCwd string
	var addDebounce time.Duration
	var addEnv []string

	addCmd := &cobra.Command{
		Use:     "start -- COMMAND ARGS...",
		Aliases: []string{"add"},
		Short:   "Start a new monitor job",
		Long: `Start a monitor job that runs COMMAND, filters output through a regex,
and delivers matching lines as messages to the specified target.

The command and its arguments must be separated from monitor flags with '--':
  thrum monitor add --name errors --match "ERROR" --to @team -- tail -F /var/log/app.log`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Extract post-'--' argv using cobra's native mechanism.
			// ArgsLenAtDash() returns the index in args where '--' appeared.
			// If '--' was not present, it returns -1.
			dashPos := cmd.ArgsLenAtDash()
			if dashPos < 0 {
				return fmt.Errorf("monitor add requires a command after '--'\nExample: thrum monitor add --name x --match y --to @t -- /bin/cmd arg1")
			}
			argv := args[dashPos:]
			if len(argv) == 0 {
				return fmt.Errorf("monitor add requires at least one command token after '--'")
			}

			cwd := addCwd
			if cwd == "" {
				var err error
				cwd, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("get working directory: %w", err)
				}
			}

			env := make(map[string]string)
			for _, e := range addEnv {
				k, v, ok := strings.Cut(e, "=")
				if !ok {
					return fmt.Errorf("invalid --env %q: expected KEY=VALUE", e)
				}
				env[k] = v
			}

			req := cli.MonitorStartRequest{
				Name:            addName,
				Argv:            argv,
				Match:           addMatch,
				Target:          addTo,
				Cwd:             cwd,
				Env:             env,
				DebounceSeconds: int(addDebounce.Seconds()),
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.MonitorStart(client, req)
			if err != nil {
				return err
			}
			fmt.Printf("Started monitor %s (%s) — target %s\n", addName, result.ID, addTo)
			return nil
		},
	}
	addCmd.Flags().StringVar(&addName, "name", "", "Unique monitor name (required)")
	addCmd.Flags().StringVar(&addMatch, "match", "", "Regex pattern to filter output (required)")
	addCmd.Flags().StringVar(&addTo, "to", "", "Target agent or group for matched messages (required)")
	addCmd.Flags().StringVar(&addCwd, "cwd", "", "Working directory for the command (default: current directory)")
	addCmd.Flags().DurationVar(&addDebounce, "debounce", 60*time.Second, "Leading-edge debounce window (minimum 30s)")
	addCmd.Flags().StringArrayVar(&addEnv, "env", nil, "Environment variable in KEY=VALUE form (repeatable)")
	_ = addCmd.MarkFlagRequired("name")
	_ = addCmd.MarkFlagRequired("match")
	_ = addCmd.MarkFlagRequired("to")
	cmd.AddCommand(addCmd)

	// thrum monitor list [--all] [--json]
	{
		var includeAll bool
		listCmd := &cobra.Command{
			Use:   "list",
			Short: "List monitor jobs (default: running only; --all shows stopped/dead <1wk)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				client, err := getClient()
				if err != nil {
					return fmt.Errorf("connect to daemon: %w", err)
				}
				defer func() { _ = client.Close() }()
				if flagJSON {
					jobs, err := cli.MonitorListJSON(client, includeAll)
					if err != nil {
						return err
					}
					return cli.EmitJSON(jobs)
				}
				return cli.MonitorList(client, includeAll, os.Stdout)
			},
		}
		listCmd.Flags().BoolVar(&includeAll, "all", false,
			"Include stopped/dead monitors (younger than a week)")
		cmd.AddCommand(listCmd)
	}

	// thrum monitor show <id> [--json]
	cmd.AddCommand(&cobra.Command{
		Use:   "show <id>",
		Short: "Show details of a monitor job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()
			if flagJSON {
				job, err := cli.MonitorShowJSON(client, args[0])
				if err != nil {
					return err
				}
				return cli.EmitJSON(job)
			}
			return cli.MonitorShow(client, args[0], os.Stdout)
		},
	})

	// thrum monitor stop <id>
	cmd.AddCommand(&cobra.Command{
		Use:   "stop <id>",
		Short: "Stop and remove a monitor job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()
			if err := cli.MonitorStop(client, args[0]); err != nil {
				return err
			}
			fmt.Printf("Stopped monitor %s\n", args[0])
			return nil
		},
	})

	// thrum monitor restart <id>
	cmd.AddCommand(&cobra.Command{
		Use:   "restart <id>",
		Short: "Restart a monitor job (preserves the same ID)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()
			result, err := cli.MonitorRestart(client, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Restarted — ID: %s\n", result.ID)
			return nil
		},
	})

	// thrum monitor logs <id>
	{
		var logsLimit int
		logsCmd := &cobra.Command{
			Use:   "logs <id>",
			Short: "Show the most recent monitor matches (historical lookup)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				client, err := getClient()
				if err != nil {
					return fmt.Errorf("connect to daemon: %w", err)
				}
				defer func() { _ = client.Close() }()
				return cli.MonitorLogs(client, args[0], logsLimit, os.Stdout)
			},
		}
		logsCmd.Flags().IntVarP(&logsLimit, "limit", "n", 20, "Max number of matches to return")
		cmd.AddCommand(logsCmd)
	}

	return cmd
}
