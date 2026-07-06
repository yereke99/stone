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
	if replyTooLong(message) {
		return outgoingReplyValidation{
			Message:   safeContextualFallbackText(conversation.Language, conversation),
			Status:    "failed_too_long",
			Prevented: true,
		}
	}
	if containsTooCasualTone(message) {
		return outgoingReplyValidation{
			Message:   safeContextualFallbackText(conversation.Language, conversation),
			Status:    "failed_too_casual",
			Prevented: true,
		}
	}
	if containsUnsupportedPrice(message) {
		return outgoingReplyValidation{
			Message:   safeContextualFallbackText(conversation.Language, conversation),
			Status:    "failed_unsupported_price",
			Prevented: true,
		}
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
	if containsUnsafeDronePromise(message) {
		return outgoingReplyValidation{
			Message:   safeDroneReplyText(conversation.Language, conversation),
			Status:    "failed_real_drone_promise",
			Prevented: true,
		}
	}
	if claimsMediaAlreadySent(message) {
		return outgoingReplyValidation{
			Message:   safeContextualFallbackText(conversation.Language, conversation),
			Status:    "failed_false_media_claim",
			Prevented: true,
		}
	}
	if asksDeadlineTooEarly(message, stage, conversation) {
		return outgoingReplyValidation{
			Message:   nonRepeatedNextQuestionText(conversation.Language, conversation),
			Status:    "failed_deadline_too_early",
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

func replyTooLong(message string) bool {
	return len([]rune(strings.TrimSpace(message))) > 900
}

func containsTooCasualTone(message string) bool {
	for _, phrase := range []string{
		"супер",
		"класс",
		"круто",
		"ого",
		"без проблемчик",
		"ща",
		"щас",
		"кайф",
		"топчик",
	} {
		if containsWordOrPhrase(message, phrase) {
			return true
		}
	}
	return false
}

func containsUnsupportedPrice(message string) bool {
	normalized := normalizeForAnalysis(message)
	if !containsAny(normalized, []string{"тг", "тенге", "kzt"}) {
		return false
	}
	compact := strings.NewReplacer(" ", "", "\u00a0", "", ".", "", ",", "").Replace(normalized)
	for _, price := range []string{"35000", "50000", "75000"} {
		compact = strings.ReplaceAll(compact, price, "")
	}
	var sequence strings.Builder
	flush := func() bool {
		value := sequence.String()
		sequence.Reset()
		if len(value) < 2 || value == "48" {
			return false
		}
		return true
	}
	for _, r := range compact {
		if r >= '0' && r <= '9' {
			sequence.WriteRune(r)
			continue
		}
		if flush() {
			return true
		}
	}
	return flush()
}

func askedFieldsContain(fields []string, target string) bool {
	target = normalizeFieldName(target)
	for _, field := range fields {
		if normalizeFieldName(field) == target {
			return true
		}
	}
	return false
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

func containsUnsafeDronePromise(message string) bool {
	normalized := normalizeForAnalysis(message)
	if !containsAny(normalized, []string{"дрон", "drone"}) {
		return false
	}
	if containsAny(normalized, []string{"ai", "ии", "ai-визуал", "ии-визуал", "уточнить с менеджером", "уточнит менеджер", "реальная съемка отдельно", "реальная съёмка отдельно"}) {
		return false
	}
	return containsAny(normalized, []string{
		"снимем",
		"снимать",
		"проведем съемку",
		"проведём съёмку",
		"съемка с дрона",
		"съёмка с дрона",
		"реальную съемку",
		"реальную съёмку",
		"дрон съемку",
		"дрон съёмку",
	})
}

func claimsMediaAlreadySent(message string) bool {
	normalized := normalizeForAnalysis(message)
	if !containsAny(normalized, []string{"пример", "видео", "ролик", "кейс", "материал"}) {
		return false
	}
	return containsAny(normalized, []string{
		"уже отправил",
		"уже отправила",
		"отправил вам",
		"отправила вам",
		"выслал",
		"выслала",
		"прикрепил",
		"прикрепила",
		"скинул",
		"скинула",
	})
}

func asksDeadlineTooEarly(message string, stage string, conversation Conversation) bool {
	if stage != ClientStateAwaitingQualification && stage != StageQualification && stage != StageDiagnosis {
		return false
	}
	if isValidNiche(conversation.Lead.Niche) && isValidGoal(conversation.Lead.Goal) {
		return false
	}
	return askedFieldsContain(fieldsAskedByMessage(message, stage), fieldDeadline)
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

func safeDroneReplyText(language string, conversation Conversation) string {
	followup := ""
	if missing := qualificationMissingFields(conversation.Lead); len(missing) > 0 {
		followup = " " + qualificationFollowupText(language, conversation)
	}
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Біз сату үшін AI-визуализация/ролик дайындай аламыз. Егер нақты дронмен real съёмка керек болса, оны менеджермен бөлек нақтылаған дұрыс." + followup
	case "en":
		return "We can prepare an AI visualization/video for sales. If you need real drone filming specifically, it is better to confirm that separately with a manager." + followup
	default:
		return "Мы можем подготовить AI-визуализацию/ролик под продажу. Если нужна именно реальная съёмка с дрона, это лучше отдельно уточнить с менеджером." + followup
	}
}
