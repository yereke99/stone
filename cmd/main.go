package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yereke99/stone/internal/bot"
	"github.com/yereke99/stone/internal/config"
	"github.com/yereke99/stone/internal/greenapi"
	"github.com/yereke99/stone/internal/logger"
	"github.com/yereke99/stone/internal/openai"
	"go.uber.org/zap"
)

const (
	initialReceiveBackoff = time.Second
	maxReceiveBackoff     = 10 * time.Second
	deleteTimeout         = 10 * time.Second
	startupGracePeriod    = 2 * time.Minute
)

type notificationClient interface {
	ReceiveNotification(ctx context.Context) (*greenapi.Notification, bool, error)
	DeleteNotification(ctx context.Context, receiptID int) error
}

type incomingMessageHandler interface {
	HandleIncomingMessage(ctx context.Context, msg bot.IncomingMessage) error
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	zapLogger, err := logger.New(cfg.AppEnv)
	if err != nil {
		log.Fatalf("logger error: %v", err)
	}
	defer func() {
		_ = zapLogger.Sync()
	}()

	httpClient := &http.Client{Timeout: cfg.HTTPClientTimeout}
	greenClient := greenapi.NewClient(
		cfg.GreenAPI.APIURL,
		cfg.GreenAPI.MediaAPIURL,
		cfg.GreenAPI.IDInstance,
		cfg.GreenAPI.APIToken,
		cfg.ReceiveTimeoutSeconds,
		httpClient,
	)
	openAIClient := openai.NewClient(
		cfg.OpenAI.APIKey,
		cfg.OpenAI.Model,
		cfg.MaxOpenAIOutputTokens,
		cfg.AnalyzerMaxOutputTokens,
		cfg.OpenAI.Temperature,
		httpClient,
	)
	conversationStore, err := bot.NewSQLiteConversationStore(context.Background(), cfg.Database.Path)
	if err != nil {
		log.Fatalf("storage error: %v", err)
	}
	defer func() {
		if err := conversationStore.Close(); err != nil {
			zapLogger.Warn("storage close failed", zap.Error(err))
		}
	}()
	botService := bot.NewService(
		greenClient,
		openAIClient,
		conversationStore,
		cfg.PortfolioVideoDir,
		bot.PortfolioLinks{
			TestURL:     cfg.Portfolio.TestURL,
			BasicURL:    cfg.Portfolio.BasicURL,
			StandardURL: cfg.Portfolio.StandardURL,
		},
		cfg.BotReplyLanguageMode,
		zapLogger,
		cfg.AdminChatIDs...,
	)
	botService.SetDelayedPackageOptions(bot.DelayedPackageOptions{
		Enabled: cfg.NewLeadAutoPackages.Enabled,
		After:   cfg.NewLeadAutoPackages.After,
	})

	checkPortfolioVideos(cfg.PortfolioVideoDir, zapLogger)
	if !cfg.BotAutoReplyEnabled {
		zapLogger.Warn("bot auto reply is disabled; set BOT_AUTO_REPLY_ENABLED=true to start GreenAPI polling")
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serviceStartedAt := time.Now().UTC()
	zapLogger.Info("stone greenapi bot started", zap.String("env", cfg.AppEnv))
	botService.StartDelayedPackageScheduler(ctx)
	runPolling(ctx, greenClient, botService, conversationStore, cfg.BotMaxMessageAge, cfg.BotAutoReplyEnabled, serviceStartedAt, zapLogger)
	zapLogger.Info("application stopped")
}

func runPolling(
	ctx context.Context,
	greenClient notificationClient,
	botService incomingMessageHandler,
	conversationStore *bot.ConversationStore,
	maxMessageAge time.Duration,
	autoReplyEnabled bool,
	serviceStartedAt time.Time,
	zapLogger *zap.Logger,
) {
	backoff := initialReceiveBackoff

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		notification, ok, err := greenClient.ReceiveNotification(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			zapLogger.Warn("receiveNotification failed", zap.Error(err), zap.Duration("backoff", backoff))
			if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
				return
			}
			backoff *= 2
			if backoff > maxReceiveBackoff {
				backoff = maxReceiveBackoff
			}
			continue
		}
		backoff = initialReceiveBackoff

		if !ok {
			continue
		}

		processNotification(ctx, greenClient, botService, conversationStore, maxMessageAge, autoReplyEnabled, serviceStartedAt, notification, zapLogger)
	}
}

