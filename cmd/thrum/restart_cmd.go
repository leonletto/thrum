package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/restart"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:9876-9884
// Destination: restart_cmd.go:21-29
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func restartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Moved to 'thrum tmux snapshot'",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("restart commands moved to 'thrum tmux snapshot save/restore/check'")
		},
	}
}

// ORIGIN[thrum-8kxh]: moved from main.go:9888-10062
// Destination: restart_cmd.go:39-213
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
// restartSnapshotSubcmds returns the save, restore, and check subcommands for
// use under 'thrum tmux snapshot'.
func restartSnapshotSubcmds() []*cobra.Command {
	saveCmd := &cobra.Command{
		Use:   "save",
		Short: "Save conversation snapshot for session restart",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve identity from local identity file first (avoids PID-based
			// daemon resolution which can find the wrong agent in multi-worktree setups)
			idFile, _, err := config.LoadIdentityWithPath(flagRepo)
			if err != nil {
				return fmt.Errorf("load identity file: %w", err)
			}
			agentName := idFile.Agent.Name
			sessionID := idFile.SessionID
			thrumDir := filepath.Join(flagRepo, ".thrum")

			pid := idFile.AgentPID
			if pid == 0 {
				// Fallback: query the daemon for the agent's AgentPID. The
				// daemon path is itself a best-effort fallback — if we
				// can't reach the daemon (dial failure, closed socket) we
				// fall through to the pid==0 refusal below rather than
				// returning an unrelated "connect to daemon" error. The
				// no-pid hint then renders with actionable remediation.
				if client, err := getClient(); err == nil {
					var agents []struct {
						AgentID  string `json:"agent_id"`
						AgentPID int    `json:"agent_pid"`
					}
					if err := client.Call("agent.list", nil, &agents); err == nil {
						for _, a := range agents {
							if a.AgentID == agentName && a.AgentPID > 0 {
								pid = a.AgentPID
								break
							}
						}
					}
					_ = client.Close()
				}
			}
			if pid == 0 {
				cli.EmitStderr([]cli.Hint{cli.SnapshotSaveNoPIDHint(agentName)}, flagQuiet, flagJSON)
				return fmt.Errorf("no agent PID found for %s — ensure agent is registered with an agent PID", agentName)
			}

			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home directory: %w", err)
			}
			claudeDir := filepath.Join(homeDir, ".claude")

			// Resolution order (thrum-ufv5.7):
			//   1. --jsonl <path> flag — explicit caller override, skip auto-detect.
			//   2. restart.FindSessionJSONL — PID-based lookup (primary path).
			//   3. restart.FindLatestJSONLForCwd — mtime fallback using the
			//      worktree path from the identity file. Covers the case where
			//      ~/.claude/sessions/<pid>.json is missing or stale but the
			//      project dir still has the current conversation JSONL.
			// If all three fail, emit the no-jsonl hint and return.
			var jsonlPath string
			if explicit, _ := cmd.Flags().GetString("jsonl"); explicit != "" {
				// Pre-flight: stat the explicit path so typos surface with a
				// targeted hint instead of being misdiagnosed as an extract
				// failure. ExtractConversation's own error would misdirect
				// toward permission/corruption remediation.
				if _, statErr := os.Stat(explicit); os.IsNotExist(statErr) {
					cli.EmitStderr([]cli.Hint{cli.SnapshotSaveJSONLNotFoundHint(explicit)}, flagQuiet, flagJSON)
					return fmt.Errorf("jsonl path not found: %s", explicit)
				}
				jsonlPath = explicit
			} else {
				jsonlPath, err = restart.FindSessionJSONL(claudeDir, pid)
				if err != nil {
					// Layer 3: mtime fallback. Track fallback diagnostics so
					// the no-jsonl hint can explain exactly WHY the fallback
					// didn't succeed (dir missing vs empty vs no worktree).
					noJSONLCtx := cli.SnapshotSaveNoJSONLContext{}
					if idFile.Worktree == "" {
						noJSONLCtx.WorktreeMissing = true
					} else {
						fb, ferr := restart.FindLatestJSONLForCwd(claudeDir, idFile.Worktree)
						if ferr == nil && fb != "" {
							jsonlPath = fb
						} else if ferr != nil {
							noJSONLCtx.ProjectDirReadErr = ferr
						} else {
							noJSONLCtx.ProjectDirEmpty = true
						}
					}
					if jsonlPath == "" {
						cli.EmitStderr([]cli.Hint{cli.SnapshotSaveNoJSONLHint(pid, claudeDir, noJSONLCtx)}, flagQuiet, flagJSON)
						return fmt.Errorf("find session JSONL: %w", err)
					}
				}
			}

			cfg, _ := config.LoadThrumConfig(thrumDir)
			maxLines := cfg.Restart.RestartMaxLines()

			conversation, err := restart.ExtractConversation(jsonlPath, maxLines)
			if err != nil {
				cli.EmitStderr([]cli.Hint{cli.SnapshotSaveExtractFailedHint(jsonlPath)}, flagQuiet, flagJSON)
				return fmt.Errorf("extract conversation: %w", err)
			}

			reason, _ := cmd.Flags().GetString("reason")
			if reason == "" {
				reason = "self-initiated"
			}

			snapshot := restart.FormatRestartSnapshot(agentName, sessionID, reason, conversation)
			if err := restart.SaveSnapshot(thrumDir, agentName, snapshot); err != nil {
				return err
			}

			lines := strings.Count(snapshot, "\n")
			if !flagQuiet {
				fmt.Printf("Restart snapshot saved for %s (%d lines)\n", agentName, lines)
			}
			return nil
		},
	}
	saveCmd.Flags().String("reason", "self-initiated", "Reason for restart")
	saveCmd.Flags().String("jsonl", "", "Explicit path to Claude conversation JSONL (bypasses auto-detect; use when ls ~/.claude/projects/<slug>/ shows the correct file)")

	restoreCmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore and output a restart snapshot",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			whoami, err := cli.AgentWhoami(client)
			if err != nil {
				return fmt.Errorf("resolve identity: %w", err)
			}

			thrumDir := filepath.Join(flagRepo, ".thrum")
			content, err := restart.Restore(thrumDir, whoami.AgentID)
			if err != nil {
				fmt.Fprintln(os.Stderr, "No restart snapshot found")
				os.Exit(1)
			}
			fmt.Print(content)
			return nil
		},
	}

	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "Check if a restart snapshot exists (exit 0 = yes, exit 1 = no)",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			whoami, err := cli.AgentWhoami(client)
			if err != nil {
				return fmt.Errorf("resolve identity: %w", err)
			}

			thrumDir := filepath.Join(flagRepo, ".thrum")
			if !restart.SnapshotExists(thrumDir, whoami.AgentID) {
				os.Exit(1)
			}
			return nil
		},
	}

	return []*cobra.Command{saveCmd, restoreCmd, checkCmd}
}
