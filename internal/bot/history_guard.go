package bot

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yereke99/stone/internal/greenapi"
	"github.com/yereke99/stone/internal/openai"
	"go.uber.org/zap"
)

const (
	HistoryClassificationNewClient          = "new_client"
	HistoryClassificationLegacyExisting     = "legacy_existing"
	HistoryClassificationLegacyProcessed    = "legacy_processed"
	HistoryClassificationLegacyReengagement = "legacy_reengagement"
	HistoryClassificationHistoryCheckFailed = "history_check_failed"
	HistoryClassificationUnknown            = "unknown"
)

const (
	defaultHistoryGuardLookbackCount        = 10
	defaultHistoryGuardTimeout              = 8 * time.Second
	defaultHistoryGuardAIMessageLimit       = 3
	defaultHistoryGuardAIMaxCharsPerMessage = 400
	defaultHistoryGuardAIMaxTotalChars      = 1200
)

var historyGuardPhonePattern = regexp.MustCompile(`(?:\+?\d[\d\s().-]{7,}\d|[0-9]{7,}@(?:c|g)\.us)`)

type ChatHistorySource interface {
	GetChatHistory(ctx context.Context, chatID string, count int) ([]greenapi.ChatHistoryMessage, error)
}

type HistoryGuardAI interface {
	ClassifyHistoryGuard(ctx context.Context, payload string) (openai.HistoryGuardResponse, error)
}

type HistoryGuardOptions struct {
	Enabled              bool
	LookbackCount        int
	Timeout              time.Duration
	FailClosed           bool
	AIEnabled            bool
	AIMessageLimit       int
	AIMaxCharsPerMessage int
	AIMaxTotalChars      int
}

type HistoryGuardDecision struct {
	Classification        string
	HistoryDetected       bool
	HistoryMessageCount   int
	DoNotAutoStart        bool
	ShouldSoftClarify     bool
	Summary               string
	Reason                string
	CheckedAt             time.Time
	AIUsed                bool
	AIInputMessageCount   int
	AIInputTotalCharCount int
	AIConfidence          float64
	ShouldAutoStartFunnel bool
	AIShouldSoftClarify   bool
	KnownNiche            string
	KnownGoal             string
	KnownDeadline         string
	KnownPackageInterest  string
}

type historyGuardRuntime struct {
	source  ChatHistorySource
	options HistoryGuardOptions
}

func (s *Service) SetHistoryGuard(source ChatHistorySource, options HistoryGuardOptions) {
	options = normalizeHistoryGuardOptions(options)
	s.historyGuard = historyGuardRuntime{
		source:  source,
		options: options,
	}
}

func normalizeHistoryGuardOptions(options HistoryGuardOptions) HistoryGuardOptions {
	if options.LookbackCount <= 0 {
		options.LookbackCount = defaultHistoryGuardLookbackCount
	}
	if options.Timeout <= 0 {
		options.Timeout = defaultHistoryGuardTimeout
	}
	if options.AIMessageLimit <= 0 {
		options.AIMessageLimit = defaultHistoryGuardAIMessageLimit
	}
	if options.AIMaxCharsPerMessage <= 0 {
		options.AIMaxCharsPerMessage = defaultHistoryGuardAIMaxCharsPerMessage
	}
	if options.AIMaxTotalChars <= 0 {
		options.AIMaxTotalChars = defaultHistoryGuardAIMaxTotalChars
	}
	return options
}

func normalizeHistoryClassification(classification string) string {
	switch strings.TrimSpace(classification) {
	case HistoryClassificationNewClient,
		HistoryClassificationLegacyExisting,
		HistoryClassificationLegacyProcessed,
		HistoryClassificationLegacyReengagement,
		HistoryClassificationHistoryCheckFailed,
		HistoryClassificationUnknown:
		return strings.TrimSpace(classification)
	default:
		return HistoryClassificationUnknown
	}
}

