package bot

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

const (
	fieldNiche           = "niche"
	fieldCity            = "city"
	fieldGoal            = "goal"
	fieldPlatform        = "platform"
	fieldPlatforms       = fieldPlatform
	fieldDeadline        = "deadline"
	fieldPreviousAIAds   = "ai_experience"
	fieldPackage         = "package"
	fieldPackageInterest = "package_interest"
	fieldVideoQuantity   = "video_quantity"
	fieldProductService  = "product_or_service"
	fieldTargetAudience  = "target_audience"
	fieldReferenceLinks  = "reference_links"
	fieldLikedFormats    = "liked_formats"
	fieldVoicePreference = "voice_preference"
	fieldBrief           = "brief"

	IntentGreeting                 = "greeting"
	IntentAnswer                   = "answer"
	IntentPortfolioRequest         = "portfolio_request"
	IntentNicheSpecificCaseRequest = "niche_specific_case_request"
	IntentFeasibilityQuestion      = "feasibility_question"
	IntentConfusion                = "confusion"
	IntentVoiceQuestion            = "voice_question"
	IntentCopyrightQuestion        = "copyright_question"
	IntentFormatPreference         = "format_preference"
	IntentPriceQuestion            = "price_question"
	IntentPackageQuestion          = "package_question"
	IntentPackageSelection         = "package_selection"
	IntentQuantityDiscountQuestion = "quantity_discount_question"
	IntentAgreement                = "agreement"
	IntentRefusal                  = "refusal"
	IntentDeadlineQuestion         = "deadline_question"
	IntentReadyToOrder             = "ready_to_order"
	IntentObjection                = "objection"
	IntentDefer                    = "defer"
	IntentFrustration              = "frustration"
	IntentNegativeReaction         = "negative_reaction"
	IntentBriefAnswer              = "brief_answer"
	IntentHumanRequest             = "human_request"
	IntentMute                     = "mute"
	IntentFAQ                      = "faq"
	IntentBusinessLink             = "business_link"
	IntentFormatAdvice             = "asks_which_format_is_best"
	IntentOther                    = "other"
)

var prefixedValuePattern = regexp.MustCompile(`(?i)(?:^|\s)(%s)\s*[:=\-—]?\s*([^,.!?;]+)`)
var numberedQualificationLinePattern = regexp.MustCompile(`(?m)^\s*([123])\s*[\).:\-—]\s*(.+?)\s*$`)
var videoQuantityRangePattern = regexp.MustCompile(`(?i)(?:^|[^0-9])(\d{1,3})\s*[-–—]\s*(\d{1,3})(?:\s*(?:ролик(?:а|ов)?|видео|креатив(?:а|ов)?|штук|шт\.?))?(?:$|[^0-9])`)
var videoQuantitySingleWithUnitPattern = regexp.MustCompile(`(?i)(?:^|[^0-9])(\d{1,3})\s*(?:ролик(?:а|ов)?|видео|креатив(?:а|ов)?|штук|шт\.?)(?:$|[^0-9])`)
var bareVideoQuantityRangePattern = regexp.MustCompile(`^(\d{1,3})[-–—](\d{1,3})$`)
var bareVideoQuantitySinglePattern = regexp.MustCompile(`^\d{1,3}$`)
var urlLikePattern = regexp.MustCompile(`(?i)\b(?:https?://|www\.|(?:instagram|instagr\.am|tiktok|youtube|youtu\.be|wa|taplink|linktr|2gis)\.)[^\s<>"']+|(?:^|\s)@[a-z0-9_.]{3,}`)

type LeadState struct {
	HasBeenGreeted           bool     `json:"has_been_greeted,omitempty"`
	Niche                    string   `json:"niche,omitempty"`
	City                     string   `json:"city,omitempty"`
	Goal                     string   `json:"goal,omitempty"`
	Platform                 string   `json:"platform,omitempty"`
	Platforms                []string `json:"platforms,omitempty"`
	Deadline                 string   `json:"deadline,omitempty"`
	PreviousAIAds            *bool    `json:"previous_ai_ads,omitempty"`
	AIExperience             string   `json:"ai_experience,omitempty"`
	Budget                   string   `json:"budget,omitempty"`
	PriceInterest            bool     `json:"price_interest,omitempty"`
	QuantityDiscountInterest bool     `json:"quantity_discount_interest,omitempty"`
	VideoQuantity            string   `json:"video_quantity,omitempty"`
	VoicePreference          string   `json:"voice_preference,omitempty"`
	CopyrightConcern         string   `json:"copyright_concern,omitempty"`
	CampaignContext          string   `json:"campaign_context,omitempty"`
	HookIdea                 string   `json:"hook_idea,omitempty"`
	LikedFormats             []string `json:"liked_formats,omitempty"`
	ProductOrService         string   `json:"product_or_service,omitempty"`
	StrongSide               string   `json:"strong_side,omitempty"`
	Offer                    string   `json:"offer,omitempty"`
	WebsiteOrInstagram       string   `json:"website_or_instagram,omitempty"`
	ReferenceLinks           []string `json:"reference_links,omitempty"`
	SelectedPackage          string   `json:"selected_package,omitempty"`
	WantsQuestionnaire       bool     `json:"wants_questionnaire,omitempty"`
	ClientName               string   `json:"client_name,omitempty"`
	TargetAudience           string   `json:"target_audience,omitempty"`
	Notes                    string   `json:"notes,omitempty"`
	FreeText                 string   `json:"free_text,omitempty"`
	PortfolioSent            bool     `json:"portfolio_sent,omitempty"`
	OfferSent                bool     `json:"offer_sent,omitempty"`
	BriefRequested           bool     `json:"brief_requested,omitempty"`
	BriefCompleted           bool     `json:"brief_completed,omitempty"`
	ContactBriefReady        bool     `json:"contact_brief_ready,omitempty"`
	LeadStatus               string   `json:"lead_status,omitempty"`
}

type CustomerAnalysis struct {
	Niche               *string  `json:"niche"`
	City                *string  `json:"city,omitempty"`
	Goal                *string  `json:"goal"`
	Platforms           []string `json:"platforms"`
	Deadline            *string  `json:"deadline"`
	PreviousAIAds       *bool    `json:"previous_ai_ads"`
	AIExperience        *string  `json:"ai_experience,omitempty"`
	Budget              *string  `json:"budget,omitempty"`
	VideoQuantity       *string  `json:"video_quantity,omitempty"`
	VoicePreference     *string  `json:"voice_preference,omitempty"`
	CopyrightConcern    *string  `json:"copyright_concern,omitempty"`
	CampaignContext     *string  `json:"campaign_context,omitempty"`
	HookIdea            *string  `json:"hook_idea,omitempty"`
	LikedFormats        []string `json:"liked_formats,omitempty"`
	TargetAudience      *string  `json:"target_audience,omitempty"`
	ProductOrService    *string  `json:"product_or_service,omitempty"`
	StrongSide          *string  `json:"strong_side,omitempty"`
	Offer               *string  `json:"offer,omitempty"`
	BusinessLink        *string  `json:"business_link,omitempty"`
	ReferenceLinks      []string `json:"reference_links,omitempty"`
	Intent              string   `json:"intent"`
	SelectedLevel       int      `json:"selected_level,omitempty"`
	PackageInterest     *string  `json:"package_interest,omitempty"`
	WantsQuestionnaire  bool     `json:"wants_questionnaire,omitempty"`
	ShouldHandoff       bool     `json:"should_handoff,omitempty"`
	ShouldStop          bool     `json:"should_stop,omitempty"`
	Frustrated          bool     `json:"frustrated,omitempty"`
	AsksForFoodExamples bool     `json:"asks_for_food_examples,omitempty"`
	AsksForMoreOptions  bool     `json:"asks_for_more_options,omitempty"`
	FAQKey              string   `json:"faq_key,omitempty"`
	MissingFields       []string `json:"missing_fields"`

	NumberedQualificationAnswer bool               `json:"-"`
	AnsweredQuestions           []AnsweredQuestion `json:"answered_questions,omitempty"`
}

type AnsweredQuestion struct {
	BotQuestion    string  `json:"bot_question"`
	CustomerAnswer string  `json:"customer_answer"`
	Field          string  `json:"field"`
	Confidence     float64 `json:"confidence"`
}

