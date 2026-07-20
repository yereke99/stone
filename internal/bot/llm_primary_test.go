package bot

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yereke99/stone/internal/openai"
)

type sequencedUnderstandingAI struct {
	fakeAI
	mu            sync.Mutex
	queue         []openai.CustomerUnderstanding
	err           error
	replyResponse openai.ReplyTextResponse
	replyErr      error
	replyCalls    int
	replyPayloads []string
}

func (ai *sequencedUnderstandingAI) AnalyzeCustomerMessage(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.CustomerUnderstanding, error) {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	ai.analysisCalled = true
	if ai.err != nil {
		return openai.CustomerUnderstanding{}, ai.err
	}
	if len(ai.queue) == 0 {
		return openai.CustomerUnderstanding{Language: "ru", Intent: "other", Confidence: 1}, nil
	}
	next := ai.queue[0]
	ai.queue = ai.queue[1:]
	return next, nil
}

func (ai *sequencedUnderstandingAI) GenerateReplyText(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.ReplyTextResponse, error) {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	ai.called = true
	ai.replyCalls++
	for _, message := range messages {
		ai.replyPayloads = append(ai.replyPayloads, message.Content)
	}
	if ai.replyErr != nil {
		return openai.ReplyTextResponse{}, ai.replyErr
	}
	return ai.replyResponse, nil
}

// TestScreenshotConversationRegression reproduces the production dialogue:
// pricing question, Instagram link, follower-growth goal, butter product.
func TestScreenshotConversationRegression(t *testing.T) {
	videoDir := withAIWorksTestRoot(t, "horeca", "b2b")
	sender := &fakeSender{}
	store := NewConversationStore()
	instagramURL := "https://www.instagram.com/maslo_petropavlovsk"
	ai := &sequencedUnderstandingAI{queue: []openai.CustomerUnderstanding{
		{
			Language:          "ru",
			Intent:            "price_question",
			Confidence:        0.9,
			ReplyText:         "Да, верно, указанная стоимость — за один ролик.",
			RecommendedAction: "answer_question",
			NextAction:        "send_text",
		},
		{
			Language:           "ru",
			Intent:             "business_link",
			Confidence:         0.9,
			WebsiteOrInstagram: testString(instagramURL),
			ReplyText:          "Спасибо, посмотрел ваш Instagram. Подскажите, какая цель ролика: заявки, продажи или узнаваемость?",
			RecommendedAction:  "ask_goal",
			NextAction:         "send_text",
		},
		{
			Language:          "ru",
			Intent:            "qualification_answer",
			Confidence:        0.9,
			Goal:              testString("привлечение подписчиков и узнаваемость"),
			ReplyText:         "Отлично, зафиксировал: рост подписчиков и узнаваемость. Подскажите, что именно продвигаем?",
			RecommendedAction: "ask_next_question",
			NextAction:        "send_text",
		},
		{
			Language:          "ru",
			Intent:            "qualification_answer",
			Confidence:        0.9,
			Niche:             testString("молочная продукция"),
			ProductOrService:  testString("сливочное масло"),
			ReplyText:         "Понял: сливочное масло, цель — узнаваемость и новые подписчики. Сейчас отправлю близкие примеры по продуктовой тематике.",
			RecommendedAction: "send_relevant_examples",
			NextAction:        "send_relevant_examples",
			PortfolioTags:     []string{"сливочное масло", "молочная продукция", "продукты питания", "fmcg", "оптовые продажи"},
		},
	}}
	service := NewService(sender, ai, store, videoDir, PortfolioLinks{}, "auto", nil)
	chatID := "chat-screenshot-regression"
	seedPresentedPackageMessages(store, chatID)
	// The customer replies took minutes in the real dialogue; move the reply
	// clock back between steps so the rapid-burst suppression window does not
	// swallow the follow-up messages.
	simulatePause := func() {
		store.Update(chatID, func(conversation *Conversation) {
			conversation.LastReplyAt = conversation.LastReplyAt.Add(-time.Minute)
		})
	}

	sendText(t, service, chatID, "Это цены за 1 ролик верно?")
	simulatePause()
	if len(sender.messages) == 0 || !strings.Contains(sender.messages[len(sender.messages)-1], "за один ролик") {
		t.Fatalf("pricing question was not answered directly: %#v", sender.messages)
	}

	sendText(t, service, chatID, instagramURL)
	simulatePause()
	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.WebsiteOrInstagram != instagramURL {
		t.Fatalf("instagram url was not stored: %q", conversation.Lead.WebsiteOrInstagram)
	}
	if !strings.Contains(sender.messages[len(sender.messages)-1], "посмотрел ваш Instagram") {
		t.Fatalf("instagram link did not get the LLM acknowledgement: %#v", sender.messages)
	}

	sendText(t, service, chatID, "Цель привлечение подписчиков и узнаваемость")
	simulatePause()
	conversation = snapshotConversation(t, store, chatID)
	goal := strings.ToLower(conversation.Lead.Goal)
	if !strings.Contains(goal, "подписчик") || !strings.Contains(goal, "узнаваем") {
		t.Fatalf("goal lost follower growth or awareness: %q", conversation.Lead.Goal)
	}
	if strings.Contains(goal, "клиент") {
		t.Fatalf("goal was rewritten into lead generation: %q", conversation.Lead.Goal)
	}

	sendText(t, service, chatID, "Сливочное масло")
	simulatePause()
	conversation = snapshotConversation(t, store, chatID)
	if conversation.Lead.ProductOrService != "сливочное масло" {
		t.Fatalf("product was not stored: %q", conversation.Lead.ProductOrService)
	}
	if !strings.Contains(strings.ToLower(conversation.Lead.Niche), "молочная") {
		t.Fatalf("niche was not stored: %q", conversation.Lead.Niche)
	}

	if len(sender.files) == 0 {
		t.Fatalf("no portfolio videos were sent: messages=%#v", sender.messages)
	}
	seen := make(map[string]bool, len(sender.files))
	for _, file := range sender.files {
		if !strings.Contains(file, "ai-works/horeca/") && !strings.Contains(file, "ai-works/b2b/") {
			t.Fatalf("unrelated portfolio video sent: %q", file)
		}
		if seen[file] {
			t.Fatalf("portfolio video sent twice: %q", file)
		}
		seen[file] = true
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("sent portfolio file does not exist on disk: %q: %v", file, err)
		}
	}

	for i, message := range sender.messages {
		normalized := strings.ToLower(message)
		if strings.Contains(normalized, "что продаёте и какая цель") || strings.Contains(normalized, "что продаете и какая цель") {
			t.Fatalf("bot repeated the full qualification question at index %d: %q\nall messages: %#v", i, message, sender.messages)
		}
	}
	joinedMessages := strings.Join(sender.messages, "\n")
	if !strings.Contains(joinedMessages, "сливочное масло") {
		t.Fatalf("LLM reply was not sent before follow-up: %#v", sender.messages)
	}
	if !conversation.QuestionnaireOfferSent {
		t.Fatalf("questionnaire was not offered after relevant cases: %#v", conversation)
	}
}

