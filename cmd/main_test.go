package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yereke99/stone/internal/bot"
	"github.com/yereke99/stone/internal/greenapi"
	"github.com/yereke99/stone/internal/openai"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
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
	files    []string
}

func (s *testSender) SendMessage(ctx context.Context, chatID string, message string) error {
	s.messages = append(s.messages, message)
	return nil
}

func (s *testSender) SendFileByUpload(ctx context.Context, chatID string, filePath string, caption string) (string, error) {
	s.files = append(s.files, filePath)
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

func TestIncomingGroupNotificationSkippedBeforeHandlerAndLogs(t *testing.T) {
	client := &fakeNotificationClient{}
	handler := &fakeIncomingHandler{}
	store := bot.NewConversationStore()
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)
	groupChatID := "120363123456789@g.us"
	notification := incomingTextNotification(221, "group-msg", groupChatID, "Здравствуйте")

	processNotification(context.Background(), client, handler, store, time.Hour, true, time.Time{}, notification, logger)

	if handler.calls != 0 {
		t.Fatalf("handler calls = %d, want 0", handler.calls)
	}
	if len(client.deleted) != 1 {
		t.Fatalf("deleted receipts = %#v, want one acknowledgement", client.deleted)
	}
	if exists, err := store.ConversationExists(context.Background(), groupChatID); err != nil || exists {
		t.Fatalf("group conversation exists=%v err=%v, want no customer lead row", exists, err)
	}
	if logs.FilterMessage("incoming WhatsApp group message skipped; automation disabled for groups").Len() != 1 {
		t.Fatalf("group skip log not written: %#v", logs.All())
	}
}

func TestGroupNotificationDetectedFromMessageDataRemoteJID(t *testing.T) {
	now := time.Now().UTC()
	notification := incomingTextNotification(222, "group-remote", "77012345678@c.us", "Здравствуйте")
	notification.Body.MessageData.RemoteJID = "120363123456789@g.us"

	if _, _, ok, reason := shouldProcessNotification(notification, now, time.Hour, time.Time{}, true); ok || reason != "whatsapp_group_automation_disabled" {
		t.Fatalf("remoteJID group ok=%v reason=%q, want whatsapp_group_automation_disabled", ok, reason)
	}
}

func TestSuppressedIncomingNotificationSkipsBotAndDeletesReceipt(t *testing.T) {
	client := &fakeNotificationClient{}
	store, err := bot.NewSQLiteConversationStore(context.Background(), filepath.Join(t.TempDir(), "stone.sqlite3"))
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	sender := &testSender{}
	service := bot.NewService(sender, &testAI{}, store, "./video", bot.PortfolioLinks{}, "ru", zap.NewNop())
	notification := incomingTextNotification(231, "suppressed-msg", "77012357383@c.us", "Здравствуйте")

	processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, notification, zap.NewNop())

	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("suppressed notification got automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
	if len(client.deleted) != 1 {
		t.Fatalf("deleted receipts = %#v, want one acknowledgement", client.deleted)
	}

	duplicate := incomingTextNotification(232, "suppressed-msg", "77012357383@c.us", "Здравствуйте")
	processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, duplicate, zap.NewNop())
	if len(client.deleted) != 2 {
		t.Fatalf("duplicate suppressed receipt was not acknowledged: %#v", client.deleted)
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

func TestOutgoingPhoneStopManuallyStopsConversation(t *testing.T) {
	client := &fakeNotificationClient{}
	store := bot.NewConversationStore()
	sender := &testSender{}
	service := bot.NewService(sender, &testAI{}, store, "./video", bot.PortfolioLinks{}, "ru", zap.NewNop())
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)
	chatID := "77033330000@c.us"
	stopNotification := incomingTextNotification(331, "manual-stop", chatID, "  StoP  ")
	stopNotification.Body.TypeWebhook = greenapi.TypeWebhookOutgoingMessage

	processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, stopNotification, logger)

	if len(client.deleted) != 1 {
		t.Fatalf("deleted receipts = %#v, want stop acknowledgement", client.deleted)
	}
	conversation, err := store.Snapshot(context.Background(), chatID)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !conversation.Stopped || !conversation.AutomationClosed || conversation.Stage != bot.ClientStateStopped {
		t.Fatalf("conversation not manually stopped: stage=%q stopped=%v closed=%v", conversation.Stage, conversation.Stopped, conversation.AutomationClosed)
	}
	if conversation.StopReason != bot.StopReasonManualAdminStop || conversation.StoppedBy != bot.StoppedByManualAdmin || conversation.StopMessageID != "manual-stop" {
		t.Fatalf("stop metadata = reason=%q by=%q id=%q", conversation.StopReason, conversation.StoppedBy, conversation.StopMessageID)
	}
	stopLogs := logs.FilterMessage("manual admin stop trigger detected").All()
	if len(stopLogs) != 1 {
		t.Fatalf("stop trigger log count = %d, want 1: %#v", len(stopLogs), logs.All())
	}
	fields := stopLogs[0].ContextMap()
	if fields["chat_id"] != chatID || fields["message_id"] != "manual-stop" || fields["normalized_text"] != "stop" || fields["previous_state"] != bot.ClientStateNeutralNew || fields["new_state"] != bot.ClientStateStopped {
		t.Fatalf("stop trigger log fields = %#v", fields)
	}
	if fields["reason"] != bot.StopReasonManualAdminStop || fields["stopped_by"] != bot.StoppedByManualAdmin {
		t.Fatalf("stop trigger metadata fields = %#v", fields)
	}

	incoming := incomingTextNotification(332, "after-manual-stop", chatID, "Здравствуйте")
	processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, incoming, zap.NewNop())
	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("manual stopped chat got automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
	conversation, err = store.Snapshot(context.Background(), chatID)
	if err != nil {
		t.Fatalf("Snapshot() after incoming error = %v", err)
	}
	if len(conversation.Messages) == 0 || conversation.Messages[len(conversation.Messages)-1].Role != "user" || conversation.Messages[len(conversation.Messages)-1].Content != "Здравствуйте" {
		t.Fatalf("incoming message after stop was not saved: %#v", conversation.Messages)
	}
}

