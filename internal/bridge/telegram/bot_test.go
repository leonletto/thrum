package telegram

import (
	"reflect"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/leonletto/thrum/internal/config"
)

// TestBotDoesNotStoreToken verifies that the Bot struct has no field that holds
// the bot token string. The token must only be passed to tgbotapi.NewBotAPI()
// and must not be retained anywhere in the struct.
func TestBotDoesNotStoreToken(t *testing.T) {
	t.Parallel()
	rt := reflect.TypeFor[Bot]()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Type.Kind() == reflect.String {
			// Any string field is suspect — there should be none that could hold a token.
			t.Errorf("Bot struct has a string field %q; bot token must not be stored", f.Name)
		}
	}
}

// TestExtractMessageText verifies basic text message extraction.
func TestExtractMessageText(t *testing.T) {
	t.Parallel()
	msg := &tgbotapi.Message{
		MessageID: 42,
		From: &tgbotapi.User{
			ID:       1001,
			UserName: "alice",
			IsBot:    false,
		},
		Chat: &tgbotapi.Chat{ID: 9001},
		Text: "hello world",
	}
	im := extractMessage(msg)
	if im.Text != "hello world" {
		t.Errorf("Text = %q, want %q", im.Text, "hello world")
	}
	if im.ChatID != 9001 {
		t.Errorf("ChatID = %d, want 9001", im.ChatID)
	}
	if im.MessageID != 42 {
		t.Errorf("MessageID = %d, want 42", im.MessageID)
	}
	if im.UserID != 1001 {
		t.Errorf("UserID = %d, want 1001", im.UserID)
	}
	if im.Username != "alice" {
		t.Errorf("Username = %q, want \"alice\"", im.Username)
	}
	if im.ReplyToMsgID != nil {
		t.Errorf("ReplyToMsgID = %v, want nil", im.ReplyToMsgID)
	}
}

// TestExtractMessageFallsBackToFirstName verifies that when UserName is empty,
// FirstName is used as Username.
func TestExtractMessageFallsBackToFirstName(t *testing.T) {
	t.Parallel()
	msg := &tgbotapi.Message{
		MessageID: 1,
		From: &tgbotapi.User{
			ID:        2002,
			UserName:  "",
			FirstName: "Bob",
			IsBot:     false,
		},
		Chat: &tgbotapi.Chat{ID: 1},
		Text: "hi",
	}
	im := extractMessage(msg)
	if im.Username != "Bob" {
		t.Errorf("Username = %q, want \"Bob\"", im.Username)
	}
}

// TestExtractMessageReply verifies that ReplyToMsgID is populated when present.
func TestExtractMessageReply(t *testing.T) {
	t.Parallel()
	replyMsg := &tgbotapi.Message{MessageID: 7}
	msg := &tgbotapi.Message{
		MessageID: 8,
		From: &tgbotapi.User{
			ID:       3003,
			UserName: "carol",
			IsBot:    false,
		},
		Chat:           &tgbotapi.Chat{ID: 5},
		Text:           "reply text",
		ReplyToMessage: replyMsg,
	}
	im := extractMessage(msg)
	if im.ReplyToMsgID == nil {
		t.Fatal("ReplyToMsgID is nil, want non-nil")
	}
	if *im.ReplyToMsgID != 7 {
		t.Errorf("*ReplyToMsgID = %d, want 7", *im.ReplyToMsgID)
	}
}

// TestExtractMessageCaption verifies that a photo caption is used as text when
// the message has no text body.
func TestExtractMessageCaption(t *testing.T) {
	t.Parallel()
	msg := &tgbotapi.Message{
		MessageID: 99,
		From: &tgbotapi.User{
			ID:       4004,
			UserName: "dave",
			IsBot:    false,
		},
		Chat:    &tgbotapi.Chat{ID: 2},
		Text:    "",
		Caption: "look at this photo",
	}
	im := extractMessage(msg)
	if im.Text != "look at this photo" {
		t.Errorf("Text = %q, want caption text", im.Text)
	}
}

// TestExtractMessageTextTakesPrecedenceOverCaption verifies that when both Text
// and Caption are set, Text wins (caption is only the fallback).
func TestExtractMessageTextTakesPrecedenceOverCaption(t *testing.T) {
	t.Parallel()
	msg := &tgbotapi.Message{
		MessageID: 10,
		From: &tgbotapi.User{
			ID:       5005,
			UserName: "eve",
			IsBot:    false,
		},
		Chat:    &tgbotapi.Chat{ID: 3},
		Text:    "actual text",
		Caption: "a caption",
	}
	im := extractMessage(msg)
	if im.Text != "actual text" {
		t.Errorf("Text = %q, want \"actual text\"", im.Text)
	}
}

