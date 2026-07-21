package bot

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yereke99/stone/internal/openai"
	"go.uber.org/zap"
)

// The primary conversational path sends the analyzer's reply_text to the
// customer after deterministic validation. The template state machine stays
// as the operational fallback when the analyzer is unavailable, returns no
// reply, or the conversation is inside a structured flow (brief,
// questionnaire, package selection, handoff, stop).

type outgoingReplySourceKey struct{}

const replySourceLLMPrimary = "llm_primary"

const llmDuplicateQuestionCorrectionPrompt = `Ты — редактор WhatsApp-ответа менеджера Stone Production.
Черновик ответа повторно спрашивает данные, которые клиент уже сообщил. Перепиши ответ:
- сохрани смысл черновика и ответ на вопрос клиента;
- полностью убери вопросы про поля из already_known_fields;
- можно оставить максимум один вопрос и только про поле из still_missing_fields;
- не добавляй новые обещания, цены, скидки, ссылки или файлы;
- пиши на том же языке, что и черновик.
Верни строго JSON: только поле reply_text.`

func replySourceFromContext(ctx context.Context) string {
	source, _ := ctx.Value(outgoingReplySourceKey{}).(string)
	return source
}

// handleLLMPrimaryConversation routes a normal conversational message through
// the analyzer-generated reply. It returns handled=false when the message must
// stay on the deterministic state machine.
func (s *Service) handleLLMPrimaryConversation(ctx context.Context, chatID string, text string, language string, conversation Conversation, analysis CustomerAnalysis) (bool, error) {
	if !s.llmReply.PrimaryEnabled {
		return false, nil
	}
	reply := strings.TrimSpace(analysis.ReplyText)
	if reply == "" {
		return false, nil
	}
	if strings.TrimSpace(analysis.NextAction) == "no_reply" || strings.TrimSpace(analysis.RecommendedAction) == "no_reply" {
		return false, nil
	}
	if llmPrimaryConversationBlocked(conversation, analysis) {
		return false, nil
	}
	stage := replyStageForConversation(conversation)
	if known := knownFieldsAskedByReply(reply, stage, conversation); len(known) > 0 {
		corrected, ok := s.regenerateReplyWithoutKnownFields(ctx, chatID, reply, stage, known, conversation)
		if ok {
			reply = corrected
		}
		// When the retry fails or still repeats a known field, the validation
		// gate in sendAndRemember replaces the reply with a safe non-repeating
		// continuation, so processing never loops.
	}
	wantsExamples := llmWantsPortfolioExamples(conversation, analysis)
	sendPricing := strings.TrimSpace(analysis.RecommendedAction) == "send_price_options" &&
		!conversation.PackagesSent && !conversation.Lead.OfferSent
	sendQuestionnaire := strings.TrimSpace(analysis.RecommendedAction) == "send_questionnaire" &&
		!conversation.QuestionnaireOfferSent && !conversation.QuestionnaireSent && !conversation.Lead.BriefRequested

	s.info("llm primary conversation reply selected",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("intent", analysis.Intent),
		zap.String("stage", stage),
		zap.String("recommended_action", analysis.RecommendedAction),
		zap.String("next_action", analysis.NextAction),
		zap.Bool("wants_portfolio_examples", wantsExamples),
		zap.Bool("send_pricing", sendPricing),
		zap.Bool("send_questionnaire", sendQuestionnaire),
		zap.Float64("confidence", analysis.Confidence),
		zap.Strings("portfolio_tags", normalizePortfolioTags(analysis.PortfolioTags)),
	)

	ctx = context.WithValue(ctx, outgoingReplySourceKey{}, replySourceLLMPrimary)
	if wantsExamples {
		selection := selectAIWorkExamples(conversation.Lead, analysis, aiWorkExamplesLimit())
		if hasUnsentAIWorkVideos(selection, conversation) {
			return true, s.sendRelevantAIWorkExamples(ctx, chatID, language, conversation, analysis, reply)
		}
		s.info("llm primary portfolio selection empty; sending text reply only",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Strings("unmatched_portfolio_tags", normalizePortfolioTags(analysis.PortfolioTags)),
		)
	}
	if err := s.sendAndRemember(ctx, chatID, reply, stage, selectedLevelFromConversation(conversation)); err != nil {
		return true, err
	}
	if sendPricing {
		latest, err := s.store.Snapshot(ctx, chatID)
		if err != nil {
			return true, err
		}
		if err := s.presentPortfolioAndPackages(ctx, chatID, language, latest, analysis); err != nil {
			return true, err
		}
	}
	if sendQuestionnaire {
		latest, err := s.store.Snapshot(ctx, chatID)
		if err != nil {
			return true, err
		}
		if err := s.sendQuestionnaireOffer(ctx, chatID, language, selectedLevelFromConversation(latest)); err != nil {
			return true, err
		}
	}
	return true, nil
}

