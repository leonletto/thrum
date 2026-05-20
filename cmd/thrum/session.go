package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/types"
	"github.com/spf13/cobra"
)

// sessionStartRunE is the shared RunE for 'session start' and 'agent start'.
// ORIGIN[thrum-8kxh]: moved from main.go:3108-3153
// Destination: session.go:21-66
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: d03544a037
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func sessionStartRunE(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Get current agent ID from whoami
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
	}
	whoami, err := cli.AgentWhoami(client, agentID)
	if err != nil {
		return fmt.Errorf("failed to get agent identity: %w\n\nHint: Register first with 'thrum quickstart --name <name> --role <role> --module <module>'", err)
	}

	// Parse scope flags (optional for now, will be used in Epic 4)
	// scopes, _ := cmd.Flags().GetStringSlice("scope")
	// TODO: Parse scopes when Epic 4 is implemented

	opts := cli.SessionStartOptions{
		AgentID: whoami.AgentID,
	}

	// Auto-set worktree ref so heartbeat can extract git context.
	// Use flagRepo so the resolved worktree matches the caller's --repo
	// (or THRUM_HOME) — not the test-runner's CWD. With "." the call
	// would land on the process's actual working directory, which in
	// fixture tests is the parent thrum source tree and pollutes
	// session_refs with cross-agent collisions at the same git root.
	if worktreeRoot := cli.GitTopLevel(flagRepo); worktreeRoot != "" {
		opts.Refs = append(opts.Refs, types.Ref{Type: "worktree", Value: worktreeRoot})
	}

	result, err := cli.SessionStart(client, opts)
	if err != nil {
		return err
	}

	if flagJSON {
		return cli.EmitJSON(result)
	}
	fmt.Print(cli.FormatSessionStart(result))
	return nil
}

// sessionEndRunE is the shared RunE for 'session end' and 'agent end'.
// ORIGIN[thrum-8kxh]: moved from main.go:3156-3199
// Destination: session.go:75-118
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: d03544a037
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func sessionEndRunE(cmd *cobra.Command, args []string) error {
	reason, _ := cmd.Flags().GetString("reason")
	sessionID, _ := cmd.Flags().GetString("session-id")

	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	// If no session ID provided, get current session from whoami
	if sessionID == "" {
		agentID, err := resolveLocalAgentID()
		if err != nil {
			return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
		}
		whoami, err := cli.AgentWhoami(client, agentID)
		if err != nil {
			return fmt.Errorf("failed to get agent identity: %w", err)
		}

		if whoami.SessionID == "" {
			return fmt.Errorf("no active session to end")
		}

		sessionID = whoami.SessionID
	}

	opts := cli.SessionEndOptions{
		SessionID: sessionID,
		Reason:    reason,
	}

	result, err := cli.SessionEnd(client, opts)
	if err != nil {
		return err
	}

	if flagJSON {
		return cli.EmitJSON(result)
	}
	fmt.Print(cli.FormatSessionEnd(result))
	return nil
}

// sessionSetIntentRunE is the shared RunE for 'session set-intent' and 'agent set-intent'.
// ORIGIN[thrum-8kxh]: moved from main.go:3202-3242
// Destination: session.go:127-167
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: d03544a037
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func sessionSetIntentRunE(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Get current session from whoami
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
	}
	whoami, err := cli.AgentWhoami(client, agentID)
	if err != nil {
		return fmt.Errorf("failed to get agent identity: %w", err)
	}
	if whoami.SessionID == "" {
		return fmt.Errorf("no active session - start one with 'thrum session start'")
	}

	newIntent := args[0]
	result, err := cli.SessionSetIntent(client, whoami.SessionID, newIntent)
	if err != nil {
		return err
	}

	// Write intent back to identity file
	if idFile, _, loadErr := config.LoadIdentityWithPath(flagRepo); loadErr == nil {
		thrumDir := filepath.Join(flagRepo, ".thrum")
		idFile.Intent = newIntent
		_ = config.SaveIdentityFile(thrumDir, idFile)
	}

	if flagJSON {
		return cli.EmitJSON(result)
	}
	if !flagQuiet {
		fmt.Print(cli.FormatSetIntent(result))
	}
	return nil
}

