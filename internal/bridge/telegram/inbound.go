package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// InboundRelay converts Telegram messages to Thrum messages via WebSocket RPC.
type InboundRelay struct {
	ws     *WSClient
	msgMap *MessageMap
	userID string // "user:leon-letto" — CallerAgentID for RPC calls
	target string // "@coordinator_main" — mention target for messages
}

// NewInboundRelay creates a relay that sends Telegram messages to Thrum.
func NewInboundRelay(ws *WSClient, msgMap *MessageMap, userID, target string) *InboundRelay {
	return &InboundRelay{ws: ws, msgMap: msgMap, userID: userID, target: target}
}

// Run reads from the bot's message channel and relays each to Thrum.
// Blocks until ctx is cancelled or the messages channel is closed.
func (r *InboundRelay) Run(ctx context.Context, messages <-chan InboundMessage) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-messages:
			if !ok {
				return
			}
			if err := r.relay(ctx, msg); err != nil {
				log.Printf("telegram inbound: relay failed: %v", err)
			}
		}
	}
}

// relay sends a single Telegram message to Thrum via message.send RPC.
func (r *InboundRelay) relay(ctx context.Context, msg InboundMessage) error {
	structured := map[string]any{
		"source":           "telegram",
		"chat_id":          msg.ChatID,
		"message_id":       msg.MessageID,
		"telegram_user":    msg.Username,
		"telegram_user_id": msg.UserID,
	}

	sendReq := map[string]any{
		"content":          msg.Text,
		"mentions":         []string{r.target},
		"caller_agent_id":  r.userID,
		"structured":       structured,
	}

	// Threading: if Telegram message is a reply, look up the Thrum message_id
	if msg.ReplyToMsgID != nil {
		if thrumID, ok := r.msgMap.ThrumID(msg.ChatID, *msg.ReplyToMsgID); ok {
			sendReq["reply_to"] = thrumID
		}
	}

	result, err := r.ws.Call(ctx, "message.send", sendReq)
	if err != nil {
		return fmt.Errorf("message.send: %w", err)
	}

	// Extract thrum message_id from response, store in map for future threading
	var resp struct {
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(result, &resp); err == nil && resp.MessageID != "" {
		r.msgMap.Store(msg.ChatID, msg.MessageID, resp.MessageID)
	}

	return nil
}
