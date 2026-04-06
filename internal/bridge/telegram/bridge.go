package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

// Bridge orchestrates the Telegram↔Thrum bridge.
// It connects to the daemon's WebSocket, polls Telegram, and relays messages
// bidirectionally. The token lives in cfg.Token (within the embedded
// TelegramConfig) and is passed to NewBot() on each restart cycle.
// It is never logged beyond MaskedToken().
// BridgeStatus contains the current runtime state of the bridge.
type BridgeStatus struct {
	Running      bool   `json:"running"`
	ConnectedAt  string `json:"connected_at,omitempty"`
	LastError    string `json:"error,omitempty"`
	InboundCount int64  `json:"inbound_count"`
}

// PairResult holds the identity information extracted from the first message
// received while the bridge is in pair mode.
type PairResult struct {
	UserID    int64  `json:"telegram_user_id"`
	Username  string `json:"telegram_username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"message_text"`
}

type Bridge struct {
	cfg          config.TelegramConfig
	wsPort       string
	logger       *log.Logger
	mu           sync.Mutex
	pairMu       sync.Mutex
	cancelRun    context.CancelFunc // cancels the current run loop for restart
	running      atomic.Bool
	bot          atomic.Pointer[Bot]
	connectedAt  time.Time
	lastError    string
	inboundCount atomic.Int64
}

// New creates a new Bridge. The token is read from cfg but not stored
// separately — it flows through to NewBot() at run time.
func New(cfg config.TelegramConfig, wsPort string) *Bridge {
	return &Bridge{
		cfg:    cfg,
		wsPort: wsPort,
		logger: log.New(os.Stderr, "telegram bridge: ", log.LstdFlags),
	}
}

// Status returns the current bridge runtime state.
func (b *Bridge) Status() BridgeStatus {
	s := BridgeStatus{
		Running:      b.running.Load(),
		InboundCount: b.inboundCount.Load(),
	}
	b.mu.Lock()
	if !b.connectedAt.IsZero() {
		s.ConnectedAt = b.connectedAt.Format(time.RFC3339)
	}
	s.LastError = b.lastError
	b.mu.Unlock()
	return s
}

// Running reports whether the bridge is currently connected and running.
func (b *Bridge) Running() bool {
	return b.running.Load()
}

var (
	// ErrPairingInProgress is returned by Pair() when another pairing is already underway.
	ErrPairingInProgress = errors.New("pairing already in progress")
	// ErrBridgeNotRunning is returned by Pair() when the bridge is not connected.
	ErrBridgeNotRunning = errors.New("bridge not running")
)

// Pair waits up to timeout for a Telegram user to send any message, then
// returns their identity. Only one Pair() may be in progress at a time;
// concurrent callers receive ErrPairingInProgress. Messages from bots are
// still silently dropped during pair mode (the IsBot guard fires before the
// pair-mode branch in Poll).
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

// Restart cancels the current run loop and relaunches with new config.
func (b *Bridge) Restart(newCfg config.TelegramConfig) {
	b.mu.Lock()
	b.cfg = newCfg
	cancel := b.cancelRun
	b.mu.Unlock()

	if cancel != nil {
		cancel() // triggers retry loop with new config
	}
}

