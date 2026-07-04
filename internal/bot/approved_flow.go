package bot

import "strings"

const (
	faqHowWork      = "how_work"
	faqAIProduction = "ai_production"
	faqRealistic    = "realistic"
	faqTimeline     = "timeline"
	faqNoShooting   = "no_shooting"
	faqIncluded     = "included"
	faqAds          = "ads"
	faqRevisions    = "revisions"
	faqChangeIdea   = "change_idea"
	faqQuality      = "quality"
	faqAIErrors     = "ai_errors"
	faqPayment      = "payment"
	faqOnline       = "online"
	faqAIVsShooting = "ai_vs_shooting"
	faqDuration     = "duration"
)

func QualificationQuestionsText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Роликті дәл сіздің міндетіңізге сай жасау үшін қысқаша жазыңыз:\n\n— Не сатасыз / қай ниша?\n— Роликтің мақсаты қандай: өтінім, сату немесе танымалдық?"
	case "en":
		return "To make the video fit your task, please share briefly:\n\n— What do you sell / what is your niche?\n— What is the video goal: leads, sales, or awareness?"
	default:
		return "Чтобы сделать ролик точно под вашу задачу, напишите, пожалуйста, кратко:\n\n— Что продаёте / какая ниша?\n— Какая цель ролика: заявки, продажи или узнаваемость?"
	}
}

func FormatQuestionText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Қай формат ұнады?"
	case "en":
		return "Which format did you like?"
	default:
		return "Какой формат вам понравился?"
	}
}

func QuestionnaireReminderText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Еске салайын 🙌 Сіздің задачаңызға тегін сценарий мен концепт дайындай аламыз.\n\nАнкета шамамен 1 минут алады.\n\nАнкетаны жіберейін бе?"
	case "en":
		return "A quick reminder 🙌 We can prepare a free script and concept for your task.\n\nThe questionnaire takes about 1 minute.\n\nShould I send it?"
	default:
		return "Напомню 🙌 Можем бесплатно подготовить сценарий и концепт под вашу задачу.\n\nАнкета занимает около 1 минуты.\n\nОтправить анкету?"
	}
}

func WeeklyDiscountFollowupText(language string) string {
	return weeklyDiscountFollowupText
}

const weeklyDiscountFollowupText = "Здравствуйте! 👋\n\n🎬 Сделали новый AI-проект и решили поделиться результатом.\n\n🎁 Для новых клиентов условия можно обсудить индивидуально под задачу.\n\nЕсли интересно протестировать, напишите + в ответ."

func BriefContextReturnText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Жоғарыдағы анкета сұрақтарына жауап бере аласыз — сол бойынша ролик идеясы мен форматын ұсынамыз."
	case "en":
		return "You can answer the questionnaire above, and we will suggest the idea and video format from it."
	default:
		return "Можете ответить на вопросы анкеты выше — по ним предложим идею и формат ролика."
	}
}

func FAQAnswerText(key string, language string) string {
	if normalizeLanguageCode(language) != "ru" {
		return faqAnswerRU(key)
	}
	return faqAnswerRU(key)
}