// sessionSetTaskRunE is the shared RunE for 'session set-task' and 'agent set-task'.
// ORIGIN[thrum-8kxh]: moved from main.go:3245-3277
// Destination: session.go:176-208
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: d03544a037
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func sessionSetTaskRunE(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Get current session from whoami
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
	}
	whoami, err := cli.AgentWhoami(client, agentID)
	if err != nil {
		return fmt.Errorf("failed to get agent identity: %w", err)
	}
	if whoami.SessionID == "" {
		return fmt.Errorf("no active session - start one with 'thrum session start'")
	}

	result, err := cli.SessionSetTask(client, whoami.SessionID, args[0])
	if err != nil {
		return err
	}

	if flagJSON {
		return cli.EmitJSON(result)
	}
	if !flagQuiet {
		fmt.Print(cli.FormatSetTask(result))
	}
	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:3279-3410
// Destination: session.go:216-347
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: d03544a037
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage sessions",
	}

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start a new session",
		Long: `Start a new work session for the current agent.

The agent must be registered first (use 'thrum quickstart').
Starting a new session will automatically recover any orphaned sessions.`,
		RunE: sessionStartRunE,
	}
	// startCmd.Flags().StringSlice("scope", nil, "Session scope (repeatable, format: type:value)")
	cmd.AddCommand(startCmd)

	endCmd := &cobra.Command{
		Use:   "end",
		Short: "End current session",
		Long: `End the current active session.

Specify the session ID to end, or use 'thrum agent whoami' to find your current session.`,
		RunE: sessionEndRunE,
	}
	endCmd.Flags().String("reason", "normal", "End reason (normal|crash)")
	endCmd.Flags().String("session-id", "", "Session ID to end (defaults to current session)")
	cmd.AddCommand(endCmd)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List sessions",
		Long: `List all sessions (active and ended).

Use --active to show only active sessions.
Use --agent to filter by agent ID.

Examples:
  thrum session list
  thrum session list --active
  thrum session list --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			activeOnly, _ := cmd.Flags().GetBool("active")
			agentID, _ := cmd.Flags().GetString("agent")

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			opts := cli.SessionListOptions{
				AgentID:    agentID,
				ActiveOnly: activeOnly,
			}

			result, err := cli.SessionList(client, opts)
			if err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(result)
			}
			fmt.Print(cli.FormatSessionList(result))
			return nil
		},
	}
	listCmd.Flags().Bool("active", false, "Show only active sessions")
	listCmd.Flags().String("agent", "", "Filter by agent ID")
	cmd.AddCommand(listCmd)

	// heartbeat subcommand
	heartbeatCmd := &cobra.Command{
		Use:   "heartbeat",
		Short: "Send a session heartbeat",
		Long: `Send a heartbeat for the current session.

This triggers git context extraction and updates the agent's last-seen time.
Optionally add or remove scopes and refs.

Examples:
  thrum session heartbeat
  thrum session heartbeat --add-scope module:auth
  thrum session heartbeat --remove-ref pr:42`,
		RunE: sessionHeartbeatRunE,
	}
	heartbeatCmd.Flags().StringSlice("add-scope", nil, "Add scope (repeatable, format: type:value)")
	heartbeatCmd.Flags().StringSlice("remove-scope", nil, "Remove scope (repeatable, format: type:value)")
	heartbeatCmd.Flags().StringSlice("add-ref", nil, "Add ref (repeatable, format: type:value)")
	heartbeatCmd.Flags().StringSlice("remove-ref", nil, "Remove ref (repeatable, format: type:value)")
	cmd.AddCommand(heartbeatCmd)

	// set-intent subcommand
	cmd.AddCommand(&cobra.Command{
		Use:   "set-intent TEXT",
		Short: "Set session intent (what you're working on)",
		Long: `Set the work intent for the current session.

