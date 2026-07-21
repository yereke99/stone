package bot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Greetings, confirmations, case/price questions, timing words and stop
// commands must never be extracted as a niche, even when the niche is the only
// missing lead field (the most aggressive extraction mode).
func TestNonBusinessMessagesAreNeverSavedAsNiche(t *testing.T) {
	leadMissingOnlyNiche := LeadState{
		Goal:     "рост продаж",
		Deadline: "на этой неделе",
	}
	tests := []struct {
		text       string
		wantIntent string
	}{
		{text: "ДОБРОЕ УТРО", wantIntent: IntentGreeting},
		{text: "Доброе утро"},
		{text: "Добрый день"},
		{text: "Здравствуйте", wantIntent: IntentGreeting},
		{text: "да"},
		{text: "ок"},
		{text: "принято"},
		{text: "понял"},
		{text: "никакой", wantIntent: IntentNegativeSelection},
		{text: "не знаю"},
		{text: "потом"},
		{text: "посмотрю"},
		{text: "хорошо"},
		{text: "Есть кейсы?", wantIntent: IntentPortfolioRequest},
		{text: "Как отправите кейсы?", wantIntent: IntentPortfolioRequest},
		{text: "Хронометраж видео какой", wantIntent: IntentFAQ},
		{text: "Сколько секунд ролик?", wantIntent: IntentFAQ},
		{text: "Сейчас"},
		{text: "Завтра"},
		{text: "Бот собираюсь"},
		{text: "стоп"},
		{text: "stop"},
		{text: "Сколько стоит?", wantIntent: IntentPriceQuestion},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			for _, lead := range []LeadState{{}, leadMissingOnlyNiche} {
				analysis := AnalyzeCustomerMessage(tt.text, lead, "ru")
				if analysis.Niche != nil {
					t.Fatalf("AnalyzeCustomerMessage(%q) extracted niche %q, want none", tt.text, *analysis.Niche)
				}
				updated := lead
				updated.ApplyAnalysis(analysis)
				if updated.Niche != lead.Niche {
					t.Fatalf("ApplyAnalysis(%q) changed niche from %q to %q", tt.text, lead.Niche, updated.Niche)
				}
				if tt.wantIntent != "" && lead.Niche == "" && analysis.Intent != tt.wantIntent {
					t.Fatalf("AnalyzeCustomerMessage(%q) intent = %q, want %q", tt.text, analysis.Intent, tt.wantIntent)
				}
			}
		})
	}
}

func TestBusinessMessagesAreExtracted(t *testing.T) {
	leadMissingOnlyNiche := LeadState{Goal: "рост продаж"}

	nicheTests := []struct {
		text string
		lead LeadState
	}{
		{text: "мебель", lead: leadMissingOnlyNiche},
		{text: "ТВ зоны", lead: LeadState{}},
		{text: "продаём обувь", lead: LeadState{}},
		{text: "салон красоты", lead: LeadState{}},
		{text: "у нас мебельная ниша, в том числе ТВ зона", lead: LeadState{}},
	}
	for _, tt := range nicheTests {
		t.Run("niche/"+tt.text, func(t *testing.T) {
			analysis := AnalyzeCustomerMessage(tt.text, tt.lead, "ru")
			if analysis.Niche == nil || strings.TrimSpace(*analysis.Niche) == "" {
				t.Fatalf("AnalyzeCustomerMessage(%q) did not extract a niche", tt.text)
			}
		})
	}

	goalTests := []string{"Продажи", "Хочу заявки", "узнаваемость", "хочу больше клиентов"}
	for _, text := range goalTests {
		t.Run("goal/"+text, func(t *testing.T) {
			analysis := AnalyzeCustomerMessage(text, LeadState{Niche: "мебель"}, "ru")
			if analysis.Goal == nil || strings.TrimSpace(*analysis.Goal) == "" {
				t.Fatalf("AnalyzeCustomerMessage(%q) did not extract a goal", text)
			}
			if analysis.Niche != nil && *analysis.Niche != "мебель" {
				t.Fatalf("AnalyzeCustomerMessage(%q) overwrote niche with %q", text, *analysis.Niche)
			}
		})
	}
}

