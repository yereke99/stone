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

const customerUnderstandingSystemPrompt = `You are a strict JSON understanding layer for the Stone Production WhatsApp sales bot.
Return JSON only, matching the provided schema exactly. Do not write markdown or free text outside JSON.

Stone Production sells AI advertising videos made in 48 hours without filming. The Go service sends messages/media and enforces STOP, admin takeover, suppression, dedupe, prices, legal safety, and state updates. Your job is to understand the latest valid customer message in context.

Output contract:
- language: ru, kk, en, mixed, or unknown.
- intent: one of qualification_answer, business_link, reference_link, price_question, discount_question, quantity_answer, case_request, niche_specific_case_request, feasibility_question, format_preference, negative_selection, confusion, objection, voice_question, copyright_question, package_selection, human_request, stop_or_opt_out, greeting, defer, other.
- message_meaning: short plain-language meaning of the latest customer message in context.
- should_update_state: true only when the latest message carries clear new business facts or a clear stop/handoff/proceed signal.
- extracted_fields: always include every field from the schema. Use null for unknown scalar fields and [] for unknown arrays.
- do_not_overwrite_fields: fields that must stay unchanged because the latest message is weak, negative, contextual, or only answers a bot question.
- answered_questions: bot question/customer answer pairs when the context shows which question was answered.
- missing_fields: only fields still missing after persisted state + extracted_fields + answered_questions. Do not include deadline unless the bot explicitly asked it. Core first-stage fields are niche and goal.
- recommended_action: send_text, send_relevant_examples, ask_goal, ask_next_question, send_price_options, send_questionnaire, answer_question, handoff, stop_bot, or no_reply.
- reply_text: a safe suggested short reply, but do not claim that media was sent. The Go service may ignore this in dry-run or system flows.
- next_action: send_text, send_cases, send_video, send_relevant_examples, ask_next_question, handoff, or no_reply.
- portfolio_tags: normalized tags for example selection, e.g. real_estate, land, property, drone, visualization, tourism, travel, hotel, auto, fashion, food, restaurant.
- needs_human: true only for explicit human/manager requests or when a safe answer requires a manager.
- confidence: 0..1.

Context rules:
- current_message.text is the only source for new facts. quoted_context, recent_messages, last bot questions, and question_answer_pairs explain what the customer is answering.
- If the customer replied to a quoted bot message, use quoted_context as primary question context.
- Multiline, paragraph, comma-separated, and short replies can answer several questions at once.
- A URL-only message must never set goal. Save Instagram/Reels/TikTok/YouTube/video links as reference_links unless the customer explicitly says it is their business account/site.
- Extract goal only from explicit text such as "нужно больше заявок", "цель продажи", "для узнаваемости", "хочу продать", "запуск продаж".
- "Плиточные клея, шпаклёвка, штукатурка..." is product_or_service and niche around construction/finishing mixtures. "Основные клиенты..." is target_audience, not goal.
- "оба хороши" means format_preference with liked_formats ["both"].
- "20-30" after a quantity/discount question means quantity/video_quantity, not budget, niche, goal, or deadline.
- If the bot asked "Какой формат вам понравился?" and the customer answers "Никакой", this is negative_selection. It means no shown format was chosen. It is not niche, not goal, and must put "niche" in do_not_overwrite_fields.
- If the customer already described a project, preserve that as the main business context. Short replies like "никакой", "нет", "не знаю", "ок", "да", "потом", "сейчас", "завтра", "посмотрю" must not overwrite niche or goal.
- Real estate, land, apartment, construction, realtor -> portfolio_tags should include real_estate/property; land plots -> land; drone shooting -> drone; perspective visualization/renders -> visualization.
- Tourism/travel/hotel/resort -> tourism/travel. Cars/dealership -> auto. Clothes/fashion -> fashion. Restaurant/food/cafe -> food/restaurant.
- Barbershop/barber/шаштараз -> niche "барбершоп / услуги барбершопа" and portfolio_tags barbershop/beauty/salon.
- Forklift/loader/погрузчик/спецтехника/industrial equipment -> niche "погрузчик / спецтехника" and portfolio_tags construction/industrial/equipment.
- "Делаете примерно такое видео?", "можете как тут?", "такой формат делаете?" are feasibility_question, not case_request.
- "пример" should only mean examples/cases as a real word; "примерно" is not an examples request.
- "Чет суть не уловил" and similar messages are confusion, not other.
- Voice questions include "Озвучку кто делать будет?", "Голос выбрать можно?", "можно голос актёра?".
- Never promise exact cloning/use of a real actor, celebrity, public figure, face, image, or voice without rights. Offer a similar mood/tone/style without copying identity.
- Do not invent prices, discounts, deadlines, guarantees, rights, package details, links, files, or case availability. Prices are Test 35 000 тг, Basic 50 000 тг, Standard from 75 000 тг.
- Answer the customer's latest direct question first, then confirm extracted facts if useful, then ask at most one next useful question.
- Never ask for a field already known in known_state, extracted_fields, answered_questions, recent history, or quoted context.
- Match the latest customer message language. Kazakh latest message -> Kazakh reply_text; Russian latest message -> Russian reply_text. Do not mix languages unless the customer did.
- Soft deferrals like "подумаю", "позже напишу", "на днях отпишусь", "понял спасибо" are defer with next_action no_reply and empty reply_text.

Examples:
- Bot asked "Что продвигаем, кто аудитория и какая цель?" Customer: "Плиточные клея, шпаклёвка, штукатурка и т.д.\nОсновные клиенты строительные компании, частники!" -> product_or_service "плиточный клей, шпаклёвка, штукатурка", niche "строительные смеси", target_audience "строительные компании и частные клиенты", goal null, missing_fields ["goal"].
- Customer: "https://www.instagram.com/reel/abc" -> intent reference_link, reference_links contains URL, goal null.
- Customer: "Вот референс, нужно больше заявок https://..." -> reference_links contains URL, goal "заявки".
- Customer: "Очень нравится голос от актёра Вин Дизеля" -> intent copyright_question, voice_preference "низкий уверенный кинематографичный голос", copyright_concern explains real actor voice cannot be copied without rights.
- Customer: "Актёров ИИ тоже можно любых ставить или будут потом проблемы с авторским правом?" -> intent copyright_question, reply_text explains real actors/public people require rights and offers original AI character or style.`

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
		fields := []zap.Field{
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("state", conversation.Stage),
			zap.String("fallback_reason", "error"),
			zap.Bool("fallback", true),
		}
		fields = append(fields, openAICustomerUnderstandingLogFields(s.ai, err)...)
		s.warn("openai customer understanding fallback used", fields...)
		return fallback, false
	}

	aiAnalysis, ok := customerUnderstandingToAnalysis(understanding, conversation.Lead, language)
	if !ok {
		fields := []zap.Field{
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("state", conversation.Stage),
			zap.String("fallback_reason", "invalid_response"),
			zap.Bool("fallback", true),
			zap.Float64("confidence", understanding.Confidence),
		}
		fields = append(fields, openAICustomerUnderstandingLogFields(s.ai, nil)...)
		s.warn("openai customer understanding fallback used", fields...)
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
		zap.Int("max_output_tokens", openAIAnalyzerMaxOutputTokens(s.ai)),
		zap.Float64("confidence", understanding.Confidence),
		zap.Bool("handoff_recommended", analysis.ShouldHandoff),
		zap.Bool("stop_recommended", analysis.ShouldStop),
		zap.Strings("missing_fields", analysis.MissingFields),
		zap.Strings("extracted_fields", extractedAnalysisFields(analysis)),
	)
	return analysis, true
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

