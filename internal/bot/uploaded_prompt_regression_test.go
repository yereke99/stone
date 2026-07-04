package bot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yereke99/stone/internal/openai"
)

func TestProductNicheAnswerStoresNicheAndAsksOnlyMissingFields(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-bad-product"

	sendText(t, service, chatID, "Здравствуйте")
	sendText(t, service, chatID, "Бад для похудения продаю")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "бад для похудения" {
		t.Fatalf("niche = %q, want product niche", conversation.Lead.Niche)
	}
	last := sender.messages[len(sender.messages)-1]
	lower := strings.ToLower(last)
	if strings.Contains(lower, "подскажите нишу") ||
		strings.Contains(lower, "какая у вас ниша") ||
		strings.Contains(lower, "для какой ниши") {
		t.Fatalf("bot asked for already known niche: %q", last)
	}
	if !strings.Contains(lower, "цель") {
		t.Fatalf("missing-field reply %q does not ask for the goal", last)
	}
	if strings.Contains(lower, "срок") || strings.Contains(lower, "когда") {
		t.Fatalf("missing-field reply asked for launch timing in the first qualification: %q", last)
	}
}

func TestScreenshotNicheCityMessageDoesNotRepeatNicheQuestion(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &fakeAI{}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "chat-screenshot-niche-city"
	seedPresentedPackageMessages(store, chatID)

	sendText(t, service, chatID, "здравствуйте ниша ! Стирка Ковров в Алматы!")

	if !ai.analysisCalled {
		t.Fatal("OpenAI understanding was not called")
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "стирка ковров" || conversation.Lead.City != "Алматы" {
		t.Fatalf("lead fields = %#v, want niche/city extracted", conversation.Lead)
	}
	last := sender.messages[len(sender.messages)-1]
	lower := strings.ToLower(last)
	for _, forbidden := range []string{"подскажите нишу", "какая у вас ниша", "для какой ниши"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("reply repeated/stretched qualification question %q in: %q", forbidden, last)
		}
	}
	if !strings.Contains(lower, "цель") {
		t.Fatalf("reply did not ask only for the missing goal: %q", last)
	}
	if strings.Contains(lower, "когда") || strings.Contains(lower, "срок") {
		t.Fatalf("reply asked for launch timing in the first qualification: %q", last)
	}
}

func TestShortNicheAndDeadlineAsksOnlyGoal(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-short-niche-deadline"

	sendText(t, service, chatID, "Здравствуйте")
	sendText(t, service, chatID, "у меня работа копирайтинг, за три дня надо сделать")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "копирайтинг" || conversation.Lead.Deadline != "за 3 дня" {
		t.Fatalf("lead fields = %#v, want copywriting and 3-day deadline", conversation.Lead)
	}
	last := sender.messages[len(sender.messages)-1]
	lower := strings.ToLower(last)
	if strings.Contains(lower, "ниша") || strings.Contains(lower, "срок") || strings.Contains(lower, "когда") {
		t.Fatalf("reply asked for already known field: %q", last)
	}
	if !strings.Contains(lower, "цель") {
		t.Fatalf("reply did not ask for the next missing goal: %q", last)
	}
}

func TestOpenAIRecommendedHandoffSavesFieldsAndNotifiesAdmin(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &scriptedUnderstandingAI{response: openai.CustomerUnderstanding{
		Language: "ru",
		Intent:   "choose_package",
		Extracted: openai.CustomerUnderstandingExtracted{
			Niche:           testString("Стирка ковров"),
			City:            testString("Алматы"),
			Deadline:        testString("завтра"),
			PackageInterest: testString("standard"),
		},
		StateUpdate: openai.CustomerUnderstandingState{
			ShouldSave:             true,
			ShouldHandoffToManager: true,
		},
		Confidence: 1,
	}}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil, "77019519013@c.us")
	chatID := "chat-ai-handoff"
	seedPresentedPackageMessages(store, chatID)

	sendText(t, service, chatID, "стирка ковров Алматы, нужен стандарт, запуск завтра")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.HandedOffToOwner || !conversation.AutomationClosed || !conversation.Stopped {
		t.Fatalf("handoff state = stage=%q handed=%v closed=%v stopped=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.Stopped)
	}
	if conversation.Lead.Niche != "стирка ковров" || conversation.Lead.City != "Алматы" || conversation.Lead.Deadline != "завтра" || conversation.Lead.SelectedPackage != "standard" {
		t.Fatalf("lead fields = %#v", conversation.Lead)
	}
	if got := countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp"); got != 1 {
		t.Fatalf("admin notification count = %d messages=%#v", got, sender.messages)
	}
	lastClientReply := messagesToChat(sender, chatID)[0]
	if strings.Contains(strings.ToLower(lastClientReply), "подскажите") {
		t.Fatalf("handoff reply asked another question: %q", lastClientReply)
	}
}

