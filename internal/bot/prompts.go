package bot

const SystemPrompt = `Ты — премиальный WhatsApp-менеджер Stone production.

Stone production делает AI-рекламные ролики за 48 часов без съёмки, под запуск рекламы.
Цель диалога: понять задачу клиента, ответить на прямой вопрос, сохранить данные лида и довести горячего клиента до короткого брифа или менеджера.

You are not a questionnaire bot. You are a human-like WhatsApp sales manager. Always understand the client’s message first, extract all useful information, update the lead state, answer the client’s direct question, and only then ask the minimum missing question if needed. Never repeat questions for fields that are already known or already asked. Never restart the funnel for an existing chat. Never change the existing video/package sending logic.

Всегда учитывай состояние диалога JSON:
- stage и lead_status: neutral, new, warm, hot, handoff_required, closed, muted;
- lead: niche, goal, platform/platforms, deadline, ai_experience, selected_package, budget, target_audience, client_name, notes;
- completed_fields: уже собранные поля;
- asked_fields: вопросы, которые уже задавали;
- sent_videos и portfolio_sent: какие примеры уже отправлялись;
- brief_requested/brief_completed: статус брифа;
- последние сообщения диалога.

Жёсткие правила:
- не спрашивай выбор языка; отвечай на ru, kk или en из текущего состояния;
- не начинай диалог заново для существующего chatID;
- не спрашивай niche, goal, platform, deadline, ai_experience, package или brief, если поле уже заполнено или уже спрашивалось;
- если не хватает platform, спрашивай с примерами: Instagram, TikTok, Facebook, WhatsApp, сайт;
- сначала отвечай на прямой вопрос клиента: цена, примеры, сроки, пакет, Instagram/TikTok, как работает;
- одно входящее сообщение = максимум один текстовый reply;
- reply: 1–4 коротких предложения, WhatsApp-формат, без давления и длинных списков;
- если клиент warm/hot, не возвращайся к квалификации; двигай к примеру, пакету, брифу или менеджеру;
- если selected_package заполнен, не продавай заново и не повторяй цены; переходи к короткому брифу;
- если transferred_to_manager=true, automation_closed=true, brief_completed=true или lead_status=handoff_required, не продолжай продажу и не начинай квалификацию заново;
- если клиент просит оператора, админа, менеджера, живого человека, звонок или пишет срочно связаться — это прямой запрос человека: stage=handoff_required, lead_status=handoff_required, need_human=true; сначала подтверди подключение менеджера и не спрашивай пакет/портфолио заново;
- не передавай менеджеру и не ставь handoff_required, если нет валидных niche, goal, deadline и selected_package/package_interest;
- "давайте откроем анкету" означает intent to proceed, но не полную квалификацию; если поля отсутствуют, спроси только missing_fields;
- не считай односимвольные или мусорные значения вроде "м", "-", ".", "не знаю" валидной niche/goal;
- если пакет не выбран и клиент пишет, что менеджер подскажет, package_interest = "needs_manager_recommendation";
- не отправляй видео повторно, если оно уже есть в sent_videos, кроме явной просьбы отправить ещё раз;
- не выдумывай ссылки и файлы, используй только текущую механику send_videos.

Цены и видео:
- Test — 35 000 тг — video_level_1.mp4;
- Basic — 50 000 тг — video_level_2.mp4;
- Standard — от 75 000 тг — video_level_3.mp4.

Короткий бриф RU:
"Отлично, тогда короткий бриф:
1) Что рекламируем и в чём главная ценность?
2) Какая боль/желание у вашей аудитории?
3) Какой оффер показать в ролике?
Можете также отправить Instagram/сайт."

Формат ответа всегда строго JSON:
{
  "reply": "короткий ответ клиенту",
  "language": "ru|kk|en",
  "stage": "new_lead|qualification|platform_detected|ai_experience_checked|package_suggested|portfolio_sent|package_selected|brief_requested|brief_collected|handoff_required|objection|closing|muted|diagnosis|offer|portfolio|offtopic",
  "recommended_level": 0,
  "send_videos": [],
  "ask_brief": false,
  "need_human": false,
  "lead_status": "neutral|new|warm|hot|handoff_required|closed|muted",
  "completed_fields": [],
  "asked_fields": []
}

Нельзя возвращать markdown вне JSON.
Нельзя добавлять объяснения вне JSON.`

func FallbackText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Рақмет. Нишаны, мақсатты және іске қосу мерзімін қысқаша жазыңыз."
	case "en":
		return "Thanks. Please share the niche, video goal, platform, and launch timeline."
	default:
		return "Понял. Уточните нишу, цель ролика, площадку и сроки запуска."
	}
}

func NonTextFallbackText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Өтінемін, нишаны, мақсатты және іске қосу мерзімін мәтінмен жазыңыз."
	case "en":
		return "Please send text: niche, video goal, platform, and launch timeline."
	default:
		return "Пожалуйста, напишите текстом нишу, цель ролика, площадку и сроки запуска."
	}
}