func TestOutgoingPhoneStopCommandVariants(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "russian", text: "стоп"},
		{name: "russian uppercase", text: "СТОП"},
		{name: "russian punctuation", text: " стоп. "},
		{name: "english uppercase", text: "STOP"},
		{name: "english punctuation", text: "stop!"},
		{name: "slash english", text: "/stop"},
		{name: "slash russian", text: "/стоп"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeNotificationClient{}
			store := bot.NewConversationStore()
			service := bot.NewService(&testSender{}, &testAI{}, store, "./video", bot.PortfolioLinks{}, "ru", zap.NewNop())
			chatID := "77035550000@c.us"
			notification := incomingTextNotification(350, "manual-stop-"+tt.name, chatID, tt.text)
			notification.Body.TypeWebhook = greenapi.TypeWebhookOutgoingMessage

			processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, notification, zap.NewNop())

			conversation, err := store.Snapshot(context.Background(), chatID)
			if err != nil {
				t.Fatalf("Snapshot() error = %v", err)
			}
			if conversation.Stage != bot.ClientStateStopped || !conversation.Stopped {
				t.Fatalf("variant %q did not stop conversation: stage=%q stopped=%v", tt.text, conversation.Stage, conversation.Stopped)
			}
		})
	}
}

func TestOutgoingPhoneStopUsesExtendedTextPayload(t *testing.T) {
	client := &fakeNotificationClient{}
	store := bot.NewConversationStore()
	service := bot.NewService(&testSender{}, &testAI{}, store, "./video", bot.PortfolioLinks{}, "ru", zap.NewNop())
	chatID := "77035660000@c.us"
	notification := incomingTextNotification(356, "manual-stop-extended", chatID, "")
	notification.Body.TypeWebhook = greenapi.TypeWebhookOutgoingMessage
	notification.Body.MessageData = greenapi.MessageData{
		TypeMessage: greenapi.TypeMessageExtendedText,
		ExtendedTextMessageData: greenapi.ExtendedTextMessageData{
			Text: " СТОП! ",
		},
	}

	processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, notification, zap.NewNop())

	conversation, err := store.Snapshot(context.Background(), chatID)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if conversation.Stage != bot.ClientStateStopped || !conversation.Stopped {
		t.Fatalf("extended text payload did not stop conversation: stage=%q stopped=%v", conversation.Stage, conversation.Stopped)
	}
}

