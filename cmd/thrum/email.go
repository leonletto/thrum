package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/spf13/cobra"
)

// emailCmd assembles the `thrum email` parent command and all 8 sub-commands.
func emailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "email",
		Short: "Manage the email bridge",
	}
	cmd.AddCommand(emailInitCmd())
	cmd.AddCommand(emailPairCmd())
	cmd.AddCommand(emailListCmd())
	cmd.AddCommand(emailRevokeCmd())
	cmd.AddCommand(emailRebindCmd())
	cmd.AddCommand(emailStatusCmd())
	cmd.AddCommand(emailUnblockCmd())
	cmd.AddCommand(emailSendCmd())
	return cmd
}

// --- pre-flight helper ---

// emailClientOrHint attempts to connect to the daemon. On failure it emits a
// hint to stderr via the existing hint system and returns nil so the caller
// can bail early without printing a raw connection error.
func emailClientOrHint() *cli.Client {
	client, err := getClient()
	if err != nil {
		hint := cli.Hint{
			Code:     "email.daemon-offline",
			Severity: cli.SeverityWarn,
			Message:  "Thrum daemon is not running. Start it with: thrum daemon start",
			Options: []cli.Option{
				{Label: "start", Cmd: "thrum daemon start"},
			},
		}
		cli.EmitStderr([]cli.Hint{hint}, flagQuiet, flagJSON)
		return nil
	}
	return client
}

// --- email init ---

// emailInitProviderDefaults maps provider shorthand to IMAP/SMTP defaults.
// Custom provider starts at zero-value and accepts explicit flags.
type emailInitProviderDefaults struct {
	imapHost string
	imapPort int
	smtpHost string
	smtpPort int
}

var emailProviderPresets = map[string]emailInitProviderDefaults{
	"gmail": {
		imapHost: "imap.gmail.com",
		imapPort: 993,
		smtpHost: "smtp.gmail.com",
		smtpPort: 587,
	},
	"fastmail": {
		imapHost: "imap.fastmail.com",
		imapPort: 993,
		smtpHost: "smtp.fastmail.com",
		smtpPort: 587,
	},
	"icloud": {
		imapHost: "imap.mail.me.com",
		imapPort: 993,
		smtpHost: "smtp.mail.me.com",
		smtpPort: 587,
	},
	"custom": {},
}

func emailInitCmd() *cobra.Command {
	var (
		flagProvider       string
		flagNonInteractive bool
		flagImapHost       string
		flagImapPort       int
		flagSmtpHost       string
		flagSmtpPort       int
		flagPassword       string
		flagDaemonHandle   string
		flagTargetUser     string
		flagTargetEmail    string
		flagFromAddress    string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Interactive wizard to configure the email bridge",
		Long: `Configure the email bridge in a guided 6-step wizard.

Provider templates are available for Gmail, Fastmail, and iCloud.
App-password authentication only; OAuth is deferred to v0.11.x.

Non-interactive mode (--non-interactive) accepts all values via flags
and is useful for scripted provisioning and tests.

Examples:
  thrum email init
  thrum email init --provider gmail
  thrum email init --provider gmail --non-interactive \
    --password "app-password" --daemon-handle myhandle \
    --target-user leon --target-email me@gmail.com`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEmailInit(
				cmd,
				flagProvider,
				flagNonInteractive,
				flagImapHost, flagImapPort,
				flagSmtpHost, flagSmtpPort,
				flagPassword,
				flagDaemonHandle,
				flagTargetUser, flagTargetEmail,
				flagFromAddress,
			)
		},
	}

	cmd.Flags().StringVar(&flagProvider, "provider", "custom", "Provider: gmail|fastmail|icloud|custom")
	cmd.Flags().BoolVar(&flagNonInteractive, "non-interactive", false, "Accept all defaults; require all values via flags")
	cmd.Flags().StringVar(&flagImapHost, "imap-host", "", "IMAP hostname (custom provider or override)")
	cmd.Flags().IntVar(&flagImapPort, "imap-port", 0, "IMAP port (custom provider or override)")
	cmd.Flags().StringVar(&flagSmtpHost, "smtp-host", "", "SMTP hostname (custom provider or override)")
	cmd.Flags().IntVar(&flagSmtpPort, "smtp-port", 0, "SMTP port (custom provider or override)")
	cmd.Flags().StringVar(&flagPassword, "password", "", "App password (non-interactive mode)")
	cmd.Flags().StringVar(&flagDaemonHandle, "daemon-handle", "", "Mesh-visible handle (default: <repo>-<hostname>)")
	cmd.Flags().StringVar(&flagTargetUser, "target-user", "", "Thrum username this mailbox bridges to")
	cmd.Flags().StringVar(&flagTargetEmail, "target-email", "", "Operator contact email")
	cmd.Flags().StringVar(&flagFromAddress, "from-address", "", "RFC 5322 From: address (defaults to target-email)")

	return cmd
}