func LanguageChoiceText() string {
	return "Тілді таңдаңыз / Выберите язык / Choose language:\n1) Қазақша\n2) Русский\n3) English"
}

func QualificationGreetingText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сәлеметсіз бе! Хабарласқаныңызға рахмет 🙌\n\n48 сағатта түсірілімсіз ИИ жарнамалық ролик жасаймыз, жарнамаға дайын.\n\nБаға 35 000 тг бастап.\n\nТүсіну үшін айтыңыз:\n1) Қай нишада жұмыс істейсіз?\n2) Мақсат — өтінім / сату / танымалдық?\n3) Қашан іске қосу керек?"
	case "en":
		return "Hello! Thanks for reaching out 🙌\n\nWe create AI ad videos in 48 hours without filming, ready to launch ads.\n\nPricing starts from 35,000 KZT.\n\nTo see if this fits, please share:\n1) What niche are you in?\n2) Goal — leads / sales / awareness?\n3) When do you need to launch?"
	default:
		return "Здравствуйте! Спасибо за обращение 🙌\n\nДелаем ИИ рекламные ролики за 48 часов без съёмки, под запуск рекламы.\n\nСтоимость от 35 000 тг.\n\nЧтобы понять, подойдём ли мы вам, подскажите:\n1) В какой нише работаете?\n2) Какая цель — заявки / продажи / узнаваемость?\n3) В какие сроки нужно запустить?"
	}
}

func PriceText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "3 формат бар: Test — 35 000 тг, Basic — 50 000 тг, Standard — 75 000 тг бастап. Алғашқы іске қосуға әдетте Test ұсынамын."
	case "en":
		return "There are 3 formats: Test — 35,000 KZT, Basic — 50,000 KZT, Standard — from 75,000 KZT. For a first launch, I usually recommend Test."
	default:
		return "У нас 3 формата: Test — 35 000 тг, Basic — 50 000 тг, Standard — от 75 000 тг. Для первого запуска обычно советуем Test: быстро проверить креатив и реакцию аудитории."
	}
}

func PortfolioIntroText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Иә, AI-ролик мысалын жіберемін. Көргеннен кейін нишаңызға ыңғайлы форматты таңдай аламыз."
	case "en":
		return "Yes, I am sending an AI video example. After viewing it, we can choose the best format for your niche."
	default:
		return "Да, отправляю пример нашего AI-ролика. После просмотра можем подобрать формат под вашу нишу."
	}
}

func BriefText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Керемет, онда қысқа бриф:\n1) Нені жарнамалаймыз және басты құндылығы қандай?\n2) Аудиторияның қандай мәселесі/қалауы бар?\n3) Роликте қандай оффер көрсету керек?\nInstagram/сайт жіберсеңіз болады."
	case "en":
		return "Great, then a short brief:\n1) What are we advertising and what is the main value?\n2) What pain/desire does your audience have?\n3) What offer should we show in the video?\nYou can also send Instagram/website."
	default:
		return "Отлично, тогда короткий бриф:\n1) Что рекламируем и в чём главная ценность?\n2) Какая боль/желание у вашей аудитории?\n3) Какой оффер показать в ролике?\nМожете также отправить Instagram/сайт."
	}
}

func BriefTextForPackage(language string, level int) string {
	label := formatLabel(language, level)
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Керемет, " + label + " формат аламыз. Қысқа бриф:\n1) Нені жарнамалаймыз және басты құндылығы қандай?\n2) Аудиторияның қандай мәселесі/қалауы бар?\n3) Қандай оффер көрсету керек?\nInstagram/сайт жіберсеңіз болады."
	case "en":
		return "Great, we will take the " + label + " format. Short brief:\n1) What are we advertising and what is the main value?\n2) What pain/desire does your audience have?\n3) What offer should we show?\nYou can also send Instagram/website."
	default:
		return "Отлично, берём " + label + " формат. Короткий бриф:\n1) Что рекламируем и в чём главная ценность?\n2) Какая боль/желание у вашей аудитории?\n3) Какой оффер показать в ролике?\nМожете также отправить Instagram/сайт."
	}
}

func ClarifyPackageText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Қай форматты аламыз: тестілік 35 000 тг, базалық 50 000 тг немесе стандарт 75 000 тг?"
	case "en":
		return "Which format should we take: Test 35,000 KZT, Basic 50,000 KZT, or Standard 75,000 KZT?"
	default:
		return "Какой формат берём: тестовый 35 000 тг, базовый 50 000 тг или стандарт 75 000 тг?"
	}
}

func BriefReminderText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Жақсы, күтемін. Қысқа брифке жауап берсеңіз, менеджер ролик құрылымын дайындайды."
	case "en":
		return "Great, I am waiting. Send the short brief answers and the manager will prepare the video structure."
	default:
		return "Отлично, жду. Ответьте на короткий бриф, и менеджер подготовит структуру ролика."
	}
}

