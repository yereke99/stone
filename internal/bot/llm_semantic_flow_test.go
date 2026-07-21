package bot

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yereke99/stone/internal/openai"
)

type capturingDecisionAI struct {
	response     openai.CustomerUnderstanding
	systemPrompt string
	messages     []openai.Message
	calls        int
}

func (ai *capturingDecisionAI) AnalyzeCustomerMessage(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.CustomerUnderstanding, error) {
	ai.calls++
	ai.systemPrompt = systemPrompt
	ai.messages = append([]openai.Message(nil), messages...)
	return ai.response, nil
}

func (ai *capturingDecisionAI) GenerateReplyText(ctx context.Context, systemPrompt string, messages []openai.Message) (openai.ReplyTextResponse, error) {
	return openai.ReplyTextResponse{ReplyText: ""}, nil
}

type failingAdminSender struct {
	fakeSender
	failChatID string
	err        error
}

func (s *failingAdminSender) SendMessage(ctx context.Context, chatID string, message string) error {
	if chatID == s.failChatID {
		if s.err != nil {
			return s.err
		}
		return errors.New("admin send failed")
	}
	return s.fakeSender.SendMessage(ctx, chatID, message)
}

func semanticDecision(reply string) openai.CustomerUnderstanding {
	return openai.CustomerUnderstanding{
		Language:            "ru",
		Intent:              "qualification_answer",
		MessageMeaning:      "semantic test decision",
		ShouldUpdateState:   true,
		RecommendedAction:   "send_text",
		NextAction:          "send_text",
		CustomerReply:       reply,
		ReplyText:           reply,
		Confidence:          0.95,
		MissingFields:       []string{},
		PortfolioTags:       []string{},
		PortfolioSearchTags: []string{},
		UnresolvedQuestions: []string{},
	}
}

func TestProductionFlowSendsLatestTenRoleOrderedMessagesToLLM(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	response := semanticDecision("Келесі аптаға белгілеп қойдым.")
	response.Language = "kk"
	response.LeadUpdates.Deadline = testString("келесі апта")
	response.ExtractedFields.Deadline = testString("келесі апта")
	ai := &capturingDecisionAI{response: response}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "chat-history-context"

	base := time.Now().Add(-20 * time.Minute)
	for i := 0; i < 6; i++ {
		store.Update(chatID, func(conversation *Conversation) {
			conversation.Messages = append(conversation.Messages,
				ChatMessage{Role: "user", Content: "client " + strconvItoa(i), CreatedAt: base.Add(time.Duration(i*2) * time.Minute)},
				ChatMessage{Role: "assistant", Content: "bot " + strconvItoa(i), CreatedAt: base.Add(time.Duration(i*2+1) * time.Minute)},
			)
		})
	}

	sendText(t, service, chatID, "Келесі аптаға.")

	if ai.calls != 1 {
		t.Fatalf("AI calls/messages = %d/%d", ai.calls, len(ai.messages))
	}
	if len(ai.messages) != 10 {
		t.Fatalf("messages = %d, want 10: %#v", len(ai.messages), ai.messages)
	}
	if ai.messages[0].Role != "assistant" || ai.messages[0].Content != "bot 1" {
		t.Fatalf("first message = %#v, want bot 1 assistant message", ai.messages[0])
	}
	if ai.messages[9].Role != "user" || ai.messages[9].Content != "Келесі аптаға." {
		t.Fatalf("last message = %#v, want current user message", ai.messages[9])
	}
	for i, message := range ai.messages {
		if strings.TrimSpace(message.Content) == "" {
			t.Fatalf("message %d has empty content", i)
		}
		if message.Role != "user" && message.Role != "assistant" {
			t.Fatalf("message %d role = %q, want user/assistant", i, message.Role)
		}
	}
	if !strings.Contains(ai.systemPrompt, "Dynamic backend context JSON:") {
		t.Fatal("dynamic backend context was not included in the system prompt")
	}
	if strings.Contains(ai.systemPrompt, `"text":"Келесі аптаға."`) {
		t.Fatal("latest customer text was duplicated into dynamic backend context")
	}
	if !strings.Contains(ai.systemPrompt, `"known_state"`) {
		t.Fatal("known_state was not included in dynamic backend context")
	}
}