func TestQuantityDiscountUnderstandingExtractsVideoQuantity(t *testing.T) {
	discount := AnalyzeCustomerMessage("Есть скидка за количество видео", LeadState{}, "ru")
	if discount.Intent != IntentQuantityDiscountQuestion {
		t.Fatalf("discount intent = %q, want %q", discount.Intent, IntentQuantityDiscountQuestion)
	}
	if discount.Offer != nil {
		t.Fatalf("quantity discount question was saved as customer offer: %q", *discount.Offer)
	}

	lead := LeadState{Niche: "производство мебели"}
	lead.ApplyAnalysis(discount)
	if !lead.QuantityDiscountInterest {
		t.Fatal("quantity discount context was not saved on lead")
	}

	quantity := AnalyzeCustomerMessage("20 -30", lead, "ru")
	if quantity.Intent != IntentQuantityDiscountQuestion {
		t.Fatalf("quantity intent = %q, want %q", quantity.Intent, IntentQuantityDiscountQuestion)
	}
	if quantity.VideoQuantity == nil || *quantity.VideoQuantity != "20-30" {
		t.Fatalf("video quantity = %#v, want 20-30", quantity.VideoQuantity)
	}
	if quantity.Budget != nil {
		t.Fatalf("video quantity was misread as budget: %q", *quantity.Budget)
	}
	if quantity.Niche != nil || quantity.Goal != nil || quantity.Deadline != nil || quantity.PackageInterest != nil {
		t.Fatalf("quantity polluted other fields: %#v", quantity)
	}
}

func TestBudgetDoesNotSelectPackageByEmbeddedOfficialPrice(t *testing.T) {
	analysis := AnalyzeCustomerMessage("Бюджет около 150 000 тенге", LeadState{}, "ru")
	if analysis.SelectedLevel != 0 || analysis.PackageInterest != nil || analysis.Intent == IntentPackageSelection {
		t.Fatalf("budget was mistaken for package selection: %#v", analysis)
	}
	if analysis.Budget == nil || !strings.Contains(*analysis.Budget, "150") {
		t.Fatalf("budget was not extracted correctly: %#v", analysis.Budget)
	}
}

func TestSamrukStyleMultilineBriefFactsAreExtracted(t *testing.T) {
	text := "мед услуги для b2b\nопыт в крупных проектах\nкрупные компании в сфере производства, промышленность, добыча, строительство и тд\nнет оффера"

	analysis := AnalyzeCustomerMessage(text, LeadState{}, "ru")
	lead := LeadState{}
	lead.ApplyAnalysis(analysis)

	if analysis.Niche == nil || !strings.Contains(normalizeForAnalysis(*analysis.Niche), "мед") {
		t.Fatalf("niche = %#v, want B2B medical services", analysis.Niche)
	}
	if lead.ProductOrService == "" || !strings.Contains(normalizeForAnalysis(lead.ProductOrService), "b2b") {
		t.Fatalf("product_or_service = %q, want B2B medical services", lead.ProductOrService)
	}
	if lead.StrongSide == "" || !strings.Contains(normalizeForAnalysis(lead.StrongSide), "опыт") {
		t.Fatalf("strong_side = %q, want experience in large projects", lead.StrongSide)
	}
	audience := normalizeForAnalysis(lead.TargetAudience)
	for _, want := range []string{"крупные компании", "производства", "промышленность", "добыча", "строительство"} {
		if !strings.Contains(audience, want) {
			t.Fatalf("target_audience = %q, want it to contain %q", lead.TargetAudience, want)
		}
	}
	if lead.Offer == "" || !strings.Contains(normalizeForAnalysis(lead.Offer), "нет") {
		t.Fatalf("offer = %q, want no current offer", lead.Offer)
	}
	if analysis.Intent != IntentAnswer {
		t.Fatalf("intent = %q, want answer", analysis.Intent)
	}
	if !sameFields(analysis.MissingFields, []string{fieldGoal}) {
		t.Fatalf("missing fields = %#v, want only goal", analysis.MissingFields)
	}
}

