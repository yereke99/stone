package bot

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yereke99/stone/internal/openai"
	"go.uber.org/zap"
)

const customerUnderstandingTimeout = 8 * time.Second

const customerUnderstandingSystemPrompt = `You are a strict JSON lead analyzer for a WhatsApp sales bot.
The bot sells Stone Production AI advertising videos made within 48 hours without filming.
Use Russian, Kazakh, English, and mixed informal WhatsApp text. Treat every short or multiline line as potentially meaningful.
Return valid JSON only. Do not write a free-form chatbot reply. Do not invent facts.

Your job:
- extract structured facts from the latest customer message and conversation context;
- merge meaning with existing lead state mentally, but never overwrite good existing values with null;
- never ask for a field that is already known;
- identify direct requests for examples, packages/options, price, deadline, human manager, or questionnaire.

Fields:
- language: ru, kk, en, mixed, or unknown.
- intent: greeting, qualification_answer, asks_examples, asks_packages, asks_price, asks_discount, asks_deadline, asks_human, ready_to_order, defer, unclear, or irrelevant.
- niche: business category or product niche if known.
- goal: business/video goal if known.
- deadline: launch or production deadline if known.
- platform: advertising/use context such as Instagram, TikTok, site, or advertising.
- product_or_service: concrete product/service if present.
- website_or_instagram: website, Instagram handle, or business link if present.
- package_interest: test, basic, standard, unknown, or null.
- asks_for_food_examples: true when the customer asks whether food/farm-product videos/examples exist.
- asks_for_more_options: true when the customer asks for more execution/package/format options.
- ready_for_questionnaire: true when the customer wants to proceed/open the brief.
- needs_manager: true only for a direct manager/human/operator request.
- missing_fields: only missing core fields among niche and goal after considering existing state. Deadline is NOT a core field and must never be in missing_fields.
- confidence: 0..1.

Rules:
- Understand answers even if they are not formal sentences.
- "По рекламе" is platform/context, not the product niche.
- "Пылесосы" can be the niche/product.
- "Продажи" is a goal.
- "Через 2 дня" is a deadline only when the customer volunteers timing; never ask for it.
- Food/farm products count as a valid niche.
- A website or Instagram handle is business context.
- If niche + goal are known, missing_fields must be [].
- NEVER classify a greeting as a niche: "доброе утро", "добрый день", "здравствуйте", "привет", "салем" are intent "greeting" with niche=null.
- NEVER classify confirmations ("да", "ок", "понял", "хорошо") or timing words ("сейчас", "завтра", "сегодня") as a niche; they carry no niche/goal facts.
- Soft deferrals like "спасибо, позже напишу", "я подумаю", "на днях отпишусь", "свяжусь позже", "пока не готов", "понял спасибо" are intent "defer". They are not negative, not a manager request, not a stop/unsubscribe, and must not need a manager.
- NEVER classify questions about cases/examples ("есть кейсы?", "как отправите кейсы?", "покажите примеры") as a niche; that is intent "asks_examples" with niche=null unless a real product is also named.
- NEVER classify price/package/format questions or "стоп"/"stop" as a niche.
- Extract niche only when the text clearly names a business, product, or service (e.g. "мебель", "ТВ зоны", "продаём обувь", "салон красоты", "кофейня", "онлайн курс").
- If the customer asks examples/formats/packages, classify that request first while still extracting facts.
- If unclear but related, use intent "unclear"; if unrelated, use "irrelevant". Low-meaning fragments like "бот собираюсь" are "unclear" with niche=null.
- Do not confuse outgoing API bot messages with human moderator messages; this analyzer only sees customer messages.

Example 1:
Input:
"По рекламе
Пылесосы
Продажи
Через 2 дня"
Expected:
{"language":"ru","intent":"qualification_answer","niche":"пылесосы","goal":"продажи","deadline":"через 2 дня","platform":"реклама","product_or_service":"пылесосы","website_or_instagram":null,"package_interest":null,"asks_for_food_examples":false,"asks_for_more_options":false,"ready_for_questionnaire":false,"needs_manager":false,"missing_fields":[],"confidence":0.95}

Example 2:
Input:
"С едой есть ролики?
Фермерские продукты.
superferma.kz наш инстаграмм"
Expected:
{"language":"ru","intent":"asks_examples","niche":"фермерские продукты / еда","goal":null,"deadline":null,"platform":null,"product_or_service":"фермерские продукты","website_or_instagram":"superferma.kz","package_interest":null,"asks_for_food_examples":true,"asks_for_more_options":false,"ready_for_questionnaire":false,"needs_manager":false,"missing_fields":["goal"],"confidence":0.9}

Example 4:
Input:
"Доброе утро"
Expected:
{"language":"ru","intent":"greeting","niche":null,"goal":null,"deadline":null,"platform":null,"product_or_service":null,"website_or_instagram":null,"package_interest":null,"asks_for_food_examples":false,"asks_for_more_options":false,"ready_for_questionnaire":false,"needs_manager":false,"missing_fields":["niche","goal"],"confidence":0.95}

Example 5:
Input:
"Есть кейсы?"
Expected:
{"language":"ru","intent":"asks_examples","niche":null,"goal":null,"deadline":null,"platform":null,"product_or_service":null,"website_or_instagram":null,"package_interest":null,"asks_for_food_examples":false,"asks_for_more_options":false,"ready_for_questionnaire":false,"needs_manager":false,"missing_fields":["niche","goal"],"confidence":0.9}

Example 3:
Input:
"в целом интересно, давайте еще варианты исполнения подумаем"
Expected:
{"language":"ru","intent":"asks_packages","niche":null,"goal":null,"deadline":null,"platform":null,"product_or_service":null,"website_or_instagram":null,"package_interest":"unknown","asks_for_food_examples":false,"asks_for_more_options":true,"ready_for_questionnaire":false,"needs_manager":false,"missing_fields":[],"confidence":0.85}`