// runEmailInit executes the 6-step init wizard.
//
// In non-interactive mode every value comes from flags; prompts are skipped.
// In interactive mode (isInteractive() && !flagNonInteractive) each step
// prompts on stdout/stdin so a TTY user can confirm or override defaults.
// The stdin reader is injectable via cmd.InOrStdin() so tests can drive it
// with a bytes.Buffer.
func runEmailInit(
	cmd *cobra.Command,
	provider string,
	nonInteractive bool,
	imapHost string, imapPort int,
	smtpHost string, smtpPort int,
	password string,
	daemonHandle string,
	targetUser string, targetEmail string,
	fromAddress string,
) error {
	interactive := isInteractive() && !nonInteractive

	// Validate provider name.
	preset, ok := emailProviderPresets[provider]
	if !ok {
		return fmt.Errorf("unknown provider %q: valid values are gmail, fastmail, icloud, custom", provider)
	}

	scanner := bufio.NewScanner(cmd.InOrStdin())

	// Step 1: Provider selection.
	if interactive {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nStep 1/6 — Provider selection\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Provider [%s]: ", provider)
		if scanner.Scan() && scanner.Text() != "" {
			provider = strings.TrimSpace(scanner.Text())
			p, valid := emailProviderPresets[provider]
			if !valid {
				return fmt.Errorf("unknown provider %q: valid values are gmail, fastmail, icloud, custom", provider)
			}
			preset = p
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Note: OAuth deferred to v0.11.x — password-only for now.\n")
	}

	// Merge flag overrides into preset.
	if imapHost != "" {
		preset.imapHost = imapHost
	}
	if imapPort != 0 {
		preset.imapPort = imapPort
	}
	if smtpHost != "" {
		preset.smtpHost = smtpHost
	}
	if smtpPort != 0 {
		preset.smtpPort = smtpPort
	}

	// Interactive: prompt for custom host/port when provider==custom or override needed.
	if interactive && provider == "custom" {
		if preset.imapHost == "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "IMAP host: ")
			if scanner.Scan() {
				preset.imapHost = strings.TrimSpace(scanner.Text())
			}
		}
		if preset.imapPort == 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "IMAP port [993]: ")
			if scanner.Scan() {
				txt := strings.TrimSpace(scanner.Text())
				if txt == "" {
					preset.imapPort = 993
				} else {
					if _, err := fmt.Sscanf(txt, "%d", &preset.imapPort); err != nil {
						return fmt.Errorf("invalid IMAP port: %s", txt)
					}
				}
			}
		}
		if preset.smtpHost == "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "SMTP host: ")
			if scanner.Scan() {
				preset.smtpHost = strings.TrimSpace(scanner.Text())
			}
		}
		if preset.smtpPort == 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "SMTP port [587]: ")
			if scanner.Scan() {
				txt := strings.TrimSpace(scanner.Text())
				if txt == "" {
					preset.smtpPort = 587
				} else {
					if _, err := fmt.Sscanf(txt, "%d", &preset.smtpPort); err != nil {
						return fmt.Errorf("invalid SMTP port: %s", txt)
					}
				}
			}
		}
	}

	// Non-interactive validation: custom provider must have explicit hosts.
	if !interactive && provider == "custom" {
		if preset.imapHost == "" {
			return fmt.Errorf("--imap-host is required for custom provider")
		}
		if preset.smtpHost == "" {
			return fmt.Errorf("--smtp-host is required for custom provider")
		}
	}

	// Step 2: App password.
	if interactive {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nStep 2/6 — App password\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "App password (input hidden): ")
		// Use term.ReadPassword when stdin is a real TTY for non-echoing input.
		// The test path (stdin is a pipe) falls through to the scanner path.
		if term.IsTerminal(int(os.Stdin.Fd())) { // #nosec G115
			pw, err := term.ReadPassword(int(os.Stdin.Fd())) // #nosec G115
			_, _ = fmt.Fprintln(cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("read password: %w", err)
			}
			password = string(pw)
		} else {
			if scanner.Scan() {
				password = strings.TrimSpace(scanner.Text())
			}
		}
	}

	if password == "" {
		if nonInteractive {
			return fmt.Errorf("--password is required in non-interactive mode")
		}
		return fmt.Errorf("app password is required")
	}

	// Step 3: Daemon handle.
	if daemonHandle == "" {
		hostname, _ := os.Hostname()
		repoBase := filepath.Base(flagRepo)
		if repoBase == "." || repoBase == "" {
			repoBase = "thrum"
		}
		daemonHandle = fmt.Sprintf("%s-%s", repoBase, hostname)
	}

	if interactive {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nStep 3/6 — Daemon handle\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Daemon handle [%s]: ", daemonHandle)
		if scanner.Scan() && scanner.Text() != "" {
			daemonHandle = strings.TrimSpace(scanner.Text())
		}
	}

	// Step 4: Target user + email.
	if interactive {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nStep 4/6 — Target user + email\n")
		if targetUser == "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Target user (e.g., leon-letto): ")
			if scanner.Scan() {
				targetUser = strings.TrimSpace(scanner.Text())
			}
		}
		if targetEmail == "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Target email: ")
			if scanner.Scan() {
				targetEmail = strings.TrimSpace(scanner.Text())
			}
		}
	}

	if !nonInteractive {
		if targetUser == "" {
			return fmt.Errorf("target user is required")
		}
		if targetEmail == "" {
			return fmt.Errorf("target email is required")
		}
	}

	if fromAddress == "" {
		fromAddress = targetEmail
	}

	// Step 5: Write config files.
	if interactive {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nStep 5/6 — Writing config files\n")
	}

	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		// Fallback for brand-new repos not yet initialised.
		thrumDir = filepath.Join(flagRepo, ".thrum")
	}

	// Load-or-create config.json.
	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		cfg = &config.ThrumConfig{}
	}

	cfg.Email.Enabled = true
	cfg.Email.AuthMethod = "password"
	cfg.Email.DaemonHandle = daemonHandle
	cfg.Email.TargetUser = targetUser
	cfg.Email.TargetEmail = targetEmail
	cfg.Email.FromAddress = fromAddress
	cfg.Email.IMAP = config.EmailIMAP{
		Host: preset.imapHost,
		Port: preset.imapPort,
	}
	cfg.Email.SMTP = config.EmailSMTP{
		Host: preset.smtpHost,
		Port: preset.smtpPort,
	}

	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		return fmt.Errorf("save config.json: %w", err)
	}

	// Write secrets file atomically (mode 0600).
	secretsDir := filepath.Join(thrumDir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		return fmt.Errorf("create secrets dir: %w", err)
	}

	secrets := config.EmailSecrets{
		IMAPPassword: password,
		SMTPPassword: password,
	}
	secretsData, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal secrets: %w", err)
	}
	secretsPath := filepath.Join(secretsDir, "email.json")
	tmpPath := secretsPath + ".tmp"
	if err := os.WriteFile(tmpPath, secretsData, 0o600); err != nil {
		return fmt.Errorf("write secrets tmp: %w", err)
	}
	if err := os.Rename(tmpPath, secretsPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("finalize secrets file: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  config.json: updated\n")
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  secrets/email.json: written (mode 0600)\n")

	// Step 6: Optional first peer pair.
	if interactive {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nStep 6/6 — Optional first pair\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Pair with a peer now? [Y/n]: ")
		if scanner.Scan() {
			ans := strings.TrimSpace(scanner.Text())
			if ans == "" || strings.EqualFold(ans, "y") {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Peer handle: ")
				handle := ""
				if scanner.Scan() {
					handle = strings.TrimSpace(scanner.Text())
				}
				if handle != "" {
					client := emailClientOrHint()
					if client != nil {
						defer client.Close() //nolint:errcheck
						if err := emailPairViaClient(client, handle); err != nil {
							_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Pairing failed: %v\n", err)
						} else {
							_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Paired with %s\n", handle) // #nosec G705 -- CLI stdout, not web output
						}
					}
				}
			}
		}
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nBridge setup complete. Run `thrum daemon restart` to activate.\n")
	return nil
}

// --- email pair ---

func emailPairCmd() *cobra.Command {
	var flagTo string

	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Confirm a pending stranger-pair",
		Long: `Confirm a pending stranger-pair request from another Thrum daemon.

The peer must have sent a pair-request envelope to this daemon's inbox
address before you run this command.

Example:
  thrum email pair --to mybeta`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagTo == "" {
				return fmt.Errorf("--to is required")
			}
			client := emailClientOrHint()
			if client == nil {
				return nil
			}
			defer client.Close() //nolint:errcheck

			if err := emailPairViaClient(client, flagTo); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Paired with %s\n", flagTo)
			return nil
		},
	}
	cmd.Flags().StringVar(&flagTo, "to", "", "Handle of the pending stranger-pair to confirm (required)")
	return cmd
}

