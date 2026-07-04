package bot

import (
	"fmt"
	"strings"
)

type leadReply struct {
	text        string
	stage       string
	level       int
	askedFields []string
}

func buildLeadReply(language string, lead LeadState, analysis CustomerAnalysis, conversation Conversation) (leadReply, bool) {
	if analysis.Intent == IntentNegativeReaction {
		field := firstAskableMissingField(lead, conversation)
		return leadReply{text: negativeMissingReply(language, lead, field), stage: StageDiagnosis, askedFields: []string{field}}, true
	}

	if missing := askableMissingFields(lead, conversation); len(missing) > 0 {
		fields := limitFieldsToAsk(missing, 2)
		return leadReply{text: askMissingFieldsReply(language, lead, fields), stage: StageQualification, askedFields: fields}, true
	}
	if len(lead.MissingCoreFields()) > 0 {
		return leadReply{text: unclearAfterAskedReply(language, lead), stage: StageQualification}, true
	}

	if lead.PreviousAIAds == nil && !conversation.AskedFields[fieldPreviousAIAds] {
		return leadReply{text: askPreviousAIWithDiagnosis(language, lead), stage: StagePlatformDetected, askedFields: []string{fieldPreviousAIAds}}, true
	}

	if analysis.PreviousAIAds != nil || conversation.Stage == StageDiagnosis || conversation.Stage == StagePlatformDetected {
		return leadReply{text: previousAIOffer(language, *lead.PreviousAIAds), stage: StagePackageSuggested}, true
	}

	return leadReply{}, false
}

func buildReturningLeadReply(language string, conversation Conversation, analysis CustomerAnalysis) (leadReply, bool) {
	if !isCasualReturningIntent(analysis.Intent) {
		return leadReply{}, false
	}

	lead := conversation.Lead
	level := selectedLevelFromConversation(conversation)
	switch {
	case lead.BriefCompleted || lead.ContactBriefReady || normalizeLeadStatus(lead.LeadStatus) == LeadStatusHandoffRequired:
		return leadReply{text: handoffStatusText(language), stage: StageHandoffRequired, level: level}, true
	case lead.BriefRequested && level > 0:
		return leadReply{text: continueBriefForPackageText(language, level), stage: StageBriefRequested, level: level}, true
	case lead.BriefRequested:
		return leadReply{text: BriefReminderText(language), stage: StageBriefRequested, level: level}, true
	case strings.TrimSpace(lead.SelectedPackage) != "" && level > 0:
		return leadReply{text: BriefTextForPackage(language, level), stage: StageBriefRequested, level: level}, true
	case lead.OfferSent || conversation.Stage == StagePackageSuggested || conversation.Stage == StageOffer:
		return leadReply{text: continueAfterOfferText(language), stage: StagePackageSuggested, level: 0}, true
	case lead.PortfolioSent || conversation.Stage == StagePortfolioSent || conversation.Stage == StagePortfolio:
		return leadReply{text: continueAfterPortfolioText(language), stage: StagePortfolioSent, level: 0}, true
	default:
		return leadReply{}, false
	}
}

func isCasualReturningIntent(intent string) bool {
	switch intent {
	case IntentGreeting, IntentAgreement, IntentOther:
		return true
	default:
		return false
	}
}

func askableMissingFields(lead LeadState, conversation Conversation) []string {
	missing := lead.MissingCoreFields()
	result := make([]string, 0, len(missing))
	for _, field := range missing {
		field = normalizeFieldName(field)
		if field == "" {
			continue
		}
		if conversation.CompletedFields[field] || conversation.AskedFields[field] {
			continue
		}
		result = append(result, field)
	}
	return result
}

func firstAskableMissingField(lead LeadState, conversation Conversation) string {
	if fields := askableMissingFields(lead, conversation); len(fields) > 0 {
		return fields[0]
	}
	if lead.PreviousAIAds == nil && !conversation.AskedFields[fieldPreviousAIAds] {
		return fieldPreviousAIAds
	}
	return ""
}

