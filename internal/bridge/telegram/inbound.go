package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/leonletto/thrum/internal/config"
)

// InboundRelay converts Telegram messages to Thrum messages via WebSocket RPC.
type InboundRelay struct {
	ws      *WSClient
	msgMap  *MessageMap
	userID  string // "user:leon-letto" — CallerAgentID for RPC calls
	target  string // "@coordinator_main" — mention target for messages
	groups  []config.TelegramGroup
	botName string // our bot's username (without @)
}

// NewInboundRelay creates a relay that sends Telegram messages to Thrum.
func NewInboundRelay(ws *WSClient, msgMap *MessageMap, userID, target string, groups []config.TelegramGroup, botName string) *InboundRelay {
	return &InboundRelay{ws: ws, msgMap: msgMap, userID: userID, target: target, groups: groups, botName: botName}
}

// Run reads from the bot's message channel and relays each to Thrum.
// Blocks until ctx is canceled or the messages channel is closed.
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

// Relay routes an inbound message to the correct handler based on whether it is
// a group message (GroupChatID < 0) or a direct message.
func (r *InboundRelay) Relay(ctx context.Context, msg InboundMessage) error {
	log.Printf("telegram inbound: Relay called — GroupChatID=%d, text=%q", msg.GroupChatID, msg.Text)
	if msg.GroupChatID < 0 {
		return r.relayGroup(ctx, msg)
	}
	return r.relay(ctx, msg)
}

// senderIdentity returns "user:{username}" for humans and "bot:{bot_username}"
// for bots.
func senderIdentity(msg InboundMessage) string {
	if msg.IsBotSender {
		return "bot:" + msg.BotUsername
	}
	return "user:" + msg.Username
}

// findGroup looks up a group config by Telegram chat ID.
func (r *InboundRelay) findGroup(chatID int64) *config.TelegramGroup {
	for i := range r.groups {
		if r.groups[i].ChatID == chatID {
			return &r.groups[i]
		}
	}
	return nil
}

// relayGroup handles messages from Telegram group chats.
// It applies @mention routing: messages that mention our bot (or have no
// @mention) are forwarded; messages that mention a different bot are dropped.
func (r *InboundRelay) relayGroup(ctx context.Context, msg InboundMessage) error {
	grp := r.findGroup(msg.GroupChatID)
	if grp == nil {
		log.Printf("telegram inbound: no group config for chat_id %d — dropping", msg.GroupChatID)
		return nil
	}
	log.Printf("telegram inbound: matched group %q for chat_id %d", grp.Name, msg.GroupChatID)

	// @mention routing: check if message mentions our bot or another bot.
	mentions := ParseMentions(msg.Text)
	if len(mentions) > 0 {
		mentionsUs := false
		for _, m := range mentions {
			if m == r.botName {
				mentionsUs = true
				break
			}
		}
		if !mentionsUs {
			// Mentions something other than us — ignore
			return nil
		}
	}
	// Strip our bot's @mention from the content so Thrum agents see clean text.
	content := StripMention(msg.Text, r.botName)

	thrumGroup := "tg:" + grp.Name
	structured := map[string]any{
		"source":           "telegram",
		"chat_id":          msg.GroupChatID,
		"message_id":       msg.MessageID,
		"telegram_user":    msg.Username,
		"telegram_user_id": msg.UserID,
		"group_name":       grp.Name,
	}

	sendReq := map[string]any{
		"content":         content,
		"group":           thrumGroup,
		"mentions":        []string{thrumGroup},
		"caller_agent_id": senderIdentity(msg),
		"structured":      structured,
	}

	// Threading: if Telegram message is a reply, look up the Thrum message_id
	if msg.ReplyToMsgID != nil {
		if thrumID, ok := r.msgMap.ThrumID(msg.GroupChatID, *msg.ReplyToMsgID); ok {
			sendReq["reply_to"] = thrumID
		}
	}

	result, err := r.ws.Call(ctx, "message.send", sendReq)
	if err != nil {
		return fmt.Errorf("group message.send: %w", err)
	}

	var resp struct {
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(result, &resp); err == nil && resp.MessageID != "" {
		r.msgMap.Store(msg.GroupChatID, msg.MessageID, resp.MessageID)
	}

	return nil
}

// relay sends a single Telegram message to Thrum via message.send RPC.
//
// Mention routing:
//   - For fresh DMs (no reply_to), the mention target is the configured
//     bridge target (e.g. @coordinator_main).
//   - For replies to a message that originated inside Thrum (stored in
//     msgMap), the mention target is the ORIGINAL AUTHOR of that parent
//     message. This lets Telegram users reply to messages from any agent
//     — not just the configured target — and have those replies actually
//     reach the agent they were responding to. Falls back to r.target if
//     the parent author cannot be resolved, or if the parent was authored
//     by the bridge user themselves (avoiding a self-mention loop).
func (r *InboundRelay) relay(ctx context.Context, msg InboundMessage) error {
	structured := map[string]any{
		"source":           "telegram",
		"chat_id":          msg.ChatID,
		"message_id":       msg.MessageID,
		"telegram_user":    msg.Username,
		"telegram_user_id": msg.UserID,
	}

	// Default mention target is the configured bridge target.
	mentionTarget := r.target

	// Threading: if Telegram message is a reply, look up the Thrum message_id
	// and route the mention to the parent message's author when possible.
	var replyToThrumID string
	if msg.ReplyToMsgID != nil {
		if thrumID, ok := r.msgMap.ThrumID(msg.ChatID, *msg.ReplyToMsgID); ok {
			replyToThrumID = thrumID
			if author, err := r.fetchMessageAuthor(ctx, thrumID); err != nil {
				log.Printf("telegram inbound: fetch parent author for %s: %v — falling back to %s",
					thrumID, err, r.target)
			} else if author != "" && author != r.userID {
				mentionTarget = "@" + author
			}
		}
	}

	sendReq := map[string]any{
		"content":         msg.Text,
		"mentions":        []string{mentionTarget},
		"caller_agent_id": r.userID,
		"structured":      structured,
	}
	if replyToThrumID != "" {
		sendReq["reply_to"] = replyToThrumID
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

// fetchMessageAuthor resolves the author agent_id of a Thrum message via the
// message.get RPC. Returns an empty string (with no error) if the response
// omits the author field.
func (r *InboundRelay) fetchMessageAuthor(ctx context.Context, thrumID string) (string, error) {
	result, err := r.ws.Call(ctx, "message.get", map[string]any{
		"message_id": thrumID,
	})
	if err != nil {
		return "", fmt.Errorf("message.get: %w", err)
	}

	var parsed struct {
		Message struct {
			Author struct {
				AgentID string `json:"agent_id"`
			} `json:"author"`
		} `json:"message"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return "", fmt.Errorf("parse message.get response: %w", err)
	}
	return parsed.Message.Author.AgentID, nil
}
