package bot

import (
	"context"
	"strings"

	"go.uber.org/zap"
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
	if sameFields(missing, []string{fieldNiche, fieldGoal}) {
		return QualificationQuestionsText(language), ClientStateAwaitingQualification, 0, missing
	}
	reply := qualificationFollowupText(language, conversation)
	return reply, ClientStateAwaitingQualification, 0, qualificationFollowupAskedFields(reply, missing)
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
	if isExplicitOptOutText(text) || analysis.Intent == IntentMute {
		return s.stopClient(ctx, chatID, false)
	}
	if isGenericAcknowledgement(text) {
		return nil
	}
	if analysis.Intent == IntentFormatAdvice {
		return s.handleFormatAdvice(ctx, chatID, language, conversation)
	}
	if analysis.Intent == IntentBusinessLink {
		s.recordBriefMessage(chatID, text, analysis)
		return s.continueBriefAfterSavedMessage(ctx, chatID, language)
	}

	s.recordBriefMessage(chatID, text, analysis)
	return s.continueBriefAfterSavedMessage(ctx, chatID, language)
}

func (s *Service) continueBriefAfterSavedMessage(ctx context.Context, chatID string, language string) error {
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
	message := status.nextQuestion
	if leadHasBusinessLink(conversation.Lead) && !strings.Contains(normalizeForAnalysis(message), "ссыл") {
		message = briefLinkAcknowledgedNextQuestion(language, message)
	}
	return s.sendAndRemember(ctx, chatID, message, StageBriefRequested, selectedLevelFromConversation(conversation), fieldBrief)
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
		if analysis.ProductOrService != nil && strings.TrimSpace(*analysis.ProductOrService) != "" {
			conversation.Lead.ProductOrService = strings.TrimSpace(*analysis.ProductOrService)
		}
		if analysis.TargetAudience != nil && strings.TrimSpace(*analysis.TargetAudience) != "" {
			conversation.Lead.TargetAudience = strings.TrimSpace(*analysis.TargetAudience)
		}
		if analysis.StrongSide != nil && strings.TrimSpace(*analysis.StrongSide) != "" {
			conversation.Lead.StrongSide = strings.TrimSpace(*analysis.StrongSide)
		}
		if analysis.Offer != nil && strings.TrimSpace(*analysis.Offer) != "" {
			conversation.Lead.Offer = strings.TrimSpace(*analysis.Offer)
		}
		if analysis.BusinessLink != nil && strings.TrimSpace(*analysis.BusinessLink) != "" {
			conversation.Lead.WebsiteOrInstagram = strings.TrimSpace(*analysis.BusinessLink)
		}
		for _, link := range analysis.ReferenceLinks {
			conversation.Lead.ReferenceLinks = appendUniqueString(conversation.Lead.ReferenceLinks, link)
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
	if !briefHasStrength(text, conversation) {
		missing = append(missing, "strength")
	}
	if !briefHasClient(text, conversation) {
		missing = append(missing, "client")
	}
	if !briefHasOffer(text, conversation) {
		missing = append(missing, "offer")
	}
	if len(missing) == 0 {
		if !isValidGoal(conversation.Lead.Goal) {
			return briefStatus{nextQuestion: briefMissingQuestion(fieldGoal)}
		}
		return briefStatus{complete: true}
	}
	return briefStatus{nextQuestion: briefMissingQuestion(missing[0])}
}

func briefHasProduct(text string, conversation Conversation) bool {
	if isValidNiche(conversation.Lead.Niche) {
		return true
	}
	if strings.TrimSpace(conversation.Lead.ProductOrService) != "" {
		return true
	}
	if knownNicheFromText(text) != "" {
		return true
	}
	if shortServiceLine(text) {
		return true
	}
	return containsAny(text, []string{
		"прода", "рекламируем", "продвигаем", "товар", "услуг", "продукт", "мебель", "одежд",
		"обув", "курс", "салон", "магазин", "клиник", "стоматолог", "окрашиван", "делаем",
	})
}

func briefHasStrength(text string, conversation ...Conversation) bool {
	if len(conversation) > 0 && strings.TrimSpace(conversation[0].Lead.StrongSide) != "" {
		return true
	}
	return containsAny(text, []string{
		"сильн", "преимущ", "ценность", "отлич", "качество", "быстро", "на заказ", "опыт",
		"гарант", "premium", "преми", "эксклюзив", "уникаль", "высокий чек",
	})
}

func briefHasClient(text string, conversation ...Conversation) bool {
	if len(conversation) > 0 && strings.TrimSpace(conversation[0].Lead.TargetAudience) != "" {
		return true
	}
	return containsAny(text, []string{
		"клиент", "аудитор", "покупател", "семьи", "семь", "женщ", "девуш", "мужчин", "муж",
		"предпринимател", "бизнес", "новые квартиры", "для тех", "ца ", "чек", "20-35",
		"компани", "b2b", "б2б", "производств", "промышлен", "добыч", "завод", "корпоратив",
	}) || audienceLikeLine(text)
}

func briefHasOffer(text string, conversation ...Conversation) bool {
	if len(conversation) > 0 && strings.TrimSpace(conversation[0].Lead.Offer) != "" {
		return true
	}
	if strings.Contains(text, "http") || strings.Contains(text, "www") || strings.Contains(text, "instagram") || strings.Contains(text, "@") {
		return true
	}
	return containsAny(text, []string{
		"акци", "оффер", "офер", "скид", "бонус", "подар", "рассроч", "сайт",
		"нету", "нет акции", "акции нет", "нет оффера", "оффера нет", "пока нет", "не знаю",
		"не придумали", "жок", "no",
	})
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
	case fieldGoal:
		return "Какая цель ролика: заявки, продажи или узнаваемость?"
	default:
		return BriefContextReturnText("ru")
	}
}

func briefLinkAcknowledgedNextQuestion(language string, question string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return LinkReceivedBriefText(language)
	}
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сілтемені алдым, рақмет. " + question
	case "en":
		return "Got the link, thank you. " + question
	default:
		return "Ссылку получил, спасибо. " + question
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
		conversation.Lead.ReadyForManagerHandoff = true
		conversation.Lead.LeadStatus = LeadStatusHandoffRequired
		conversation.LeadStatus = LeadStatusHandoffRequired
		conversation.Lead.FreeText = appendBriefText(conversation.Lead.FreeText, conversation.LastIncomingText)
	})
	analysis := CustomerAnalysis{
		Intent:            IntentReadyToOrder,
		ReadyForManager:   true,
		ShouldHandoff:     true,
		ClientIntent:      "ответил после follow-up, нужен менеджер",
		RecommendedAction: "handoff",
		NextAction:        "handoff",
	}
	result, escalationErr := s.executeManagerEscalation(ctx, chatID, analysis, "Клиент ответил после follow-up, нужен менеджер")
	if escalationErr != nil || !result.Sent {
		s.warn("follow-up manager escalation failed; customer-safe fallback selected",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.String("escalation_reason", result.Reason),
			zap.Bool("manager_notification_sent", result.Sent),
			zap.Error(escalationErr),
		)
		s.markManagerEscalationFailed(context.WithoutCancel(ctx), chatID)
		return s.sendAndRemember(ctx, chatID, ManagerEscalationFallbackText(language), ClientStatePackagesPresented, level)
	}
	if err := s.sendAndRemember(ctx, chatID, HumanHandoffText(language), ClientStateHandedOff, level); err != nil {
		return err
	}
	return s.cancelFollowups(ctx, chatID)
}