// The first qualification stage requires only the niche and the goal: launch
// timing must never be a missing/required field anymore.
func TestFirstQualificationRequiresOnlyNicheAndGoal(t *testing.T) {
	if missing := (LeadState{}).MissingCoreFields(); !sameFields(missing, []string{fieldNiche, fieldGoal}) {
		t.Fatalf("MissingCoreFields() = %#v, want niche/goal only", missing)
	}
	if missing := qualificationMissingFields(LeadState{Niche: "мебель"}); !sameFields(missing, []string{fieldGoal}) {
		t.Fatalf("qualificationMissingFields() = %#v, want goal only", missing)
	}
	lead := LeadState{Niche: "мебель", Goal: "рост продаж", SelectedPackage: "standard"}
	if !leadHasRequiredManagerFields(lead) {
		t.Fatal("lead with niche/goal/package must be manager-ready without a deadline")
	}
}

// No first-stage qualification prompt may ask about launch timing.
func TestFirstStagePromptsDoNotAskDeadline(t *testing.T) {
	forbidden := []string{
		"срок", "Срок", "когда", "Когда", "deadline", "timeline",
		"мерзім", "қашан", "Қашан", "when do you need", "when to launch",
	}
	prompts := map[string]string{}
	for _, language := range []string{"ru", "kk", "en"} {
		prompts["greeting/"+language] = QualificationGreetingText(language)
		prompts["questions/"+language] = QualificationQuestionsText(language)
		prompts["reengagement/"+language] = LegacyReengagementClarificationText(language)
		prompts["fallback/"+language] = FallbackText(language)
		prompts["followup-both/"+language] = qualificationFollowupText(language, Conversation{})
		prompts["followup-goal/"+language] = qualificationFollowupText(language, Conversation{Lead: LeadState{Niche: "мебель"}})
		prompts["followup-niche/"+language] = qualificationFollowupText(language, Conversation{Lead: LeadState{Goal: "рост продаж"}})
		prompts["food-goal/"+language] = foodExamplesMissingText(language, LeadState{Niche: "еда"}, []string{fieldGoal})
		prompts["cases-both/"+language] = casesRequestQualificationText(language, LeadState{}, []string{fieldNiche, fieldGoal}, false)
		prompts["cases-delivery/"+language] = casesRequestQualificationText(language, LeadState{}, []string{fieldNiche, fieldGoal}, true)
	}
	for name, prompt := range prompts {
		for _, word := range forbidden {
			if strings.Contains(prompt, word) {
				t.Fatalf("first-stage prompt %q asks for timing (%q): %q", name, word, prompt)
			}
		}
	}
}

func TestGreetingDoesNotSaveNicheAndAsksQualification(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-greeting-no-niche"

	sendText(t, service, chatID, "ДОБРОЕ УТРО")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "" {
		t.Fatalf("greeting was saved as niche: %q", conversation.Lead.Niche)
	}
	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], FirstContactWelcomeText("ru")) {
		t.Fatalf("greeting reply = %#v, want first-contact welcome", sender.messages)
	}
	if len(sender.files) != 3 {
		t.Fatalf("greeting files=%#v, want first-contact package videos", sender.files)
	}
}

func TestGoalAnswerAfterKnownNicheDoesNotRepeatNicheQuestion(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-goal-after-niche"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
		conversation.Lead.Niche = "мебель"
	})

	sendText(t, service, chatID, "Продажи")

	conversation := snapshotConversation(t, store, chatID)
	if !isValidGoal(conversation.Lead.Goal) {
		t.Fatalf("goal was not saved: %#v", conversation.Lead)
	}
	if conversation.Lead.Niche != "мебель" {
		t.Fatalf("niche was overwritten: %q", conversation.Lead.Niche)
	}
	for _, message := range sender.messages {
		lower := strings.ToLower(message)
		if strings.Contains(lower, "какая ниша") || strings.Contains(lower, "что продаёте") || strings.Contains(lower, "для какой ниши") {
			t.Fatalf("bot asked niche again after goal answer: %q", message)
		}
	}
}