func (s *Service) understandCustomerMessage(ctx context.Context, chatID string, msg IncomingMessage, text string, language string, conversation Conversation) (CustomerAnalysis, bool) {
	fallback := AnalyzeCustomerMessage(text, conversation.Lead, language)
	if s.ai == nil || strings.TrimSpace(text) == "" {
		return fallback, false
	}

	payload := customerUnderstandingPayload(msg, text, language, conversation)
	aiCtx, cancel := context.WithTimeout(ctx, customerUnderstandingTimeout)
	defer cancel()

	understanding, err := s.ai.AnalyzeCustomerMessage(aiCtx, customerUnderstandingSystemPrompt, []openai.Message{
		{Role: "user", Content: payload},
	})
	if err != nil {
		s.warn("openai customer understanding failed; using deterministic fallback",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("state", conversation.Stage),
			zap.Error(err),
		)
		return fallback, false
	}

	aiAnalysis, ok := customerUnderstandingToAnalysis(understanding, conversation.Lead, language)
	if !ok {
		s.warn("openai customer understanding invalid; using deterministic fallback",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("state", conversation.Stage),
			zap.Float64("confidence", understanding.Confidence),
		)
		return fallback, false
	}

	analysis := mergeCustomerAnalysis(fallback, aiAnalysis)
	if isClientDeferText(text) {
		analysis.Intent = IntentDefer
		analysis.WantsQuestionnaire = false
		analysis.ShouldHandoff = false
		analysis.ShouldStop = false
		analysis.Frustrated = false
	}
	updated := conversation.Lead
	updated.ApplyAnalysis(analysis)
	analysis.MissingFields = updated.MissingCoreFields()

	s.info("openai customer understanding used",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("state", conversation.Stage),
		zap.String("intent", analysis.Intent),
		zap.Float64("confidence", understanding.Confidence),
		zap.Bool("handoff_recommended", analysis.ShouldHandoff),
		zap.Bool("stop_recommended", analysis.ShouldStop),
		zap.Strings("missing_fields", analysis.MissingFields),
		zap.Strings("extracted_fields", extractedAnalysisFields(analysis)),
	)
	return analysis, true
}

