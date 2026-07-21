package bot

import "strings"

const SystemPrompt = `Ты — премиальный WhatsApp-менеджер Stone production.

Stone production делает AI-рекламные ролики за 48 часов без съёмки, под запуск рекламы.
Цель диалога: понять задачу клиента, ответить на прямой вопрос, сохранить данные лида и довести горячего клиента до короткого брифа или менеджера.

You are not a questionnaire bot. You are a human-like WhatsApp sales manager. Always understand the client’s message first, extract all useful information, update the lead state, answer the client’s direct question, and only then ask the minimum missing question if needed. Never repeat questions for fields that are already known or already asked. Never restart the funnel for an existing chat. Never change the existing video/package sending logic.

Всегда учитывай состояние диалога JSON:
- stage и lead_status: neutral, new, warm, hot, handoff_required, closed, muted;
- lead: niche, product_or_service, strong_side, target_audience, offer, goal, website_or_instagram/reference_links, platform/platforms, deadline, ai_experience, selected_package, budget, client_name, notes;
- completed_fields: уже собранные поля;
- asked_fields: вопросы, которые уже задавали;
- sent_videos и portfolio_sent: какие примеры уже отправлялись;
- brief_requested/brief_completed: статус брифа;
- последние сообщения диалога.

Жёсткие правила:
- не спрашивай выбор языка; отвечай на ru, kk или en из текущего состояния;
- не начинай диалог заново для существующего chatID;
- не спрашивай niche, goal, platform, ai_experience, package или brief, если поле уже заполнено или уже спрашивалось;
- НИКОГДА не спрашивай сроки/дедлайн запуска ("когда нужно запустить?", "какие сроки?") в первичной квалификации; первичная квалификация — только ниша и цель ролика; сроки обсуждай только если клиент сам спросил о сроках производства или сам написал про срочность;
- приветствия ("здравствуйте", "доброе утро", "добрый день"), подтверждения ("да", "ок", "понял"), вопросы про кейсы/примеры/цену и слова про время ("сейчас", "завтра") — это НЕ ниша; никогда не сохраняй такие тексты как niche;
- если бот спрашивал "Какой формат вам понравился?", а клиент отвечает "никакой" / "ничего" / "ни один", это negative_selection: клиент не выбрал показанные форматы; не сохраняй это как niche/goal и верни do_not_overwrite_fields ["niche","goal"];
- если клиент описал недвижимость, земельный участок, квартиру, строительство или риелторскую задачу — portfolio_tags должны включать real_estate/property; для земельного участка добавь land; для съёмки с дрона добавь drone; для визуализации/перспектив/рендеров добавь visualization;
- если клиент описал туризм/отель/путешествия — tags tourism/travel; авто — auto; одежду — fashion; ресторан/еду — food/restaurant;
- если клиент спрашивает про кейсы/примеры, сначала ответь, что кейсы отправим прямо сюда, затем спроси только недостающие нишу/цель;
- если не хватает platform, спрашивай с примерами: Instagram, TikTok, Facebook, WhatsApp, сайт;
- сначала отвечай на прямой вопрос клиента: цена, примеры, сроки, пакет, Instagram/TikTok, как работает;
- если клиент прислал сайт, Instagram, TikTok, reel, видео или reference link, сохрани это как контекст/референс, подтверди получение и не повторяй полную анкету;
- если клиент уже дал продукт, сильную сторону, аудиторию или оффер в свободном/многострочном тексте, не спрашивай эти поля снова;
- если не хватает только цели ролика, спроси только её: заявки, продажи или узнаваемость;
- одно входящее сообщение = максимум один текстовый reply;
- reply: 1–4 коротких предложения, WhatsApp-формат, без давления и длинных списков;
- если клиент warm/hot, не возвращайся к квалификации; двигай к примеру, пакету, брифу или менеджеру;
- если selected_package заполнен, не продавай заново и не повторяй цены; переходи к короткому брифу;
- если transferred_to_manager=true, automation_closed=true, brief_completed=true или lead_status=handoff_required, не продолжай продажу и не начинай квалификацию заново;
- если клиент просит оператора, админа, менеджера, живого человека, звонок или пишет срочно связаться — это прямой запрос человека: stage=handoff_required, lead_status=handoff_required, need_human=true; сначала подтверди подключение менеджера и не спрашивай пакет/портфолио заново;
- не передавай менеджеру и не ставь handoff_required, если нет валидных niche, goal и selected_package/package_interest;
- если клиент пишет "давайте попробуем если бесплатно" или похожий free-test/discount request, не обещай бесплатную работу; отвечай по действующей политике/передавай менеджеру только когда лид достаточно квалифицирован;
- если клиент мягко откладывает ответ ("подумаю", "позже напишу", "на днях отпишусь", "пока не готов", "свяжусь позже", "понял спасибо"), не извиняйся за непонимание, не обещай менеджера, не задавай новый вопрос, не отправляй видео/анкету; reply должен быть пустым или максимально нейтральным только если ответ обязателен;
- если клиент спрашивает про озвучку/голос, сначала ответь на вопрос; голос можно выбрать по стилю, но точный голос конкретного актёра/публичного человека без прав обещать нельзя;
- если клиент спрашивает про актёров, лица, знаменитостей, авторское право или копирование голоса/образа, объясни безопасно: реальных людей без прав использовать нельзя, можно сделать оригинальный AI-персонаж или похожее настроение/тембр/подачу без копирования личности;
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
  "intent": "qualification_answer|business_link|reference_link|price_question|discount_question|quantity_answer|case_request|niche_specific_case_request|feasibility_question|format_preference|negative_selection|confusion|objection|voice_question|copyright_question|package_selection|human_request|stop_or_opt_out|greeting|defer|other",
  "message_meaning": "что означает последнее сообщение с учетом истории",
  "should_update_state": true,
  "extracted_fields": {
    "niche": null,
    "product_or_service": null,
    "target_audience": null,
    "goal": null,
    "deadline": null,
    "quantity": null,
    "video_quantity": null,
    "budget": null,
    "reference_links": [],
    "liked_formats": [],
    "selected_package": null,
    "package_interest": null,
    "voice_preference": null,
    "copyright_concern": null,
    "campaign_context": null,
    "hook_idea": null,
    "city": null,
    "website_or_instagram": null,
    "business_link": null,
    "platform": null,
    "strong_side": null,
    "offer": null
  },
  "do_not_overwrite_fields": [],
  "answered_questions": [],
  "missing_fields": [],
  "recommended_action": "send_text|send_relevant_examples|ask_goal|ask_next_question|send_price_options|send_questionnaire|answer_question|handoff|stop_bot|no_reply",
  "reply_text": "короткий ответ клиенту",
  "next_action": "send_text|send_cases|send_video|send_relevant_examples|ask_next_question|handoff|no_reply",
  "portfolio_tags": [],
  "needs_human": false,
  "confidence": 0.0
}

Нельзя возвращать markdown вне JSON.
Нельзя добавлять объяснения вне JSON.`