func TestOpenAIFailureFallsBackToDeterministicUnderstanding(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &scriptedUnderstandingAI{err: errors.New("timeout")}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "chat-ai-fallback"
	seedPresentedPackageMessages(store, chatID)

	sendText(t, service, chatID, "стирка ковров Алматы")

	if !ai.analysisCalled {
		t.Fatal("OpenAI understanding was not attempted")
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "стирка ковров" || conversation.Lead.City != "Алматы" {
		t.Fatalf("fallback did not save deterministic fields: %#v", conversation.Lead)
	}
	last := strings.ToLower(sender.messages[len(sender.messages)-1])
	if strings.Contains(last, "подскажите нишу") || strings.Contains(last, "для какой ниши") {
		t.Fatalf("fallback repeated known niche question: %q", sender.messages[len(sender.messages)-1])
	}
}

func TestExplicitManagerRequestHandsOffAndStopsAutomation(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t), "77019519013@c.us")
	chatID := "chat-manager-request"
	seedPresentedPackageMessages(store, chatID)

	sendText(t, service, chatID, "Давай отправь на менеджера. Формат ролика пусть он предложит то что продается")

	conversation := snapshotConversation(t, store, chatID)
	if conversation.Stage != ClientStateHandedOff || !conversation.HandedOffToOwner || !conversation.AutomationClosed || !conversation.Stopped {
		t.Fatalf("manager request did not close automation: stage=%q handed=%v closed=%v stopped=%v", conversation.Stage, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.Stopped)
	}
	if conversation.Lead.SelectedPackage != packageNeedsManagerRecommendation {
		t.Fatalf("selected package = %q, want manager recommendation", conversation.Lead.SelectedPackage)
	}
	clientReplies := messagesToChat(sender, chatID)
	if len(clientReplies) != 1 {
		t.Fatalf("client replies = %#v, want one acknowledgement", clientReplies)
	}
	if strings.Contains(clientReplies[0], "Какой формат") || strings.Contains(clientReplies[0], "Какой пакет") {
		t.Fatalf("handoff acknowledgement asked a stale sales question: %q", clientReplies[0])
	}

	before := len(sender.messages)
	sendText(t, service, chatID, "Без ИИ консультанта")
	if len(sender.messages) != before {
		t.Fatalf("bot replied after manager handoff: %#v", sender.messages[before:])
	}
}

func TestYesAfterQuestionnaireOfferSendsBriefImmediately(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-questionnaire-yes"
	seedPresentedQualifiedLead(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateAwaitingQuestionnaireConfirm
		conversation.QuestionnaireOfferSent = true
		conversation.SelectedLevel = 3
		conversation.Lead.SelectedPackage = "standard"
	})

	sendText(t, service, chatID, "Да")

	if len(sender.messages) == 0 || sender.messages[len(sender.messages)-1] != BriefText("ru") {
		t.Fatalf("last reply = %#v, want questionnaire brief", sender.messages)
	}
	conversation := snapshotConversation(t, store, chatID)
	if !conversation.QuestionnaireSent || conversation.Stage != StageBriefRequested {
		t.Fatalf("questionnaire state = stage=%q sent=%v", conversation.Stage, conversation.QuestionnaireSent)
	}
}

func TestInstagramLinkAfterPackagesDoesNotAskFormat(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-link-after-packages"
	seedPresentedQualifiedLead(store, chatID)

	sendText(t, service, chatID, "https://www.instagram.com/stone.production")

	last := sender.messages[len(sender.messages)-1]
	if !strings.Contains(last, "Ссылку получил") {
		t.Fatalf("link was not acknowledged: %q", last)
	}
	if strings.Contains(last, "Какой формат вам понравился") {
		t.Fatalf("link fell through to format question: %q", last)
	}
}