func (s *Service) maybeApplyHistoryGuard(ctx context.Context, msg IncomingMessage, text string, language string, conversation Conversation) (bool, error) {
	if shouldSilenceForStoredHistory(conversation) {
		if shouldReengageStoredLegacyConversation(conversation, text) {
			decision := HistoryGuardDecision{
				Classification:      HistoryClassificationLegacyReengagement,
				HistoryDetected:     true,
				HistoryMessageCount: conversation.HistoryMessageCount,
				DoNotAutoStart:      false,
				ShouldSoftClarify:   true,
				Summary:             strings.TrimSpace(conversation.HistorySummary),
				Reason:              "stored_legacy_current_message_reengages",
				CheckedAt:           time.Now().UTC(),
			}
			if decision.Summary == "" {
				decision.Summary = "Stored legacy conversation re-engaged with a new request."
			}
			if err := s.store.ApplyHistoryGuardDecision(ctx, msg.ChatID, decision); err != nil {
				return false, err
			}
			s.info("history guard stored legacy reengagement",
				zap.String("chat_hash", chatFingerprint(msg.ChatID)),
				zap.String("previous_classification", conversation.HistoryClassification),
				zap.String("classification", decision.Classification),
			)
			return true, s.sendAndRemember(ctx, msg.ChatID, LegacyReengagementClarificationText(language), ClientStateAwaitingQualification, 0, fieldNiche, fieldGoal, fieldDeadline)
		}
		s.info("history guard stored decision silenced automation",
			zap.String("chat_hash", chatFingerprint(msg.ChatID)),
			zap.String("classification", conversation.HistoryClassification),
		)
		return true, nil
	}
	if !s.shouldRunHistoryGuard(msg, conversation) {
		return false, nil
	}

	decision, err := s.runHistoryGuard(ctx, msg, text, language)
	if err != nil {
		return false, err
	}
	switch decision.Classification {
	case HistoryClassificationNewClient:
		s.info("history guard final decision",
			zap.String("chat_hash", chatFingerprint(msg.ChatID)),
			zap.String("decision", "continue_funnel"),
			zap.String("classification", decision.Classification),
		)
		return false, nil
	case HistoryClassificationLegacyReengagement:
		s.info("history guard final decision",
			zap.String("chat_hash", chatFingerprint(msg.ChatID)),
			zap.String("decision", "legacy_reengagement"),
			zap.String("classification", decision.Classification),
		)
		return true, s.sendAndRemember(ctx, msg.ChatID, LegacyReengagementClarificationText(language), ClientStateAwaitingQualification, 0, fieldNiche, fieldGoal, fieldDeadline)
	case HistoryClassificationLegacyExisting, HistoryClassificationLegacyProcessed, HistoryClassificationHistoryCheckFailed, HistoryClassificationUnknown:
		s.info("history guard final decision",
			zap.String("chat_hash", chatFingerprint(msg.ChatID)),
			zap.String("decision", "legacy_skip"),
			zap.String("classification", decision.Classification),
		)
		return true, nil
	default:
		s.info("history guard final decision",
			zap.String("chat_hash", chatFingerprint(msg.ChatID)),
			zap.String("decision", "safe_skip"),
			zap.String("classification", decision.Classification),
		)
		return true, nil
	}
}

func shouldReengageStoredLegacyConversation(conversation Conversation, text string) bool {
	if !looksLikeNewOrderRequest(text) {
		return false
	}
	if conversation.HistoryClassification == HistoryClassificationHistoryCheckFailed {
		return false
	}
	return conversation.LegacyExisting ||
		conversation.LegacyProcessed ||
		conversation.HistoryClassification == HistoryClassificationLegacyExisting ||
		conversation.HistoryClassification == HistoryClassificationLegacyProcessed ||
		conversation.HistoryClassification == HistoryClassificationUnknown
}