func limitFieldsToAsk(fields []string, limit int) []string {
	if limit <= 0 || len(fields) <= limit {
		return fields
	}
	return append([]string(nil), fields[:limit]...)
}

func askMissingFieldsReply(language string, lead LeadState, missing []string) string {
	language = normalizeLanguageCode(language)
	platforms := formatPlatformList(lead.Platforms, language)

	if sameFields(missing, []string{fieldNiche, fieldGoal}) && platforms != "" {
		switch language {
		case "kk":
			return fmt.Sprintf("Түсіндім, іске қосу %s үшін. Не сатасыз және роликтің мақсаты қандай екенін қысқаша жазыңыз.", platforms)
		case "en":
			return fmt.Sprintf("Got it, launch for %s. Please share what you sell and the video goal.", platforms)
		default:
			return fmt.Sprintf("Понял, запуск под %s. Подскажите, что продаёте и какая цель ролика.", platforms)
		}
	}

	if sameFields(missing, []string{fieldNiche, fieldPlatforms}) && lead.Goal != "" && lead.Deadline != "" {
		switch language {
		case "kk":
			return fmt.Sprintf("Түсіндім: мақсат — %s, мерзім — %s. Қай ниша және қай алаң?", lead.Goal, lead.Deadline)
		case "en":
			return fmt.Sprintf("Got it: goal — %s, launch — %s. What niche and ad platform?", lead.Goal, lead.Deadline)
		default:
			return fmt.Sprintf("Понял: цель — %s, запуск %s. В какой нише работаете и где планируете рекламу?", lead.Goal, lead.Deadline)
		}
	}

	if len(missing) == 1 {
		return singleMissingQuestion(language, missing[0], lead)
	}

	switch language {
	case "kk":
		return fmt.Sprintf("Түсіндім. Тек мынаны нақтылаңыз: %s.", missingFieldsLabel(language, missing))
	case "en":
		return fmt.Sprintf("Got it. Please clarify only: %s.", missingFieldsLabel(language, missing))
	default:
		return fmt.Sprintf("Понял. Уточните только: %s.", missingFieldsLabel(language, missing))
	}
}

func singleMissingQuestion(language string, field string, lead LeadState) string {
	language = normalizeLanguageCode(language)
	switch field {
	case fieldNiche:
		switch language {
		case "kk":
			return "Қай нишаға ролик керек?"
		case "en":
			return "Which niche is the video for?"
		default:
			return "Для какой ниши нужен ролик?"
		}
	case fieldGoal:
		switch language {
		case "kk":
			return "Негізгі мақсат қандай: өтінім, сату немесе танымалдық?"
		case "en":
			return "What is the main goal: leads, sales, or awareness?"
		default:
			return "Какая цель ролика: заявки, продажи или узнаваемость?"
		}
	case fieldPlatforms:
		switch language {
		case "kk":
			return "Роликті қай жерде қолданасыз: Instagram, TikTok, Facebook, WhatsApp, сайт немесе басқа платформа?"
		case "en":
			return "Where will you use the video: Instagram, TikTok, Facebook, WhatsApp, website, or another platform?"
		default:
			return "Где планируете запускать ролик: Instagram, TikTok, Facebook, WhatsApp, сайт или другая площадка?"
		}
	case fieldDeadline:
		switch language {
		case "kk":
			return "Іске қосу мерзімі қандай?"
		case "en":
			return "What launch timeline should we plan for?"
		default:
			return "В какие сроки нужно запустить ролик?"
		}
	case fieldPreviousAIAds:
		return askPreviousAIWithDiagnosis(language, lead)
	case fieldPackage, fieldPackageInterest:
		switch language {
		case "kk":
			return "Қай пакет қызықты: Test, Basic немесе Standard? Егер білмесеңіз, менеджер ыңғайлысын ұсына алады."
		case "en":
			return "Which package is interesting: Test, Basic, or Standard? If you are not sure, the manager can recommend one."
		default:
			return "Какой пакет интересен: Test, Basic или Standard? Если не уверены, менеджер подскажет подходящий."
		}
	}
	return askMissingFieldsReply(language, lead, lead.MissingCoreFields())
}