func FallbackText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Рақмет. Не сатасыз және роликтің мақсаты қандай екенін қысқаша жазыңыз."
	case "en":
		return "Thanks. Please share what you sell and the video goal: leads, sales, or awareness."
	default:
		return "Понял. Подскажите, пожалуйста, что продаёте и какая цель ролика: заявки, продажи или узнаваемость?"
	}
}

func OpenAITemporaryFallbackText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Хабарламаңызды алдым. Stone Production 48 сағатта түсірілімсіз AI-жарнамалық ролик жасайды, баға 35 000 тг бастап. Ролик бойынша қай сұрақты нақтылайын?"
	case "en":
		return "I received your message. Stone Production creates AI ad videos in 48 hours without filming, starting from 35,000 KZT. What should I clarify about the video?"
	default:
		return "Сообщение получил. Stone Production делает AI-рекламные ролики за 48 часов без съёмки, стоимость от 35 000 тг. Что уточнить по ролику?"
	}
}

func NonTextFallbackText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Материал алдым. Чатта медианы әрдайым дұрыс талдай алмаймын, сондықтан қысқаша мәтінмен жазыңыз: нені продвигаем және ролик мақсаты қандай. Немесе менеджерге беремін."
	case "en":
		return "I received the material. I cannot always parse media correctly in chat, so please write briefly in text: what we are promoting and the video goal. Or I can pass it to a manager."
	default:
		return "Материал получил. Я не всегда могу корректно разобрать медиа в чате, поэтому напишите, пожалуйста, коротко текстом: что продвигаем и какая цель ролика. Либо передам менеджеру."
	}
}