func (s *Service) shouldRunHistoryGuard(msg IncomingMessage, conversation Conversation) bool {
	options := s.historyGuard.options
	if !options.Enabled || s.historyGuard.source == nil {
		return false
	}
	if msg.LocalChatKnown || !conversation.HistoryCheckedAt.IsZero() {
		return false
	}
	if conversation.AutomationClosed || conversation.HandedOffToOwner || !conversation.TransferredAt.IsZero() ||
		conversation.Stopped || conversation.OptOut {
		return false
	}
	if conversation.InitialMessageSent || conversation.PackagesSent || conversation.SentPortfolio ||
		conversation.QuestionnaireOfferSent || conversation.QuestionnaireSent {
		return false
	}
	return clientStateForConversation(&conversation) == ClientStateNeutralNew
}

func (s *Service) runHistoryGuard(ctx context.Context, msg IncomingMessage, text string, language string) (HistoryGuardDecision, error) {
	options := normalizeHistoryGuardOptions(s.historyGuard.options)
	chatID := strings.TrimSpace(msg.ChatID)
	checkedAt := time.Now().UTC()
	s.info("history guard started",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.Int("lookback_count", options.LookbackCount),
	)

	historyCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	history, err := s.historyGuard.source.GetChatHistory(historyCtx, chatID, options.LookbackCount)
	if err != nil {
		s.warn("history guard fetch failed",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Error(err),
		)
		decision := HistoryGuardDecision{
			Classification:      HistoryClassificationHistoryCheckFailed,
			HistoryDetected:     false,
			HistoryMessageCount: 0,
			DoNotAutoStart:      options.FailClosed,
			Summary:             "GreenAPI chat history check failed.",
			Reason:              "history_fetch_error",
			CheckedAt:           checkedAt,
		}
		if !options.FailClosed {
			decision.Classification = HistoryClassificationNewClient
			decision.ShouldAutoStartFunnel = true
			decision.DoNotAutoStart = false
		}
		if persistErr := s.store.ApplyHistoryGuardDecision(ctx, chatID, decision); persistErr != nil {
			return HistoryGuardDecision{}, persistErr
		}
		s.info("history guard classification result",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("classification", decision.Classification),
			zap.Bool("fail_closed", options.FailClosed),
		)
		return decision, nil
	}

	prior := priorHistoryMessages(history, msg, text)
	s.info("greenapi history loaded",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.Int("loaded_count", len(history)),
		zap.Bool("prior_messages_detected", len(prior) > 0),
	)

	decision := classifyHistoryDeterministically(prior, text, checkedAt)
	decision.HistoryMessageCount = len(prior)
	if len(prior) == 0 {
		decision.HistoryMessageCount = len(history)
	}
	if decision.Classification == HistoryClassificationLegacyExisting && options.AIEnabled {
		decision = s.maybeRefineHistoryGuardWithAI(ctx, decision, msg, text, prior, options)
	} else {
		s.info("history guard ai skipped",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Bool("ai_enabled", options.AIEnabled),
			zap.String("classification", decision.Classification),
		)
	}
	decision = enforceHistoryGuardDecision(decision, len(prior) > 0, text)
	if err := s.store.ApplyHistoryGuardDecision(ctx, chatID, decision); err != nil {
		return HistoryGuardDecision{}, err
	}
	s.info("history guard classification result",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("classification", decision.Classification),
		zap.Bool("history_detected", decision.HistoryDetected),
		zap.Bool("do_not_auto_start", decision.DoNotAutoStart),
		zap.Bool("ai_used", decision.AIUsed),
		zap.Float64("ai_confidence", decision.AIConfidence),
	)
	return decision, nil
}

func classifyHistoryDeterministically(prior []greenapi.ChatHistoryMessage, text string, checkedAt time.Time) HistoryGuardDecision {
	if len(prior) == 0 {
		return HistoryGuardDecision{
			Classification:        HistoryClassificationNewClient,
			HistoryDetected:       false,
			HistoryMessageCount:   0,
			DoNotAutoStart:        false,
			ShouldAutoStartFunnel: true,
			Summary:               "No prior GreenAPI chat history was returned.",
			Reason:                "no_prior_history",
			CheckedAt:             checkedAt,
		}
	}
	classification := HistoryClassificationLegacyExisting
	reason := "prior_history_detected"
	softClarify := false
	if looksLikeNewOrderRequest(text) {
		classification = HistoryClassificationLegacyReengagement
		reason = "current_message_reengages"
		softClarify = true
	} else if historyLooksProcessed(prior) {
		classification = HistoryClassificationLegacyProcessed
		reason = "prior_history_processed"
	}
	return HistoryGuardDecision{
		Classification:        classification,
		HistoryDetected:       true,
		HistoryMessageCount:   len(prior),
		DoNotAutoStart:        classification != HistoryClassificationLegacyReengagement,
		ShouldSoftClarify:     softClarify,
		Summary:               "Prior WhatsApp chat history exists.",
		Reason:                reason,
		CheckedAt:             checkedAt,
		AIShouldSoftClarify:   softClarify,
		AIInputMessageCount:   0,
		AIInputTotalCharCount: 0,
	}
}