func TestFormatAdviceQuestionGetsConsultativeAnswer(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))

	sendText(t, service, "chat-format-advice", "какой формат в рекламе лучше заходит?")

	last := sender.messages[len(sender.messages)-1]
	if !strings.Contains(last, "UGC") || !strings.Contains(last, "problem-solution") {
		t.Fatalf("format advice reply is not consultative: %q", last)
	}
	if strings.Contains(last, "Какой формат вам понравился") {
		t.Fatalf("format advice asked package fallback: %q", last)
	}
}

func TestMediaMessageGetsHelpfulFallbackAndHandoffStaysSilent(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-media"

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: chatID, TypeMessage: "imageMessage"}); err != nil {
		t.Fatalf("HandleIncomingMessage(media) error = %v", err)
	}
	if len(sender.messages) != 1 || !strings.Contains(sender.messages[0], "Материал получил") {
		t.Fatalf("media fallback = %#v", sender.messages)
	}

	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = ClientStateHandedOff
		conversation.HandedOffToOwner = true
		conversation.AutomationClosed = true
		conversation.Stopped = true
	})
	before := len(sender.messages)
	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: chatID, TypeMessage: "voiceMessage"}); err != nil {
		t.Fatalf("HandleIncomingMessage(post-handoff media) error = %v", err)
	}
	if len(sender.messages) != before {
		t.Fatalf("post-handoff media got automation: %#v", sender.messages[before:])
	}
}

func TestGenericOkAfterQuestionnaireSentStaysQuiet(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-ok-after-brief"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Stage = StageBriefRequested
		conversation.QuestionnaireSent = true
		conversation.Lead.BriefRequested = true
	})

	sendText(t, service, chatID, "Ок")

	if len(sender.messages) != 0 {
		t.Fatalf("generic acknowledgement after questionnaire should not get repeated prompt: %#v", sender.messages)
	}
}

func TestQuantityDiscountScenarioUsesIndividualTermsAndQuantityContext(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-quantity-discount"

	sendText(t, service, chatID, "Есть скидка за количество видео")

	first := sender.messages[len(sender.messages)-1]
	assertNoForbiddenQuantityDiscountPhrases(t, first)
	if !strings.Contains(first, "пакетные условия индивидуально") {
		t.Fatalf("first discount reply did not use safe individual terms: %q", first)
	}

	sendText(t, service, chatID, "Производства мебели")
	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "производство мебели" && conversation.Lead.Niche != "мебель" {
		t.Fatalf("niche = %q, want furniture production or furniture", conversation.Lead.Niche)
	}

	sendText(t, service, chatID, "20 -30")

	conversation = snapshotConversation(t, store, chatID)
	if conversation.Lead.VideoQuantity != "20-30" {
		t.Fatalf("video quantity = %q, want 20-30", conversation.Lead.VideoQuantity)
	}
	if conversation.Lead.Budget != "" {
		t.Fatalf("quantity was stored as budget: %q", conversation.Lead.Budget)
	}
	last := sender.messages[len(sender.messages)-1]
	assertNoForbiddenQuantityDiscountPhrases(t, last)
	lower := strings.ToLower(last)
	if !strings.Contains(lower, "производство мебели") && !strings.Contains(lower, "мебель") {
		t.Fatalf("quantity reply did not acknowledge known niche: %q", last)
	}
	if !strings.Contains(last, "20–30") {
		t.Fatalf("quantity reply did not acknowledge 20-30 videos: %q", last)
	}
	if !strings.Contains(lower, "цель") {
		t.Fatalf("quantity reply did not ask the next missing goal: %q", last)
	}
	if strings.Contains(lower, "что прода") || strings.Contains(lower, "какая у вас ниша") || strings.Contains(lower, "для какой ниши") {
		t.Fatalf("quantity reply repeated known niche question: %q", last)
	}
	if len(sender.files) != 0 {
		t.Fatalf("discount scenario should not send videos before goal is known: %#v", sender.files)
	}
	for _, reply := range sender.messages {
		assertNoForbiddenQuantityDiscountPhrases(t, reply)
	}
}

