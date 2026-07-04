package bot

import (
	"context"
	"fmt"
	"strings"
)

func (s *Service) handleConfusionReply(ctx context.Context, chatID string, language string, conversation Conversation) error {
	base := confusionExplanationText(language)
	if followup := nextUsefulQuestionText(language, conversation); followup != "" {
		base += " " + followup
	}
	return s.sendAndRemember(ctx, chatID, base, replyStageForConversation(conversation), selectedLevelFromConversation(conversation), qualificationMissingFields(conversation.Lead)...)
}

func (s *Service) handleFeasibilityQuestion(ctx context.Context, chatID string, language string, conversation Conversation) error {
	message := feasibilityAnswerText(language)
	if followup := nextUsefulQuestionText(language, conversation); followup != "" {
		message += " " + followup
	}
	return s.sendAndRemember(ctx, chatID, message, replyStageForConversation(conversation), selectedLevelFromConversation(conversation), qualificationMissingFields(conversation.Lead)...)
}

func (s *Service) handleVoiceQuestion(ctx context.Context, chatID string, text string, language string, conversation Conversation) error {
	message := voiceQuestionAnswerText(language, text)
	if followup := voiceFollowupQuestion(language, conversation); followup != "" {
		message += " " + followup
	}
	return s.sendAndRemember(ctx, chatID, message, replyStageForConversation(conversation), selectedLevelFromConversation(conversation), fieldVoicePreference)
}

func (s *Service) handleCopyrightQuestion(ctx context.Context, chatID string, language string, conversation Conversation) error {
	message := copyrightSafeReplyText(language, conversation)
	return s.sendAndRemember(ctx, chatID, message, replyStageForConversation(conversation), selectedLevelFromConversation(conversation), fieldVoicePreference)
}

func (s *Service) handleFormatPreference(ctx context.Context, chatID string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	liked := "выбранный формат"
	if len(analysis.LikedFormats) > 0 && analysis.LikedFormats[0] == "both" {
		liked = "оба формата"
	}
	message := formatPreferenceText(language, liked)
	stage := replyStageForConversation(conversation)
	askedFields := []string{fieldLikedFormats}
	if missing := qualificationMissingFields(conversation.Lead); len(missing) > 0 {
		followup := qualificationFollowupText(language, conversation)
		message += " " + followup
		askedFields = append(askedFields, missing...)
	} else if hasPackageFlowStarted(conversation) {
		message += " " + questionnaireConfirmationFallbackText(language)
		stage = ClientStateAwaitingQuestionnaireConfirm
	}
	return s.sendAndRemember(ctx, chatID, message, stage, selectedLevelFromConversation(conversation), askedFields...)
}

func (s *Service) handleNicheSpecificCaseRequest(ctx context.Context, chatID string, language string, conversation Conversation, analysis CustomerAnalysis) error {
	niche := strings.TrimSpace(conversation.Lead.Niche)
	if niche == "" && analysis.Niche != nil {
		niche = strings.TrimSpace(*analysis.Niche)
	}
	if niche == "" && analysis.ProductOrService != nil {
		niche = strings.TrimSpace(*analysis.ProductOrService)
	}
	if niche == "" {
		niche = "вашей нише"
	}
	message := nicheCaseRequestText(language, niche)
	if followup := nextUsefulQuestionText(language, conversation); followup != "" {
		message += " " + followup
	}
	return s.sendAndRemember(ctx, chatID, message, replyStageForConversation(conversation), selectedLevelFromConversation(conversation), qualificationMissingFields(conversation.Lead)...)
}

func replyStageForConversation(conversation Conversation) string {
	if conversation.Stage != "" && conversation.Stage != ClientStateNeutralNew {
		return conversation.Stage
	}
	return ClientStateAwaitingQualification
}

func nextUsefulQuestionText(language string, conversation Conversation) string {
	if missing := qualificationMissingFields(conversation.Lead); len(missing) > 0 {
		return qualificationFollowupText(language, conversation)
	}
	if hasPackageFlowStarted(conversation) && !isValidPackageInterest(conversation.Lead.SelectedPackage) {
		return FormatQuestionText(language)
	}
	return ""
}