func TestOutgoingPhoneStopUsesMessageDataChatIDFallback(t *testing.T) {
	client := &fakeNotificationClient{}
	store := bot.NewConversationStore()
	service := bot.NewService(&testSender{}, &testAI{}, store, "./video", bot.PortfolioLinks{}, "ru", zap.NewNop())
	chatID := "77036660000@c.us"
	notification := incomingTextNotification(360, "manual-stop-fallback", "", "стоп.")
	notification.Body.TypeWebhook = greenapi.TypeWebhookOutgoingMessage
	notification.Body.MessageData.ChatID = chatID

	processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, notification, zap.NewNop())

	conversation, err := store.Snapshot(context.Background(), chatID)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if conversation.Stage != bot.ClientStateStopped || !conversation.Stopped {
		t.Fatalf("messageData chat id did not stop conversation: stage=%q stopped=%v", conversation.Stage, conversation.Stopped)
	}
	if exists, err := store.ConversationExists(context.Background(), ""); err != nil || exists {
		t.Fatalf("empty chat conversation exists=%v err=%v, want none", exists, err)
	}
}

func TestOutgoingPhoneDuplicateStopIsIdempotent(t *testing.T) {
	client := &fakeNotificationClient{}
	store := bot.NewConversationStore()
	sender := &testSender{}
	service := bot.NewService(sender, &testAI{}, store, "./video", bot.PortfolioLinks{}, "ru", zap.NewNop())
	chatID := "77036880000@c.us"
	first := incomingTextNotification(368, "manual-stop-duplicate", chatID, "/stop")
	first.Body.TypeWebhook = greenapi.TypeWebhookOutgoingMessage
	duplicate := incomingTextNotification(369, "manual-stop-duplicate", chatID, "/stop")
	duplicate.Body.TypeWebhook = greenapi.TypeWebhookOutgoingMessage

	processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, first, zap.NewNop())
	processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, duplicate, zap.NewNop())

	if len(client.deleted) != 2 {
		t.Fatalf("deleted receipts = %#v, want two acknowledgements", client.deleted)
	}
	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("duplicate manual stop sent automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
	conversation, err := store.Snapshot(context.Background(), chatID)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if conversation.Stage != bot.ClientStateStopped || !conversation.Stopped || conversation.StopMessageID != "manual-stop-duplicate" {
		t.Fatalf("duplicate stop state = stage=%q stopped=%v id=%q", conversation.Stage, conversation.Stopped, conversation.StopMessageID)
	}
}

func TestOutgoingPhoneNormalMessageDoesNotRunAutomation(t *testing.T) {
	client := &fakeNotificationClient{}
	store := bot.NewConversationStore()
	sender := &testSender{}
	service := bot.NewService(sender, &testAI{}, store, "./video", bot.PortfolioLinks{}, "ru", zap.NewNop())
	chatID := "77037770000@c.us"
	notification := incomingTextNotification(370, "manual-normal", chatID, "Здравствуйте, я менеджер")
	notification.Body.TypeWebhook = greenapi.TypeWebhookOutgoingMessage

	processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, notification, zap.NewNop())

	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("normal manual outgoing got automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
	if exists, err := store.ConversationExists(context.Background(), chatID); err != nil || exists {
		t.Fatalf("normal manual outgoing conversation exists=%v err=%v, want no stopped lead row", exists, err)
	}
}

func TestOutgoingPhoneStopFalsePositivesDoNotStop(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		messageType string
	}{
		{name: "normal sentence", text: "stop motion video", messageType: greenapi.TypeMessageText},
		{name: "russian non command", text: "останови ролик", messageType: greenapi.TypeMessageText},
		{name: "media caption", text: "stop", messageType: greenapi.TypeMessageImage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeNotificationClient{}
			store := bot.NewConversationStore()
			service := bot.NewService(&testSender{}, &testAI{}, store, "./video", bot.PortfolioLinks{}, "ru", zap.NewNop())
			chatID := "77037880000@c.us"
			notification := incomingTextNotification(378, "manual-normal-"+tt.name, chatID, tt.text)
			notification.Body.TypeWebhook = greenapi.TypeWebhookOutgoingMessage
			if tt.messageType == greenapi.TypeMessageImage {
				notification.Body.MessageData = greenapi.MessageData{
					TypeMessage: greenapi.TypeMessageImage,
					FileMessageData: greenapi.FileMessageData{
						Caption: tt.text,
					},
				}
			}

			processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, notification, zap.NewNop())

			if exists, err := store.ConversationExists(context.Background(), chatID); err != nil || exists {
				t.Fatalf("conversation exists=%v err=%v, want no stopped lead row", exists, err)
			}
		})
	}
}

