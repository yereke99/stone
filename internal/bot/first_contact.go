package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

func firstContactWelcomePackageRequired(conversation Conversation) bool {
	if welcomePackageAlreadyCompleted(conversation) {
		return false
	}
	if isConversationClosedForAutomation(conversation) || shouldSilenceForStoredHistory(conversation) {
		return false
	}
	if conversation.DoNotAutoStart || conversation.LegacyExisting || conversation.LegacyProcessed {
		return false
	}
	if conversation.QuestionnaireOfferSent || conversation.QuestionnaireSent || conversation.Lead.BriefRequested {
		return false
	}
	if conversation.HandedOffToOwner || conversation.AutomationClosed || conversation.Stopped || conversation.OptOut {
		return false
	}

	switch strings.TrimSpace(conversation.Stage) {
	case "", ClientStateNeutralNew, ClientStateAwaitingQualification, StageQualification, StageDiagnosis, StageNewLead:
		return true
	default:
		return false
	}
}

func welcomePackageAlreadyCompleted(conversation Conversation) bool {
	if !conversation.AutoPackagesSentAt.IsZero() && conversation.PackagesSent && (conversation.SentPortfolio || conversation.Lead.PortfolioSent) {
		return true
	}
	if !(conversation.PackagesSent || conversation.Lead.OfferSent) {
		return false
	}
	if !(conversation.SentPortfolio || conversation.Lead.PortfolioSent) {
		return false
	}
	return len(missingPortfolioExampleVideos(conversation)) == 0
}

func isFirstContactMeaningfulText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return true
	}
	if meaningfulRuneCount(normalized) < 2 {
		return false
	}
	if isExplicitOptOutText(text) || isMuteRequest(normalized) || isClientDeferText(text) || containsHumanRequest(normalized) {
		return false
	}
	return !isLocalOfftopic(normalized)
}

func shouldBypassOpenAIForFirstContactPackage(analysis CustomerAnalysis) bool {
	if analysis.Intent == IntentFAQ || analysis.Intent == IntentPriceQuestion {
		return true
	}
	return !analysis.HasBusinessSignal()
}

func (s *Service) handleRequiredFirstContactWelcomePackageBeforeOpenAI(ctx context.Context, chatID string, text string, language string, conversation Conversation) (bool, CustomerAnalysis, error) {
	analysis := AnalyzeCustomerMessage(text, conversation.Lead, language)
	if !firstContactWelcomePackageRequired(conversation) || !isFirstContactMeaningfulText(text) {
		return false, analysis, nil
	}
	if !shouldBypassOpenAIForFirstContactPackage(analysis) {
		return false, analysis, nil
	}
	lead := conversation.Lead
	lead.ApplyAnalysis(analysis)
	if err := s.store.UpdateLead(ctx, chatID, lead); err != nil {
		return true, analysis, err
	}
	latest, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return true, analysis, err
	}
	return true, analysis, s.sendFirstContactWelcomePackage(ctx, chatID, language, latest, analysis)
}

func (s *Service) sendFirstContactWelcomePackage(ctx context.Context, chatID string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	s.info("deterministic first-contact welcome package selected",
		zap.String("chat_hash", chatFingerprint(chatID)),
		zap.String("state", conversation.Stage),
		zap.String("intent", analysis.Intent),
		zap.Bool("welcome_package_completed", welcomePackageAlreadyCompleted(conversation)),
	)

	if err := s.sendAndRemember(ctx, chatID, firstContactWelcomeReplyText(language, conversation, analysis), ClientStateAwaitingQualification, selectedLevelFromConversation(conversation), fieldPackageInterest); err != nil {
		return err
	}
	files, captions, relevant := s.firstContactPortfolioVideos(conversation, analysis, language)
	sent, err := s.sendVideosWithCaptions(ctx, chatID, files, language, false, captions)
	if err != nil {
		return err
	}
	latest, err := s.store.Snapshot(ctx, chatID)
	if err != nil {
		return err
	}
	if relevant {
		if sent < len(files) {
			s.warn("first-contact relevant welcome package incomplete; not marking completed",
				zap.String("chat_hash", chatFingerprint(chatID)),
				zap.Int("sent_media_count", sent),
				zap.Strings("video_files", files),
			)
			return fmt.Errorf("first-contact relevant welcome package incomplete: sent %d of %d", sent, len(files))
		}
	} else if missing := missingPortfolioExampleVideos(latest); len(missing) > 0 {
		s.warn("first-contact welcome package incomplete; not marking completed",
			zap.String("chat_hash", chatFingerprint(chatID)),
			zap.Int("sent_media_count", sent),
			zap.Strings("missing_files", missing),
		)
		return fmt.Errorf("first-contact welcome package incomplete: missing %s", strings.Join(missing, ", "))
	}
	now := time.Now().UTC()
	if err := s.store.MarkAutoPackagesSent(context.WithoutCancel(ctx), chatID, now); err != nil {
		return err
	}
	return s.cancelFollowups(ctx, chatID)
}