func llmPrimaryConversationBlocked(conversation Conversation, analysis CustomerAnalysis) bool {
	if conversation.Stage == StageBriefRequested ||
		conversation.Stage == ClientStateAwaitingQuestionnaireConfirm ||
		conversation.QuestionnaireSent ||
		conversation.QuestionnaireOfferSent ||
		conversation.Lead.BriefRequested ||
		conversationIsWaitingForBrief(conversation) {
		if llmCanAnswerInsideStructuredFlow(analysis) {
			return false
		}
		return true
	}
	switch analysis.Intent {
	case IntentPackageSelection,
		IntentReadyToOrder,
		IntentBriefAnswer,
		IntentHumanRequest,
		IntentMute,
		IntentDefer,
		IntentQuantityDiscountQuestion,
		IntentNegativeReaction,
		IntentFrustration:
		return true
	}
	if analysis.WantsQuestionnaire || analysis.ShouldHandoff || analysis.ShouldStop || analysis.SelectedLevel > 0 {
		return true
	}
	if shouldSuppressRapidFollowup(conversation, analysis) {
		return true
	}
	// A pure greeting on first contact keeps the approved greeting flow so the
	// delayed-package follow-up is still scheduled.
	if !conversation.InitialMessageSent && !conversation.Lead.HasBeenGreeted && !analysis.HasBusinessSignal() {
		switch analysis.Intent {
		case IntentGreeting, IntentAgreement, IntentOther:
			return true
		}
	}
	return false
}

func llmCanAnswerInsideStructuredFlow(analysis CustomerAnalysis) bool {
	if strings.TrimSpace(analysis.ReplyText) == "" {
		return false
	}
	if analysis.WantsQuestionnaire || analysis.ShouldHandoff || analysis.ShouldStop || analysis.SelectedLevel > 0 {
		return false
	}
	switch analysis.Intent {
	case IntentFAQ,
		IntentObjection,
		IntentFeasibilityQuestion,
		IntentVoiceQuestion,
		IntentCopyrightQuestion,
		IntentConfusion,
		IntentFormatAdvice,
		IntentPortfolioRequest,
		IntentNicheSpecificCaseRequest:
		return true
	default:
		return false
	}
}

func llmWantsPortfolioExamples(conversation Conversation, analysis CustomerAnalysis) bool {
	if conversation.Stage == StageBriefRequested || conversation.QuestionnaireSent || conversation.Lead.BriefRequested || conversation.QuestionnaireOfferSent {
		return false
	}
	if strings.TrimSpace(analysis.RecommendedAction) == "send_relevant_examples" {
		return true
	}
	switch strings.TrimSpace(analysis.NextAction) {
	case "send_relevant_examples", "send_cases", "send_video":
		return true
	}
	switch analysis.Intent {
	case IntentPortfolioRequest, IntentNicheSpecificCaseRequest:
		return true
	}
	return false
}