func AnalyzeCustomerMessage(text string, current LeadState, language string) CustomerAnalysis {
	normalized := normalizeForAnalysis(text)
	analysis := CustomerAnalysis{
		Platforms: []string{},
		Intent:    IntentOther,
	}
	if normalized == "" {
		analysis.MissingFields = current.MissingCoreFields()
		return analysis
	}
	foodExampleRequest := asksForFoodExamples(normalized)
	moreOptionsRequest := asksForMoreOptions(normalized)
	quantityDiscountQuestion := isQuantityDiscountQuestion(normalized)
	feasibilityQuestion := isFeasibilityQuestion(normalized)
	confusionQuestion := isConfusionText(normalized)
	voiceQuestion := isVoiceQuestion(normalized)
	copyrightQuestion := isCopyrightQuestion(normalized)
	formatPreference := detectLikedFormats(normalized)

	if niche := extractNiche(text, current); niche != "" {
		analysis.Niche = stringPointer(niche)
	}
	if city := extractCity(text); city != "" {
		analysis.City = stringPointer(city)
	}
	if !foodExampleRequest {
		if goal := extractGoal(text, current); goal != "" {
			analysis.Goal = stringPointer(goal)
		}
	}
	analysis.Platforms = extractPlatforms(text)
	if deadline := extractDeadline(text, current); deadline != "" {
		analysis.Deadline = stringPointer(deadline)
	}
	if used := extractPreviousAIAds(text, current); used != nil {
		analysis.PreviousAIAds = used
		analysis.AIExperience = stringPointer(aiExperienceLabel(*used))
	}
	if quantity := extractVideoQuantity(text, current); quantity != "" {
		analysis.VideoQuantity = stringPointer(quantity)
	}
	if voice := extractVoicePreference(text); voice != "" {
		analysis.VoicePreference = stringPointer(voice)
	}
	if copyrightConcern := extractCopyrightConcern(text); copyrightConcern != "" {
		analysis.CopyrightConcern = stringPointer(copyrightConcern)
	}
	if campaignContext := extractCampaignContext(text); campaignContext != "" {
		analysis.CampaignContext = stringPointer(campaignContext)
	}
	if hookIdea := extractHookIdea(text); hookIdea != "" {
		analysis.HookIdea = stringPointer(hookIdea)
	}
	if len(formatPreference) > 0 {
		analysis.LikedFormats = formatPreference
	}
	if budget := extractBudget(text); budget != "" {
		analysis.Budget = stringPointer(budget)
	}
	if audience := extractTargetAudience(text); audience != "" {
		analysis.TargetAudience = stringPointer(audience)
	}
	if product := extractProductOrService(text); product != "" {
		analysis.ProductOrService = stringPointer(product)
		if analysis.Niche == nil && isValidNiche(product) && !isNonNicheCandidateText(normalizeForAnalysis(product)) {
			analysis.Niche = stringPointer(normalizeNiche(product))
		}
	}
	if strongSide := extractStrongSide(text); strongSide != "" {
		analysis.StrongSide = stringPointer(strongSide)
	}
	if !quantityDiscountQuestion {
		if offer := extractOffer(text); offer != "" {
			analysis.Offer = stringPointer(offer)
		}
	}
	if link := extractBusinessLink(text); link != "" {
		analysis.BusinessLink = stringPointer(link)
	}
	if links := extractReferenceLinks(text); len(links) > 0 {
		analysis.ReferenceLinks = links
	}
	if facts := extractMessyQualificationFacts(text, current); facts.hasAny() {
		if facts.niche != "" {
			analysis.Niche = stringPointer(facts.niche)
		}
		if facts.goal != "" {
			analysis.Goal = stringPointer(facts.goal)
		}
		if facts.deadline != "" {
			analysis.Deadline = stringPointer(facts.deadline)
		}
		if len(facts.platforms) > 0 {
			analysis.Platforms = mergePlatforms(analysis.Platforms, facts.platforms)
		}
		if facts.productOrService != "" {
			analysis.ProductOrService = stringPointer(facts.productOrService)
		}
		if facts.audience != "" {
			analysis.TargetAudience = stringPointer(facts.audience)
		}
		if facts.strongSide != "" {
			analysis.StrongSide = stringPointer(facts.strongSide)
		}
		if facts.offer != "" && !quantityDiscountQuestion {
			analysis.Offer = stringPointer(facts.offer)
		}
	}
	analysis.AsksForFoodExamples = foodExampleRequest
	analysis.AsksForMoreOptions = moreOptionsRequest
	if analysis.AsksForFoodExamples && analysis.Niche == nil {
		analysis.Niche = stringPointer("еда")
	}
	if numbered := extractNumberedQualificationAnswers(text); numbered.found {
		analysis.NumberedQualificationAnswer = true
		if numbered.niche != "" {
			analysis.Niche = stringPointer(numbered.niche)
		}
		if numbered.goal != "" {
			analysis.Goal = stringPointer(numbered.goal)
		}
		if numbered.deadline != "" {
			analysis.Deadline = stringPointer(numbered.deadline)
		}
	}
	analysis.SelectedLevel = extractSelectedLevel(text)
	if packageInterest := extractPackageInterest(text, current, analysis.SelectedLevel); packageInterest != "" {
		analysis.PackageInterest = stringPointer(packageInterest)
	}
	questionnaireIntent := containsQuestionnaireIntent(normalized)
	readySignal := containsReadySignal(text)
	deferRequest := isClientDeferText(text)
	if moreOptionsRequest || deferRequest {
		readySignal = false
	}
	analysis.WantsQuestionnaire = (questionnaireIntent || readySignal) && !deferRequest

	switch {
	case isMuteRequest(normalized):
		analysis.Intent = IntentMute
	case isFrustrationComplaint(normalized):
		analysis.Intent = IntentFrustration
		analysis.Frustrated = true
	case isNegativeReaction(normalized):
		analysis.Intent = IntentNegativeReaction
		analysis.Frustrated = true
		analysis.ShouldHandoff = true
		analysis.ShouldStop = true
	case containsHumanRequest(normalized):
		analysis.Intent = IntentHumanRequest
	case isFreeTestRequest(normalized):
		analysis.Intent = IntentObjection
	case quantityDiscountQuestion || (analysis.VideoQuantity != nil && current.QuantityDiscountInterest):
		analysis.Intent = IntentQuantityDiscountQuestion
	case copyrightQuestion:
		analysis.Intent = IntentCopyrightQuestion
	case voiceQuestion:
		analysis.Intent = IntentVoiceQuestion
	case feasibilityQuestion:
		analysis.Intent = IntentFeasibilityQuestion
	case confusionQuestion:
		analysis.Intent = IntentConfusion
	case len(formatPreference) > 0:
		analysis.Intent = IntentFormatPreference
	case analysis.AsksForMoreOptions:
		analysis.Intent = IntentPackageQuestion
	case analysis.AsksForFoodExamples:
		analysis.Intent = IntentPortfolioRequest
	case asksWhichFormatWorksBest(normalized):
		analysis.Intent = IntentFormatAdvice
	case analysis.BusinessLink != nil:
		analysis.Intent = IntentBusinessLink
	case analysis.PackageInterest != nil:
		analysis.Intent = IntentPackageSelection
	case containsPortfolioRequest(text):
		if analysis.Niche != nil || analysis.ProductOrService != nil {
			analysis.Intent = IntentNicheSpecificCaseRequest
		} else {
			analysis.Intent = IntentPortfolioRequest
		}
	case isDurationQuestion(normalized):
		analysis.Intent = IntentFAQ
		analysis.FAQKey = faqDuration
	case containsDeadlineQuestion(normalized):
		analysis.Intent = IntentDeadlineQuestion
	case analysis.SelectedLevel > 0 && isPackageInfoQuestion(normalized):
		analysis.Intent = IntentPackageQuestion
	case containsPriceQuestion(normalized):
		analysis.Intent = IntentPriceQuestion
	case analysis.SelectedLevel > 0 && looksLikePackageSelection(normalized):
		analysis.Intent = IntentPackageSelection
	case deferRequest:
		analysis.Intent = IntentDefer
	case readySignal || questionnaireIntent:
		analysis.Intent = IntentReadyToOrder
	case containsObjection(text):
		analysis.Intent = IntentObjection
	case analysis.PreviousAIAds != nil:
		analysis.Intent = IntentAnswer
	case isRefusal(normalized):
		analysis.Intent = IntentRefusal
	case isAgreement(normalized):
		analysis.Intent = IntentAgreement
	case isGreeting(normalized):
		analysis.Intent = IntentGreeting
	case analysis.HasBusinessSignal():
		analysis.Intent = IntentAnswer
	default:
		analysis.Intent = IntentOther
	}
	if analysis.Intent == IntentPackageSelection && !questionnaireIntent {
		analysis.WantsQuestionnaire = false
	}

	updated := current
	updated.ApplyAnalysis(analysis)
	analysis.MissingFields = updated.MissingCoreFields()
	return analysis
}

func (a CustomerAnalysis) HasBusinessSignal() bool {
	return a.Niche != nil ||
		a.City != nil ||
		a.Goal != nil ||
		len(a.Platforms) > 0 ||
		a.Deadline != nil ||
		a.PreviousAIAds != nil ||
		a.Budget != nil ||
		a.VideoQuantity != nil ||
		a.VoicePreference != nil ||
		a.CopyrightConcern != nil ||
		a.CampaignContext != nil ||
		a.HookIdea != nil ||
		len(a.LikedFormats) > 0 ||
		a.TargetAudience != nil ||
		a.ProductOrService != nil ||
		a.StrongSide != nil ||
		a.Offer != nil ||
		a.SelectedLevel > 0 ||
		a.PackageInterest != nil ||
		a.BusinessLink != nil ||
		len(a.ReferenceLinks) > 0 ||
		a.WantsQuestionnaire
}

func (s *LeadState) ApplyAnalysis(analysis CustomerAnalysis) {
	updateQualificationFields := analysis.Intent != IntentBriefAnswer
	// A valid stored niche/goal is only replaced by another valid value, so a
	// greeting, question or unclear fragment can never destroy good data.
	if (updateQualificationFields || !isValidNiche(s.Niche)) && analysis.Niche != nil && isValidNiche(*analysis.Niche) && !isNonNicheCandidateText(normalizeForAnalysis(*analysis.Niche)) {
		s.Niche = strings.TrimSpace(*analysis.Niche)
	}
	if (updateQualificationFields || strings.TrimSpace(s.City) == "") && analysis.City != nil && strings.TrimSpace(*analysis.City) != "" {
		s.City = strings.TrimSpace(*analysis.City)
	}
	if (updateQualificationFields || !isValidGoal(s.Goal)) && analysis.Goal != nil && isValidGoal(*analysis.Goal) {
		s.Goal = strings.TrimSpace(*analysis.Goal)
	}
	if (updateQualificationFields || len(s.Platforms) == 0) && len(analysis.Platforms) > 0 {
		s.Platforms = mergePlatforms(s.Platforms, analysis.Platforms)
		s.Platform = strings.Join(s.Platforms, ", ")
	}
	if (updateQualificationFields || !isValidDeadline(s.Deadline)) && analysis.Deadline != nil && strings.TrimSpace(*analysis.Deadline) != "" {
		s.Deadline = strings.TrimSpace(*analysis.Deadline)
	}
	if analysis.PreviousAIAds != nil {
		value := *analysis.PreviousAIAds
		s.PreviousAIAds = &value
		s.AIExperience = aiExperienceLabel(value)
	}
	if analysis.AIExperience != nil && strings.TrimSpace(*analysis.AIExperience) != "" {
		s.AIExperience = strings.TrimSpace(*analysis.AIExperience)
	}
	if analysis.Budget != nil && strings.TrimSpace(*analysis.Budget) != "" {
		s.Budget = strings.TrimSpace(*analysis.Budget)
	}
	if analysis.VideoQuantity != nil && strings.TrimSpace(*analysis.VideoQuantity) != "" {
		s.VideoQuantity = strings.TrimSpace(*analysis.VideoQuantity)
	}
	if analysis.VoicePreference != nil && strings.TrimSpace(*analysis.VoicePreference) != "" {
		s.VoicePreference = strings.TrimSpace(*analysis.VoicePreference)
	}
	if analysis.CopyrightConcern != nil && strings.TrimSpace(*analysis.CopyrightConcern) != "" {
		s.CopyrightConcern = strings.TrimSpace(*analysis.CopyrightConcern)
	}
	if analysis.CampaignContext != nil && strings.TrimSpace(*analysis.CampaignContext) != "" {
		s.CampaignContext = strings.TrimSpace(*analysis.CampaignContext)
	}
	if analysis.HookIdea != nil && strings.TrimSpace(*analysis.HookIdea) != "" {
		s.HookIdea = strings.TrimSpace(*analysis.HookIdea)
	}
	for _, liked := range analysis.LikedFormats {
		s.LikedFormats = appendUniqueString(s.LikedFormats, liked)
	}
	if analysis.Intent == IntentQuantityDiscountQuestion {
		s.QuantityDiscountInterest = true
	}
	if analysis.TargetAudience != nil && strings.TrimSpace(*analysis.TargetAudience) != "" {
		s.TargetAudience = strings.TrimSpace(*analysis.TargetAudience)
	}
	if analysis.ProductOrService != nil && strings.TrimSpace(*analysis.ProductOrService) != "" {
		s.ProductOrService = strings.TrimSpace(*analysis.ProductOrService)
		if !isValidNiche(s.Niche) && isValidNiche(s.ProductOrService) && !isNonNicheCandidateText(normalizeForAnalysis(s.ProductOrService)) {
			s.Niche = s.ProductOrService
		}
	}
	if analysis.StrongSide != nil && strings.TrimSpace(*analysis.StrongSide) != "" {
		s.StrongSide = strings.TrimSpace(*analysis.StrongSide)
	}
	if analysis.Offer != nil && strings.TrimSpace(*analysis.Offer) != "" {
		s.Offer = strings.TrimSpace(*analysis.Offer)
	}
	if analysis.BusinessLink != nil && strings.TrimSpace(*analysis.BusinessLink) != "" {
		s.WebsiteOrInstagram = strings.TrimSpace(*analysis.BusinessLink)
		s.Notes = appendBriefText(s.Notes, "Ссылка: "+s.WebsiteOrInstagram)
	}
	for _, link := range analysis.ReferenceLinks {
		link = strings.TrimSpace(link)
		if link == "" {
			continue
		}
		s.ReferenceLinks = appendUniqueString(s.ReferenceLinks, link)
		s.Notes = appendBriefText(s.Notes, "Референс: "+link)
	}
	if updateQualificationFields && analysis.PackageInterest != nil && isValidPackageInterest(*analysis.PackageInterest) {
		s.SelectedPackage = normalizePackageInterest(*analysis.PackageInterest)
		s.LeadStatus = LeadStatusHot
	}
	if updateQualificationFields && (analysis.Intent == IntentPackageSelection || analysis.Intent == IntentHumanRequest) && analysis.SelectedLevel > 0 {
		s.SelectedPackage = packageKey(analysis.SelectedLevel)
		s.LeadStatus = LeadStatusHot
	}
	if analysis.WantsQuestionnaire || analysis.Intent == IntentReadyToOrder {
		s.WantsQuestionnaire = true
	}
	if analysis.Intent == IntentPriceQuestion {
		s.PriceInterest = true
	}
	if analysis.Intent == IntentBriefAnswer {
		s.BriefCompleted = true
		s.ContactBriefReady = true
		s.LeadStatus = LeadStatusHandoffRequired
	}
	if analysis.Intent == IntentHumanRequest {
		s.WantsQuestionnaire = true
		if !isValidPackageInterest(s.SelectedPackage) {
			s.SelectedPackage = packageNeedsManagerRecommendation
		}
		if strings.TrimSpace(s.LeadStatus) == "" || normalizeLeadStatus(s.LeadStatus) == LeadStatusNew {
			s.LeadStatus = LeadStatusHot
		}
	}
	if analysis.Intent == IntentRefusal {
		s.LeadStatus = LeadStatusClosed
	}
	if analysis.Intent == IntentMute {
		s.LeadStatus = LeadStatusMuted
	}
}