func (s *Service) maybeRefineHistoryGuardWithAI(ctx context.Context, decision HistoryGuardDecision, msg IncomingMessage, text string, prior []greenapi.ChatHistoryMessage, options HistoryGuardOptions) HistoryGuardDecision {
	ai, ok := s.ai.(HistoryGuardAI)
	if !ok {
		s.info("history guard ai skipped",
			zap.String("chat_hash", chatFingerprint(msg.ChatID)),
			zap.String("reason", "ai_client_not_available"),
		)
		return decision
	}
	payload, messageCount, totalChars := buildHistoryGuardAIPayload(msg, text, prior, options)
	if strings.TrimSpace(payload) == "" || messageCount == 0 {
		s.info("history guard ai skipped",
			zap.String("chat_hash", chatFingerprint(msg.ChatID)),
			zap.String("reason", "empty_ai_payload"),
		)
		return decision
	}
	s.info("history guard ai called",
		zap.String("chat_hash", chatFingerprint(msg.ChatID)),
		zap.Int("input_message_count", messageCount),
		zap.Int("input_total_chars", totalChars),
	)
	result, err := ai.ClassifyHistoryGuard(ctx, payload)
	if err != nil {
		s.warn("history guard ai failed",
			zap.String("chat_hash", chatFingerprint(msg.ChatID)),
			zap.Error(err),
		)
		return decision
	}
	decision.AIUsed = true
	decision.AIInputMessageCount = messageCount
	decision.AIInputTotalCharCount = totalChars
	decision.AIConfidence = result.Confidence
	decision.AIShouldSoftClarify = result.ShouldSendSoftClarification
	decision.ShouldAutoStartFunnel = result.ShouldAutoStartFunnel
	decision.KnownNiche = strings.TrimSpace(result.KnownFields.Niche)
	decision.KnownGoal = strings.TrimSpace(result.KnownFields.Goal)
	decision.KnownDeadline = strings.TrimSpace(result.KnownFields.Deadline)
	decision.KnownPackageInterest = strings.TrimSpace(result.KnownFields.PackageInterest)
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		decision.Summary = truncateRunes(summary, 240)
	}
	if reason := strings.TrimSpace(result.Reason); reason != "" {
		decision.Reason = truncateRunes(reason, 180)
	}

	switch normalizeHistoryClassification(result.ChatType) {
	case HistoryClassificationLegacyReengagement:
		if result.Confidence >= 0.6 || result.ShouldSendSoftClarification {
			decision.Classification = HistoryClassificationLegacyReengagement
			decision.DoNotAutoStart = false
			decision.ShouldSoftClarify = true
		}
	case HistoryClassificationLegacyProcessed:
		decision.Classification = HistoryClassificationLegacyProcessed
		decision.DoNotAutoStart = true
		decision.ShouldSoftClarify = false
	case HistoryClassificationLegacyExisting:
		decision.Classification = HistoryClassificationLegacyExisting
		decision.DoNotAutoStart = true
		decision.ShouldSoftClarify = false
	default:
		decision.Classification = HistoryClassificationLegacyExisting
		decision.DoNotAutoStart = true
		decision.ShouldSoftClarify = false
	}
	return decision
}