func TestLLMDecisionUpdatesManyLeadFieldsInOneProductionMessage(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	reply := "Понял: сеть кофеен в Алматы, вертикальный ролик для Instagram, цель — увеличить доставку."
	response := semanticDecision(reply)
	response.LeadUpdates.BusinessNiche = testString("сеть кофеен")
	response.LeadUpdates.GeographicMarket = testString("Алматы")
	response.LeadUpdates.ProductOrService = testString("кофе и доставка")
	response.LeadUpdates.AdvertisingGoal = testString("увеличить заказы на доставку")
	response.LeadUpdates.DesiredVideoFormat = testString("короткий вертикальный")
	response.LeadUpdates.DistributionPlatform = testString("Instagram")
	response.LeadUpdates.Deadline = testString("следующая неделя")
	response.LeadUpdates.Budget = testString("около 150 000 тенге")
	response.LeadUpdates.NumberOfVideos = testString("1 ролик")
	response.ExtractedFields.Niche = testString("сеть кофеен")
	response.ExtractedFields.ProductOrService = testString("кофе и доставка")
	response.ExtractedFields.Goal = testString("увеличить заказы на доставку")
	response.ExtractedFields.Platform = testString("Instagram")
	response.ExtractedFields.Deadline = testString("следующая неделя")
	response.ExtractedFields.Budget = testString("около 150 000 тенге")
	response.ExtractedFields.VideoQuantity = testString("1 ролик")
	response.ConfirmedFields = []string{"niche", "product_or_service", "goal", "distribution_platform", "deadline", "budget", "video_quantity", "desired_video_format"}
	ai := &capturingDecisionAI{response: response}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)

	sendText(t, service, "chat-multi-field", "У нас сеть кофеен в Алматы. Хотим короткий вертикальный ролик для Instagram, чтобы увеличить доставку. Желательно запустить на следующей неделе. Бюджет около 150 000 тенге.")

	conversation := snapshotConversation(t, store, "chat-multi-field")
	lead := conversation.Lead
	if lead.Niche != "сеть кофеен" || lead.City != "Алматы" || lead.ProductOrService != "кофе и доставка" ||
		lead.Goal != "увеличить заказы на доставку" || lead.DesiredVideoFormat != "короткий вертикальный" ||
		lead.DistributionPlatform != "Instagram" || lead.Deadline != "следующая неделя" ||
		lead.Budget != "около 150 000 тенге" || lead.VideoQuantity != "1" {
		t.Fatalf("lead fields not updated together: %#v", lead)
	}
	if got := sender.messages[len(sender.messages)-1]; got != reply || strings.Contains(strings.ToLower(got), "какая у вас ниша") {
		t.Fatalf("customer reply not LLM/non-repeating: %q", got)
	}
}

func TestServiceAndCostQuestionAnswersAndSendsApprovedPortfolioVideos(t *testing.T) {
	sender := &fakeSender{fileMessageIDs: []string{"test-video-id", "basic-video-id", "standard-video-id"}}
	store := NewConversationStore()
	reply := "Здравствуйте! Stone Production делает AI-рекламные ролики без съёмки: сценарий, AI-визуал, монтаж, озвучка и подготовка под рекламу. Стоимость: Test — 35 000 тг, Basic — 50 000 тг, Standard — от 75 000 тг. Сейчас отправлю три примера. Подскажите, какая у вас ниша?"
	response := semanticDecision(reply)
	response.Intent = "price_question"
	response.RecommendedAction = "send_price_options"
	response.NextAction = "send_relevant_examples"
	response.ShouldSendPortfolio = true
	response.PortfolioTags = []string{}
	response.PortfolioSearchTags = []string{}
	ai := &capturingDecisionAI{response: response}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil, "77019519013@c.us")
	chatID := "chat-services-cost"

	sendText(t, service, chatID, "Здравствуйте! Какие услуги предлагаете и стоимость?")

	clientReplies := messagesToChat(sender, chatID)
	if len(clientReplies) != 1 {
		t.Fatalf("client replies = %#v, want one text reply", clientReplies)
	}
	lower := strings.ToLower(clientReplies[0])
	for _, forbidden := range []string{"сообщение получили", "вернёмся с ответом", "вернемся с ответом", "менеджер продолжит", "не понимаю"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("reply used prohibited fallback %q: %q", forbidden, clientReplies[0])
		}
	}
	for _, want := range []string{"ai-рекламные ролики", "35 000", "50 000", "75 000"} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			t.Fatalf("reply missing %q: %q", want, clientReplies[0])
		}
	}
	if countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp") != 0 {
		t.Fatalf("generic services/price question escalated unexpectedly: %#v", sender.messages)
	}
	if len(sender.files) != 3 {
		t.Fatalf("sent files = %#v, want three approved portfolio videos", sender.files)
	}
	for i, want := range []string{VideoLevel1, VideoLevel2, VideoLevel3} {
		if got := filepath.Base(sender.files[i]); got != want {
			t.Fatalf("file %d = %q, want %q", i, got, want)
		}
	}
	conversation := snapshotConversation(t, store, chatID)
	if !conversation.PackagesSent || !conversation.SentPortfolio {
		t.Fatalf("package portfolio state not persisted: packages=%v portfolio=%v", conversation.PackagesSent, conversation.SentPortfolio)
	}
}