This is a free-text description of what the agent is currently working on.
It appears in 'thrum agent list --context'.
Pass an empty string to clear the intent.

Examples:
  thrum session set-intent "Fixing memory leak in connection pool"
  thrum session set-intent "Refactoring login flow"
  thrum session set-intent ""   # clear intent`,
		Args: cobra.ExactArgs(1),
		RunE: sessionSetIntentRunE,
	})

	// set-task subcommand
	cmd.AddCommand(&cobra.Command{
		Use:   "set-task TASK",
		Short: "Set current task (e.g., beads issue ID)",
		Long: `Set the current task identifier for the session.

This links the session to a task tracker (e.g., beads issue).
It appears in 'thrum agent list --context'.
Pass an empty string to clear the task.

Examples:
  thrum session set-task beads:thrum-xyz
  thrum session set-task "JIRA-1234"
  thrum session set-task ""   # clear task`,
		Args: cobra.ExactArgs(1),
		RunE: sessionSetTaskRunE,
	})

	return cmd
}

// sessionHeartbeatRunE is the shared RunE for 'session heartbeat' and 'agent heartbeat'.
// ORIGIN[thrum-8kxh]: moved from main.go:4448-4523
// Destination: session.go:356-431
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: d03544a037
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func sessionHeartbeatRunE(cmd *cobra.Command, args []string) error {
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Get current session from whoami
	agentID, err := resolveLocalAgentID()
	if err != nil {
		return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
	}
	whoami, err := cli.AgentWhoami(client, agentID)
	if err != nil {
		return fmt.Errorf("failed to get agent identity: %w", err)
	}
	if whoami.SessionID == "" {
		return fmt.Errorf("no active session - start one with 'thrum session start'")
	}

	// Parse scope/ref flags
	addScopes, _ := cmd.Flags().GetStringSlice("add-scope")
	removeScopes, _ := cmd.Flags().GetStringSlice("remove-scope")
	addRefs, _ := cmd.Flags().GetStringSlice("add-ref")
	removeRefs, _ := cmd.Flags().GetStringSlice("remove-ref")

	opts := cli.HeartbeatOptions{
		SessionID: whoami.SessionID,
	}

	// Parse scopes (type:value format)
	for _, s := range addScopes {
		parts := strings.SplitN(s, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid scope format %q, expected type:value", s)
		}
		opts.AddScopes = append(opts.AddScopes, types.Scope{Type: parts[0], Value: parts[1]})
	}
	for _, s := range removeScopes {
		parts := strings.SplitN(s, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid scope format %q, expected type:value", s)
		}
		opts.RemoveScopes = append(opts.RemoveScopes, types.Scope{Type: parts[0], Value: parts[1]})
	}

	// Parse refs (type:value format)
	for _, r := range addRefs {
		parts := strings.SplitN(r, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid ref format %q, expected type:value", r)
		}
		opts.AddRefs = append(opts.AddRefs, types.Ref{Type: parts[0], Value: parts[1]})
	}
	for _, r := range removeRefs {
		parts := strings.SplitN(r, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid ref format %q, expected type:value", r)
		}
		opts.RemoveRefs = append(opts.RemoveRefs, types.Ref{Type: parts[0], Value: parts[1]})
	}

	result, err := cli.SessionHeartbeat(client, opts)
	if err != nil {
		return err
	}

	if flagJSON {
		return cli.EmitJSON(result)
	}
	fmt.Print(cli.FormatHeartbeat(result))
	if !flagQuiet {
		fmt.Print(cli.LegacyHint("session.heartbeat", flagQuiet, flagJSON))
	}
	return nil
}
