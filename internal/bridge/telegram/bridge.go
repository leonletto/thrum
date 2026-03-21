package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
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

type Bridge struct {
	cfg       config.TelegramConfig
	wsPort    string
	logger    *log.Logger
	mu        sync.Mutex
	cancelRun context.CancelFunc // cancels the current run loop for restart
	running   atomic.Bool
	connectedAt time.Time
	lastError   string
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

// Run starts the bridge. Blocks until ctx is cancelled.
// Designed to be called as: go bridge.Run(ctx)
//
// Panics are recovered and treated as transient errors (triggers retry).
// Transient errors cause a 5s backoff before retrying.
func (b *Bridge) Run(ctx context.Context) {
	b.logger.Println("starting...")
	defer b.logger.Println("stopped")

	for {
		// Create a child context for this run cycle (can be cancelled by Restart)
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

	// 4. Initialize Telegram bot (token flows through, not stored on Bridge)
	bot, err := NewBot(b.cfg.Token, b.cfg)
	if err != nil {
		return fmt.Errorf("telegram bot: %w", err)
	}

	// 5. Create message map and relays
	msgMap := NewMessageMap(10000)
	inbound := NewInboundRelay(ws, msgMap, userID, b.cfg.Target)
	outbound := NewOutboundRelay(ws, bot, msgMap, userID, b.cfg.ChatID)

	// 6. Start sub-goroutines (each with panic recovery)
	go b.safeGo("bot.Poll", func() { bot.Poll(ctx) })
	go b.safeGo("outbound.Run", func() { outbound.Run(ctx) })
	go b.safeGo("heartbeat", func() { b.heartbeatLoop(ctx, ws, sess.SessionID) })

	// 7. Track connection state and process inbound messages
	b.running.Store(true)
	b.mu.Lock()
	b.connectedAt = time.Now()
	b.lastError = ""
	b.mu.Unlock()
	defer b.running.Store(false)

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
			if err := inbound.relay(ctx, msg); err != nil {
				b.logger.Printf("inbound relay error: %v", err)
			}
		}
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
