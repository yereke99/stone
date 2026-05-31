package main

import (
	"context"
	"testing"
	"time"

	"github.com/yereke99/stone/internal/bot"
	"github.com/yereke99/stone/internal/greenapi"
	"github.com/yereke99/stone/internal/openai"
	"go.uber.org/zap"
)

func TestIsStaleNotification(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	tests := []struct {
		name      string
		timestamp int64
		maxAge    time.Duration
		want      bool
	}{
		{
			name:      "fresh",
			timestamp: now.Add(-30 * time.Second).Unix(),
			maxAge:    2 * time.Minute,
			want:      false,
		},
		{
			name:      "stale",
			timestamp: now.Add(-3 * time.Minute).Unix(),
			maxAge:    2 * time.Minute,
			want:      true,
		},
		{
			name:      "missing timestamp",
			timestamp: 0,
			maxAge:    2 * time.Minute,
			want:      false,
		},
		{
			name:      "disabled age guard",
			timestamp: now.Add(-3 * time.Minute).Unix(),
			maxAge:    0,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notification := &greenapi.Notification{
				Body: greenapi.NotificationBody{
					Timestamp: tt.timestamp,
				},
			}
			if got := isStaleNotification(notification, tt.maxAge, now); got != tt.want {
				t.Fatalf("isStaleNotification() = %v, want %v", got, tt.want)
			}
		})
	}
}

type fakeNotificationClient struct {
	deleted []int
}

func (c *fakeNotificationClient) ReceiveNotification(ctx context.Context) (*greenapi.Notification, bool, error) {
	return nil, false, nil
}

func (c *fakeNotificationClient) DeleteNotification(ctx context.Context, receiptID int) error {
	c.deleted = append(c.deleted, receiptID)
	return nil
}

type fakeIncomingHandler struct {
	calls int
	last  bot.IncomingMessage
}

func (h *fakeIncomingHandler) HandleIncomingMessage(ctx context.Context, msg bot.IncomingMessage) error {
	h.calls++
	h.last = msg
	return nil
}

type testSender struct {
	messages []string
}

func (s *testSender) SendMessage(ctx context.Context, chatID string, message string) error {
	s.messages = append(s.messages, message)
	return nil
}

func (s *testSender) SendFileByUpload(ctx context.Context, chatID string, filePath string, caption string) (string, error) {
	return "", nil
}

type testAI struct{}

func (ai *testAI) GenerateSalesReply(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.SalesResponse, error) {
	return openai.SalesResponse{Reply: "Ок.", Language: "ru", Stage: "diagnosis"}, nil
}

func (ai *testAI) AnalyzeCustomerMessage(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.CustomerUnderstanding, error) {
	return openai.CustomerUnderstanding{
		Language:   "ru",
		Intent:     "other",
		Confidence: 1,
	}, nil
}

func TestDuplicateIncomingMessageIDIsIgnored(t *testing.T) {
	client := &fakeNotificationClient{}
	handler := &fakeIncomingHandler{}
	store := bot.NewConversationStore()
	logger := zap.NewNop()

	first := incomingTextNotification(101, "msg-1", "77010000000@c.us", "Здравствуйте")
	second := incomingTextNotification(102, "msg-1", "77010000000@c.us", "Здравствуйте")

	processNotification(context.Background(), client, handler, store, time.Hour, true, time.Time{}, first, logger)
	processNotification(context.Background(), client, handler, store, time.Hour, true, time.Time{}, second, logger)

	if handler.calls != 1 {
		t.Fatalf("handler calls = %d, want 1", handler.calls)
	}
	if len(client.deleted) != 2 {
		t.Fatalf("deleted receipts = %#v, want two acknowledgements", client.deleted)
	}
}

func TestRepeatedSameIDMessageDoesNotSendAgain(t *testing.T) {
	client := &fakeNotificationClient{}
	store := bot.NewConversationStore()
	sender := &testSender{}
	service := bot.NewService(sender, &testAI{}, store, "./video", bot.PortfolioLinks{}, "ru", zap.NewNop())

	notification := incomingTextNotification(201, "same-id", "77020000000@c.us", "Ок стандарт нам надо")
	processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, notification, zap.NewNop())

	duplicate := incomingTextNotification(202, "same-id", "77020000000@c.us", "Ок стандарт нам надо")
	processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, duplicate, zap.NewNop())

	if len(sender.messages) != 1 {
		t.Fatalf("sent messages = %d, want 1: %#v", len(sender.messages), sender.messages)
	}
}