// emailPairViaClient calls the email.peer.pair RPC. Extracted so the init
// wizard can reuse it without re-parsing flags.
func emailPairViaClient(client *cli.Client, handle string) error {
	agentID, _ := resolveLocalAgentID()
	if agentID == "" {
		agentID = "user:" + os.Getenv("USER")
	}

	req := rpc.EmailPeerPairRequest{
		CallerAgentID: agentID,
		ToHandle:      handle,
	}
	var resp rpc.EmailPeerPairResponse
	if err := client.Call("email.peer.pair", req, &resp); err != nil {
		return fmt.Errorf("email.peer.pair: %w", err)
	}
	if !resp.Pending {
		return fmt.Errorf("no pending pair found for %q", handle)
	}
	return nil
}

// --- email list ---

func emailListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List email bridge peers",
		Long: `List all email bridge peers from the peer roster.

Default output: aligned table with handle, daemon-id-short, trust, vouched-by, added-at.
JSON mode (--json): full EmailPeerListResponse.

Example:
  thrum email list
  thrum email list --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := emailClientOrHint()
			if client == nil {
				return nil
			}
			defer client.Close() //nolint:errcheck

			agentID, _ := resolveLocalAgentID()
			if agentID == "" {
				agentID = "user:" + os.Getenv("USER")
			}

			req := rpc.EmailPeerListRequest{CallerAgentID: agentID}
			var resp rpc.EmailPeerListResponse
			if err := client.Call("email.peer.list", req, &resp); err != nil {
				return fmt.Errorf("email.peer.list: %w", err)
			}

			if flagJSON {
				return cli.EmitJSON(resp)
			}

			if len(resp.Peers) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No peers configured.")
				return nil
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-10s %-10s %-20s %-25s\n",
				"HANDLE", "DAEMON-ID", "TRUST", "VOUCHED-BY", "ADDED-AT")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), strings.Repeat("─", 90))
			for _, p := range resp.Peers {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-10s %-10s %-20s %-25s\n",
					p.Handle, p.DaemonIDShort, p.Trust, p.VouchedBy, p.AddedAt)
			}
			return nil
		},
	}
	return cmd
}

// --- email revoke ---

func emailRevokeCmd() *cobra.Command {
	var flagYes bool

	cmd := &cobra.Command{
		Use:   "revoke <handle>",
		Short: "Revoke an email bridge peer",
		Args:  cobra.ExactArgs(1),
		Long: `Remove a peer from the email bridge peer roster.