func TestKazakhForkliftRequestDoesNotAskNicheAgain(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-forklift-kz"

	sendText(t, service, chatID, "Менде погрузчик техникасы бар. Сол техникаға жарнама керек болды")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Language != "kk" {
		t.Fatalf("language = %q, want kk", conversation.Language)
	}
	if conversation.Lead.Niche != "погрузчик / спецтехника" {
		t.Fatalf("niche = %q, want погрузчик / спецтехника", conversation.Lead.Niche)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("messages = %#v, want one missing-goal follow-up", sender.messages)
	}
	reply := strings.ToLower(sender.messages[0])
	if strings.Contains(reply, "не сатасыз") || strings.Contains(reply, "қай ниша") || strings.Contains(reply, "что прода") {
		t.Fatalf("bot asked known niche again: %q", sender.messages[0])
	}
	if !strings.Contains(sender.messages[0], FirstContactWelcomeText("kk")) {
		t.Fatalf("bot should send Kazakh first-contact package: %q", sender.messages[0])
	}
	if len(sender.files) != 3 {
		t.Fatalf("Kazakh first-contact files=%#v, want package videos", sender.files)
	}
}

func TestBarbershopPhraseNormalizesNicheAndDoesNotEchoRawSentence(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-barbershop-kz"

	sendText(t, service, chatID, "барбершопта жұмыс істейміз")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "барбершоп / услуги барбершопа" {
		t.Fatalf("niche = %q, want canonical barbershop", conversation.Lead.Niche)
	}
	if strings.Contains(strings.ToLower(conversation.Lead.Niche), "жұмыс") {
		t.Fatalf("raw customer phrase was saved as niche: %q", conversation.Lead.Niche)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("messages = %#v, want one follow-up", sender.messages)
	}
	reply := strings.ToLower(sender.messages[0])
	if strings.Contains(reply, "барбершопта жұмыс істейміз") || strings.Contains(reply, "что прода") || strings.Contains(reply, "не сатасыз") {
		t.Fatalf("bot echoed raw phrase or repeated niche question: %q", sender.messages[0])
	}
}

func TestQuotedConstructionReplyUsesTypedTextAndPreservesGoal(t *testing.T) {
	sender := &fakeSender{fileMessageIDs: []string{"test-video-id", "basic-video-id", "standard-video-id"}}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-quoted-construction-answer"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Language = "ru"
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
		conversation.Lead.Goal = "получать заявки"
	})

	currentText := "Плиточные клея, шпаклёвка, штукатурка и т.д.\nОсновные клиенты строительные компании, частники!"
	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		IDMessage:       "quoted-construction-answer",
		ChatID:          chatID,
		TypeMessage:     "quotedMessage",
		Text:            currentText,
		QuotedMessageID: "old-qualification-question",
		QuotedType:      "textMessage",
		QuotedText:      "Чтобы предложить точный формат, напишите, пожалуйста, что именно продвигаем, кто ваша аудитория и какая цель: заявки, продажи или узнаваемость.",
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	conversation := snapshotConversation(t, store, chatID)
	niche := normalizeForAnalysis(conversation.Lead.Niche)
	for _, want := range []string{"плиточные клея", "шпаклевка", "штукатурка"} {
		if !strings.Contains(niche, want) {
			t.Fatalf("niche = %q, want it to contain %q", conversation.Lead.Niche, want)
		}
	}
	if conversation.Lead.Goal != "получать заявки" {
		t.Fatalf("goal = %q, want preserved known goal", conversation.Lead.Goal)
	}
	audience := normalizeForAnalysis(conversation.Lead.TargetAudience)
	if !strings.Contains(audience, "строительные компании") || !strings.Contains(audience, "частники") {
		t.Fatalf("audience = %q, want construction companies and private customers", conversation.Lead.TargetAudience)
	}
	if len(sender.files) != 3 {
		t.Fatalf("qualified quoted reply should send package examples, files=%#v", sender.files)
	}
	for _, message := range sender.messages {
		lower := strings.ToLower(message)
		if strings.Contains(lower, "что продаёте") ||
			strings.Contains(lower, "что продаете") ||
			strings.Contains(lower, "какая у вас ниша") {
			t.Fatalf("bot asked niche again after quoted typed answer: %q", message)
		}
	}
}