func TestURLOnlyReferenceDoesNotBecomeGoal(t *testing.T) {
	text := "https://www.instagram.com/stone.production/reel/ABC123/"
	analysis := AnalyzeCustomerMessage(text, LeadState{}, "ru")
	if analysis.Goal != nil {
		t.Fatalf("url-only message was saved as goal: %q", *analysis.Goal)
	}
	if analysis.BusinessLink == nil || *analysis.BusinessLink != text {
		t.Fatalf("business link = %#v, want %q", analysis.BusinessLink, text)
	}

	lead := LeadState{}
	lead.ApplyAnalysis(analysis)
	if lead.Goal != "" {
		t.Fatalf("lead goal = %q, want empty", lead.Goal)
	}
	if lead.WebsiteOrInstagram != text {
		t.Fatalf("website/link = %q, want %q", lead.WebsiteOrInstagram, text)
	}
}

func TestApproximatelyReferenceQuestionIsNotPortfolioRequest(t *testing.T) {
	text := "Делаете примерно такое видео?"
	if containsPortfolioRequest(text) {
		t.Fatalf("containsPortfolioRequest(%q) matched inside примерно", text)
	}
	analysis := AnalyzeCustomerMessage(text, LeadState{}, "ru")
	if analysis.Intent != IntentFeasibilityQuestion {
		t.Fatalf("intent = %q, want feasibility question", analysis.Intent)
	}

	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	sendText(t, service, "chat-approximately-reference", text)

	last := sender.messages[len(sender.messages)-1]
	if !strings.Contains(last, "можем адаптировать") {
		t.Fatalf("feasibility reply mismatch: %q", last)
	}
	if len(sender.files) != 0 {
		t.Fatalf("примерно question sent portfolio videos: %#v", sender.files)
	}
}

func TestLongTileGlueMessageExtractsRichFactsWithoutLaunchDeadline(t *testing.T) {
	text := "Здравствуйте. Мы продаём плиточные клея и сухие смеси.\nКлиенты: строительные магазины, прорабы и частники.\nЗавтра откроем запуск, нужен AI-ролик для рекламы."
	analysis := AnalyzeCustomerMessage(text, LeadState{}, "ru")
	lead := LeadState{}
	lead.ApplyAnalysis(analysis)

	if !strings.Contains(normalizeForAnalysis(lead.ProductOrService), "плиточ") {
		t.Fatalf("product_or_service = %q, want tile glue/sdry mixes", lead.ProductOrService)
	}
	if !strings.Contains(normalizeForAnalysis(lead.Niche), "плиточ") {
		t.Fatalf("niche = %q, want tile glue/sdry mixes", lead.Niche)
	}
	audience := normalizeForAnalysis(lead.TargetAudience)
	for _, want := range []string{"строительные магазины", "прорабы", "частники"} {
		if !strings.Contains(audience, want) {
			t.Fatalf("target_audience = %q, want %q", lead.TargetAudience, want)
		}
	}
	if lead.Deadline != "" {
		t.Fatalf("launch-context deadline = %q, want empty", lead.Deadline)
	}
	if !sameFields(analysis.MissingFields, []string{fieldGoal}) {
		t.Fatalf("missing fields = %#v, want only goal", analysis.MissingFields)
	}
}

func TestConfusionGetsExplanationInsteadOfSilence(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))

	sendText(t, service, "chat-confusion", "Чет суть не уловил")

	if len(sender.messages) != 1 {
		t.Fatalf("messages = %#v, want one explanation", sender.messages)
	}
	if !strings.Contains(sender.messages[0], "объясню проще") {
		t.Fatalf("confusion reply mismatch: %q", sender.messages[0])
	}
}

