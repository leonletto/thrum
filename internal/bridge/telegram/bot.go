package telegram

import (
	"context"
	"fmt"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/leonletto/thrum/internal/config"
)

// InboundMessage is a message received from Telegram.
type InboundMessage struct {
	Text         string
	ChatID       int64
	MessageID    int
	Username     string
	UserID       int64
	ReplyToMsgID *int
}

// Bot is a Telegram long-poller with an access gate.
// The bot token is NOT stored as a field — it is passed only to tgbotapi.NewBotAPI.
type Bot struct {
	api      *tgbotapi.BotAPI
	config   config.TelegramConfig
	messages chan InboundMessage
}

// NewBot creates a new Bot. The token is passed to tgbotapi.NewBotAPI and not retained.
func NewBot(token string, cfg config.TelegramConfig) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("telegram: failed to create bot API: %w", err)
	}
	return &Bot{
		api:      api,
		config:   cfg,
		messages: make(chan InboundMessage, 32),
	}, nil
}

// Messages returns the channel of inbound messages that have passed the access gate.
func (b *Bot) Messages() <-chan InboundMessage {
	return b.messages
}

// Poll long-polls Telegram for updates and forwards allowed messages to the messages channel.
// It runs until ctx is cancelled.
func (b *Bot) Poll(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := b.api.GetUpdatesChan(u)
	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			if update.Message == nil {
				continue
			}
			msg := update.Message
			from := msg.From
			if from == nil {
				continue
			}

			// SECURITY: Gate check FIRST — before any extraction, logging of content,
			// or other processing. Blocked senders produce zero observable side effects.

			// Drop bot messages — bots are never allowed, even if ID is in AllowFrom.
			if from.IsBot {
				continue
			}

			// Fail-closed access check using config.IsAllowed.
			if !b.config.IsAllowed(from.ID) {
				continue
			}

			// Access granted — now extract the message.
			im := extractMessage(msg)

			select {
			case b.messages <- im:
			case <-ctx.Done():
				b.api.StopReceivingUpdates()
				return
			default:
				log.Printf("telegram: inbound message channel full, dropping message from user %d", from.ID)
			}
		}
	}
}

// SendMessage sends a text message to the given chatID.
// If replyToMsgID is non-nil, the message is sent as a reply to that message.
// Returns the Telegram message ID of the sent message.
func (b *Bot) SendMessage(chatID int64, text string, replyToMsgID *int) (int, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	if replyToMsgID != nil {
		msg.ReplyToMessageID = *replyToMsgID
	}
	sent, err := b.api.Send(msg)
	if err != nil {
		return 0, fmt.Errorf("telegram: send message: %w", err)
	}
	return sent.MessageID, nil
}

// extractMessage converts a tgbotapi.Message into an InboundMessage.
// Callers MUST check IsAllowed and IsBot BEFORE calling this function.
func extractMessage(msg *tgbotapi.Message) InboundMessage {
	im := InboundMessage{
		Text:      msg.Text,
		ChatID:    msg.Chat.ID,
		MessageID: msg.MessageID,
		UserID:    msg.From.ID,
	}
	if msg.From.UserName != "" {
		im.Username = msg.From.UserName
	} else {
		im.Username = msg.From.FirstName
	}
	if msg.ReplyToMessage != nil {
		replyID := msg.ReplyToMessage.MessageID
		im.ReplyToMsgID = &replyID
	}
	// Photo with caption: use caption as text when no text body present.
	if msg.Caption != "" && msg.Text == "" {
		im.Text = msg.Caption
	}
	return im
}
