package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:1026-1165
// Destination: messaging.go:21-160
// Tests: cmd/thrum/main_test.go (indirect via Execute()); cmd/thrum/send_test.go (t698)
// Commit: a07612c4fb
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func sendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send MESSAGE",
		Short: "Send a message",
		Long: `Send a message to the Thrum messaging system.

Messages can include scopes (context), refs (references), and mentions.
The daemon must be running and you must have an active session.

A recipient flag is required (thrum-t698 — BREAKING CHANGE in v0.10.5):
  thrum send 'hello'  --to @coordinator_main    # directed send
  thrum send 'hello'  --broadcast                # explicit team fanout

Invoking 'thrum send' with no recipient flag is a hard error. The previous
default — silent broadcast to every team agent — was a footgun (the wider
the team, the easier to flood mid-cycle). Use --to @<agent_name> for the
common case, or --broadcast when an explicit team-wide announcement is
intended.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scopes, _ := cmd.Flags().GetStringSlice("scope")
			refs, _ := cmd.Flags().GetStringSlice("ref")
			mentions, _ := cmd.Flags().GetStringSlice("mention")
			structured, _ := cmd.Flags().GetString("structured")
			format, _ := cmd.Flags().GetString("format")
			to, _ := cmd.Flags().GetString("to")
			broadcast, _ := cmd.Flags().GetBool("broadcast")

			// thrum-t698: require an explicit recipient flag. The
			// previous default (silent broadcast when --to absent)
			// was a real footgun — coord live-demonstrated it during
			// Session 75 with an accidental 94-agent broadcast.
			// Convention (CLAUDE.md "send to specific names, never
			// role names") already says always --to; this aligns the
			// CLI default with the convention.
			if to == "" && !broadcast {
				return fmt.Errorf("thrum send: missing recipient. Did you intend to:\n  - send to a specific agent? Use --to @agent_name\n  - broadcast to the entire team? Use --broadcast")
			}
			// --broadcast desugars to the existing @everyone audience
			// the daemon already accepts. --to @everyone continues
			// to work as the explicit-keyword form. Mutual exclusivity
			// of --to + --broadcast is enforced by
			// MarkFlagsMutuallyExclusive (registered at cmd-build
			// time below the RunE closure; fires during arg parsing
			// before RunE runs, so we never observe both flags set
			// here).
			if broadcast {
				to = "@everyone"
			}

			opts := cli.SendOptions{
				Content:       args[0],
				Scopes:        scopes,
				Refs:          refs,
				Mentions:      mentions,
				Structured:    structured,
				Format:        format,
				To:            to,
				CallerAgentID: "", // set below
			}

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}
			opts.CallerAgentID = agentID

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			// Hint pipeline: pre-action collection only. Send has no
			// post-action hints in the pilot; recipient-stale is info
			// severity so HandlePreAction never blocks — but collecting
			// through the gate keeps the wiring symmetric with tmux.create.
			state := cli.NewLiveStateAccessor(client)
			preCtx := cli.HintCtx{
				Command: "send",
				Flags:   map[string]any{"to": to},
				Post:    false,
				State:   state,
			}
			preHints := cli.Collect(preCtx)
			if abortErr := cli.HandlePreAction(preHints, false); abortErr != nil {
				return cli.EmitAbort(abortErr, flagQuiet, flagJSON)
			}

			result, err := cli.Send(client, opts)
			if err != nil {
				return err
			}

			if flagJSON {
				if err := cli.EmitJSONWithHints(result, preHints); err != nil {
					return err
				}
			} else if !flagQuiet {
				// Human-readable output
				fmt.Printf("✓ Message sent: %s\n", result.MessageID)
				if result.ThreadID != "" {
					fmt.Printf("  Thread: %s\n", result.ThreadID)
				}
				fmt.Printf("  Created: %s\n", result.CreatedAt)
				if len(result.Audiences) > 0 {
					parts := make([]string, len(result.Audiences))
					for i, audience := range result.Audiences {
						parts[i] = audience.Type + ":" + audience.Value
					}
					fmt.Printf("  To: %s\n", strings.Join(parts, ", "))
				}
				if len(result.Recipients) > 0 {
					names := make([]string, len(result.Recipients))
					for i, recipient := range result.Recipients {
						names[i] = recipient.AgentID
					}
					fmt.Printf("  Recipients: %s\n", strings.Join(names, ", "))
				}
				for _, w := range result.Warnings {
					fmt.Fprintf(os.Stderr, "  warning: %s\n", w)
				}
				cli.EmitStderr(preHints, flagQuiet, flagJSON)
			}

			return nil
		},
	}

	cmd.Flags().StringSlice("scope", nil, "Add scope (repeatable, format: type:value)")
	cmd.Flags().StringSlice("ref", nil, "Add reference (repeatable, format: type:value)")
	cmd.Flags().StringSlice("mention", nil, "Mention a role (repeatable, format: @role)")
	cmd.Flags().String("structured", "", "Structured payload (JSON)")
	cmd.Flags().String("format", "markdown", "Message format (markdown, plain, json)")
	cmd.Flags().String("to", "", "Recipient (@agent_name or @everyone)")
	cmd.Flags().Bool("broadcast", false, "Fan out to the entire team (mutually exclusive with --to)")
	cmd.MarkFlagsMutuallyExclusive("to", "broadcast")

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:1167-1224
// Destination: messaging.go:168-225
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: a07612c4fb
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func sentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sent",
		Short: "List messages you sent",
		Long: `List messages authored by the current agent, including recipient snapshots
