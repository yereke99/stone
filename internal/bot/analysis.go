package bot

import (
	"encoding/json"
	"regexp"
	"strings"
)

const (
	fieldNiche         = "niche"
	fieldGoal          = "goal"
	fieldPlatform      = "platform"
	fieldPlatforms     = fieldPlatform
	fieldDeadline      = "deadline"
	fieldPreviousAIAds = "ai_experience"
	fieldPackage       = "package"
	fieldBrief         = "brief"

	IntentGreeting         = "greeting"
	IntentAnswer           = "answer"
	IntentPortfolioRequest = "portfolio_request"
	IntentPriceQuestion    = "price_question"
	IntentPackageQuestion  = "package_question"
	IntentPackageSelection = "package_selection"
	IntentAgreement        = "agreement"
	IntentRefusal          = "refusal"
	IntentDeadlineQuestion = "deadline_question"
	IntentReadyToOrder     = "ready_to_order"
	IntentObjection        = "objection"
	IntentNegativeReaction = "negative_reaction"
	IntentBriefAnswer      = "brief_answer"
	IntentHumanRequest     = "human_request"
	IntentMute             = "mute"
	IntentOther            = "other"
)

var prefixedValuePattern = regexp.MustCompile(`(?i)(?:^|\s)(%s)\s*[:=\-—]?\s*([^,.!?;]+)`)

type LeadState struct {
	HasBeenGreeted    bool     `json:"has_been_greeted,omitempty"`
	Niche             string   `json:"niche,omitempty"`
	Goal              string   `json:"goal,omitempty"`
	Platform          string   `json:"platform,omitempty"`
	Platforms         []string `json:"platforms,omitempty"`
	Deadline          string   `json:"deadline,omitempty"`
	PreviousAIAds     *bool    `json:"previous_ai_ads,omitempty"`
	AIExperience      string   `json:"ai_experience,omitempty"`
	Budget            string   `json:"budget,omitempty"`
	PriceInterest     bool     `json:"price_interest,omitempty"`
	SelectedPackage   string   `json:"selected_package,omitempty"`
	ClientName        string   `json:"client_name,omitempty"`
	TargetAudience    string   `json:"target_audience,omitempty"`
	Notes             string   `json:"notes,omitempty"`
	FreeText          string   `json:"free_text,omitempty"`
	PortfolioSent     bool     `json:"portfolio_sent,omitempty"`
	OfferSent         bool     `json:"offer_sent,omitempty"`
	BriefRequested    bool     `json:"brief_requested,omitempty"`
	BriefCompleted    bool     `json:"brief_completed,omitempty"`
	ContactBriefReady bool     `json:"contact_brief_ready,omitempty"`
	LeadStatus        string   `json:"lead_status,omitempty"`
}