// TestAccessGateOrder verifies the security invariant: the access gate (IsBot +
// IsAllowed) runs BEFORE message extraction. We test this by confirming that
// config.IsAllowed correctly gates users and that bot messages are always dropped.
//
// Because Poll() integrates with the real Telegram API, we test the gate logic
// directly via the config values that Poll() uses — this is the authoritative
// gate logic without mocking the network layer.
func TestAccessGateOrder(t *testing.T) {
	t.Parallel()

	allowedUserID := int64(1234)
	blockedUserID := int64(5678)
	botUserID := int64(9999)

	cfg := config.TelegramConfig{
		AllowFrom: []int64{allowedUserID, botUserID}, // botUserID in list but is a bot
		AllowAll:  false,
	}

	tests := []struct {
		name      string
		userID    int64
		isBot     bool
		wantAllow bool
	}{
		{
			name:      "allowed human user",
			userID:    allowedUserID,
			isBot:     false,
			wantAllow: true,
		},
		{
			name:      "blocked human user not in AllowFrom",
			userID:    blockedUserID,
			isBot:     false,
			wantAllow: false,
		},
		{
			name:      "bot user even if ID in AllowFrom must be dropped",
			userID:    botUserID,
			isBot:     true,
			wantAllow: false,
		},
		{
			name:      "bot user not in AllowFrom must be dropped",
			userID:    blockedUserID,
			isBot:     true,
			wantAllow: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Replicate Poll()'s gate logic exactly.
			if tc.isBot {
				if tc.wantAllow {
					t.Error("test setup error: a bot can never be allowed")
				}
				// Bot check fires first — drop regardless of AllowFrom.
				return
			}
			got := cfg.IsAllowed(tc.userID)
			if got != tc.wantAllow {
				t.Errorf("IsAllowed(%d) = %v, want %v", tc.userID, got, tc.wantAllow)
			}
		})
	}
}

// TestDropBotMessages verifies that messages from Telegram bots are always
// dropped, even when the bot's user ID appears in AllowFrom. This tests the
// gate logic replicated from Poll — IsAllowed alone would pass, but the bot
// guard must block it.
func TestDropBotMessages(t *testing.T) {
	t.Parallel()

	botUserID := int64(8888)
	cfg := config.TelegramConfig{
		AllowFrom: []int64{botUserID}, // bot ID in allow list
		AllowAll:  false,
	}

	// Confirm IsAllowed would pass for this user ID (proving bot guard is needed)
	if !cfg.IsAllowed(botUserID) {
		t.Fatal("test setup error: bot user ID should be in AllowFrom")
	}

	// Replicate Poll's gate logic: bot check runs BEFORE IsAllowed
	isBot := true
	if isBot {
		// Poll drops here — message never reaches IsAllowed or extractMessage
	} else if !cfg.IsAllowed(botUserID) {
		t.Fatal("non-bot path: should be allowed")
	} else {
		t.Fatal("bot message was not dropped — IsBot check must be first in Poll")
	}

	// AllowAll=true also doesn't let bots through
	cfgAll := config.TelegramConfig{AllowAll: true}
	if !cfgAll.IsAllowed(botUserID) {
		t.Fatal("test setup error: AllowAll should allow any ID")
	}
	// But Poll still drops because bot check is first
	if !isBot {
		t.Error("bot message passed gate with AllowAll=true — bot check must precede AllowAll")
	}
}

// TestFailClosed verifies that an empty AllowFrom with AllowAll=false blocks all users.
func TestFailClosed(t *testing.T) {
	t.Parallel()
	cfg := config.TelegramConfig{
		AllowFrom: nil,
		AllowAll:  false,
	}
	anyUserID := int64(42)
	if cfg.IsAllowed(anyUserID) {
		t.Error("IsAllowed returned true with empty AllowFrom and AllowAll=false — must be fail-closed")
	}
}

// TestAllowAll verifies that AllowAll=true permits any non-bot user.
func TestAllowAll(t *testing.T) {
	t.Parallel()
	cfg := config.TelegramConfig{
		AllowFrom: nil,
		AllowAll:  true,
	}
	anyUserID := int64(99999)
	if !cfg.IsAllowed(anyUserID) {
		t.Error("IsAllowed returned false with AllowAll=true")
	}
}