func TestLLMDecisionDoesNotStoreBotPriceAsClientBudget(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	ai := &capturingDecisionAI{response: semanticDecision("Хорошо, если удобно, напишите продукт и задачу ролика.")}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil)
	chatID := "chat-price-not-budget"
	store.Update(chatID, func(conversation *Conversation) {
		conversation.Messages = append(conversation.Messages, ChatMessage{Role: "assistant", Content: "Стоимость начинается от 35 000 тенге."})
		conversation.LastReplyText = "Стоимость начинается от 35 000 тенге."
	})

	sendText(t, service, chatID, "Хорошо.")

	if budget := snapshotConversation(t, store, chatID).Lead.Budget; budget != "" {
		t.Fatalf("bot price was stored as client budget: %q", budget)
	}
}

func TestLLMDecisionSeparatesContactFromCompanyAndABATSURegression(t *testing.T) {
	sender := &fakeSender{}
	store := NewConversationStore()
	reply := "ABAT SU бойынша ақпаратты алдым. Ерекшеліктерді сақтадым, менеджерге қысқаша жеткіземін."
	response := semanticDecision(reply)
	response.Language = "kk"
	response.LeadUpdates.CompanyName = testString("ABAT SU")
	response.LeadUpdates.BrandName = testString("ABAT SU")
	response.LeadUpdates.BusinessNiche = testString("filtered bottled drinking water production and sales")
	response.LeadUpdates.ProductOrService = testString("filtered bottled drinking water")
	response.LeadUpdates.ProductFeatures = []string{"multi-stage filtration system", "produced in accordance with sanitary requirements"}
	response.LeadUpdates.AdvertisingGoal = nil
	response.LeadUpdates.Deadline = nil
	response.LeadUpdates.QuestionnaireStatus = testString("completed")
	response.LeadUpdates.ReadinessForManagerHandoff = true
	response.LeadUpdates.RecommendedNextStep = testString("Manager should contact the client and recommend a format.")
	response.ExtractedFields.Niche = testString("filtered bottled drinking water production and sales")
	response.ExtractedFields.ProductOrService = testString("filtered bottled drinking water")
	response.ReadyForManager = true
	response.RecommendedAction = "handoff"
	response.NextAction = "handoff"
	response.QuestionnaireStatus = "completed"
	response.ManagerSummary = "Company ABAT SU produces and sells filtered bottled drinking water. Product features: multi-stage filtration and sanitary compliance. Advertising goal and deadline are not specified."
	response.UnresolvedQuestions = []string{"advertising goal", "deadline"}
	ai := &capturingDecisionAI{response: response}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil, "77019519013@c.us")

	if err := service.HandleIncomingMessage(context.Background(), IncomingMessage{
		ChatID:     "chat-abat-su",
		SenderName: "Ulan U.D.",
		Text:       "Біз – ABAT SU. Көпсатылы сүзгілеу жүйесі. Санитарлық талаптарға сәйкес дайындалады.",
	}); err != nil {
		t.Fatalf("HandleIncomingMessage() error = %v", err)
	}

	conversation := snapshotConversation(t, store, "chat-abat-su")
	lead := conversation.Lead
	if lead.ContactName != "Ulan U.D." || lead.CompanyName != "ABAT SU" || lead.BrandName != "ABAT SU" {
		t.Fatalf("contact/company split failed: %#v", lead)
	}
	if lead.Goal != "" || lead.Deadline != "" {
		t.Fatalf("goal/deadline should remain unknown: %#v", lead)
	}
	if len(lead.ProductFeatures) != 2 {
		t.Fatalf("product features not stored: %#v", lead.ProductFeatures)
	}
	adminMessage := firstMessageContaining(sender.messages, "Новый квалифицированный лид WhatsApp")
	if adminMessage == "" {
		t.Fatalf("admin notification was not sent: %#v", sender.messages)
	}
	for _, want := range []string{
		"Компания / бренд: ABAT SU",
		"Ниша: filtered bottled drinking water production and sales",
		"Продукт / услуга: filtered bottled drinking water",
		"Цель: не указано",
		"Срок: не указано",
		"Особенности продукта: multi-stage filtration system; produced in accordance with sanitary requirements",
		"Нерешённые вопросы: advertising goal; deadline",
	} {
		if !strings.Contains(adminMessage, want) {
			t.Fatalf("admin message missing %q:\n%s", want, adminMessage)
		}
	}
	if strings.Contains(adminMessage, "questionnaire_confirmation") {
		t.Fatalf("questionnaire state contradiction in admin message:\n%s", adminMessage)
	}
}