and durable read state.

Like inbox, sent supports filtering and pagination. Use 'thrum message get <id>'
to inspect a message with full recipient state.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			to, _ := cmd.Flags().GetString("to")
			unread, _ := cmd.Flags().GetBool("unread")
			pageSize, _ := cmd.Flags().GetInt("page-size")
			page, _ := cmd.Flags().GetInt("page")
			if cmd.Flags().Changed("limit") {
				pageSize, _ = cmd.Flags().GetInt("limit")
			}

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.MessageOutbox(client, cli.OutboxOptions{
				CallerAgentID: agentID,
				To:            to,
				Unread:        unread,
				PageSize:      pageSize,
				Page:          page,
			})
			if err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(result)
			}
			if !flagQuiet {
				fmt.Print(cli.FormatOutbox(result))
			}
			return nil
		},
	}

	cmd.Flags().Int("page-size", 10, "Results per page")
	cmd.Flags().Int("limit", 0, "Alias for --page-size")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().String("to", "", "Only sent messages addressed to this audience or recipient (format: @agent, @role, @group, @everyone)")
	cmd.Flags().Bool("unread", false, "Only sent messages with at least one unread recipient")

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:1229-1367
// Destination: messaging.go:233-371
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: a07612c4fb
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func inboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "List messages in your inbox",
		Long: `List messages in your inbox with filtering and pagination.

By default, inbox auto-filters to show messages addressed to you (via --to)
plus broadcasts and general messages. Use --all to see all messages.

The daemon must be running and you must have an active session.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, _ := cmd.Flags().GetString("scope")
			mentions, _ := cmd.Flags().GetBool("mentions")
			unread, _ := cmd.Flags().GetBool("unread")
			showAll, _ := cmd.Flags().GetBool("all")
			pageSize, _ := cmd.Flags().GetInt("page-size")
			page, _ := cmd.Flags().GetInt("page")
			fromAgent, _ := cmd.Flags().GetString("from")

			// --limit is an alias for --page-size
			if cmd.Flags().Changed("limit") {
				pageSize, _ = cmd.Flags().GetInt("limit")
			}

			// Strip optional leading @ on --from value.
			if len(fromAgent) > 0 && fromAgent[0] == '@' {
				fromAgent = fromAgent[1:]
			}

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}
			agentRole, err := resolveLocalMentionRole()
			if err != nil {
				return fmt.Errorf("failed to resolve agent role: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}

			opts := cli.InboxOptions{
				Scope:             scope,
				Mentions:          mentions,
				Unread:            unread,
				PageSize:          pageSize,
				Page:              page,
				CallerAgentID:     agentID,
				CallerMentionRole: agentRole,
				AuthorID:          fromAgent,
			}

			// Auto-filter: when identity is resolved and --all is not set,
			// show only messages addressed to this agent + broadcasts
			if !showAll && agentID != "" {
				opts.ForAgent = agentID
				opts.ForAgentRole = agentRole
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			// Validate --from agent exists. Mirrors --to behavior: fast-fail
			// with a clear error rather than silently returning an empty
			// inbox on a typo.
			if fromAgent != "" {
				agentsResp, listErr := cli.AgentList(client, cli.AgentListOptions{})
				if listErr != nil {
					return fmt.Errorf("validate --from @%s: %w", fromAgent, listErr)
				}
				known := false
				for _, a := range agentsResp.Agents {
					if a.AgentID == fromAgent {
						known = true
						break
					}
				}
				if !known {
					return fmt.Errorf("unknown agent: @%s (use 'thrum team --all' to see registered agents)", fromAgent)
				}
			}

			result, err := cli.Inbox(client, opts)
			if err != nil {
				return err
			}

			if flagJSON {
				if err := cli.EmitJSON(result); err != nil {
					return err
				}
			} else {
				// Human-readable formatted output with filter context
				fmtOpts := cli.InboxFormatOptions{
					ActiveScope: scope,
					ForAgent:    opts.ForAgent,
					Unread:      unread,
					Quiet:       flagQuiet,
					JSON:        flagJSON,
				}
				fmt.Print(cli.FormatInboxWithOptions(result, fmtOpts))
				// Suppress hint for --unread + empty (silent polling).
				if !flagQuiet && (!unread || len(result.Messages) != 0) {
					hintGroup := "inbox"
					if unread && len(result.Messages) > 0 {
						hintGroup = "inbox.unread"
					}
					fmt.Print(cli.LegacyHint(hintGroup, flagQuiet, flagJSON))
				}
			}

			// Auto mark-as-read: mark all displayed messages as read
			// Skip when --unread is set so agents can peek without consuming messages.
			if !unread && len(result.Messages) > 0 {
				ids := make([]string, len(result.Messages))
				for i, m := range result.Messages {
					ids[i] = m.MessageID
				}
				// Best-effort: don't fail the command if mark-read fails.
				// IDs come from the listing we just rendered — closed set,
				// no race surface; no watermark needed.
				_, _ = cli.MessageMarkRead(client, ids, agentID, "")
			}

			return nil
		},
	}

	cmd.Flags().String("scope", "", "Filter by scope (format: type:value)")
	cmd.Flags().Bool("mentions", false, "Only messages mentioning me")
	cmd.Flags().Bool("unread", false, "Only unread messages")
	cmd.Flags().BoolP("all", "a", false, "Show all messages (disable auto-filtering)")
	cmd.Flags().Int("page-size", 10, "Results per page")
	cmd.Flags().Int("limit", 0, "Alias for --page-size")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().String("from", "", "Filter inbox to messages from a specific agent (use @agent_name or agent_name)")

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:1404-1541
// Destination: messaging.go:379-516
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: a07612c4fb
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func waitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for notifications (for hooks)",
		Long: `Block until a matching message arrives or timeout occurs.