func faqAnswerRU(key string) string {
	switch key {
	case faqHowWork:
		return "Работаем онлайн — экономим ваше время и делаем процесс максимально прозрачным.\n\nКак всё устроено:\n1️⃣ Сценарий — пишем решение под вашу задачу и утверждаем с вами.\n2️⃣ Материалы — вы присылаете фото/видео продукта (если нет — сделаем без них).\n3️⃣ Концепт — показываем ключевые сцены до генерации, чтобы вы видели будущий стиль.\n4️⃣ Финал — реализуем проект и отдаем готовый ролик.\n\n⏱️ Срок: до 48 часов зависит от сложности проекта\nВсе этапы под вашим контролем! 🤝"
	case faqAIProduction:
		return "🤖 Это AI-production. Ролики создаются с помощью нейросетей без полноценной съемочной команды, но максимально реалистичной картинкой и рекламной подачей."
	case faqRealistic:
		return "🎬 Да. Мы делаем максимально реалистичный AI-визуал на 95% с упором на premium качество."
	case faqTimeline:
		return "⏰ В среднем 2–3 рабочих дня после утверждения концепта с вами и получения всех материалов."
	case faqNoShooting:
		return "🚫 Нет. Большинство проектов делается полностью онлайн без участия клиента в съемочном процессе."
	case faqIncluded:
		return "📦 Сначала мы подготавливаем и утверждаем с вами концепт, сценарий и визуальное направление ролика.\n\nТолько после полного согласования вы производите оплату, и мы приступаем к генерации. В стоимость входит: сценарий, AI-генерация, озвучка, музыка, монтаж. Финальный результат будет максимально соответствовать утвержденному дизайн-концепту."
	case faqAds:
		return "📈 Да. Основная цель наших роликов — привлечение внимания, охваты и заявки через рекламу и соцсети."
	case faqRevisions:
		return "✏️ Да. В стоимость входят 2 итерации бесплатных правок. Сначала мы подготавливаем и утверждаем с вами концепт, сценарий и визуальное направление ролика. Только после полного согласования вы производите оплату, и мы приступаем к генерации."
	case faqChangeIdea:
		return "📌 Полная смена концепции после утверждения и запуска проекта считается новым заказом."
	case faqQuality:
		return "🖥️ Формат полностью готов под Instagram Reels, TikTok и рекламные кабинеты."
	case faqAIErrors:
		return "🤖 Да. Иногда могут быть мелкие AI-погрешности в деталях — это особенность текущих AI технологий."
	case faqPayment:
		return "💳 До 100 000 ₸ — 100% предоплата. Свыше 100 000 ₸ — 50% до старта и 50% перед финальной сдачей."
	case faqOnline:
		return "🌍 Да. Весь процесс — от брифа до сдачи ролика — можно пройти дистанционно."
	case faqAIVsShooting:
		return "⚡ Это быстрее, дешевле и позволяет тестировать креативы без больших затрат на съемочную команду, студии и технику."
	case faqDuration:
		return "Для рекламы у нас обычно 30–45 секунд."
	default:
		return ""
	}
}

func detectFAQIntent(text string) (string, bool) {
	normalized := normalizeForAnalysis(text)
	if normalized == "" {
		return "", false
	}
	// Duration questions ("хронометраж видео какой", "сколько секунд ролик")
	// are direct questions even without a question mark or question word.
	if isDurationQuestion(normalized) {
		return faqDuration, true
	}
	hasQuestionShape := strings.Contains(normalized, "?") ||
		containsAny(normalized, []string{"как", "что", "сколько", "нужно", "надо", "можно", "это", "а если", "чем", "в каком", "выгляд"})
	if !hasQuestionShape {
		return "", false
	}

	switch {
	case containsAny(normalized, []string{"как вы работаете", "как работаете", "как все устроено", "как происходит процесс", "этапы работы", "процесс работы"}):
		return faqHowWork, true
	case containsAny(normalized, []string{"настоящая съемка", "настоящая сьемка", "это ai", "это ии", "нейросет", "без съем", "без сьем", "ai production", "ai-production"}):
		return faqAIProduction, true
	case containsAny(normalized, []string{"реалист", "95", "как настоящее", "выглядит как", "premium качество", "премиум качество"}):
		return faqRealistic, true
	case containsAny(normalized, []string{"сколько делается", "как долго", "сколько времени", "за сколько", "срок изготовления", "когда будет готов", "48 часов", "2-3"}):
		return faqTimeline, true
	case containsAny(normalized, []string{"приезжать", "приехать", "на съемку", "на сьемку", "участвовать в съем", "участвовать в сьем", "нужно быть"}):
		return faqNoShooting, true
	case containsAny(normalized, []string{"что входит", "что включено", "входит в стоимость", "включено в стоимость", "за что плат", "что получу"}):
		return faqIncluded, true
	case isAdsFitQuestion(normalized):
		return faqAds, true
	case containsAny(normalized, []string{"правки", "исправ", "передел", "корректиров", "можно изменить"}):
		return faqRevisions, true
	case containsAny(normalized, []string{"поменять идею", "сменить идею", "полностью поменять", "полная смена", "другая идея", "новая идея"}):
		return faqChangeIdea, true
	case containsAny(normalized, []string{"качестве сдаете", "качестве сдаете", "формат сдаете", "формате сдаете", "reels", "тик ток", "tiktok", "качество видео"}):
		return faqQuality, true
	case containsAny(normalized, []string{"ai ошибается", "ии ошибается", "ошибки ai", "ошибки ии", "погрешности", "артефакт", "косяки нейросет"}):
		return faqAIErrors, true
	case containsAny(normalized, []string{"как оплата", "как происходит оплата", "предоплат", "оплатить", "оплата", "50%", "100%"}):
		return faqPayment, true
	case containsAny(normalized, []string{"полностью онлайн", "можно онлайн", "дистанционно", "удаленно", "онлайн работать", "весь процесс онлайн"}):
		return faqOnline, true
	case containsAny(normalized, []string{"чем ai лучше", "чем ии лучше", "лучше обычной съем", "лучше обычной сьем", "зачем ai", "зачем ии", "обычная съемка"}):
		return faqAIVsShooting, true
	default:
		return "", false
	}
}