func TestPositioningGoalAnswerIsStoredAsGoal(t *testing.T) {
	current := LeadState{
		Niche:           "бизнесмены и блогеры",
		Deadline:        "на этой неделе",
		SelectedPackage: "standard",
	}
	analysis := AnalyzeCustomerMessage("цель: чтобы клиенты поняли почему именно я", current, "ru")
	if analysis.Goal == nil || *analysis.Goal != "объяснить преимущество" {
		t.Fatalf("goal = %#v, want positioning goal", analysis.Goal)
	}
	if analysis.TargetAudience != nil {
		t.Fatalf("positioning goal was misread as audience: %q", *analysis.TargetAudience)
	}
	lead := current
	lead.ApplyAnalysis(analysis)
	if lead.Goal != "объяснить преимущество" {
		t.Fatalf("applied goal = %q, want positioning goal", lead.Goal)
	}

	service := NewService(&fakeSender{}, &fakeAI{}, NewConversationStore(), testVideoDir(t), PortfolioLinks{}, "auto", nil)
	conversation := Conversation{
		Language: "ru",
		Stage:    ClientStateAwaitingQualification,
		Lead:     current,
	}
	serviceAnalysis, _ := service.understandCustomerMessage(context.Background(), "chat-positioning-goal", IncomingMessage{ChatID: "chat-positioning-goal", Text: "цель: чтобы клиенты поняли почему именно я"}, "цель: чтобы клиенты поняли почему именно я", "ru", conversation)
	if serviceAnalysis.Goal == nil || *serviceAnalysis.Goal != "объяснить преимущество" {
		t.Fatalf("service understanding goal = %#v, want positioning goal; analysis=%#v", serviceAnalysis.Goal, serviceAnalysis)
	}
}

func TestNicheSpecificCaseRequestAnswersWithoutGenericPortfolioFallback(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))

	sendText(t, service, "chat-spare-parts-cases", "С запчастями есть образцы?")

	last := sender.messages[len(sender.messages)-1]
	if !strings.Contains(strings.ToLower(last), "запчаст") || !strings.Contains(last, "близкие примеры") {
		t.Fatalf("niche-specific case reply mismatch: %q", last)
	}
	if len(sender.files) != 0 {
		t.Fatalf("niche-specific case question sent generic videos: %#v", sender.files)
	}
}

func TestBothFormatsAnswerDoesNotRepeatFormatQuestion(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))
	chatID := "chat-both-formats"
	seedPresentedQualifiedLead(store, chatID)

	sendText(t, service, chatID, "Оба хороши")

	conversation := snapshotConversation(t, store, chatID)
	if len(conversation.Lead.LikedFormats) != 1 || conversation.Lead.LikedFormats[0] != "both" {
		t.Fatalf("liked formats = %#v, want both", conversation.Lead.LikedFormats)
	}
	last := sender.messages[len(sender.messages)-1]
	if !strings.Contains(last, "оба формата") || !strings.Contains(last, "Отправить анкету?") {
		t.Fatalf("both-formats reply mismatch: %q", last)
	}
	if strings.Contains(last, "Какой формат вам понравился") {
		t.Fatalf("both-formats reply repeated format question: %q", last)
	}
}

func TestVoiceAndCopyrightQuestionsGetSafeAnswers(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := newTestServiceWithVideoDir(sender, store, PortfolioLinks{}, testVideoDir(t))

	sendText(t, service, "chat-voice-question", "Можно выбрать голос?")
	if last := sender.messages[len(sender.messages)-1]; !strings.Contains(last, "голос можно выбрать") {
		t.Fatalf("voice reply mismatch: %q", last)
	}

	sendText(t, service, "chat-copyright-question", "Можно сделать голос как у Вин Дизеля без прав?")
	last := sender.messages[len(sender.messages)-1]
	if !strings.Contains(last, "без прав использовать нельзя") {
		t.Fatalf("copyright reply is not safe: %q", last)
	}
	for _, forbidden := range []string{"скопируем", "клонируем", "поставим голос Вин"} {
		if strings.Contains(strings.ToLower(last), strings.ToLower(forbidden)) {
			t.Fatalf("copyright reply contains unsafe promise %q: %q", forbidden, last)
		}
	}
}

func TestEnabledLLMReplySendsStructuredReplyAndContext(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &scriptedUnderstandingAI{salesResponse: openai.SalesResponse{
		Intent:     "qualification_answer",
		ReplyText:  "Понял, продвигаем чай. Какая цель ролика?",
		NextAction: "ask_question",
		MissingFields: []string{
			fieldGoal,
		},
		Confidence: 1,
	}}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	service.llmReply.Enabled = true

	sendText(t, service, "chat-llm-reply", "Продаем чай")

	if !ai.called {
		t.Fatal("LLM reply generator was not called")
	}
	last := sender.messages[len(sender.messages)-1]
	if last != "Понял, продвигаем чай. Какая цель ролика?" {
		t.Fatalf("last reply = %q, want LLM reply", last)
	}
	if len(ai.salesMessages) != 1 || !strings.Contains(ai.salesMessages[0].Content, "question_answer_pairs") {
		t.Fatalf("LLM payload did not include paired-context schema: %#v", ai.salesMessages)
	}
}