func managerMissingFieldsReply(language string, lead LeadState, missing []string, wantsQuestionnaire bool) string {
	language = normalizeLanguageCode(language)
	missing = normalizeFieldList(missing)
	if len(missing) == 1 {
		if wantsQuestionnaire {
			switch language {
			case "kk":
				return "Жақсы, анкетаны ашып беремін. Тек нақтылау үшін: " + lowerFirst(singleMissingQuestion(language, missing[0], lead))
			case "en":
				return "Good, I will open the questionnaire. One detail first: " + lowerFirst(singleMissingQuestion(language, missing[0], lead))
			default:
				return "Хорошо, анкету откроем. Только уточню: " + lowerFirst(singleMissingQuestion(language, missing[0], lead))
			}
		}
		return singleMissingQuestion(language, missing[0], lead)
	}

	switch language {
	case "kk":
		if wantsQuestionnaire {
			return "Жақсы, анкетаны ашып беремін. Дұрыс толтыру үшін нақтылап алайын: " + missingFieldsLabel(language, missing) + "."
		}
		return "Түсіндім. Дұрыс жіберу үшін нақтылаңыз: " + missingFieldsLabel(language, missing) + "."
	case "en":
		if wantsQuestionnaire {
			return "Good, I will open the questionnaire. To send it correctly, please clarify: " + missingFieldsLabel(language, missing) + "."
		}
		return "Got it. To prepare this correctly, please clarify: " + missingFieldsLabel(language, missing) + "."
	default:
		if wantsQuestionnaire {
			return "Хорошо, анкету откроем. Чтобы передать задачу правильно, уточните: " + missingFieldsLabel(language, missing) + "."
		}
		return "Понял. Чтобы передать задачу правильно, уточните: " + missingFieldsLabel(language, missing) + "."
	}
}

func negativeMissingReply(language string, lead LeadState, field string) string {
	if field == "" && lead.PreviousAIAds == nil {
		field = fieldPreviousAIAds
	}

	language = normalizeLanguageCode(language)
	if field == "" {
		switch language {
		case "kk":
			return "Түсіндім. Қайталамаймын. Қаласаңыз, бірден қысқа брифке өтейік."
		case "en":
			return "Understood. I will not repeat it. We can move straight to the brief."
		default:
			return "Понял вас. Не буду повторяться. Можем сразу перейти к короткому брифу."
		}
	}

	question := singleMissingQuestion(language, field, lead)
	switch language {
	case "kk":
		return "Түсіндім. Қайталамаймын. Тек жетіспейтінін нақтылаймын: " + lowerFirst(question)
	case "en":
		return "Understood. I will not repeat it. One missing detail: " + lowerFirst(question)
	default:
		return "Понял вас. Не буду повторяться. Уточню только недостающее: " + lowerFirst(question)
	}
}

func unclearAfterAskedReply(language string, lead LeadState) string {
	if len(lead.MissingCoreFields()) == 0 {
		switch normalizeLanguageCode(language) {
		case "kk":
			return "Түсіндім. Деректер бар, қысқа брифке өте аламыз."
		case "en":
			return "Got it. The details are enough; we can move to the short brief."
		default:
			return "Понял. Данные уже есть, можем перейти к короткому брифу для запуска."
		}
	}

	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім. Қайталамаймын. Бір хабарламада міндетті қысқаша сипаттасаңыз, форматты ұсынамын."
	case "en":
		return "Got it. I will not repeat the questions. Send the task in one message and I will suggest the format."
	default:
		return "Понял. Не буду повторять вопросы. Опишите задачу одним сообщением, и я предложу подходящий формат."
	}
}

func portfolioAlreadySentText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Мысалды жоғарыда жібергенмін. Қаласаңыз, соның негізінде нишаңызға формат таңдаймыз."
	case "en":
		return "I already sent the example above. We can use it to choose the right format for your niche."
	default:
		return "Пример уже отправлял выше. Можем по нему подобрать формат под вашу нишу."
	}
}