Useful for automation and hooks that need to wait for specific messages.

Use --after to filter by relative time (sign convention):
  -30s  = include messages sent up to 30 seconds ago  (negative = "N ago")
  -5m   = include messages sent up to 5 minutes ago   (negative = "N ago")
  +60s  = only messages arriving at least 60 seconds in the future (positive = "N from now")

When --after is not specified, defaults to "now" (only messages arriving after wait starts).

Exit codes:
  0 = message received
  1 = timeout
  2 = error`,
		RunE: func(cmd *cobra.Command, args []string) error {
			timeoutStr, _ := cmd.Flags().GetString("timeout")
			timeout, err := time.ParseDuration(timeoutStr)
			if err != nil {
				return fmt.Errorf("invalid timeout: %w", err)
			}

			scope, _ := cmd.Flags().GetString("scope")
			mention, _ := cmd.Flags().GetString("mention")
			afterStr, _ := cmd.Flags().GetString("after")

			// Parse --after relative time
			var afterTime time.Time
			if afterStr != "" {
				// Parse as relative duration from now
				// "-30s" = 30s ago, "30s" or "+30s" = 30s from now
				durationStr := afterStr
				negate := false
				if strings.HasPrefix(durationStr, "-") {
					negate = true
					durationStr = durationStr[1:]
				} else if strings.HasPrefix(durationStr, "+") {
					durationStr = durationStr[1:]
				}
				d, parseErr := time.ParseDuration(durationStr)
				if parseErr != nil {
					return fmt.Errorf("invalid --after duration %q: %w (examples: -30s, -5m, +60s)", afterStr, parseErr)
				}
				if negate {
					afterTime = time.Now().Add(-d)
				} else {
					afterTime = time.Now().Add(d)
				}
			} else {
				// Default: look back 1s to avoid race between sender and wait startup.
				// Without this, a message sent at the same instant as wait starts
				// would be filtered out by the created_after > threshold check.
				afterTime = time.Now().Add(-1 * time.Second)
			}

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}
			agentRole, err := resolveLocalMentionRole()
			if err != nil {
				return fmt.Errorf("failed to resolve agent role: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}

			opts := cli.WaitOptions{
				Timeout:       timeout,
				Scope:         scope,
				Mention:       mention,
				After:         afterTime,
				CallerAgentID: agentID,
				ForAgent:      agentID,
				ForAgentRole:  agentRole,
				Quiet:         flagQuiet || flagJSON,
			}

			// Resolve PID file path for spawn coordination
			agentName, _ := cmd.Flags().GetString("agent-name")
			if agentName == "" {
				agentName = os.Getenv("THRUM_AGENT_ID")
				if agentName == "" {
					agentName = os.Getenv("THRUM_NAME")
				}
			}
			if agentName != "" {
				thrumDir, err := paths.ResolveThrumDir(flagRepo)
				if err != nil {
					thrumDir = filepath.Join(flagRepo, ".thrum")
				}
				varDir := filepath.Join(thrumDir, "var")
				_ = os.MkdirAll(varDir, 0o750)
				opts.PIDFilePath = filepath.Join(varDir, agentName+"-listener.pid")
			}

			if flagVerbose && !afterTime.IsZero() {
				fmt.Fprintf(os.Stderr, "Listening for messages after %s\n", afterTime.Format(time.RFC3339))
			}

			socketPath := os.Getenv("THRUM_SOCKET")
			if socketPath == "" {
				socketPath = cli.DefaultSocketPath(flagRepo)
			}

			_, err = cli.Wait(socketPath, opts)
			if err != nil {
				if err.Error() == "timeout waiting for message" {
					if !flagQuiet {
						fmt.Fprintln(os.Stderr, "NO_MESSAGES_TIMEOUT — re-run thrum wait to continue listening")
					}
					os.Exit(1)
				}
				return err
			}

			if flagJSON {
				return cli.EmitJSON(map[string]string{
					"status": "received",
					"action": "ACTION REQUIRED: You have unread messages. Run `thrum inbox --unread` now to read and respond to them.",
				})
			}
			if !flagQuiet {
				fmt.Println("MESSAGES_RECEIVED")
			}
			return nil
		},
	}

	cmd.Flags().String("timeout", "30s", "Max wait time (e.g., 30s, 5m)")
	cmd.Flags().String("scope", "", "Filter by scope (format: type:value)")
	cmd.Flags().String("mention", "", "Wait for mentions of role (format: @role)")
	cmd.Flags().String("after", "", "Only return messages after this relative time (e.g., -30s, -5m, +60s)")
	cmd.Flags().String("agent-name", "", "Agent name for listener PID file (enables spawn coordination)")

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:1781-1837
// Destination: messaging.go:524-580
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: a07612c4fb
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func replyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reply MSG_ID TEXT",
		Short: "Reply to a message with same audience",
		Long: `Reply to a message, copying the parent message's audience (mentions/scopes).

