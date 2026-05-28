package bot

import (
	"context"
	"strings"
)

func (s *Service) handleFAQ(ctx context.Context, chatID string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	answer := FAQAnswerText(analysis.FAQKey, language)
	if strings.TrimSpace(answer) == "" {
		return nil
	}
	if len(qualificationMissingFields(conversation.Lead)) == 0 &&
		!(conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent) &&
		!conversation.QuestionnaireOfferSent &&
		conversation.Stage != StageBriefRequested &&
		!conversation.QuestionnaireSent &&
		!conversation.Lead.BriefRequested {
		if err := s.sendAndRemember(ctx, chatID, answer, ClientStateAwaitingQualification, selectedLevelFromConversation(conversation)); err != nil {
			return err
		}
		return s.presentPortfolioAndPackages(ctx, chatID, language, conversation, analysis)
	}

	continuation, stage, level, askedFields := faqContinuation(language, conversation)
	message := answer
	if strings.TrimSpace(continuation) != "" {
		message += "\n\n" + continuation
	}
	if err := s.sendAndRemember(ctx, chatID, message, stage, level, askedFields...); err != nil {
		return err
	}
	switch stage {
	case ClientStatePackagesPresented:
		return s.scheduleFollowup(ctx, chatID, followupStageQuestionnaireOffer, packageSelectionFollowupAfter, conversation.LastReplyAt)
	case ClientStateAwaitingQuestionnaireConfirm:
		return s.scheduleFollowup(ctx, chatID, followupStageQuestionnaireReminder, questionnaireReminderAfter, conversation.LastReplyAt)
	default:
		return nil
	}
}

func faqContinuation(language string, conversation Conversation) (string, string, int, []string) {
	level := selectedLevelFromConversation(conversation)
	if conversation.Stage == StageBriefRequested || conversation.QuestionnaireSent || conversation.Lead.BriefRequested {
		return BriefContextReturnText(language), StageBriefRequested, level, []string{fieldBrief}
	}
	if conversation.QuestionnaireOfferSent || conversation.Stage == ClientStateAwaitingQuestionnaireConfirm {
		return questionnaireConfirmationFallbackText(language), ClientStateAwaitingQuestionnaireConfirm, level, nil
	}
	if conversation.PackagesSent || conversation.Lead.OfferSent || conversation.SentPortfolio || conversation.Lead.PortfolioSent {
		return FormatQuestionText(language), ClientStatePackagesPresented, level, []string{fieldPackageInterest}
	}
	missing := qualificationMissingFields(conversation.Lead)
	if len(missing) == 0 {
		return FormatQuestionText(language), ClientStatePackagesPresented, level, []string{fieldPackageInterest}
	}
	if sameFields(missing, []string{fieldNiche, fieldGoal, fieldDeadline}) {
		return QualificationQuestionsText(language), ClientStateAwaitingQualification, 0, missing
	}
	return qualificationFollowupText(language, conversation), ClientStateAwaitingQualification, 0, missing
}

func (s *Service) handleBriefRequested(ctx context.Context, chatID string, text string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	if analysis.Intent == IntentFAQ && strings.TrimSpace(analysis.FAQKey) != "" {
		answer := FAQAnswerText(analysis.FAQKey, language)
		if answer == "" {
			return nil
		}
		return s.sendAndRemember(ctx, chatID, answer+"\n\n"+BriefContextReturnText(language), StageBriefRequested, selectedLevelFromConversation(conversation), fieldBrief)
	}
	if analysis.Intent == IntentHumanRequest {
		return s.completeBriefAndHandoff(ctx, chatID, language, selectedLevelFromConversation(conversation))
	}
	if isSoftNo(text) || analysis.Intent == IntentRefusal {
		return s.stopClient(ctx, chatID, false)
	}

	s.recordBriefMessage(chatID, text, analysis)
	conversation, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	status := briefCompletionStatus(conversation)
	if status.complete {
		return s.completeBriefAndHandoff(ctx, chatID, language, selectedLevelFromConversation(conversation))
	}
	if status.nextQuestion == "" {
		return s.sendAndRemember(ctx, chatID, BriefContextReturnText(language), StageBriefRequested, selectedLevelFromConversation(conversation), fieldBrief)
	}
	return s.sendAndRemember(ctx, chatID, status.nextQuestion, StageBriefRequested, selectedLevelFromConversation(conversation), fieldBrief)
}