type CustomerAnalysis struct {
	Niche          *string  `json:"niche"`
	Goal           *string  `json:"goal"`
	Platforms      []string `json:"platforms"`
	Deadline       *string  `json:"deadline"`
	PreviousAIAds  *bool    `json:"previous_ai_ads"`
	AIExperience   *string  `json:"ai_experience,omitempty"`
	Budget         *string  `json:"budget,omitempty"`
	TargetAudience *string  `json:"target_audience,omitempty"`
	Intent         string   `json:"intent"`
	SelectedLevel  int      `json:"selected_level,omitempty"`
	MissingFields  []string `json:"missing_fields"`
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

	if niche := extractNiche(text, current); niche != "" {
		analysis.Niche = stringPointer(niche)
	}
	if goal := extractGoal(text, current); goal != "" {
		analysis.Goal = stringPointer(goal)
	}
	analysis.Platforms = extractPlatforms(text)
	if deadline := extractDeadline(text, current); deadline != "" {
		analysis.Deadline = stringPointer(deadline)
	}
	if used := extractPreviousAIAds(text, current); used != nil {
		analysis.PreviousAIAds = used
		analysis.AIExperience = stringPointer(aiExperienceLabel(*used))
	}
	if budget := extractBudget(text); budget != "" {
		analysis.Budget = stringPointer(budget)
	}
	if audience := extractTargetAudience(text); audience != "" {
		analysis.TargetAudience = stringPointer(audience)
	}
	analysis.SelectedLevel = extractSelectedLevel(text)

	switch {
	case isMuteRequest(normalized):
		analysis.Intent = IntentMute
	case isNegativeReaction(normalized):
		analysis.Intent = IntentNegativeReaction
	case containsHumanRequest(normalized):
		analysis.Intent = IntentHumanRequest
	case containsPortfolioRequest(text):
		analysis.Intent = IntentPortfolioRequest
	case containsDeadlineQuestion(normalized):
		analysis.Intent = IntentDeadlineQuestion
	case analysis.SelectedLevel > 0 && isPackageInfoQuestion(normalized):
		analysis.Intent = IntentPackageQuestion
	case containsPriceQuestion(normalized):
		analysis.Intent = IntentPriceQuestion
	case analysis.SelectedLevel > 0 && looksLikePackageSelection(normalized):
		analysis.Intent = IntentPackageSelection
	case containsReadySignal(text):
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

	updated := current
	updated.ApplyAnalysis(analysis)
	analysis.MissingFields = updated.MissingCoreFields()
	return analysis
}

func (a CustomerAnalysis) HasBusinessSignal() bool {
	return a.Niche != nil ||
		a.Goal != nil ||
		len(a.Platforms) > 0 ||
		a.Deadline != nil ||
		a.PreviousAIAds != nil ||
		a.Budget != nil ||
		a.TargetAudience != nil ||
		a.SelectedLevel > 0
}

func (s *LeadState) ApplyAnalysis(analysis CustomerAnalysis) {
	if analysis.Niche != nil && strings.TrimSpace(*analysis.Niche) != "" {
		s.Niche = strings.TrimSpace(*analysis.Niche)
	}
	if analysis.Goal != nil && strings.TrimSpace(*analysis.Goal) != "" {
		s.Goal = strings.TrimSpace(*analysis.Goal)
	}
	if len(analysis.Platforms) > 0 {
		s.Platforms = mergePlatforms(s.Platforms, analysis.Platforms)
		s.Platform = strings.Join(s.Platforms, ", ")
	}
	if analysis.Deadline != nil && strings.TrimSpace(*analysis.Deadline) != "" {
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
	if analysis.TargetAudience != nil && strings.TrimSpace(*analysis.TargetAudience) != "" {
		s.TargetAudience = strings.TrimSpace(*analysis.TargetAudience)
	}
	if (analysis.Intent == IntentPackageSelection || analysis.Intent == IntentHumanRequest) && analysis.SelectedLevel > 0 {
		s.SelectedPackage = packageKey(analysis.SelectedLevel)
		s.LeadStatus = LeadStatusHot
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
		s.ContactBriefReady = true
		s.LeadStatus = LeadStatusHandoffRequired
	}
	if analysis.Intent == IntentRefusal {
		s.LeadStatus = LeadStatusClosed
	}
	if analysis.Intent == IntentMute {
		s.LeadStatus = LeadStatusMuted
	}
}

func (s LeadState) MissingCoreFields() []string {
	missing := make([]string, 0, 4)
	if strings.TrimSpace(s.Niche) == "" {
		missing = append(missing, fieldNiche)
	}
	if strings.TrimSpace(s.Goal) == "" {
		missing = append(missing, fieldGoal)
	}
	if len(s.Platforms) == 0 && strings.TrimSpace(s.Platform) == "" {
		missing = append(missing, fieldPlatforms)
	}
	if strings.TrimSpace(s.Deadline) == "" {
		missing = append(missing, fieldDeadline)
	}
	return missing
}

func (s LeadState) HasCoreFields() bool {
	return len(s.MissingCoreFields()) == 0
}

func (s LeadState) PromptJSON(stage string) string {
	summary := struct {
		HasBeenGreeted    bool     `json:"has_been_greeted"`
		Niche             *string  `json:"niche"`
		Goal              *string  `json:"goal"`
		Platform          *string  `json:"platform"`
		Platforms         []string `json:"platforms"`
		Deadline          *string  `json:"deadline"`
		PreviousAIAds     *bool    `json:"previous_ai_ads"`
		AIExperience      *string  `json:"ai_experience"`
		Budget            *string  `json:"budget"`
		PriceInterest     bool     `json:"price_interest"`
		SelectedPackage   *string  `json:"selected_package"`
		ClientName        *string  `json:"client_name"`
		TargetAudience    *string  `json:"target_audience"`
		Notes             *string  `json:"notes"`
		FreeText          *string  `json:"free_text"`
		PortfolioSent     bool     `json:"portfolio_sent"`
		OfferSent         bool     `json:"offer_sent"`
		BriefRequested    bool     `json:"brief_requested"`
		BriefCompleted    bool     `json:"brief_completed"`
		ContactBriefReady bool     `json:"contact_brief_ready"`
		LeadStatus        string   `json:"lead_status"`
		Stage             string   `json:"stage"`
	}{
		HasBeenGreeted:    s.HasBeenGreeted,
		Niche:             nullableString(s.Niche),
		Goal:              nullableString(s.Goal),
		Platform:          nullableString(s.platformSummary()),
		Platforms:         append([]string(nil), s.Platforms...),
		Deadline:          nullableString(s.Deadline),
		PreviousAIAds:     s.PreviousAIAds,
		AIExperience:      nullableString(s.AIExperience),
		Budget:            nullableString(s.Budget),
		PriceInterest:     s.PriceInterest,
		SelectedPackage:   nullableString(s.SelectedPackage),
		ClientName:        nullableString(s.ClientName),
		TargetAudience:    nullableString(s.TargetAudience),
		Notes:             nullableString(s.Notes),
		FreeText:          nullableString(s.FreeText),
		PortfolioSent:     s.PortfolioSent,
		OfferSent:         s.OfferSent,
		BriefRequested:    s.BriefRequested,
		BriefCompleted:    s.BriefCompleted,
		ContactBriefReady: s.ContactBriefReady,
		LeadStatus:        normalizeLeadStatus(s.LeadStatus),
		Stage:             strings.TrimSpace(stage),
	}
	if summary.Platforms == nil {
		summary.Platforms = []string{}
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
	if value := extractPrefixedValue(normalized, []string{
		"ниша", "сфера", "бизнес", "направление", "сала", "niche", "industry",
	}, []string{
		"цель", "максат", "goal", "срок", "сроки", "мерзим", "deadline", "площад", "platform",
	}); value != "" {
		return normalizeNiche(value)
	}

	if hasGoalMarker(normalized) || hasDeadlineMarker(normalized) || len(extractPlatforms(text)) > 0 {
		if value := knownNicheFromText(normalized); value != "" {
			return value
		}
		return ""
	}

	if isLikelyShortAnswerFor(fieldNiche, current, normalized) {
		return normalizeNiche(normalized)
	}

	return knownNicheFromText(normalized)
}

func extractGoal(text string, current LeadState) string {
	normalized := normalizeForAnalysis(text)
	if value := extractPrefixedValue(normalized, []string{
		"цель", "максат", "goal", "задача", "нужно", "керек",
	}, []string{
		"ниша", "сфера", "сала", "срок", "сроки", "мерзим", "deadline", "площад", "platform",
	}); value != "" {
		if goal := normalizeGoal(value); goal != "" {
			return goal
		}
	}
	if goal := normalizeGoal(normalized); goal != "" {
		return goal
	}
	if isLikelyShortAnswerFor(fieldGoal, current, normalized) {
		return normalizeGoal(normalized)
	}
	return ""
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
	if containsAny(normalized, []string{"таргет", "target", "targeted ads", "рекламага", "жарнама"}) {
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
		"аудитория", "ца", "target audience", "клиенты", "клиенттер",
	}, []string{
		"ниша", "сфера", "сала", "цель", "максат", "goal", "срок", "сроки", "мерзим", "deadline", "площад", "platform",
	}); value != "" {
		return strings.Trim(value, " -—:;,.!?")
	}
	if strings.Contains(normalized, "женщины 25") {
		return "женщины 25+"
	}
	if strings.Contains(normalized, "мужчины 25") {
		return "мужчины 25+"
	}
	return ""
}

func containsPriceQuestion(normalized string) bool {
	return containsAny(normalized, []string{
		"цена", "стоимость", "сколько", "прайс", "тариф", "қанша", "баға", "price", "cost",
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
		"шаршатты", "мазалама", "жоғал", "annoying", "stop asking",
	})
}

func containsHumanRequest(normalized string) bool {
	return containsAny(normalized, []string{
		"оператор", "менеджер", "админ", "администратор", "живой человек", "специалист",
		"свяжите", "соедините", "подключите", "позвоните", "напишите админу", "пишите к админу",
		"нужен оператор", "нужен менеджер", "где оператор", "где менеджер", "передайте менеджеру",
		"срочно менеджер", "срочно оператор", "manager", "operator", "human", "real person", "admin",
		"connect me", "call me", "need a manager", "need operator", "менеджер керек", "оператор керек",
	})
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
		"иә", "иа", "жаксы", "жақсы", "yes", "yes.", "ok", "okay", "sure":
		return true
	default:
		return false
	}
}

func isRefusal(normalized string) bool {
	return containsAny(normalized, []string{
		"нет", "не надо", "не интересно", "отказыва", "жоқ", "керек емес", "no thanks", "not interested",
	})
}

func isGreeting(normalized string) bool {
	return containsAny(normalized, []string{
		"здравствуйте", "добрый", "привет", "салам", "сәлем", "салем", "hello", "hi",
	})
}

func extractSelectedLevel(text string) int {
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return 0
	}
	compact := compactNumericText(normalized)

	switch {
	case containsAny(normalized, []string{"стандарт", "премиум", "standard", "premium", "стандартный"}) ||
		strings.Contains(compact, "75000") ||
		priceShortcutSelected(normalized, "75"):
		return 3
	case containsAny(normalized, []string{"базов", "basic", "базалык", "базалық"}) ||
		strings.Contains(compact, "50000") ||
		priceShortcutSelected(normalized, "50"):
		return 2
	case containsAny(normalized, []string{"тест", "test", "тестовый", "тестілік"}) ||
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
	switch {
	case containsAny(value, []string{"удво", "2x", "x2"}) && containsAny(value, []string{"продаж", "сат", "sales"}):
		return "удвоить продажи"
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
	case containsAny(value, []string{"запуск реклам", "реклам", "жарнама", "ads"}):
		return "запуск рекламы"
	default:
		return ""
	}
}

func normalizeDeadline(value string) string {
	value = normalizeForAnalysis(value)
	weekday := deadlineWeekday(value)
	concreteDate := concreteDatePhrase(value)
	switch {
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
	case containsAny(value, []string{"месяц", "ай", "month"}):
		return "в течение месяца"
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

func knownNicheFromText(normalized string) string {
	candidates := []string{
		"спорт", "фитнес", "йога", "стоматология", "медицина", "косметология",
		"салон красоты", "ресторан", "кафе", "доставка", "одежда", "обувь",
		"недвижимость", "ремонт", "строительство", "образование", "курсы",
		"мебель", "авто", "туризм", "отель", "барбершоп", "маркетинг",
	}
	for _, candidate := range candidates {
		if strings.Contains(normalized, candidate) {
			if len(strings.Fields(normalized)) <= 4 {
				return normalizeNiche(normalized)
			}
			return candidate
		}
	}
	return ""
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
	if value == "" || value == "и тд" || value == "и так далее" {
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
