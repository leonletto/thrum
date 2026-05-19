package main

import (
	"fmt"
	"strings"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:5465-5515
// Destination: presence.go:17-67
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func overviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "overview",
		Short: "Show combined status, team, and inbox view",
		Long: `Show a comprehensive overview of your agent, team, and inbox.

Combines identity, work context, team activity, inbox counts,
and sync status into a single orientation view.

Examples:
  thrum overview
  thrum overview --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Always emit the legacy 'Tip:' trailer in text mode, even when
			// the daemon connect or RPC fails. The L5 contract is that
			// commands wired to cli.LegacyHint produce a tip on stderr-or-
			// stdout regardless of the action outcome — useful guidance
			// (e.g. "next, try X") still helps the user when the daemon is
			// down. JSON / quiet modes keep the structured-output contract.
			defer func() {
				if !flagJSON && !flagQuiet {
					fmt.Print(cli.LegacyHint("overview", flagQuiet, flagJSON))
				}
			}()

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}
			result, err := cli.Overview(client, agentID)
			if err != nil {
				return err
			}

			// Add WebSocket port for UI URL display
			result.WebSocketPort = cli.ReadWebSocketPort(flagRepo)

			if flagJSON {
				return cli.EmitJSON(result)
			}
			fmt.Print(cli.FormatOverview(result))
			return nil
		},
	}
}

// ORIGIN[thrum-8kxh]: moved from main.go:5517-5648
// Destination: presence.go:75-206
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func teamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team [@AGENT]",
		Short: "Show status of all active agents",
		Long: `Show a rich, multi-line status report for every active agent.

Displays session info, work context, inbox counts, branch status,
and per-file change details for all agents with active sessions.

With a positional @AGENT argument the output switches to the expanded
single-agent view (per spec §7.6) showing the body fallback chain.

Examples:
  thrum team
  thrum team --all
  thrum team --system
  thrum team --json
  thrum team @docs_bot
  thrum team @docs_bot --journal
  thrum team @docs_bot --files`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			includeAll, _ := cmd.Flags().GetBool("all")
			includeSystem, _ := cmd.Flags().GetBool("system")
			showJournal, _ := cmd.Flags().GetBool("journal")
			showFiles, _ := cmd.Flags().GetBool("files")

			// Extract the @<name> positional. Leading "@" is optional
			// for symmetry with `thrum send --to @name` (which accepts
			// either form); the daemon AgentFilter matches the AgentID
			// without the prefix.
			var agentFilter string
			if len(args) == 1 {
				agentFilter = strings.TrimPrefix(args[0], "@")
				if agentFilter == "" {
					return fmt.Errorf("agent name cannot be empty (got %q)", args[0])
				}
			}

			if agentFilter == "" && (showJournal || showFiles) {
				return fmt.Errorf("--journal and --files require a single-agent argument (e.g. 'thrum team @docs_bot --journal')")
			}

			req := cli.TeamListRequest{
				IncludeOffline: includeAll,
				IncludeSystem:  includeSystem,
				AgentFilter:    agentFilter,
			}

			var result cli.TeamListResponse
			if err := client.Call("team.list", req, &result); err != nil {
				return fmt.Errorf("team.list RPC failed: %w", err)
			}

			if agentFilter != "" && len(result.Members) == 0 {
				return fmt.Errorf("agent %q not found (use 'thrum team' to list active agents)", agentFilter)
			}

			// Optional sections — fetched only for the single-agent
			// expanded view. Errors collected and surfaced inline so
			// the operator sees a partial view rather than nothing.
			var journalResp *cli.JournalResponse
			var filesPaths []string
			var filesAvailable bool

			if agentFilter != "" && showJournal {
				var j cli.JournalResponse
				if err := client.Call("team.journal", cli.JournalRequest{AgentName: agentFilter}, &j); err != nil {
					return fmt.Errorf("team.journal RPC failed: %w", err)
				}
				journalResp = &j
			}

			if agentFilter != "" && showFiles {
				paths, available, err := probeAgentListFiles(client, agentFilter)
				if err != nil {
					return fmt.Errorf("agent.listFiles probe failed: %w", err)
				}
				filesPaths = paths
				filesAvailable = available
			}

			if flagJSON {
				out := map[string]any{
					"team": result,
				}
				if journalResp != nil {
					out["journal"] = journalResp
				}
				// Guard on both flag AND agentFilter — symmetric with
				// the probe gate above. The early input-validation
				// rejects `--files` without `@AGENT`, but duplicating
				// the guard here keeps the JSON shape honest if that
				// early-rejection ever changes.
				if showFiles && agentFilter != "" {
					out["files"] = map[string]any{
						"available": filesAvailable,
						"paths":     filesPaths,
					}
				}
				return cli.EmitJSON(out)
			}

			if agentFilter != "" {
				fmt.Print(cli.FormatTeamExpanded(&result.Members[0]))
				if showFiles {
					fmt.Print(cli.FormatFilesSection(filesPaths, filesAvailable))
				}
				if journalResp != nil {
					fmt.Print(cli.FormatJournalSection(journalResp))
				}
				return nil
			}

			fmt.Print(cli.FormatTeam(&result))
			return nil
		},
	}

	cmd.Flags().Bool("all", false, "Include offline agents")
	cmd.Flags().Bool("system", false, "Include reserved pseudo-agents (@supervisor_*, etc.)")
	cmd.Flags().Bool("journal", false, "Append the agent's lifecycle journal (requires @AGENT)")
	cmd.Flags().Bool("files", false, "List files in the agent's state folder (requires @AGENT)")

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:5656-5679
// Destination: presence.go:220-243
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// probeAgentListFiles calls agent.listFiles for the target agent and
// returns its file paths. When the daemon doesn't register that RPC
// (cross-epic MB-1.S2 Q10 not yet shipped), the JSON-RPC server
// returns a "method not found" error; we catch that as
// available=false so the CLI renders FilesRPCUnavailable instead of
// failing. Any other error propagates.
func probeAgentListFiles(client *cli.Client, agentName string) ([]string, bool, error) {
	// Cross-epic dep: response shape is provisional pending MB-1.S2
	// Q10. The probe accepts whatever the daemon returns under the
	// "paths" / "files" keys and ignores the rest. Until the daemon
	// registers agent.listFiles, the catch-all returns "available=false"
	// based on the method-not-found error string.
	var raw map[string]any
	if err := client.Call("agent.listFiles", map[string]string{"agent_name": agentName}, &raw); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "Method not found") || strings.Contains(msg, "method not found") || strings.Contains(msg, "-32601") {
			return nil, false, nil
		}
		return nil, false, err
	}
	var paths []string
	if pv, ok := raw["paths"].([]any); ok {
		for _, p := range pv {
			if s, ok := p.(string); ok {
				paths = append(paths, s)
			}
		}
	}
	return paths, true, nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:5681-5718
// Destination: presence.go:251-288
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func whoHasCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "who-has FILE",
		Short: "Check which agents are editing a file",
		Long: `Check which agents are currently editing a file.

Shows agents with the file in their uncommitted changes or changed files,
along with branch and change count information.

Examples:
  thrum who-has auth.go
  thrum who-has internal/cli/agent.go`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := args[0]

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.AgentListContext(client, "", "", file)
			if err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(result)
			}
			fmt.Print(cli.FormatWhoHas(file, result))
			if !flagQuiet {
				fmt.Print(cli.LegacyHint("who-has", flagQuiet, flagJSON))
			}
			return nil
		},
	}
}

// ORIGIN[thrum-8kxh]: moved from main.go:5720-5769
// Destination: presence.go:296-345
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func pingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ping AGENT",
		Short: "Check if an agent is online",
		Long: `Check the presence status of an agent.

Shows whether the agent is active or offline, along with their current
intent, task, and branch information if active.

The agent can be specified with or without the @ prefix.

Examples:
  thrum ping @reviewer
  thrum ping planner`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimPrefix(args[0], "@")

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			// Get agent list to find the agent
			agents, err := cli.AgentList(client, cli.AgentListOptions{})
			if err != nil {
				return err
			}

			// Get contexts for active status
			contexts, err := cli.AgentListContext(client, "", "", "")
			if err != nil {
				contexts = nil // Non-fatal, show what we can
			}

			if flagJSON {
				return cli.EmitJSON(map[string]any{
					"agents":   agents,
					"contexts": contexts,
				})
			}
			fmt.Print(cli.FormatPing(name, agents, contexts))
			if !flagQuiet {
				fmt.Print(cli.LegacyHint("ping", flagQuiet, flagJSON))
			}
			return nil
		},
	}
}