func processNotification(
	ctx context.Context,
	greenClient notificationClient,
	botService incomingMessageHandler,
	conversationStore *bot.ConversationStore,
	maxMessageAge time.Duration,
	autoReplyEnabled bool,
	serviceStartedAt time.Time,
	notification *greenapi.Notification,
	zapLogger *zap.Logger,
) {
	if notification == nil {
		return
	}

	receiptID := notification.ReceiptID
	messageID := notification.IDMessage()
	dedupeMessageID := messageID
	processingStarted := false
	processingSucceeded := false
	shouldDelete := false
	zapLogger.Debug("greenapi notification received", notificationDebugFields(notification)...)
	defer func() {
		if recovered := recover(); recovered != nil {
			zapLogger.Error("panic recovered while processing notification", zap.Any("panic", recovered))
		}
		if processingStarted {
			if err := conversationStore.FinishMessageProcessing(context.Background(), dedupeMessageID, processingSucceeded); err != nil {
				zapLogger.Warn("message processing finish failed",
					zap.String("message_id", messageID),
					zap.Error(err),
				)
			}
		}
		if !shouldDelete {
			return
		}
		if receiptID <= 0 {
			return
		}
		deleteCtx, cancel := context.WithTimeout(context.Background(), deleteTimeout)
		defer cancel()
		if err := greenClient.DeleteNotification(deleteCtx, receiptID); err != nil {
			zapLogger.Warn("deleteNotification failed", zap.Error(err))
		}
	}()

	if notification.Body.TypeWebhook == greenapi.TypeWebhookOutgoingMessage {
		shouldDelete = true
		handleOutgoingPhoneWebhook(ctx, conversationStore, notification, zapLogger)
		return
	}
	if notification.Body.TypeWebhook == greenapi.TypeWebhookOutgoingAPIMessage {
		shouldDelete = true
		zapLogger.Debug("greenapi outgoing API notification ignored for manual stop logic",
			zap.String("message_id", messageID),
			zap.String("chat_id", notification.OutgoingChatID()),
			zap.String("chat_hash", bot.ChatFingerprintForLog(notification.OutgoingChatID())),
			zap.String("normalized_text", normalizeManualAdminCommand(notification.Text())),
			zap.Bool("manual_admin_stop_matched", isManualAdminStopNotification(notification)),
		)
		return
	}

	chatID, text, ok, reason := shouldProcessNotification(notification, time.Now(), maxMessageAge, serviceStartedAt, autoReplyEnabled)
	if !ok {
		if reason == "whatsapp_group_automation_disabled" {
			zapLogger.Info("incoming WhatsApp group message skipped; automation disabled for groups",
				zap.String("message_id", messageID),
				zap.String("chat_hash", bot.ChatFingerprintForLog(notification.ChatID())),
				zap.String("type_webhook", notification.Body.TypeWebhook),
				zap.String("message_type", notification.TypeMessage()),
			)
			shouldDelete = true
			return
		}
		zapLogger.Debug("greenapi notification skipped",
			zap.String("reason", reason),
			zap.String("type_webhook", notification.Body.TypeWebhook),
			zap.String("message_type", notification.TypeMessage()),
			zap.String("message_id", messageID),
			zap.String("chat_hash", bot.ChatFingerprintForLog(notification.ChatID())),
		)
		shouldDelete = true
		return
	}
	messageID, dedupeMessageID = notificationMessageKeys(notification, chatID, text)

	zapLogger.Info("incoming notification accepted",
		zap.String("message_type", notification.TypeMessage()),
		zap.String("message_id", messageID),
		zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
		zap.Bool("has_reply_context", notification.QuotedMessageID() != "" || notification.QuotedText() != "" || notification.QuotedCaption() != ""),
		zap.String("quoted_type", notification.QuotedType()),
	)

	localChatKnown, err := conversationStore.ConversationExists(ctx, chatID)
	if err != nil {
		zapLogger.Warn("conversation existence check failed",
			zap.String("message_id", messageID),
			zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
			zap.Error(err),
		)
		return
	}
	dedupeDecision, err := conversationStore.BeginIncomingMessageProcessing(ctx, bot.WhatsAppMessageLog{
		ChatID:            chatID,
		DisplayName:       notification.SenderName(),
		GreenAPIMessageID: messageID,
		DedupeKey:         dedupeMessageID,
		Direction:         "incoming",
		MessageType:       notification.TypeMessage(),
		Text:              text,
		RawPayloadJSON:    notificationRawJSON(notification),
	})
	if err != nil {
		zapLogger.Warn("message dedupe failed", zap.Error(err))
		return
	}
	zapLogger.Info("incoming message dedupe decision",
		zap.String("message_id", messageID),
		zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
		zap.String("decision", string(dedupeDecision)),
	)
	if dedupeDecision == bot.MessageDedupeDuplicate || dedupeDecision == bot.MessageDedupeInFlight {
		zapLogger.Debug("duplicate message skipped",
			zap.String("message_id", messageID),
			zap.String("decision", string(dedupeDecision)),
		)
		shouldDelete = true
		return
	}
	processingStarted = dedupeDecision == bot.MessageDedupeNew
	if conversationStore.IsSuppressedPhoneOrChatID(chatID, bot.NormalizePhone(chatID)) {
		if err := conversationStore.AppendMessage(ctx, chatID, "user", text); err != nil {
			zapLogger.Warn("suppressed incoming message transcript save failed",
				zap.String("message_id", messageID),
				zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
				zap.Error(err),
			)
			return
		}
		if err := conversationStore.MarkIncoming(ctx, chatID, text); err != nil {
			zapLogger.Warn("suppressed incoming state update failed",
				zap.String("message_id", messageID),
				zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
				zap.Error(err),
			)
			return
		}
		processingSucceeded = true
		conversationStore.MarkProcessedMessage(chatID, dedupeMessageID)
		zapLogger.Info("incoming message saved and skipped because chat is in automation suppression list",
			zap.String("message_id", messageID),
			zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
		)
		shouldDelete = true
		return
	}

	msg := bot.IncomingMessage{
		IDMessage:       messageID,
		DedupeKey:       dedupeMessageID,
		ChatID:          chatID,
		SenderName:      notification.SenderName(),
		TypeMessage:     notification.TypeMessage(),
		Text:            text,
		DownloadURL:     notification.DownloadURL(),
		FileName:        notification.FileName(),
		MimeType:        notification.MimeType(),
		Timestamp:       time.Unix(notification.Body.Timestamp, 0).UTC(),
		QuotedMessageID: notification.QuotedMessageID(),
		QuotedText:      notification.QuotedText(),
		QuotedCaption:   notification.QuotedCaption(),
		QuotedType:      notification.QuotedType(),
		QuotedFileName:  notification.QuotedFileName(),
		LocalChatKnown:  localChatKnown,
	}
	if err := botService.HandleIncomingMessage(ctx, msg); err != nil {
		zapLogger.Warn("incoming message handling failed",
			zap.String("message_id", messageID),
			zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
			zap.Error(err),
		)
		return
	}
	processingSucceeded = true
	conversationStore.MarkProcessedMessage(chatID, dedupeMessageID)
	shouldDelete = true
}

