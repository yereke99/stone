package bot

import (
	"strings"
)

type outgoingReplyValidation struct {
	Message   string
	Status    string
	Prevented bool
}

func validateOutgoingReply(message string, stage string, conversation Conversation) outgoingReplyValidation {
	message = strings.TrimSpace(message)
	if message == "" {
		return outgoingReplyValidation{Status: "empty"}
	}
	if containsForbiddenDiscountPromise(message) {
		return outgoingReplyValidation{
			Message:   safeContextualFallbackText(conversation.Language, conversation),
			Status:    "failed_forbidden_discount",
			Prevented: true,
		}
	}
	if containsUnsafeCelebrityPromise(message) {
		return outgoingReplyValidation{
			Message:   copyrightSafeReplyText(conversation.Language, conversation),
			Status:    "failed_celebrity_rights",
			Prevented: true,
		}
	}
	if stage == StageBriefRequested {
		return outgoingReplyValidation{Message: message, Status: "passed"}
	}
	if stage == ClientStateAwaitingQuestionnaireConfirm &&
		conversation.QuestionnaireOfferSent &&
		strings.Contains(normalizeForAnalysis(message), "отправить анкету") {
		return outgoingReplyValidation{Message: message, Status: "passed"}
	}
	knownAsked := knownFieldsAskedByReply(message, stage, conversation)
	if len(knownAsked) == 0 {
		return outgoingReplyValidation{Message: message, Status: "passed"}
	}
	replacement := nonRepeatedNextQuestionText(conversation.Language, conversation)
	return outgoingReplyValidation{
		Message:   replacement,
		Status:    "failed_repeated_question",
		Prevented: true,
	}
}

func knownFieldsAskedByReply(message string, stage string, conversation Conversation) []string {
	fields := fieldsAskedByMessage(message, stage)
	known := make([]string, 0, len(fields))
	for _, field := range fields {
		if fieldKnownInConversation(conversation, field) {
			known = append(known, field)
		}
	}
	return normalizeFieldList(known)
}

func nonRepeatedNextQuestionText(language string, conversation Conversation) string {
	if missing := qualificationMissingFields(conversation.Lead); len(missing) > 0 {
		return qualificationFollowupText(language, conversation)
	}
	if hasPackageFlowStarted(conversation) && !isValidPackageInterest(conversation.Lead.SelectedPackage) {
		return FormatQuestionText(language)
	}
	return packagesPresentedFallbackText(language)
}

func safeContextualFallbackText(language string, conversation Conversation) string {
	if missing := qualificationMissingFields(conversation.Lead); len(missing) > 0 {
		return qualificationFollowupText(language, conversation)
	}
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім, нақты шартты менеджер есептеп береді. Қысқа брифке өте аламыз."
	case "en":
		return "Got it, the exact terms should be calculated by a manager. We can move to the short brief."
	default:
		return "Понял, точные условия лучше посчитает менеджер. Можем перейти к короткому брифу."
	}
}

func containsForbiddenDiscountPromise(message string) bool {
	normalized := normalizeForAnalysis(message)
	return containsAny(normalized, []string{
		"от 2 роликов 10%",
		"от 3-5 до 20%",
		"скидки до 30%",
		"от 10 роликов",
	})
}

func containsUnsafeCelebrityPromise(message string) bool {
	normalized := normalizeForAnalysis(message)
	mentionsCelebrity := containsAny(normalized, []string{
		"вин диз", "vin diesel", "актер", "актёр", "селебр", "звезд", "публичн",
	})
	if !mentionsCelebrity {
		return false
	}
	if containsAny(normalized, []string{"нельзя", "без прав нельзя", "без копирования", "не копируя", "нужны права", "нельзя использовать без прав"}) {
		return false
	}
	return containsAny(normalized, []string{"можем", "сделаем", "поставим", "используем", "скопируем", "клонируем"})
}

func copyrightSafeReplyText(language string, conversation Conversation) string {
	followup := ""
	if strings.TrimSpace(conversation.Lead.VoicePreference) == "" {
		switch normalizeLanguageCode(language) {
		case "kk":
			followup = " Қандай стиль жақын: салмақты, премиалды, энергиялы немесе кинематографиялық?"
		case "en":
			followup = " Which style is closer: serious, premium, energetic, or cinematic?"
		default:
			followup = " Какой стиль ближе: серьёзный, премиальный, энергичный или кинематографичный?"
		}
	}
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Нақты актердің/публичный адамның дауысын немесе образын құқықсыз қолдануға болмайды. Бірақ сол көңіл-күйге жақын оригинал стиль жасай аламыз, нақты адамды көшірмей." + followup
	case "en":
		return "We cannot use or clone a specific actor/public person's voice or likeness without rights. We can create a similar mood or tone as an original style without copying that person." + followup
	default:
		return "Точный голос или образ конкретного актёра/публичного человека без прав использовать нельзя. Но можем сделать похожее настроение и подачу без копирования конкретного человека." + followup
	}
}
