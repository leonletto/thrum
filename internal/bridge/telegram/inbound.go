package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/leonletto/thrum/internal/config"
)

// PendingNudgeLookup resolves the supervisor's most-recent pending
// permission nudge so a fresh DM ('y'/'n'/etc, no reply_to) can still
// drive TryResolve. Returns the Thrum firstDetect message_id of the
// pending nudge, or empty string if none match. Implementations should
// return an error only on storage failures — "no pending nudge" is the
// ordinary (empty, nil) case.
type PendingNudgeLookup func(ctx context.Context, supervisorAgentID string) (thrumMsgID string, err error)

// InboundRelay converts Telegram messages to Thrum messages via WebSocket RPC.
type InboundRelay struct {
	ws      *WSClient
	msgMap  *MessageMap
	userID  string // "user:leon-letto" — CallerAgentID for RPC calls
	target  string // "@coordinator_main" — mention target for messages
	groups  []config.TelegramGroup
	botName string // our bot's username (without @)

	// lookupPendingNudge, if non-nil, enables the fresh-DM fallback
	// added in thrum-48kt.3: when a supervisor replies with y/n/etc
	// as a fresh DM (no Telegram reply-thread), this lookup returns
	// the most-recent pending nudge for the supervisor so reply_to can
	// be set on the relayed message and TryResolve still fires.
	//
	// Stored as atomic.Pointer so SetPendingNudgeLookup and relay() are
	// race-safe regardless of call ordering — matches the b.db /
	// b.pendingNudgeLookup pattern in bridge.go. Defensive rather than
	// load-bearing in current code (bootstrap sets once before Run),
	// but eliminates a latent race if a future reconfigure path ever
	// rewires it live.
	lookupPendingNudge atomic.Pointer[PendingNudgeLookup]
}

// NewInboundRelay creates a relay that sends Telegram messages to Thrum.
func NewInboundRelay(ws *WSClient, msgMap *MessageMap, userID, target string, groups []config.TelegramGroup, botName string) *InboundRelay {
	return &InboundRelay{ws: ws, msgMap: msgMap, userID: userID, target: target, groups: groups, botName: botName}
}

// SetPendingNudgeLookup wires the fresh-DM fallback. Typically called
// once at bootstrap before Run; atomic.Pointer storage makes concurrent
// calls safe. A nil fn (the zero value) keeps the pre-thrum-48kt.3
// behavior where a fresh DM never sets reply_to.
func (r *InboundRelay) SetPendingNudgeLookup(fn PendingNudgeLookup) {
	r.lookupPendingNudge.Store(&fn)
}

// permissionResponseTokens are the set of body strings that trigger the
// fresh-DM fallback. Match is case-insensitive after trimming. Kept
// deliberately tight: longer utterances ("yeah I think so", "nope")
// fall through to normal DM handling.
var permissionResponseTokens = map[string]struct{}{
	"y": {}, "n": {}, "yes": {}, "no": {}, "allow": {}, "deny": {},
}

// isPermissionResponse reports whether body is one of the tight
// permission-response tokens (case-insensitive, whitespace-trimmed).
func isPermissionResponse(body string) bool {
	_, ok := permissionResponseTokens[strings.ToLower(strings.TrimSpace(body))]
	return ok
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
				slog.Error("telegram inbound: relay failed", "err", err)
			}
		}
	}
}

// Relay routes an inbound message to the correct handler based on whether it is
// a group message (GroupChatID < 0) or a direct message.
func (r *InboundRelay) Relay(ctx context.Context, msg InboundMessage) error {
	slog.Debug("telegram inbound: Relay called", "group_chat_id", msg.GroupChatID, "text", truncateForLog(msg.Text, 200))
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
		slog.Debug("telegram inbound: no group config — dropping", "chat_id", msg.GroupChatID)
		return nil
	}
	slog.Debug("telegram inbound: matched group", "group", grp.Name, "chat_id", msg.GroupChatID)

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
				slog.Warn("telegram inbound: fetch parent author failed — falling back",
					"thrum_id", thrumID, "err", err, "fallback", r.target)
			} else if author != "" && author != r.userID {
				mentionTarget = "@" + author
			}
		}
	}

	// Fresh-DM fallback (thrum-48kt.3): if the supervisor sent a bare
	// permission-response token (y/n/yes/no/allow/deny) as a NEW DM
	// rather than a Telegram reply, look up their most-recent pending
	// nudge and route to it. Keyed on r.userID so one human's fresh 'y'
	// cannot resolve a different human's pending nudge.
	if replyToThrumID == "" && isPermissionResponse(msg.Text) {
		if lookup := r.lookupPendingNudge.Load(); lookup != nil {
			if thrumID, err := (*lookup)(ctx, r.userID); err != nil {
				slog.Warn("telegram inbound: pending-nudge lookup failed — proceeding without reply_to",
					"supervisor", r.userID, "err", err)
			} else if thrumID != "" {
				replyToThrumID = thrumID
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

func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen > len("...(truncated)") {
		return s[:maxLen-len("...(truncated)")] + "...(truncated)"
	}
	return s[:maxLen]
}
