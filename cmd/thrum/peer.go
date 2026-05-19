package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:1771-2121
// Destination: peer.go:24-374
// Tests: cmd/thrum/peer_cli_test.go (references peerCmd by name; stays package main); cmd/thrum/main_test.go (indirect via Execute())
// Commit: 05e04ad25f
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func peerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peer",
		Short: "Manage sync peers",
		Long:  `Pair, list, and manage Tailscale sync peers.`,
	}

	// thrum peer add — start pairing on this machine
	var addType, addAddress string
	addCmd := &cobra.Command{
		Use:   "add",
		Short: "Start pairing and wait for a peer to connect",
		Long: `Starts a pairing session and displays a peercode.
Share this code with the person running 'thrum peer join' on the other side.
Blocks until a peer connects or the session times out (5 minutes).

--type is required. Run 'thrum peer add' with no flags to see the full
list of transports and a one-line "when to use" for each.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			peerType, parseErr := cli.ParsePeerType(addType)
			if parseErr != nil {
				if errors.Is(parseErr, cli.ErrPeerTypeMissing) {
					//nolint:staticcheck // ST1005: multi-line user-facing CLI hint; formatting is intentional
					return errors.New(cli.MissingTypeMessage)
				}
				return parseErr
			}
			if peerType == cli.PeerTypeRepair {
				//nolint:staticcheck // ST1005: multi-line user-facing CLI hint; formatting is intentional
				return errors.New("--type repair is not valid for 'peer add'.\n" +
					"Use 'thrum peer join --type repair <peer-name>' to reconcile an existing peer.")
			}
			if peerType == cli.PeerTypeNetwork {
				trimmed := strings.TrimSpace(addAddress)
				if trimmed == "" {
					return errors.New("--type network requires --address <ip>")
				}
				if net.ParseIP(trimmed) == nil {
					return fmt.Errorf("--type network --address %q: not a valid IP address", trimmed)
				}
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			// For tailscale: ensure auth key is available unless the daemon
			// already has a healthy tsnet node (xir.26 fix). Other transports
			// never need an auth key.
			pairingParams := &cli.PeerStartPairingParams{
				Type:    string(peerType),
				Address: strings.TrimSpace(addAddress),
			}
			if peerType == cli.PeerTypeTailscale {
				authKey := os.Getenv("THRUM_TS_AUTHKEY")
				var health cli.HealthResult
				var healthPtr *cli.HealthResult
				if hErr := client.Call("health", map[string]any{}, &health); hErr == nil {
					healthPtr = &health
				}
				if cli.AuthKeyPromptNeeded(authKey, healthPtr) {
					fmt.Print("Enter Tailscale auth key: ")
					if _, scanErr := fmt.Scanln(&authKey); scanErr != nil {
						return fmt.Errorf("failed to read auth key: %w", scanErr)
					}
					authKey = strings.TrimSpace(authKey)
					if authKey == "" {
						return errors.New("auth key is required for --type tailscale")
					}
					if flagRepo != "" {
						thrumDir := filepath.Join(flagRepo, ".thrum")
						if saveErr := config.SaveAuthKeyToEnvFile(thrumDir, authKey); saveErr != nil {
							fmt.Fprintf(os.Stderr, "Warning: could not save auth key to .env: %v\n", saveErr)
						}
					}
					pairingParams.AuthKey = authKey
				}
			}

			result, err := cli.PeerStartPairing(client, pairingParams)
			if err != nil {
				return err
			}

			localHostname, _ := os.Hostname()
			if result.Address != "" {
				connStr := daemon.FormatPeercode(localHostname, result.Address, result.Code)
				transportTag := result.Transport
				if transportTag == "" {
					transportTag = string(peerType)
				}
				fmt.Printf("Waiting for connection...\nPairing code (transport=%s): %s\n\n", transportTag, connStr)
				fmt.Printf("Share this with the other side:\n  thrum peer join --type %s --peercode %s\n\n", peerType, connStr)
			} else {
				fmt.Printf("Waiting for connection... Pairing code: %s\n", result.Code)
			}

			waitResult, err := cli.PeerWaitPairing(client)
			if err != nil {
				return err
			}

			if waitResult.Status == "paired" {
				fmt.Printf("Paired with %q (%s). Syncing started.\n", waitResult.PeerName, waitResult.PeerAddress)
				fmt.Println("\nTo enable message routing for an agent on this peer:")
				fmt.Println("  thrum peer configure <peer-name> add-agent <agent-name>")
			} else {
				fmt.Println("Pairing timed out. Run 'thrum peer add --type <transport>' again.")
			}

			return nil
		},
	}
	addCmd.Flags().StringVar(&addType, "type", "", "Transport: tailscale | local | network (REQUIRED)")
	addCmd.Flags().StringVar(&addAddress, "address", "", "LAN IP for --type network (must be assigned to a local NIC)")
	cmd.AddCommand(addCmd)

	// thrum peer join — connect to a remote peer using a peercode (or
	// reconcile an existing peer entry via --type repair).
	var peerCode string
	var repoPath string
	var joinType string
	var joinPeerName string
	var joinAddress string
	joinCmd := &cobra.Command{
		Use:   "join [peercode]",
		Short: "Join a remote peer (or repair an existing one)",
		Long: `Connects to a remote peer using the peercode from 'thrum peer add'.

--type is required. Run 'thrum peer join' with no flags to see the full
list of transports and a one-line "when to use" for each.

Peercode input methods (for --type tailscale|local|network):
  thrum peer join --type T name:ip:port:code              (positional argument)
  thrum peer join --type T --peercode name:ip:port:code   (flag)
  echo "name:ip:port:code" | thrum peer join --type T     (pipe, no flag)
  thrum peer join --type T --peercode -                   (pipe via stdin flag)
  thrum peer join --type T                                 (interactive prompt)

--type repair requires <peer-name> (positional or --peer-name) — uses
stored secrets in peers.json to re-handshake without minting a new token.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			peerType, parseErr := cli.ParsePeerType(joinType)
			if parseErr != nil {
				if errors.Is(parseErr, cli.ErrPeerTypeMissing) {
					//nolint:staticcheck // ST1005: multi-line user-facing CLI hint; formatting is intentional
					return errors.New(cli.MissingTypeMessage)
				}
				return parseErr
			}

			client, err := getClient()
			if err != nil {
				return fmt.Errorf("connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			params := &cli.PeerJoinParams{Type: string(peerType)}

			switch peerType {
			case cli.PeerTypeRepair:
				name := strings.TrimSpace(joinPeerName)
				if name == "" && len(args) > 0 {
					name = strings.TrimSpace(args[0])
				}
				if name == "" {
					return errors.New("--type repair requires a peer name (positional arg or --peer-name)")
				}
				params.PeerName = name

			default: // tailscale, local, network — peercode-based
				code := peerCode
				if code == "-" {
					code = ""
				}
				if code == "" && len(args) > 0 {
					code = strings.TrimSpace(args[0])
				}
				if code == "" {
					stat, _ := os.Stdin.Stat()
					if (stat.Mode() & os.ModeCharDevice) == 0 {
						scanner := bufio.NewScanner(os.Stdin)
						if scanner.Scan() {
							code = strings.TrimSpace(scanner.Text())
						}
					}
				}
				if code == "" {
					fmt.Print("Enter peercode: ")
					var input string
					if _, scanErr := fmt.Scanln(&input); scanErr != nil {
						return fmt.Errorf("failed to read peercode: %w", scanErr)
					}
					code = strings.TrimSpace(input)
				}

				_, ip, port, pairCode, parseErr := daemon.ParseConnectionString(code)
				if parseErr != nil {
					return parseErr
				}
				params.Address = fmt.Sprintf("%s:%d", ip, port)
				params.Code = pairCode
				params.RepoPath = repoPath
				params.LocalAddress = strings.TrimSpace(joinAddress)

				if peerType == cli.PeerTypeLocal && daemon.DetectTransport(params.Address) != "local" {
					fmt.Fprintf(os.Stderr,
						"warning: --type local but peercode address %s is not loopback; "+
							"the peer add side likely emitted a non-local peercode\n", params.Address)
				}
				if peerType == cli.PeerTypeNetwork {
					if daemon.DetectTransport(params.Address) == "local" {
						fmt.Fprintf(os.Stderr,
							"warning: --type network but peercode address %s is loopback; "+
								"did you mean --type local?\n", params.Address)
					}
					if params.LocalAddress == "" {
						return errors.New("--type network requires --address <ip> on this side too " +
							"(the LAN IP this daemon should bind for sync reach-back)")
					}
				}
			}

			result, err := cli.PeerJoin(client, params)
			if err != nil {
				return err
			}

			if result.Status == "paired" {
				name := result.PeerName
				if name == "" {
					name = "<peer>"
				}
				fmt.Printf("Paired with %q. Syncing started.\n", name)
				fmt.Println("\nTo enable message routing for an agent on this peer:")
				fmt.Println("  thrum peer configure <peer-name> add-agent <agent-name>")
			} else {
				fmt.Printf("Pairing failed: %s\n", result.Message)
			}

			return nil
		},
	}
	joinCmd.Flags().StringVar(&joinType, "type", "", "Transport: tailscale | local | network | repair (REQUIRED)")
	joinCmd.Flags().StringVar(&peerCode, "peercode", "", "Connection string from 'thrum peer add' (peercode-based types)")
	joinCmd.Flags().StringVar(&repoPath, "repo-path", "", "Filesystem path to the peer's repo (legacy hint; --type local preferred)")
	joinCmd.Flags().StringVar(&joinPeerName, "peer-name", "", "Existing peer name for --type repair")
	joinCmd.Flags().StringVar(&joinAddress, "address", "", "LAN IP for --type network (this daemon's reach-back address)")
	cmd.AddCommand(joinCmd)

	// thrum peer list — show all peers
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List paired peers",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			peers, err := cli.PeerList(client)
			if err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(peers)
			}
			fmt.Print(cli.FormatPeerList(peers))
			return nil
		},
	})

	// thrum peer remove <name> — remove a peer
	cmd.AddCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a paired peer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			if err := cli.PeerRemove(client, args[0]); err != nil {
				return err
			}

			fmt.Printf("Removed peer %q. Sync stopped.\n", args[0])
			return nil
		},
	})

	// thrum peer status — detailed health per peer
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show detailed sync status for all peers",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			peers, err := cli.PeerStatus(client)
			if err != nil {
				return err
			}

			if flagJSON {
				return cli.EmitJSON(peers)
			}
			fmt.Print(cli.FormatPeerStatus(peers))
			return nil
		},
	})

	// thrum peer configure <peer-name> <action> <agent-name> — manage proxy agents
	cmd.AddCommand(&cobra.Command{
		Use:   "configure <peer-name> <action> <agent-name>",
		Short: "Configure proxy agents for a peer",
		Long:  "Add or remove proxy agents for a peer. Actions: add-agent, remove-agent",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getClient()
			if err != nil {
				return fmt.Errorf("failed to connect to daemon: %w", err)
			}
			defer func() { _ = client.Close() }()

			peerName, action, agentName := args[0], args[1], args[2]
			var result any
			if err := client.Call("peer.configure", map[string]any{
				"peer_name":  peerName,
				"action":     action,
				"agent_name": agentName,
			}, &result); err != nil {
				return err
			}
			fmt.Printf("✓ %s: %s %s\n", peerName, action, agentName)
			return nil
		},
	})

	return cmd
}