func TestDurationQuestionAnswersFAQAndDoesNotPolluteLead(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-duration-question"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Language = "ru"
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
	})

	sendText(t, service, chatID, "Хронометраж видео какой")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "" || conversation.Lead.Goal != "" {
		t.Fatalf("duration question polluted lead fields: %#v", conversation.Lead)
	}
	if len(sender.files) != 3 {
		t.Fatalf("duration FAQ files=%#v, want first-contact package videos", sender.files)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("messages = %#v, want one FAQ reply", sender.messages)
	}
	reply := sender.messages[0]
	if !strings.Contains(reply, "30–45 секунд") {
		t.Fatalf("duration reply = %q, want 30–45 секунд answer", reply)
	}
	if strings.Contains(strings.ToLower(reply), "понял, хронометраж") {
		t.Fatalf("duration question was echoed as qualification data: %q", reply)
	}
	if !strings.Contains(reply, FirstContactWelcomeText("ru")) {
		t.Fatalf("duration first-contact reply missing welcome package text: %q", reply)
	}
	if conversation.Stage != ClientStatePackagesPresented || conversation.AutoPackagesSentAt.IsZero() {
		t.Fatalf("duration first-contact package not persisted: stage=%q sent_at=%v", conversation.Stage, conversation.AutoPackagesSentAt)
	}
}

func TestCasesRequestWithoutQualificationAnswersAndAsksFields(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-cases-unqualified"

	sendText(t, service, chatID, "Есть кейсы?")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "" {
		t.Fatalf("cases question was saved as niche: %q", conversation.Lead.Niche)
	}
	if len(sender.files) != 3 {
		t.Fatalf("cases first-contact files=%#v, want package videos", sender.files)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("messages = %#v, want a single cases answer", sender.messages)
	}
	reply := sender.messages[0]
	if !strings.Contains(reply, "кейсы можем отправить") || !strings.Contains(reply, FirstContactWelcomeText("ru")) {
		t.Fatalf("cases reply did not answer and send welcome package: %q", reply)
	}
}

func TestCasesDeliveryQuestionAnswersAndAsksFields(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-cases-delivery"

	sendText(t, service, chatID, "Как отправите кейсы?")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "" {
		t.Fatalf("delivery question was saved as niche: %q", conversation.Lead.Niche)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("messages = %#v, want a single delivery answer", sender.messages)
	}
	reply := sender.messages[0]
	if !strings.Contains(reply, "Отправим прямо сюда") || !strings.Contains(reply, FirstContactWelcomeText("ru")) {
		t.Fatalf("delivery reply did not answer and send welcome package: %q", reply)
	}
	if len(sender.files) != 3 {
		t.Fatalf("delivery first-contact files=%#v, want package videos", sender.files)
	}
}

func TestCasesRequestWithQualifiedLeadSendsExamples(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-cases-qualified"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
		conversation.Lead.Niche = "мебель"
		conversation.Lead.Goal = "рост продаж"
	})

	sendText(t, service, chatID, "Есть кейсы?")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "мебель" || !isValidGoal(conversation.Lead.Goal) {
		t.Fatalf("qualified lead fields were damaged: %#v", conversation.Lead)
	}
	if len(sender.files) != 3 {
		t.Fatalf("examples were not sent for a qualified lead: %#v", sender.files)
	}
	first := sender.messages[0]
	firstLower := strings.ToLower(first)
	if !(strings.Contains(firstLower, "отправим примеры") || strings.Contains(firstLower, "отправлю") && strings.Contains(firstLower, "примеры")) || !strings.Contains(first, "мебель") {
		t.Fatalf("cases acknowledgement is wrong: %q", first)
	}
	for _, message := range sender.messages {
		lower := strings.ToLower(message)
		if strings.Contains(lower, "что продаёте") || strings.Contains(lower, "какая у вас ниша") {
			t.Fatalf("bot re-asked qualification for a qualified lead: %q", message)
		}
	}
}

