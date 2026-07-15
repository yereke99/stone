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

	s.info("llm primary conversation reply selected",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("intent", analysis.Intent),
		zap.String("stage", stage),
		zap.String("recommended_action", analysis.RecommendedAction),
		zap.String("next_action", analysis.NextAction),
		zap.Bool("wants_portfolio_examples", wantsExamples),
		zap.Bool("send_pricing", sendPricing),
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
	return true, nil
}

func llmPrimaryConversationBlocked(conversation Conversation, analysis CustomerAnalysis) bool {
	if conversation.Stage == StageBriefRequested ||
		conversation.Stage == ClientStateAwaitingQuestionnaireConfirm ||
		conversation.QuestionnaireSent ||
		conversation.QuestionnaireOfferSent ||
		conversation.Lead.BriefRequested ||
		conversationIsWaitingForBrief(conversation) {
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
		case fieldTargetAudience:
			values[field] = strings.TrimSpace(lead.TargetAudience)
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
