# Telegram Pairing Flow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add interactive Telegram pairing to `thrum telegram configure` so
users can pair their Telegram account without manually copying user/chat IDs.

**Architecture:** The daemon gets a new `telegram.pair` RPC that temporarily
opens the bridge's allow gate, captures the first inbound Telegram message, and
returns the sender info. The CLI calls this RPC after writing config and
restarting the daemon, then confirms with the user and locks down `allow_from`
via the existing `telegram.configure` RPC.

**Tech Stack:** Go 1.26, cobra CLI, JSON-RPC 2.0 over Unix socket,
atomic.Pointer for lock-free goroutine coordination.

**Spec:** `docs/superpowers/specs/2026-03-24-telegram-pairing-design.md`

---

## File Map

| File                                      | Action | Responsibility                                                          |
| ----------------------------------------- | ------ | ----------------------------------------------------------------------- |
| `internal/bridge/telegram/bot.go`         | Modify | Add `pairMode` atomic bool, `pairCh` atomic pointer; branch in `Poll()` |
| `internal/bridge/telegram/bridge.go`      | Modify | Add `PairResult` struct, `Pair()` method, `pairMu` mutex                |
| `internal/bridge/telegram/bridge_test.go` | Create | Unit tests for `Pair()` — success, timeout, concurrent rejection        |
| `internal/bridge/telegram/bot_test.go`    | Modify | Unit tests for `Poll()` pair mode routing                               |
| `internal/daemon/rpc/telegram.go`         | Modify | Add `HandlePair` RPC handler with readiness polling and timeout cap     |
| `internal/daemon/rpc/telegram_test.go`    | Modify | Unit tests for `HandlePair`                                             |
| `cmd/thrum/main.go`                       | Modify | Add `telegram pair` subcommand, extend `configure` with pairing flow    |

---

## Task 1: Add PairResult struct and Bot pair-mode fields

**Files:**

- Modify: `internal/bridge/telegram/bridge.go:22-27` (add `PairResult` after
  `BridgeStatus`)
- Modify: `internal/bridge/telegram/bot.go:34-39` (add fields to `Bot` struct)

- [ ] **Step 1: Define PairResult struct in bridge.go**

Add after the `BridgeStatus` struct (line 27):