func (s *Service) handleLLMDecisionConversation(ctx context.Context, chatID string, language string, conversation Conversation, analysis CustomerAnalysis, openAIAnalyzerUsed bool) (bool, error) {
	if !openAIAnalyzerUsed || !s.llmReply.PrimaryEnabled {
		return false, nil
	}
	if !llmDecisionHasPrimaryAction(analysis) {
		return false, nil
	}
	if strings.TrimSpace(analysis.RecommendedAction) == "stop_bot" || analysis.Intent == IntentMute {
		s.info("llm decision selected stop automation",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("intent", analysis.Intent),
			zap.String("client_intent", analysis.ClientIntent),
		)
		if strings.TrimSpace(analysis.ReplyText) != "" {
			ctx = context.WithValue(ctx, outgoingReplySourceKey{}, replySourceLLMPrimary)
			if err := s.sendAndRemember(ctx, chatID, analysis.ReplyText, ClientStateOptOut, selectedLevelFromConversation(conversation)); err != nil {
				return true, err
			}
		}
		return true, s.stopAutomationSilently(ctx, chatID, selectedLevelFromConversation(conversation), StopReasonCustomerOptOut, true)
	}
	if strings.TrimSpace(analysis.NextAction) == "no_reply" || strings.TrimSpace(analysis.RecommendedAction) == "no_reply" {
		s.info("llm decision selected no reply",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("intent", analysis.Intent),
			zap.String("client_intent", analysis.ClientIntent),
		)
		return true, s.applyLLMDecisionState(ctx, chatID, conversation, analysis, replyStageForConversation(conversation), false)
	}

	reply := strings.TrimSpace(analysis.ReplyText)
	if reply == "" {
		s.warn("llm decision missing customer reply; technical fallback selected",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("intent", analysis.Intent),
			zap.String("recommended_action", analysis.RecommendedAction),
			zap.String("next_action", analysis.NextAction),
		)
		return true, s.sendAndRemember(ctx, chatID, OpenAITemporaryFallbackText(language), replyStageForConversation(conversation), selectedLevelFromConversation(conversation))
	}
	stageForValidation := replyStageForConversation(conversation)
	if known := knownFieldsAskedByReply(reply, stageForValidation, conversation); len(known) > 0 {
		corrected, ok := s.regenerateReplyWithoutKnownFields(ctx, chatID, reply, stageForValidation, known, conversation)
		if ok {
			reply = corrected
		}
	}

	readyForManager := analysis.ReadyForManager || analysis.ShouldHandoff
	stage := llmDecisionStage(conversation, analysis, readyForManager)
	if err := s.applyLLMDecisionState(ctx, chatID, conversation, analysis, stage, readyForManager); err != nil {
		return true, err
	}

	s.info("llm semantic decision executing",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("intent", analysis.Intent),
		zap.String("client_intent", analysis.ClientIntent),
		zap.String("recommended_action", analysis.RecommendedAction),
		zap.String("next_action", analysis.NextAction),
		zap.Bool("should_ask_question", analysis.ShouldAskQuestion),
		zap.String("next_question_field", analysis.NextQuestionField),
		zap.Bool("should_send_portfolio", analysis.ShouldSendPortfolio),
		zap.Bool("ready_for_manager", readyForManager),
		zap.String("questionnaire_status", analysis.QuestionnaireStatus),
		zap.Strings("portfolio_tags", normalizePortfolioTags(analysis.PortfolioTags)),
		zap.Strings("updated_fields", normalizeFieldList(append(extractedAnalysisFields(analysis), analysis.ConfirmedFields...))),
		zap.Strings("corrected_fields", analysis.CorrectedFields),
	)

	ctx = context.WithValue(ctx, outgoingReplySourceKey{}, replySourceLLMPrimary)
	askedFields := llmDecisionAskedFields(analysis)
	if err := s.sendAndRemember(ctx, chatID, reply, stage, selectedLevelFromConversation(conversation), askedFields...); err != nil {
		return true, err
	}
	if readyForManager {
		return true, nil
	}
	if analysis.ShouldSendPortfolio || strings.TrimSpace(analysis.RecommendedAction) == "send_relevant_examples" || strings.TrimSpace(analysis.NextAction) == "send_relevant_examples" || strings.TrimSpace(analysis.NextAction) == "send_cases" {
		latest, err := s.store.Snapshot(ctx, chatID)
		if err != nil {
			return true, err
		}
		if err := s.sendLLMSelectedPortfolio(ctx, chatID, language, latest, analysis); err != nil {
			return true, err
		}
	}
	return true, nil
}

func llmDecisionHasPrimaryAction(analysis CustomerAnalysis) bool {
	if strings.TrimSpace(analysis.ReplyText) != "" ||
		strings.TrimSpace(analysis.RecommendedAction) != "" ||
		strings.TrimSpace(analysis.NextAction) != "" ||
		analysis.ShouldSendPortfolio ||
		analysis.ShouldAskQuestion ||
		analysis.ReadyForManager ||
		analysis.ShouldHandoff ||
		strings.TrimSpace(analysis.QuestionnaireStatus) != "" ||
		strings.TrimSpace(analysis.ClientIntent) != "" ||
		strings.TrimSpace(analysis.ManagerSummary) != "" {
		return true
	}
	return false
}

func llmDecisionStage(conversation Conversation, analysis CustomerAnalysis, readyForManager bool) string {
	if readyForManager {
		return ClientStateHandedOff
	}
	status := strings.TrimSpace(analysis.QuestionnaireStatus)
	switch status {
	case "awaiting_answers", "partially_completed":
		return StageBriefRequested
	case "completed", "transferred_to_manager":
		return StageHandoffRequired
	case "offered", "awaiting_confirmation":
		return ClientStateAwaitingQuestionnaireConfirm
	}
	if analysis.ShouldSendPortfolio || strings.TrimSpace(analysis.RecommendedAction) == "send_relevant_examples" || strings.TrimSpace(analysis.NextAction) == "send_cases" || strings.TrimSpace(analysis.NextAction) == "send_relevant_examples" {
		return StagePortfolioSent
	}
	if analysis.ShouldAskQuestion || strings.TrimSpace(analysis.NextQuestionField) != "" {
		return ClientStateAwaitingQualification
	}
	return replyStageForConversation(conversation)
}