func enforceHistoryGuardDecision(decision HistoryGuardDecision, priorExists bool, currentText string) HistoryGuardDecision {
	decision.Classification = normalizeHistoryClassification(decision.Classification)
	if priorExists {
		decision.HistoryDetected = true
		if decision.Classification == HistoryClassificationNewClient {
			decision.Classification = HistoryClassificationLegacyExisting
		}
		if looksLikeNewOrderRequest(currentText) {
			decision.Classification = HistoryClassificationLegacyReengagement
		}
	}
	switch decision.Classification {
	case HistoryClassificationNewClient:
		decision.DoNotAutoStart = false
		decision.ShouldAutoStartFunnel = true
		decision.ShouldSoftClarify = false
	case HistoryClassificationLegacyReengagement:
		decision.DoNotAutoStart = false
		decision.ShouldAutoStartFunnel = false
		decision.ShouldSoftClarify = true
	case HistoryClassificationLegacyExisting, HistoryClassificationLegacyProcessed, HistoryClassificationHistoryCheckFailed, HistoryClassificationUnknown:
		decision.DoNotAutoStart = true
		decision.ShouldAutoStartFunnel = false
		decision.ShouldSoftClarify = false
	default:
		decision.Classification = HistoryClassificationLegacyExisting
		decision.DoNotAutoStart = true
		decision.ShouldAutoStartFunnel = false
	}
	return decision
}

func shouldSilenceForStoredHistory(conversation Conversation) bool {
	if conversation.HistoryCheckedAt.IsZero() || !conversation.DoNotAutoStart {
		return false
	}
	if conversation.LegacyReengagement || conversation.HistoryClassification == HistoryClassificationLegacyReengagement {
		return false
	}
	switch conversation.HistoryClassification {
	case HistoryClassificationLegacyExisting, HistoryClassificationLegacyProcessed, HistoryClassificationHistoryCheckFailed, HistoryClassificationUnknown:
		return true
	default:
		return conversation.LegacyExisting || conversation.LegacyProcessed
	}
}

func priorHistoryMessages(history []greenapi.ChatHistoryMessage, msg IncomingMessage, currentText string) []greenapi.ChatHistoryMessage {
	currentID := strings.TrimSpace(msg.IDMessage)
	currentText = normalizeForAnalysis(currentText)
	currentTimestamp := msg.Timestamp.Unix()
	result := make([]greenapi.ChatHistoryMessage, 0, len(history))
	for _, item := range history {
		if item.IsDeleted {
			continue
		}
		if currentID != "" && strings.TrimSpace(item.IDMessage) == currentID {
			continue
		}
		itemText := normalizeForAnalysis(item.Text())
		if currentID == "" && item.Direction() == "incoming" && itemText != "" && itemText == currentText && currentTimestamp > 0 {
			if absInt64(item.Timestamp-currentTimestamp) <= 120 {
				continue
			}
		}
		if currentTimestamp > 0 && item.Timestamp > currentTimestamp+5 {
			continue
		}
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Timestamp > result[j].Timestamp
	})
	return result
}

func historyLooksProcessed(history []greenapi.ChatHistoryMessage) bool {
	for _, item := range history {
		text := normalizeForAnalysis(item.Text())
		if text == "" {
			continue
		}
		if containsAny(text, []string{
			"оплат", "предоплат", "чек", "kaspi", "каспи", "инвойс", "счет", "счёт",
			"бриф", "тз", "техническое задание", "менеджер", "передаю", "передал",
			"заказ", "заявка принята", "готово", "отправили", "сдали", "монтаж",
			"договор", "закрыт", "обработк", "brief", "payment", "paid", "manager",
		}) {
			return true
		}
	}
	return false
}

func looksLikeNewOrderRequest(text string) bool {
	normalized := normalizeForAnalysis(text)
	if normalized == "" || isGreeting(normalized) || isAgreement(normalized) {
		return false
	}
	return containsAny(normalized, []string{
		"нужно новое видео", "нужен новый ролик", "нужен ролик", "нужно видео",
		"хочу ролик", "хотим ролик", "хочу видео", "можно заказать", "заказать",
		"сделаете", "сможете сделать", "новый проект", "нового проекта", "новое видео",
		"новый ролик", "сколько стоит", "стоимость", "цена", "прайс", "баға", "қанша",
		"ролик керек", "видео керек", "тапсырыс", "order", "new video", "new project",
		"want a video", "price", "cost",
	})
}