func askPreviousAIWithDiagnosis(language string, lead LeadState) string {
	language = normalizeLanguageCode(language)
	niche := strings.TrimSpace(lead.Niche)
	platforms := formatPlatformList(lead.Platforms, language)
	if niche == "" {
		niche = "вашей ниши"
	}
	if platforms == "" {
		platforms = "вашей площадки"
	}

	switch language {
	case "kk":
		return fmt.Sprintf("Жақсы. %s үшін %s-қа динамикалық ролик сай келеді. Бұрын AI ролик қолдандыңыз ба?", niche, platforms)
	case "en":
		return fmt.Sprintf("Great. For %s, a dynamic ad video for %s fits well. Have you used AI videos before?", niche, platforms)
	default:
		return fmt.Sprintf("Отлично. Для %s подойдёт динамичный рекламный ролик под %s. Ранее использовали ИИ-ролики в рекламе?", niche, platforms)
	}
}

func previousAIOffer(language string, usedBefore bool) string {
	language = normalizeLanguageCode(language)
	if usedBefore {
		switch language {
		case "kk":
			return "Онда мақсатқа сай формат таңдаймыз: тестілік 35 000 тг, базалық 50 000 тг немесе стандарт 75 000 тг."
		case "en":
			return "Then we choose by goal: Test 35,000 KZT, Basic 50,000 KZT, or Standard 75,000 KZT."
		default:
			return "Тогда подберём формат под цель: тестовый 35 000 тг, базовый 50 000 тг или стандарт 75 000 тг."
		}
	}

	switch language {
	case "kk":
		return "Онда 35 000 тг тестілік формат ұсынамын — Basic немесе Standard алдында креативті тексереміз."
	case "en":
		return "Then I recommend the Test format for 35,000 KZT to validate the creative first."
	default:
		return "Тогда рекомендую тестовый формат за 35 000 тг — проверить креатив перед Базовым или Стандартом."
	}
}

func packagePriceText(language string, level int) string {
	language = normalizeLanguageCode(language)
	switch level {
	case 1:
		switch language {
		case "kk":
			return "Тестілік формат — 35 000 тг. Креативті тез тексеріп, кейін масштабтауға ыңғайлы."
		case "en":
			return "Test format is 35,000 KZT. It works well to validate the creative before scaling."
		default:
			return "Тестовый формат — 35 000 тг. Подходит, чтобы быстро проверить креатив перед масштабированием."
		}
	case 2:
		switch language {
		case "kk":
			return "Базалық формат — 50 000 тг. Толық AI-сахналармен динамикалық жарнама керек болса ыңғайлы."
		case "en":
			return "Basic format is 50,000 KZT. It fits a more dynamic ad with full AI scenes."
		default:
			return "Базовый формат — 50 000 тг. Подходит, если нужен более динамичный ролик с AI-сценами."
		}
	default:
		switch language {
		case "kk":
			return "Стандарт / премиум формат — 75 000 тг бастап. Жарнамаға және масштабтауға мықты ролик керек болса ыңғайлы."
		case "en":
			return "Standard / premium format starts from 75,000 KZT. It fits stronger ads and scaling."
		default:
			return "Стандарт / премиум формат — от 75 000 тг. Он подходит, если нужен сильный ролик под рекламу и масштабирование."
		}
	}
}