func (s *Service) recordBriefMessage(chatID string, text string, analysis CustomerAnalysis) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.store.Update(chatID, func(conversation *Conversation) {
		if analysis.Niche != nil && isValidNiche(*analysis.Niche) && !isValidNiche(conversation.Lead.Niche) {
			conversation.Lead.Niche = strings.TrimSpace(*analysis.Niche)
		}
		if analysis.Goal != nil && isValidGoal(*analysis.Goal) && !isValidGoal(conversation.Lead.Goal) {
			conversation.Lead.Goal = strings.TrimSpace(*analysis.Goal)
		}
		if analysis.Deadline != nil && isValidDeadline(*analysis.Deadline) && !isValidDeadline(conversation.Lead.Deadline) {
			conversation.Lead.Deadline = strings.TrimSpace(*analysis.Deadline)
		}
		if len(analysis.Platforms) > 0 {
			conversation.Lead.Platforms = mergePlatforms(conversation.Lead.Platforms, analysis.Platforms)
			conversation.Lead.Platform = strings.Join(conversation.Lead.Platforms, ", ")
		}
		conversation.Lead.FreeText = appendBriefText(conversation.Lead.FreeText, text)
		conversation.Lead.Notes = appendBriefText(conversation.Lead.Notes, text)
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.QuestionnaireSent = true
		conversation.Lead.BriefRequested = true
	})
}

type briefStatus struct {
	complete     bool
	nextQuestion string
}

func briefCompletionStatus(conversation Conversation) briefStatus {
	text := normalizeForAnalysis(strings.Join([]string{
		conversation.Lead.FreeText,
		conversation.Lead.Notes,
		conversation.LastIncomingText,
	}, " "))
	if text == "" {
		return briefStatus{nextQuestion: briefMissingQuestion("product")}
	}
	if hasNumberedBriefStructure(text) {
		return briefStatus{complete: true}
	}
	missing := make([]string, 0, 4)
	if !briefHasProduct(text, conversation) {
		missing = append(missing, "product")
	}
	if !briefHasStrength(text) {
		missing = append(missing, "strength")
	}
	if !briefHasClient(text) {
		missing = append(missing, "client")
	}
	if !briefHasOffer(text) {
		missing = append(missing, "offer")
	}
	if len(missing) == 0 {
		return briefStatus{complete: true}
	}
	return briefStatus{nextQuestion: briefMissingQuestion(missing[0])}
}

func briefHasProduct(text string, conversation Conversation) bool {
	if isValidNiche(conversation.Lead.Niche) {
		return true
	}
	return containsAny(text, []string{"прода", "рекламируем", "продвигаем", "товар", "услуг", "продукт", "мебель", "одежд", "курс", "салон", "клиник", "стоматолог"})
}

func briefHasStrength(text string) bool {
	return containsAny(text, []string{"сильн", "преимущ", "ценность", "отлич", "качество", "быстро", "на заказ", "опыт", "гарант", "premium", "премиум"})
}

func briefHasClient(text string) bool {
	return containsAny(text, []string{"клиент", "аудитор", "покупател", "семьи", "женщ", "мужчин", "бизнес", "новые квартиры", "для тех", "ца "})
}

func briefHasOffer(text string) bool {
	if strings.Contains(text, "http") || strings.Contains(text, "www") || strings.Contains(text, "instagram") || strings.Contains(text, "@") {
		return true
	}
	return containsAny(text, []string{"акци", "оффер", "скид", "бонус", "подар", "рассроч", "нет акции", "нет оффера", "сайт"})
}

func briefMissingQuestion(field string) string {
	switch field {
	case "product":
		return "Что продаёте?"
	case "strength":
		return "В чём ваша сильная сторона?"
	case "client":
		return "Кто ваш клиент?"
	case "offer":
		return "Есть ли сейчас акция / оффер?"
	default:
		return BriefContextReturnText("ru")
	}
}

func appendBriefText(existing string, incoming string) string {
	existing = strings.TrimSpace(existing)
	incoming = strings.TrimSpace(incoming)
	if incoming == "" {
		return existing
	}
	if existing == "" {
		return incoming
	}
	if strings.Contains(existing, incoming) {
		return existing
	}
	return existing + "\n" + incoming
}

func isReplyAfterWeeklyFollowup(conversation Conversation) bool {
	return strings.TrimSpace(conversation.FollowupStage) == followupStageWeeklyDiscountSent &&
		!conversation.LastFollowupSentAt.IsZero() &&
		(conversation.LastIncomingAt.IsZero() || conversation.LastIncomingAt.After(conversation.LastFollowupSentAt))
}

func (s *Service) completeFollowupReplyHandoff(ctx context.Context, chatID string, language string, level int) error {
	s.store.Update(chatID, func(conversation *Conversation) {
		if level > 0 {
			conversation.SelectedLevel = level
			conversation.Lead.SelectedPackage = packageKey(level)
		}
		if !isValidPackageInterest(conversation.Lead.SelectedPackage) {
			conversation.Lead.SelectedPackage = packageNeedsManagerRecommendation
		}
		conversation.WantsQuestionnaire = true
		conversation.Lead.WantsQuestionnaire = true
		conversation.Lead.BriefCompleted = true
		conversation.Lead.ContactBriefReady = true
		conversation.Lead.FreeText = appendBriefText(conversation.Lead.FreeText, conversation.LastIncomingText)
	})
	if err := s.sendAndRemember(ctx, chatID, HumanHandoffText(language), ClientStateHandedOff, level); err != nil {
		return err
	}
	return s.cancelFollowups(ctx, chatID)
}