func llmDecisionAskedFields(analysis CustomerAnalysis) []string {
	fields := make([]string, 0, 4)
	if field := normalizeFieldName(analysis.NextQuestionField); field != "" {
		fields = append(fields, field)
	}
	if analysis.ShouldAskQuestion {
		fields = append(fields, normalizeFieldList(analysis.MissingFields)...)
	}
	return normalizeFieldList(fields)
}

func (s *Service) applyLLMDecisionState(ctx context.Context, chatID string, conversation Conversation, analysis CustomerAnalysis, stage string, readyForManager bool) error {
	now := time.Now().UTC()
	level := selectedLevelFromConversation(conversation)
	s.store.Update(chatID, func(current *Conversation) {
		if strings.TrimSpace(analysis.QuestionnaireStatus) != "" {
			current.Lead.QuestionnaireStatus = strings.TrimSpace(analysis.QuestionnaireStatus)
		}
		if len(analysis.UnresolvedQuestions) > 0 {
			current.Lead.UnresolvedQuestions = nil
			for _, question := range analysis.UnresolvedQuestions {
				current.Lead.UnresolvedQuestions = appendUniqueString(current.Lead.UnresolvedQuestions, question)
			}
		}
		if strings.TrimSpace(analysis.ManagerSummary) != "" {
			current.Lead.ManagerSummary = strings.TrimSpace(analysis.ManagerSummary)
			current.ConversationSummary = strings.TrimSpace(analysis.ManagerSummary)
		}
		if strings.TrimSpace(analysis.RecommendedNextStep) != "" {
			current.Lead.RecommendedNextStep = strings.TrimSpace(analysis.RecommendedNextStep)
		}
		if strings.TrimSpace(analysis.ClientIntent) != "" {
			current.Lead.ClientIntent = strings.TrimSpace(analysis.ClientIntent)
		}
		for _, field := range analysis.ConfirmedFields {
			if field = normalizeFieldName(field); field != "" {
				current.CompletedFields[field] = true
			}
		}
		current.MissingFields = normalizeFieldList(analysis.MissingFields)
		switch strings.TrimSpace(analysis.QuestionnaireStatus) {
		case "offered", "awaiting_confirmation":
			current.QuestionnaireOfferSent = true
		case "awaiting_answers", "partially_completed":
			current.QuestionnaireOfferSent = true
			current.QuestionnaireSent = true
			current.WantsQuestionnaire = true
			current.Lead.WantsQuestionnaire = true
			current.Lead.BriefRequested = true
		case "completed":
			current.QuestionnaireOfferSent = true
			current.QuestionnaireSent = true
			current.WantsQuestionnaire = true
			current.Lead.WantsQuestionnaire = true
			current.Lead.BriefRequested = true
			current.Lead.BriefCompleted = true
			current.Lead.ContactBriefReady = true
		}
		if readyForManager {
			current.Lead.ReadyForManagerHandoff = true
			if !isValidPackageInterest(current.Lead.SelectedPackage) {
				current.Lead.SelectedPackage = packageNeedsManagerRecommendation
			}
			if level > 0 {
				current.SelectedLevel = level
			}
			current.Lead.LeadStatus = LeadStatusHandoffRequired
			current.LeadStatus = LeadStatusHandoffRequired
			current.HandedOffToOwner = true
			current.AutomationClosed = true
			current.Stopped = true
			if current.TransferredAt.IsZero() {
				current.TransferredAt = now
			}
			current.NextFollowupAt = time.Time{}
			current.FollowupStage = ""
			current.FollowupReferenceAt = time.Time{}
		}
	})
	return nil
}

func (s *Service) sendLLMSelectedPortfolio(ctx context.Context, chatID string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	selection := selectAIWorkExamplesByTags(analysis.PortfolioTags, aiWorkExamplesLimit())
	if len(selection.Videos) == 0 {
		s.info("llm portfolio decision had no matching local videos",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Strings("portfolio_tags", normalizePortfolioTags(analysis.PortfolioTags)),
		)
		return nil
	}
	files := make([]string, 0, len(selection.Videos))
	captions := make(map[string]string, len(selection.Videos))
	for _, video := range selection.Videos {
		if _, sent := conversation.SentVideoFiles[video.Path]; sent {
			continue
		}
		files = append(files, video.Path)
		captions[video.Path] = aiWorkCaption(video, language, selection.Exact)
	}
	if len(files) == 0 {
		s.info("llm portfolio videos skipped because all selected files were already sent",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Strings("portfolio_tags", selection.Tags),
		)
		return nil
	}
	s.info("llm portfolio videos selected",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.Strings("portfolio_tags", selection.Tags),
		zap.Strings("video_files", files),
		zap.Bool("exact_match", selection.Exact),
	)
	sent, err := s.sendVideosWithCaptions(ctx, chatID, files, language, false, captions)
	if err != nil {
		return err
	}
	if sent == 0 {
		s.warn("llm portfolio decision selected videos but delivery sent zero files",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Strings("video_files", files),
		)
	}
	return nil
}