func packageDetailText(language string, level int) string {
	language = normalizeLanguageCode(language)
	switch level {
	case 1:
		switch language {
		case "kk":
			return "Test — 35 000 тг. Бірінші креативті тез тексеруге ыңғайлы, кейін Basic немесе Standard-қа масштабтаймыз."
		case "en":
			return "Test is 35,000 KZT. It is best for quickly checking the first creative before scaling to Basic or Standard."
		default:
			return "Test за 35 000 тг — быстрый формат, чтобы проверить первый креатив. Если реакция хорошая, дальше масштабируем в Basic или Standard."
		}
	case 2:
		switch language {
		case "kk":
			return "Basic — 50 000 тг. Сценарий мен визуал Test-тен тереңірек, жарнамаға дайын динамикалық роликке ыңғайлы."
		case "en":
			return "Basic is 50,000 KZT. It has deeper script and visuals than Test, so it fits a ready ad launch better."
		default:
			return "Basic за 50 000 тг глубже прорабатывается по сценарию и визуалу, чем Test. Он лучше подходит, если ролик сразу нужен под рекламу."
		}
	default:
		switch language {
		case "kk":
			return "Standard — 75 000 тг бастап. Көріністер, монтаж және жарнамалық визуал тереңірек, масштабтауға ыңғайлы."
		case "en":
			return "Standard starts from 75,000 KZT. It has deeper scenes, editing, and ad-level visuals for scaling."
		default:
			return "Standard — от 75 000 тг. Там больше сцен, детальнее монтаж и сильнее рекламный визуал, если ролик нужен под масштабирование."
		}
	}
}

func shortPriceReminderText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Баға 35 000 тг бастап. Мықты жарнамалық ролик керек болса, стандарт / премиум 75 000 тг бастап."
	case "en":
		return "Pricing starts from 35,000 KZT. For a stronger ad video, Standard / premium starts from 75,000 KZT."
	default:
		return "Стоимость начинается от 35 000 тг. Если нужен сильный ролик под рекламу, стандарт / премиум — от 75 000 тг."
	}
}

type quantityDiscountReply struct {
	text        string
	templateID  string
	askedFields []string
}

func quantityDiscountResponse(language string, lead LeadState) quantityDiscountReply {
	language = normalizeLanguageCode(language)
	quantity := formatVideoQuantityForReply(lead.VideoQuantity)
	niche := quantityDiscountNicheForReply(lead)
	missing := qualificationMissingFields(lead)
	askedFields := normalizeFieldList(missing)

	parts := make([]string, 0, 3)
	switch language {
	case "kk":
		if quantity != "" && niche != "" {
			parts = append(parts, fmt.Sprintf("Түсіндім, %s, көлемі %s ролик.", niche, quantity))
		} else if quantity != "" {
			parts = append(parts, fmt.Sprintf("Түсіндім, көлемі %s ролик.", quantity))
		}
		if quantity == "" {
			parts = append(parts, "Көлем бойынша пакеттік шарттарды жеке талқылай аламыз.")
			parts = append(parts, "Нақты құнын міндет пен ролик санына қарай есептеген дұрыс.")
		} else {
			parts = append(parts, "Мұндай көлемде шарттарды жеке есептеген дұрыс.")
		}
		if followup := quantityDiscountFollowupQuestion(language, missing); followup != "" {
			parts = append(parts, followup)
		}
	case "en":
		if quantity != "" && niche != "" {
			parts = append(parts, fmt.Sprintf("Got it: %s, %s videos.", niche, quantity))
		} else if quantity != "" {
			parts = append(parts, fmt.Sprintf("Got it, %s videos.", quantity))
		}
		if quantity == "" {
			parts = append(parts, "For volume, we can discuss package terms individually.")
			parts = append(parts, "The exact cost is better calculated for the task and number of videos.")
		} else {
			parts = append(parts, "For that volume, terms are better calculated individually.")
		}
		if followup := quantityDiscountFollowupQuestion(language, missing); followup != "" {
			parts = append(parts, followup)
		}
	default:
		if quantity != "" && niche != "" {
			parts = append(parts, fmt.Sprintf("Понял, %s, объём %s роликов.", niche, quantity))
		} else if quantity != "" {
			parts = append(parts, fmt.Sprintf("Понял, объём %s роликов.", quantity))
		}
		if quantity == "" {
			parts = append(parts, "По объёму можем обсудить пакетные условия индивидуально.")
			parts = append(parts, "Точную стоимость лучше посчитать под задачу и количество роликов.")
		} else {
			parts = append(parts, "По такому количеству условия лучше посчитать индивидуально.")
		}
		if followup := quantityDiscountFollowupQuestion(language, missing); followup != "" {
			parts = append(parts, followup)
		}
	}

	return quantityDiscountReply{
		text:        strings.Join(parts, " "),
		templateID:  quantityDiscountTemplateID(quantity, missing),
		askedFields: askedFields,
	}
}