The reply will include a reply_to reference to the parent message and will be sent
to the same recipients as the parent message.

Examples:
  thrum reply msg_01HXE... "Good idea, let's do that"
  thrum reply msg_01HXE... "Acknowledged" --format plain`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, _ := cmd.Flags().GetString("format")

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}

			opts := cli.ReplyOptions{
				MessageID:     args[0],
				Content:       args[1],
				Format:        format,
				CallerAgentID: agentID,
			}

			result, err := cli.Reply(client, opts)
			if err != nil {
				return err
			}

			// Auto mark-as-read: mark the replied-to message as read.
			// Single explicit ID; no race surface; no watermark needed.
			_, _ = cli.MessageMarkRead(client, []string{opts.MessageID}, agentID, "")

			if flagJSON {
				return cli.EmitJSON(result)
			}
			if !flagQuiet {
				fmt.Printf("✓ Reply sent: %s\n", result.MessageID)
			}
			return nil
		},
	}

	cmd.Flags().String("format", "markdown", "Message format (markdown, plain, json)")

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:1839-2079
// Destination: messaging.go:588-828
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: a07612c4fb
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func messageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "message",
		Short: "Manage individual messages",
	}

	getCmd := &cobra.Command{
		Use:   "get MSG_ID",
		Short: "Get a single message with full details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.MessageGet(client, args[0])
			if err != nil {
				return err
			}

			// Auto mark-as-read (best effort — don't fail if identity resolution fails)
			agentID, err := resolveLocalAgentID()
			if err != nil {
				if !flagQuiet {
					fmt.Fprintf(os.Stderr, "Warning: Could not mark as read (no identity): %v\n", err)
				}
			} else {
				// User named the exact ID; no race surface; no watermark.
				_, _ = cli.MessageMarkRead(client, []string{args[0]}, agentID, "")
			}

			if flagJSON {
				return cli.EmitJSON(result)
			}
			fmt.Print(cli.FormatMessageGet(result))
			return nil
		},
	}
	cmd.AddCommand(getCmd)

	editCmd := &cobra.Command{
		Use:   "edit MSG_ID TEXT",
		Short: "Edit a message (full replacement)",
		Long: `Edit a message by replacing its content entirely.