func (s *Service) firstContactPortfolioVideos(conversation Conversation, analysis CustomerAnalysis, language string) ([]string, map[string]string, bool) {
	selection := selectAIWorkExamples(conversation.Lead, analysis, firstContactAIWorkExamplesLimit())
	if len(selection.Videos) > 0 && len(normalizePortfolioTags(selection.Tags)) > 0 {
		files := make([]string, 0, len(selection.Videos))
		captions := make(map[string]string, len(selection.Videos))
		for _, video := range selection.Videos {
			if _, _, err := s.resolveVideoFilePath(video.Path); err != nil {
				continue
			}
			files = append(files, video.Path)
			captions[video.Path] = aiWorkCaption(video, language, selection.Exact)
		}
		if len(files) >= 3 {
			return files[:3], captions, true
		}
	}
	return []string{VideoLevel1, VideoLevel2, VideoLevel3}, nil, false
}

func firstContactAIWorkExamplesLimit() int {
	limit := aiWorkExamplesLimit()
	if limit > 0 && limit < 3 {
		return 3
	}
	return limit
}

func firstContactWelcomeReplyText(language string, conversation Conversation, analysis CustomerAnalysis) string {
	base := FirstContactWelcomeText(language)
	prefix := strings.TrimSpace(firstContactDirectAnswerText(language, conversation, analysis))
	if prefix == "" {
		return base
	}
	return prefix + "\n\n" + base
}

func isFirstContactWelcomePackageReply(message string) bool {
	normalized := normalizeForAnalysis(message)
	if normalized == "" {
		return false
	}
	compact := strings.NewReplacer(" ", "", ",", "", "\u00a0", "").Replace(normalized)
	return strings.Contains(normalized, "stone production") &&
		strings.Contains(compact, "35000") &&
		strings.Contains(compact, "50000") &&
		strings.Contains(compact, "75000") &&
		containsAny(normalized, []string{"пакет", "package"})
}

func firstContactDirectAnswerText(language string, conversation Conversation, analysis CustomerAnalysis) string {
	switch analysis.Intent {
	case IntentFAQ:
		return firstContactFAQAnswerText(language, analysis.FAQKey)
	case IntentFormatAdvice:
		return firstContactFormatAdviceText(language)
	case IntentQuantityDiscountQuestion:
		return firstContactQuantityAnswerText(language)
	case IntentFeasibilityQuestion:
		return firstContactFeasibilityAnswerText(language)
	case IntentConfusion:
		return firstContactConfusionAnswerText(language)
	case IntentVoiceQuestion:
		return firstContactVoiceAnswerText(language)
	case IntentCopyrightQuestion:
		return firstContactCopyrightAnswerText(language)
	case IntentPortfolioRequest:
		if isValidNiche(conversation.Lead.Niche) || strings.TrimSpace(conversation.Lead.ProductOrService) != "" {
			return firstContactRelevantExamplesAnswerText(language, conversation.Lead)
		}
		return firstContactPortfolioRequestAnswerText(language, conversation)
	case IntentNicheSpecificCaseRequest:
		return firstContactRelevantExamplesAnswerText(language, conversation.Lead)
	default:
		return ""
	}
}

func firstContactFAQAnswerText(language string, key string) string {
	switch key {
	case faqAIProduction:
		switch normalizeLanguageCode(language) {
		case "kk":
			return "Бұл AI-production: роликтерді толық түсірілім тобысыз нейрожелілер арқылы жасаймыз, бірақ реалистік визуал мен жарнамалық подачаға мән береміз."
		case "en":
			return "This is AI production: we create videos with neural tools without a full filming crew, while keeping a realistic visual and ad-focused delivery."
		default:
			return FAQAnswerText(key, language)
		}
	case faqDuration:
		switch normalizeLanguageCode(language) {
		case "kk":
			return "Жарнамаға әдетте 30-45 секундтық ролик жасаймыз."
		case "en":
			return "For ads, we usually make 30-45 second videos."
		default:
			return FAQAnswerText(key, language)
		}
	case faqTimeline:
		switch normalizeLanguageCode(language) {
		case "kk":
			return "Орташа дайындалу мерзімі - сценарий мен материалдар бекітілгеннен кейін 2-3 жұмыс күні."
		case "en":
			return "Average production time is 2-3 business days after the script and materials are approved."
		default:
			return FAQAnswerText(key, language)
		}
	case faqPayment:
		switch normalizeLanguageCode(language) {
		case "kk":
			return "100 000 тг дейін - 100% алдын ала төлем, одан жоғары жобаларда шартты менеджер нақтылайды."
		case "en":
			return "Up to 100,000 KZT we work with 100% prepayment; larger projects are confirmed by a manager."
		default:
			return FAQAnswerText(key, language)
		}
	default:
		if approved := strings.TrimSpace(FAQAnswerText(key, language)); approved != "" {
			return approved
		}
		switch normalizeLanguageCode(language) {
		case "kk":
			return "Иә, қысқаша: роликті онлайн дайындаймыз, сценарий мен визуал бағытын бекітіп, жарнамаға дайын монтаж береміз."
		case "en":
			return "Yes. In short: we prepare the video online, approve the script and visual direction, then deliver ad-ready editing."
		default:
			return "Да, коротко: ролик готовим онлайн, согласуем сценарий и визуальное направление, затем отдаём монтаж под рекламу."
		}
	}
}