func TestLLMPrimaryReplyRepeatingKnownFieldIsCorrectedOnce(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	corrected := "Производство занимает 48 часов после утверждения сценария."
	ai := &sequencedUnderstandingAI{
		queue: []openai.CustomerUnderstanding{{
			Language:          "ru",
			Intent:            "price_question",
			Confidence:        0.9,
			ReplyText:         "Обычно 48 часов. Подскажите, что продаёте и какая цель ролика?",
			RecommendedAction: "answer_question",
			NextAction:        "send_text",
		}},
		replyResponse: openai.ReplyTextResponse{ReplyText: corrected},
	}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "chat-duplicate-question-guard"
	seedPresentedPackageMessages(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Lead.Niche = "молочная продукция"
		conversation.Lead.Goal = "повысить узнаваемость"
		conversation.CompletedFields[fieldNiche] = true
		conversation.CompletedFields[fieldGoal] = true
	})

	sendText(t, service, chatID, "Сколько по времени делаете ролик?")

	if ai.replyCalls != 1 {
		t.Fatalf("correction retries = %d, want exactly 1", ai.replyCalls)
	}
	last := sender.messages[len(sender.messages)-1]
	if last != corrected {
		t.Fatalf("corrected reply was not sent: %q", last)
	}
}

func TestLLMPrimaryReplyRepeatingKnownFieldFallsBackWhenRetryFails(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &sequencedUnderstandingAI{
		queue: []openai.CustomerUnderstanding{{
			Language:          "ru",
			Intent:            "price_question",
			Confidence:        0.9,
			ReplyText:         "Обычно 48 часов. Подскажите, что продаёте и какая цель ролика?",
			RecommendedAction: "answer_question",
			NextAction:        "send_text",
		}},
		replyErr: errors.New("retry timeout"),
	}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "chat-duplicate-question-fallback"
	seedPresentedPackageMessages(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Lead.Niche = "молочная продукция"
		conversation.Lead.Goal = "повысить узнаваемость"
		conversation.CompletedFields[fieldNiche] = true
		conversation.CompletedFields[fieldGoal] = true
	})

	sendText(t, service, chatID, "Сколько по времени делаете ролик?")

	if len(sender.messages) == 0 {
		t.Fatal("duplicate-question fallback went silent")
	}
	last := strings.ToLower(sender.messages[len(sender.messages)-1])
	if strings.Contains(last, "что продаёте") || strings.Contains(last, "что продаете") {
		t.Fatalf("repeated niche question reached the customer: %q", last)
	}
	if ai.replyCalls != 1 {
		t.Fatalf("correction retries = %d, want exactly 1 (no regeneration loop)", ai.replyCalls)
	}
}