Only the message author can edit their own messages.

Examples:
  thrum message edit msg_01HXE... "Updated text here"`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w", err)
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.MessageEdit(client, args[0], args[1], agentID)
			if err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(result)
			}
			if !flagQuiet {
				fmt.Print(cli.FormatMessageEdit(result))
			}
			return nil
		},
	}
	cmd.AddCommand(editCmd)

	deleteCmd := &cobra.Command{
		Use:   "delete MSG_ID",
		Short: "Delete a message",
		Long: `Delete a message by ID.

Requires --force flag to confirm deletion.

Examples:
  thrum message delete msg_01HXE... --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			if !force {
				return fmt.Errorf("use --force to confirm deletion")
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			// Pass caller's claimed identity so the daemon's
			// shared-worktree disambiguation (thrum-0pos) can accept
			// the claim when peercred picks a co-located sibling.
			callerID, _ := resolveLocalAgentID()
			result, err := cli.MessageDelete(client, args[0], callerID)
			if err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(result)
			}
			if !flagQuiet {
				fmt.Print(cli.FormatMessageDelete(result))
			}
			return nil
		},
	}
	deleteCmd.Flags().Bool("force", false, "Confirm deletion")
	cmd.AddCommand(deleteCmd)

	readCmd := &cobra.Command{
		Use:   "read [MSG_ID...]",
		Short: "Mark messages as read",
		Long: `Mark one or more messages as read, or all unread messages with --all.

Examples:
  thrum message read msg_01HXE...
  thrum message read msg_01 msg_02 msg_03
  thrum message read --all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if !all && len(args) == 0 {
				return fmt.Errorf("requires at least 1 arg(s) or --all flag")
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			messageIDs := args
			if all {
				agentID, err := resolveLocalAgentID()
				if err != nil {
					return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
				}
				agentRole, err := resolveLocalMentionRole()
				if err != nil {
					return fmt.Errorf("failed to resolve agent role: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
				}

				// Capture watermark BEFORE listing. The daemon will skip any
				// supplied IDs whose created_at exceeds this when we mark,
				// so messages arriving during the list+mark gap stay unread
				// and remain visible on the next inbox check. The listing
				// itself intentionally does NOT receive the watermark — we
				// want the user to see everything that's arrived up to query
				// time; the watermark only guards what gets *marked*.
				markedBefore := time.Now().UTC().Format(time.RFC3339Nano)

				// Fetch all unread message IDs (capped at 100 per page)
				inboxResult, err := cli.Inbox(client, cli.InboxOptions{
					Unread:            true,
					PageSize:          100,
					CallerAgentID:     agentID,
					CallerMentionRole: agentRole,
				})
				if err != nil {
					return fmt.Errorf("failed to list unread messages: %w", err)
				}
				if len(inboxResult.Messages) == 0 {
					if !flagQuiet {
						fmt.Println("No unread messages.")
					}
					return nil
				}
				messageIDs = make([]string, len(inboxResult.Messages))
				for i, m := range inboxResult.Messages {
					messageIDs[i] = m.MessageID
				}

				result, err := cli.MessageMarkRead(client, messageIDs, agentID, markedBefore)
				if err != nil {
					return err
				}

				remaining := inboxResult.Unread - result.MarkedCount
				if flagJSON {
					return cli.EmitJSON(result)
				}
				if !flagQuiet {
					fmt.Print(cli.FormatMarkRead(result))
					if remaining > 0 {
						fmt.Printf("  %d unread messages remaining (run again to mark more)\n", remaining)
					}
					// Addendum A: late-arrival warning. SkippedCount counts
					// IDs the daemon refused via the marked_before watermark
					// — those are messages that arrived between when we
					// captured the watermark and when the daemon evaluated
					// the mark. They stay unread and the user should
					// re-check the inbox.
					if result.SkippedCount > 0 {
						msgWord := "messages"
						if result.SkippedCount == 1 {
							msgWord = "message"
						}
						fmt.Printf("  %d new %s arrived since you started reading — run `thrum inbox --unread` to see them.\n",
							result.SkippedCount, msgWord)
					}
				}
				return nil
			}

			agentID, err := resolveLocalAgentID()
			if err != nil {
				return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
			}
			// User provided the closed set of IDs; no race surface; no watermark.
			result, err := cli.MessageMarkRead(client, messageIDs, agentID, "")
			if err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(result)
			}
			if !flagQuiet {
				fmt.Print(cli.FormatMarkRead(result))
			}
			return nil
		},
	}
	readCmd.Flags().Bool("all", false, "Mark all unread messages as read")
	cmd.AddCommand(readCmd)

	return cmd
}