Prompts for confirmation unless --yes is set.

Example:
  thrum email revoke mybeta
  thrum email revoke mybeta --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			handle := args[0]

			if !flagYes {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Are you sure you want to revoke peer %q? [y/N]: ", handle)
				var ans string
				_, _ = fmt.Fscan(cmd.InOrStdin(), &ans)
				if !strings.EqualFold(strings.TrimSpace(ans), "y") {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Canceled.")
					return nil
				}
			}

			client := emailClientOrHint()
			if client == nil {
				return nil
			}
			defer client.Close() //nolint:errcheck

			agentID, _ := resolveLocalAgentID()
			if agentID == "" {
				agentID = "user:" + os.Getenv("USER")
			}

			req := rpc.EmailPeerRevokeRequest{
				CallerAgentID: agentID,
				ToHandle:      handle,
			}
			var resp rpc.EmailPeerRevokeResponse
			if err := client.Call("email.peer.revoke", req, &resp); err != nil {
				return fmt.Errorf("email.peer.revoke: %w", err)
			}

			if resp.Removed {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Revoked peer %q\n", handle)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Peer %q not found in roster\n", handle)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&flagYes, "yes", false, "Skip confirmation prompt")
	return cmd
}

// --- email rebind ---

func emailRebindCmd() *cobra.Command {
	var flagResumeAs string
	var flagNewDaemonID string

	cmd := &cobra.Command{
		Use:   "rebind",
		Short: "Update a peer's daemon-id after rotation",
		Long: `Apply a new daemon-id for a named peer handle after the peer
signals a daemon-id rotation.

Example:
  thrum email rebind --resume-as mybeta --new-daemon-id abc-123-def-456`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagResumeAs == "" {
				return fmt.Errorf("--resume-as is required")
			}
			if flagNewDaemonID == "" {
				return fmt.Errorf("--new-daemon-id is required")
			}

			client := emailClientOrHint()
			if client == nil {
				return nil
			}
			defer client.Close() //nolint:errcheck

			agentID, _ := resolveLocalAgentID()
			if agentID == "" {
				agentID = "user:" + os.Getenv("USER")
			}

			req := rpc.EmailPeerRebindRequest{
				CallerAgentID: agentID,
				ToHandle:      flagResumeAs,
				NewDaemonID:   flagNewDaemonID,
			}
			var resp rpc.EmailPeerRebindResponse
			if err := client.Call("email.peer.rebind", req, &resp); err != nil {
				return fmt.Errorf("email.peer.rebind: %w", err)
			}

			if resp.Updated {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Rebound %q to daemon-id %s\n", flagResumeAs, flagNewDaemonID)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Peer %q not found\n", flagResumeAs)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flagResumeAs, "resume-as", "", "Handle of the peer to update (required)")
	cmd.Flags().StringVar(&flagNewDaemonID, "new-daemon-id", "", "New daemon UUID for the peer (required)")
	return cmd
}