func StopConfirmationText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім, автоматты хабарламалар тоқтатылды. Енді бот сізге жазбайды."
	case "en":
		return "Understood, automated messages have been stopped. The bot will not write to you anymore."
	default:
		return "Понял, автоматические сообщения остановлены. Бот больше не будет вам писать."
	}
}

func LanguageChoiceText() string {
	return "Тілді таңдаңыз / Выберите язык / Choose language:\n1) Қазақша\n2) Русский\n3) English"
}

func QualificationGreetingText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сәлеметсіз бе! Хабарласқаныңызға рахмет 🙌\n\n48 сағатта түсірілімсіз ИИ жарнамалық ролик жасаймыз, жарнамаға дайын.\n\nБаға 35 000 тг бастап.\n\nРоликті дәл сіздің міндетіңізге сай жасау үшін қысқаша жазыңыз:\n\n— Не сатасыз / қай ниша?\n— Роликтің мақсаты қандай: өтінім, сату немесе танымалдық?\n\nInstagram немесе сайт жіберсеңіз де болады 🎯\nСодан кейін ролик идеясы мен форматын ұсынамыз 🤝"
	case "en":
		return "Hello! Thanks for reaching out 🙌\n\nWe create AI ad videos in 48 hours without filming, ready to launch ads.\n\nPricing starts from 35,000 KZT.\n\nTo make the video fit your task, please share briefly:\n\n— What do you sell / what is your niche?\n— What is the video goal: leads, sales, or awareness?\n\nYou can also send your Instagram or website 🎯\nAfter that we will suggest the idea and format 🤝"
	default:
		return "Здравствуйте! Спасибо за обращение 🙌\n\nСтоимость от 35 000 тг.\n\nЧтобы сделать ролик точно под вашу задачу, напишите, пожалуйста, кратко:\n\n— Что продаёте / какая ниша?\n— Какая цель ролика: заявки, продажи или узнаваемость?\n\nТакже можете отправить Instagram или сайт 🎯\nПосле этого предложим идею и формат ролика 🤝"
	}
}

func FirstContactWelcomeText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сәлеметсіз бе! Stone Production түсірілімсіз 48 сағатта жарнамаға дайын AI-ролик жасайды: сценарий, AI-визуал, дыбыстау/музыка және монтаж. Пакеттер: Test — 35 000 тг, Basic — 50 000 тг, Standard — 75 000 тг бастап. Төменде үш мысал жіберемін. Test, Basic немесе Standard ішінен қайсысын қараймыз?"
	case "en":
		return "Hello! Stone Production creates AI ad videos in 48 hours without filming: script, AI visuals, voice/music, and ad-ready editing. Packages: Test — 35,000 KZT, Basic — 50,000 KZT, Standard — from 75,000 KZT. I will send three examples below. Which package feels closest: Test, Basic, or Standard?"
	default:
		return "Здравствуйте! Stone Production делает AI-рекламные ролики за 48 часов без съёмки: сценарий, AI-визуал, озвучка/музыка и монтаж под рекламу. Пакеты: Test — 35 000 тг, Basic — 50 000 тг, Standard — от 75 000 тг. Ниже отправлю три примера. Какой пакет ближе по задаче: Test, Basic или Standard?"
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
		return "Чтобы сделать ролик точно под вашу задачу 🙌\nНапишите, пожалуйста, кратко:\n\n— Что продаёте?\n— В чём ваша сильная сторона?\n— Кто ваш клиент?\n— Есть ли сейчас акция / оффер?\n\nТакже можете отправить Instagram или сайт 🎯\nПосле этого предложим идею и формат ролика 🤝"
	}
}