func shouldProcessNotification(notification *greenapi.Notification, now time.Time, maxMessageAge time.Duration, serviceStartedAt time.Time, autoReplyEnabled bool) (chatID string, text string, ok bool, reason string) {
	if !autoReplyEnabled {
		return "", "", false, "auto_reply_disabled"
	}
	if notification == nil {
		return "", "", false, "nil_notification"
	}
	if notification.Body.TypeWebhook != greenapi.TypeWebhookIncomingMessage {
		return "", "", false, "unsupported_webhook"
	}
	if notification.Body.Timestamp <= 0 {
		return "", "", false, "empty_timestamp"
	}
	if isStartupStaleNotification(notification, serviceStartedAt) {
		return "", "", false, "older_than_startup_grace"
	}
	if isStaleNotification(notification, maxMessageAge, now) {
		return "", "", false, "older_than_max_age"
	}
	if notification.IsWhatsAppGroupMessage() {
		return notification.ChatID(), "", false, "whatsapp_group_automation_disabled"
	}
	if !notification.IsTextMessage() && !notification.IsMediaMessage() {
		return "", "", false, "unsupported_message_type"
	}
	chatID = notification.ChatID()
	if chatID == "" {
		return "", "", false, "empty_chat_id"
	}
	text = strings.TrimSpace(notification.Text())
	if notification.IsTextMessage() && text == "" {
		return "", "", false, "empty_text"
	}
	return chatID, text, true, "accepted"
}