// MissingCoreFields returns the fields required for the first qualification
// stage. The launch deadline is intentionally NOT part of it: the bot must not
// ask about timing unless the customer brings it up, so only niche and goal
// are required. A volunteered deadline is still stored when extracted.
func (s LeadState) MissingCoreFields() []string {
	missing := make([]string, 0, 2)
	if !isValidNiche(s.Niche) {
		missing = append(missing, fieldNiche)
	}
	if !isValidGoal(s.Goal) {
		missing = append(missing, fieldGoal)
	}
	return missing
}

func (s LeadState) HasCoreFields() bool {
	return len(s.MissingCoreFields()) == 0
}

func (s LeadState) PromptJSON(stage string) string {
	summary := struct {
		HasBeenGreeted           bool     `json:"has_been_greeted"`
		Niche                    *string  `json:"niche"`
		City                     *string  `json:"city"`
		Goal                     *string  `json:"goal"`
		Platform                 *string  `json:"platform"`
		Platforms                []string `json:"platforms"`
		Deadline                 *string  `json:"deadline"`
		PreviousAIAds            *bool    `json:"previous_ai_ads"`
		AIExperience             *string  `json:"ai_experience"`
		Budget                   *string  `json:"budget"`
		PriceInterest            bool     `json:"price_interest"`
		QuantityDiscountInterest bool     `json:"quantity_discount_interest"`
		VideoQuantity            *string  `json:"video_quantity"`
		VoicePreference          *string  `json:"voice_preference"`
		CopyrightConcern         *string  `json:"copyright_concern"`
		CampaignContext          *string  `json:"campaign_context"`
		HookIdea                 *string  `json:"hook_idea"`
		LikedFormats             []string `json:"liked_formats"`
		SelectedPackage          *string  `json:"selected_package"`
		WantsQuestionnaire       bool     `json:"wants_questionnaire"`
		ClientName               *string  `json:"client_name"`
		TargetAudience           *string  `json:"target_audience"`
		ProductOrService         *string  `json:"product_or_service"`
		StrongSide               *string  `json:"strong_side"`
		Offer                    *string  `json:"offer"`
		WebsiteOrInstagram       *string  `json:"website_or_instagram"`
		ReferenceLinks           []string `json:"reference_links"`
		Notes                    *string  `json:"notes"`
		FreeText                 *string  `json:"free_text"`
		PortfolioSent            bool     `json:"portfolio_sent"`
		OfferSent                bool     `json:"offer_sent"`
		BriefRequested           bool     `json:"brief_requested"`
		BriefCompleted           bool     `json:"brief_completed"`
		ContactBriefReady        bool     `json:"contact_brief_ready"`
		LeadStatus               string   `json:"lead_status"`
		Stage                    string   `json:"stage"`
	}{
		HasBeenGreeted:           s.HasBeenGreeted,
		Niche:                    nullableString(s.Niche),
		City:                     nullableString(s.City),
		Goal:                     nullableString(s.Goal),
		Platform:                 nullableString(s.platformSummary()),
		Platforms:                append([]string(nil), s.Platforms...),
		Deadline:                 nullableString(s.Deadline),
		PreviousAIAds:            s.PreviousAIAds,
		AIExperience:             nullableString(s.AIExperience),
		Budget:                   nullableString(s.Budget),
		PriceInterest:            s.PriceInterest,
		QuantityDiscountInterest: s.QuantityDiscountInterest,
		VideoQuantity:            nullableString(s.VideoQuantity),
		VoicePreference:          nullableString(s.VoicePreference),
		CopyrightConcern:         nullableString(s.CopyrightConcern),
		CampaignContext:          nullableString(s.CampaignContext),
		HookIdea:                 nullableString(s.HookIdea),
		LikedFormats:             append([]string(nil), s.LikedFormats...),
		SelectedPackage:          nullableString(s.SelectedPackage),
		WantsQuestionnaire:       s.WantsQuestionnaire,
		ClientName:               nullableString(s.ClientName),
		TargetAudience:           nullableString(s.TargetAudience),
		ProductOrService:         nullableString(s.ProductOrService),
		StrongSide:               nullableString(s.StrongSide),
		Offer:                    nullableString(s.Offer),
		WebsiteOrInstagram:       nullableString(s.WebsiteOrInstagram),
		ReferenceLinks:           append([]string(nil), s.ReferenceLinks...),
		Notes:                    nullableString(s.Notes),
		FreeText:                 nullableString(s.FreeText),
		PortfolioSent:            s.PortfolioSent,
		OfferSent:                s.OfferSent,
		BriefRequested:           s.BriefRequested,
		BriefCompleted:           s.BriefCompleted,
		ContactBriefReady:        s.ContactBriefReady,
		LeadStatus:               normalizeLeadStatus(s.LeadStatus),
		Stage:                    strings.TrimSpace(stage),
	}
	if summary.Platforms == nil {
		summary.Platforms = []string{}
	}
	if summary.ReferenceLinks == nil {
		summary.ReferenceLinks = []string{}
	}
	if summary.LikedFormats == nil {
		summary.LikedFormats = []string{}
	}
	if summary.LeadStatus == "" {
		summary.LeadStatus = LeadStatusNew
	}
	if summary.Stage == "" {
		summary.Stage = "qualification"
	}

	data, err := json.Marshal(summary)
	if err != nil {
		return `{"stage":"qualification"}`
	}
	return string(data)
}

func (s LeadState) platformSummary() string {
	if strings.TrimSpace(s.Platform) != "" {
		return strings.TrimSpace(s.Platform)
	}
	return strings.Join(s.Platforms, ", ")
}

func normalizeLeadStatus(status string) string {
	status = strings.TrimSpace(status)
	switch status {
	case LeadStatusNeutral, LeadStatusNew, LeadStatusWarm, LeadStatusHot, LeadStatusClosed, LeadStatusHandoffRequired, LeadStatusMuted:
		return status
	default:
		return ""
	}
}

func (a CustomerAnalysis) JSON() string {
	if a.Platforms == nil {
		a.Platforms = []string{}
	}
	if a.MissingFields == nil {
		a.MissingFields = []string{}
	}
	data, err := json.Marshal(a)
	if err != nil {
		return `{"intent":"other","missing_fields":[]}`
	}
	return string(data)
}

func extractNiche(text string, current LeadState) string {
	normalized := normalizeForAnalysis(text)
	if extractVideoQuantity(text, current) != "" {
		return ""
	}
	if isURLOnlyMessage(text) {
		return ""
	}
	switch {
	case strings.Contains(normalized, "стирка ковров"):
		return "стирка ковров"
	case strings.Contains(normalized, "химчистка ковров"):
		return "химчистка ковров"
	case strings.Contains(normalized, "чистка ковров"):
		return "чистка ковров"
	case strings.Contains(normalized, "копирайтинг"):
		return "копирайтинг"
	}
	if value := extractPrefixedValue(normalized, []string{
		"ниша", "сфера", "бизнес", "направление", "сала", "niche", "industry",
	}, []string{
		"цель", "максат", "goal", "срок", "сроки", "мерзим", "deadline", "площад", "platform",
	}); value != "" {
		return normalizeNiche(cleanNicheSource(value))
	}

	if value := nicheFromProductEnumeration(text); value != "" {
		return value
	}
	if value := productFromSalesLaunchText(normalized); value != "" {
		return value
	}

	cleaned := cleanNicheSource(normalized)
	if value := knownNicheFromText(cleaned); value != "" {
		return value
	}

	// Questions, greetings, confirmations, timing words and case/price requests
	// must never fall through to the generic short-answer niche extraction.
	if isNonNicheCandidateText(normalized) || isNonNicheCandidateText(cleaned) {
		return ""
	}

	if hasGoalMarker(normalized) || hasDeadlineMarker(normalized) || len(extractPlatforms(text)) > 0 {
		return ""
	}

	if value := productNicheFromText(cleaned); value != "" {
		return value
	}

	if isLikelyShortAnswerFor(fieldNiche, current, cleaned) {
		return normalizeNiche(cleaned)
	}

	return ""
}

func extractCity(text string) string {
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return ""
	}
	cities := []struct {
		value    string
		variants []string
	}{
		{value: "Алматы", variants: []string{"алматы", "алмате", "алмата"}},
		{value: "Астана", variants: []string{"астана", "астане", "нур султан", "нурсултан"}},
		{value: "Шымкент", variants: []string{"шымкент", "шимкент", "шымкенте", "шимкенте"}},
		{value: "Караганда", variants: []string{"караганда", "караганде", "қарағанды"}},
		{value: "Актобе", variants: []string{"актобе", "ақтөбе"}},
		{value: "Атырау", variants: []string{"атырау"}},
		{value: "Актау", variants: []string{"актау", "ақтау"}},
	}
	for _, city := range cities {
		for _, variant := range city.variants {
			if containsWordOrPhrase(normalized, variant) {
				return city.value
			}
		}
	}
	return ""
}

func extractGoal(text string, current LeadState) string {
	normalized := normalizeForAnalysis(text)
	if extractVideoQuantity(text, current) != "" {
		return ""
	}
	if asksWhichFormatWorksBest(normalized) {
		return ""
	}
	withoutLinks := stripURLLikeText(text)
	if strings.TrimSpace(withoutLinks) == "" {
		return ""
	}
	normalizedWithoutLinks := normalizeForAnalysis(withoutLinks)
	if value := extractPrefixedValue(normalized, []string{
		"цель", "максат", "goal", "задача", "нужно", "керек",
	}, []string{
		"ниша", "сфера", "сала", "срок", "сроки", "мерзим", "deadline", "площад", "platform",
	}); value != "" {
		if goal := normalizeGoal(value); goal != "" {
			return goal
		}
	}
	// "Основные клиенты строительные компании, частники" describes the
	// audience; the bare "клиент" keyword must not turn it into a goal.
	if isAudienceDescriptionText(normalized) && !hasExplicitGoalSignal(normalized) {
		return ""
	}
	if hasExplicitGoalSignal(normalizedWithoutLinks) {
		if goal := normalizeGoal(normalizedWithoutLinks); goal != "" {
			return goal
		}
	}
	if !containsURLLikeText(text) {
		if goal := normalizeGoal(normalized); goal != "" {
			return goal
		}
	}
	if goal := launchSalesGoal(normalizedWithoutLinks); goal != "" {
		return goal
	}
	if isLikelyShortAnswerFor(fieldGoal, current, normalizedWithoutLinks) {
		return normalizeGoal(normalizedWithoutLinks)
	}
	return ""
}

