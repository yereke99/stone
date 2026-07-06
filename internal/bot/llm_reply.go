package bot

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yereke99/stone/internal/openai"
	"go.uber.org/zap"
)

const finalReplySystemPrompt = `Ты — редактор финального WhatsApp-ответа Stone Production.
Бэкенд уже выбрал безопасное действие, stage, поля, медиа и бизнес-логику. Нельзя менять смысл, обещания, цены, stage, пакет, отправку видео, STOP, handoff или порядок процесса.

Сгенерируй только customer-facing reply_text на основе backend_reply_text.
Тон: формальный, деловой, вежливый, профессиональный, без сленга, без чрезмерных эмоций и без давления.
Отвечай на языке клиента, если он понятен; если неясно — на русском.

Правила:
- ответь на прямой вопрос клиента, если backend_reply_text уже содержит этот ответ;
- не спрашивай niche/goal/package/deadline, если backend указал, что поле уже известно;
- не спрашивай deadline в первичной квалификации;
- не выдумывай скидки, сроки, гарантии, ссылки, файлы, кейсы или цены;
- официальные цены: Test 35 000 тг, Basic 50 000 тг, Standard от 75 000 тг;
- если речь о drone/дроне, формулируй как AI-визуализацию/ролик; реальную съёмку с дрона отдельно уточняет менеджер;
- не обещай точный голос/образ знаменитостей или публичных людей без прав;
- не говори, что примеры/видео уже отправлены, если backend_reply_text говорит только о будущей отправке;
- одно входящее сообщение = один короткий текстовый ответ.

Верни строго JSON по схеме: только поле reply_text. Никаких других полей, markdown или пояснений.`

func (s *Service) maybeGenerateConversationReply(ctx context.Context, chatID string, backendReply string, stage string, selectedLevel int, askedFields []string, conversation Conversation) (string, bool) {
	backendReply = strings.TrimSpace(backendReply)
	if !s.llmReply.Enabled || s.ai == nil || backendReply == "" {
		return "", false
	}
	if shouldKeepBackendReplyVerbatim(backendReply, stage) {
		return "", false
	}
	payload := finalReplyPayload(backendReply, stage, selectedLevel, askedFields, conversation)
	timeout := s.llmReply.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	replyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	response, err := s.ai.GenerateReplyText(replyCtx, finalReplySystemPrompt, []openai.Message{
		{Role: "user", Content: payload},
	})
	if err != nil {
		status := "error"
		if replyCtx.Err() != nil {
			status = "timeout"
		}
		s.warn("openai final customer reply failed; using backend fallback",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("stage", stage),
			zap.String("openai_reply_status", status),
			zap.Int("max_output_tokens", s.llmReply.MaxOutputTokens),
			zap.String("openai_error", openai.SafeErrorMessage(err)),
		)
		return "", true
	}
	reply := strings.TrimSpace(response.ReplyText)
	if reply == "" {
		s.warn("openai final customer reply empty; using backend fallback",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("stage", stage),
			zap.String("openai_reply_status", "empty"),
		)
		return "", true
	}
	s.info("openai final customer reply generated",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("stage", stage),
		zap.Bool("dry_run", s.llmReply.DryRun),
		zap.Int("max_output_tokens", s.llmReply.MaxOutputTokens),
		zap.String("backend_reply_preview", previewText(backendReply, 180)),
		zap.String("final_reply_preview", previewText(reply, 180)),
	)
	if s.llmReply.DryRun {
		return "", true
	}
	return reply, true
}

func finalReplyPayload(backendReply string, stage string, selectedLevel int, askedFields []string, conversation Conversation) string {
	payload := struct {
		BackendReplyText string          `json:"backend_reply_text"`
		BackendDecision  map[string]any  `json:"backend_decision"`
		KnownState       json.RawMessage `json:"known_state"`
		RecentMessages   []ChatMessage   `json:"recent_messages"`
		ReplyConstraints []string        `json:"reply_constraints"`
	}{
		BackendReplyText: strings.TrimSpace(backendReply),
		BackendDecision: map[string]any{
			"stage":          strings.TrimSpace(stage),
			"selected_level": selectedLevel,
			"asked_fields":   normalizeFieldList(askedFields),
			"package_examples_required_before_question":  needsPortfolioExamplesBeforeFormatQuestion(backendReply),
			"backend_controls_media_and_state":           true,
			"llm_may_only_rewrite_customer_visible_text": true,
		},
		KnownState:     json.RawMessage(conversationPromptJSON(conversation)),
		RecentMessages: conversation.Messages,
		ReplyConstraints: []string{
			"formal_business_tone",
			"concise_but_complete",
			"preserve_backend_reply_meaning",
			"do_not_add_new_questions_beyond_backend_fields",
			"do_not_claim_media_already_sent",
			"use_only_official_prices",
			"safe_drone_and_copyright_language",
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return strings.TrimSpace(backendReply)
	}
	return string(data)
}

func shouldKeepBackendReplyVerbatim(message string, stage string) bool {
	normalized := normalizeForAnalysis(message)
	if stage == StageBriefRequested || stage == StageBriefCollected || stage == ClientStateHandedOff || stage == StageHandoffRequired {
		return true
	}
	return containsAny(normalized, []string{
		"короткий бриф",
		"1)",
		"2)",
		"3)",
		"4)",
	})
}

func previewText(value string, max int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if max <= 0 || len([]rune(value)) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max])
}
