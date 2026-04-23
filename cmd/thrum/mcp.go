package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/leonletto/thrum/internal/cli"
	thrummcp "github.com/leonletto/thrum/internal/mcp"
)

func mcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP server integration",
	}

	cmd.AddCommand(mcpServeCmd())
	return cmd
}

func mcpServeCmd() *cobra.Command {
	var agentID string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start MCP stdio server for agent messaging",
		Long: `Starts an MCP server on stdin/stdout for native tool-based messaging.

Requires the Thrum daemon to be running. Agents communicate via MCP tools
(send_message, check_messages, wait_for_message) instead of CLI shell-outs.

Configure in Claude Code's .claude/settings.json:
  {
    "mcpServers": {
      "thrum": {
        "type": "stdio",
        "command": "thrum",
        "args": ["mcp", "serve"]
      }
    }
  }`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPServe(agentID)
		},
	}

	cmd.Flags().StringVar(&agentID, "agent-id", "", "Override agent identity (selects .thrum/identities/{name}.json)")
	return cmd
}

func runMCPServe(agentID string) error {
	// Resolve repo path
	repoPath, err := filepath.Abs(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve repo path: %w", err)
	}

	// Set THRUM_NAME before config.LoadWithPath reads it.
	// Safe: this is a CLI process, not a library — no concurrent access.
	if agentID != "" {
		if err := os.Setenv("THRUM_NAME", agentID); err != nil {
			return fmt.Errorf("set THRUM_NAME: %w", err)
		}
	}

	// Check daemon is running before starting MCP server
	socketPath := cli.DefaultSocketPath(repoPath)
	client, err := cli.NewClient(socketPath)
	if err != nil {
		return fmt.Errorf("thrum daemon is not running. Start it with: thrum daemon start\n  (socket: %s)", socketPath)
	}

	var healthResult map[string]any
	if err := client.Call("health", nil, &healthResult); err != nil {
		_ = client.Close()
		return fmt.Errorf("thrum daemon is not responding. Restart with: thrum daemon start\n  (error: %w)", err)
	}
	_ = client.Close()

	// Create MCP server
	server, err := thrummcp.NewServer(repoPath, thrummcp.WithVersion(Version))
	if err != nil {
		return err
	}

	// Set up context with signal handling for clean shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Initialize WebSocket waiter for wait_for_message (best-effort)
	wsPort := cli.ReadWebSocketPort(repoPath)
	if wsPort > 0 {
		wsURL := fmt.Sprintf("ws://localhost:%d/ws", wsPort)
		if initErr := server.InitWaiter(ctx, wsURL); initErr != nil {
			// Log but continue — MCP server works without waiter
			fmt.Fprintf(os.Stderr, "Warning: WebSocket waiter not available (wait_for_message will fail): %v\n", initErr)
		}
	}

	// Run MCP server (blocks on stdio until client disconnects)
	return server.Run(ctx)
}