func TestExamplesRequestSendsThreePortfolioVideosWithCaptionsOnce(t *testing.T) {
	sender := &fakeSender{fileMessageIDs: []string{"test-video-id", "basic-video-id", "standard-video-id"}}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-examples-captioned"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
		conversation.Lead.Niche = "мебель"
		conversation.Lead.Goal = "получать заявки"
	})

	sendText(t, service, chatID, "покажите примеры")

	if len(sender.files) != 3 {
		t.Fatalf("files = %#v, want three local portfolio videos", sender.files)
	}
	for i, want := range []string{VideoLevel1, VideoLevel2, VideoLevel3} {
		if got := filepath.Base(sender.files[i]); got != want {
			t.Fatalf("file[%d] = %q, want %q", i, got, want)
		}
	}
	if len(sender.captions) != 3 {
		t.Fatalf("captions = %#v, want one caption per video upload", sender.captions)
	}
	for i, caption := range sender.captions {
		if strings.TrimSpace(caption) == "" {
			t.Fatalf("caption[%d] is empty", i)
		}
	}
	if !strings.Contains(sender.captions[0], "Тестовый формат") ||
		!strings.Contains(sender.captions[1], "Базовый формат") ||
		!strings.Contains(sender.captions[2], "Стандарт") {
		t.Fatalf("captions do not match package videos: %#v", sender.captions)
	}
	if countMessagesContaining(sender.messages, "Тестовый формат") != 0 ||
		countMessagesContaining(sender.messages, "Базовый формат") != 0 ||
		countMessagesContaining(sender.messages, "Стандарт (премиум") != 0 {
		t.Fatalf("video captions were sent as standalone text: %#v", sender.messages)
	}

	sendText(t, service, chatID, "покажите примеры")

	if len(sender.files) != 3 {
		t.Fatalf("portfolio videos repeated without explicit repeat request: %#v", sender.files)
	}
}

// After a manual admin stop the customer must be ignored completely: no AI
// call, no reply, no media, no follow-up.
func TestStoppedChatIgnoresCustomerSilently(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &fakeAI{}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "77043330000@c.us"

	if err := store.MarkManualStop(context.Background(), chatID, "stop-message-id", time.Now().UTC(), StoppedByManualAdmin); err != nil {
		t.Fatalf("MarkManualStop() error = %v", err)
	}

	for _, text := range []string{"Здравствуйте", "Есть кейсы?", "мебель, цель продажи"} {
		sendText(t, service, chatID, text)
	}

	if ai.analysisCalled || ai.called {
		t.Fatalf("AI was called for a stopped chat: analysis=%v sales=%v", ai.analysisCalled, ai.called)
	}
	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("stopped chat received automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
	conversation := snapshotConversation(t, store, chatID)
	if !conversation.Stopped || conversation.StopReason != StopReasonManualAdminStop || conversation.StoppedBy != StoppedByManualAdmin {
		t.Fatalf("stop state was lost: stopped=%v reason=%q by=%q", conversation.Stopped, conversation.StopReason, conversation.StoppedBy)
	}
	if conversation.StopMessageID != "stop-message-id" || conversation.StoppedAt.IsZero() {
		t.Fatalf("stop metadata was lost: id=%q at=%v", conversation.StopMessageID, conversation.StoppedAt)
	}
	if !conversation.NextFollowupAt.IsZero() || strings.TrimSpace(conversation.FollowupStage) != "" {
		t.Fatalf("follow-up survived manual stop: at=%v stage=%q", conversation.NextFollowupAt, conversation.FollowupStage)
	}
}

func TestTimingWordsAreNotSavedAsNicheInFlow(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-timing-words"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQualification
		conversation.InitialMessageSent = true
		conversation.Lead.Goal = "рост продаж"
	})

	for _, text := range []string{"Сейчас", "Завтра"} {
		store.Update(chatID, func(conversation *Conversation) {
			conversation.LastReplyAt = time.Now().Add(-10 * time.Second)
		})
		sendText(t, service, chatID, text)
		conversation := snapshotConversation(t, store, chatID)
		if conversation.Lead.Niche != "" {
			t.Fatalf("timing word %q was saved as niche: %q", text, conversation.Lead.Niche)
		}
	}
}