func TestMissingIDMessageUsesFallbackDedupe(t *testing.T) {
	client := &fakeNotificationClient{}
	handler := &fakeIncomingHandler{}
	store := bot.NewConversationStore()
	logger := zap.NewNop()

	first := incomingTextNotification(251, "", "77025000000@c.us", "Здравствуйте")
	second := incomingTextNotification(252, "", "77025000000@c.us", "Здравствуйте")
	second.Body.Timestamp = first.Body.Timestamp

	processNotification(context.Background(), client, handler, store, time.Hour, true, time.Time{}, first, logger)
	processNotification(context.Background(), client, handler, store, time.Hour, true, time.Time{}, second, logger)

	if handler.calls != 1 {
		t.Fatalf("handler calls = %d, want 1", handler.calls)
	}
	if len(client.deleted) != 2 {
		t.Fatalf("deleted receipts = %#v, want two acknowledgements", client.deleted)
	}
}

func TestOutgoingAndStatusNotificationsAreSkipped(t *testing.T) {
	tests := []struct {
		name        string
		typeWebhook string
	}{
		{name: "outgoing", typeWebhook: "outgoingMessageReceived"},
		{name: "status", typeWebhook: "statusInstanceChanged"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeNotificationClient{}
			handler := &fakeIncomingHandler{}
			store := bot.NewConversationStore()
			notification := incomingTextNotification(301, "skip-"+tt.name, "77030000000@c.us", "Здравствуйте")
			notification.Body.TypeWebhook = tt.typeWebhook

			processNotification(context.Background(), client, handler, store, time.Hour, true, time.Time{}, notification, zap.NewNop())

			if handler.calls != 0 {
				t.Fatalf("handler calls = %d, want 0", handler.calls)
			}
			if len(client.deleted) != 1 {
				t.Fatalf("deleted receipts = %#v, want one acknowledgement", client.deleted)
			}
		})
	}
}

func TestOldMessageAfterRestartIsSkipped(t *testing.T) {
	client := &fakeNotificationClient{}
	handler := &fakeIncomingHandler{}
	store := bot.NewConversationStore()
	startedAt := time.Now().UTC()
	notification := incomingTextNotification(401, "old-msg", "77040000000@c.us", "Здравствуйте")
	notification.Body.Timestamp = startedAt.Add(-3 * time.Minute).Unix()

	processNotification(context.Background(), client, handler, store, time.Hour, true, startedAt, notification, zap.NewNop())

	if handler.calls != 0 {
		t.Fatalf("handler calls = %d, want 0", handler.calls)
	}
	if len(client.deleted) != 1 {
		t.Fatalf("deleted receipts = %#v, want one acknowledgement", client.deleted)
	}
}

func TestShouldProcessNotificationRequiresAutoReplyAndText(t *testing.T) {
	now := time.Now().UTC()
	notification := incomingTextNotification(501, "auto-off", "77050000000@c.us", "Здравствуйте")

	if _, _, ok, reason := shouldProcessNotification(notification, now, time.Hour, time.Time{}, false); ok || reason != "auto_reply_disabled" {
		t.Fatalf("auto disabled ok=%v reason=%q, want auto_reply_disabled", ok, reason)
	}

	notification.Body.MessageData.TextMessageData.TextMessage = "   "
	if _, _, ok, reason := shouldProcessNotification(notification, now, time.Hour, time.Time{}, true); ok || reason != "empty_text" {
		t.Fatalf("empty text ok=%v reason=%q, want empty_text", ok, reason)
	}
}

