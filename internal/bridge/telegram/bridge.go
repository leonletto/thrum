package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

// Bridge orchestrates the Telegram↔Thrum bridge.
// It connects to the daemon's WebSocket, polls Telegram, and relays messages
// bidirectionally. The bridge does NOT store the bot token — it reads it
// from config at startup/restart time.
type Bridge struct {
	cfg    config.TelegramConfig
	wsPort string
	logger *log.Logger
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

// Run starts the bridge. Blocks until ctx is cancelled.
// Designed to be called as: go bridge.Run(ctx)
//
// The goroutine recovers from panics to avoid crashing the daemon.
// Transient errors cause a 5s backoff before retrying.
func (b *Bridge) Run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Printf("PANIC (recovered): %v", r)
		}
	}()

	b.logger.Println("starting...")
	defer b.logger.Println("stopped")

	for {
		err := b.run(ctx)
		if ctx.Err() != nil {
			return // Clean shutdown
		}
		b.logger.Printf("restarting after error: %v", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
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
		"caller_agent_id": userID,
	})
	if err != nil {
		return fmt.Errorf("session.start: %w", err)
	}

	var sess struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(sessResult, &sess)
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

	// 6. Start sub-goroutines
	go bot.Poll(ctx)
	go outbound.Run(ctx)
	go b.heartbeatLoop(ctx, ws, sess.SessionID)

	// 7. Process inbound messages (main loop for this goroutine)
	b.logger.Printf("connected (user: %s, target: %s, token: %s...)", userID, b.cfg.Target, b.cfg.MaskedToken())
	inbound.Run(ctx, bot.Messages())
	return nil
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