func TestIncomingCustomerStopStillUsesIncomingPipeline(t *testing.T) {
	client := &fakeNotificationClient{}
	handler := &fakeIncomingHandler{}
	store := bot.NewConversationStore()
	notification := incomingTextNotification(380, "incoming-stop", "77038880000@c.us", "стоп")

	processNotification(context.Background(), client, handler, store, time.Hour, true, time.Time{}, notification, zap.NewNop())

	if handler.calls != 1 {
		t.Fatalf("handler calls = %d, want incoming customer stop to be processed as incoming", handler.calls)
	}
	if handler.last.Text != "стоп" {
		t.Fatalf("handler text = %q, want стоп", handler.last.Text)
	}
}

func TestManualStopPersistsAcrossSQLiteRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stone.sqlite3")
	store, err := bot.NewSQLiteConversationStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore() error = %v", err)
	}
	client := &fakeNotificationClient{}
	chatID := "77039990000@c.us"
	stopNotification := incomingTextNotification(390, "manual-stop-sqlite", chatID, "стоп.")
	stopNotification.Body.TypeWebhook = greenapi.TypeWebhookOutgoingMessage
	service := bot.NewService(&testSender{}, &testAI{}, store, "./video", bot.PortfolioLinks{}, "ru", zap.NewNop())

	processNotification(context.Background(), client, service, store, time.Hour, true, time.Time{}, stopNotification, zap.NewNop())
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	restartedStore, err := bot.NewSQLiteConversationStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteConversationStore() after restart error = %v", err)
	}
	t.Cleanup(func() {
		_ = restartedStore.Close()
	})
	conversation, err := restartedStore.Snapshot(context.Background(), chatID)
	if err != nil {
		t.Fatalf("Snapshot() after restart error = %v", err)
	}
	if conversation.Stage != bot.ClientStateStopped || !conversation.Stopped || !conversation.AutomationClosed {
		t.Fatalf("stopped state did not persist: stage=%q stopped=%v closed=%v", conversation.Stage, conversation.Stopped, conversation.AutomationClosed)
	}

	sender := &testSender{}
	restartedService := bot.NewService(sender, &testAI{}, restartedStore, "./video", bot.PortfolioLinks{}, "ru", zap.NewNop())
	incoming := incomingTextNotification(391, "after-restart-stop", chatID, "Ок стандарт нам надо")
	processNotification(context.Background(), client, restartedService, restartedStore, time.Hour, true, time.Time{}, incoming, zap.NewNop())

	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("restarted stopped chat got automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
	conversation, err = restartedStore.Snapshot(context.Background(), chatID)
	if err != nil {
		t.Fatalf("Snapshot() after incoming error = %v", err)
	}
	if len(conversation.Messages) == 0 || conversation.Messages[len(conversation.Messages)-1].Content != "Ок стандарт нам надо" {
		t.Fatalf("incoming message after restart stop was not saved: %#v", conversation.Messages)
	}
}

func TestOutgoingAPIStopDoesNotTriggerManualStop(t *testing.T) {
	client := &fakeNotificationClient{}
	handler := &fakeIncomingHandler{}
	store := bot.NewConversationStore()
	chatID := "77034440000@c.us"
	notification := incomingTextNotification(341, "api-stop", chatID, "stop")
	notification.Body.TypeWebhook = greenapi.TypeWebhookOutgoingAPIMessage

	processNotification(context.Background(), client, handler, store, time.Hour, true, time.Time{}, notification, zap.NewNop())

	if handler.calls != 0 {
		t.Fatalf("handler calls = %d, want 0", handler.calls)
	}
	if len(client.deleted) != 1 {
		t.Fatalf("deleted receipts = %#v, want one acknowledgement", client.deleted)
	}
	if exists, err := store.ConversationExists(context.Background(), chatID); err != nil || exists {
		t.Fatalf("conversation exists=%v err=%v, want no manual stop row", exists, err)
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