func customerUnderstandingPayload(msg IncomingMessage, text string, language string, conversation Conversation) string {
	state := json.RawMessage(conversationPromptJSON(conversation))
	if !json.Valid(state) {
		state = json.RawMessage(`{}`)
	}
	payload := struct {
		Incoming struct {
			Text           string `json:"text"`
			Type           string `json:"type"`
			Language       string `json:"language"`
			QuotedText     string `json:"quoted_text,omitempty"`
			QuotedCaption  string `json:"quoted_caption,omitempty"`
			QuotedType     string `json:"quoted_type,omitempty"`
			QuotedFileName string `json:"quoted_file_name,omitempty"`
		} `json:"incoming"`
		ConversationState json.RawMessage `json:"conversation_state"`
		LastBotQuestion   struct {
			Text        string   `json:"text"`
			AskedFields []string `json:"asked_fields"`
		} `json:"last_bot_question"`
		MissingFields []string `json:"current_missing_fields"`
	}{
		ConversationState: state,
		MissingFields:     requiredLeadMissingFields(conversation),
	}
	payload.Incoming.Text = strings.TrimSpace(text)
	payload.Incoming.Type = strings.TrimSpace(msg.TypeMessage)
	payload.Incoming.Language = normalizeLanguageCode(language)
	payload.Incoming.QuotedText = strings.TrimSpace(msg.QuotedText)
	payload.Incoming.QuotedCaption = strings.TrimSpace(msg.QuotedCaption)
	payload.Incoming.QuotedType = strings.TrimSpace(msg.QuotedType)
	payload.Incoming.QuotedFileName = strings.TrimSpace(msg.QuotedFileName)
	payload.LastBotQuestion.Text = strings.TrimSpace(conversation.LastReplyText)
	payload.LastBotQuestion.AskedFields = fieldsAskedByMessage(conversation.LastReplyText, conversation.Stage)

	data, err := json.Marshal(payload)
	if err != nil {
		return `{"incoming":{"text":` + strconvQuote(strings.TrimSpace(text)) + `}}`
	}
	return string(data)
}

