package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	ttmux "github.com/leonletto/thrum/internal/tmux"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:5140-5805
// Destination: tmux.go:26-691
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func tmuxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tmux",
		Short: "Manage tmux sessions for agents",
	}

	// create
	createCmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a tmux session for an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := cmd.Flags().GetString("cwd")
			agentName, _ := cmd.Flags().GetString("name")
			role, _ := cmd.Flags().GetString("role")
			module, _ := cmd.Flags().GetString("module")
			intent, _ := cmd.Flags().GetString("intent")
			runtimeFlag, _ := cmd.Flags().GetString("runtime")
			noAgent, _ := cmd.Flags().GetBool("no-agent")
			force, _ := cmd.Flags().GetBool("force")

			// Hint pipeline: pre-action collection + gating runs BEFORE the
			// daemon connect. The pre-action hints (not-a-worktree,
			// session-exists, identity-exists) are FS-only by design — they
			// must fire even when the daemon is unreachable so users get
			// actionable guidance instead of a cryptic socket error. Use
			// FSOnlyStateAccessor here; the LiveStateAccessor below is for
			// post-action observations that legitimately need the RPC.
			hintFlags := map[string]any{"cwd": cwd, "force": force}
			preCtx := cli.HintCtx{
				Command: "tmux.create",
				Args:    args,
				Flags:   hintFlags,
				Post:    false,
				State:   cli.NewFSOnlyStateAccessor(),
			}
			preHints := cli.Collect(preCtx)
			if abortErr := cli.HandlePreAction(preHints, force); abortErr != nil {
				if flagJSON {
					// Emit abort body to stdout so agents can parse it,
					// then return a terse error for the exit code. Render
					// only the blocking hints for symmetry with the text
					// path (EmitAbort) — non-blocking info hints, if any,
					// aren't what caused the abort.
					var he *cli.HintAbortError
					var blockers []cli.Hint
					if errors.As(abortErr, &he) {
						blockers = he.Hints
					}
					body := map[string]any{
						"error":   "aborted by hint",
						"aborted": true,
					}
					if err := cli.EmitJSONWithHints(body, blockers); err != nil {
						return fmt.Errorf("render abort body: %w", err)
					}
					return fmt.Errorf("aborted")
				}
				return cli.EmitAbort(abortErr, flagQuiet, flagJSON)
			}

			// Pre-action checks passed; now connect to the daemon for the
			// actual mutation. Failure here is a real error (we already know
			// the request is otherwise sensible).
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			// LiveStateAccessor is needed for post-action hint collection
			// (session-exists liveness, identity-status liveness) which
			// legitimately needs the daemon view.
			state := cli.NewLiveStateAccessor(client)

			// Snapshot pre-state for the identity-replaced audit marker. Done
			// BEFORE the mutation so we can detect whether --force overwrote
			// a stale identity without re-reading (now-mutated) FS state.
			var replaceMarker cli.TmuxCreateResultMarker
			if force && cwd != "" {
				if status, preAgent, serr := state.IdentityStatus(cwd); serr == nil && status == cli.IdentityStale && preAgent != nil {
					replaceMarker = cli.TmuxCreateResultMarker{
						ReplacedStaleIdentity: true,
						ReplacedAgentName:     preAgent.AgentID,
					}
				}
			}

			result, err := cli.TmuxCreate(client, cli.TmuxCreateOptions{
				Name:      args[0],
				Cwd:       cwd,
				AgentName: agentName,
				Role:      role,
				Module:    module,
				Intent:    intent,
				Runtime:   runtimeFlag,
				Force:     force,
				NoAgent:   noAgent,
			})
			if err != nil {
				return err
			}

			// Post-action hint collection. preHints are also re-emitted at
			// this point — a warn+AllowForce=true hint that was force-cleared
			// is still useful context (e.g. 'session was replaced, FYI').
			postCtx := cli.HintCtx{
				Command: "tmux.create",
				Args:    args,
				Flags:   hintFlags,
				Post:    true,
				Result:  replaceMarker,
				State:   state,
			}
			postHints := cli.Collect(postCtx)
			allHints := append(preHints, postHints...) //nolint:gocritic // appendAssign: intentionally combining into new slice

			if flagJSON {
				if err := cli.EmitJSONWithHints(result, allHints); err != nil {
					return fmt.Errorf("render tmux create response: %w", err)
				}
			} else {
				fmt.Print(cli.FormatTmuxCreate(result))
				cli.EmitStderr(allHints, flagQuiet, flagJSON)
			}
			return nil
		},
	}
	createCmd.Flags().String("cwd", "", "Working directory for the session")
	_ = createCmd.MarkFlagRequired("cwd")
	createCmd.Flags().String("name", "", "Agent name for quickstart registration")
	createCmd.Flags().String("role", "", "Agent role for quickstart registration")
	createCmd.Flags().String("module", "", "Agent module for quickstart registration")
	createCmd.Flags().String("intent", "", "Agent intent")
	createCmd.Flags().String("runtime", "", "Preferred runtime")
	createCmd.Flags().Bool("no-agent", false, "Skip agent registration (create bare session)")
	createCmd.Flags().Bool("force", false, "Re-register even if agent exists; kill+recreate existing session")
	cmd.AddCommand(createCmd)

	// quickstart (alias for create)
	quickstartCmd := &cobra.Command{
		Use:   "quickstart <session-name>",
		Short: "Create a tmux session and register an agent (alias for 'tmux create')",
		Args:  cobra.ExactArgs(1),
		RunE:  createCmd.RunE,
	}
	quickstartCmd.Flags().AddFlagSet(createCmd.Flags())
	cmd.AddCommand(quickstartCmd)

	// launch
	launchCmd := &cobra.Command{
		Use:   "launch <name>",
		Short: "Start an AI tool inside a tmux session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rtOverride, _ := cmd.Flags().GetString("runtime")
			rt := "claude"
			// Resolution: --runtime flag > identity PreferredRuntime > config > "claude"
			out, err := exec.Command("tmux", "display-message", // #nosec G204 -- args are session name from CLI
				"-t", args[0], "-p", "#{pane_current_path}").Output()
			if err == nil {
				sessionCwd := strings.TrimSpace(string(out))
				thrumDir := filepath.Join(sessionCwd, ".thrum")
				if _, statErr := os.Stat(thrumDir); statErr == nil {
					cfg, _ := config.LoadThrumConfig(thrumDir)
					if cfg.Runtime.Primary != "" {
						rt = cfg.Runtime.Primary
					}
				}
				// Use LoadIdentityFromWorktree (not LoadIdentityWithPath) to bypass
				// THRUM_HOME/THRUM_NAME env vars from the calling shell. The launch
				// command resolves the target worktree's identity, not the caller's.
				if idFile, loadErr := config.LoadIdentityFromWorktree(sessionCwd); loadErr == nil && idFile != nil {
					if idFile.PreferredRuntime != "" {
						rt = idFile.PreferredRuntime
					}
				} else {
					return fmt.Errorf("no agent identity found in %s\n  Register first: thrum quickstart --name <agent> --role <role> --module <module>", sessionCwd)
				}
			}
			if rtOverride != "" {
				rt = rtOverride
			}
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.TmuxLaunch(client, cli.TmuxLaunchOptions{
				Name: args[0], Runtime: rt,
			})
			if err != nil {
				return err
			}
			if flagJSON {
				return cli.EmitJSON(result)
			}
			fmt.Printf("Launched %s in session %s\n", result.Runtime, result.Session)
			return nil
		},
	}
	launchCmd.Flags().String("runtime", "", "AI tool to launch (default: from config or claude)")
	cmd.AddCommand(launchCmd)

	// status (primary) + list (alias)
	statusCmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"list"},
		Short:   "Show tmux-managed sessions with state",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.TmuxStatus(client)
			if err != nil {
				return err
			}
			if flagJSON {
				return cli.EmitJSON(result)
			}
			fmt.Print(cli.FormatTmuxStatus(result))
			return nil
		},
	}
	cmd.AddCommand(statusCmd)

	// kill
	killCmd := &cobra.Command{
		Use:   "kill <name>",
		Short: "Tear down a tmux session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()
			if err := cli.TmuxKill(client, args[0]); err != nil {
				return err
			}
			if !flagQuiet {
				fmt.Printf("Session %s killed\n", args[0])
			}
			return nil
		},
	}
	cmd.AddCommand(killCmd)

	// send
	sendCmd := &cobra.Command{
		Use:   "send <name> <text>",
		Short: "Send text into a tmux session",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()
			return cli.TmuxSend(client, args[0], args[1])
		},
	}
	cmd.AddCommand(sendCmd)

	// capture
	captureCmd := &cobra.Command{
		Use:   "capture <name>",
		Short: "Capture pane content from a tmux session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lines, _ := cmd.Flags().GetInt("lines")
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.TmuxCapture(client, args[0], lines)
			if err != nil {
				return err
			}
			fmt.Print(result.Content)
			return nil
		},
	}
	captureCmd.Flags().Int("lines", 50, "Number of lines to capture")
	cmd.AddCommand(captureCmd)

	// check-pane (hidden — called by tmux silence hooks)
	checkPaneCmd := &cobra.Command{
		Use:    "check-pane <session>",
		Short:  "Check a tmux pane for permission prompts or idle state (called by tmux hooks)",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			session := args[0]
			target := session + ":0.0"

			content, err := ttmux.CapturePane(target, 5)
			if err != nil {
				return err
			}

			// Runtime resolution and permission-prompt detection both
			// live on the daemon side (HandleCheckPane). The CLI used to
			// load .thrum/identities/*.json from cwd to resolve runtime,
			// but tmux's alert-silence run-shell fires from the tmux
			// server's cwd — not the agent's worktree — so identity
			// lookup was unreliable. The daemon has authoritative
			// session → identity mapping via findIdentityForSession, so
			// we send only (session, content) and let the daemon handle
			// detection as a single source of truth.
			client, err := getClient()
			if err != nil {
				return nil // Daemon not running, silently skip
			}
			defer func() { _ = client.Close() }()

			req := map[string]string{
				"session": session,
				"content": content,
			}
			var result any
			_ = client.Call("tmux.check-pane", req, &result)
			return nil
		},
	}
	// --repo is kept as a flag for backward compatibility with baked-in
	// tmux hooks from older thrum binaries. The new CLI ignores it —
	// the daemon is the single source of truth for runtime resolution.
	checkPaneCmd.Flags().String("repo", "", "Repository path (deprecated — unused; daemon resolves identity)")
	cmd.AddCommand(checkPaneCmd)

	// connect
	connectCmd := &cobra.Command{
		Use:   "connect [name]",
		Short: "Attach to a running agent's tmux session",
		Long: `Attach to a running agent's tmux session.

With a session name argument, attaches directly.
Without arguments, shows a numbered list of alive sessions to choose from.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				// Direct attach by name
				return tmuxAttach(args[0])
			}

			// Interactive: list alive sessions and let user pick
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			result, err := cli.TmuxStatus(client)
			if err != nil {
				return err
			}

			// Filter to alive sessions only
			var alive []cli.TmuxSessionInfo
			for _, s := range result.Sessions {
				if s.State == "alive" {
					alive = append(alive, s)
				}
			}
			if len(alive) == 0 {
				fmt.Println("No alive tmux sessions")
				return nil
			}

			// Show numbered list
			fmt.Printf("%-4s %-25s %-20s %-10s %s\n", "#", "SESSION", "AGENT", "RUNTIME", "BRANCH")
			for i, s := range alive {
				agentDisplay := s.Agent
				if agentDisplay != "" {
					agentDisplay = "@" + agentDisplay
				}
				fmt.Printf("%-4d %-25s %-20s %-10s %s\n",
					i+1, s.Name, agentDisplay, s.Runtime, s.Branch)
			}

			// Read selection
			fmt.Printf("\nEnter number (1-%d): ", len(alive))
			scanner := bufio.NewScanner(os.Stdin)
			if !scanner.Scan() {
				return nil
			}
			input := strings.TrimSpace(scanner.Text())
			var choice int
			if _, err := fmt.Sscanf(input, "%d", &choice); err != nil || choice < 1 || choice > len(alive) {
				return fmt.Errorf("invalid selection: %s", input)
			}

			return tmuxAttach(alive[choice-1].Name)
		},
	}
	cmd.AddCommand(connectCmd)

	// restart
	tmuxRestartCmd := &cobra.Command{
		Use:   "restart <name>",
		Short: "Restart a tmux-managed agent session with context snapshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			rt, _ := cmd.Flags().GetString("runtime")

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			req := map[string]any{
				"name":  args[0],
				"force": force,
			}
			if rt != "" {
				req["runtime"] = rt
			}
			var result cli.TmuxRestartResponse
			if err := client.Call("tmux.restart", req, &result); err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(result)
			}
			fmt.Printf("Session %s restarted (%d snapshot lines)\n", result.Session, result.SnapshotLines)
			if result.SnapshotLines == 0 {
				fmt.Println("  ⚠ No conversation history captured — agent will start without prior context")
			}
			return nil
		},
	}
	tmuxRestartCmd.Flags().Bool("force", false, "Skip graceful signal, force restart")
	tmuxRestartCmd.Flags().String("runtime", "", "Runtime override (default: same as before)")
	cmd.AddCommand(tmuxRestartCmd)

	// start — one-command launch: create session, start runtime, prime, attach
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Launch an agent session in the current directory and attach",
		Long: `Creates a tmux session, launches the configured runtime (default: claude),
runs /thrum:prime for agent registration, and attaches to the session.

The session name is derived from the current directory name.
The runtime is read from the repo's config (runtime.primary), defaulting to claude.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			// Derive session name from directory basename
			sessionName := filepath.Base(cwd)
			nameOverride, _ := cmd.Flags().GetString("name")
			if nameOverride != "" {
				sessionName = nameOverride
			}

			// Check if session already exists
			if ttmux.HasSession(sessionName) {
				fmt.Printf("Session %s already exists — attaching\n", sessionName)
				return tmuxAttach(sessionName)
			}

			// Determine runtime: --runtime flag > identity PreferredRuntime > config > "claude"
			thrumDir := filepath.Join(cwd, ".thrum")
			runtime := "claude"
			if _, err := os.Stat(thrumDir); err == nil {
				cfg, _ := config.LoadThrumConfig(thrumDir)
				if cfg.Runtime.Primary != "" {
					runtime = cfg.Runtime.Primary
				}
			}
			if idFile, _, err := config.LoadIdentityWithPath(cwd); err == nil && idFile != nil {
				if idFile.PreferredRuntime != "" {
					runtime = idFile.PreferredRuntime
				}
			}
			rtOverride, _ := cmd.Flags().GetString("runtime")
			if rtOverride != "" {
				runtime = rtOverride
			}

			// Create session via daemon
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			if _, err := cli.TmuxCreate(client, cli.TmuxCreateOptions{
				Name: sessionName, Cwd: cwd, NoAgent: true,
			}); err != nil {
				return fmt.Errorf("create session: %w", err)
			}

			// Launch runtime
			if _, err := cli.TmuxLaunch(client, cli.TmuxLaunchOptions{
				Name: sessionName, Runtime: runtime,
			}); err != nil {
				return fmt.Errorf("launch runtime: %w", err)
			}

			fmt.Printf("Session %s created with %s — waiting for startup...\n", sessionName, runtime)

			// HandleLaunch sends the prime command via a background goroutine;
			// just wait for the runtime to initialize before attaching.
			time.Sleep(10 * time.Second)
			return tmuxAttach(sessionName)
		},
	}
	startCmd.Flags().String("name", "", "Override session name (default: directory name)")
	startCmd.Flags().String("runtime", "", "Override runtime (default: from config or claude)")
	cmd.AddCommand(startCmd)

	// queue
	queueCmd := &cobra.Command{
		Use:   "queue <session> <command>",
		Short: "Submit a command to a tmux session's queue",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			session := args[0]
			text := strings.Join(args[1:], " ")
			timeoutSecs, _ := cmd.Flags().GetInt("timeout")
			wait, _ := cmd.Flags().GetBool("wait")
			silenceSecs, _ := cmd.Flags().GetFloat64("silence")

			idFile, _, err := config.LoadIdentityWithPath(flagRepo)
			if err != nil || idFile == nil {
				return fmt.Errorf("resolve requester identity: %w", err)
			}
			requester := idFile.Agent.Name

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			opts := cli.TmuxQueueOptions{
				Session:   session,
				Text:      text,
				TimeoutMs: int64(timeoutSecs) * 1000,
				Requester: requester,
			}
			if silenceSecs > 0 {
				opts.SilenceMs = int64(silenceSecs * 1000)
			}
			if wait {
				// --wait mode: caller reads result from queue-wait response,
				// so suppress the @system inbox notification.
				f := false
				opts.NotifyOnComplete = &f
			}

			resp, err := cli.TmuxQueue(client, opts)
			if err != nil {
				return err
			}
			fmt.Printf("Queued %s (position %d)\n", resp.CommandID, resp.Position)

			if wait {
				// Long-poll for the result. Buffer the RPC timeout past the
				// queue own timeout so the socket deadline does not fire first.
				waitOpts := cli.TmuxQueueWaitOptions{
					CommandID: resp.CommandID,
					TimeoutMs: int64(timeoutSecs+10) * 1000,
				}
				result, err := cli.TmuxQueueWait(client, waitOpts)
				if err != nil {
					return err
				}
				fmt.Printf("State: %s\nElapsed: %dms\n\n%s\n", result.State, result.ElapsedMs, result.Output)
			}
			return nil
		},
	}
	queueCmd.Flags().Int("timeout", 120, "Command timeout in seconds")
	queueCmd.Flags().Bool("wait", false, "Block until the command reaches a terminal state")
	queueCmd.Flags().Float64("silence", 0, "Silence threshold in seconds (fractional OK; default 5.0 server-side)")
	cmd.AddCommand(queueCmd)

	// queue-status
	queueStatusCmd := &cobra.Command{
		Use:   "queue-status <session>",
		Short: "Show the command queue for a tmux session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			resp, err := cli.TmuxQueueStatus(client, args[0])
			if err != nil {
				return err
			}
			if flagJSON {
				return cli.EmitJSON(resp)
			}
			fmt.Printf("Session: %s\n", resp.Session)
			if resp.Active != nil {
				fmt.Printf("Active: %s \"%.40s\" (%s)\n", resp.Active.ID, resp.Active.Text, resp.Active.State)
			} else {
				fmt.Println("Active: (none)")
			}
			if len(resp.Queued) == 0 {
				fmt.Println("Queued: (empty)")
			} else {
				fmt.Printf("Queued: %d commands\n", len(resp.Queued))
				for i, q := range resp.Queued {
					fmt.Printf("  [%d] %s \"%.40s\"\n", i+1, q.ID, q.Text)
				}
			}
			return nil
		},
	}
	cmd.AddCommand(queueStatusCmd)

	// cancel
	cancelCmd := &cobra.Command{
		Use:   "cancel <command-id>",
		Short: "Cancel a queued or active command",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			resp, err := cli.TmuxCancel(client, args[0])
			if err != nil {
				return err
			}
			if flagJSON {
				return cli.EmitJSON(resp)
			}
			fmt.Printf("Canceled %s (state: %s)\n", resp.CommandID, resp.State)
			return nil
		},
	}
	cmd.AddCommand(cancelCmd)

	// snapshot — save/restore/check subcommands (moved from top-level 'restart')
	snapshotCmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage conversation snapshots for session restart",
	}
	for _, sub := range restartSnapshotSubcmds() {
		snapshotCmd.AddCommand(sub)
	}
	cmd.AddCommand(snapshotCmd)

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:5807-5812
// Destination: tmux.go:699-704
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 2
// Remove this ORIGIN marker once refactor verified green.
func tmuxAttach(session string) error {
	// Use safecmd.TmuxExec to replace the thrum process with tmux.
	// This makes the terminal see "tmux" as the process, which then
	// propagates session/window titles to the terminal tab correctly.
	return safecmd.TmuxExec("attach-session", "-t", session)
}