func containsURLLikeText(text string) bool {
	return urlLikePattern.FindString(text) != ""
}

func stripURLLikeText(text string) string {
	cleaned := urlLikePattern.ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(cleaned), " ")
}

func isURLOnlyMessage(text string) bool {
	withoutLinks := stripURLLikeText(text)
	return strings.TrimSpace(withoutLinks) == "" && containsURLLikeText(text)
}

func launchSalesGoal(normalized string) string {
	if containsAny(normalized, []string{"запуск продаж", "открытие продаж", "откроем продажи", "открываем продажи", "старт продаж"}) {
		return "запуск продаж"
	}
	return ""
}

// isAudienceDescriptionText detects "основные клиенты ..." style statements
// that describe who the customers are rather than what the video goal is.
func isAudienceDescriptionText(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" {
		return false
	}
	if containsAny(normalized, []string{
		"основные клиенты", "основной клиент", "наши клиенты", "наш клиент",
		"основные покупатели", "клиенты в основном", "покупатели в основном",
		"целевая аудитория", "основная аудитория", "негизги клиенттер",
		"main clients", "our clients", "our customers", "target audience",
	}) {
		return true
	}
	return strings.HasPrefix(normalized, "клиенты ") || strings.HasPrefix(normalized, "аудитория ")
}

// hasExplicitGoalSignal reports whether the text names a video/business goal
// explicitly, so an audience-looking sentence can still carry a goal.
func hasExplicitGoalSignal(normalized string) bool {
	return containsAny(normalized, []string{
		"цель", "максат", "мақсат", "goal", "хочу", "хотим", "нужны", "нужно", "керек",
		"привлеч", "заяв", "лид", "продаж", "узнаваем", "охват", "трафик", "подпис", "запуск",
	})
}

// audienceFromDescriptionLine extracts the audience value from an audience
// statement line ("Основные клиенты строительные компании, частники!").
func audienceFromDescriptionLine(line string) string {
	normalized := normalizeForAnalysis(line)
	for _, marker := range []string{
		"основные клиенты", "основной клиент", "наши клиенты", "наш клиент",
		"основные покупатели", "клиенты в основном", "покупатели в основном",
		"целевая аудитория", "основная аудитория", "негизги клиенттер",
		"main clients", "our clients", "our customers", "target audience",
		"клиенты", "аудитория",
	} {
		normalized = strings.ReplaceAll(normalized, marker, " ")
	}
	normalized = strings.Trim(strings.Join(strings.Fields(normalized), " "), " -—:;,.!?")
	if meaningfulRuneCount(normalized) < 3 {
		return ""
	}
	return normalized
}

func audienceLikeLine(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" {
		return false
	}
	return containsAny(normalized, []string{
		"крупные компании", "крупных компаний", "компании в сфере", "компании из сферы",
		"производств", "промышлен", "добыч", "завод", "заводы", "корпоратив",
		"b2b клиенты", "b2b клиент", "б2б клиенты", "б2б клиент", "для бизнеса",
		"строительные компании", "компании строитель", "частники",
	})
}

func shouldTreatLineAsAudience(normalized string, current LeadState) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" {
		return false
	}
	if isAudienceDescriptionText(normalized) {
		return true
	}
	if !audienceLikeLine(normalized) {
		return false
	}
	if knownNicheFromText(normalized) != "" || isLikelyShortAnswerFor(fieldNiche, current, normalized) {
		return false
	}
	words := strings.Fields(normalized)
	return len(words) > 3 || containsAny(normalized, []string{
		"клиент", "аудитор", "компании", "частники", "b2b", "б2б", "корпоратив",
	})
}

func cleanAudienceValue(line string) string {
	value := audienceFromDescriptionLine(line)
	if value == "" {
		value = normalizeForAnalysis(line)
	}
	value = strings.Trim(value, " -—:;,.!?")
	return strings.Join(strings.Fields(value), " ")
}

func briefStrengthLine(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" {
		return false
	}
	if containsAny(normalized, []string{
		"сильн", "преимущ", "ценность", "отлич", "уникаль", "качество",
		"гарант", "быстро", "на заказ", "опыт", "экспертиз", "premium", "преми",
	}) {
		return true
	}
	return strings.HasPrefix(normalized, "мы ") && containsAny(normalized, []string{"умеем", "делаем", "создаем", "создаём"})
}

func cleanBriefFactValue(value string) string {
	value = normalizeForAnalysis(value)
	for _, marker := range []string{
		"сильная сторона", "преимущество", "преимущества", "главная ценность",
		"ценность", "оффер", "офер", "акция", "скидка", "промо",
	} {
		value = strings.ReplaceAll(value, marker, " ")
	}
	value = strings.Trim(value, " -—:;,.!?")
	return strings.Join(strings.Fields(value), " ")
}

func shortServiceLine(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" || strings.Contains(normalized, "?") {
		return false
	}
	if normalizeGoal(normalized) != "" || normalizeDeadline(normalized) != "" {
		return false
	}
	words := strings.Fields(normalized)
	if len(words) == 0 || len(words) > 6 {
		return false
	}
	return containsAny(normalized, []string{"услуг", "услуга", "сервис", "service", "services", "b2b", "b2c"})
}

func extractPlatforms(text string) []string {
	normalized := normalizeForAnalysis(text)
	platforms := make([]string, 0, 4)

	add := func(platform string) {
		platforms = mergePlatforms(platforms, []string{platform})
	}

	if containsAny(normalized, []string{"instagram", "insta", "инстаграм", "инста", "инст ", "инсту"}) {
		add("Instagram")
	}
	if containsAny(normalized, []string{"tiktok", "tik tok", "тик ток", "тикток"}) {
		add("TikTok")
	}
	if containsAny(normalized, []string{"reels", "рилс", "риелс"}) {
		add("Reels")
	}
	if containsAny(normalized, []string{"stories", "story", "сторис", "стори"}) {
		add("Stories")
	}
	if containsAny(normalized, []string{"таргет", "target", "targeted ads", "по рекламе", "для рекламы", "рекламага", "жарнама"}) {
		add("таргетированная реклама")
	}
	if containsAny(normalized, []string{"youtube", "ютуб", "shorts", "шортс"}) {
		add("YouTube")
	}
	if containsAny(normalized, []string{"facebook", "фейсбук", "meta ads"}) {
		add("Facebook")
	}
	if containsAny(normalized, []string{"whatsapp", "what's app", "ватсап", "уатсап", "вацап"}) {
		add("WhatsApp")
	}
	if containsAny(normalized, []string{"сайт", "website", "landing", "лендинг", "посадоч"}) {
		add("сайт")
	}

	return platforms
}

func extractDeadline(text string, current LeadState) string {
	normalized := normalizeForAnalysis(text)
	if isLaunchContextTiming(normalized) {
		return ""
	}
	if value := extractPrefixedValue(normalized, []string{
		"срок", "сроки", "мерзим", "deadline", "тайминг", "кашан",
	}, []string{
		"ниша", "сфера", "сала", "цель", "максат", "goal", "площад", "platform",
	}); value != "" {
		if deadline := normalizeDeadline(value); deadline != "" {
			return deadline
		}
	}
	if deadline := normalizeDeadline(normalized); deadline != "" {
		return deadline
	}
	if isLikelyShortAnswerFor(fieldDeadline, current, normalized) {
		return normalizeDeadline(normalized)
	}
	return ""
}

func isLaunchContextTiming(normalized string) bool {
	if !containsAny(normalized, []string{"завтра", "сегодня", "tomorrow", "today"}) {
		return false
	}
	if containsAny(normalized, []string{"нужно к", "надо к", "успеть к", "сдать к", "срок", "deadline", "за 48", "за два", "за три", "за 2", "за 3"}) {
		return false
	}
	return containsAny(normalized, []string{
		"откроем продажи", "открываем продажи", "открытие продаж", "запуск продаж", "старт продаж",
		"откроем запуск", "открываем запуск", "запуск рекламы", "запускаем рекламу", "старт рекламы",
	})
}

type numberedQualificationAnswers struct {
	found    bool
	niche    string
	goal     string
	deadline string
}

func extractNumberedQualificationAnswers(text string) numberedQualificationAnswers {
	matches := numberedQualificationLinePattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return numberedQualificationAnswers{}
	}
	answers := numberedQualificationAnswers{}
	seen := make(map[string]bool, 3)
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		number := strings.TrimSpace(match[1])
		if seen[number] {
			continue
		}
		value := cleanNumberedQualificationValue(match[2])
		if value == "" {
			continue
		}
		seen[number] = true
		switch number {
		case "1":
			answers.niche = value
		case "2":
			answers.goal = value
		case "3":
			answers.deadline = value
		}
	}
	answers.found = answers.niche != "" || answers.goal != "" || answers.deadline != ""
	return answers
}

func cleanNumberedQualificationValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, " \t\r\n-—:;,.!?")
	return strings.Join(strings.Fields(value), " ")
}

func extractPreviousAIAds(text string, current LeadState) *bool {
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return nil
	}

	expectsAnswer := current.HasCoreFields() && current.PreviousAIAds == nil
	mentionsUse := containsAny(normalized, []string{
		"использ", "тест", "попроб", "пробовал", "пробовали", "қолдан", "сына", "used", "tried", "try",
	})
	negative := containsAny(normalized, []string{
		"нет", "не использ", "не проб", "никогда", "первый раз", "впервые", "жоқ", "қолданба", "бірінші рет", "алғаш", "no", "never", "not yet", "not used", "first time",
	})
	if negative && (expectsAnswer || mentionsUse) {
		value := false
		return &value
	}

	positive := containsAny(normalized, []string{
		"да", "использ", "пробовал", "пробовали", "иә", "қолдандым", "болды", "yes", "used", "already",
	})
	if positive && (expectsAnswer || mentionsUse) {
		value := true
		return &value
	}

	return nil
}

func aiExperienceLabel(used bool) string {
	if used {
		return "used_before"
	}
	return "first_time"
}

func extractBudget(text string) string {
	normalized := normalizeForAnalysis(text)
	compact := compactNumericText(normalized)
	switch {
	case strings.Contains(compact, "35000") || priceShortcutSelected(normalized, "35"):
		return "35 000 тг"
	case strings.Contains(compact, "50000") || priceShortcutSelected(normalized, "50"):
		return "50 000 тг"
	case strings.Contains(compact, "75000") || priceShortcutSelected(normalized, "75"):
		return "от 75 000 тг"
	default:
		return ""
	}
}