func BriefCollectedText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Қабылдадым, рақмет. Деректерді Stone production менеджеріне беремін — әрі қарай детальдарды нақтылап, роликті дайындаймыз."
	case "en":
		return "Received, thank you. I am passing the details to the Stone production manager — next we will clarify details and prepare the video."
	default:
		return "Принял, спасибо. Передаю данные менеджеру Stone production — дальше уточним детали и подготовим ролик."
	}
}

func HumanHandoffText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім, менеджерді қосамын. Ол осы чатта жауап беріп, тапсырысты нақтылайды."
	case "en":
		return "Got it, I am connecting a manager. They will reply here and help with the order."
	default:
		return "Понял, подключаю менеджера Stone production. Он ответит в этом чате и поможет с заказом."
	}
}

func ObjectionText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсінемін. Сондықтан 35 000 тг тестілік формат ұсынамыз — алдымен үлкен бюджетсіз креативті тексеру үшін."
	case "en":
		return "I understand. That is why we offer the Test format for 35,000 KZT: first validate the creative without a large budget."
	default:
		return "Понимаю. Поэтому мы и предлагаем тестовый формат за 35 000 тг, чтобы сначала проверить креатив без большого бюджета."
	}
}

func QuestionnaireOfferText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Анкетаны толтырсаңыз, біз тегін сценарий жазып, концепт дайындаймыз. Содан кейін қай формат ыңғайлы екенін таңдайсыз.\n\n1 минут алады.\n\nАнкетаны жіберейін бе?"
	case "en":
		return "You can fill in a short questionnaire, we will write a free script and prepare the concept. Then you choose the format that fits.\n\nIt takes 1 minute.\n\nShould I send it?"
	default:
		return "Вы можете заполнить анкету, мы пропишем вам бесплатный сценарий, далее подготовим концепт, и уже выберете, какой формат вам подходит.\n\nЭто займет 1 минуту.\n\nОтправить анкету?"
	}
}

func packageOptionsText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Форматтар:\nTest — 35 000 тг\nBasic — 50 000 тг\nStandard — 75 000 тг"
	case "en":
		return "Packages:\nTest — 35,000 KZT\nBasic — 50,000 KZT\nStandard — 75,000 KZT"
	default:
		return "Пакеты:\nTest — 35 000 тг\nBasic — 50 000 тг\nStandard — 75 000 тг"
	}
}

func testPackageRecommendationText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Егер алғаш рет іске қосып, форматты тексергіңіз келсе, “Тестовый” пакетін ұсынамын."
	case "en":
		return "If you want to launch for the first time and test the format, I recommend the “Test” package."
	default:
		return "Если вы хотите запустить впервые и протестировать, предлагаю пакет “Тестовый”."
	}
}

func packagesPresentedFallbackText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім. Егер формат ұнаса, қысқа анкетаны жіберемін де, сізге тегін сценарий дайындаймыз."
	case "en":
		return "Got it. If the format works for you, I can send a short questionnaire and we will prepare a free script."
	default:
		return "Понял вас. Если формат подходит, отправлю короткую анкету — по ней бесплатно подготовим сценарий."
	}
}

func questionnaireConfirmationFallbackText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Жақсы. Анкетаны жіберейін бе?"
	case "en":
		return "Good. Should I send the questionnaire?"
	default:
		return "Хорошо. Отправить анкету?"
	}
}

func OfftopicText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Мен тек Stone production ИИ-жарнамалық роликтері бойынша кеңес беремін. Ролик қай нишаға керек?"
	case "en":
		return "I consult only on Stone production AI ad videos. Which niche is the video for?"
	default:
		return "Я консультирую только по ИИ-рекламным роликам Stone production. Подскажите, для какой ниши нужен ролик?"
	}
}

func VideoUnavailableText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Видео уақытша жіберілмеді. Форматты мәтінмен сипаттап, брифті қабылдай аламын."
	case "en":
		return "The video did not send for now. I can describe the format and take the brief."
	default:
		return "Видео временно не отправилось. Могу описать формат текстом и принять бриф."
	}
}

func FormatSendingText(level int, language string) string {
	if normalizeLanguageCode(language) == "kk" {
		switch level {
		case 1:
			return "Жіберемін: тестілік формат және осы деңгейдегі видео."
		case 2:
			return "Жіберемін: базалық формат және осы деңгейдегі видео."
		default:
			return "Жіберемін: стандарт / премиум формат және осы деңгейдегі видео."
		}
	}
	if normalizeLanguageCode(language) == "en" {
		switch level {
		case 1:
			return "Sending the Test format and video of this level."
		case 2:
			return "Sending the Basic format and video of this level."
		default:
			return "Sending the Standard format and video of this level."
		}
	}

	switch level {
	case 1:
		return "Отправляю тестовый формат и видео этого уровня."
	case 2:
		return "Отправляю базовый формат и видео этого уровня."
	default:
		return "Отправляю стандарт / премиум формат и видео этого уровня."
	}
}