func handleOutgoingPhoneWebhook(ctx context.Context, conversationStore *bot.ConversationStore, notification *greenapi.Notification, zapLogger *zap.Logger) {
	if notification == nil {
		return
	}
	chatID := strings.TrimSpace(notification.OutgoingChatID())
	text := strings.TrimSpace(notification.Text())
	normalizedText := normalizeManualAdminCommand(text)
	messageID := notification.IDMessage()
	if chatID == "" {
		zapLogger.Debug("greenapi outgoing phone notification skipped",
			zap.String("reason", "empty_chat_id"),
			zap.String("message_id", messageID),
			zap.String("normalized_text", normalizedText),
		)
		return
	}
	if notification.IsWhatsAppGroupMessage() {
		zapLogger.Info("greenapi outgoing phone notification skipped for WhatsApp group",
			zap.String("message_id", messageID),
			zap.String("chat_id", chatID),
			zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
			zap.String("normalized_text", normalizedText),
		)
		return
	}
	if err := conversationStore.LogOutgoingWhatsAppMessage(ctx, bot.WhatsAppMessageLog{
		ChatID:            chatID,
		GreenAPIMessageID: messageID,
		DedupeKey:         outgoingNotificationDedupeKey(notification, chatID, text),
		Direction:         "outgoing",
		MessageType:       notification.TypeMessage(),
		Text:              text,
		RawPayloadJSON:    notificationRawJSON(notification),
		ProcessedAt:       notificationTime(notification),
	}); err != nil {
		zapLogger.Warn("greenapi outgoing phone notification journal failed",
			zap.String("message_id", messageID),
			zap.String("chat_id", chatID),
			zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
			zap.Error(err),
		)
	}
	if !isManualAdminStopNotification(notification) {
		zapLogger.Info("greenapi outgoingMessageReceived received but not a stop command",
			zap.String("message_id", messageID),
			zap.String("chat_id", chatID),
			zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
			zap.String("message_type", notification.TypeMessage()),
			zap.String("normalized_text", normalizedText),
			zap.Bool("manual_outgoing", true),
			zap.Bool("manual_admin_stop_matched", false),
		)
		return
	}
	previous, err := conversationStore.Snapshot(ctx, chatID)
	if err != nil {
		zapLogger.Warn("manual admin stop previous state lookup failed",
			zap.String("message_id", messageID),
			zap.String("chat_id", chatID),
			zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
			zap.String("normalized_text", normalizedText),
			zap.Error(err),
		)
		return
	}
	stoppedAt := notificationTime(notification)
	if err := conversationStore.MarkManualStop(ctx, chatID, messageID, stoppedAt, bot.StoppedByManualAdmin); err != nil {
		zapLogger.Warn("manual admin stop failed",
			zap.String("message_id", messageID),
			zap.String("chat_id", chatID),
			zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
			zap.String("normalized_text", normalizedText),
			zap.String("previous_state", conversationStateForLog(previous)),
			zap.Error(err),
		)
		return
	}
	latest, err := conversationStore.Snapshot(ctx, chatID)
	if err != nil {
		zapLogger.Warn("manual admin stop new state lookup failed",
			zap.String("message_id", messageID),
			zap.String("chat_id", chatID),
			zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
			zap.String("normalized_text", normalizedText),
			zap.String("previous_state", conversationStateForLog(previous)),
			zap.Error(err),
		)
		return
	}
	zapLogger.Info("manual admin stop trigger detected",
		zap.String("message_id", messageID),
		zap.String("chat_id", chatID),
		zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
		zap.String("normalized_text", normalizedText),
		zap.String("previous_state", conversationStateForLog(previous)),
		zap.String("new_state", conversationStateForLog(latest)),
		zap.String("reason", bot.StopReasonManualAdminStop),
		zap.String("stopped_by", bot.StoppedByManualAdmin),
	)
}

func isManualAdminStopNotification(notification *greenapi.Notification) bool {
	if notification == nil || !isManualAdminStopCommand(notification.Text()) {
		return false
	}
	messageType := notification.TypeMessage()
	return messageType == "" || notification.IsTextMessage()
}

func normalizeManualAdminCommand(text string) string {
	return bot.NormalizeAdminStopCommand(text)
}

func isManualAdminStopCommand(text string) bool {
	return bot.IsAdminStopCommand(text)
}

func outgoingNotificationDedupeKey(notification *greenapi.Notification, chatID string, text string) string {
	if notification == nil {
		return ""
	}
	if messageID := notification.IDMessage(); messageID != "" {
		return "out|" + chatID + "|" + messageID
	}
	normalizedText := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
	timestamp := strconv.FormatInt(notification.Body.Timestamp, 10)
	sum := sha256.Sum256([]byte(chatID + "|" + timestamp + "|" + notification.TypeMessage() + "|" + normalizedText))
	return "out|" + chatID + "|fallback|" + timestamp + "|" + hex.EncodeToString(sum[:])[:16]
}

