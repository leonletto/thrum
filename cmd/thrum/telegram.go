package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/spf13/cobra"
)

// ORIGIN[thrum-8kxh]: moved from main.go:9493-9502
// Destination: telegram.go:23-32
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func telegramCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Manage Telegram bridge",
	}
	cmd.AddCommand(telegramConfigureCmd())
	cmd.AddCommand(telegramStatusCmd())
	cmd.AddCommand(telegramPairCmd())
	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:9504-9544
// Destination: telegram.go:40-80
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func telegramConfigureCmd() *cobra.Command {
	var (
		flagToken       string
		flagTarget      string
		flagUser        string
		flagYes         bool
		flagAllowFrom   int64
		flagChatID      int64
		flagPairTimeout time.Duration
		flagSkipPair    bool
	)

	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Configure the Telegram bridge",
		Long: `Configure the Telegram bridge connection.

Set the bot token from BotFather, the target agent that receives Telegram
messages, and your Thrum user ID.

Examples:
  thrum telegram configure --token 123456789:AAH... --target @coordinator_main --user leon-letto
  thrum telegram configure  # interactive mode
  thrum telegram configure --token 123456789:AAH... --skip-pair
  thrum telegram configure --allow-from 987654321`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTelegramConfigure(flagToken, flagTarget, flagUser, flagYes, flagAllowFrom, flagChatID, flagPairTimeout, flagSkipPair)
		},
	}

	cmd.Flags().StringVar(&flagToken, "token", "", "Telegram bot token from BotFather")
	cmd.Flags().StringVar(&flagTarget, "target", "", "Target agent for incoming messages (e.g., @coordinator_main)")
	cmd.Flags().StringVar(&flagUser, "user", "", "Your Thrum username (e.g., leon-letto)")
	cmd.Flags().BoolVar(&flagYes, "yes", false, "Skip confirmation prompts")
	cmd.Flags().Int64Var(&flagAllowFrom, "allow-from", 0, "Telegram user ID to whitelist (skips pairing)")
	cmd.Flags().Int64Var(&flagChatID, "chat-id", 0, "Telegram chat ID for outbound (defaults to --allow-from)")
	cmd.Flags().DurationVar(&flagPairTimeout, "pair-timeout", 60*time.Second, "How long to wait for a pairing message")
	cmd.Flags().BoolVar(&flagSkipPair, "skip-pair", false, "Write config only, don't pair")

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:9546-9664
// Destination: telegram.go:88-206
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func runTelegramConfigure(token, target, userID string, skipConfirm bool, allowFrom, chatID int64, pairTimeout time.Duration, skipPair bool) error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Interactive prompts for missing fields
	if token == "" {
		if cfg.Telegram.Token != "" {
			fmt.Printf("Current token: %s...\n", cfg.Telegram.MaskedToken())
		}
		fmt.Print("Bot token (from @BotFather): ")
		if _, err := fmt.Scanln(&token); err != nil {
			return fmt.Errorf("read token: %w", err)
		}
	}

	// Validate token format: numeric:alphanumeric
	if !isValidBotToken(token) {
		return fmt.Errorf("invalid token format (expected: 123456789:AAH...)")
	}

	if target == "" {
		target = "@coordinator_main"
		fmt.Printf("Target agent [%s]: ", target)
		var input string
		if _, err := fmt.Scanln(&input); err == nil && input != "" {
			target = input
		}
	}

	// Validate target starts with @
	if len(target) == 0 || target[0] != '@' {
		return fmt.Errorf("target must start with @ (e.g., @coordinator_main)")
	}

	if userID == "" {
		// Auto-detect from git config
		userID = detectGitUser()
		if userID != "" {
			fmt.Printf("User ID [%s]: ", userID)
			var input string
			if _, err := fmt.Scanln(&input); err == nil && input != "" {
				userID = input
			}
		} else {
			fmt.Print("User ID (your Thrum username): ")
			if _, err := fmt.Scanln(&userID); err != nil {
				return fmt.Errorf("read user ID: %w", err)
			}
		}
	}

	if userID == "" {
		return fmt.Errorf("user ID is required")
	}

	// Confirm if replacing existing token
	if cfg.Telegram.Token != "" && !skipConfirm {
		fmt.Printf("Existing token will be replaced (%s... → %s...)\n",
			cfg.Telegram.MaskedToken(), maskToken(token))
		fmt.Print("Continue? [y/N]: ")
		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if confirm != "y" && confirm != "Y" {
			fmt.Println("Canceled.")
			return nil
		}
	}

	// Update config (preserve existing fields like AllowFrom, ChatID)
	cfg.Telegram.Token = token
	cfg.Telegram.Target = target
	cfg.Telegram.UserID = userID

	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("Telegram bridge configured:\n")
	fmt.Printf("  Token:  %s...\n", maskToken(token))
	fmt.Printf("  Target: %s\n", target)
	fmt.Printf("  User:   %s\n", userID)

	// Path 1: --allow-from provided — write directly, skip pairing
	if allowFrom != 0 {
		chatIDVal := chatID
		if chatIDVal == 0 {
			chatIDVal = allowFrom // personal chat: chat_id == user_id
		}
		cfg.Telegram.AllowFrom = []int64{allowFrom}
		cfg.Telegram.ChatID = chatIDVal
		if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Println("\nRestart the daemon to apply: thrum daemon restart")
		return nil
	}

	// Path 2: --skip-pair — just save and instruct restart
	if skipPair {
		fmt.Println("\nRestart the daemon to apply: thrum daemon restart")
		return nil
	}

	// Path 3: Auto-pair flow
	fmt.Println("\nStarting daemon with new config...")
	if err := cli.DaemonRestart(flagRepo, false, false); err != nil {
		return fmt.Errorf("daemon restart: %w", err)
	}
	fmt.Println("Daemon restarted")

	return runTelegramPair(pairTimeout, skipConfirm)
}