// --- email status ---

func emailStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show email bridge status",
		Long: `Show the current status of the email bridge.

Default: human-readable summary.
JSON mode (--json): full EmailStatusResponse.

Example:
  thrum email status
  thrum email status --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := emailClientOrHint()
			if client == nil {
				return nil
			}
			defer client.Close() //nolint:errcheck

			agentID, _ := resolveLocalAgentID()
			if agentID == "" {
				agentID = "user:" + os.Getenv("USER")
			}

			req := rpc.EmailStatusRequest{CallerAgentID: agentID}
			var resp rpc.EmailStatusResponse
			if err := client.Call("email.status", req, &resp); err != nil {
				return fmt.Errorf("email.status: %w", err)
			}

			if flagJSON {
				return cli.EmitJSON(resp)
			}

			w := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(w, "Email Bridge")
			_, _ = fmt.Fprintln(w, "────────────")
			runningStr := "no"
			if resp.Running {
				runningStr = "yes"
			}
			_, _ = fmt.Fprintf(w, "  Running:        %s\n", runningStr)
			if resp.LastError != "" {
				_, _ = fmt.Fprintf(w, "  Last error:     %s\n", resp.LastError)
			}
			_, _ = fmt.Fprintf(w, "  Inbound msgs:   %d\n", resp.InboundCount)
			_, _ = fmt.Fprintf(w, "  Outbound queue: %d\n", resp.OutboundQueueDepth)
			if len(resp.PausedPeers) > 0 {
				_, _ = fmt.Fprintf(w, "  Paused peers:   %s\n", strings.Join(resp.PausedPeers, ", "))
			}
			return nil
		},
	}
	return cmd
}

// --- email unblock ---

func emailUnblockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unblock <peer_key>",
		Short: "Clear the rate-limiter pause for a peer",
		Args:  cobra.ExactArgs(1),
		Long: `Clear the rate-limiter pause for the specified peer key,