func extractTargetAudience(text string) string {
	normalized := normalizeForAnalysis(text)
	if value := extractPrefixedValue(normalized, []string{
		"аудитория", "ца", "target audience", "клиенты", "клиенттер", "клиент", "client", "clients",
	}, []string{
		"ниша", "сфера", "сала", "цель", "максат", "goal", "срок", "сроки", "мерзим", "deadline", "площад", "platform",
	}); value != "" {
		if isPositioningGoalText(value) {
			return ""
		}
		return strings.Trim(value, " -—:;,.!?")
	}
	for _, line := range meaningfulMessageLines(text) {
		if audienceLikeLine(normalizeForAnalysis(line)) {
			return cleanAudienceValue(line)
		}
	}
	if strings.Contains(normalized, "женщины 25") {
		return "женщины 25+"
	}
	if strings.Contains(normalized, "мужчины 25") {
		return "мужчины 25+"
	}
	return ""
}

func extractStrongSide(text string) string {
	for _, line := range meaningfulMessageLines(text) {
		normalized := normalizeForAnalysis(line)
		if normalized == "" || strings.Contains(normalized, "?") {
			continue
		}
		if value := extractPrefixedValue(normalized, []string{
			"сильная сторона", "сильная сторона у нас", "преимущество", "преимущества",
			"главная ценность", "ценность", "usp", "strong side", "advantage",
		}, []string{
			"клиент", "клиенты", "аудитория", "ца", "оффер", "офер", "акция", "скидка",
			"сайт", "instagram", "цель", "goal", "ниша", "сфера",
		}); value != "" {
			return cleanBriefFactValue(value)
		}
		if briefStrengthLine(normalized) {
			return cleanBriefFactValue(line)
		}
	}
	return ""
}

func extractOffer(text string) string {
	for _, line := range meaningfulMessageLines(text) {
		normalized := normalizeForAnalysis(line)
		if normalized == "" || strings.Contains(normalized, "?") {
			continue
		}
		if isNoOfferBriefAnswer(normalized) {
			return "нет текущего оффера"
		}
		if value := extractPrefixedValue(normalized, []string{
			"оффер", "офер", "акция", "скидка", "промо", "promo", "offer", "discount",
		}, []string{
			"клиент", "клиенты", "аудитория", "ца", "сайт", "instagram", "цель", "goal", "ниша", "сфера",
		}); value != "" {
			return cleanBriefFactValue(value)
		}
		if containsAny(normalized, []string{"скидк", "акци", "бонус", "подар", "рассроч"}) {
			return cleanBriefFactValue(line)
		}
	}
	return ""
}

func extractBusinessLink(text string) string {
	links := extractReferenceLinks(text)
	if len(links) > 0 {
		return links[0]
	}
	return ""
}

func extractReferenceLinks(text string) []string {
	normalized := strings.TrimSpace(text)
	if normalized == "" {
		return nil
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bhttps?://[^\s<>"']+`),
		regexp.MustCompile(`(?i)\bwww\.[^\s<>"']+`),
		regexp.MustCompile(`(?i)\b(?:instagram|instagr\.am|tiktok|wa|taplink|linktr|2gis)\.[^\s<>"']+`),
		regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9-]*(?:\.[a-z0-9][a-z0-9-]*)+(?:/[^\s<>"']*)?`),
		regexp.MustCompile(`(?i)(?:^|\s)@[a-z0-9_.]{3,}`),
	}
	links := make([]string, 0, 2)
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllString(normalized, -1) {
			link := strings.Trim(strings.TrimSpace(match), ".,;!?)(")
			if link == "" {
				continue
			}
			links = appendUniqueString(links, link)
		}
	}
	return links
}

func extractProductOrService(text string) string {
	for _, line := range meaningfulMessageLines(text) {
		normalized := normalizeForAnalysis(line)
		if shouldTreatLineAsAudience(normalized, LeadState{}) {
			continue
		}
		if value := productFromSalesLaunchText(normalized); value != "" {
			return value
		}
		if value := nicheFromProductEnumeration(line); value != "" {
			return value
		}
	}
	return ""
}

type messyQualificationFacts struct {
	niche            string
	goal             string
	deadline         string
	productOrService string
	audience         string
	strongSide       string
	offer            string
	platforms        []string
}

func (f messyQualificationFacts) hasAny() bool {
	return f.niche != "" || f.goal != "" || f.deadline != "" || f.productOrService != "" || f.audience != "" || f.strongSide != "" || f.offer != "" || len(f.platforms) > 0
}

func extractMessyQualificationFacts(text string, current LeadState) messyQualificationFacts {
	lines := meaningfulMessageLines(text)
	if len(lines) == 0 {
		return messyQualificationFacts{}
	}

	facts := messyQualificationFacts{}
	lineCurrent := current
	for _, line := range lines {
		normalized := normalizeForAnalysis(line)
		if normalized == "" || isGreeting(normalized) {
			continue
		}
		if link := extractBusinessLink(line); link != "" {
			continue
		}
		if platform := shortPlatformContext(normalized); platform != "" {
			facts.platforms = mergePlatforms(facts.platforms, []string{platform})
			continue
		}
		if len(extractPlatforms(line)) > 0 {
			facts.platforms = mergePlatforms(facts.platforms, extractPlatforms(line))
			continue
		}
		if strings.Contains(normalized, "?") {
			continue
		}
		if facts.strongSide == "" && briefStrengthLine(normalized) {
			facts.strongSide = cleanBriefFactValue(line)
			continue
		}
		if facts.offer == "" {
			if isNoOfferBriefAnswer(normalized) {
				facts.offer = "нет текущего оффера"
				continue
			}
			if containsAny(normalized, []string{"оффер", "офер", "акци", "скид", "бонус", "подар"}) {
				facts.offer = cleanBriefFactValue(line)
				continue
			}
		}
		if extractVideoQuantity(line, lineCurrent) != "" {
			continue
		}
		// Audience statements ("Основные клиенты строительные компании,
		// частники") are stored as the audience and never as a goal/niche.
		if shouldTreatLineAsAudience(normalized, lineCurrent) && !hasExplicitGoalSignal(normalized) {
			if facts.audience == "" {
				facts.audience = cleanAudienceValue(line)
			}
			continue
		}
		if facts.niche == "" {
			if product := productFromSalesLaunchText(normalized); product != "" {
				facts.niche = product
				facts.productOrService = product
				lineCurrent.Niche = product
				continue
			}
			if niche := shortProductOrNicheLine(line, lineCurrent); niche != "" {
				facts.niche = niche
				facts.productOrService = niche
				lineCurrent.Niche = niche
				continue
			}
		}
		if facts.deadline == "" && !isLaunchContextTiming(normalized) {
			if isValidDeadline(normalized) && len(strings.Fields(normalized)) <= 4 {
				facts.deadline = normalized
				lineCurrent.Deadline = normalized
				continue
			}
			if deadline := normalizeDeadline(normalized); deadline != "" {
				facts.deadline = deadline
				lineCurrent.Deadline = deadline
				continue
			}
		}
		if facts.goal == "" {
			if isValidGoal(normalized) && len(strings.Fields(normalized)) <= 3 && !looksLikeOnlyPlatformContext(normalized) {
				facts.goal = normalized
				lineCurrent.Goal = normalized
				continue
			}
			if goal := normalizeGoal(normalized); goal != "" && !looksLikeOnlyPlatformContext(normalized) {
				facts.goal = goal
				lineCurrent.Goal = goal
				continue
			}
		}
	}

	if facts.niche == "" && asksForFoodExamples(normalizeForAnalysis(text)) {
		facts.niche = "еда"
		facts.productOrService = "еда"
	}
	return facts
}