func isAdsFitQuestion(normalized string) bool {
	normalized = normalizeForAnalysis(normalized)
	return containsAny(normalized, []string{
		"под рекламу", "для рекламы", "рекламный кабинет", "запуск рекламы",
		"подойдет для рекламы", "подойдёт для рекламы", "подойдет под рекламу", "подойдёт под рекламу",
		"лиды через рекламу",
	})
}

func isBusinessRelevantMessage(text string, analysis CustomerAnalysis, faqDetected bool, conversation Conversation) bool {
	if faqDetected || analysis.HasBusinessSignal() {
		return true
	}
	switch analysis.Intent {
	case IntentGreeting,
		IntentAnswer,
		IntentPortfolioRequest,
		IntentNicheSpecificCaseRequest,
		IntentFeasibilityQuestion,
		IntentConfusion,
		IntentVoiceQuestion,
		IntentCopyrightQuestion,
		IntentFormatPreference,
		IntentPriceQuestion,
		IntentPackageQuestion,
		IntentPackageSelection,
		IntentQuantityDiscountQuestion,
		IntentAgreement,
		IntentRefusal,
		IntentDeadlineQuestion,
		IntentReadyToOrder,
		IntentObjection,
		IntentDefer,
		IntentFrustration,
		IntentNegativeReaction,
		IntentBriefAnswer,
		IntentHumanRequest,
		IntentBusinessLink,
		IntentFormatAdvice,
		IntentMute:
		return true
	}
	normalized := normalizeForAnalysis(text)
	if normalized == "" || isLocalOfftopic(normalized) {
		return false
	}
	if conversation.Stage == ClientStateAwaitingQualification {
		return true
	}
	if conversationIsWaitingForBrief(conversation) {
		return isBriefLikeBusinessText(normalized)
	}
	if conversation.Stage == ClientStatePackagesPresented || conversation.PackagesSent || conversation.SentPortfolio || conversation.Lead.OfferSent || conversation.Lead.PortfolioSent || conversation.QuestionnaireOfferSent {
		return true
	}
	return containsAny(normalized, []string{
		"ролик", "видео", "ai", "ии", "нейросет", "реклама", "креатив", "пакет", "формат",
		"анкета", "бриф", "сценар", "концепт", "instagram", "инстаграм", "сайт",
	})
}

func isBriefLikeBusinessText(normalized string) bool {
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "http") || strings.Contains(normalized, "www") || strings.Contains(normalized, "instagram") || strings.Contains(normalized, "@") {
		return true
	}
	if isNoOfferBriefAnswer(normalized) {
		return true
	}
	return containsAny(normalized, []string{
		"прода", "услуг", "товар", "продукт", "делаем", "клиент", "аудитор", "семьи", "бизнес",
		"предпринимател", "муж", "жен", "девуш", "чек", "преми", "обув", "одежд", "мебель",
		"магазин", "салон", "стоматолог", "оффер", "офер", "акци", "скид", "сильн",
		"сторона", "преимущ", "качество", "заказ",
	})
}