func buildHistoryGuardAIPayload(msg IncomingMessage, currentText string, prior []greenapi.ChatHistoryMessage, options HistoryGuardOptions) (string, int, int) {
	limit := options.AIMessageLimit
	if limit <= 0 {
		limit = defaultHistoryGuardAIMessageLimit
	}
	perMessageLimit := options.AIMaxCharsPerMessage
	if perMessageLimit <= 0 {
		perMessageLimit = defaultHistoryGuardAIMaxCharsPerMessage
	}
	totalLimit := options.AIMaxTotalChars
	if totalLimit <= 0 {
		totalLimit = defaultHistoryGuardAIMaxTotalChars
	}

	type compactMessage struct {
		Direction string `json:"direction"`
		Timestamp string `json:"timestamp"`
		Text      string `json:"text"`
	}
	payload := struct {
		CurrentMessage  compactMessage   `json:"current_message"`
		HistoryMessages []compactMessage `json:"history_messages"`
	}{
		CurrentMessage: compactMessage{
			Direction: "incoming",
			Timestamp: timeForHistoryPayload(msg.Timestamp),
			Text:      truncateRunes(sanitizeHistoryGuardText(currentText), perMessageLimit),
		},
		HistoryMessages: []compactMessage{},
	}
	totalChars := utf8.RuneCountInString(payload.CurrentMessage.Text)
	for _, item := range prior {
		if len(payload.HistoryMessages) >= limit {
			break
		}
		text := truncateRunes(sanitizeHistoryGuardText(item.Text()), perMessageLimit)
		if text == "" {
			continue
		}
		if totalChars+utf8.RuneCountInString(text) > totalLimit {
			remaining := totalLimit - totalChars
			if remaining <= 0 {
				break
			}
			text = truncateRunes(text, remaining)
		}
		payload.HistoryMessages = append(payload.HistoryMessages, compactMessage{
			Direction: historyDirectionForAI(item),
			Timestamp: unixForHistoryPayload(item.Timestamp),
			Text:      text,
		})
		totalChars += utf8.RuneCountInString(text)
		if totalChars >= totalLimit {
			break
		}
	}
	if len(payload.HistoryMessages) == 0 {
		return "", 0, 0
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", 0, 0
	}
	return string(data), len(payload.HistoryMessages) + 1, totalChars
}

func sanitizeHistoryGuardText(text string) string {
	text = strings.TrimSpace(text)
	text = historyGuardPhonePattern.ReplaceAllString(text, "[phone]")
	text = strings.ReplaceAll(text, "\x00", "")
	return strings.Join(strings.Fields(text), " ")
}

func truncateRunes(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit]))
}

func historyDirectionForAI(item greenapi.ChatHistoryMessage) string {
	if direction := item.Direction(); direction != "" {
		return direction
	}
	return "incoming"
}

func timeForHistoryPayload(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func unixForHistoryPayload(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.Unix(value, 0).UTC().Format(time.RFC3339)
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func LegacyReengagementClarificationText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сәлеметсіз бе! Иә, көмектесе аламыз 🙌\n\nҚазір қай жобаға ролик керек екенін жазыңыз:\n1) ниша/сала;\n2) мақсат — өтінім, сату немесе танымалдық;\n3) іске қосу мерзімі?"
	case "en":
		return "Hello! Yes, we can help 🙌\n\nPlease share which project needs a video now:\n1) niche/field;\n2) goal — leads, sales, or awareness;\n3) launch timeline?"
	default:
		return "Здравствуйте! Да, можем помочь 🙌\n\nПодскажите, пожалуйста, для какого проекта нужен ролик сейчас:\n1) ниша/сфера;\n2) цель — заявки, продажи или узнаваемость;\n3) сроки запуска?"
	}
}
