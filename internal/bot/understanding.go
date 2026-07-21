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

const customerUnderstandingSystemPrompt = `You are the primary semantic decision-maker for the Stone Production WhatsApp sales bot.
Return JSON only, matching the schema exactly. Do not write markdown or text outside JSON.

Stone Production sells AI advertising videos made in about 48 hours without filming. The backend handles WhatsApp delivery, dedupe, STOP/admin suppression, portfolio file sending, official package prices, persistence, and manager notifications. Your job is to understand the latest client message in full context and return one structured decision that drives all normal actions.

Always reason semantically from:
- current_message: the latest incoming client message and the only source of new client facts;
- recent_messages: up to 10 previous messages in chronological order with roles client, bot, manager, or system;
- known_state: already collected/confirmed lead data, dialogue stage, questionnaire status, sent portfolio/videos, unanswered questions, and previous manager handoff state;
- official_packages and service_information: approved pricing/capability constraints;
- portfolio_catalog: available local example tags. You choose semantic search tags, not file paths.

Understand Russian, Kazakh, English, mixed languages, transliteration, informal writing, spelling mistakes, voice-like transcripts, short contextual answers, and long unstructured business descriptions. Use the client's latest dominant language in customer_reply/reply_text.

Core decision rules:
- ChatGPT generates customer_reply/reply_text. It is the actual WhatsApp reply unless next_action is no_reply.
- Answer the client's direct question first, then acknowledge useful facts, then ask at most one genuinely needed question.
- Never ask for information that is already present in known_state, recent_messages, quoted_context, answered_questions, or lead_updates.
- One client message may update many fields. Extract every clear field at once.
- Keep unknown data null or empty. Do not invent budget, deadline, audience, package, company name, portfolio delivery, or manager facts.
- Distinguish contact_name from company_name and brand_name. WhatsApp display name is not the company unless the client explicitly says so.
- Distinguish business niche, product/service, product features, product advantages, company mission, advertising goal, audience, deadline, format, platform, and budget by meaning.
- A deadline must be a real date, duration, relative time, urgency, launch milestone, flexible-deadline statement, or explicit no-deadline statement. Product features or quality statements are not deadlines.
- Advertising goal is the goal of the requested video/campaign, not merely the company's general mission.
- The bot does not need every possible field before handoff. If company/product/niche/request and intent to proceed are clear enough, ready_for_manager can be true with missing items left unresolved.
- If the client asks for examples or examples would naturally move the sale forward, set should_send_portfolio true and return portfolio_search_tags. Say you will send examples now, but do not claim they were already sent.
- Use manager_summary and recommended_next_step to prepare the manager notification from normalized data, not copied random fragments.
- questionnaire_status must be consistent: completed or transferred_to_manager cannot coexist with questionnaire_confirmation as an unresolved question.
- Use official_packages for prices only: Test 35 000 KZT, Basic 50 000 KZT, Standard from 75 000 KZT. Each package price is for one video unless a manager confirms a custom volume calculation.
- For real actors, public figures, faces, or voices, do not promise cloning/copying without rights. Offer an original AI character or similar mood/style.
- For soft deferrals, set next_action no_reply and reply_text empty unless a short acknowledgement is clearly needed.
- For STOP/opt-out, set recommended_action stop_bot.

Output requirements:
- Fill both lead_updates and extracted_fields. lead_updates is the primary normalized data contract; extracted_fields mirrors legacy fields where applicable.
- Fill confirmed_fields, inferred_fields, unknown_fields, corrected_fields using the allowed field names.
- Fill unanswered/unresolved items in unresolved_questions as human-readable manager-facing items.
- Fill missing_fields only for fields still useful to ask after known_state + lead_updates. Do not include a field just because the schema has it.
- Fill customer_reply and reply_text with the same value.
- Use null for unknown scalar lead_updates/extracted_fields values and [] for unknown arrays.
- confidence is 0..1 for this decision.`