func TestAnalyzerFailureFallsBackToDeterministicFlowWithoutSilence(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &sequencedUnderstandingAI{err: errors.New("openai unavailable")}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "chat-analyzer-failure"
	seedPresentedPackageMessages(store, chatID)
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Lead.Niche = "туризм"
		conversation.Lead.Goal = "привлечь клиентов"
	})

	sendText(t, service, chatID, "Сколько стоит один ролик?")

	if len(sender.messages) == 0 {
		t.Fatal("analyzer failure caused a silent conversation")
	}
	if ai.replyCalls != 0 {
		t.Fatalf("reply generation attempted without analyzer output: %d", ai.replyCalls)
	}
	conversation := snapshotConversation(t, store, chatID)
	if conversation.Lead.Niche != "туризм" || conversation.Lead.Goal != "привлечь клиентов" {
		t.Fatalf("analyzer failure corrupted lead state: %#v", conversation.Lead)
	}
}

func TestDuplicateMessageIDDoesNotResendLLMReplyOrVideos(t *testing.T) {
	videoDir := withAIWorksTestRoot(t, "horeca", "b2b")
	sender := &fakeSender{}
	store := NewConversationStore()
	understanding := openai.CustomerUnderstanding{
		Language:          "ru",
		Intent:            "qualification_answer",
		Confidence:        0.9,
		Niche:             testString("молочная продукция"),
		ProductOrService:  testString("сливочное масло"),
		Goal:              testString("повысить узнаваемость"),
		ReplyText:         "Понял: сливочное масло. Сейчас отправлю близкие примеры.",
		RecommendedAction: "send_relevant_examples",
		NextAction:        "send_relevant_examples",
		PortfolioTags:     []string{"молочная продукция", "продукты питания"},
	}
	ai := &sequencedUnderstandingAI{queue: []openai.CustomerUnderstanding{understanding, understanding}}
	service := NewService(sender, ai, store, videoDir, PortfolioLinks{}, "auto", nil)
	chatID := "chat-duplicate-processing"
	seedPresentedPackageMessages(store, chatID)

	message := IncomingMessage{IDMessage: "green-msg-1", ChatID: chatID, Text: "Сливочное масло, нужна узнаваемость"}
	if err := service.HandleIncomingMessage(context.Background(), message); err != nil {
		t.Fatalf("first HandleIncomingMessage() error = %v", err)
	}
	sentMessages := len(sender.messages)
	sentFiles := len(sender.files)
	if sentMessages == 0 || sentFiles == 0 {
		t.Fatalf("first processing did not send reply and videos: messages=%d files=%d", sentMessages, sentFiles)
	}

	if err := service.HandleIncomingMessage(context.Background(), message); err != nil {
		t.Fatalf("second HandleIncomingMessage() error = %v", err)
	}
	if len(sender.messages) != sentMessages || len(sender.files) != sentFiles {
		t.Fatalf("duplicate processing resent content: messages %d->%d files %d->%d",
			sentMessages, len(sender.messages), sentFiles, len(sender.files))
	}
}

func TestRapidMessagesMergeFactsWithoutLostUpdates(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	service := NewService(sender, &fakeAI{}, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "chat-rapid-merge"
	seedPresentedPackageMessages(store, chatID)

	var wg sync.WaitGroup
	inputs := []string{"ниша мебель", "цель получать заявки"}
	for _, text := range inputs {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			_ = service.HandleIncomingMessage(context.Background(), IncomingMessage{ChatID: chatID, Text: text})
		}(text)
	}
	wg.Wait()

	conversation := snapshotConversation(t, store, chatID)
	if !strings.Contains(conversation.Lead.Niche, "мебель") {
		t.Fatalf("niche lost during concurrent processing: %q", conversation.Lead.Niche)
	}
	if !strings.Contains(conversation.Lead.Goal, "заявки") {
		t.Fatalf("goal lost during concurrent processing: %q", conversation.Lead.Goal)
	}
}