func quantityDiscountFollowupQuestion(language string, missing []string) string {
	missing = normalizeFieldList(missing)
	switch normalizeLanguageCode(language) {
	case "kk":
		if sameFields(missing, []string{fieldNiche, fieldGoal}) {
			return "Қай ниша және ролик мақсаты қандай: өтінім, сату немесе танымалдық?"
		}
		if sameFields(missing, []string{fieldGoal}) {
			return "Роликтердің негізгі мақсаты қандай: өтінім, сату немесе танымалдық?"
		}
	case "en":
		if sameFields(missing, []string{fieldNiche, fieldGoal}) {
			return "Please share the niche and video goal: leads, sales, or awareness."
		}
		if sameFields(missing, []string{fieldGoal}) {
			return "What is the main goal of the videos: leads, sales, or awareness?"
		}
	default:
		if sameFields(missing, []string{fieldNiche, fieldGoal}) {
			return "Подскажите, пожалуйста, нишу и цель роликов: заявки, продажи или узнаваемость?"
		}
		if sameFields(missing, []string{fieldGoal}) {
			return "Какая основная цель роликов: заявки, продажи или узнаваемость?"
		}
	}
	if sameFields(missing, []string{fieldNiche}) {
		return singleMissingQuestion(language, fieldNiche, LeadState{})
	}
	return ""
}

func quantityDiscountTemplateID(quantity string, missing []string) string {
	templateID := "quantity_discount_individual"
	if strings.TrimSpace(quantity) != "" {
		templateID += "_with_quantity"
	} else {
		templateID += "_without_quantity"
	}
	missing = normalizeFieldList(missing)
	if len(missing) > 0 {
		templateID += "_ask_" + strings.Join(missing, "_")
	}
	return templateID
}

func quantityDiscountNicheForReply(lead LeadState) string {
	value := strings.TrimSpace(lead.ProductOrService)
	if value == "" {
		value = strings.TrimSpace(lead.Niche)
	}
	value = normalizeNiche(value)
	return value
}

func formatVideoQuantityForReply(quantity string) string {
	quantity = strings.TrimSpace(quantity)
	if quantity == "" {
		return ""
	}
	return strings.ReplaceAll(quantity, "-", "–")
}

func packageChoiceNoPricesText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Қай форматты бекітеміз: тестілік, базалық немесе стандарт / премиум?"
	case "en":
		return "Which format should we lock in: Test, Basic, or Standard / premium?"
	default:
		return "Какой формат фиксируем: тестовый, базовый или стандарт / премиум?"
	}
}

func continueBriefForPackageText(language string, level int) string {
	label := formatLabel(language, level)
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сәлеметсіз бе. Иә, " + label + " формат бойынша жалғастырамыз. Қысқа брифке жауап жіберсеңіз, ролик құрылымын дайындаймыз."
	case "en":
		return "Hello. Yes, we can continue with the " + label + " format. Send the short brief answers and we will prepare the video structure."
	default:
		return "Здравствуйте. Да, можем продолжить по " + label + " формату. Отправьте ответы на короткий бриф, и подготовим структуру ролика."
	}
}

func packageSelectedNextStepText(language string, level int) string {
	return QuestionnaireOfferText(language)
}

func continueAfterOfferText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сәлеметсіз бе. Форматтар бойынша жалғастырамыз. Егер жарнамаға мықты ролик керек болса, стандарт жақсы келеді."
	case "en":
		return "Hello. We can continue from the formats. If you need a stronger ad video, Standard fits best."
	default:
		return "Здравствуйте. Можем продолжить по форматам. Если нужен сильный ролик под рекламу, лучше подойдёт стандарт."
	}
}

func continueAfterPortfolioText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сәлеметсіз бе. Портфолионы жібергенмін. Қай формат жақын: тестілік, базалық немесе стандарт?"
	case "en":
		return "Hello. I already sent the portfolio. Which format feels closer: Test, Basic, or Standard?"
	default:
		return "Здравствуйте. Портфолио уже отправлял. Какой формат ближе: тестовый, базовый или стандарт?"
	}
}