func customerUnderstandingPayload(msg IncomingMessage, text string, language string, conversation Conversation) string {
	state := json.RawMessage(conversationPromptJSON(conversation))
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
		OfficialPackages    []struct {
			Key      string `json:"key"`
			Name     string `json:"name"`
			Price    string `json:"price"`
			FileName string `json:"file_name"`
		} `json:"official_packages"`
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
		KnownState:        state,
		MissingFields:     requiredLeadMissingFields(conversation),
	}
	payload.CurrentMessage.Role = "customer"
	payload.CurrentMessage.Text = strings.TrimSpace(text)
	payload.CurrentMessage.MessageID = strings.TrimSpace(msg.IDMessage)
	if !msg.Timestamp.IsZero() {
		payload.CurrentMessage.Timestamp = msg.Timestamp.UTC().Format(time.RFC3339)
	}
	payload.QuotedContext = quotedUnderstandingContext(msg)
	payload.RecentMessages = recentUnderstandingMessages(conversation)
	payload.PendingQuestions = pendingUnderstandingQuestions(conversation)
	payload.QuestionAnswerPairs = questionAnswerPairsForUnderstanding(msg, text, conversation)
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

func recentUnderstandingMessages(conversation Conversation) []understandingMessage {
	messages := conversation.Messages
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
		case "user", "customer":
			role = "customer"
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
		Platforms:         []string{},
		Intent:            IntentOther,
		PortfolioTags:     normalizePortfolioTags(understanding.PortfolioTags),
		RecommendedAction: strings.TrimSpace(understanding.RecommendedAction),
	}
	protected := protectedFieldsMap(understanding.DoNotOverwrite)
	lowConfidence := understanding.Confidence > 0 && understanding.Confidence < 0.35
	if !lowConfidence {
		extracted := understanding.ExtractedFields
		legacyExtracted := understanding.Extracted
		if value := normalizedAIString(firstStringPointer(understanding.Niche, extracted.Niche, legacyExtracted.Niche)); !protected[fieldNiche] && isValidNiche(value) && !isNonNicheCandidateText(normalizeForAnalysis(value)) {
			analysis.Niche = stringPointer(normalizeNiche(value))
		}
		if value := normalizeCity(normalizedAIString(firstStringPointer(understanding.City, extracted.City, legacyExtracted.City))); value != "" {
			analysis.City = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(understanding.Goal, extracted.Goal, legacyExtracted.Goal)); !protected[fieldGoal] && value != "" {
			if isValidGoal(value) {
				analysis.Goal = stringPointer(value)
			} else if goal := normalizeGoal(value); goal != "" {
				analysis.Goal = stringPointer(goal)
			}
		}
		if value := normalizedAIString(firstStringPointer(understanding.Deadline, extracted.Deadline, legacyExtracted.Deadline)); !protected[fieldDeadline] && value != "" {
			if isValidDeadline(value) {
				analysis.Deadline = stringPointer(value)
			} else if deadline := normalizeDeadline(value); deadline != "" {
				analysis.Deadline = stringPointer(deadline)
			}
		}
		if value := normalizedAIString(firstStringPointer(understanding.Platform, extracted.Platform, legacyExtracted.Platform)); value != "" {
			analysis.Platforms = mergePlatforms(analysis.Platforms, platformsFromAIString(value))
		}
		if value := normalizedAIString(firstStringPointer(understanding.TargetAudience, extracted.TargetAudience, legacyExtracted.TargetAudience)); !protected[fieldTargetAudience] && value != "" {
			analysis.TargetAudience = stringPointer(value)
		}
		if value := normalizedAIString(firstStringPointer(understanding.ProductOrService, extracted.ProductOrService)); !protected[fieldProductService] && value != "" {
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
		if value := normalizedAIString(firstStringPointer(understanding.Budget, extracted.Budget, legacyExtracted.Budget)); !protected[fieldBudget] && value != "" {
			analysis.Budget = stringPointer(value)
		}
		if value := normalizeVideoQuantity(normalizedAIString(firstStringPointer(understanding.VideoQuantity, understanding.Quantity, extracted.VideoQuantity, extracted.Quantity, legacyExtracted.VideoQuantity))); !protected[fieldVideoQuantity] && value != "" {
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
		if value := normalizePackageInterest(normalizedAIString(firstStringPointer(understanding.PackageInterest, understanding.SelectedPackage, extracted.PackageInterest, extracted.SelectedPackage, legacyExtracted.PackageInterest))); !protected[fieldPackageInterest] && value != "" {
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
	if len(ai.PortfolioTags) > 0 {
		result.PortfolioTags = normalizePortfolioTags(append(result.PortfolioTags, ai.PortfolioTags...))
	}
	if strings.TrimSpace(ai.RecommendedAction) != "" {
		result.RecommendedAction = strings.TrimSpace(ai.RecommendedAction)
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