func hasUnsentAIWorkVideos(selection AIWorkSelection, conversation Conversation) bool {
	for _, video := range selection.Videos {
		if _, sent := conversation.SentVideoFiles[video.Path]; !sent {
			return true
		}
	}
	return false
}

func (s *Service) regenerateReplyWithoutKnownFields(ctx context.Context, chatID string, draft string, stage string, knownFields []string, conversation Conversation) (string, bool) {
	if s.ai == nil {
		return "", false
	}
	payload, err := json.Marshal(struct {
		DraftReply         string            `json:"draft_reply"`
		AlreadyKnownFields map[string]string `json:"already_known_fields"`
		StillMissingFields []string          `json:"still_missing_fields"`
	}{
		DraftReply:         draft,
		AlreadyKnownFields: knownFieldValues(conversation, knownFields),
		StillMissingFields: qualificationMissingFields(conversation.Lead),
	})
	if err != nil {
		return "", false
	}
	timeout := s.llmReply.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	retryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := s.ai.GenerateReplyText(retryCtx, llmDuplicateQuestionCorrectionPrompt, []openai.Message{
		{Role: "user", Content: string(payload)},
	})
	if err != nil {
		s.warn("llm duplicate-question correction failed; validation gate will replace the reply",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Strings("known_fields_asked", knownFields),
			zap.String("openai_error", openai.SafeErrorMessage(err)),
		)
		return "", false
	}
	corrected := strings.TrimSpace(response.ReplyText)
	if corrected == "" {
		return "", false
	}
	if len(knownFieldsAskedByReply(corrected, stage, conversation)) > 0 {
		s.warn("llm duplicate-question correction still repeats a known field; validation gate will replace the reply",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Strings("known_fields_asked", knownFields),
		)
		return "", false
	}
	s.info("llm duplicate-question correction applied",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.Strings("known_fields_asked", knownFields),
	)
	return corrected, true
}

func knownFieldValues(conversation Conversation, fields []string) map[string]string {
	lead := conversation.Lead
	values := make(map[string]string, len(fields))
	for _, field := range normalizeFieldList(fields) {
		switch field {
		case fieldNiche:
			values[field] = strings.TrimSpace(lead.Niche)
		case fieldGoal:
			values[field] = strings.TrimSpace(lead.Goal)
		case fieldProductService:
			values[field] = strings.TrimSpace(lead.ProductOrService)
		case fieldCompanyName:
			values[field] = strings.TrimSpace(firstNonEmpty(lead.CompanyName, lead.BrandName, lead.ClientName))
		case fieldContactName:
			values[field] = strings.TrimSpace(firstNonEmpty(lead.ContactName, conversation.DisplayName))
		case fieldBusinessDescription:
			values[field] = strings.TrimSpace(firstNonEmpty(lead.BusinessDescription, lead.StrongSide))
		case fieldTargetAudience:
			values[field] = strings.TrimSpace(lead.TargetAudience)
		case fieldVideoType:
			values[field] = strings.TrimSpace(lead.DesiredVideoType)
		case fieldVideoFormat:
			values[field] = strings.TrimSpace(lead.DesiredVideoFormat)
		case fieldDistributionPlatform:
			values[field] = strings.TrimSpace(firstNonEmpty(lead.DistributionPlatform, lead.Platform))
		case fieldDeadline:
			values[field] = strings.TrimSpace(lead.Deadline)
		case fieldVideoQuantity:
			values[field] = strings.TrimSpace(lead.VideoQuantity)
		case fieldPackageInterest:
			values[field] = strings.TrimSpace(lead.SelectedPackage)
		case fieldReferenceLinks:
			values[field] = strings.TrimSpace(strings.Join(lead.ReferenceLinks, ", "))
			if values[field] == "" {
				values[field] = strings.TrimSpace(lead.WebsiteOrInstagram)
			}
		case fieldVoicePreference:
			values[field] = strings.TrimSpace(lead.VoicePreference)
		default:
			values[field] = "known"
		}
	}
	return values
}