```go
// PairResult holds sender info captured during a pairing handshake.
type PairResult struct {
	UserID   int64  `json:"telegram_user_id"`
	Username string `json:"telegram_username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	ChatID   int64  `json:"chat_id"`
	Text     string `json:"message_text"`
}
```

- [ ] **Step 2: Add pair-mode fields to Bot struct in bot.go**

Add `"sync/atomic"` to the import block in `bot.go` (it is NOT currently
imported in this file — `bridge.go` imports it separately).

Add two fields to the `Bot` struct (after `rateLimit`):

```go
type Bot struct {
	api       *tgbotapi.BotAPI
	config    config.TelegramConfig
	messages  chan InboundMessage
	rateLimit rateLimiter
	pairMode  atomic.Bool
	pairCh    atomic.Pointer[chan PairResult]
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./internal/bridge/telegram/...` Expected: clean build, no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/bridge/telegram/bridge.go internal/bridge/telegram/bot.go
git commit -m "feat(telegram): add PairResult struct and bot pair-mode fields"
```

---

## Task 2: Implement Bot.Poll() pair-mode branch

**Files:**

- Modify: `internal/bridge/telegram/bot.go:124-136` (add pair branch before
  `IsAllowed`)
- Modify: `internal/bridge/telegram/bot_test.go` (add pair-mode tests)

- [ ] **Step 1: Write test — pair-mode atomic plumbing works correctly**

In `bot_test.go`, add tests that verify the atomic `pairMode`/`pairCh`
coordination. These test the channel plumbing that `Poll()` uses — `Poll()`
itself requires a live Telegram API connection and is covered by the E2E test in
Task 7.

```go
func TestBot_PairModeAtomicPlumbing(t *testing.T) {
	bot := &Bot{
		messages: make(chan InboundMessage, 1),
	}

	// Initially no pair mode
	assert.False(t, bot.pairMode.Load())
	assert.Nil(t, bot.pairCh.Load())

	// Set up pair channel (store BEFORE pairMode, matching Pair() ordering)
	pairCh := make(chan PairResult, 1)
	bot.pairCh.Store(&pairCh)
	bot.pairMode.Store(true)

	// Verify the atomic load path that Poll() uses
	assert.True(t, bot.pairMode.Load())
	ch := bot.pairCh.Load()
	require.NotNil(t, ch)

	// Send a result through the channel (same as Poll() does)
	result := PairResult{
		UserID:    12345,
		Username:  "testuser",
		FirstName: "Test",
		LastName:  "User",
		ChatID:    12345,
		Text:      "hello",
	}
	select {
	case *ch <- result:
	default:
		t.Fatal("pairCh should accept the result")
	}

	// Verify it arrived
	select {
	case got := <-pairCh:
		assert.Equal(t, int64(12345), got.UserID)
		assert.Equal(t, "testuser", got.Username)
	case <-time.After(time.Second):
		t.Fatal("expected result on pairCh")
	}

	// messages channel should be empty (Poll skips normal path in pair mode)
	select {
	case <-bot.messages:
		t.Fatal("message should not have been sent to normal channel")
	default:
	}

	// Teardown (matching Pair() defer)
	bot.pairMode.Store(false)
	bot.pairCh.Store(nil)
	assert.False(t, bot.pairMode.Load())
	assert.Nil(t, bot.pairCh.Load())
}
```

- [ ] **Step 2: Run test to verify it compiles and passes**

Run: `go test ./internal/bridge/telegram/... -run TestBot_PairMode -v` Expected:
PASS — this verifies the atomic fields and channel plumbing added in Task 1 work
correctly.

- [ ] **Step 3: Add pair-mode branch to Poll()**

In `bot.go` `Poll()` method, after the `from.IsBot` check (line 126) and
**before** the `IsAllowed` check (line 129), add:

```go
		// Pair mode: intercept message for pairing handshake.
		// pairCh is stored via atomic.Pointer BEFORE pairMode is set,
		// so loading pairCh after checking pairMode is safe.
		if b.pairMode.Load() {
			if ch := b.pairCh.Load(); ch != nil {
				im := extractMessage(msg)
				select {
				case *ch <- PairResult{
					UserID:    from.ID,
					Username:  from.UserName,
					FirstName: from.FirstName,
					LastName:  from.LastName,
					ChatID:    msg.Chat.ID,
					Text:      im.Text,
				}:
				default:
				}
				continue
			}
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/bridge/telegram/... -run TestBot_PairMode -v` Expected:
PASS

- [ ] **Step 5: Verify Poll() pair-mode branch compiles and build passes**

Run: `go build ./internal/bridge/telegram/...` Expected: clean build. The
pair-mode branch in `Poll()` is exercised end-to-end in Task 7's manual E2E test
with a real Telegram bot.

- [ ] **Step 7: Commit**

```bash
git add internal/bridge/telegram/bot.go internal/bridge/telegram/bot_test.go
git commit -m "feat(telegram): add pair-mode branch in Bot.Poll()"
```

---

## Task 3: Implement Bridge.Pair() method

**Files:**

- Modify: `internal/bridge/telegram/bridge.go:29-39` (add `pairMu` to Bridge
  struct)
- Modify: `internal/bridge/telegram/bridge.go` (add `Pair()` method)
- Create: `internal/bridge/telegram/bridge_test.go` (unit tests)

- [ ] **Step 1: Add pairMu and bot atomic pointer to Bridge struct**

Add `pairMu sync.Mutex` and `bot atomic.Pointer[Bot]` fields to the `Bridge`
struct. Using `atomic.Pointer` (not a plain `*Bot`) because `bot` is written by
the `run()` goroutine and read by `Pair()` from the RPC handler goroutine:

```go
type Bridge struct {
	cfg          config.TelegramConfig
	wsPort       string
	logger       *log.Logger
	mu           sync.Mutex
	cancelRun    context.CancelFunc
	running      atomic.Bool
	connectedAt  time.Time
	lastError    string
	inboundCount atomic.Int64
	pairMu       sync.Mutex
	bot          atomic.Pointer[Bot]
}
```

In `run()` (around line 171 where `NewBot` is called), store the bot atomically
**before** `b.running.Store(true)` to ensure memory ordering:

```go
bot := NewBot(b.cfg.Token, b.cfg)
b.bot.Store(bot)        // BEFORE running.Store(true)
b.running.Store(true)   // Pair() checks running first, then loads bot
```

Update `Pair()` to load the bot atomically (see Step 4 below).

Update the main loop in `run()` that reads from `bot.Messages()` — it already
has a local `bot` variable, so no change needed there. Just ensure
`b.bot.Store(bot)` is added before the `running` flag is set.

- [ ] **Step 2: Write failing test — Pair returns sender info**

Create `internal/bridge/telegram/bridge_test.go`:

```go
package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBridge_Pair_Success(t *testing.T) {
	b := &Bridge{}
	b.running.Store(true)
	bot := &Bot{
		messages: make(chan InboundMessage, 1),
	}
	b.bot.Store(bot)

	// Simulate: Pair() sets up pairCh, then a goroutine sends a result
	go func() {
		// Wait for pairMode to be set
		for !b.bot.Load().pairMode.Load() {
			time.Sleep(10 * time.Millisecond)
		}
		ch := b.bot.Load().pairCh.Load()
		require.NotNil(t, ch)
		*ch <- PairResult{
			UserID:    123456789,
			Username:  "jdoe",
			FirstName: "Leon",
			LastName:  "Letto",
			ChatID:    123456789,
			Text:      "hello",
		}
	}()

	result, err := b.Pair(context.Background(), 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, int64(123456789), result.UserID)
	assert.Equal(t, "jdoe", result.Username)
	assert.Equal(t, "Leon", result.FirstName)
	assert.Equal(t, int64(123456789), result.ChatID)

	// pairMode should be reverted
	assert.False(t, b.bot.Load().pairMode.Load())
	assert.Nil(t, b.bot.Load().pairCh.Load())
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/bridge/telegram/... -run TestBridge_Pair_Success -v`
Expected: FAIL — `Pair()` method doesn't exist yet.

- [ ] **Step 4: Implement Bridge.Pair()**

Add to `bridge.go`:

```go
var ErrPairingInProgress = errors.New("pairing already in progress")
var ErrBridgeNotRunning = errors.New("bridge not running")

func (b *Bridge) Pair(ctx context.Context, timeout time.Duration) (PairResult, error) {
	if !b.running.Load() {
		return PairResult{}, ErrBridgeNotRunning
	}
	bot := b.bot.Load()
	if bot == nil {
		return PairResult{}, ErrBridgeNotRunning
	}
	if !b.pairMu.TryLock() {
		return PairResult{}, ErrPairingInProgress
	}
	defer b.pairMu.Unlock()

	pairCh := make(chan PairResult, 1)
	// Store channel BEFORE setting pairMode (memory ordering guarantee)
	bot.pairCh.Store(&pairCh)
	bot.pairMode.Store(true)
	defer func() {
		bot.pairMode.Store(false)
		bot.pairCh.Store(nil)
	}()

	select {
	case result := <-pairCh:
		return result, nil
	case <-time.After(timeout):
		return PairResult{}, fmt.Errorf("no message received within %s", timeout)
	case <-ctx.Done():
		return PairResult{}, ctx.Err()
	}
}
```

Add `"errors"` to imports if not present.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/bridge/telegram/... -run TestBridge_Pair_Success -v`
Expected: PASS

- [ ] **Step 6: Write test — Pair times out**

```go
func TestBridge_Pair_Timeout(t *testing.T) {
	b := &Bridge{}
	b.running.Store(true)
	bot := &Bot{
		messages: make(chan InboundMessage, 1),
	}
	b.bot.Store(bot)

	// No one sends a message, so it should time out
	_, err := b.Pair(context.Background(), 100*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no message received")

	// pairMode should be reverted
	assert.False(t, b.bot.Load().pairMode.Load())
}
```

- [ ] **Step 7: Run test**

Run: `go test ./internal/bridge/telegram/... -run TestBridge_Pair_Timeout -v`
Expected: PASS

- [ ] **Step 8: Write test — concurrent Pair rejected**

```go
func TestBridge_Pair_ConcurrentRejected(t *testing.T) {
	b := &Bridge{}
	b.running.Store(true)
	bot := &Bot{
		messages: make(chan InboundMessage, 1),
	}
	b.bot.Store(bot)

	// Lock pairMu to simulate an in-progress pairing
	b.pairMu.Lock()

	_, err := b.Pair(context.Background(), 100*time.Millisecond)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPairingInProgress)

	b.pairMu.Unlock()
}
```

- [ ] **Step 9: Run test**

Run: `go test ./internal/bridge/telegram/... -run TestBridge_Pair_Concurrent -v`
Expected: PASS

- [ ] **Step 10: Write test — Pair with bridge not running**

```go
func TestBridge_Pair_NotRunning(t *testing.T) {
	b := &Bridge{}
	b.running.Store(false)
	b.bot.Store(&Bot{})

	_, err := b.Pair(context.Background(), 100*time.Millisecond)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBridgeNotRunning)
}
```

- [ ] **Step 11: Run all bridge tests**

Run: `go test ./internal/bridge/telegram/... -run TestBridge_Pair -v` Expected:
all 4 tests PASS

- [ ] **Step 12: Commit**

```bash
git add internal/bridge/telegram/bridge.go internal/bridge/telegram/bridge_test.go
git commit -m "feat(telegram): implement Bridge.Pair() with timeout and mutex guard"
```

---

## Task 4: Add HandlePair RPC handler

**Files:**

- Modify: `internal/daemon/rpc/telegram.go` (add `HandlePair`)
- Modify: `internal/daemon/rpc/telegram_test.go` (add tests)
- Modify: `cmd/thrum/main.go:5307-5317` (register `telegram.pair`)

- [ ] **Step 1: Write failing test — HandlePair returns sender info**

In `telegram_test.go`, add (adapt to existing test patterns in that file):

```go
func TestHandlePair_Success(t *testing.T) {
	handler := NewTelegramHandler("/tmp/test-repo")

	// Create a mock bridge that returns a PairResult
	// Since we can't easily mock Bridge.Pair(), test the handler's
	// timeout validation and error paths first.

	// Test: missing bridge returns error
	result, err := handler.HandlePair(context.Background(), json.RawMessage(`{"timeout_seconds": 10}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
	_ = result
}

func TestHandlePair_BridgeNotReady(t *testing.T) {
	handler := NewTelegramHandler("/tmp/test-repo")

	// Set a bridge that is not running (Running() returns false)
	bridge := &telegram.Bridge{}
	handler.SetBridge(bridge)

	// Should poll for 5s then fail — use a short context deadline to speed test
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := handler.HandlePair(ctx, json.RawMessage(`{"timeout_seconds": 10}`))
	require.Error(t, err)
	// Either "not connected" (poll timeout) or context deadline
	assert.True(t,
		strings.Contains(err.Error(), "not connected") ||
			strings.Contains(err.Error(), "context deadline"),
		"unexpected error: %v", err)
}

func TestHandlePair_TimeoutValidation(t *testing.T) {
	handler := NewTelegramHandler("/tmp/test-repo")

	// timeout_seconds = 0 should error
	_, err := handler.HandlePair(context.Background(), json.RawMessage(`{"timeout_seconds": 0}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout_seconds must be between 1 and 300")

	// timeout_seconds = 500 should error
	_, err = handler.HandlePair(context.Background(), json.RawMessage(`{"timeout_seconds": 500}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout_seconds must be between 1 and 300")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/rpc/... -run TestHandlePair -v` Expected: FAIL —
`HandlePair` doesn't exist.

- [ ] **Step 3: Implement HandlePair**

Add to `telegram.go`:

```go
type TelegramPairRequest struct {
	TimeoutSeconds int `json:"timeout_seconds"`
}

type TelegramPairResponse struct {
	UserID   int64  `json:"telegram_user_id"`
	Username string `json:"telegram_username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	ChatID   int64  `json:"chat_id"`
	Text     string `json:"message_text"`
}

func (h *TelegramHandler) HandlePair(ctx context.Context, raw json.RawMessage) (any, error) {
	var req TelegramPairRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.TimeoutSeconds < 1 || req.TimeoutSeconds > 300 {
		return nil, fmt.Errorf("timeout_seconds must be between 1 and 300")
	}

	if h.bridge == nil {
		return nil, fmt.Errorf("telegram bridge not configured")
	}

	// Poll for bridge readiness (up to 5 seconds after daemon restart)
	deadline := time.Now().Add(5 * time.Second)
	for !h.bridge.Running() && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !h.bridge.Running() {
		return nil, fmt.Errorf("telegram bridge not connected (waited 5s)")
	}

	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	result, err := h.bridge.Pair(ctx, timeout)
	if err != nil {
		return nil, err
	}

	return TelegramPairResponse{
		UserID:    result.UserID,
		Username:  result.Username,
		FirstName: result.FirstName,
		LastName:  result.LastName,
		ChatID:    result.ChatID,
		Text:      result.Text,
	}, nil
}
```

Also add a `Running() bool` method to Bridge if it doesn't exist (it has
`running` as an `atomic.Bool` field — just expose it):

```go
func (b *Bridge) Running() bool {
	return b.running.Load()
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/daemon/rpc/... -run TestHandlePair -v` Expected: PASS

- [ ] **Step 5: Register telegram.pair in daemon startup**

In `cmd/thrum/main.go` around line 5310, after the existing `telegram.status`
registration, add:

```go
server.RegisterHandler("telegram.pair", telegramHandler.HandlePair)
```

- [ ] **Step 6: Verify build**

Run: `go build ./cmd/thrum/...` Expected: clean build.

- [ ] **Step 7: Commit**

```bash
git add internal/bridge/telegram/bridge.go internal/daemon/rpc/telegram.go \
  internal/daemon/rpc/telegram_test.go cmd/thrum/main.go
git commit -m "feat(telegram): add telegram.pair RPC handler with readiness polling"
```

---

## Task 5: Add `thrum telegram pair` CLI subcommand

**Files:**

- Modify: `cmd/thrum/main.go:6083-6091` (add `telegramPairCmd` to `telegramCmd`)
- Modify: `cmd/thrum/main.go` (add `telegramPairCmd()` function and
  `runTelegramPair()`)

- [ ] **Step 1: Add telegramPairCmd() function**

Add near the existing `telegramStatusCmd()` (around line 6214):

```go
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
```

- [ ] **Step 2: Implement runTelegramPair()**

```go
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
	defer client.Close()

	// Call telegram.pair RPC with extended timeout
	fmt.Printf("Pairing — send any message to your bot from Telegram (timeout: %s)...\n", pairTimeout)

	var result struct {
		UserID    int64  `json:"telegram_user_id"`
		Username  string `json:"telegram_username"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		ChatID    int64  `json:"chat_id"`
		Text      string `json:"message_text"`
	}

	req := map[string]any{"timeout_seconds": int(pairTimeout.Seconds())}
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
		fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Println("Pairing skipped. Run 'thrum telegram pair' to retry.")
			return nil
		}
	}

	// Set allow_from and chat_id via telegram.configure RPC
	configReq := map[string]any{
		"allow_from": []int64{result.UserID},
		"chat_id":    result.ChatID,
	}
	var configResult map[string]any
	if err := client.Call("telegram.configure", configReq, &configResult); err != nil {
		return fmt.Errorf("failed to save pairing config: %w", err)
	}

	fmt.Printf("\nPaired! Allowed users: [%d]\n", result.UserID)
	return nil
}
```

- [ ] **Step 3: Register in telegramCmd()**

In `telegramCmd()` (around line 6089), add:

```go
cmd.AddCommand(telegramPairCmd())
```

- [ ] **Step 4: Verify build**

Run: `go build ./cmd/thrum/...` Expected: clean build.

- [ ] **Step 5: Commit**

```bash
git add cmd/thrum/main.go
git commit -m "feat(telegram): add 'thrum telegram pair' subcommand"
```

---

## Task 6: Extend `thrum telegram configure` with pairing flow

**Files:**

- Modify: `cmd/thrum/main.go:6093-6212` (add flags, extend
  `runTelegramConfigure`)

- [ ] **Step 1: Add new flags to telegramConfigureCmd()**

In `telegramConfigureCmd()` (around line 6113), add flags alongside the existing
`--token`, `--target`, `--user`, `--yes`:

```go
var (
	flagAllowFrom   int64
	flagChatID       int64
	flagPairTimeout  time.Duration
	flagSkipPair     bool
)

cmd.Flags().Int64Var(&flagAllowFrom, "allow-from", 0, "Telegram user ID to whitelist (skips pairing)")
cmd.Flags().Int64Var(&flagChatID, "chat-id", 0, "Telegram chat ID for outbound (defaults to --allow-from)")
cmd.Flags().DurationVar(&flagPairTimeout, "pair-timeout", 60*time.Second, "How long to wait for a pairing message")
cmd.Flags().BoolVar(&flagSkipPair, "skip-pair", false, "Write config only, don't pair")
```

Update `runTelegramConfigure` signature to accept the new flags. The existing
signature is
`func runTelegramConfigure(token, target, userID string, skipConfirm bool)`.
Change it to capture the new flags via closure in the `RunE` function instead of
passing them as parameters — this matches the pattern used by other commands
with many flags (e.g., `runTelegramPair` in Task 5). The `RunE` closure already
has access to the flag variables declared in `telegramConfigureCmd()`.

- [ ] **Step 2: Extend runTelegramConfigure() — allow-from path**

After the existing config save logic (around line 6200), add the three paths.
Replace the current "print restart instruction" ending with:

```go
	// Path 1: --allow-from provided — write directly, skip pairing
	if flagAllowFrom != 0 {
		chatID := flagChatID
		if chatID == 0 {
			chatID = flagAllowFrom // personal chat: chat_id == user_id
		}
		cfg.Telegram.AllowFrom = []int64{flagAllowFrom}
		cfg.Telegram.ChatID = chatID
		if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Println("\nRestart the daemon to apply: thrum daemon restart")
		return nil
	}

	// Path 2: --skip-pair — just save and instruct restart
	if flagSkipPair {
		fmt.Println("\nRestart the daemon to apply: thrum daemon restart")
		return nil
	}

	// Path 3: Auto-pair flow
	fmt.Println("\nStarting daemon with new config...")
	if err := cli.DaemonRestart(flagRepo, false); err != nil {
		return fmt.Errorf("daemon restart: %w", err)
	}
	fmt.Println("Daemon restarted")

	return runTelegramPair(flagPairTimeout, flagYes)
```

- [ ] **Step 3: Verify build**

Run: `go build ./cmd/thrum/...` Expected: clean build.

- [ ] **Step 4: Manual smoke test — configure with --allow-from**

Run against a test repo (or the current repo):

```bash
thrum telegram configure --token <test-token> --target @test --user test --allow-from 12345 --yes
```

Expected: config written with `allow_from: [12345]`, `chat_id: 12345`.

- [ ] **Step 5: Commit**

```bash
git add cmd/thrum/main.go
git commit -m "feat(telegram): extend configure with --allow-from, --skip-pair, and auto-pair flow"
```

---

## Task 7: End-to-end smoke test and documentation

**Files:**

- Modify: `website/docs/telegram.md` (if exists — add pairing section)

- [ ] **Step 1: Run full test suite**

Run: `make test` Expected: all existing tests pass, no regressions.

- [ ] **Step 2: Run lint**

Run: `make lint` Expected: clean.

- [ ] **Step 3: Manual E2E test — full pair flow**

In a test repo with a real Telegram bot token:

```bash
thrum telegram configure --token <real-token> --target @coordinator_main --user leon-letto
# Send a message from Telegram when prompted
# Confirm [y/n]: y
# Verify: thrum telegram status shows allow_from and chat_id
```

- [ ] **Step 4: Manual E2E test — pair subcommand**

```bash
# Reset allow_from in config.json manually, then:
thrum telegram pair
# Send a message from Telegram
# Confirm
# Verify status
```

- [ ] **Step 5: Update telegram documentation**

If `website/docs/telegram.md` exists, add a "Pairing" section documenting the
new flow, including the security model summary. If no docs file exists, add the
pairing instructions to the existing CLI help text (already done via cobra
`Long` description).

- [ ] **Step 6: Commit**

```bash
git add website/docs/telegram.md
git commit -m "docs(telegram): add pairing flow documentation"
```

- [ ] **Step 7: Push**

```bash
git push origin thrum-dev
```