func firstContactFormatAdviceText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Жарнамада көбіне қысқа problem-solution, UGC немесе нәтиже көрсететін формат жақсы өтеді."
	case "en":
		return "For ads, short problem-solution, UGC, or result-demo formats usually work best."
	default:
		return "Для рекламы чаще всего лучше заходят короткие problem-solution, UGC или демонстрация результата."
	}
}

func firstContactQuantityAnswerText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Бірнеше ролик бойынша шарт жеке есептеледі; төмендегі бағалар бір роликке арналған базалық пакеттер."
	case "en":
		return "For multiple videos, terms are calculated individually; the prices below are base packages for one video."
	default:
		return "По серии роликов пакетные условия индивидуально; цены ниже - базовые пакеты за один ролик."
	}
}

func firstContactFeasibilityAnswerText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Иә, референске ұқсас көңіл-күй мен құрылымды жасауға болады, бірақ нақты адамды немесе брендті құқықсыз көшірмейміз."
	case "en":
		return "Yes, we can match the mood and structure of a reference, but we do not copy a real person or brand without rights."
	default:
		return "Да, можем адаптировать настроение и структуру по референсу, но не копируем реального человека или бренд без прав."
	}
}

func firstContactConfusionAnswerText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Қарапайым айтқанда, біз бизнесіңізге түсірілімсіз AI-жарнамалық ролик жасап, оны жарнамаға дайындаймыз."
	case "en":
		return "Simply put, we create an AI ad video for your business without filming and prepare it for ads."
	default:
		return "объясню проще: мы делаем AI-рекламный ролик для вашего бизнеса без съёмки и готовим его под запуск рекламы."
	}
}

func firstContactVoiceAnswerText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Озвучканы стиль бойынша таңдай аламыз; нақты адамның дауысын құқықсыз көшірмейміз."
	case "en":
		return "We can choose a voice style, but we do not copy a specific person's voice without rights."
	default:
		return "Да, голос можно выбрать по стилю, но голос конкретного человека без прав не копируем."
	}
}

func firstContactCopyrightAnswerText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Құқық жағынан қауіпсіз жұмыс істейміз: нақты адамдарды, тұлғаларды немесе дауысты рұқсатсыз көшірмейміз."
	case "en":
		return "We work safely with rights: no copying real people, public figures, faces, or voices without permission."
	default:
		return "без прав использовать нельзя: реальных людей, публичные лица, лица и голоса без разрешения не копируем."
	}
}

func firstContactPortfolioRequestAnswerText(language string, conversation Conversation) string {
	delivery := isCasesDeliveryQuestion(conversation.LastIncomingText)
	switch normalizeLanguageCode(language) {
	case "kk":
		if delivery {
			return "Кейстерді осы WhatsApp-қа видео мысалдар ретінде жіберемін."
		}
		return "Иә, кейстерді осы жерге жібере аламын."
	case "en":
		if delivery {
			return "I will send the cases right here in WhatsApp as video examples."
		}
		return "Yes, I can send cases right here."
	default:
		if delivery {
			return "Отправим прямо сюда в WhatsApp видео-примеры и форматы."
		}
		return "Да, кейсы можем отправить прямо сюда."
	}
}

func firstContactRelevantExamplesAnswerText(language string, lead LeadState) string {
	niche := strings.TrimSpace(firstNonEmpty(lead.ProductOrService, lead.Niche))
	if niche == "" {
		return ""
	}
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім, " + niche + " бағытына жақын мысалдарды жіберемін."
	case "en":
		return "Got it, I will send examples close to " + niche + "."
	default:
		return "Понял, отправлю близкие примеры под задачу: " + niche + "."
	}
}