func TestLLMHandoffNotificationFailureDoesNotClaimTransfer(t *testing.T) {
	adminChatID := "77019519013@c.us"
	sender := &failingAdminSender{failChatID: adminChatID, err: errors.New("green api unavailable")}
	store := NewConversationStore()
	reply := "Данные получил, передаю менеджеру. Он продолжит по стоимости."
	response := semanticDecision(reply)
	response.Intent = "human_request"
	response.ReadyForManager = true
	response.NeedsHuman = true
	response.RecommendedAction = "handoff"
	response.NextAction = "handoff"
	response.CustomerReply = reply
	response.ReplyText = reply
	response.ClientIntent = "просит менеджера"
	response.ManagerSummary = "Клиент просит точный расчёт и менеджера."
	response.LeadUpdates.ReadinessForManagerHandoff = true
	response.LeadUpdates.BusinessNiche = testString("салон красоты")
	response.LeadUpdates.AdvertisingGoal = testString("получать заявки")
	response.LeadUpdates.SelectedPackage = testString("standard")
	response.ExtractedFields.Niche = testString("салон красоты")
	response.ExtractedFields.Goal = testString("получать заявки")
	response.ExtractedFields.PackageInterest = testString("standard")
	ai := &capturingDecisionAI{response: response}
	service := NewService(sender, ai, store, testVideoDir(t), PortfolioLinks{}, "auto", nil, adminChatID)
	chatID := "chat-handoff-failure"

	sendText(t, service, chatID, "Нужен точный расчет, передайте менеджеру")

	clientReplies := messagesToChat(&sender.fakeSender, chatID)
	if len(clientReplies) != 1 {
		t.Fatalf("client replies = %#v, want one safe fallback", clientReplies)
	}
	if clientReplies[0] != ManagerEscalationFallbackText("ru") {
		t.Fatalf("client reply = %q, want safe fallback", clientReplies[0])
	}
	if strings.Contains(strings.ToLower(clientReplies[0]), "передаю менеджеру") {
		t.Fatalf("fallback falsely claimed transfer: %q", clientReplies[0])
	}
	conversation := snapshotConversation(t, store, chatID)
	if !conversation.AdminNotifiedAt.IsZero() || conversation.HandedOffToOwner || conversation.AutomationClosed || conversation.Stopped {
		t.Fatalf("failed escalation marked as transferred: admin=%v handed=%v closed=%v stopped=%v", conversation.AdminNotifiedAt, conversation.HandedOffToOwner, conversation.AutomationClosed, conversation.Stopped)
	}
	if countMessagesContaining(sender.messages, "Новый квалифицированный лид WhatsApp") != 0 {
		t.Fatalf("failed admin notification was recorded as sent: %#v", sender.messages)
	}
}

func strconvItoa(value int) string {
	return string(rune('0' + value))
}