func handoffStatusText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сәлеметсіз бе. Бриф қабылданды, енді ролик құрылымы мен визуал бағытын дайындаймыз."
	case "en":
		return "Hello. The brief is received; now we are preparing the video structure and visual direction."
	default:
		return "Здравствуйте. Бриф уже приняли, дальше подготовим структуру ролика и визуальное направление."
	}
}

func refusalText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім, мазаламаймын. Егер AI жарнамалық ролик керек болса, осы чатта жаза аласыз."
	case "en":
		return "Understood, I will not bother you. If you need an AI ad video, message us here."
	default:
		return "Понял, не буду беспокоить. Если понадобится AI-рекламный ролик, можете написать сюда."
	}
}

func fallbackForLead(language string, lead LeadState) string {
	if lead.BriefRequested || lead.SelectedPackage != "" {
		return BriefReminderText(language)
	}
	if missing := lead.MissingCoreFields(); len(missing) > 0 {
		return askMissingFieldsReply(language, lead, missing)
	}
	if lead.PreviousAIAds == nil {
		return askPreviousAIWithDiagnosis(language, lead)
	}
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім. Қысқа брифке өтсек болады."
	case "en":
		return "Understood. We can move to the short brief."
	default:
		return "Понял. Можем перейти к короткому брифу."
	}
}

func portfolioLinksMessage(language string, links PortfolioLinks, requestedLevel int) string {
	language = normalizeLanguageCode(language)
	if requestedLevel > 0 {
		if url := links.URLByLevel(requestedLevel); url != "" {
			switch language {
			case "kk":
				return fmt.Sprintf("%s портфолиосы: %s", formatLabel(language, requestedLevel), url)
			case "en":
				return fmt.Sprintf("%s portfolio: %s", formatLabel(language, requestedLevel), url)
			default:
				return fmt.Sprintf("Портфолио формата «%s»: %s", formatLabel(language, requestedLevel), url)
			}
		}
		switch language {
		case "kk":
			return "Бұл форматқа сілтеме конфигурацияда жоқ. Видеоны осы жерде жібере аламын."
		case "en":
			return "This format link is not configured. I can send the video here."
		default:
			return "Ссылка на этот формат не настроена. Могу отправить видео здесь."
		}
	}

	if !links.HasAny() {
		switch language {
		case "kk":
			return "Портфолионы формат бойынша жібере аламын: тестілік, базалық немесе стандарт. Қайсысын көрсетейін?"
		case "en":
			return "I can send portfolio by format: Test, Basic, or Standard. Which one should I show?"
		default:
			return "Портфолио могу отправить по форматам: тестовый, базовый или стандарт. Какой показать?"
		}
	}

	parts := make([]string, 0, 3)
	for level := 1; level <= 3; level++ {
		if url := links.URLByLevel(level); url != "" {
			parts = append(parts, fmt.Sprintf("%s — %s", formatLabel(language, level), url))
		}
	}
	switch language {
	case "kk":
		return "Портфолио: " + strings.Join(parts, "; ") + ". Қай формат жақын?"
	case "en":
		return "Portfolio: " + strings.Join(parts, "; ") + ". Which format is closer?"
	default:
		return "Портфолио: " + strings.Join(parts, "; ") + ". Какой формат ближе?"
	}
}

func (links PortfolioLinks) HasAny() bool {
	return strings.TrimSpace(links.TestURL) != "" ||
		strings.TrimSpace(links.BasicURL) != "" ||
		strings.TrimSpace(links.StandardURL) != ""
}

func (links PortfolioLinks) URLByLevel(level int) string {
	switch level {
	case 1:
		return strings.TrimSpace(links.TestURL)
	case 2:
		return strings.TrimSpace(links.BasicURL)
	case 3:
		return strings.TrimSpace(links.StandardURL)
	default:
		return ""
	}
}