func customerUnderstandingToAnalysis(understanding openai.CustomerUnderstanding, current LeadState, language string) (CustomerAnalysis, bool) {
	if understanding.Confidence < 0 || understanding.Confidence > 1 {
		return CustomerAnalysis{}, false
	}

	analysis := CustomerAnalysis{
		Platforms: []string{},
		Intent:    IntentOther,
	}
	lowConfidence := understanding.Confidence > 0 && understanding.Confidence < 0.35
	if !lowConfidence {
		if value := normalizedAIString(firstStringPointer(understanding.Niche, understanding.Extracted.Niche)); isValidNiche(value) && !isNonNicheCandidateText(normalizeForAnalysis(value)) {
			analysis.Niche = stringPointer(normalizeNiche(value))
		}
		if value := normalizeCity(normalizedAIString(understanding.Extracted.City)); value != "" {
			analysis.City = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(understanding.Goal, understanding.Extracted.Goal)); value != "" {
			if isValidGoal(value) {
				analysis.Goal = stringPointer(value)
			} else if goal := normalizeGoal(value); goal != "" {
				analysis.Goal = stringPointer(goal)
			}
		}
		if value := normalizedAIString(firstStringPointer(understanding.Deadline, understanding.Extracted.Deadline)); value != "" {
			if isValidDeadline(value) {
				analysis.Deadline = stringPointer(value)
			} else if deadline := normalizeDeadline(value); deadline != "" {
				analysis.Deadline = stringPointer(deadline)
			}
		}
		if value := normalizedAIString(firstStringPointer(understanding.Platform, understanding.Extracted.Platform)); value != "" {
			analysis.Platforms = mergePlatforms(analysis.Platforms, platformsFromAIString(value))
		}
		if value := normalizedAIString(understanding.Extracted.TargetAudience); value != "" {
			analysis.TargetAudience = stringPointer(value)
		}
		if value := normalizedAIString(understanding.ProductOrService); value != "" {
			analysis.ProductOrService = stringPointer(value)
			if analysis.Niche == nil && isValidNiche(value) && !isNonNicheCandidateText(normalizeForAnalysis(value)) {
				analysis.Niche = stringPointer(normalizeNiche(value))
			}
		}
		if value := normalizedAIString(understanding.WebsiteOrInstagram); value != "" {
			analysis.BusinessLink = stringPointer(value)
		}
		if value := normalizePackageInterest(normalizedAIString(firstStringPointer(understanding.PackageInterest, understanding.Extracted.PackageInterest))); value != "" {
			analysis.PackageInterest = stringPointer(value)
			analysis.SelectedLevel = levelByPackageKey(value)
		}
	}
	analysis.AsksForFoodExamples = understanding.AsksForFoodExamples
	analysis.AsksForMoreOptions = understanding.AsksForMoreOptions

	switch strings.TrimSpace(understanding.Intent) {
	case "greeting":
		analysis.Intent = IntentGreeting
	case "qualification_answer":
		if analysis.HasBusinessSignal() {
			analysis.Intent = IntentAnswer
		}
	case "asks_examples":
		analysis.Intent = IntentPortfolioRequest
	case "asks_packages":
		analysis.Intent = IntentPackageQuestion
		analysis.AsksForMoreOptions = true
	case "asks_price":
		analysis.Intent = IntentPriceQuestion
	case "asks_discount":
		analysis.Intent = IntentObjection
	case "asks_deadline":
		analysis.Intent = IntentDeadlineQuestion
	case "asks_human":
		analysis.Intent = IntentHumanRequest
	case "ready_to_order":
		analysis.Intent = IntentReadyToOrder
	case "defer", "wait", "client_will_reply_later":
		analysis.Intent = IntentDefer
	case "unclear", "irrelevant":
		analysis.Intent = IntentOther
	case "choose_package":
		analysis.Intent = IntentPackageSelection
	case "request_manager":
		analysis.Intent = IntentHumanRequest
	case "negative_reaction":
		analysis.Intent = IntentNegativeReaction
	case "stop":
		analysis.Intent = IntentMute
	case "provide_info":
		if analysis.HasBusinessSignal() {
			analysis.Intent = IntentAnswer
		}
	case "ask_question":
		analysis.Intent = IntentOther
	default:
		analysis.Intent = IntentOther
	}

	if understanding.Sentiment.Negative || understanding.Sentiment.Frustrated || understanding.Sentiment.WantsToStop {
		analysis.Intent = IntentNegativeReaction
		analysis.Frustrated = true
	}
	if strings.TrimSpace(understanding.Intent) == "defer" {
		analysis.Intent = IntentDefer
	}
	if analysis.Intent == IntentDefer {
		analysis.ShouldHandoff = false
		analysis.ShouldStop = false
		analysis.Frustrated = false
	}
	if understanding.ReadyForQuestionnaire {
		analysis.WantsQuestionnaire = true
	}
	if understanding.NeedsManager {
		analysis.ShouldHandoff = true
		if analysis.Intent == IntentOther {
			analysis.Intent = IntentHumanRequest
		}
	}
	analysis.ShouldHandoff = analysis.ShouldHandoff || understanding.StateUpdate.ShouldHandoffToManager || analysis.Intent == IntentHumanRequest || analysis.Intent == IntentNegativeReaction
	analysis.ShouldStop = understanding.StateUpdate.ShouldStopAutomation || understanding.Sentiment.WantsToStop || analysis.Intent == IntentNegativeReaction
	if analysis.Intent == IntentHumanRequest {
		analysis.WantsQuestionnaire = true
	}
	if analysis.Intent == IntentDefer {
		analysis.WantsQuestionnaire = false
		analysis.ShouldHandoff = false
		analysis.ShouldStop = false
		analysis.Frustrated = false
	}

	updated := current
	updated.ApplyAnalysis(analysis)
	analysis.MissingFields = updated.MissingCoreFields()
	return analysis, true
}