func BriefTextAfterLink(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сілтемені алдым, рақмет. Команда парақшаны қарайды.\n\nСценарий үшін қысқаша жазыңыз:\n— Не сатасыз?\n— Күшті жағыңыз қандай?\n— Клиентіңіз кім?\n— Қазір акция / оффер бар ма?"
	case "en":
		return "Got the link, thank you. The team will review the page.\n\nFor the script, please write briefly:\n— What do you sell?\n— What is your strongest side?\n— Who is your customer?\n— Do you have a current promo / offer?"
	default:
		return "Ссылку получил, спасибо. Команда посмотрит страницу.\n\nДля сценария ещё напишите кратко:\n— Что продаёте?\n— В чём ваша сильная сторона?\n— Кто ваш клиент?\n— Есть ли сейчас акция / оффер?"
	}
}

func BriefTextForPackage(language string, level int) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return BriefText(language)
	case "en":
		return BriefText(language)
	default:
		return BriefText(language)
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
		return "Рақмет, брифті алдық. Stone production менеджеріне өңдеуге беремін — ол тапсырманы қарап, келесі қадамды осы чатта жазады."
	case "en":
		return "Thank you, we received the brief. I am sending it to the Stone production manager for processing, and they will reply here with the next step."
	default:
		return "Спасибо, бриф получили. Передаю менеджеру Stone production в обработку — он посмотрит задачу и ответит здесь по следующему шагу."
	}
}

func HumanHandoffText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім. Менеджерге беремін, ол өніміңізге ыңғайлы форматты ұсынады. Әзірге Instagram/сайт немесе қысқа сипаттама жіберсеңіз болады — ұсынысты тезірек дайындауға көмектеседі."
	case "en":
		return "Got it. I will pass this to a manager so they can recommend the right format for your product. Meanwhile, you can send Instagram/website or a short description to speed up the proposal."
	default:
		return "Понял вас. Передам менеджеру, чтобы он предложил подходящий формат под ваш продукт. Пока можете отправить Instagram/сайт или короткое описание — это поможет быстрее подготовить предложение."
	}
}

func ManagerEscalationFallbackText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сұрағыңызды белгіледім. Қазір менеджерге берілгенін растай алмаймын, бірақ осында көмектесе беремін: Instagram/сайт немесе қысқа сипаттама жіберсеңіз, нақты ұсынысқа дайындаймын."
	case "en":
		return "I saved your request. I cannot confirm the manager transfer right now, but I can keep helping here: send your Instagram/website or a short description, and I will prepare the details for an exact proposal."
	default:
		return "Запрос зафиксировал. Сейчас не могу подтвердить передачу менеджеру, но продолжу помогать здесь: отправьте Instagram/сайт или короткое описание, и я подготовлю детали для точного предложения."
	}
}

func QualifiedLeadHandoffText(language string, lead LeadState) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Түсіндім, ақпаратты белгілеп алдым. Менеджерге беремін — ол келесі қадамды осы чатта жалғастырады."
	case "en":
		return "Got it, I have saved the details. I will pass this to a manager, and they will continue with the next step here."
	default:
		prefix := "Понял, зафиксировал"
		if phrase := leadNicheLocationPhrase(lead); phrase != "по задаче" {
			prefix += ": " + phrase
			if lead.Deadline != "" {
				prefix += ", запуск — " + lead.Deadline
			}
			if lead.SelectedPackage != "" {
				prefix += ", пакет — " + adminPackageLabel(lead.SelectedPackage)
			}
		}
		return prefix + ". Передаю менеджеру, он продолжит и уточнит детали."
	}
}

func FormatAdviceText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Рекламада көбіне қысқа әрі түсінікті роликтер жақсы өтеді: UGC, problem-solution, нәтиже демонстрациясы немесе before/after. Нақты форматты таңдау үшін өнімді, аудиторияны және іске қосу мақсатын түсіну керек."
	case "en":
		return "For ads, short videos with a clear pain, solution, and offer usually work best: UGC, problem-solution, result demo, or before/after. To choose the exact format, we need to understand the product, audience, and launch goal."
	default:
		return "Для рекламы чаще всего лучше заходят короткие ролики с понятной болью, решением и оффером: UGC, problem-solution, демонстрация результата или before/after. Чтобы точно выбрать формат, нужно понять продукт, аудиторию и цель запуска."
	}
}