func notificationTime(notification *greenapi.Notification) time.Time {
	if notification != nil && notification.Body.Timestamp > 0 {
		return time.Unix(notification.Body.Timestamp, 0).UTC()
	}
	return time.Now().UTC()
}

func notificationDebugFields(notification *greenapi.Notification) []zap.Field {
	if notification == nil {
		return []zap.Field{zap.Bool("nil_notification", true)}
	}
	chatID := notification.ChatID()
	if notification.Body.TypeWebhook == greenapi.TypeWebhookOutgoingMessage || notification.Body.TypeWebhook == greenapi.TypeWebhookOutgoingAPIMessage {
		chatID = notification.OutgoingChatID()
	}
	text := notification.Text()
	return []zap.Field{
		zap.String("type_webhook", notification.Body.TypeWebhook),
		zap.String("message_direction", notificationDirection(notification.Body.TypeWebhook)),
		zap.String("chat_id", chatID),
		zap.String("chat_hash", bot.ChatFingerprintForLog(chatID)),
		zap.String("sender", strings.TrimSpace(notification.Body.SenderData.Sender)),
		zap.String("message_type", notification.TypeMessage()),
		zap.String("message_id", notification.IDMessage()),
		zap.Int("text_len", len(text)),
		zap.Bool("manual_outgoing", notification.Body.TypeWebhook == greenapi.TypeWebhookOutgoingMessage),
		zap.Bool("api_outgoing", notification.Body.TypeWebhook == greenapi.TypeWebhookOutgoingAPIMessage),
		zap.Bool("manual_admin_stop_matched", notification.Body.TypeWebhook == greenapi.TypeWebhookOutgoingMessage && isManualAdminStopNotification(notification)),
	}
}

func notificationDirection(typeWebhook string) string {
	switch typeWebhook {
	case greenapi.TypeWebhookIncomingMessage:
		return "incoming"
	case greenapi.TypeWebhookOutgoingMessage:
		return "outgoing_manual"
	case greenapi.TypeWebhookOutgoingAPIMessage:
		return "outgoing_api"
	default:
		return ""
	}
}

func conversationStateForLog(conversation bot.Conversation) string {
	if conversation.OptOut {
		return bot.ClientStateOptOut
	}
	if conversation.HandedOffToOwner {
		return bot.ClientStateHandedOff
	}
	if conversation.Stopped || conversation.AutomationClosed {
		return bot.ClientStateStopped
	}
	if state := strings.TrimSpace(conversation.Stage); state != "" {
		return state
	}
	return bot.ClientStateNeutralNew
}

func notificationMessageKeys(notification *greenapi.Notification, chatID string, text string) (messageID string, dedupeKey string) {
	messageID = notification.IDMessage()
	if messageID != "" {
		return messageID, chatID + "|" + messageID
	}
	normalizedText := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
	timestamp := strconv.FormatInt(notification.Body.Timestamp, 10)
	sum := sha256.Sum256([]byte(chatID + "|" + timestamp + "|" + notification.TypeMessage() + "|" + normalizedText))
	return "", chatID + "|fallback|" + timestamp + "|" + hex.EncodeToString(sum[:])[:16]
}

func notificationRawJSON(notification *greenapi.Notification) string {
	if notification == nil {
		return ""
	}
	if len(notification.RawJSON) > 0 {
		return string(notification.RawJSON)
	}
	data, err := json.Marshal(notification)
	if err != nil {
		return ""
	}
	return string(data)
}

func isStaleNotification(notification *greenapi.Notification, maxAge time.Duration, now time.Time) bool {
	if notification == nil || maxAge <= 0 || notification.Body.Timestamp <= 0 {
		return false
	}
	sentAt := time.Unix(notification.Body.Timestamp, 0)
	return now.Sub(sentAt) > maxAge
}

func isStartupStaleNotification(notification *greenapi.Notification, serviceStartedAt time.Time) bool {
	if notification == nil || serviceStartedAt.IsZero() || notification.Body.Timestamp <= 0 {
		return false
	}
	sentAt := time.Unix(notification.Body.Timestamp, 0)
	return sentAt.Before(serviceStartedAt.Add(-startupGracePeriod))
}

func checkPortfolioVideos(videoDir string, zapLogger *zap.Logger) {
	for _, fileName := range bot.ExpectedVideoFiles {
		path := filepath.Join(videoDir, fileName)
		if _, err := os.Stat(path); err != nil {
			zapLogger.Warn("portfolio video file is missing", zap.String("file_name", fileName), zap.Error(err))
		}
	}
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