// ORIGIN[thrum-8kxh]: moved from main.go:9666-9674
// Destination: telegram.go:214-222
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func telegramStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show Telegram bridge status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTelegramStatus()
		},
	}
}

// ORIGIN[thrum-8kxh]: moved from main.go:9676-9746
// Destination: telegram.go:230-300
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func runTelegramStatus() error {
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}

	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	tg := cfg.Telegram

	if flagJSON {
		status := map[string]any{
			"configured": tg.Token != "",
			"enabled":    tg.TelegramEnabled(),
			"target":     tg.Target,
			"user_id":    tg.UserID,
			"chat_id":    tg.ChatID,
			"allow_all":  tg.AllowAll,
		}
		if tg.Token != "" {
			status["token"] = tg.MaskedToken() + "..."
		}
		if len(tg.AllowFrom) > 0 {
			status["allow_from"] = tg.AllowFrom
		}
		return cli.EmitJSON(status)
	}

	if tg.Token == "" {
		fmt.Println("Telegram bridge: not configured")
		fmt.Println("\nRun 'thrum telegram configure' to set up.")
		return nil
	}

	fmt.Println("Telegram Bridge")
	fmt.Println("───────────────")
	fmt.Printf("  Token:   %s...\n", tg.MaskedToken())
	fmt.Printf("  Target:  %s\n", tg.Target)
	fmt.Printf("  User:    %s\n", tg.UserID)
	if tg.ChatID != 0 {
		fmt.Printf("  Chat ID: %d\n", tg.ChatID)
	}

	if tg.Enabled != nil && !*tg.Enabled {
		fmt.Printf("  Enabled: no (explicitly disabled)\n")
	} else {
		fmt.Printf("  Enabled: yes\n")
	}

	// Access control
	if tg.AllowAll {
		fmt.Printf("  Access:  allow all\n")
	} else if len(tg.AllowFrom) > 0 {
		fmt.Printf("  Access:  %d allowed user(s)\n", len(tg.AllowFrom))
	} else {
		fmt.Printf("  Access:  block all (no AllowFrom configured)\n")
	}

	// Check daemon
	wsPort := cli.ReadWebSocketPort(flagRepo)
	if wsPort > 0 {
		fmt.Printf("  Daemon:  running (port %d)\n", wsPort)
	} else {
		fmt.Printf("  Daemon:  not running\n")
	}

	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:9748-9771