func requestedLevelFromText(text string) int {
	normalized := normalizeForAnalysis(text)
	switch {
	case containsAny(normalized, []string{"тест", "test", "тестілік", "первый вариант", "1 вариант", "номер 1", "первый"}):
		return 1
	case containsAny(normalized, []string{"базов", "basic", "базалық", "второй вариант", "2 вариант", "номер 2", "второй"}):
		return 2
	case containsAny(normalized, []string{"стандарт", "премиум", "standard", "premium", "третий вариант", "3 вариант", "номер 3", "третий"}):
		return 3
	default:
		return 0
	}
}

func formatLabel(language string, level int) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		switch level {
		case 1:
			return "тестілік"
		case 2:
			return "базалық"
		default:
			return "стандарт / премиум"
		}
	case "en":
		switch level {
		case 1:
			return "Test"
		case 2:
			return "Basic"
		default:
			return "Standard"
		}
	default:
		switch level {
		case 1:
			return "тестовый"
		case 2:
			return "базовый"
		default:
			return "стандарт / премиум"
		}
	}
}

func missingFieldsLabel(language string, fields []string) string {
	labels := make([]string, 0, len(fields))
	for _, field := range fields {
		switch normalizeLanguageCode(language) {
		case "kk":
			switch field {
			case fieldNiche:
				labels = append(labels, "ниша")
			case fieldGoal:
				labels = append(labels, "мақсат")
			case fieldPlatforms:
				labels = append(labels, "алаң")
			case fieldDeadline:
				labels = append(labels, "мерзім")
			case fieldPackage, fieldPackageInterest:
				labels = append(labels, "пакет")
			}
		case "en":
			switch field {
			case fieldNiche:
				labels = append(labels, "niche")
			case fieldGoal:
				labels = append(labels, "goal")
			case fieldPlatforms:
				labels = append(labels, "platform")
			case fieldDeadline:
				labels = append(labels, "timeline")
			case fieldPackage, fieldPackageInterest:
				labels = append(labels, "package")
			}
		default:
			switch field {
			case fieldNiche:
				labels = append(labels, "нишу")
			case fieldGoal:
				labels = append(labels, "цель")
			case fieldPlatforms:
				labels = append(labels, "площадку")
			case fieldDeadline:
				labels = append(labels, "сроки")
			case fieldPackage, fieldPackageInterest:
				labels = append(labels, "пакет")
			}
		}
	}
	return joinHuman(labels, normalizeLanguageCode(language))
}

func formatPlatformList(platforms []string, language string) string {
	if len(platforms) == 0 {
		return ""
	}
	filtered := make([]string, 0, len(platforms))
	hasMediaPlatform := false
	for _, platform := range platforms {
		if !strings.Contains(strings.ToLower(platform), "таргет") {
			hasMediaPlatform = true
			break
		}
	}
	for _, platform := range platforms {
		if hasMediaPlatform && strings.Contains(strings.ToLower(platform), "таргет") {
			continue
		}
		filtered = append(filtered, platform)
	}
	return joinHuman(filtered, normalizeLanguageCode(language))
}

func joinHuman(values []string, language string) string {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			clean = append(clean, value)
		}
	}
	switch len(clean) {
	case 0:
		return ""
	case 1:
		return clean[0]
	case 2:
		separator := " и "
		if language == "kk" {
			separator = " және "
		}
		if language == "en" {
			separator = " and "
		}
		return clean[0] + separator + clean[1]
	default:
		last := clean[len(clean)-1]
		head := strings.Join(clean[:len(clean)-1], ", ")
		separator := " и "
		if language == "kk" {
			separator = " және "
		}
		if language == "en" {
			separator = ", and "
		}
		return head + separator + last
	}
}

func normalizeLanguageCode(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "kk", "kz":
		return "kk"
	case "en":
		return "en"
	default:
		return "ru"
	}
}

func sameFields(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
	}
	return true
}

func lowerFirst(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	runes := []rune(text)
	runes[0] = []rune(strings.ToLower(string(runes[0])))[0]
	return string(runes)
}