func productFromSalesLaunchText(normalized string) string {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" || strings.Contains(normalized, "?") {
		return ""
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:продажи|сату|sales)\s+(?:на|по|для)?\s*(.+?)(?:\s*\(|,|\.|$)`),
		regexp.MustCompile(`(?:продаем|продаём|продаю|сатамыз|sell|selling)\s+(.+?)(?:,|\.|$)`),
	}
	for _, pattern := range patterns {
		match := pattern.FindStringSubmatch(normalized)
		if len(match) < 2 {
			continue
		}
		value := strings.Trim(match[1], " -—:;,.!?")
		value = strings.TrimPrefix(value, "на ")
		value = strings.TrimPrefix(value, "по ")
		value = strings.TrimPrefix(value, "для ")
		value = strings.ReplaceAll(value, " и на ", " и ")
		value = strings.ReplaceAll(value, " и по ", " и ")
		value = strings.Join(strings.Fields(value), " ")
		if meaningfulRuneCount(value) >= 3 && !isNonNicheCandidateText(value) && normalizeGoal(value) == "" {
			return normalizeNiche(value)
		}
	}
	return ""
}

func meaningfulMessageLines(text string) []string {
	rawLines := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	result := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		line = strings.Trim(line, " \t-—:;,.!")
		if line == "" {
			continue
		}
		result = append(result, line)
	}
	return result
}

func shortPlatformContext(normalized string) string {
	clean := strings.Trim(normalized, " .,!?:;")
	switch clean {
	case "по рекламе", "для рекламы", "в рекламу", "реклама", "жарнама", "ads", "advertising":
		return "реклама"
	default:
		return ""
	}
}

func looksLikeOnlyPlatformContext(normalized string) bool {
	return shortPlatformContext(normalized) != ""
}

func shortProductOrNicheLine(line string, current LeadState) string {
	normalized := normalizeForAnalysis(line)
	if normalized == "" ||
		strings.Contains(normalized, "?") ||
		strings.Contains(normalized, "http") ||
		strings.Contains(normalized, "www") ||
		strings.Contains(normalized, "@") ||
		normalizeGoal(normalized) != "" ||
		normalizeDeadline(normalized) != "" ||
		shortPlatformContext(normalized) != "" ||
		len(extractPlatforms(line)) > 0 {
		return ""
	}
	if value := nicheFromProductEnumeration(line); value != "" {
		return value
	}
	if shortServiceLine(normalized) {
		return normalizeNiche(normalized)
	}
	if value := knownNicheFromText(normalized); value != "" {
		return value
	}
	if isNonNicheCandidateText(normalized) {
		return ""
	}
	words := strings.Fields(normalized)
	if len(words) == 0 || len(words) > 5 {
		return ""
	}
	if isLikelyShortAnswerFor(fieldNiche, current, normalized) || len(words) <= 3 {
		return normalizeNiche(normalized)
	}
	return ""
}

// nicheFromProductEnumeration recognises comma-separated product listings such
// as "Плиточные клея, шпаклёвка, штукатурка и т.д." and keeps the whole
// enumeration as the niche/products text.
func nicheFromProductEnumeration(line string) string {
	normalized := normalizeForAnalysis(line)
	if normalized == "" ||
		strings.Contains(normalized, "?") ||
		strings.Contains(normalized, "http") ||
		strings.Contains(normalized, "www") ||
		strings.Contains(normalized, "@") {
		return ""
	}
	if isAudienceDescriptionText(normalized) || normalizeGoal(normalized) != "" ||
		normalizeDeadline(normalized) != "" || len(extractPlatforms(line)) > 0 {
		return ""
	}
	parts := strings.Split(normalized, ",")
	if len(parts) < 2 {
		return ""
	}
	etcReplacer := strings.NewReplacer("и т.д", " ", "и т.п", " ", "и так далее", " ", "и тд", " ", "и тп", " ", "etc", " ")
	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(etcReplacer.Replace(part))
		part = strings.Trim(part, " -—:;,.!")
		if part == "" {
			continue
		}
		words := strings.Fields(part)
		if len(words) > 3 {
			return ""
		}
		if isNonNicheCandidateText(part) || normalizeGoal(part) != "" || normalizeDeadline(part) != "" {
			return ""
		}
		cleanParts = append(cleanParts, strings.Join(words, " "))
	}
	if len(cleanParts) < 2 {
		return ""
	}
	niche := strings.Join(cleanParts, ", ")
	if !isValidNiche(niche) {
		return ""
	}
	return niche
}

func isNonNicheShortReply(normalized string) bool {
	clean := strings.Trim(normalized, " .,!?:;")
	switch clean {
	case "вот этот", "вот эта", "этот", "эта", "это", "тот", "та", "да", "ок", "окей",
		"хорошо", "супер", "анкета", "анкету", "бриф", "заявка", "заявку", "давайте",
		"отправьте", "пришлите", "жду", "ага", "угу", "принято":
		return true
	default:
		return false
	}
}

func asksForFoodExamples(normalized string) bool {
	if normalized == "" {
		return false
	}
	asksVideoOrExample := containsAny(normalized, []string{
		"ролик", "ролики", "видео", "пример", "примеры", "мысал", "example", "examples", "video",
	})
	mentionsFood := containsAny(normalized, []string{
		"еда", "едой", "еды", "продукт", "фермер", "food", "farm product", "farm products",
	})
	return asksVideoOrExample && mentionsFood
}

func asksForMoreOptions(normalized string) bool {
	if normalized == "" {
		return false
	}
	return containsAny(normalized, []string{
		"варианты исполнения", "еще варианты", "ещё варианты", "другие варианты", "варианты подумаем",
		"варианты формата", "package options", "more options", "other options",
	})
}

func isFeasibilityQuestion(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" {
		return false
	}
	asksCanDo := containsAny(normalized, []string{
		"делаете", "сделаете", "можете", "сможете", "получится", "реально", "адаптировать",
		"can you", "could you", "possible",
	})
	referenceStyle := containsAny(normalized, []string{
		"примерно такое", "такое видео", "такой формат", "как тут", "как здесь", "как в видео",
		"похож", "повторить стиль", "стиль референса", "similar", "like this",
	})
	return asksCanDo && referenceStyle
}

func isConfusionText(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" {
		return false
	}
	return containsAny(normalized, []string{
		"суть не уловил", "суть не понял", "не уловил", "не понял", "не поняла",
		"что это значит", "объясните", "объясни", "не ясно", "непонятно",
		"i don't get", "dont get", "unclear", "what does it mean",
	})
}

func isVoiceQuestion(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" {
		return false
	}
	return containsAny(normalized, []string{
		"озвуч", "голос", "диктор", "voice", "voiceover", "voice over",
	}) && !isCopyrightQuestion(normalized)
}

func isCopyrightQuestion(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" {
		return false
	}
	mentionsRights := containsAny(normalized, []string{
		"авторск", "права", "без прав", "copyright", "legal", "закон",
	})
	mentionsPerson := containsAny(normalized, []string{
		"актер", "актёр", "актеров", "актёров", "вин дизель", "дизел", "селебр", "звезд", "публичн", "реальн",
		"actor", "celebrity", "public figure",
	})
	return mentionsRights || mentionsPerson && containsAny(normalized, []string{"голос", "образ", "лицо", "акт"})
}

func detectLikedFormats(normalized string) []string {
	normalized = strings.Trim(normalizeForAnalysis(normalized), " .,!?:;")
	switch normalized {
	case "оба", "оба хороши", "оба подходят", "две хороши", "два хороши", "оба норм", "both", "both are good":
		return []string{"both"}
	case "первый", "первый хорош", "1", "номер 1", "first":
		return []string{"first"}
	case "второй", "второй хорош", "2", "номер 2", "second":
		return []string{"second"}
	case "третий", "третий хорош", "3", "номер 3", "third":
		return []string{"third"}
	default:
		return nil
	}
}

func extractVoicePreference(text string) string {
	normalized := normalizeForAnalysis(text)
	if normalized == "" || !containsAny(normalized, []string{"голос", "озвуч", "диктор", "voice"}) {
		return ""
	}
	parts := make([]string, 0, 4)
	for _, marker := range []string{"низк", "глубок", "уверенн", "кинематограф", "премиальн", "энергичн", "мужск", "женск", "спокойн"} {
		if strings.Contains(normalized, marker) {
			parts = append(parts, marker)
		}
	}
	if containsAny(normalized, []string{"вин дизел", "вин дизель", "vin diesel"}) {
		parts = append(parts, "низкий кинематографичный голос")
	}
	if len(parts) == 0 && isVoiceQuestion(normalized) {
		return "интерес к озвучке/выбору голоса"
	}
	return strings.Join(parts, ", ")
}

func extractCopyrightConcern(text string) string {
	normalized := normalizeForAnalysis(text)
	if isCopyrightQuestion(normalized) {
		return strings.TrimSpace(text)
	}
	return ""
}

func extractCampaignContext(text string) string {
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return ""
	}
	if containsAny(normalized, []string{"блогер", "розыгрыш", "квартира", "запуск продаж", "открытие продаж"}) {
		return strings.TrimSpace(text)
	}
	return ""
}

func extractHookIdea(text string) string {
	normalized := normalizeForAnalysis(text)
	if !containsAny(normalized, []string{"хук", "hook"}) {
		return ""
	}
	for _, line := range meaningfulMessageLines(text) {
		lineNormalized := normalizeForAnalysis(line)
		if containsAny(lineNormalized, []string{"хук", "hook"}) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func containsPriceQuestion(normalized string) bool {
	return containsAny(normalized, []string{
		"цена", "стоимость", "сколько", "прайс", "тариф", "қанша", "баға", "price", "cost",
	})
}

func isQuantityDiscountQuestion(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" || isFreeTestRequest(normalized) {
		return false
	}
	mentionsDiscount := containsAny(normalized, []string{
		"скид", "discount", "женилдик", "жеңілдік",
	})
	mentionsQuantity := containsAny(normalized, []string{
		"количество", "количеств", "объем", "объём", "обьем", "ролик", "видео", "креатив",
		"пакетные условия", "опт", "bulk", "quantity", "volume",
	})
	if mentionsDiscount && mentionsQuantity {
		return true
	}
	return containsAny(normalized, []string{
		"пакетные условия", "bulk package", "bulk pricing",
	}) && mentionsQuantity
}

func extractVideoQuantity(text string, current LeadState) string {
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return ""
	}
	quantityContext := current.QuantityDiscountInterest || containsAny(normalized, []string{
		"количество", "количеств", "объем", "объём", "обьем", "ролик", "видео", "креатив",
		"пакетные условия", "опт", "bulk", "quantity", "volume",
	})
	if !quantityContext {
		return ""
	}
	if match := videoQuantityRangePattern.FindStringSubmatch(normalized); len(match) >= 3 {
		return normalizeVideoQuantityRange(match[1], match[2])
	}
	if match := videoQuantitySingleWithUnitPattern.FindStringSubmatch(normalized); len(match) >= 2 {
		return normalizeVideoQuantity(match[1])
	}
	if current.QuantityDiscountInterest {
		if quantity := bareVideoQuantityReply(normalized); quantity != "" {
			return quantity
		}
	}
	return ""
}

func bareVideoQuantityReply(normalized string) string {
	clean := strings.ReplaceAll(strings.Trim(normalized, " \t\r\n.,!?;:"), " ", "")
	if clean == "" {
		return ""
	}
	if match := bareVideoQuantityRangePattern.FindStringSubmatch(clean); len(match) >= 3 {
		return normalizeVideoQuantityRange(match[1], match[2])
	}
	if bareVideoQuantitySinglePattern.MatchString(clean) {
		return normalizeVideoQuantity(clean)
	}
	return ""
}

func normalizeVideoQuantity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if match := videoQuantityRangePattern.FindStringSubmatch(value); len(match) >= 3 {
		return normalizeVideoQuantityRange(match[1], match[2])
	}
	clean := strings.Trim(value, " \t\r\n.,!?;:")
	if bareVideoQuantitySinglePattern.MatchString(clean) && validVideoQuantityNumber(clean) {
		return clean
	}
	return ""
}

func normalizeVideoQuantityRange(left string, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if !validVideoQuantityNumber(left) || !validVideoQuantityNumber(right) {
		return ""
	}
	leftNumber, _ := strconv.Atoi(left)
	rightNumber, _ := strconv.Atoi(right)
	if leftNumber > rightNumber {
		leftNumber, rightNumber = rightNumber, leftNumber
	}
	if leftNumber == rightNumber {
		return strconv.Itoa(leftNumber)
	}
	return strconv.Itoa(leftNumber) + "-" + strconv.Itoa(rightNumber)
}

func validVideoQuantityNumber(value string) bool {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	return err == nil && number > 0 && number <= 500
}

func isFreeTestRequest(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" {
		return false
	}
	return containsAny(normalized, []string{
		"если бесплатно", "бесплатно попроб", "попробуем бесплатно", "попробовать бесплатно",
		"free test", "if free", "for free", "бесплатный тест", "тест бесплатно",
	})
}

func containsDeadlineQuestion(normalized string) bool {
	return strings.Contains(normalized, "?") && containsAny(normalized, []string{
		"срок", "сроки", "когда", "успеете", "48", "мерзим", "кашан", "deadline", "timeline", "how long",
	})
}

func isNegativeReaction(normalized string) bool {
	return containsAny(normalized, []string{
		"надоел", "достал", "отстан", "отвали", "иди ты", "пошел", "пошёл", "тупой",
		"желание пропало", "пропало желание", "уже не интересно", "вообще не понимаете",
		"не понимаете", "бот тупит", "бот тупой", "оставьте", "не надо", "стоп",
		"шаршатты", "мазалама", "жоғал", "annoying", "stop asking", "not interested",
	})
}

func isFrustrationComplaint(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" {
		return false
	}
	return containsAny(normalized, []string{
		"читай внимательно", "читать внимательно", "внимательно читай", "уже написал",
		"уже писала", "я уже писал", "я уже писала", "данные уже есть", "не повторяй",
		"сколько можно", "бот читай", "бот не понял", "бот не понимает", "дорогой бот",
		"вы не читаете", "не читаете", "не понимаете", "бот тупит",
	})
}

func containsHumanRequest(normalized string) bool {
	return containsAny(normalized, []string{
		"оператор", "менеджер", "админ", "администратор", "живой человек", "специалист",
		"свяжите", "соедините", "подключите", "позвоните", "напишите админу", "пишите к админу",
		"нужен оператор", "нужен менеджер", "где оператор", "где менеджер", "передайте менеджеру",
		"передай менеджеру", "на менеджера", "отправь на менеджера", "отправьте на менеджера",
		"менеджер пусть", "пусть менеджер", "без ии", "без ai", "без бота", "не бот",
		"пусть человек", "человек ответит", "живой менеджер", "живой консультант",
		"срочно менеджер", "срочно оператор", "manager", "operator", "human", "real person", "admin",
		"connect me", "call me", "need a manager", "need operator", "менеджер керек", "оператор керек",
	})
}

func asksWhichFormatWorksBest(normalized string) bool {
	if normalized == "" {
		return false
	}
	hasFormat := containsAny(normalized, []string{"формат", "ролик", "креатив", "подача", "угс", "ugc", "format", "creative"})
	hasBest := containsAny(normalized, []string{
		"лучше", "лучший", "заходит", "работает", "эффектив", "конверт", "продает", "продаёт",
		"какой выбрать", "что выбрать", "кайсы жаксы", "жаксы отеди", "best", "works best", "converts",
	})
	hasAds := containsAny(normalized, []string{"реклам", "жарнама", "ads", "performance"})
	return hasFormat && hasBest || hasAds && hasBest && containsAny(normalized, []string{"какой", "что", "қай", "which", "what"})
}

func isMuteRequest(normalized string) bool {
	return containsAny(normalized, []string{
		"не пишите", "не писать", "не беспокой", "не звоните", "больше не надо", "не надо писать",
		"мазаламаңыз", "жазбаңыз", "do not message", "don't message", "stop messaging",
	})
}

func isAgreement(normalized string) bool {
	switch normalized {
	case "да", "да.", "ок", "ок.", "окей", "окей.", "хорошо", "супер", "ага", "угу",
		"принято", "принято.", "иә", "иа", "жаксы", "жақсы", "yes", "yes.", "ok", "okay", "sure":
		return true
	default:
		return false
	}
}

func isRefusal(normalized string) bool {
	if isNoOfferBriefAnswer(normalized) {
		return false
	}
	return containsAny(normalized, []string{
		"нет", "не надо", "не интересно", "не актуально", "отказыва", "жоқ", "керек емес", "no thanks", "not interested",
	})
}

func isGreeting(normalized string) bool {
	return containsAny(normalized, []string{
		"здравствуйте", "здраствуйте", "добрый", "доброе", "доброго", "привет",
		"салам", "сәлем", "салем", "ассалаум", "кайырлы", "қайырлы", "hello", "hi ", "good morning", "good afternoon",
	}) || strings.Trim(normalized, " .,!?:;") == "hi"
}

// isNonNicheCandidateText guards the generic "short answer becomes the niche"
// extraction paths. Greetings, confirmations, timing words, questions, case/
// example/price requests, duration questions and stop commands must never be
// stored as a niche, even when the niche is the only missing field.
func isNonNicheCandidateText(normalized string) bool {
	clean := strings.Trim(normalized, " .,!?:;")
	if clean == "" {
		return true
	}
	if isGreeting(clean) || isAgreement(clean) || isGenericAcknowledgement(clean) || isNonNicheShortReply(clean) {
		return true
	}
	if strings.Contains(normalized, "?") {
		return true
	}
	if containsPortfolioRequest(clean) || containsPriceQuestion(clean) {
		return true
	}
	if isDurationQuestion(clean) {
		return true
	}
	if normalizeDeadline(clean) != "" || isTimingOnlyReply(clean) {
		return true
	}
	if IsAdminStopCommand(clean) || isMuteRequest(clean) {
		return true
	}
	return wordsAreOnlyNonNicheNoise(clean)
}

// isDurationQuestion detects direct questions about the video duration
// ("хронометраж видео какой", "сколько секунд ролик", "длина видео какая").
// They must be answered directly and never stored as a niche or a goal.
func isDurationQuestion(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "хронометраж") {
		return true
	}
	mentionsVideo := containsAny(normalized, []string{"видео", "ролик", "video", "creative"})
	if containsAny(normalized, []string{"секунд", "seconds", "сек ", "sec "}) &&
		(mentionsVideo || containsAny(normalized, []string{"сколько", "канша", "қанша", "how many"})) {
		return true
	}
	return mentionsVideo && containsAny(normalized, []string{
		"длина", "длительность", "продолжительность", "узактыг", "узактык",
		"length", "duration", "how long",
	})
}

// isTimingOnlyReply detects bare timing/context answers such as "сейчас" or
// "завтра" that must not be confused with a business niche.
func isTimingOnlyReply(clean string) bool {
	switch clean {
	case "сейчас", "сегодня", "завтра", "послезавтра", "позже", "потом", "скоро",
		"на днях", "пока нет", "не сейчас", "казир", "қазір", "ертен", "ертең",
		"now", "today", "tomorrow", "later", "soon":
		return true
	default:
		return false
	}
}

// wordsAreOnlyNonNicheNoise reports whether every word of a short reply is a
// filler/intent word ("бот собираюсь", "ну давайте"), so the text carries no
// business meaning that could be a niche.
func wordsAreOnlyNonNicheNoise(clean string) bool {
	words := strings.Fields(clean)
	if len(words) == 0 || len(words) > 4 {
		return false
	}
	noise := map[string]bool{
		"бот": true, "бота": true, "боту": true, "bot": true,
		"собираюсь": true, "собираемся": true, "планирую": true, "планируем": true,
		"думаю": true, "думаем": true, "хочу": true, "хотим": true, "надо": true,
		"нужно": true, "ну": true, "вот": true, "это": true, "просто": true,
		"давай": true, "давайте": true, "можно": true, "пока": true, "еще": true, "ещё": true,
	}
	for _, word := range words {
		if !noise[strings.Trim(word, " .,!?:;")] {
			return false
		}
	}
	return true
}

func extractSelectedLevel(text string) int {
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return 0
	}
	compact := compactNumericText(normalized)

	switch {
	case containsAny(normalized, []string{"стандарт", "премиум", "standard", "premium", "стандартный"}) ||
		containsAny(normalized, []string{"третий вариант", "3 вариант", "номер 3", "третий", "третье"}) ||
		strings.Contains(compact, "75000") ||
		priceShortcutSelected(normalized, "75"):
		return 3
	case containsAny(normalized, []string{"базов", "basic", "базалык", "базалық"}) ||
		containsAny(normalized, []string{"второй вариант", "2 вариант", "номер 2", "второй", "второе"}) ||
		strings.Contains(compact, "50000") ||
		priceShortcutSelected(normalized, "50"):
		return 2
	case containsAny(normalized, []string{"тест", "test", "тестовый", "тестілік"}) ||
		containsAny(normalized, []string{"первый вариант", "1 вариант", "номер 1", "первый", "первое"}) ||
		strings.Contains(compact, "35000") ||
		priceShortcutSelected(normalized, "35"):
		return 1
	default:
		return 0
	}
}

func looksLikePackageSelection(normalized string) bool {
	clean := strings.Trim(normalized, " .,!?:;")
	switch clean {
	case "тест", "тестовый", "test", "базовый", "basic", "стандарт", "премиум", "standard", "premium",
		"35", "50", "75", "35000", "50000", "75000":
		return true
	}
	return containsAny(normalized, []string{
		"ок ", "окей", "беру", "берем", "берём", "нам надо", "подходит", "давайте", "супер", "аламыз",
		"керек", "take", "we need", "let's", "lets",
	}) || priceShortcutSelected(normalized, "35") ||
		priceShortcutSelected(normalized, "50") ||
		priceShortcutSelected(normalized, "75")
}

func isPackageInfoQuestion(normalized string) bool {
	return strings.Contains(normalized, "?") && containsAny(normalized, []string{
		"чем отлич", "разниц", "что входит", "подробнее", "айырмаш", "не кіреді", "difference", "what is included", "details",
	})
}

func compactNumericText(text string) string {
	var builder strings.Builder
	for _, r := range text {
		if r >= '0' && r <= '9' {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func priceShortcutSelected(normalized string, value string) bool {
	pattern := regexp.MustCompile(`(?:^|[^0-9])` + regexp.QuoteMeta(value) + `\s*(?:к|k|тыс|000|тг|тенге|kzt)(?:$|[^a-zа-я0-9])`)
	return pattern.MatchString(normalized)
}

func packageKey(level int) string {
	switch level {
	case 1:
		return "test"
	case 2:
		return "basic"
	case 3:
		return "standard"
	default:
		return ""
	}
}

func hasGoalMarker(normalized string) bool {
	return normalizeGoal(normalized) != "" ||
		containsAny(normalized, []string{"цель", "максат", "goal", "задача"})
}

func hasDeadlineMarker(normalized string) bool {
	return normalizeDeadline(normalized) != "" ||
		containsAny(normalized, []string{"срок", "сроки", "мерзим", "deadline"})
}

func normalizeGoal(value string) string {
	value = normalizeForAnalysis(value)
	if isURLOnlyMessage(value) {
		return ""
	}
	switch {
	case isPositioningGoalText(value):
		return "объяснить преимущество"
	case containsAny(value, []string{"объяснить преимущество", "объяснить преимущества", "позиционир", "преимуществ"}):
		return "объяснить преимущество"
	case containsAny(value, []string{"удво", "2x", "x2"}) && containsAny(value, []string{"продаж", "сат", "sales"}):
		return "удвоить продажи"
	case containsAny(value, []string{
		"привлеч", "больше клиент", "новых клиент", "получать клиент", "получить клиент",
		"client acquisition", "more clients", "new clients", "кобейт", "көбейт", "көбейту",
	}):
		return "привлечь клиентов"
	case containsAny(value, []string{"reels", "рилс", "контент", "content", "tiktok", "тик ток", "instagram", "инстаграм"}):
		return "контент для продвижения"
	case containsAny(value, []string{"продаж", "продав", "выруч", "сбыт", "сатылым", "сату", "sales", "revenue"}):
		return "рост продаж"
	case containsAny(value, []string{"заяв", "лид", "лиды", "өтінім", "lead", "leads"}):
		return "получать заявки"
	case containsAny(value, []string{"узнаваем", "охват", "бренд", "танымал", "awareness", "reach"}):
		return "повысить узнаваемость"
	case containsAny(value, []string{"трафик", "посещ", "traffic"}):
		return "увеличить трафик"
	case containsAny(value, []string{"подпис", "жазыл", "followers", "subscribers"}):
		return "увеличить подписчиков"
	case containsAny(value, []string{"презентац", "таныстыр", "presentation"}):
		return "презентация продукта"
	case containsAny(value, []string{"запуск реклам", "старт реклам", "запустить реклам", "ads launch"}):
		return "запуск рекламы"
	default:
		return ""
	}
}

func isPositioningGoalText(value string) bool {
	value = normalizeForAnalysis(value)
	return containsAny(value, []string{"почему именно", "почему выбрать", "почему мы", "почему у нас", "отличаемся", "отстроиться"})
}

func normalizeDeadline(value string) string {
	value = normalizeForAnalysis(value)
	weekday := deadlineWeekday(value)
	concreteDate := concreteDatePhrase(value)
	monthDate := concreteMonthDatePhrase(value)
	wordNumberDate := wordNumberDeadlinePhrase(value)
	switch {
	case containsAny(value, []string{"без срока", "без строгого срока", "нет срока", "не срочно", "срок не важен", "no strict deadline", "no deadline"}):
		return "без строгого срока"
	case containsAny(value, []string{"48 час", "48 сағ"}):
		return "за 48 часов"
	case containsAny(value, []string{"сегодня", "бүгін", "today"}):
		return "сегодня"
	case containsAny(value, []string{"завтра", "ертең", "tomorrow"}):
		return "завтра"
	case weekday != "":
		return "до " + weekday
	case containsAny(value, []string{"срочно", "тез", "urgent", "asap"}):
		return "срочно"
	case containsAny(value, []string{"этой недел", "осы апта", "this week"}):
		return "на этой неделе"
	case containsAny(value, []string{"недел", "апта", "week"}):
		return "через неделю"
	case concreteDate != "":
		return concreteDate
	case monthDate != "":
		return monthDate
	case containsAny(value, []string{"месяц", "month", "бир ай"}) || containsWordOrPhrase(value, "ай"):
		return "в течение месяца"
	case wordNumberDate != "":
		return wordNumberDate
	case containsAny(value, []string{"день", "дня", "дней", "күн", "days"}):
		return firstDeadlinePhrase(value)
	default:
		return ""
	}
}

func deadlineWeekday(value string) string {
	weekdays := []string{
		"понедельник", "понедельника", "вторник", "вторника", "среда", "среду", "среды", "четверг", "четверга", "пятница", "пятницу", "пятницы", "суббота", "субботу", "субботы", "воскресенье", "воскресенья",
		"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday",
	}
	for _, weekday := range weekdays {
		if strings.Contains(value, "до "+weekday) || strings.Contains(value, "к "+weekday) || strings.Contains(value, "by "+weekday) {
			if weekday == "пятницу" {
				return "пятницы"
			}
			if weekday == "пятница" {
				return "пятницы"
			}
			if weekday == "среду" {
				return "среды"
			}
			if weekday == "среда" {
				return "среды"
			}
			if weekday == "субботу" {
				return "субботы"
			}
			if weekday == "суббота" {
				return "субботы"
			}
			return weekday
		}
	}
	return ""
}

func concreteDatePhrase(value string) string {
	pattern := regexp.MustCompile(`(?:до|к|by)?\s*(\d{1,2})[./-](\d{1,2})(?:[./-](\d{2,4}))?`)
	match := pattern.FindStringSubmatch(value)
	if len(match) < 3 {
		return ""
	}
	date := match[1] + "." + match[2]
	if len(match) > 3 && match[3] != "" {
		date += "." + match[3]
	}
	return "до " + date
}

func concreteMonthDatePhrase(value string) string {
	months := []string{
		"января", "январь", "февраля", "февраль", "марта", "март", "апреля", "апрель",
		"мая", "май", "июня", "июнь", "июля", "июль", "августа", "август",
		"сентября", "сентябрь", "октября", "октябрь", "ноября", "ноябрь", "декабря", "декабрь",
	}
	pattern := regexp.MustCompile(`(?:до|к|by)?\s*(\d{1,2})\s+(` + strings.Join(months, "|") + `)`)
	match := pattern.FindStringSubmatch(value)
	if len(match) < 3 {
		return ""
	}
	return "до " + match[1] + " " + match[2]
}

func firstDeadlinePhrase(value string) string {
	parts := strings.Fields(value)
	for i, part := range parts {
		if isNumericToken(part) && i+1 < len(parts) {
			next := parts[i+1]
			if strings.HasPrefix(next, "д") || strings.HasPrefix(next, "к") || strings.HasPrefix(next, "day") {
				return "за " + part + " " + next
			}
		}
	}
	return "в ближайшие дни"
}

func wordNumberDeadlinePhrase(value string) string {
	numbers := map[string]string{
		"один": "1", "одну": "1", "одного": "1",
		"два": "2", "две": "2",
		"три": "3", "четыре": "4", "пять": "5",
		"шесть": "6", "семь": "7", "восемь": "8", "девять": "9", "десять": "10",
	}
	parts := strings.Fields(value)
	for i, part := range parts {
		number := numbers[strings.Trim(part, " .,!?:;")]
		if number == "" || i+1 >= len(parts) {
			continue
		}
		next := strings.Trim(parts[i+1], " .,!?:;")
		if strings.HasPrefix(next, "д") || strings.HasPrefix(next, "к") {
			return "за " + number + " " + next
		}
	}
	return ""
}

func knownNicheFromText(normalized string) string {
	if containsAny(normalized, []string{"запчаст", "автозапчаст"}) {
		return "запчасти"
	}
	candidates := []string{
		"стирка ковров", "чистка ковров", "химчистка ковров", "копирайтинг",
		"доставка еды", "магазин одежды", "барбер", "барбершоп",
		"фермерские продукты", "пылесосы", "еда",
		"плиточный клей", "плиточные клея", "строительные материалы",
		"отделочные материалы", "сухие смеси", "стройматериалы",
		"чай", "носки", "чай и носки", "автозапчасти", "авто запчасти", "запчасти",
		"шпаклевка", "штукатурка", "ламинат", "керамогранит", "гипсокартон",
		"мед услуги", "медицинские услуги", "индустриальная медицина",
		"промышленная медицина", "спорт", "фитнес", "йога", "стоматология", "медицина", "косметология",
		"салон красоты", "ресторан", "кафе", "доставка", "одежда", "обувь",
		"недвижимость", "ремонт", "строительство", "строительная компания", "образование", "курсы",
		"мебель", "мебельная ниша", "тв зона", "тв зоны", "кофейня", "детская одежда",
		"авто", "туризм", "отель", "барбершоп", "маркетинг",
		"бад для похудения", "бад", "бады", "нутрицевтик", "биодобав",
		"онлайн курс", "online course", "real estate", "beauty salon", "expert blog",
		"clothing brand", "education", "fitness", "construction", "medical clinic",
		"медицинская клиника", "клиника", "экспертный блог", "бренд одежды",
	}
	for _, candidate := range candidates {
		if containsWordOrPhrase(normalized, candidate) {
			if strings.Contains(candidate, " ") {
				return candidate
			}
			if len(strings.Fields(normalized)) <= 4 {
				return normalizeNiche(normalized)
			}
			return candidate
		}
	}
	return ""
}

func cleanNicheSource(value string) string {
	value = normalizeForAnalysis(value)
	replacer := strings.NewReplacer(
		"!", " ",
		"?", " ",
		".", " ",
		",", " ",
		";", " ",
		":", " ",
		"—", " ",
		"-", " ",
	)
	value = replacer.Replace(value)
	value = removeKnownCityPhrases(value)
	noise := []string{
		"здравствуйте", "здраствуйте", "добрый день", "добрый вечер", "доброе утро",
		"доброго дня", "доброго утра", "доброго вечера", "привет", "салам",
		"ниша", "сфера", "направление",
		"у нас", "у меня", "моя", "мой", "наша", "наш", "работа", "занимаюсь", "занимаемся",
	}
	for _, item := range noise {
		value = strings.ReplaceAll(value, item, " ")
	}
	return strings.Join(strings.Fields(value), " ")
}

func removeKnownCityPhrases(value string) string {
	replacements := []string{
		"в алматы", "г алматы", "город алматы", "алматы", "алмате", "алмата",
		"в астане", "астана", "астане", "нур султан", "нурсултан",
		"в шымкенте", "в шимкенте", "шымкент", "шимкент",
		"караганда", "караганде", "актобе", "атырау", "актау",
	}
	for _, item := range replacements {
		value = strings.ReplaceAll(value, item, " ")
	}
	return strings.Join(strings.Fields(value), " ")
}

func containsWordOrPhrase(text string, phrase string) bool {
	clean := func(value string) string {
		value = normalizeForAnalysis(value)
		value = strings.NewReplacer(
			"!", " ",
			"?", " ",
			".", " ",
			",", " ",
			";", " ",
			":", " ",
			"—", " ",
			"-", " ",
		).Replace(value)
		return strings.Join(strings.Fields(value), " ")
	}
	text = " " + clean(text) + " "
	phrase = " " + clean(phrase) + " "
	return strings.Contains(text, phrase)
}

func productNicheFromText(normalized string) string {
	if normalized == "" || strings.Contains(normalized, "?") || strings.Contains(normalized, "http") || strings.Contains(normalized, "www") {
		return ""
	}
	if normalizeGoal(normalized) != "" || normalizeDeadline(normalized) != "" {
		return ""
	}
	words := strings.Fields(normalized)
	if len(words) == 0 || len(words) > 8 {
		return ""
	}
	productMarkers := []string{
		"продаю", "продаем", "продаём", "продвигаю", "продвигаем", "рекламирую", "рекламируем",
		"занимаюсь", "занимаемся", "у меня", "у нас", "сатамын", "сатамыз", "sell", "selling",
	}
	value := normalized
	found := false
	for _, marker := range productMarkers {
		if strings.Contains(value, marker) {
			value = strings.ReplaceAll(value, marker, " ")
			found = true
		}
	}
	if !found && knownNicheFromText(normalized) == "" {
		return ""
	}
	value = strings.Join(strings.Fields(value), " ")
	value = strings.Trim(value, " -—:;,.!?")
	if value == "" {
		return knownNicheFromText(normalized)
	}
	return normalizeNiche(value)
}

func isLikelyShortAnswerFor(field string, current LeadState, normalized string) bool {
	if normalized == "" {
		return false
	}
	if containsAny(normalized, []string{"?", "http", "www", "@"}) {
		return false
	}
	words := strings.Fields(normalized)
	if len(words) == 0 || len(words) > 5 {
		return false
	}

	missing := current.MissingCoreFields()
	if len(missing) != 1 || missing[0] != field {
		return false
	}

	switch field {
	case fieldNiche:
		return normalizeGoal(normalized) == "" && normalizeDeadline(normalized) == "" && len(extractPlatforms(normalized)) == 0
	case fieldGoal:
		return normalizeGoal(normalized) != ""
	case fieldPlatforms:
		return len(extractPlatforms(normalized)) > 0
	case fieldDeadline:
		return normalizeDeadline(normalized) != ""
	default:
		return false
	}
}

func extractPrefixedValue(normalized string, prefixes []string, stopWords []string) string {
	if normalized == "" {
		return ""
	}
	pattern := regexp.MustCompile(strings.Replace(prefixedValuePattern.String(), "%s", strings.Join(prefixes, "|"), 1))
	match := pattern.FindStringSubmatch(normalized)
	if len(match) < 3 {
		return ""
	}
	value := strings.TrimSpace(match[2])
	for _, stopWord := range stopWords {
		if index := strings.Index(value, " "+stopWord); index >= 0 {
			value = value[:index]
		}
	}
	return strings.TrimSpace(value)
}

func normalizeNiche(value string) string {
	value = normalizeForAnalysis(value)
	value = strings.Trim(value, " -—:;,.!?")
	if strings.HasPrefix(value, "производства ") {
		value = "производство " + strings.TrimSpace(strings.TrimPrefix(value, "производства "))
	}
	if !isValidNiche(value) || value == "и тд" || value == "и так далее" {
		return ""
	}
	return value
}

func normalizeForAnalysis(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer(
		"ё", "е",
		"ә", "а",
		"ғ", "г",
		"қ", "к",
		"ң", "н",
		"ө", "о",
		"ұ", "у",
		"ү", "у",
		"һ", "х",
		"і", "и",
		"\n", " ",
		"\t", " ",
	)
	text = replacer.Replace(text)
	return strings.Join(strings.Fields(text), " ")
}

func mergePlatforms(existing []string, incoming []string) []string {
	result := make([]string, 0, len(existing)+len(incoming))
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	for _, platform := range append(existing, incoming...) {
		platform = strings.TrimSpace(platform)
		if platform == "" {
			continue
		}
		key := strings.ToLower(platform)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, platform)
	}
	return result
}

func appendUniqueString(existing []string, incoming string) []string {
	incoming = strings.TrimSpace(incoming)
	if incoming == "" {
		return existing
	}
	for _, item := range existing {
		if strings.EqualFold(strings.TrimSpace(item), incoming) {
			return existing
		}
	}
	return append(existing, incoming)
}

func nullableString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func stringPointer(value string) *string {
	value = strings.TrimSpace(value)
	return &value
}