func TestLLMPrimarySkipsBriefAndPackageFlows(t *testing.T) {
	conversation := Conversation{Stage: StageBriefRequested}
	if !llmPrimaryConversationBlocked(conversation, CustomerAnalysis{Intent: IntentAnswer}) {
		t.Fatal("brief stage must stay on the deterministic flow")
	}
	if !llmPrimaryConversationBlocked(Conversation{QuestionnaireOfferSent: true}, CustomerAnalysis{Intent: IntentAnswer}) {
		t.Fatal("questionnaire offer stage must stay on the deterministic flow")
	}
	if !llmPrimaryConversationBlocked(Conversation{}, CustomerAnalysis{Intent: IntentPackageSelection, SelectedLevel: 2}) {
		t.Fatal("package selection must stay on the deterministic flow")
	}
	if !llmPrimaryConversationBlocked(Conversation{}, CustomerAnalysis{Intent: IntentHumanRequest}) {
		t.Fatal("human handoff must stay on the deterministic flow")
	}
	if llmPrimaryConversationBlocked(Conversation{InitialMessageSent: true}, CustomerAnalysis{Intent: IntentPriceQuestion}) {
		t.Fatal("normal price question must go through the LLM reply path")
	}
}

func TestRecentUnderstandingMessagesExcludeCurrentAndKeepOrder(t *testing.T) {
	conversation := Conversation{Messages: []ChatMessage{
		{Role: "user", Content: "Здравствуйте", CreatedAt: time.Now().Add(-5 * time.Minute)},
		{Role: "assistant", Content: "Добрый день! Чем можем помочь?", CreatedAt: time.Now().Add(-4 * time.Minute)},
		{Role: "user", Content: "Это цены за 1 ролик верно?", CreatedAt: time.Now()},
	}}

	messages := recentUnderstandingMessages(conversation, "Это цены за 1 ролик верно?")
	if len(messages) != 2 {
		t.Fatalf("recent messages = %d, want 2 (current message excluded): %#v", len(messages), messages)
	}
	if messages[0].Role != "customer" || messages[1].Role != "bot" {
		t.Fatalf("roles are wrong: %#v", messages)
	}
	if messages[0].Text != "Здравствуйте" || !strings.Contains(messages[1].Text, "Добрый день") {
		t.Fatalf("chronological order lost: %#v", messages)
	}

	payload := customerUnderstandingPayload(IncomingMessage{Text: "Это цены за 1 ролик верно?"}, "Это цены за 1 ролик верно?", "ru", conversation)
	if got := strings.Count(payload, "Это цены за 1 ролик верно?"); got != 1 {
		t.Fatalf("current message appears %d times in the payload, want exactly 1:\n%s", got, payload)
	}
}

func TestRecentUnderstandingMessagesCapAtTenTotal(t *testing.T) {
	conversation := Conversation{}
	for i := 0; i < 8; i++ {
		conversation.Messages = append(conversation.Messages,
			ChatMessage{Role: "user", Content: "вопрос"},
			ChatMessage{Role: "assistant", Content: "ответ"},
		)
	}
	messages := recentUnderstandingMessages(conversation, "")
	if len(messages) != 10 {
		t.Fatalf("recent messages = %d, want 10", len(messages))
	}
	hasCustomer, hasBot := false, false
	for _, message := range messages {
		switch message.Role {
		case "customer":
			hasCustomer = true
		case "bot":
			hasBot = true
		}
	}
	if !hasCustomer || !hasBot {
		t.Fatalf("history must contain both roles: %#v", messages)
	}
}

func TestConversationHistoryIsolatedPerChat(t *testing.T) {
	store := NewConversationStore()
	ctx := context.Background()
	if err := store.AppendMessage(ctx, "chat-a", "user", "масло оптом"); err != nil {
		t.Fatalf("AppendMessage(chat-a) error = %v", err)
	}
	if err := store.AppendMessage(ctx, "chat-b", "user", "туризм"); err != nil {
		t.Fatalf("AppendMessage(chat-b) error = %v", err)
	}
	a := snapshotConversation(t, store, "chat-a")
	b := snapshotConversation(t, store, "chat-b")
	if len(a.Messages) != 1 || a.Messages[0].Content != "масло оптом" {
		t.Fatalf("chat-a history wrong: %#v", a.Messages)
	}
	if len(b.Messages) != 1 || b.Messages[0].Content != "туризм" {
		t.Fatalf("chat-b history wrong: %#v", b.Messages)
	}
}