// Destination: telegram.go:308-331
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func telegramPairCmd() *cobra.Command {
	var (
		flagPairTimeout time.Duration
		flagYes         bool
	)

	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Pair your Telegram account with the bridge",
		Long: `Start a pairing session that waits for a Telegram message to identify
your account. Send any message to the bot from Telegram, then confirm
the sender to set up the allow list.

The daemon must be running with a configured Telegram token.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTelegramPair(flagPairTimeout, flagYes)
		},
	}

	cmd.Flags().DurationVar(&flagPairTimeout, "pair-timeout", 60*time.Second, "How long to wait for a pairing message")
	cmd.Flags().BoolVar(&flagYes, "yes", false, "Auto-accept the first sender without prompting")

	return cmd
}

// ORIGIN[thrum-8kxh]: moved from main.go:9773-9834
// Destination: telegram.go:339-400
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func runTelegramPair(pairTimeout time.Duration, autoAccept bool) error {
	// Check config has a token
	thrumDir, err := paths.ResolveThrumDir(flagRepo)
	if err != nil {
		return fmt.Errorf("resolve thrum dir: %w", err)
	}
	cfg, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Telegram.Token == "" {
		return fmt.Errorf("telegram not configured — run 'thrum telegram configure' first")
	}

	// Connect to daemon
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("daemon not running — start with 'thrum daemon start'")
	}
	defer client.Close() //nolint:errcheck

	// Call telegram.pair RPC with extended timeout
	fmt.Printf("Pairing — send any message to your bot from Telegram (timeout: %s)...\n", pairTimeout)

	var result rpc.TelegramPairResponse
	req := rpc.TelegramPairRequest{TimeoutSeconds: int(pairTimeout.Seconds())}
	if err := client.CallWithTimeout("telegram.pair", req, &result, pairTimeout+5*time.Second); err != nil {
		return fmt.Errorf("pairing failed: %w", err)
	}

	// Display sender info
	name := result.FirstName
	if result.LastName != "" {
		name += " " + result.LastName
	}
	fmt.Printf("\nMessage from: %s (ID: %d)\n", name, result.UserID)

	// Confirm
	if !autoAccept {
		fmt.Print("  Allow this user? [y/n]: ")
		var answer string
		_, _ = fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Println("Pairing skipped. Run 'thrum telegram pair' to retry.")
			return nil
		}
	}

	// Set allow_from and chat_id via telegram.configure RPC
	chatID := result.ChatID
	configReq := rpc.TelegramConfigureRequest{
		AllowFrom: []int64{result.UserID},
		ChatID:    &chatID,
	}
	var configResult rpc.TelegramConfigureResponse
	if err := client.Call("telegram.configure", configReq, &configResult); err != nil {
		return fmt.Errorf("failed to save pairing config: %w", err)
	}

	fmt.Printf("\nPaired! Allowed users: [%d]\n", result.UserID)
	return nil
}

// ORIGIN[thrum-8kxh]: moved from main.go:9836-9854
// Destination: telegram.go:408-426
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func isValidBotToken(token string) bool {
	// Token format: numeric_id:alphanumeric_secret
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return false
		}
	}
	for _, c := range parts[1] {
		//nolint:staticcheck // QF1001: explicit positive-range form is clearer for character classes
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// ORIGIN[thrum-8kxh]: moved from main.go:9856-9861
// Destination: telegram.go:434-439
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func maskToken(token string) string {
	if len(token) <= 10 {
		return token
	}
	return token[:10]
}

// ORIGIN[thrum-8kxh]: moved from main.go:9863-9874
// Destination: telegram.go:447-458
// Tests: cmd/thrum/main_test.go (indirect via Execute())
// Commit: <pending>
// Phase: 1
// Remove this ORIGIN marker once refactor verified green.
func detectGitUser() string {
	// Use GitConfig (not Git) so we read the real user.name, not the
	// thrum injected override that Git/GitLong apply.
	name, err := safecmd.GitConfig(context.Background(), ".", "user.name")
	if err != nil || name == "" {
		return ""
	}
	// Convert "Leon Letto" → "leon-letto"
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	return name
}