func TestLLMReplyErrorFallsBackWithoutSilence(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &scriptedUnderstandingAI{salesErr: errors.New("reply timeout")}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	service.llmReply.Enabled = true

	sendText(t, service, "chat-llm-fallback", "Продаем чай")

	if !ai.called {
		t.Fatal("LLM reply generator was not called")
	}
	if len(sender.messages) == 0 {
		t.Fatal("LLM failure caused silent active conversation")
	}
	if strings.Contains(sender.messages[len(sender.messages)-1], "Спасибо, уточню детали") {
		t.Fatalf("fallback used fake LLM reply despite error: %#v", sender.messages)
	}
}

func TestQuotedAnswerPayloadCarriesExpectedQuestionPair(t *testing.T) {
	conversation := Conversation{
		Stage:         ClientStatePackagesPresented,
		LastReplyText: "Какой формат вам понравился?",
	}
	payload := customerUnderstandingPayload(IncomingMessage{
		IDMessage:       "reply-1",
		Text:            "Оба хороши",
		QuotedMessageID: "bot-question-1",
		QuotedText:      "Какой формат вам понравился?",
		QuotedType:      "textMessage",
	}, "Оба хороши", "ru", conversation)

	for _, want := range []string{
		`"quoted_context"`,
		`"question_answer_pairs"`,
		`"expected_field":"liked_formats"`,
		`"customer_answer":"Оба хороши"`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %s:\n%s", want, payload)
		}
	}
}

func TestGroupMessageDoesNotReachLLMReply(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &scriptedUnderstandingAI{}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	service.llmReply.Enabled = true

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: "120363111111111111@g.us", Text: "Здравствуйте, нужен ролик"}); err != nil {
		t.Fatalf("HandleIncomingMessage(group) error = %v", err)
	}
	if ai.analysisCalled || ai.called {
		t.Fatalf("group message reached AI: analysis=%v reply=%v", ai.analysisCalled, ai.called)
	}
	if len(sender.messages) != 0 || len(sender.files) != 0 {
		t.Fatalf("group message got automation: messages=%#v files=%#v", sender.messages, sender.files)
	}
}

func assertNoForbiddenQuantityDiscountPhrases(t *testing.T, message string) {
	t.Helper()
	lower := strings.ToLower(message)
	for _, forbidden := range []string{
		"от 2 роликов 10%",
		"от 3-5 до 20%",
		"скидки до 30%",
		"от 10 роликов",
	} {
		if strings.Contains(lower, strings.ToLower(forbidden)) {
			t.Fatalf("reply contains forbidden outdated discount phrase %q: %q", forbidden, message)
		}
	}
}

func messagesToChat(sender *fakeSender, chatID string) []string {
	var result []string
	for i, id := range sender.chatIDs {
		if id == chatID {
			result = append(result, sender.messages[i])
		}
	}
	return result
}

type scriptedUnderstandingAI struct {
	fakeAI
	response      openai.CustomerUnderstanding
	err           error
	salesResponse openai.SalesResponse
	salesErr      error
	salesMessages []openai.Message
}

func (ai *scriptedUnderstandingAI) AnalyzeCustomerMessage(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.CustomerUnderstanding, error) {
	ai.analysisCalled = true
	if ai.err != nil {
		return openai.CustomerUnderstanding{}, ai.err
	}
	return ai.response, nil
}

func (ai *scriptedUnderstandingAI) GenerateSalesReply(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.SalesResponse, error) {
	ai.called = true
	ai.salesMessages = append([]openai.Message(nil), messages...)
	if ai.salesErr != nil {
		return openai.SalesResponse{}, ai.salesErr
	}
	if strings.TrimSpace(ai.salesResponse.ReplyText) == "" && strings.TrimSpace(ai.salesResponse.Reply) == "" {
		return ai.fakeAI.GenerateSalesReply(ctx, systemPrompt, messages)
	}
	return ai.salesResponse, nil
}

func testString(value string) *string {
	return &value
}
