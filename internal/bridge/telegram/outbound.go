package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/leonletto/thrum/internal/config"
)

// OutboundRelay listens for Thrum notification.message events on the WebSocket
// and forwards relevant messages to Telegram.
type OutboundRelay struct {
	ws     *WSClient
	bot    *Bot
	msgMap *MessageMap
	userID string               // "user:leon-letto" — messages TO this user get forwarded
	chatID int64                // Telegram chat to send to; outbound restricted to this chat only
	groups []config.TelegramGroup // Group bridge configs for group/proxy routing
}

// NewOutboundRelay creates a relay that forwards Thrum messages to Telegram.
func NewOutboundRelay(ws *WSClient, bot *Bot, msgMap *MessageMap, userID string, chatID int64, groups []config.TelegramGroup) *OutboundRelay {
	return &OutboundRelay{ws: ws, bot: bot, msgMap: msgMap, userID: userID, chatID: chatID, groups: groups}
}

// notificationParams represents the params of a notification.message event.
type notificationParams struct {
	MessageID string `json:"message_id"`
	Author    struct {
		AgentID string `json:"agent_id"`
		Name    string `json:"name"`
	} `json:"author"`
	Preview string `json:"preview"`
}

// fullMessage represents the response from message.get RPC.
type fullMessage struct {
	Message struct {
		MessageID string `json:"message_id"`
		ReplyTo   string `json:"reply_to"`
		Author    struct {
			AgentID string `json:"agent_id"`
		} `json:"author"`
		Body struct {
			Content string `json:"content"`
		} `json:"body"`
		Recipients []struct {
			AgentID string `json:"agent_id"`
		} `json:"recipients"`
	} `json:"message"`
}

// Run listens for notifications and relays matching messages to Telegram.
// Blocks until ctx is canceled.
func (r *OutboundRelay) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case notif, ok := <-r.ws.Notifications():
			if !ok {
				return
			}
			if notif.Method != "notification.message" {
				continue
			}
			r.handleNotification(ctx, notif.Params)
		}
	}
}

// handleNotification processes a single notification.message event.
func (r *OutboundRelay) handleNotification(ctx context.Context, params json.RawMessage) {
	var data notificationParams
	if err := json.Unmarshal(params, &data); err != nil {
		return
	}

	// Skip messages FROM the bridge user (don't echo our own sends)
	if data.Author.AgentID == r.userID {
		return
	}

	// Fetch full message to check recipients and get full body
	full, err := r.fetchMessage(ctx, data.MessageID)
	if err != nil {
		log.Printf("telegram outbound: fetch message %s: %v", data.MessageID, err)
		return
	}

	// DM path — forward messages where the bridge user is a recipient
	if r.isForUser(full) {
		// Format for Telegram: "@agent_name: message content"
		content := r.formatForTelegram(data.Author.Name, full)

		// Threading: check if this Thrum message replies to a Telegram-originated message
		var replyTo *int
		if full.Message.ReplyTo != "" {
			if _, teleID, ok := r.msgMap.TeleID(full.Message.ReplyTo); ok {
				replyTo = &teleID
			}
		}

		// SECURITY: Only send to the configured chatID — never to arbitrary chat IDs
		teleMsgID, err := r.bot.SendMessage(r.chatID, content, replyTo)
		if err != nil {
			log.Printf("telegram outbound: send to chat %d failed: %v", r.chatID, err)
			return
		}

		// Store mapping for future reply threading
		r.msgMap.Store(r.chatID, teleMsgID, data.MessageID)

		// Mark as read in Thrum (best-effort)
		_, _ = r.ws.Call(ctx, "message.markRead", map[string]any{
			"message_id":      data.MessageID,
			"caller_agent_id": r.userID,
		})
		return
	}

	// Group path — check if any recipient is a mirrored Thrum group
	for _, recip := range full.Message.Recipients {
		if chatID := r.findGroupChatID(recip.AgentID); chatID != 0 {
			content := formatForTelegram(data.Author.Name, full.Message.Body.Content)
			teleMsgID, err := r.bot.SendMessage(chatID, content, nil)
			if err == nil {
				r.msgMap.Store(chatID, teleMsgID, data.MessageID)
			}
			return
		}
	}

	// Proxy agent path
	for _, recip := range full.Message.Recipients {
		if agent, chatID := r.findProxyRoute(recip.AgentID); agent != nil {
			content := fmt.Sprintf("%s @%s: %s", agent.Bot, data.Author.Name, full.Message.Body.Content)
			teleMsgID, err := r.bot.SendMessage(chatID, content, nil)
			if err == nil {
				r.msgMap.Store(chatID, teleMsgID, data.MessageID)
			}
			return
		}
	}
}

// fetchMessage retrieves the full message from Thrum via message.get RPC.
func (r *OutboundRelay) fetchMessage(ctx context.Context, msgID string) (*fullMessage, error) {
	result, err := r.ws.Call(ctx, "message.get", map[string]any{
		"message_id": msgID,
	})
	if err != nil {
		return nil, fmt.Errorf("message.get: %w", err)
	}

	var full fullMessage
	if err := json.Unmarshal(result, &full); err != nil {
		return nil, fmt.Errorf("parse message.get response: %w", err)
	}
	return &full, nil
}

// isForUser checks if the bridge user is among the message recipients.
func (r *OutboundRelay) isForUser(full *fullMessage) bool {
	for _, recip := range full.Message.Recipients {
		if recip.AgentID == r.userID {
			return true
		}
	}
	return false
}

// formatForTelegram formats a Thrum message for display in Telegram.
func (r *OutboundRelay) formatForTelegram(authorName string, full *fullMessage) string {
	return formatForTelegram(authorName, full.Message.Body.Content)
}

// formatForTelegram is a package-level helper used by group and proxy paths.
func formatForTelegram(authorName, content string) string {
	if authorName != "" {
		return fmt.Sprintf("@%s: %s", authorName, content)
	}
	return content
}

// findGroupChatID returns the Telegram chat ID for a tg: prefixed recipient,
// or 0 if no matching group is configured.
func (r *OutboundRelay) findGroupChatID(recipientID string) int64 {
	if !strings.HasPrefix(recipientID, "tg:") {
		return 0
	}
	groupName := strings.TrimPrefix(recipientID, "tg:")
	for _, g := range r.groups {
		if g.Name == groupName {
			return g.ChatID
		}
	}
	return 0
}

// findProxyRoute returns the RemoteAgent and chat ID for a proxy recipient ID
// of the form "prefix:name", or nil/0 if no match is found.
func (r *OutboundRelay) findProxyRoute(recipientID string) (*config.RemoteAgent, int64) {
	for _, g := range r.groups {
		for i, ra := range g.RemoteAgents {
			proxyName := ra.Prefix + ":" + ra.Name
			if recipientID == proxyName {
				return &g.RemoteAgents[i], g.ChatID
			}
		}
	}
	return nil, 0
}
