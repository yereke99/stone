package bot

import (
	"strings"
	"time"
)

const (
	FactStatusUnknown   = "unknown"
	FactStatusInferred  = "inferred"
	FactStatusConfirmed = "confirmed"
)

type MemoryFact struct {
	Value     string    `json:"value,omitempty"`
	Status    string    `json:"status,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type CustomerMemory struct {
	Phone                    MemoryFact `json:"phone,omitempty"`
	PreferredLanguage        MemoryFact `json:"preferred_language,omitempty"`
	CompanyName              MemoryFact `json:"company_name,omitempty"`
	Niche                    MemoryFact `json:"niche,omitempty"`
	DetailedBusinessActivity MemoryFact `json:"detailed_business_activity,omitempty"`
	ProductOrService         MemoryFact `json:"product_or_service,omitempty"`
	AdvertisingGoal          MemoryFact `json:"advertising_goal,omitempty"`
	TargetAudience           MemoryFact `json:"target_audience,omitempty"`
	RequestedContentFormat   MemoryFact `json:"requested_content_format,omitempty"`
	ApproximateDeadline      MemoryFact `json:"approximate_deadline,omitempty"`
	ApproximateBudget        MemoryFact `json:"approximate_budget,omitempty"`
	IntentLevel              MemoryFact `json:"intent_level,omitempty"`
	QualificationStatus      MemoryFact `json:"qualification_status,omitempty"`
	QuestionnaireStatus      MemoryFact `json:"questionnaire_status,omitempty"`
	LatestUnresolvedQuestion MemoryFact `json:"latest_unresolved_question,omitempty"`

	QuestionsAlreadyAsked []string          `json:"questions_already_asked,omitempty"`
	AnswersReceived       map[string]string `json:"answers_received,omitempty"`
	CustomerObjections    []string          `json:"customer_objections,omitempty"`
	CasesSent             []string          `json:"cases_sent,omitempty"`
	CaseVideosSent        []string          `json:"case_videos_sent,omitempty"`
	OffersSent            []string          `json:"offers_sent,omitempty"`
	HandedToAdministrator bool              `json:"handed_to_administrator,omitempty"`
	LastMeaningfulAt      time.Time         `json:"last_meaningful_at,omitempty"`
}

func refreshCustomerMemory(conversation *Conversation) {
	if conversation == nil {
		return
	}
	now := time.Now().UTC()
	memory := conversation.Memory
	if memory.AnswersReceived == nil {
		memory.AnswersReceived = make(map[string]string)
	}
	lead := conversation.Lead
	setFact := func(fact *MemoryFact, value string, status string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if status == "" {
			status = FactStatusConfirmed
		}
		if fact.Value == value && fact.Status == status {
			return
		}
		fact.Value = value
		fact.Status = status
		fact.UpdatedAt = now
	}

	setFact(&memory.Phone, conversation.Phone, FactStatusConfirmed)
	if strings.TrimSpace(memory.Phone.Value) == "" {
		setFact(&memory.Phone, phoneFromChatID(conversation.ChatID), FactStatusConfirmed)
	}
	setFact(&memory.PreferredLanguage, normalizeLanguageCode(conversation.Language), FactStatusConfirmed)
	setFact(&memory.CompanyName, lead.ClientName, FactStatusConfirmed)
	setFact(&memory.Niche, lead.Niche, FactStatusConfirmed)
	setFact(&memory.DetailedBusinessActivity, lead.StrongSide, FactStatusConfirmed)
	setFact(&memory.ProductOrService, lead.ProductOrService, FactStatusConfirmed)
	setFact(&memory.AdvertisingGoal, lead.Goal, FactStatusConfirmed)
	setFact(&memory.TargetAudience, lead.TargetAudience, FactStatusConfirmed)
	setFact(&memory.RequestedContentFormat, lead.SelectedPackage, FactStatusConfirmed)
	setFact(&memory.ApproximateDeadline, lead.Deadline, FactStatusConfirmed)
	setFact(&memory.ApproximateBudget, lead.Budget, FactStatusConfirmed)
	setFact(&memory.IntentLevel, normalizeLeadStatus(conversation.LeadStatus), FactStatusConfirmed)
	setFact(&memory.QualificationStatus, qualificationStatusLabelForMemory(*conversation), FactStatusConfirmed)
	setFact(&memory.QuestionnaireStatus, questionnaireStatusForMemory(*conversation), FactStatusConfirmed)
	if unresolved := unresolvedQuestionForMemory(*conversation); unresolved != "" {
		setFact(&memory.LatestUnresolvedQuestion, unresolved, FactStatusInferred)
	} else {
		memory.LatestUnresolvedQuestion = MemoryFact{Status: FactStatusUnknown, UpdatedAt: now}
	}

	for _, field := range mapKeys(conversation.AskedFields) {
		memory.QuestionsAlreadyAsked = appendUniqueString(memory.QuestionsAlreadyAsked, field)
	}
	if isValidNiche(lead.Niche) {
		memory.AnswersReceived[fieldNiche] = strings.TrimSpace(lead.Niche)
	}
	if isValidGoal(lead.Goal) {
		memory.AnswersReceived[fieldGoal] = strings.TrimSpace(lead.Goal)
	}
	if strings.TrimSpace(lead.ProductOrService) != "" {
		memory.AnswersReceived[fieldProductService] = strings.TrimSpace(lead.ProductOrService)
	}
	if strings.TrimSpace(lead.TargetAudience) != "" {
		memory.AnswersReceived[fieldTargetAudience] = strings.TrimSpace(lead.TargetAudience)
	}
	if strings.TrimSpace(lead.Deadline) != "" {
		memory.AnswersReceived[fieldDeadline] = strings.TrimSpace(lead.Deadline)
	}
	if strings.TrimSpace(lead.Budget) != "" {
		memory.AnswersReceived[fieldBudget] = strings.TrimSpace(lead.Budget)
	}
	if strings.TrimSpace(lead.CopyrightConcern) != "" {
		memory.CustomerObjections = appendUniqueString(memory.CustomerObjections, lead.CopyrightConcern)
	}
	for fileName := range conversation.SentVideoFiles {
		if normalized := normalizeVideoFileForSend(fileName, map[string]struct{}{
			VideoLevel1: {}, VideoLevel2: {}, VideoLevel3: {}, VideoLevel4: {},
		}); normalized != "" {
			memory.CaseVideosSent = appendUniqueString(memory.CaseVideosSent, normalized)
			if caseID := portfolioCaseIDByVideoPath(normalized); caseID != "" {
				memory.CasesSent = appendUniqueString(memory.CasesSent, caseID)
			}
		}
	}
	if conversation.Lead.OfferSent || conversation.PackagesSent {
		memory.OffersSent = appendUniqueString(memory.OffersSent, "package_options")
	}
	memory.HandedToAdministrator = conversation.HandedOffToOwner || !conversation.TransferredAt.IsZero()
	if !conversation.LastIncomingAt.IsZero() {
		memory.LastMeaningfulAt = conversation.LastIncomingAt
	} else if !conversation.UpdatedAt.IsZero() {
		memory.LastMeaningfulAt = conversation.UpdatedAt
	}
	conversation.Memory = memory
}

func questionnaireStatusForMemory(conversation Conversation) string {
	switch {
	case conversation.Lead.BriefCompleted || conversation.BriefCollected:
		return "completed"
	case conversation.QuestionnaireSent || conversation.Lead.BriefRequested:
		return "sent"
	case conversation.QuestionnaireOfferSent || conversation.WantsQuestionnaire || conversation.Lead.WantsQuestionnaire:
		return "offered"
	default:
		return "unknown"
	}
}

func qualificationStatusLabelForMemory(conversation Conversation) string {
	missing := requiredLeadMissingFields(conversation)
	if len(missing) == 0 {
		return "qualified"
	}
	return "missing:" + strings.Join(missing, ",")
}

func unresolvedQuestionForMemory(conversation Conversation) string {
	if missing := requiredLeadMissingFields(conversation); len(missing) > 0 {
		return strings.Join(missing, ",")
	}
	if conversation.QuestionnaireOfferSent && !conversation.QuestionnaireSent {
		return "questionnaire_confirmation"
	}
	if conversation.QuestionnaireSent && !conversation.Lead.BriefCompleted {
		return "questionnaire_answers"
	}
	return ""
}
