package bot

import (
	"encoding/json"
	"strings"
	"unicode"
)

const packageNeedsManagerRecommendation = "needs_manager_recommendation"

type managerQualification struct {
	Ready   bool
	Missing []string
}

func managerQualificationForConversation(conversation Conversation) managerQualification {
	missing := requiredLeadMissingFields(conversation)
	return managerQualification{
		Ready:   len(missing) == 0,
		Missing: missing,
	}
}

func leadHasRequiredManagerFields(lead LeadState) bool {
	return isValidNiche(lead.Niche) &&
		isValidGoal(lead.Goal) &&
		isValidDeadline(lead.Deadline) &&
		isValidPackageInterest(lead.SelectedPackage)
}

func requiredLeadMissingFields(conversation Conversation) []string {
	lead := conversation.Lead
	missing := make([]string, 0, 4)
	if !isValidNiche(lead.Niche) {
		missing = append(missing, fieldNiche)
	}
	if !isValidGoal(lead.Goal) {
		missing = append(missing, fieldGoal)
	}
	if !isValidDeadline(lead.Deadline) {
		missing = append(missing, fieldDeadline)
	}
	if !isValidPackageInterest(lead.SelectedPackage) {
		missing = append(missing, fieldPackageInterest)
	}
	return missing
}

func sanitizeLeadForQualification(lead LeadState) LeadState {
	if !isValidNiche(lead.Niche) {
		lead.Niche = ""
	}
	if !isValidGoal(lead.Goal) {
		lead.Goal = ""
	}
	if !isValidDeadline(lead.Deadline) {
		lead.Deadline = ""
	}
	if !isValidPackageInterest(lead.SelectedPackage) {
		lead.SelectedPackage = ""
	} else {
		lead.SelectedPackage = normalizePackageInterest(lead.SelectedPackage)
	}
	if !leadHasRequiredManagerFields(lead) {
		lead.BriefCompleted = false
		lead.ContactBriefReady = false
		if normalizeLeadStatus(lead.LeadStatus) == LeadStatusHandoffRequired {
			lead.LeadStatus = ""
		}
	}
	return lead
}

func isValidNiche(value string) bool {
	clean := normalizedLeadValue(value)
	if isWeakLeadValue(clean) {
		return false
	}
	if containsAny(clean, []string{"не знаю", "пока не знаю", "не определ", "хз", "no idea", "dont know", "don't know"}) {
		return false
	}
	return meaningfulRuneCount(clean) >= 2
}

func isValidGoal(value string) bool {
	clean := normalizedLeadValue(value)
	if isWeakLeadValue(clean) {
		return false
	}
	if normalizeGoal(clean) != "" {
		return true
	}
	if meaningfulRuneCount(clean) < 6 {
		return false
	}
	return containsAny(clean, []string{
		"клиент", "заяв", "лид", "продаж", "сат", "ролик", "контент", "reels", "рилс",
		"instagram", "tiktok", "тик ток", "продвиж", "реклам", "жарнама", "курс",
		"product", "service", "clients", "sales", "ads", "content",
	})
}

func isValidDeadline(value string) bool {
	clean := normalizedLeadValue(value)
	if isWeakLeadValue(clean) {
		return false
	}
	return normalizeDeadline(clean) != ""
}

func isValidPackageInterest(value string) bool {
	switch normalizePackageInterest(value) {
	case "test", "basic", "standard", packageNeedsManagerRecommendation:
		return true
	default:
		return false
	}
}

func normalizePackageInterest(value string) string {
	clean := normalizedLeadValue(value)
	switch {
	case clean == packageNeedsManagerRecommendation:
		return packageNeedsManagerRecommendation
	case containsAny(clean, []string{"тест", "test", "тестовый", "тестілік"}):
		return "test"
	case containsAny(clean, []string{"базов", "basic", "базалык", "базалық"}):
		return "basic"
	case containsAny(clean, []string{"стандарт", "премиум", "standard", "premium"}):
		return "standard"
	case packageRecommendationRequested(clean):
		return packageNeedsManagerRecommendation
	default:
		return ""
	}
}

func extractPackageInterest(text string, current LeadState, selectedLevel int) string {
	normalized := normalizeForAnalysis(text)
	if containsPriceQuestion(normalized) || isPackageInfoQuestion(normalized) {
		return ""
	}
	if selectedLevel > 0 {
		return packageKey(selectedLevel)
	}
	if packageRecommendationRequested(normalized) {
		return packageNeedsManagerRecommendation
	}
	if isValidPackageInterest(normalized) {
		return normalizePackageInterest(normalized)
	}
	if packageMissingAfterBusinessFields(current) && packageUncertainty(normalized) {
		return packageNeedsManagerRecommendation
	}
	return ""
}