func (s *Service) understandCustomerMessage(ctx context.Context, chatID string, msg IncomingMessage, text string, language string, conversation Conversation) (CustomerAnalysis, bool) {
	fallback := AnalyzeCustomerMessage(text, conversation.Lead, language)
	if s.ai == nil || strings.TrimSpace(text) == "" {
		return fallback, false
	}

	payload := customerUnderstandingPayload(msg, text, language, conversation)
	historySize := len(recentUnderstandingMessages(conversation, text))
	s.info("openai customer decision request started",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("state", conversation.Stage),
		zap.Int("recent_history_size", historySize),
		zap.String("questionnaire_status", questionnaireStatusForMemory(conversation)),
		zap.Strings("unanswered_questions", nonEmptyListFromString(unresolvedQuestionForMemory(conversation))),
	)
	aiCtx, cancel := context.WithTimeout(ctx, customerUnderstandingTimeout)
	defer cancel()

	startedAt := time.Now()
	understanding, err := s.ai.AnalyzeCustomerMessage(aiCtx, customerUnderstandingSystemPrompt, []openai.Message{
		{Role: "user", Content: payload},
	})
	latency := time.Since(startedAt)
	if err != nil {
		fields := []zap.Field{
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("state", conversation.Stage),
			zap.String("fallback_reason", "error"),
			zap.Bool("fallback", true),
			zap.Duration("openai_latency", latency),
		}
		fields = append(fields, openAICustomerUnderstandingLogFields(s.ai, err)...)
		s.warn("openai customer decision failed; technical fallback selected", fields...)
		return CustomerAnalysis{
			Intent:            IntentOther,
			RecommendedAction: "send_text",
			NextAction:        "send_text",
			ReplyText:         OpenAITemporaryFallbackText(language),
			TechnicalFallback: true,
		}, false
	}

	aiAnalysis, ok := customerUnderstandingToAnalysis(understanding, conversation.Lead, language)
	if !ok {
		fields := []zap.Field{
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("state", conversation.Stage),
			zap.String("fallback_reason", "invalid_response"),
			zap.Bool("fallback", true),
			zap.Float64("confidence", understanding.Confidence),
			zap.Duration("openai_latency", latency),
		}
		fields = append(fields, openAICustomerUnderstandingLogFields(s.ai, nil)...)
		s.warn("openai customer decision invalid; technical fallback selected", fields...)
		return CustomerAnalysis{
			Intent:            IntentOther,
			RecommendedAction: "send_text",
			NextAction:        "send_text",
			ReplyText:         OpenAITemporaryFallbackText(language),
			TechnicalFallback: true,
		}, false
	}

	analysis := aiAnalysis
	if !llmDecisionHasPrimaryAction(aiAnalysis) {
		analysis = mergeCustomerAnalysis(fallback, aiAnalysis)
	}
	if isClientDeferText(text) {
		analysis.Intent = IntentDefer
		analysis.WantsQuestionnaire = false
		analysis.ShouldHandoff = false
		analysis.ShouldStop = false
		analysis.Frustrated = false
	}
	updated := conversation.Lead
	updated.ApplyAnalysis(analysis)
	if len(analysis.MissingFields) == 0 {
		analysis.MissingFields = updated.MissingCoreFields()
	} else {
		analysis.MissingFields = normalizeFieldList(analysis.MissingFields)
	}

	s.info("openai customer decision used",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("state", conversation.Stage),
		zap.String("detected_language", normalizeLanguageCode(understanding.Language)),
		zap.String("intent", analysis.Intent),
		zap.String("understood_message", previewText(understanding.MessageMeaning, 180)),
		zap.Int("max_output_tokens", openAIAnalyzerMaxOutputTokens(s.ai)),
		zap.Duration("openai_latency", latency),
		zap.Float64("confidence", understanding.Confidence),
		zap.Bool("handoff_recommended", analysis.ShouldHandoff),
		zap.Bool("stop_recommended", analysis.ShouldStop),
		zap.Bool("should_send_portfolio", analysis.ShouldSendPortfolio),
		zap.String("questionnaire_status", analysis.QuestionnaireStatus),
		zap.String("client_intent", analysis.ClientIntent),
		zap.Strings("missing_fields", analysis.MissingFields),
		zap.Strings("extracted_fields", extractedAnalysisFields(analysis)),
		zap.Strings("corrected_fields", analysis.CorrectedFields),
	)
	return analysis, true
}