func TestNormalizeGoalPreservesCompoundFollowerAwarenessGoal(t *testing.T) {
	got := normalizeGoal("привлечение подписчиков и узнаваемость")
	if !strings.Contains(got, "подписчик") || !strings.Contains(got, "узнаваем") {
		t.Fatalf("normalizeGoal() = %q, want followers and awareness preserved", got)
	}
	if strings.Contains(got, "клиент") {
		t.Fatalf("normalizeGoal() = %q, follower/awareness goal must not become lead generation", got)
	}

	if got := normalizeGoal("нужны ролики для привлечения клиентов"); got != "привлечь клиентов" {
		t.Fatalf("normalizeGoal(clients) = %q, want привлечь клиентов", got)
	}
	if got := normalizeGoal("цель получать заявки"); got != "получать заявки" {
		t.Fatalf("normalizeGoal(leads) = %q, want получать заявки", got)
	}
	if got := normalizeGoal("повысить узнаваемость"); got != "повысить узнаваемость" {
		t.Fatalf("normalizeGoal(awareness) = %q", got)
	}
}

func TestApplyAnalysisDoesNotEraseKnownValuesWithEmptyFields(t *testing.T) {
	lead := LeadState{
		Niche:            "молочная продукция",
		Goal:             "привлечение подписчиков и узнаваемость",
		ProductOrService: "сливочное масло",
	}
	lead.ApplyAnalysis(CustomerAnalysis{Intent: IntentOther})
	if lead.Niche != "молочная продукция" || lead.ProductOrService != "сливочное масло" {
		t.Fatalf("empty analysis erased known values: %#v", lead)
	}
	if lead.Goal != "привлечение подписчиков и узнаваемость" {
		t.Fatalf("empty analysis erased goal: %q", lead.Goal)
	}

	goal := "рост продаж"
	lead.ApplyAnalysis(CustomerAnalysis{Intent: IntentAnswer, Goal: &goal})
	if lead.Goal != "рост продаж" {
		t.Fatalf("explicit correction did not replace goal: %q", lead.Goal)
	}
	if lead.Niche != "молочная продукция" {
		t.Fatalf("goal update erased niche: %q", lead.Niche)
	}
}

func TestButterPortfolioMatchingSelectsFoodAndWholesaleExamples(t *testing.T) {
	lead := LeadState{
		Niche:            "молочная продукция",
		ProductOrService: "сливочное масло",
		Goal:             "привлечение подписчиков и узнаваемость",
	}
	analysis := CustomerAnalysis{PortfolioTags: []string{"сливочное масло", "молочная продукция", "продукты питания", "fmcg", "оптовые продажи"}}

	selection := selectAIWorkExamples(lead, analysis, 3)
	if len(selection.Videos) == 0 {
		t.Fatalf("butter niche selected no portfolio videos, tags=%#v", selection.Tags)
	}
	if len(selection.Videos) > 3 {
		t.Fatalf("selection exceeded limit: %d", len(selection.Videos))
	}
	for _, video := range selection.Videos {
		if video.Category != "horeca" && video.Category != "b2b" && video.Category != "retail" {
			t.Fatalf("unrelated category selected for butter: %q (%q)", video.Category, video.FileName)
		}
	}
	seen := map[string]bool{}
	for _, video := range selection.Videos {
		if seen[video.Path] {
			t.Fatalf("duplicate portfolio item selected: %q", video.Path)
		}
		seen[video.Path] = true
	}
}

func TestUnrelatedNicheSelectsNoPortfolioVideos(t *testing.T) {
	lead := LeadState{Niche: "юридические услуги", Goal: "получать заявки"}
	selection := selectAIWorkExamples(lead, CustomerAnalysis{}, 3)
	if len(selection.Videos) != 0 {
		t.Fatalf("unrelated niche matched videos: %#v", selection.Videos)
	}
}

func TestHasUnsentAIWorkVideosExcludesAlreadySentItems(t *testing.T) {
	lead := LeadState{Niche: "туризм"}
	selection := selectAIWorkExamples(lead, CustomerAnalysis{PortfolioTags: []string{"tourism"}}, 2)
	if len(selection.Videos) == 0 {
		t.Fatal("tourism selection is empty")
	}
	conversation := Conversation{SentVideoFiles: map[string]time.Time{}}
	if !hasUnsentAIWorkVideos(selection, conversation) {
		t.Fatal("fresh conversation must have unsent videos")
	}
	for _, video := range selection.Videos {
		conversation.SentVideoFiles[video.Path] = time.Now().UTC()
	}
	if hasUnsentAIWorkVideos(selection, conversation) {
		t.Fatal("already sent selection reported as unsent")
	}
}