func packageMissingAfterBusinessFields(lead LeadState) bool {
	return isValidNiche(lead.Niche) &&
		isValidGoal(lead.Goal) &&
		isValidDeadline(lead.Deadline) &&
		!isValidPackageInterest(lead.SelectedPackage)
}

func packageRecommendationRequested(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	return containsAny(normalized, []string{
		"менеджер посовет", "менеджер подбер", "пусть менеджер", "посоветуйте",
		"подберите", "не знаю какой пакет", "не знаю пакет", "какой подойдет",
		"какой подойдёт", "на ваше усмотрение", "manager recommend", "manager can recommend",
		"recommend a package", "suggest a package",
	})
}

func packageUncertainty(normalized string) bool {
	normalized = normalizedLeadValue(normalized)
	switch normalized {
	case "не знаю", "пока не знаю", "не определился", "не определились", "хз",
		"dont know", "don't know", "not sure", "i do not know", "билмеймин", "білмеймін":
		return true
	default:
		return false
	}
}

func containsQuestionnaireIntent(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	return containsAny(normalized, []string{
		"откройте анкет", "открывайте анкет", "давайте анкет", "заполнить анкет",
		"оставить заявку", "хочу заявку", "готов заполнить", "давайте начнем", "давайте начнём",
		"хочу начать", "open the questionnaire", "start the brief", "ready to proceed",
	})
}

func normalizedLeadValue(value string) string {
	value = normalizeForAnalysis(value)
	value = strings.Trim(value, " -—:;,.!?")
	return strings.Join(strings.Fields(value), " ")
}

func isWeakLeadValue(clean string) bool {
	if clean == "" {
		return true
	}
	switch clean {
	case "-", ".", "_", "?", "!", "нет", "no", "none", "unknown", "не знаю", "пока не знаю", "неа":
		return true
	}
	return meaningfulRuneCount(clean) < 2
}

func meaningfulRuneCount(value string) int {
	count := 0
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			count++
		}
	}
	return count
}

func refreshConversationDerivedState(conversation *Conversation) {
	if conversation == nil {
		return
	}
	conversation.Lead = sanitizeLeadForQualification(conversation.Lead)
	if conversation.Lead.WantsQuestionnaire {
		conversation.WantsQuestionnaire = true
	}
	if !leadHasRequiredManagerFields(conversation.Lead) && normalizeLeadStatus(conversation.LeadStatus) == LeadStatusHandoffRequired {
		conversation.LeadStatus = LeadStatusHot
	}
	conversation.BriefAsked = conversation.Lead.BriefRequested
	conversation.BriefCollected = conversation.Lead.BriefCompleted
	conversation.MissingFields = requiredLeadMissingFields(*conversation)
	conversation.ConversationSummary = buildConversationSummary(*conversation)
	if conversation.HandedOffToOwner && conversation.TransferredAt.IsZero() && !conversation.AdminNotifiedAt.IsZero() {
		conversation.TransferredAt = conversation.AdminNotifiedAt
	}
	if conversation.HandedOffToOwner || !conversation.TransferredAt.IsZero() {
		conversation.AutomationClosed = true
	}
}

func missingFieldsJSONString(fields []string) string {
	if fields == nil {
		fields = []string{}
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func buildConversationSummary(conversation Conversation) string {
	lead := conversation.Lead
	parts := make([]string, 0, 4)
	if isValidNiche(lead.Niche) {
		parts = append(parts, "niche: "+strings.TrimSpace(lead.Niche))
	}
	if isValidGoal(lead.Goal) {
		parts = append(parts, "goal: "+strings.TrimSpace(lead.Goal))
	}
	if isValidDeadline(lead.Deadline) {
		parts = append(parts, "deadline: "+strings.TrimSpace(lead.Deadline))
	}
	if isValidPackageInterest(lead.SelectedPackage) {
		parts = append(parts, "package: "+adminPackageLabel(lead.SelectedPackage))
	}
	if len(parts) == 0 {
		return strings.TrimSpace(conversation.LastIncomingText)
	}
	summary := "Client details collected: " + strings.Join(parts, "; ") + "."
	if conversation.WantsQuestionnaire || lead.WantsQuestionnaire {
		summary += " Client wants to proceed/open the questionnaire."
	}
	return summary
}