func mergeCustomerAnalysis(fallback CustomerAnalysis, ai CustomerAnalysis) CustomerAnalysis {
	result := fallback
	if ai.Niche != nil {
		result.Niche = ai.Niche
	}
	if ai.City != nil {
		result.City = ai.City
	}
	if ai.Goal != nil {
		result.Goal = ai.Goal
	}
	if ai.Deadline != nil {
		result.Deadline = ai.Deadline
	}
	if len(ai.Platforms) > 0 {
		result.Platforms = mergePlatforms(result.Platforms, ai.Platforms)
	}
	if ai.PreviousAIAds != nil {
		result.PreviousAIAds = ai.PreviousAIAds
	}
	if ai.AIExperience != nil {
		result.AIExperience = ai.AIExperience
	}
	if ai.Budget != nil {
		result.Budget = ai.Budget
	}
	if ai.TargetAudience != nil {
		result.TargetAudience = ai.TargetAudience
	}
	if ai.ProductOrService != nil {
		result.ProductOrService = ai.ProductOrService
	}
	if ai.BusinessLink != nil {
		result.BusinessLink = ai.BusinessLink
	}
	if ai.PackageInterest != nil {
		result.PackageInterest = ai.PackageInterest
		result.SelectedLevel = ai.SelectedLevel
	}
	if ai.SelectedLevel > 0 {
		result.SelectedLevel = ai.SelectedLevel
	}
	if ai.Intent != "" && ai.Intent != IntentOther {
		result.Intent = ai.Intent
	}
	result.WantsQuestionnaire = result.WantsQuestionnaire || ai.WantsQuestionnaire
	result.ShouldHandoff = result.ShouldHandoff || ai.ShouldHandoff
	result.ShouldStop = result.ShouldStop || ai.ShouldStop
	result.Frustrated = result.Frustrated || ai.Frustrated
	result.AsksForFoodExamples = result.AsksForFoodExamples || ai.AsksForFoodExamples
	result.AsksForMoreOptions = result.AsksForMoreOptions || ai.AsksForMoreOptions
	return result
}

func firstStringPointer(values ...*string) *string {
	for _, value := range values {
		if normalizedAIString(value) != "" {
			return value
		}
	}
	return nil
}

func normalizedAIString(value *string) string {
	if value == nil {
		return ""
	}
	clean := strings.TrimSpace(*value)
	if clean == "" || strings.EqualFold(clean, "null") || strings.EqualFold(clean, "unknown") {
		return ""
	}
	return clean
}

func normalizeCity(value string) string {
	if value == "" {
		return ""
	}
	if city := extractCity(value); city != "" {
		return city
	}
	value = strings.Trim(value, " \t\r\n.,!?;:-—")
	if meaningfulRuneCount(value) < 2 {
		return ""
	}
	return strings.Join(strings.Fields(value), " ")
}

func platformsFromAIString(value string) []string {
	value = strings.NewReplacer(";", ",", "/", ",", "|", ",").Replace(value)
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		extracted := extractPlatforms(part)
		if len(extracted) == 0 {
			result = mergePlatforms(result, []string{part})
			continue
		}
		result = mergePlatforms(result, extracted)
	}
	return result
}

func extractedAnalysisFields(analysis CustomerAnalysis) []string {
	fields := make([]string, 0, 8)
	if analysis.Niche != nil {
		fields = append(fields, fieldNiche)
	}
	if analysis.City != nil {
		fields = append(fields, fieldCity)
	}
	if analysis.Goal != nil {
		fields = append(fields, fieldGoal)
	}
	if len(analysis.Platforms) > 0 {
		fields = append(fields, fieldPlatform)
	}
	if analysis.Deadline != nil {
		fields = append(fields, fieldDeadline)
	}
	if analysis.TargetAudience != nil {
		fields = append(fields, "target_audience")
	}
	if analysis.ProductOrService != nil {
		fields = append(fields, "product_or_service")
	}
	if analysis.BusinessLink != nil {
		fields = append(fields, "website_or_instagram")
	}
	if analysis.PackageInterest != nil || analysis.SelectedLevel > 0 {
		fields = append(fields, fieldPackageInterest)
	}
	return fields
}

func strconvQuote(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(data)
}