// Run starts the bridge. Blocks until ctx is canceled.
// Designed to be called as: go bridge.Run(ctx)
//
// Panics are recovered and treated as transient errors (triggers retry).
// Transient errors cause a 5s backoff before retrying.
func (b *Bridge) Run(ctx context.Context) {
	b.logger.Println("starting...")
	defer b.logger.Println("stopped")

	for {
		// Create a child context for this run cycle (can be canceled by Restart)
		runCtx, runCancel := context.WithCancel(ctx)
		b.mu.Lock()
		b.cancelRun = runCancel
		b.mu.Unlock()

		err := b.runWithRecover(runCtx)
		runCancel()

		if ctx.Err() != nil {
			return // Parent context done — clean shutdown
		}

		b.mu.Lock()
		if err != nil {
			b.lastError = err.Error()
		}
		b.mu.Unlock()

		b.logger.Printf("restarting after error: %v", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// runWithRecover wraps run() with panic recovery, converting panics to errors
// so the retry loop in Run() can restart the bridge.
func (b *Bridge) runWithRecover(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Printf("PANIC (recovered): %v", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return b.run(ctx)
}

func (b *Bridge) run(ctx context.Context) error {
	// 1. Connect to daemon WebSocket (loopback only — validated by Dial)
	wsURL := fmt.Sprintf("ws://127.0.0.1:%s/ws", b.wsPort)
	ws, err := Dial(ctx, wsURL)
	if err != nil {
		return fmt.Errorf("ws connect: %w", err)
	}
	defer ws.Close()

	// 2. Register as user
	userID := "user:" + b.cfg.UserID
	_, err = ws.Call(ctx, "user.register", map[string]any{
		"username": b.cfg.UserID,
		"display":  "Telegram Bridge (" + b.cfg.UserID + ")",
	})
	if err != nil {
		return fmt.Errorf("user.register: %w", err)
	}

	// 3. Start session
	sessResult, err := ws.Call(ctx, "session.start", map[string]any{
		"agent_id": userID,
	})
	if err != nil {
		return fmt.Errorf("session.start: %w", err)
	}

	var sess struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(sessResult, &sess); err != nil || sess.SessionID == "" {
		return fmt.Errorf("session.start: could not extract session_id")
	}
	defer func() {
		endCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = ws.Call(endCtx, "session.end", map[string]any{
			"session_id": sess.SessionID,
			"reason":     "bridge_shutdown",
		})
	}()

	// 4. Set up mirrored groups and proxy agents
	b.ensureGroups(ctx, ws, userID)

	// 5. Initialize Telegram bot (token flows through, not stored on Bridge)
	bot, err := NewBot(b.cfg.Token, b.cfg)
	if err != nil {
		return fmt.Errorf("telegram bot: %w", err)
	}

	// Store bot pointer for concurrent access by Pair() before marking running.
	b.bot.Store(bot)

	// 6. Create message map and relays
	msgMap := NewMessageMap(10000)
	inbound := NewInboundRelay(ws, msgMap, userID, b.cfg.Target, b.cfg.Groups, bot.BotUsername())
	outbound := NewOutboundRelay(ws, bot, msgMap, userID, b.cfg.ChatID, b.cfg.Groups)

	// 7. Mark running BEFORE launching goroutines so Pair() sees accurate state.
	// b.bot.Store(bot) was set above — Pair() checks running first, then loads bot.
	b.running.Store(true)
	b.mu.Lock()
	b.connectedAt = time.Now()
	b.lastError = ""
	b.mu.Unlock()
	defer func() {
		b.running.Store(false)
		b.bot.Store(nil)
	}()

	// 8. Start sub-goroutines (each with panic recovery)
	go b.safeGo("bot.Poll", func() { bot.Poll(ctx) })
	go b.safeGo("outbound.Run", func() { outbound.Run(ctx) })
	go b.safeGo("heartbeat", func() { b.heartbeatLoop(ctx, ws, sess.SessionID) })

	b.logger.Printf("connected (user: %s, target: %s, token: %s...)", userID, b.cfg.Target, b.cfg.MaskedToken())

	// Main inbound loop — count messages
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-bot.Messages():
			if !ok {
				return nil
			}
			b.inboundCount.Add(1)
			if err := inbound.Relay(ctx, msg); err != nil {
				b.logger.Printf("inbound relay error: %v", err)
			}
		}
	}
}

// ensureGroups creates mirrored Thrum groups and registers proxy agents for
// each configured Telegram group. It is idempotent: group.create is skipped if
// the group already exists, and group.member.add for existing members is a no-op.
// When b.cfg.Groups is empty this method does nothing.
func (b *Bridge) ensureGroups(ctx context.Context, ws *WSClient, userID string) {
	for _, grp := range b.cfg.Groups {
		thrumGroupName := "tg:" + grp.Name

		// Check if group exists via group.list
		var listResp struct {
			Groups []struct {
				Name string `json:"name"`
			} `json:"groups"`
		}
		listResult, _ := ws.Call(ctx, "group.list", map[string]any{})
		_ = json.Unmarshal(listResult, &listResp)

		exists := false
		for _, g := range listResp.Groups {
			if g.Name == thrumGroupName {
				exists = true
				break
			}
		}

		// Create group if needed
		if !exists {
			_, _ = ws.Call(ctx, "group.create", map[string]any{
				"name":            thrumGroupName,
				"description":     "Mirrored Telegram group: " + grp.Name,
				"caller_agent_id": userID,
			})
		}

		// Add bridge user as member
		_, _ = ws.Call(ctx, "group.member.add", map[string]any{
			"group":           thrumGroupName,
			"member_type":     "agent",
			"member_value":    userID,
			"caller_agent_id": userID,
		})

		// Add target agent as member
		if b.cfg.Target != "" {
			target := strings.TrimPrefix(b.cfg.Target, "@")
			_, _ = ws.Call(ctx, "group.member.add", map[string]any{
				"group":           thrumGroupName,
				"member_type":     "agent",
				"member_value":    target,
				"caller_agent_id": userID,
			})
		}

		// Register proxy agents — sequential: register must complete before
		// group.member.add, because HandleMemberAdd validates agent exists.
		for _, ra := range grp.RemoteAgents {
			proxyName := ra.Prefix + ":" + ra.Name
			_, regErr := ws.Call(ctx, "agent.register", map[string]any{
				"name":    proxyName,
				"role":    "remote",
				"module":  ra.Prefix,
				"display": ra.Prefix + " " + ra.Name + " (via Telegram)",
			})

			if regErr != nil {
				b.logger.Printf("group %s: failed to register proxy agent %s: %v", grp.Name, proxyName, regErr)
			} else {
				_, addErr := ws.Call(ctx, "group.member.add", map[string]any{
					"group":           thrumGroupName,
					"member_type":     "agent",
					"member_value":    proxyName,
					"caller_agent_id": userID,
				})
				if addErr != nil {
					b.logger.Printf("group %s: failed to add proxy agent %s to group: %v", grp.Name, proxyName, addErr)
				}
			}
		}

		b.logger.Printf("group %s: mirrored as %s (%d remote agents)",
			grp.Name, thrumGroupName, len(grp.RemoteAgents))
	}
}

// safeGo runs fn with panic recovery, logging any panics.
func (b *Bridge) safeGo(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Printf("PANIC in %s (recovered): %v", name, r)
		}
	}()
	fn()
}

func (b *Bridge) heartbeatLoop(ctx context.Context, ws *WSClient, sessionID string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = ws.Call(ctx, "session.heartbeat", map[string]any{
				"session_id": sessionID,
			})
		}
	}
}