allowing the peer to send/receive messages again.

The peer_key format matches what 'thrum email status --json' reports
under paused_peers.

Example:
  thrum email unblock mybeta`,
		RunE: func(cmd *cobra.Command, args []string) error {
			peerKey := args[0]

			client := emailClientOrHint()
			if client == nil {
				return nil
			}
			defer client.Close() //nolint:errcheck

			agentID, _ := resolveLocalAgentID()
			if agentID == "" {
				agentID = "user:" + os.Getenv("USER")
			}

			req := rpc.EmailUnblockRequest{
				CallerAgentID: agentID,
				PeerKey:       peerKey,
			}
			var resp rpc.EmailUnblockResponse
			if err := client.Call("email.unblock", req, &resp); err != nil {
				return fmt.Errorf("email.unblock: %w", err)
			}

			if resp.Unblocked {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Unblocked peer %q\n", peerKey)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Peer %q was not paused\n", peerKey)
			}
			return nil
		},
	}
	return cmd
}

// --- email send ---

func emailSendCmd() *cobra.Command {
	var (
		flagTo        string
		flagSubject   string
		flagBody      string
		flagFromAgent string
	)

	cmd := &cobra.Command{
		Use:   "send",
		Short: "Operator escape hatch: send an email via the bridge",
		Long: `Send an outbound email via the email bridge queue.

Body can be supplied via --body "text", --body - (reads from stdin), or
piped to stdin with no --body flag.

Example:
  thrum email send --to alice@example.com --subject "Hello" --body "Hi there"
  echo "body text" | thrum email send --to alice@example.com --subject "Hello"
  thrum email send --to alice@example.com --subject "Hello" --body -`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagTo == "" {
				return fmt.Errorf("--to is required")
			}
			if flagSubject == "" {
				return fmt.Errorf("--subject is required")
			}

			// Resolve body: flag value, stdin pipe, or --body=-
			body, err := resolveEmailBody(flagBody, cmd.InOrStdin())
			if err != nil {
				return err
			}

			client := emailClientOrHint()
			if client == nil {
				return nil
			}
			defer client.Close() //nolint:errcheck

			// Resolve caller agent ID.
			callerID := flagFromAgent
			if callerID == "" {
				callerID, _ = resolveLocalAgentID()
			}
			if callerID == "" {
				callerID = "user:" + os.Getenv("USER")
			}

			req := rpc.EmailSendRequest{
				CallerAgentID: callerID,
				ToAddress:     flagTo,
				Subject:       flagSubject,
				Body:          body,
			}
			var resp rpc.EmailSendResponse
			if err := client.Call("email.send", req, &resp); err != nil {
				return fmt.Errorf("email.send: %w", err)
			}

			if flagJSON {
				return cli.EmitJSON(resp)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Queued: id=%d message-id=%s\n", resp.QueueID, resp.MessageID)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagTo, "to", "", "Recipient email address (required)")
	cmd.Flags().StringVar(&flagSubject, "subject", "", "Email subject (required)")
	cmd.Flags().StringVar(&flagBody, "body", "", "Email body text; use - to read from stdin")
	cmd.Flags().StringVar(&flagFromAgent, "from-agent", "", "Override caller agent ID (default: local agent or user:<USER>)")

	return cmd
}

// resolveEmailBody resolves the message body from the --body flag or stdin.
//
// Rules:
//   - flag="-" → read all of stdin
//   - flag is non-empty → use it
//   - flag is empty AND stdin is a pipe → read all of stdin
//   - flag is empty AND stdin is a TTY → return error (body required)
func resolveEmailBody(flag string, stdin io.Reader) (string, error) {
	if flag == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read body from stdin: %w", err)
		}
		return string(data), nil
	}
	if flag != "" {
		return flag, nil
	}
	// No flag: try stdin if it looks like a pipe.
	if !isInteractive() {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read body from stdin: %w", err)
		}
		return string(data), nil
	}
	return "", fmt.Errorf("body is required: supply --body \"text\", --body -, or pipe text to stdin")
}