func nonEmptyListFromString(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

type openAIAnalyzerRuntimeInfo interface {
	AnalyzerMaxOutputTokens() int
	Model() string
	ResponsesEndpoint() string
}

func openAIAnalyzerMaxOutputTokens(ai SalesAI) int {
	if info, ok := ai.(openAIAnalyzerRuntimeInfo); ok {
		return info.AnalyzerMaxOutputTokens()
	}
	return 0
}

func openAICustomerUnderstandingLogFields(ai SalesAI, err error) []zap.Field {
	details, hasDetails := openai.ErrorDetails(err)
	model := details.Model
	endpoint := details.Endpoint
	statusCode := details.StatusCode
	maxOutputTokens := details.MaxOutputTokens
	if info, ok := ai.(openAIAnalyzerRuntimeInfo); ok {
		if model == "" {
			model = info.Model()
		}
		if endpoint == "" {
			endpoint = info.ResponsesEndpoint()
		}
		if maxOutputTokens == 0 {
			maxOutputTokens = info.AnalyzerMaxOutputTokens()
		}
	}
	fields := []zap.Field{
		zap.String("model", strings.TrimSpace(model)),
		zap.String("endpoint", strings.TrimSpace(endpoint)),
		zap.Int("status_code", statusCode),
		zap.Int("max_output_tokens", maxOutputTokens),
	}
	if hasDetails {
		fields = append(fields, zap.String("openai_operation", details.Operation))
	}
	if err != nil {
		fields = append(fields, zap.String("openai_error", openai.SafeErrorMessage(err)))
	}
	return fields
}

type understandingMessage struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	MessageID string `json:"message_id,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

type understandingQuotedContext struct {
	IsReplyToBot    bool   `json:"is_reply_to_bot"`
	QuotedBotText   string `json:"quoted_bot_text,omitempty"`
	QuotedMessageID string `json:"quoted_message_id,omitempty"`
	QuotedType      string `json:"quoted_type,omitempty"`
}

type understandingQuestionAnswerPair struct {
	BotQuestion    string `json:"bot_question"`
	CustomerAnswer string `json:"customer_answer,omitempty"`
	ExpectedField  string `json:"expected_field,omitempty"`
}

type understandingPendingQuestion struct {
	Text   string   `json:"text"`
	Fields []string `json:"fields"`
}

type understandingPortfolioCase struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Tags   []string `json:"tags"`
	Active bool     `json:"active"`
}

func customerUnderstandingPayload(msg IncomingMessage, text string, language string, conversation Conversation) string {
	// known_state carries the persistent facts; recent_messages is the only
	// history channel and current_message the only copy of the incoming text,
	// so nothing is duplicated in the model context.
	stateSource := conversation
	stateSource.Messages = nil
	stateSource.LastIncomingText = ""
	state := json.RawMessage(conversationPromptJSON(stateSource))
	if !json.Valid(state) {
		state = json.RawMessage(`{}`)
	}
	payload := struct {
		CurrentMessage struct {
			Role      string `json:"role"`
			Text      string `json:"text"`
			MessageID string `json:"message_id,omitempty"`
			Timestamp string `json:"timestamp,omitempty"`
		} `json:"current_message"`
		QuotedContext       understandingQuotedContext        `json:"quoted_context"`
		RecentMessages      []understandingMessage            `json:"recent_messages"`
		PendingQuestions    []understandingPendingQuestion    `json:"pending_questions"`
		QuestionAnswerPairs []understandingQuestionAnswerPair `json:"question_answer_pairs"`
		KnownState          json.RawMessage                   `json:"known_state"`
		ServiceInformation  struct {
			BusinessName       string   `json:"business_name"`
			CoreOffer          string   `json:"core_offer"`
			ProductionTime     string   `json:"production_time"`
			ImportantLimits    []string `json:"important_limits"`
			ManagerUnknownText string   `json:"manager_unknown_text"`
		} `json:"service_information"`
		OfficialPackages []struct {
			Key      string `json:"key"`
			Name     string `json:"name"`
			Price    string `json:"price"`
			FileName string `json:"file_name"`
		} `json:"official_packages"`
		PortfolioCatalog []understandingPortfolioCase `json:"portfolio_catalog"`
		Incoming         struct {
			Text           string `json:"text,omitempty"`
			Type           string `json:"type"`
			Language       string `json:"language"`
			QuotedText     string `json:"quoted_text,omitempty"`
			QuotedCaption  string `json:"quoted_caption,omitempty"`
			QuotedType     string `json:"quoted_type,omitempty"`
			QuotedFileName string `json:"quoted_file_name,omitempty"`
		} `json:"incoming"`
		LastBotQuestion struct {
			Text        string   `json:"text"`
			AskedFields []string `json:"asked_fields"`
		} `json:"last_bot_question"`
		QuestionnaireStatus string   `json:"questionnaire_status"`
		UnansweredQuestions []string `json:"unanswered_questions"`
		MissingFields       []string `json:"current_missing_fields"`
	}{
		KnownState:    state,
		MissingFields: requiredLeadMissingFields(conversation),
	}
	payload.CurrentMessage.Role = "client"
	payload.CurrentMessage.Text = strings.TrimSpace(text)
	payload.CurrentMessage.MessageID = strings.TrimSpace(msg.IDMessage)
	if !msg.Timestamp.IsZero() {
		payload.CurrentMessage.Timestamp = msg.Timestamp.UTC().Format(time.RFC3339)
	}
	payload.QuotedContext = quotedUnderstandingContext(msg)
	payload.RecentMessages = recentUnderstandingMessages(conversation, text)
	payload.PendingQuestions = pendingUnderstandingQuestions(conversation)
	payload.QuestionAnswerPairs = questionAnswerPairsForUnderstanding(msg, text, conversation)
	payload.ServiceInformation.BusinessName = "Stone Production"
	payload.ServiceInformation.CoreOffer = "AI advertising videos without filming, prepared for ad launch"
	payload.ServiceInformation.ProductionTime = "about 48 hours after approved script/materials; manager confirms custom timelines"
	payload.ServiceInformation.ImportantLimits = []string{
		"Do not invent prices beyond official_packages.",
		"Do not claim portfolio media was sent before backend delivery succeeds.",
		"Do not promise copying real people, public figures, faces, or voices without rights.",
		"Use manager handoff for custom pricing, custom volume, legal/rights questions, or qualified leads ready to proceed.",
	}
	payload.ServiceInformation.ManagerUnknownText = "не указано"
	for level := 1; level <= 3; level++ {
		offer, ok := OfferByLevel(level)
		if !ok {
			continue
		}
		price := "от 75 000 тг"
		if level == 1 {
			price = "35 000 тг"
		}
		if level == 2 {
			price = "50 000 тг"
		}
		payload.OfficialPackages = append(payload.OfficialPackages, struct {
			Key      string `json:"key"`
			Name     string `json:"name"`
			Price    string `json:"price"`
			FileName string `json:"file_name"`
		}{
			Key:      packageKey(offer.Level),
			Name:     offer.TitleRU,
			Price:    price,
			FileName: offer.FileName,
		})
	}
	for _, item := range portfolioCaseCatalogue() {
		if !item.IsActive {
			continue
		}
		payload.PortfolioCatalog = append(payload.PortfolioCatalog, understandingPortfolioCase{
			ID:     item.ID,
			Title:  item.Title,
			Tags:   normalizePortfolioTags(item.Tags),
			Active: item.IsActive,
		})
	}
	payload.Incoming.Type = strings.TrimSpace(msg.TypeMessage)
	payload.Incoming.Language = normalizeLanguageCode(language)
	payload.Incoming.QuotedText = strings.TrimSpace(msg.QuotedText)
	payload.Incoming.QuotedCaption = strings.TrimSpace(msg.QuotedCaption)
	payload.Incoming.QuotedType = strings.TrimSpace(msg.QuotedType)
	payload.Incoming.QuotedFileName = strings.TrimSpace(msg.QuotedFileName)
	payload.LastBotQuestion.Text = strings.TrimSpace(conversation.LastReplyText)
	payload.LastBotQuestion.AskedFields = fieldsAskedByMessage(conversation.LastReplyText, conversation.Stage)
	payload.QuestionnaireStatus = questionnaireStatusForMemory(conversation)
	if unresolved := unresolvedQuestionForMemory(conversation); unresolved != "" {
		payload.UnansweredQuestions = append(payload.UnansweredQuestions, unresolved)
	}
	for _, unresolved := range conversation.Lead.UnresolvedQuestions {
		if strings.TrimSpace(unresolved) != "" {
			payload.UnansweredQuestions = appendUniqueString(payload.UnansweredQuestions, unresolved)
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return `{"incoming":{"text":` + strconvQuote(strings.TrimSpace(text)) + `}}`
	}
	return string(data)
}

func quotedUnderstandingContext(msg IncomingMessage) understandingQuotedContext {
	text := strings.TrimSpace(msg.QuotedText)
	if text == "" {
		text = strings.TrimSpace(msg.QuotedCaption)
	}
	return understandingQuotedContext{
		IsReplyToBot:    text != "",
		QuotedBotText:   text,
		QuotedMessageID: strings.TrimSpace(msg.QuotedMessageID),
		QuotedType:      strings.TrimSpace(msg.QuotedType),
	}
}

func recentUnderstandingMessages(conversation Conversation, currentText string) []understandingMessage {
	messages := conversation.Messages
	// The incoming message is persisted before analysis, so it is usually the
	// last stored entry. The payload carries it separately as current_message;
	// drop the stored copy so it appears exactly once in the model context.
	currentText = strings.TrimSpace(currentText)
	if currentText != "" && len(messages) > 0 {
		last := messages[len(messages)-1]
		if (last.Role == "user" || last.Role == "customer") && strings.TrimSpace(last.Content) == currentText {
			messages = messages[:len(messages)-1]
		}
	}
	if len(messages) > 10 {
		messages = messages[len(messages)-10:]
	}
	result := make([]understandingMessage, 0, len(messages))
	for _, message := range messages {
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		role := strings.TrimSpace(message.Role)
		switch role {
		case "assistant", "bot":
			role = "bot"
		case "user", "customer", "client":
			role = "client"
		case "manager", "admin", "owner":
			role = "manager"
		default:
			role = "system"
		}
		item := understandingMessage{Role: role, Text: text}
		if !message.CreatedAt.IsZero() {
			item.Timestamp = message.CreatedAt.UTC().Format(time.RFC3339)
		}
		result = append(result, item)
	}
	return result
}

func pendingUnderstandingQuestions(conversation Conversation) []understandingPendingQuestion {
	question := strings.TrimSpace(conversation.LastReplyText)
	if question == "" {
		return nil
	}
	fields := fieldsAskedByMessage(question, conversation.Stage)
	filtered := make([]string, 0, len(fields))
	for _, field := range fields {
		if !fieldKnownInConversation(conversation, field) {
			filtered = append(filtered, field)
		}
	}
	if len(filtered) == 0 && !strings.Contains(question, "?") {
		return nil
	}
	return []understandingPendingQuestion{{Text: question, Fields: filtered}}
}

func questionAnswerPairsForUnderstanding(msg IncomingMessage, text string, conversation Conversation) []understandingQuestionAnswerPair {
	answer := strings.TrimSpace(text)
	if answer == "" {
		return nil
	}
	question := strings.TrimSpace(msg.QuotedText)
	if question == "" {
		question = strings.TrimSpace(msg.QuotedCaption)
	}
	if question == "" {
		question = strings.TrimSpace(conversation.LastReplyText)
	}
	if question == "" {
		return nil
	}
	fields := expectedFieldsForBotQuestion(question, conversation.Stage)
	if len(fields) == 0 {
		return []understandingQuestionAnswerPair{{BotQuestion: question, CustomerAnswer: answer}}
	}
	pairs := make([]understandingQuestionAnswerPair, 0, len(fields))
	for _, field := range fields {
		pairs = append(pairs, understandingQuestionAnswerPair{
			BotQuestion:    question,
			CustomerAnswer: answer,
			ExpectedField:  field,
		})
	}
	return pairs
}

func expectedFieldsForBotQuestion(question string, stage string) []string {
	fields := fieldsAskedByMessage(question, stage)
	normalized := normalizeForAnalysis(question)
	if containsAny(normalized, []string{"что именно продвигаем", "что продвигаем", "что продаете", "что продаёте"}) {
		fields = append(fields, fieldProductService)
	}
	if containsAny(normalized, []string{"кто ваша аудитория", "ваша аудитория", "кто ваш клиент", "клиенты"}) {
		fields = append(fields, fieldTargetAudience)
	}
	if containsAny(normalized, []string{"какой срок", "срок"}) {
		fields = append(fields, fieldDeadline)
	}
	return normalizeFieldList(fields)
}

func fieldKnownInConversation(conversation Conversation, field string) bool {
	field = normalizeFieldName(field)
	if field == "" {
		return false
	}
	if conversation.CompletedFields[field] {
		return true
	}
	lead := conversation.Lead
	switch field {
	case fieldNiche:
		return isValidNiche(lead.Niche)
	case fieldGoal:
		return isValidGoal(lead.Goal)
	case fieldProductService:
		return strings.TrimSpace(lead.ProductOrService) != ""
	case fieldTargetAudience:
		return strings.TrimSpace(lead.TargetAudience) != ""
	case fieldDeadline:
		return isValidDeadline(lead.Deadline)
	case fieldVideoQuantity:
		return strings.TrimSpace(lead.VideoQuantity) != ""
	case fieldPackageInterest:
		return isValidPackageInterest(lead.SelectedPackage)
	case fieldReferenceLinks:
		return len(lead.ReferenceLinks) > 0 || strings.TrimSpace(lead.WebsiteOrInstagram) != ""
	case fieldLikedFormats:
		return len(lead.LikedFormats) > 0
	case fieldVoicePreference:
		return strings.TrimSpace(lead.VoicePreference) != ""
	default:
		return false
	}
}

func customerUnderstandingToAnalysis(understanding openai.CustomerUnderstanding, current LeadState, language string) (CustomerAnalysis, bool) {
	if understanding.Confidence < 0 || understanding.Confidence > 1 {
		return CustomerAnalysis{}, false
	}

	analysis := CustomerAnalysis{
		Platforms:           []string{},
		Intent:              IntentOther,
		PortfolioTags:       normalizePortfolioTags(append(understanding.PortfolioTags, understanding.PortfolioSearchTags...)),
		RecommendedAction:   strings.TrimSpace(understanding.RecommendedAction),
		NextAction:          strings.TrimSpace(understanding.NextAction),
		ReplyText:           strings.TrimSpace(firstNonEmpty(understanding.CustomerReply, understanding.ReplyText)),
		Confidence:          understanding.Confidence,
		ShouldSendPortfolio: understanding.ShouldSendPortfolio,
		ShouldAskQuestion:   understanding.ShouldAskQuestion,
		QuestionnaireStatus: strings.TrimSpace(understanding.QuestionnaireStatus),
		ClientIntent:        strings.TrimSpace(understanding.ClientIntent),
		ReadyForManager:     understanding.ReadyForManager || understanding.LeadUpdates.ReadinessForManagerHandoff,
		ManagerSummary:      strings.TrimSpace(understanding.ManagerSummary),
		UnresolvedQuestions: append([]string(nil), understanding.UnresolvedQuestions...),
		RecommendedNextStep: strings.TrimSpace(understanding.RecommendedNextStep),
		ConfirmedFields:     normalizeFieldList(understanding.ConfirmedFields),
		InferredFields:      normalizeFieldList(understanding.InferredFields),
		UnknownFields:       normalizeFieldList(understanding.UnknownFields),
		CorrectedFields:     normalizeFieldList(understanding.CorrectedFields),
		MissingFields:       normalizeFieldList(understanding.MissingFields),
	}
	if understanding.NextQuestionField != nil {
		analysis.NextQuestionField = normalizeFieldName(*understanding.NextQuestionField)
	}
	updates := understanding.LeadUpdates
	if strings.TrimSpace(analysis.QuestionnaireStatus) == "" && updates.QuestionnaireStatus != nil {
		analysis.QuestionnaireStatus = strings.TrimSpace(*updates.QuestionnaireStatus)
	}
	if strings.TrimSpace(analysis.ClientIntent) == "" && updates.ClientIntent != nil {
		analysis.ClientIntent = strings.TrimSpace(*updates.ClientIntent)
	}
	if strings.TrimSpace(analysis.RecommendedNextStep) == "" && updates.RecommendedNextStep != nil {
		analysis.RecommendedNextStep = strings.TrimSpace(*updates.RecommendedNextStep)
	}
	for _, question := range updates.UnresolvedQuestions {
		analysis.UnresolvedQuestions = appendUniqueString(analysis.UnresolvedQuestions, question)
	}
	for _, question := range updates.ClientQuestions {
		analysis.ClientQuestions = appendUniqueString(analysis.ClientQuestions, question)
	}
	for _, objection := range updates.Objections {
		analysis.Objections = appendUniqueString(analysis.Objections, objection)
	}
	analysis.ExamplesRequested = updates.ExamplesRequested
	protected := protectedFieldsMap(understanding.DoNotOverwrite)
	lowConfidence := understanding.Confidence > 0 && understanding.Confidence < 0.35
	if !lowConfidence {
		extracted := understanding.ExtractedFields
		legacyExtracted := understanding.Extracted
		if value := normalizedAIString(firstStringPointer(updates.ContactName)); value != "" {
			analysis.ContactName = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(updates.CompanyName)); value != "" {
			analysis.CompanyName = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(updates.BrandName)); value != "" {
			analysis.BrandName = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(updates.BusinessDescription)); value != "" {
			analysis.BusinessDescription = stringPointer(value)
			if analysis.StrongSide == nil {
				analysis.StrongSide = stringPointer(value)
			}
		}
		for _, feature := range updates.ProductFeatures {
			analysis.ProductFeatures = appendUniqueString(analysis.ProductFeatures, feature)
		}
		for _, advantage := range updates.ProductAdvantages {
			analysis.ProductAdvantages = appendUniqueString(analysis.ProductAdvantages, advantage)
		}
		if value := normalizedAIString(firstStringPointer(understanding.Niche, updates.BusinessNiche, extracted.Niche, legacyExtracted.Niche)); !protected[fieldNiche] && isValidNiche(value) && !isNonNicheCandidateText(normalizeForAnalysis(value)) {
			analysis.Niche = stringPointer(normalizeNiche(value))
		}
		if value := normalizeCity(normalizedAIString(firstStringPointer(understanding.City, extracted.City, legacyExtracted.City))); value != "" {
			analysis.City = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(updates.GeographicMarket)); value != "" {
			analysis.GeographicMarket = stringPointer(value)
			if analysis.City == nil {
				analysis.City = stringPointer(value)
			}
		}
		if value := normalizedAIString(firstStringPointer(understanding.Goal, updates.AdvertisingGoal, extracted.Goal, legacyExtracted.Goal)); !protected[fieldGoal] && value != "" {
			if isValidGoal(value) {
				analysis.Goal = stringPointer(value)
			} else if goal := normalizeGoal(value); goal != "" {
				analysis.Goal = stringPointer(goal)
			}
		}
		if value := normalizedAIString(firstStringPointer(updates.DesiredResult)); value != "" {
			analysis.DesiredResult = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(understanding.Deadline, updates.Deadline, extracted.Deadline, legacyExtracted.Deadline)); !protected[fieldDeadline] && value != "" {
			if isValidDeadline(value) {
				analysis.Deadline = stringPointer(value)
			} else if deadline := normalizeDeadline(value); deadline != "" {
				analysis.Deadline = stringPointer(deadline)
			}
		}
		if value := normalizedAIString(firstStringPointer(understanding.Platform, updates.DistributionPlatform, extracted.Platform, legacyExtracted.Platform)); value != "" {
			analysis.Platforms = mergePlatforms(analysis.Platforms, platformsFromAIString(value))
			analysis.DistributionPlatform = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(understanding.TargetAudience, updates.TargetAudience, extracted.TargetAudience, legacyExtracted.TargetAudience)); !protected[fieldTargetAudience] && value != "" {
			analysis.TargetAudience = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(understanding.ProductOrService, updates.ProductOrService, extracted.ProductOrService)); !protected[fieldProductService] && value != "" {
			analysis.ProductOrService = stringPointer(value)
			if analysis.Niche == nil && isValidNiche(value) && !isNonNicheCandidateText(normalizeForAnalysis(value)) {
				analysis.Niche = stringPointer(normalizeNiche(value))
			}
		}
		if value := normalizedAIString(firstStringPointer(understanding.StrongSide, extracted.StrongSide, legacyExtracted.StrongSide)); value != "" {
			analysis.StrongSide = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(understanding.Offer, extracted.Offer, legacyExtracted.Offer)); value != "" {
			analysis.Offer = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(updates.DesiredVideoType)); value != "" {
			analysis.DesiredVideoType = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(updates.DesiredVideoFormat)); value != "" {
			analysis.DesiredVideoFormat = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(updates.DesiredStyle)); value != "" {
			analysis.DesiredStyle = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(updates.VideoDuration)); value != "" {
			analysis.VideoDuration = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(understanding.Budget, updates.Budget, extracted.Budget, legacyExtracted.Budget)); !protected[fieldBudget] && value != "" {
			analysis.Budget = stringPointer(value)
		}
		if value := normalizeVideoQuantity(normalizedAIString(firstStringPointer(understanding.VideoQuantity, understanding.Quantity, updates.NumberOfVideos, extracted.VideoQuantity, extracted.Quantity, legacyExtracted.VideoQuantity))); !protected[fieldVideoQuantity] && value != "" {
			analysis.VideoQuantity = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(understanding.VoicePreference, extracted.VoicePreference)); value != "" {
			analysis.VoicePreference = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(understanding.CopyrightConcern, extracted.CopyrightConcern)); value != "" {
			analysis.CopyrightConcern = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(understanding.CampaignContext, extracted.CampaignContext)); value != "" {
			analysis.CampaignContext = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(understanding.HookIdea, extracted.HookIdea)); value != "" {
			analysis.HookIdea = stringPointer(value)
		}
		for _, liked := range append(understanding.LikedFormats, extracted.LikedFormats...) {
			if protected[fieldLikedFormats] {
				continue
			}
			analysis.LikedFormats = appendUniqueString(analysis.LikedFormats, liked)
		}
		if value := normalizedAIString(firstStringPointer(understanding.WebsiteOrInstagram, extracted.WebsiteOrInstagram)); value != "" {
			analysis.BusinessLink = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(extracted.BusinessLink, legacyExtracted.BusinessLink)); value != "" && analysis.BusinessLink == nil {
			analysis.BusinessLink = stringPointer(value)
		}
		for _, link := range understanding.ReferenceLinks {
			analysis.ReferenceLinks = appendUniqueString(analysis.ReferenceLinks, link)
		}
		for _, link := range extracted.ReferenceLinks {
			analysis.ReferenceLinks = appendUniqueString(analysis.ReferenceLinks, link)
		}
		if analysis.BusinessLink != nil {
			analysis.ReferenceLinks = appendUniqueString(analysis.ReferenceLinks, *analysis.BusinessLink)
		}
		if value := normalizePackageInterest(normalizedAIString(firstStringPointer(understanding.PackageInterest, understanding.SelectedPackage, updates.SelectedPackage, extracted.PackageInterest, extracted.SelectedPackage, legacyExtracted.PackageInterest))); !protected[fieldPackageInterest] && value != "" {
			analysis.PackageInterest = stringPointer(value)
			analysis.SelectedLevel = levelByPackageKey(value)
		} else if updates.PackageRecommendationNeeded && !protected[fieldPackageInterest] {
			analysis.PackageInterest = stringPointer(packageNeedsManagerRecommendation)
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
	case "provide_link", "provide_reference", "business_link", "reference_link":
		analysis.Intent = IntentBusinessLink
	case "asks_examples", "case_request":
		analysis.Intent = IntentPortfolioRequest
	case "niche_specific_case_request":
		analysis.Intent = IntentNicheSpecificCaseRequest
	case "feasibility_question":
		analysis.Intent = IntentFeasibilityQuestion
	case "confusion":
		analysis.Intent = IntentConfusion
	case "voice_question":
		analysis.Intent = IntentVoiceQuestion
	case "copyright_question":
		analysis.Intent = IntentCopyrightQuestion
	case "format_preference":
		analysis.Intent = IntentFormatPreference
	case "negative_selection":
		analysis.Intent = IntentNegativeSelection
	case "asks_packages":
		analysis.Intent = IntentPackageQuestion
		analysis.AsksForMoreOptions = true
	case "asks_price", "price_question":
		analysis.Intent = IntentPriceQuestion
	case "asks_discount", "discount_question", "quantity_answer":
		analysis.Intent = IntentQuantityDiscountQuestion
	case "free_test_request":
		analysis.Intent = IntentObjection
	case "asks_deadline":
		analysis.Intent = IntentDeadlineQuestion
	case "asks_duration":
		analysis.Intent = IntentFAQ
		analysis.FAQKey = faqDuration
	case "asks_human", "human_request":
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
	case "frustration":
		analysis.Intent = IntentFrustration
		analysis.Frustrated = true
	case "stop", "stop_or_opt_out":
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

	if understanding.Sentiment.Negative || understanding.Sentiment.WantsToStop {
		analysis.Intent = IntentNegativeReaction
		analysis.Frustrated = true
	} else if understanding.Sentiment.Frustrated {
		analysis.Intent = IntentFrustration
		analysis.Frustrated = true
	}
	if strings.TrimSpace(understanding.Intent) == "defer" {
		analysis.Intent = IntentDefer
	}
	if strings.TrimSpace(understanding.RecommendedAction) == "send_relevant_examples" && analysis.Intent == IntentOther {
		analysis.Intent = IntentPortfolioRequest
	}
	if analysis.Intent == IntentDefer {
		analysis.ShouldHandoff = false
		analysis.ShouldStop = false
		analysis.Frustrated = false
	}
	if understanding.ReadyForQuestionnaire {
		analysis.WantsQuestionnaire = true
	}
	if understanding.NeedsManager || understanding.NeedsHuman {
		analysis.ShouldHandoff = true
		if analysis.Intent == IntentOther {
			analysis.Intent = IntentHumanRequest
		}
	}
	analysis.ShouldHandoff = analysis.ShouldHandoff || understanding.StateUpdate.ShouldHandoffToManager || analysis.ReadyForManager || analysis.Intent == IntentHumanRequest || analysis.Intent == IntentNegativeReaction
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
	if len(analysis.MissingFields) == 0 {
		analysis.MissingFields = updated.MissingCoreFields()
	}
	for _, answered := range understanding.AnsweredQuestions {
		if field := normalizeFieldName(answered.Field); field != "" {
			analysis.AnsweredQuestions = append(analysis.AnsweredQuestions, AnsweredQuestion{
				BotQuestion:    strings.TrimSpace(answered.BotQuestion),
				CustomerAnswer: strings.TrimSpace(answered.CustomerAnswer),
				Field:          field,
				Confidence:     answered.Confidence,
			})
		}
	}
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
	if ai.VideoQuantity != nil {
		result.VideoQuantity = ai.VideoQuantity
	}
	if ai.VoicePreference != nil {
		result.VoicePreference = ai.VoicePreference
	}
	if ai.CopyrightConcern != nil {
		result.CopyrightConcern = ai.CopyrightConcern
	}
	if ai.CampaignContext != nil {
		result.CampaignContext = ai.CampaignContext
	}
	if ai.HookIdea != nil {
		result.HookIdea = ai.HookIdea
	}
	for _, liked := range ai.LikedFormats {
		result.LikedFormats = appendUniqueString(result.LikedFormats, liked)
	}
	if ai.TargetAudience != nil {
		result.TargetAudience = ai.TargetAudience
	}
	if ai.ProductOrService != nil {
		result.ProductOrService = ai.ProductOrService
	}
	if ai.StrongSide != nil {
		result.StrongSide = ai.StrongSide
	}
	if ai.Offer != nil {
		result.Offer = ai.Offer
	}
	if ai.ContactName != nil {
		result.ContactName = ai.ContactName
	}
	if ai.CompanyName != nil {
		result.CompanyName = ai.CompanyName
	}
	if ai.BrandName != nil {
		result.BrandName = ai.BrandName
	}
	if ai.BusinessDescription != nil {
		result.BusinessDescription = ai.BusinessDescription
	}
	for _, feature := range ai.ProductFeatures {
		result.ProductFeatures = appendUniqueString(result.ProductFeatures, feature)
	}
	for _, advantage := range ai.ProductAdvantages {
		result.ProductAdvantages = appendUniqueString(result.ProductAdvantages, advantage)
	}
	if ai.DesiredResult != nil {
		result.DesiredResult = ai.DesiredResult
	}
	if ai.GeographicMarket != nil {
		result.GeographicMarket = ai.GeographicMarket
	}
	if ai.DesiredVideoType != nil {
		result.DesiredVideoType = ai.DesiredVideoType
	}
	if ai.DesiredVideoFormat != nil {
		result.DesiredVideoFormat = ai.DesiredVideoFormat
	}
	if ai.DesiredStyle != nil {
		result.DesiredStyle = ai.DesiredStyle
	}
	if ai.VideoDuration != nil {
		result.VideoDuration = ai.VideoDuration
	}
	if ai.DistributionPlatform != nil {
		result.DistributionPlatform = ai.DistributionPlatform
	}
	if len(ai.ReferenceLinks) > 0 {
		for _, link := range ai.ReferenceLinks {
			result.ReferenceLinks = appendUniqueString(result.ReferenceLinks, link)
		}
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
	if ai.FAQKey != "" {
		result.FAQKey = ai.FAQKey
	}
	result.WantsQuestionnaire = result.WantsQuestionnaire || ai.WantsQuestionnaire
	result.ShouldHandoff = result.ShouldHandoff || ai.ShouldHandoff
	result.ShouldStop = result.ShouldStop || ai.ShouldStop
	result.Frustrated = result.Frustrated || ai.Frustrated
	result.AsksForFoodExamples = result.AsksForFoodExamples || ai.AsksForFoodExamples
	result.AsksForMoreOptions = result.AsksForMoreOptions || ai.AsksForMoreOptions
	result.ShouldSendPortfolio = result.ShouldSendPortfolio || ai.ShouldSendPortfolio
	result.ShouldAskQuestion = result.ShouldAskQuestion || ai.ShouldAskQuestion
	result.ReadyForManager = result.ReadyForManager || ai.ReadyForManager
	result.ExamplesRequested = result.ExamplesRequested || ai.ExamplesRequested
	if len(ai.PortfolioTags) > 0 {
		result.PortfolioTags = normalizePortfolioTags(append(result.PortfolioTags, ai.PortfolioTags...))
	}
	if strings.TrimSpace(ai.NextQuestionField) != "" {
		result.NextQuestionField = strings.TrimSpace(ai.NextQuestionField)
	}
	if strings.TrimSpace(ai.QuestionnaireStatus) != "" {
		result.QuestionnaireStatus = strings.TrimSpace(ai.QuestionnaireStatus)
	}
	if strings.TrimSpace(ai.ClientIntent) != "" {
		result.ClientIntent = strings.TrimSpace(ai.ClientIntent)
	}
	if strings.TrimSpace(ai.ManagerSummary) != "" {
		result.ManagerSummary = strings.TrimSpace(ai.ManagerSummary)
	}
	if len(ai.UnresolvedQuestions) > 0 {
		result.UnresolvedQuestions = append([]string(nil), ai.UnresolvedQuestions...)
	}
	if strings.TrimSpace(ai.RecommendedNextStep) != "" {
		result.RecommendedNextStep = strings.TrimSpace(ai.RecommendedNextStep)
	}
	if len(ai.ConfirmedFields) > 0 {
		result.ConfirmedFields = normalizeFieldList(append(result.ConfirmedFields, ai.ConfirmedFields...))
	}
	if len(ai.InferredFields) > 0 {
		result.InferredFields = normalizeFieldList(append(result.InferredFields, ai.InferredFields...))
	}
	if len(ai.UnknownFields) > 0 {
		result.UnknownFields = normalizeFieldList(append(result.UnknownFields, ai.UnknownFields...))
	}
	if len(ai.CorrectedFields) > 0 {
		result.CorrectedFields = normalizeFieldList(append(result.CorrectedFields, ai.CorrectedFields...))
	}
	for _, question := range ai.ClientQuestions {
		result.ClientQuestions = appendUniqueString(result.ClientQuestions, question)
	}
	for _, objection := range ai.Objections {
		result.Objections = appendUniqueString(result.Objections, objection)
	}
	if strings.TrimSpace(ai.RecommendedAction) != "" {
		result.RecommendedAction = strings.TrimSpace(ai.RecommendedAction)
	}
	if strings.TrimSpace(ai.NextAction) != "" {
		result.NextAction = strings.TrimSpace(ai.NextAction)
	}
	if strings.TrimSpace(ai.ReplyText) != "" {
		result.ReplyText = strings.TrimSpace(ai.ReplyText)
	}
	if ai.Confidence > 0 {
		result.Confidence = ai.Confidence
	}
	if len(ai.AnsweredQuestions) > 0 {
		result.AnsweredQuestions = append(result.AnsweredQuestions, ai.AnsweredQuestions...)
	}
	result.NicheCorrection = result.NicheCorrection || ai.NicheCorrection
	return result
}

func protectedFieldsMap(fields []string) map[string]bool {
	protected := make(map[string]bool, len(fields))
	for _, field := range normalizeFieldList(fields) {
		protected[field] = true
	}
	return protected
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
	fields := make([]string, 0, 24)
	if analysis.ContactName != nil {
		fields = append(fields, fieldContactName)
	}
	if analysis.CompanyName != nil {
		fields = append(fields, fieldCompanyName)
	}
	if analysis.BrandName != nil {
		fields = append(fields, fieldBrandName)
	}
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
	if analysis.VideoQuantity != nil {
		fields = append(fields, fieldVideoQuantity)
	}
	if analysis.VoicePreference != nil {
		fields = append(fields, fieldVoicePreference)
	}
	if analysis.CopyrightConcern != nil {
		fields = append(fields, "copyright_concern")
	}
	if analysis.CampaignContext != nil {
		fields = append(fields, "campaign_context")
	}
	if analysis.HookIdea != nil {
		fields = append(fields, "hook_idea")
	}
	if len(analysis.LikedFormats) > 0 {
		fields = append(fields, fieldLikedFormats)
	}
	if analysis.TargetAudience != nil {
		fields = append(fields, fieldTargetAudience)
	}
	if analysis.ProductOrService != nil {
		fields = append(fields, fieldProductService)
	}
	if analysis.BusinessDescription != nil {
		fields = append(fields, fieldBusinessDescription)
	}
	if len(analysis.ProductFeatures) > 0 {
		fields = append(fields, fieldProductFeatures)
	}
	if len(analysis.ProductAdvantages) > 0 {
		fields = append(fields, fieldProductAdvantages)
	}
	if analysis.DesiredResult != nil {
		fields = append(fields, fieldDesiredResult)
	}
	if analysis.GeographicMarket != nil {
		fields = append(fields, fieldGeographicMarket)
	}
	if analysis.DesiredVideoType != nil {
		fields = append(fields, fieldVideoType)
	}
	if analysis.DesiredVideoFormat != nil {
		fields = append(fields, fieldVideoFormat)
	}
	if analysis.DesiredStyle != nil {
		fields = append(fields, fieldVideoStyle)
	}
	if analysis.VideoDuration != nil {
		fields = append(fields, fieldVideoDuration)
	}
	if analysis.DistributionPlatform != nil {
		fields = append(fields, fieldDistributionPlatform)
	}
	if analysis.StrongSide != nil {
		fields = append(fields, "strong_side")
	}
	if analysis.Offer != nil {
		fields = append(fields, "offer")
	}
	if analysis.BusinessLink != nil {
		fields = append(fields, "website_or_instagram")
	}
	if len(analysis.ReferenceLinks) > 0 {
		fields = append(fields, "reference_links")
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