func TestShouldProcessNotificationAcceptsMediaWithoutCaption(t *testing.T) {
	now := time.Now().UTC()
	notification := incomingTextNotification(525, "media-1", "77052500000@c.us", "")
	notification.Body.MessageData = greenapi.MessageData{
		TypeMessage: greenapi.TypeMessageImage,
	}

	chatID, text, ok, reason := shouldProcessNotification(notification, now, time.Hour, time.Time{}, true)
	if !ok || reason != "accepted" {
		t.Fatalf("media ok=%v reason=%q, want accepted", ok, reason)
	}
	if chatID != "77052500000@c.us" || text != "" {
		t.Fatalf("media chatID=%q text=%q", chatID, text)
	}
}

func TestQuotedNotificationUsesCurrentTextNotQuotedText(t *testing.T) {
	now := time.Now().UTC()
	currentText := "1. Фитнес обучение\n2. Заявки+продажи+узнаваемость\n3. К 10 июня"
	quotedText := bot.QualificationGreetingText("ru")
	notification := incomingTextNotification(551, "quoted-current-text", "77055000000@c.us", "")
	notification.Body.MessageData = greenapi.MessageData{
		TypeMessage: greenapi.TypeMessageQuoted,
		ExtendedTextMessageData: greenapi.ExtendedTextMessageData{
			Text:     currentText,
			StanzaID: "old-qualification-greeting",
		},
		QuotedMessage: greenapi.QuotedMessageData{
			StanzaID:    "old-qualification-greeting",
			TypeMessage: greenapi.TypeMessageText,
			TextMessage: quotedText,
		},
	}

	chatID, text, ok, reason := shouldProcessNotification(notification, now, time.Hour, time.Time{}, true)
	if !ok || reason != "accepted" {
		t.Fatalf("shouldProcessNotification ok=%v reason=%q, want accepted", ok, reason)
	}
	if chatID != "77055000000@c.us" {
		t.Fatalf("chatID = %q", chatID)
	}
	if text != currentText {
		t.Fatalf("text = %q, want current quoted reply text", text)
	}
	if notification.QuotedText() != quotedText {
		t.Fatalf("quoted text = %q, want old bot message", notification.QuotedText())
	}
}

func TestQuotedNotificationIsAcceptedAndPassesReplyContext(t *testing.T) {
	client := &fakeNotificationClient{}
	handler := &fakeIncomingHandler{}
	store := bot.NewConversationStore()
	logger := zap.NewNop()
	notification := incomingTextNotification(601, "quoted-reply", "77060000000@c.us", "")
	notification.Body.MessageData = greenapi.MessageData{
		TypeMessage: greenapi.TypeMessageQuoted,
		ExtendedTextMessageData: greenapi.ExtendedTextMessageData{
			Text:     ".",
			StanzaID: "standard-video-id",
		},
		QuotedMessage: greenapi.QuotedMessageData{
			StanzaID:    "standard-video-id",
			TypeMessage: "videoMessage",
			Caption:     "Стандарт (премиум подача)\n\n💰 от 75 000 тг",
			FileName:    "video_level_3.mp4",
		},
	}

	processNotification(context.Background(), client, handler, store, time.Hour, true, time.Time{}, notification, logger)

	if handler.calls != 1 {
		t.Fatalf("handler calls = %d, want 1", handler.calls)
	}
	if handler.last.Text != "." ||
		handler.last.QuotedMessageID != "standard-video-id" ||
		handler.last.QuotedType != "videoMessage" ||
		handler.last.QuotedFileName != "video_level_3.mp4" ||
		handler.last.QuotedCaption == "" {
		t.Fatalf("reply context not passed: %#v", handler.last)
	}
}

func incomingTextNotification(receiptID int, messageID string, chatID string, text string) *greenapi.Notification {
	return &greenapi.Notification{
		ReceiptID: receiptID,
		Body: greenapi.NotificationBody{
			TypeWebhook: greenapi.TypeWebhookIncomingMessage,
			Timestamp:   time.Now().Unix(),
			IDMessage:   messageID,
			SenderData: greenapi.SenderData{
				ChatID: chatID,
			},
			MessageData: greenapi.MessageData{
				TypeMessage: greenapi.TypeMessageText,
				TextMessageData: greenapi.TextMessageData{
					TextMessage: text,
				},
			},
		},
	}
}