func confusionExplanationText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Қарапайым түсіндірейін: біз сіздің нишаңызға қысқа AI-жарнамалық ролик жасаймыз, оны жарнамаға қосып өтінім/сатылым алуға болады."
	case "en":
		return "Let me explain simply: we create a short AI ad video for your niche, ready to launch in ads for leads or sales."
	default:
		return "Понял, объясню проще: мы делаем короткие AI-рекламные ролики под вашу нишу, чтобы быстро зацепить внимание и привести заявки или продажи."
	}
}

func feasibilityAnswerText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Иә, мұндай форматты сіздің нишаңызға бейімдей аламыз: референстің идеясын/динамикасын сақтап, өнімге сай жасаймыз."
	case "en":
		return "Yes, we can adapt that kind of format for your niche: keep the reference idea/dynamics, but make it for your product."
	default:
		return "Да, такой формат можем адаптировать под вашу нишу: сохраним идею и динамику референса, но сделаем под ваш продукт."
	}
}

func voiceQuestionAnswerText(language string, text string) string {
	normalized := normalizeForAnalysis(text)
	switch normalizeLanguageCode(language) {
	case "kk":
		if strings.Contains(normalized, "выб") || strings.Contains(normalized, "таң") {
			return "Иә, дауысты стиль бойынша таңдауға болады: ер/әйел, сабырлы, премиалды, энергиялы немесе кинематографиялық."
		}
		return "Озвучканы AI-дауыс арқылы жасай аламыз немесе керек стильге жақын дауыс таңдаймыз. Тірі диктор керек болса, оны бөлек келісеміз."
	case "en":
		if strings.Contains(normalized, "choose") || strings.Contains(normalized, "select") {
			return "Yes, the voice can be selected by style: male/female, calm, premium, energetic, or cinematic."
		}
		return "We can do the voice-over with an AI voice or select a voice for the needed style. If you need a live voice actor, we should agree that separately."
	default:
		if strings.Contains(normalized, "выб") {
			return "Да, голос можно выбрать по стилю: мужской/женский, спокойный, премиальный, энергичный или кинематографичный."
		}
		return "Озвучку можем сделать AI-голосом или подобрать голос под нужный стиль. Если нужен живой диктор, лучше согласуем отдельно."
	}
}

func voiceFollowupQuestion(language string, conversation Conversation) string {
	if strings.TrimSpace(conversation.Lead.VoicePreference) != "" {
		return ""
	}
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Қандай стиль жақын: салмақты, премиалды, энергиялы немесе кинематографиялық?"
	case "en":
		return "Which style is closer: serious, premium, energetic, or cinematic?"
	default:
		return "Какой стиль голоса хотите: серьёзный, премиальный, энергичный или более кинематографичный?"
	}
}

func formatPreferenceText(language string, liked string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Жақсы, түсіндім: форматтар ұнады. Екеуінің мықты жақтарын біріктіріп, нишаңызға бейімдей аламыз."
	case "en":
		return "Great, noted: the formats work for you. We can combine the strengths and adapt them for your niche."
	default:
		if liked == "оба формата" {
			return "Отлично, понял, оба формата подходят. Можем взять сильные стороны обоих и адаптировать под вашу нишу."
		}
		return "Отлично, понял, формат подходит. Адаптируем его под вашу нишу и задачу."
	}
}

func nicheCaseRequestText(language string, niche string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return fmt.Sprintf("Иә, %s бағытына жақын мысалдарды көрсете аламыз. Дәл осы нишада кейс болмаса, жақын тауарлық/авто форматты бейімдейміз.", niche)
	case "en":
		return fmt.Sprintf("Yes, we can show examples close to %s. If there is no exact niche case, we can use a close product/auto format and adapt it.", niche)
	default:
		return fmt.Sprintf("Да, по %s можем показать близкие примеры. Если точного кейса сейчас нет, покажем близкий товарный/авто-формат и адаптируем под вашу задачу.", niche)
	}
}
