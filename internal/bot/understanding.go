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

const customerUnderstandingSystemPrompt = `You are a strict JSON information extraction and reply-planning engine for a WhatsApp sales bot.
You analyze customer messages in Russian, Kazakh, and mixed informal language.
The bot sells AI/video advertising packages for Stone production.
Your job is to understand what the customer already said, extract lead fields, detect frustration, and suggest the next safe response inside the existing sales flow.
Never ask for information that is already known. Never repeat the same question. Return valid JSON only. Do not use markdown. Do not invent facts.
Keep the reply short, natural, polite, and human. If the customer is angry, disappointed, asks for a manager, or wants to stop, recommend manager handoff.
Do not change package prices, package logic, video logic, or manager notification format.

Extract only facts that are clearly present or strongly implied:
- niche/business type;
- city/location;
- goal/purpose;
- deadline/start date/required completion time;
- platform;
- target audience;
- package_interest: test, basic, standard, needs_manager_recommendation, or null.

Examples:
"здравствуйте ниша ! Стирка Ковров в Алматы!" => niche="Стирка ковров", city="Алматы"
"копирайтинг, за три дня надо сделать" => niche="Копирайтинг", deadline="за 3 дня"
"берем стандарт" => package_interest="standard"
"для рекламы" => goal="запуск рекламы"
"Спасибо! Желание пропало уже!" => intent="negative_reaction", frustrated=true, should_handoff_to_manager=true, should_stop_automation=true`

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
		if value := normalizedAIString(understanding.Extracted.Niche); isValidNiche(value) {
			analysis.Niche = stringPointer(normalizeNiche(value))
		}
		if value := normalizeCity(normalizedAIString(understanding.Extracted.City)); value != "" {
			analysis.City = stringPointer(value)
		}
		if value := normalizedAIString(understanding.Extracted.Goal); value != "" {
			if goal := normalizeGoal(value); goal != "" {
				analysis.Goal = stringPointer(goal)
			} else if isValidGoal(value) {
				analysis.Goal = stringPointer(value)
			}
		}
		if value := normalizedAIString(understanding.Extracted.Deadline); value != "" {
			if deadline := normalizeDeadline(value); deadline != "" {
				analysis.Deadline = stringPointer(deadline)
			}
		}
		if value := normalizedAIString(understanding.Extracted.Platform); value != "" {
			analysis.Platforms = mergePlatforms(analysis.Platforms, platformsFromAIString(value))
		}
		if value := normalizedAIString(understanding.Extracted.TargetAudience); value != "" {
			analysis.TargetAudience = stringPointer(value)
		}
		if value := normalizePackageInterest(normalizedAIString(understanding.Extracted.PackageInterest)); value != "" {
			analysis.PackageInterest = stringPointer(value)
			analysis.SelectedLevel = levelByPackageKey(value)
		}
	}

	switch strings.TrimSpace(understanding.Intent) {
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
	analysis.ShouldHandoff = understanding.StateUpdate.ShouldHandoffToManager || analysis.Intent == IntentHumanRequest || analysis.Intent == IntentNegativeReaction
	analysis.ShouldStop = understanding.StateUpdate.ShouldStopAutomation || understanding.Sentiment.WantsToStop || analysis.Intent == IntentNegativeReaction
	if analysis.Intent == IntentHumanRequest {
		analysis.WantsQuestionnaire = true
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
	return result
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