func LinkReceivedQualificationText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сілтемені алдым, рақмет. Дәл формат ұсыну үшін қысқаша жазыңыз: нені продвигаем, аудитория кім және мақсат қандай — өтінім, сату немесе танымалдық?"
	case "en":
		return "Got the link, thank you. To suggest the right format, please write briefly what we are promoting, who the audience is, and the goal: leads, sales, or awareness."
	default:
		return "Ссылку получил, спасибо. Чтобы предложить точный формат, напишите, пожалуйста, что именно продвигаем, кто ваша аудитория и какая цель: заявки, продажи или узнаваемость."
	}
}

func LinkReceivedBriefText(language string) string {
	return BriefTextAfterLink(language)
}

func linkReceivedWithKnownFieldsText(language string, lead LeadState) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Сілтемені алдым, рақмет. Деректерді сақтадым, команда парақшаны қарап, ролик идеясын нақтылайды."
	case "en":
		return "Got the link, thank you. I saved the context, and the team will review the page for the video idea."
	default:
		if summary := leadBriefSummaryRU(lead); summary != "" {
			return "Ссылку получил, спасибо. Зафиксировал: " + summary + "."
		}
		return "Ссылку получил, спасибо. Данные сохранил, команда посмотрит страницу и уточнит идею ролика."
	}
}

func frustrationNextQuestionText(language string, lead LeadState, nextQuestion string) string {
	nextQuestion = strings.TrimSpace(nextQuestion)
	if nextQuestion == "" {
		nextQuestion = BriefContextReturnText(language)
	}
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Дұрыс айтасыз, қайталамаймын. " + nextQuestion
	case "en":
		return "You are right, I will not repeat it. " + nextQuestion
	default:
		if summary := leadBriefSummaryRU(lead); summary != "" {
			return "Вы правы, данные уже есть. Зафиксировал: " + summary + ". Уточню только недостающее: " + lowerFirst(nextQuestion)
		}
		return "Вы правы, не буду повторять анкету. Уточню только недостающее: " + lowerFirst(nextQuestion)
	}
}

func leadBriefSummaryRU(lead LeadState) string {
	parts := make([]string, 0, 5)
	if value := strings.TrimSpace(lead.ProductOrService); value != "" {
		parts = append(parts, value)
	} else if value := strings.TrimSpace(lead.Niche); value != "" {
		parts = append(parts, value)
	}
	if value := strings.TrimSpace(lead.StrongSide); value != "" {
		parts = append(parts, "сильная сторона — "+value)
	}
	if value := strings.TrimSpace(lead.TargetAudience); value != "" {
		parts = append(parts, "клиенты — "+value)
	}
	if value := strings.TrimSpace(lead.Offer); value != "" {
		parts = append(parts, "оффер — "+value)
	}
	if value := strings.TrimSpace(lead.WebsiteOrInstagram); value != "" {
		parts = append(parts, "сайт/ссылка — "+value)
	}
	return strings.Join(parts, ", ")
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
		return "Вы можете заполнить анкету. Мы бесплатно пропишем для вас сценарий, далее подготовим концепт, и уже после этого вы выберете, какой формат вам подходит.\n\nЭто займет 1 минуту.\n\nОтправить анкету?"
	}
}

func packageOptionsText(language string) string {
	switch normalizeLanguageCode(language) {
	case "kk":
		return "Форматты таңдаңыз:\n1️⃣ Test — 35 000 ₸\n2️⃣ Basic — 50 000 ₸\n3️⃣ Standard — 75 000 ₸ден бастап\n\nҚай формат ұнады?"
	case "en":
		return "Choose your format:\n1️⃣ Test — 35,000 KZT\n2️⃣ Basic — 50,000 KZT\n3️⃣ Standard — from 75,000 KZT\n\nWhich format do you like?"
	default:
		return "Выберите подходящий формат:\n1️⃣ Test — 35 000 тг\n2️⃣ Basic — 50 000 тг\n3️⃣ Standard — от 75 000 тг\n\nКакой формат вам понравился?"
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
		return "Отправить анкету?"
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
